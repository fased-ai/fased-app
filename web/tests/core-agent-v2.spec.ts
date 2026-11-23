import { expect, test } from "@playwright/test";
import {
  closeNewsSources,
  installCoreFixture,
  openNewsSources,
  openProviders,
  revealControl,
  start,
  stop,
  url,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("T3Code streaming chat, Tier-1 tool execution, inline swap approval cards, and FTS5 memory work end-to-end", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(75000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.goto(url + "/#agents/chat");
  await expect(
    page.getByRole("heading", { name: "Chat", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("dialog", { name: "Allow replies", exact: true }),
  ).toHaveCount(0);

  const composer = page.getByLabel("Message", { exact: true });
  await composer.fill("Check my wallet list");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  // Tier-1 Wallets tool runs automatically and streamed reply renders without modal friction.
  const toolSummary = page.locator("details.v2-tool-call summary").first();
  await expect(toolSummary).toContainText("Wallets", { timeout: 12000 });
  await expect(toolSummary).toContainText("Done", { timeout: 12000 });
  await toolSummary.click();
  await expect(page.locator("details.v2-tool-call pre").first()).toContainText(
    "signingEnabled",
  );
  await expect(page.locator(".v2-markdown").first()).toContainText(
    "Fixture agent reviewed",
  );

  // Tier-2 swap proposal pauses on an inline review-only approval card.
  await composer.fill("Propose a small swap");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const swapCard = page.getByRole("article", {
    name: "Swap draft: 10 USDC -> SOL",
    exact: true,
  });
  await expect(swapCard).toBeVisible({ timeout: 12000 });
  await expect(swapCard).toContainText("Review-only · No signing");
  await expect(
    swapCard.getByRole("button", { name: "Approve", exact: true }),
  ).toBeVisible();
  await expect(
    swapCard.getByRole("button", { name: "Reject", exact: true }),
  ).toBeVisible();

  if (!isMobile) {
    await expect(page.getByLabel("Pending approval").first()).toBeVisible();
  }

  // Approve -> flips card status, resumes agent, and saves draft to FTS5 Memory.
  await swapCard.getByRole("button", { name: "Approve", exact: true }).click();
  await expect(swapCard).toContainText("Approved", { timeout: 12000 });
  await expect(page.locator(".v2-markdown").last()).toContainText(
    "Fixture agent reviewed draft_swap (completed)",
    { timeout: 12000 },
  );

  // Verify persistence across page reload.
  await page.reload();
  await expect(
    page.getByRole("article", {
      name: "Swap draft: 10 USDC -> SOL",
      exact: true,
    }),
  ).toContainText("Approved", { timeout: 12000 });

  // Open Memory tab and search FTS5 workspace index for saved Swap draft.
  await page.goto(url + "/#agents/memory");
  const workspaceIndex = page.getByRole("region", {
    name: "Workspace index",
    exact: true,
  });
  await expect(workspaceIndex).toContainText("Swap draft: 10 USDC -> SOL");
  await workspaceIndex.getByLabel("Search saved drafts").fill("USDC");
  await workspaceIndex
    .getByRole("button", { name: "Search", exact: true })
    .click();
  await expect(workspaceIndex).toContainText("swap_draft");

  // Return to Chat, start a fresh thread, and exercise Reject path + delete thread.
  await page.goto(url + "/#agents/chat");
  if (isMobile) {
    await page
      .getByRole("button", { name: "Conversations", exact: true })
      .click();
  }
  await page.getByRole("button", { name: "New thread", exact: true }).click();
  await composer.fill("Propose a small swap");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const rejectCard = page.getByRole("article", {
    name: "Swap draft: 10 USDC -> SOL",
    exact: true,
  });
  await expect(rejectCard).toBeVisible({ timeout: 12000 });
  await rejectCard.getByRole("button", { name: "Reject", exact: true }).click();
  await expect(rejectCard).toContainText("Rejected", { timeout: 12000 });
  await expect(page.locator(".v2-markdown").last()).toContainText("rejected", {
    timeout: 12000,
  });

  // Delete active thread and confirm it disappears from rail.
  await page
    .getByRole("button", { name: "Delete conversation", exact: true })
    .click();
  if (isMobile) {
    await page
      .getByRole("button", { name: "Conversations", exact: true })
      .click();
  }
  await expect(page.locator(".v2-thread-row")).toHaveCount(1);
});

test("Tier-1 market price and swap quote read tools stream inline summaries without live calls", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  const external: string[] = [];
  const allowedHost = new URL(url).hostname;
  page.on("request", (req) => {
    const host = new URL(req.url()).hostname;
    if (host !== allowedHost) external.push(req.url());
  });

  await page.goto(url + "/#agents/chat");
  const composer = page.getByLabel("Message", { exact: true });

  await composer.fill("Check SOL price");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const priceRow = page.locator("details.v2-tool-call summary").first();
  await expect(priceRow).toContainText("Token price", { timeout: 12000 });
  await expect(priceRow).toContainText("SOL $70.00", { timeout: 12000 });
  await expect(priceRow).toContainText("Done", { timeout: 12000 });

  await composer.fill("Get a quote for 1 SOL to USDC");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const quoteRow = page.locator("details.v2-tool-call summary").last();
  await expect(quoteRow).toContainText("Swap quote", { timeout: 12000 });
  await expect(quoteRow).toContainText("1 SOL -> 70 USDC", { timeout: 12000 });
  await expect(quoteRow).toContainText("Done", { timeout: 12000 });
  expect(external).toEqual([]);
});

test("Chat task_draft approval surfaces in Tasks panel and launches back in Chat", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.goto(url + "/#agents/chat");
  const composer = page.getByLabel("Message", { exact: true });
  await composer.fill("Draft a task to monitor SOL volatility");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const taskCard = page.getByRole("article", {
    name: "Draft task: SOL Volatility Watch",
    exact: true,
  });
  await expect(taskCard).toBeVisible({ timeout: 12000 });
  await taskCard.getByRole("button", { name: "Approve", exact: true }).click();
  await expect(taskCard).toContainText("Approved", { timeout: 12000 });

  await page.goto(url + "/#agents/tasks");
  const draftsPanel = page.getByRole("region", {
    name: "Agent task drafts",
    exact: true,
  });
  await expect(draftsPanel).toBeVisible({ timeout: 12000 });
  const draftItem = draftsPanel.getByRole("article", {
    name: "SOL Volatility Watch",
    exact: true,
  });
  await expect(draftItem).toContainText("5 minutes");

  await draftItem
    .getByRole("button", { name: "Schedule", exact: true })
    .click();
  const taskComposer = page.getByRole("complementary", {
    name: "Create a task",
    exact: true,
  });
  await expect(taskComposer).toBeVisible();
  await expect(taskComposer.getByLabel("Task name")).toHaveValue(
    "SOL Volatility Watch",
  );
  await taskComposer
    .getByRole("button", { name: "Cancel task", exact: true })
    .click();

  await draftItem
    .getByRole("button", { name: "Run in Chat", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/, { timeout: 12000 });
  await expect(
    page.getByRole("heading", { name: "SOL Volatility Watch" }),
  ).toBeVisible({
    timeout: 12000,
  });
  await expect(page.locator(".v2-markdown").last()).toContainText(
    "Fixture agent reviewed",
    { timeout: 12000 },
  );
});

test("Non-OpenAI provider surfaces its own model in Chat composer without OpenAI fallback", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.goto(url + "/#agents/chat");
  await openProviders(page);

  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers.locator('[data-provider="openrouter"] > button').click();
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-openrouter-disposable-ui");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-v2-router-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect(
    providers.getByRole("region", { name: "Models", exact: true }),
  ).toContainText("Gemini 3.8 Flash", { timeout: 12000 });

  await providers.getByRole("button", { name: "Back", exact: true }).click();

  const composerForm = page.locator("form.v2-composer");
  const replyModel = composerForm.getByLabel("Reply model", { exact: true });
  await expect(replyModel).toContainText("google/gemini-3.8-flash");
  await expect(replyModel).not.toContainText("openai");

  await replyModel.click();
  await expect(composerForm.getByLabel("Model", { exact: true })).toHaveValue(
    "google/gemini-3.8-flash",
  );
});

