import { expect, test } from "@playwright/test";
import {
  installCoreFixture,
  openProviders,
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