test("Agent Settings Rules & Memory CRUD, Enter-to-reject feedback, and 5s Archive/Delete Undo work end-to-end", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(75000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.goto(url + "/#agents/settings");
  const rulesPanel = page.getByRole("region", {
    name: "Rules & Memory",
    exact: true,
  });
  await expect(rulesPanel).toBeVisible({ timeout: 12000 });
  await rulesPanel
    .getByRole("button", { name: "Add rule", exact: true })
    .click();
  await rulesPanel.getByLabel("Title", { exact: true }).fill("SOL Risk Cap");
  await rulesPanel
    .getByLabel("Rule or fact", { exact: true })
    .fill("Never risk more than 2% per SOL trade.");
  await rulesPanel.getByRole("button", { name: "Save", exact: true }).click();
  await expect(rulesPanel).toContainText("SOL Risk Cap");
  await expect(rulesPanel).toContainText("Pinned");

  await page.reload();
  const reloadedRules = page.getByRole("region", {
    name: "Rules & Memory",
    exact: true,
  });
  await expect(reloadedRules).toContainText(
    "Never risk more than 2% per SOL trade.",
    { timeout: 12000 },
  );
  await reloadedRules
    .getByRole("button", { name: "Unpin SOL Risk Cap", exact: true })
    .click();
  await expect(
    reloadedRules.getByRole("button", {
      name: "Pin SOL Risk Cap",
      exact: true,
    }),
  ).toBeVisible();
  await reloadedRules
    .getByRole("button", { name: "Edit SOL Risk Cap", exact: true })
    .click();
  await reloadedRules
    .getByLabel("Rule or fact", { exact: true })
    .fill("Cap single swap allocation at 1% equity.");
  await reloadedRules
    .getByRole("button", { name: "Save", exact: true })
    .click();
  await expect(reloadedRules).toContainText(
    "Cap single swap allocation at 1% equity.",
  );
  await reloadedRules
    .getByRole("button", { name: "Delete SOL Risk Cap", exact: true })
    .click();
  await expect(reloadedRules).toContainText("No rules saved");

  await page.goto(url + "/#agents/chat");
  const composer = page.getByLabel("Message", { exact: true });
  await composer.fill("Propose a small swap");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const swapCard = page.getByRole("article", {
    name: "Swap draft: 10 USDC -> SOL",
    exact: true,
  });
  await expect(swapCard).toBeVisible({ timeout: 12000 });

  const noteInput = swapCard.getByLabel("Approval note", { exact: true });
  await noteInput.fill("Split into two tranches");
  await noteInput.press("Enter");
  await expect(swapCard).toContainText("Rejected", { timeout: 12000 });
  await expect(swapCard).toContainText("Split into two tranches");
  await expect(page.locator(".v2-markdown").last()).toContainText("rejected", {
    timeout: 12000,
  });

  await page
    .getByRole("button", { name: "Archive conversation", exact: true })
    .click();
  const undoToast = page.locator(".v2-undo-toast");
  await expect(undoToast).toContainText("Conversation archived");
  await undoToast.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Archive conversation", exact: true }),
  ).toBeVisible({ timeout: 12000 });

  await page
    .getByRole("button", { name: "Delete conversation", exact: true })
    .click();
  await expect(undoToast).toContainText("Conversation deleted");
  if (isMobile) {
    await page
      .getByRole("button", { name: "Conversations", exact: true })
      .click();
  }
  await expect(page.locator(".v2-thread-row")).toHaveCount(0);
  await undoToast.getByRole("button", { name: "Undo", exact: true }).click();
  await expect(page.locator(".v2-thread-row")).toHaveCount(1, {
    timeout: 12000,
  });
});

test("Scheduled reminder runs mirror into Agent V2 threads and News Ask in Chat launches V2 chat", async ({
  page,
}) => {
  test.setTimeout(75000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await page.getByLabel("Task name", { exact: true }).fill("Morning SOL Check");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Check SOL/USDC spread and funding");
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  await page.getByRole("button", { name: "Create task", exact: true }).click();

  const runBtn = page.getByRole("button", {
    name: "Run now: Morning SOL Check",
    exact: true,
  });
  await expect(runBtn).toBeVisible({ timeout: 12000 });
  await runBtn.click();

  const openThreadBtn = page.getByRole("button", {
    name: "Open thread",
    exact: true,
  });
  await expect(openThreadBtn).toBeVisible({ timeout: 12000 });
  await openThreadBtn.click();

  await expect(page).toHaveURL(/#agents\/chat$/, { timeout: 12000 });
  await expect(
    page.getByRole("heading", { name: "Morning SOL Check", exact: true }),
  ).toBeVisible({ timeout: 12000 });
  await expect(page.locator(".v2-thread-source-pill")).toHaveText("Task");
  await expect(page.locator(".v2-markdown").first()).toContainText(
    "Check SOL/USDC spread and funding",
    { timeout: 12000 },
  );
  await expect(page.locator(".v2-markdown").first()).toContainText("reminded");

  // Reload to confirm mirrored task thread persists in agents.sqlite.
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Morning SOL Check", exact: true }),
  ).toBeVisible({ timeout: 12000 });

  // NewsInputs -> Ask in Chat launches a fresh V2 chat thread with the headline.
  await page.goto(url + "/#agents/news");
  const news = page.getByRole("region", { name: "News", exact: true });
  await openNewsSources(page, true);
  await revealControl(page, news.getByLabel("Name", { exact: true }), "");
  await news.getByLabel("Name", { exact: true }).fill("Fixture SOL Feed");
  await revealControl(page, news.getByLabel("Feed URL", { exact: true }), "");
  await news
    .getByLabel("Feed URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml");
  await revealControl(
    page,
    news.getByLabel("Freshness (minutes)", { exact: true }),
    "",
  );
  await news.getByLabel("Freshness (minutes)", { exact: true }).fill("10");
  await news.getByRole("button", { name: "Add", exact: true }).click();

  const source = news.getByRole("group", {
    name: "Source Fixture SOL Feed",
    exact: true,
  });
  await expect(source).toBeVisible({ timeout: 12000 });
  await source.getByRole("button", { name: "Fetch", exact: true }).click();
  await closeNewsSources(page);
  await expect(async () => {
    await news.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect(news.locator(".news-headline").first()).toBeVisible({
      timeout: 1500,
    });
  }).toPass({ timeout: 15000 });

  const headline = news.locator(".news-headline").first();
  await headline
    .getByRole("button", { name: "Ask in Chat", exact: true })
    .click();

  await expect(page).toHaveURL(/#agents\/chat$/, { timeout: 12000 });
  await expect(page.locator(".v2-user-bubble").last()).toContainText(
    "untrusted external data",
    { timeout: 12000 },
  );
  await expect(page.locator(".v2-markdown").last()).toContainText(
    "Fixture response ready",
    { timeout: 12000 },
  );
});

test("T3Code slash /side, manual /compact persistence across reload, and Settings Usage & Cost", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.goto(url + "/#agents/chat");
  await expect(
    page.getByRole("heading", { name: "Chat", exact: true }),
  ).toBeVisible();

  const composer = page.getByLabel("Message", { exact: true });
  await composer.fill("Hello fixture thread");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.locator(".v2-markdown").first()).toContainText(
    "Fixture response ready",
    { timeout: 12000 },
  );
  await expect(page.locator(".v2-message")).toHaveCount(2);

  // Ephemeral /side renders an above-composer card without mutating thread messages or summary.
  await composer.fill("/side Quick fee check");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const sideCard = page.getByRole("complementary", {
    name: "Side question",
    exact: true,
  });
  await expect(sideCard).toBeVisible({ timeout: 12000 });
  await expect(sideCard).toContainText("Quick fee check");
  await expect(sideCard).toContainText("Fixture response ready", {
    timeout: 12000,
  });
  await expect(page.locator(".v2-message")).toHaveCount(2);

  await sideCard.getByRole("button", { name: "Dismiss", exact: true }).click();
  await expect(sideCard).toHaveCount(0);

  // /compact persists thread summary in sqlite and keeps it visible after reload.
  await composer.fill("/compact");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const summaryBanner = page.locator("details.v2-summary-banner");
  await expect(summaryBanner).toBeVisible({ timeout: 12000 });
  await expect(summaryBanner.locator("summary")).toHaveText(
    "Compacted context",
  );
  await summaryBanner.locator("summary").click();
  await expect(summaryBanner.locator("pre")).toContainText(
    "Hello fixture thread",
  );
  await expect(summaryBanner.locator("pre")).not.toContainText(
    "Quick fee check",
  );

  await page.reload();
  await expect(summaryBanner).toBeVisible({ timeout: 12000 });
  await summaryBanner.locator("summary").click();
  await expect(summaryBanner.locator("pre")).toContainText(
    "Hello fixture thread",
  );

  // Agent settings Usage & Cost aggregates stored token telemetry and switches windows cleanly.
  await page.goto(url + "/#agents/settings");
  const usagePanel = page.getByRole("region", {
    name: "Usage & Cost",
    exact: true,
  });
  await expect(usagePanel).toBeVisible({ timeout: 12000 });
  await expect(usagePanel).toContainText("fixture-v2", { timeout: 12000 });
  await usagePanel.getByRole("button", { name: "7d", exact: true }).click();
  await expect(
    usagePanel.getByRole("button", { name: "7d", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await usagePanel
    .getByRole("button", { name: "All time", exact: true })
    .click();
  await expect(
    usagePanel.getByRole("button", { name: "All time", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(usagePanel).toContainText("fixture-v2");
});

test("T3Code web_search citations, multi-agent delegation, and voice input", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.addInitScript(() => {
    class MockSpeechRecognition {
      lang = "en-US";
      continuous = false;
      interimResults = false;
      onresult: ((ev: unknown) => void) | null = null;
      onerror: ((ev: unknown) => void) | null = null;
      onend: (() => void) | null = null;
      start() {
        setTimeout(() => {
          this.onresult?.({
            resultIndex: 0,
            results: [
              {
                isFinal: true,
                length: 1,
                0: { transcript: "Please web search Solana fees" },
              },
            ],
          });
        }, 30);
      }
      stop() {
        this.onend?.();
      }
      abort() {}
    }
    (window as unknown as { SpeechRecognition?: unknown }).SpeechRecognition =
      MockSpeechRecognition;
  });

  await page.goto(url + "/#agents/chat");
  await expect(
    page.getByRole("heading", { name: "Chat", exact: true }),
  ).toBeVisible();

  // Voice input button starts recording, fills transcript into composer, and stops cleanly.
  const voiceBtn = page.getByRole("button", {
    name: "Voice input",
    exact: true,
  });
  await expect(voiceBtn).toBeVisible({ timeout: 12000 });
  await voiceBtn.click();

  const stopVoiceBtn = page.getByRole("button", {
    name: "Stop recording",
    exact: true,
  });
  await expect(stopVoiceBtn).toHaveAttribute("aria-pressed", "true");

  const composer = page.getByLabel("Message", { exact: true });
  await expect(composer).toHaveValue("Please web search Solana fees", {
    timeout: 5000,
  });
  await stopVoiceBtn.click();
  await expect(voiceBtn).toHaveAttribute("aria-pressed", "false");

  // Submitting dictated web_search prompt executes web_search, renders inline citation link & Sources strip.
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const toolSummary = page.locator("details.v2-tool-call summary").first();
  await expect(toolSummary).toContainText("Web search", { timeout: 12000 });
  await expect(toolSummary).toContainText("Done", { timeout: 12000 });

  const inlineLink = page.locator(".v2-markdown a.v2-inline-link").first();
  await expect(inlineLink).toHaveAttribute(
    "href",
    "https://docs.solana.invalid/fees",
  );
  const sources = page.locator('.v2-citations[aria-label="Sources"]').first();
  await expect(sources).toBeVisible({ timeout: 12000 });
  await expect(sources).toContainText("docs.solana.invalid");

  // Multi-agent delegation invokes delegate_to_agent -> Trading Analyst specialist synthesis.
  await composer.fill("Delegate analysis to trading analyst");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const delegateRow = page
    .locator("details.v2-tool-call")
    .filter({ hasText: "Delegate" })
    .first();
  await expect(delegateRow.locator("summary")).toContainText(
    "Trading Analyst",
    {
      timeout: 12000,
    },
  );
  await delegateRow.locator("summary").click();
  await expect(delegateRow.locator(".v2-delegate-summary")).toContainText(
    "Delegated to Trading Analyst",
  );
  await expect(page.locator(".v2-markdown").last()).toContainText(
    "Specialist analysis: SOL volatility remains moderate",
    { timeout: 12000 },
  );
});

test("Phase 8B-3 in-thread secret request cards, sandboxed visual replies, and Settings Diagnostics MCP/manifest controls work end-to-end", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(75000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  await page.goto(url + "/#agents/chat");
  await expect(
    page.getByRole("heading", { name: "Chat", exact: true }),
  ).toBeVisible();

  // 1. In-thread Secret Request Card -> encrypted vault save -> zero secret leak on reload.
  const composer = page.getByLabel("Message", { exact: true });
  await composer.fill("Please request secret for Anthropic");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const secretCard = page.getByRole("article", {
    name: "Secret request: Anthropic API key",
    exact: true,
  });
  await expect(secretCard).toBeVisible({ timeout: 12000 });
  await expect(secretCard).toContainText("Secret required");

  const keyInput = secretCard.getByLabel("API key or token", { exact: true });
  await expect(keyInput).toHaveAttribute("type", "password");
  const testSecret = "fased-test-anthropic-secret-card-key-001";
  await keyInput.fill(testSecret);

  const passInput = secretCard.getByLabel("Vault passphrase", { exact: true });
  if (await passInput.isVisible()) {
    await passInput.fill("disposable-browser-preference-vault");
  }

  await secretCard
    .getByRole("button", { name: "Save encrypted", exact: true })
    .click();
  await expect(secretCard).toContainText("Saved encrypted", { timeout: 12000 });
  expect(await page.content()).not.toContain(testSecret);

  await page.reload();
  await expect(
    page.getByRole("article", {
      name: "Secret request: Anthropic API key",
      exact: true,
    }),
  ).toContainText("Saved encrypted", { timeout: 12000 });
  expect(await page.content()).not.toContain(testSecret);

  // 2. Sandboxed Visual Replies (fased-visual + sanitized fased-html).
  await page
    .getByLabel("Message", { exact: true })
    .fill("Please render visual report");
  await page.getByRole("button", { name: "Send", exact: true }).click();

  const metricsCard = page
    .locator(".v2-visual-card")
    .filter({ hasText: "Portfolio Snapshot" })
    .first();
  await expect(metricsCard).toBeVisible({ timeout: 12000 });
  await expect(metricsCard).toContainText("$184.20");
  await expect(metricsCard).toContainText("+4.2%");

  const htmlCard = page
    .locator(".v2-visual-card")
    .filter({ hasText: "Fee Breakdown" })
    .first();
  await expect(htmlCard).toBeVisible({ timeout: 12000 });
  await expect(htmlCard).toContainText("0.0002 SOL");

  expect(
    await page.evaluate(
      () => (window as unknown as { __fasedXss?: number }).__fasedXss,
    ),
  ).toBeUndefined();
  await expect(
    page.locator('.v2-visual-card a[href^="javascript:"]'),
  ).toHaveCount(0);

  await metricsCard
    .getByRole("button", { name: "Source", exact: true })
    .click();
  await expect(metricsCard.locator("pre")).toContainText("Portfolio Snapshot");
  await metricsCard
    .getByRole("button", { name: "Preview", exact: true })
    .click();
  await expect(metricsCard.locator(".v2-visual-metrics")).toBeVisible();

  // 3. Settings / Diagnostics -> MCP endpoint & Model manifest import/clear.
  if (isMobile) {
    await page
      .getByRole("button", { name: "Preferences", exact: true })
      .click();
  } else {
    await page
      .locator("#shell-rail")
      .getByRole("button", { name: "Settings", exact: true })
      .click();
  }
  const dialog = page.getByRole("dialog", { name: /Settings \// });
  await expect(dialog).toBeVisible();

  await dialog
    .locator(".settings-modal-tabs")
    .getByRole("button", { name: "Diagnostics", exact: true })
    .click();

  await expect(dialog).toContainText("MCP endpoint");
  await expect(dialog).toContainText("/api/v1/mcp");
  await expect(dialog).toContainText("Read & draft");
  await expect(dialog).toContainText("Built-in");

  // SSRF guard blocks loopback IP literals.
  const manifestInput = dialog.getByLabel("Manifest URL", { exact: true });
  await manifestInput.fill("https://127.0.0.1/bad.json");
  await dialog.getByRole("button", { name: "Import", exact: true }).click();
  await expect(dialog.getByRole("alert")).toBeVisible({ timeout: 8000 });

  // Valid fixture .invalid URL imports and clears cleanly.
  await manifestInput.fill("https://models.fased.invalid/manifest.json");
  await dialog.getByRole("button", { name: "Import", exact: true }).click();
  await expect(dialog).toContainText("1 models", { timeout: 12000 });
  await expect(dialog).toContainText(
    "https://models.fased.invalid/manifest.json",
  );

  await dialog.getByRole("button", { name: "Clear", exact: true }).click();
  await expect(dialog).toContainText("Built-in", { timeout: 12000 });

  expect(
    await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
  ).toBe(true);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
});

test("Agent V2 chat hides legacy review rail and saves memory notes via Remember this", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);
  await page.goto(url + "/#agents/chat");
  await expect(
    page.getByRole("heading", { name: "Chat", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".v2-legacy-rail")).toHaveCount(0);

  const composer = page.getByLabel("Message", { exact: true });
  await composer.fill("Summarize SOL support levels");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const assistantMsg = page.locator(".v2-message-assistant").last();
  await expect(
    assistantMsg.getByRole("button", { name: "Remember this", exact: true }),
  ).toBeVisible({ timeout: 12000 });
  await assistantMsg
    .getByRole("button", { name: "Remember this", exact: true })
    .click();

  const memoryDialog = page.getByRole("dialog", {
    name: "Memory",
    exact: true,
  });
  await expect(memoryDialog).toBeVisible();
  await memoryDialog
    .getByLabel("Note", { exact: true })
    .fill("Track SOL weekly support levels");
  await memoryDialog
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect(memoryDialog).toHaveCount(0);

  await page.goto(url + "/#agents/memory");
  await expect(
    page.getByText("Track SOL weekly support levels", { exact: true }),
  ).toBeVisible({ timeout: 12000 });
});
