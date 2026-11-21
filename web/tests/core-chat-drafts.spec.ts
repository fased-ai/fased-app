import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import {
  browserRequest,
  finishNamedConversation,
  installedConversationAccount,
  prepareNamedConversation,
} from "../../scripts/staged-package-browser.mjs";
import { expect, test, type Request, type Route } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { once } from "node:events";
import {
  installCoreFixture,
  captureScreenshot,
  openProviders,
  exactReplyReview,
  openConversationList,
  revealControl,
  exec,
  binary,
  fixturePassword,
  ownedFixtureOrigins,
  dir,
  url,
  start,
  stop,
  privateCLI,
  fixtureProviderSelection,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("normal Chat grants bounded replies, streams Send and retains scoped history without live calls", async ({
  page,
}) => {
  test.setTimeout(75000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#settings");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#settings");
  await openProviders(page);
  const provider = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await expect(provider).toBeVisible();
  await provider.getByRole("button", { name: "API key", exact: true }).click();
  await revealControl(
    page,
    provider.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await provider
    .getByLabel("API key")
    .fill("fased-test-normal-chat-disposable-key");
  await provider
    .getByLabel("Vault passphrase")
    .fill("normal-chat-disposable-passphrase");
  await provider.getByRole("button", { name: "Save", exact: true }).click();
  await expect(provider.getByRole("status")).toContainText("Unlocked");
  const connection = await privateCLI(["ai-connection"]);
  await page.goto(url + "/#agents/chat");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page
    .getByLabel("Conversation name")
    .fill("Normal fixture conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  const chat = page.getByRole("region", { name: "Optional AI request review" });
  const picker = chat.getByRole("combobox", {
    name: "Provider",
    exact: true,
    includeHidden: true,
  });
  await expect(picker).not.toBeVisible();
  await revealControl(page, picker);
  await picker.selectOption(connection.ref);
  await chat
    .getByRole("combobox", { name: "Model", exact: true, includeHidden: true })
    .selectOption("");
  await chat.locator(".reply-model-menu > summary").click();
  await chat
    .getByLabel("Message", { exact: true })
    .fill("Explain my fixture research limits");
  await expect(
    chat.getByRole("button", {
      name: "Save message",
      exact: true,
      includeHidden: true,
    }),
  ).not.toBeVisible();
  const composer = chat.getByLabel("Message", { exact: true });
  await composer.press("Shift+Enter");
  await expect(composer).toHaveValue("Explain my fixture research limits\n");
  await composer.press("Enter");
  const permission = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(permission).toBeVisible();
  await captureScreenshot(page, {
    path: `.evidence/chat-permission-${test.info().project.name}.png`,
    fullPage: false,
  });
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await permission.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(composer).toHaveValue("Explain my fixture research limits\n");
  await composer.press("Enter");
  await permission
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  const card = chat.locator(".ai-request").last();
  await expect(
    chat.getByRole("button", { name: "Stop", exact: true }),
  ).toBeVisible();
  await expect(card.locator(".ai-answer")).not.toBeEmpty();
  let view = await privateCLI(["agents"]);
  const q = view.aiRequests[0];
  expect(q.connectionRef).toBe(connection.ref);
  expect(q.stream).toBe(true);
  expect(q.chatPermissionId).toBeTruthy();
  await expect(card.locator(".ai-answer")).toContainText(
    "Development fixture response",
    { timeout: 12000 },
  );
  await expect(card.locator(".ai-answer")).toBeInViewport();
  await expect(card.getByText("Details", { exact: true })).toBeVisible();
  await expect(
    card.getByRole("button", { name: "Use as draft", exact: true }),
  ).toBeVisible();
  await captureScreenshot(page, {
    path: `.evidence/providers-chat/${test.info().project.name}-chat.png`,
    fullPage: true,
  });
  view = await privateCLI(["agents"]);
  await expect
    .poll(async () => (await privateCLI(["agents"])).aiRequests[0].state)
    .toBe("completed");
  expect(view.providerCallsEnabled).toBe(false);
  await composer.fill("How do these limits apply to the last reply?");
  await chat.getByRole("button", { name: "Send", exact: true }).click();
  await expect(permission).not.toBeVisible();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents"])).aiRequests.filter(
          (q: any) => q.state === "completed",
        ).length,
      { timeout: 12000 },
    )
    .toBe(2);
  view = await privateCLI(["agents"]);
  expect(view.sessions[0].chat.usedRequests).toBe(2);
  expect(view.aiRequests[1].context).toContain(view.aiRequests[0].answer);
  await composer.fill("Stop this reply");
  await chat.getByRole("button", { name: "Send", exact: true }).click();
  await chat.getByRole("button", { name: "Stop", exact: true }).click();
  // The compiled fixture can finish before the browser's Stop reaches the core.
  // A completed response is immutable; cancellation must not erase it. The
  // prepared-Stop case and TestChatStopsDuringActualHTTPSStream separately
  // require cancellation before completion, including retained uncertain holds.
  await expect
    .poll(async () => (await privateCLI(["agents"])).aiRequests.at(-1).state)
    .toMatch(/^(cancelled|completed)$/);
  const stopped = (await privateCLI(["agents"])).aiRequests.at(-1);
  if (stopped.state === "cancelled") expect(stopped.answer || "").toBe("");
  else expect(stopped.answer).toContain("Development fixture response");

  const other = view.profiles.find((p: any) => p.id !== view.profileId).id;
  const isolated = await privateCLI(["agents", "--profile", other]);
  expect(isolated.aiRequests).toHaveLength(0);
  // A saved connected Demo request remains a valid explicit selection.
  expect(q.model).toBe("fixture-openai-v1");
  await chat
    .locator(".ai-request")
    .first()
    .getByRole("button", { name: "Use as draft", exact: true })
    .click();
  await expect(composer).toHaveValue(q.prompt);
  await expect(
    chat.getByRole("button", { name: "Send", exact: true }),
  ).toBeEnabled();
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(3);
  const port = new URL(url).port;
  await stop();
  await start(true, `127.0.0.1:${port}`);
  await page.reload();
  await expect(chat.locator(".ai-answer").first()).toContainText(
    "Development fixture response",
  );
  expect((await privateCLI(["ai-connection"])).locked).toBe(true);
  expect(external).toEqual([]);
});

test("normal Chat keeps a draft when its selected connection locks and stops a prepared reply without fallback", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const c = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    key: "fased-test-lock-disposable-key",
    passphrase: "lock-disposable-passphrase",
  });
  await page.reload();
  await revealControl(
    page,
    page.getByLabel("Conversation name", { exact: true }),
    "Conversation name",
  );
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("New conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  const chat = page.getByRole("region", { name: "Optional AI request review" });
  await revealControl(
    page,
    chat.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await chat
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption(c.ref);
  const input = chat.getByLabel("Message", { exact: true });
  await input.fill("Keep this question when the connection locks");
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "lock",
    ref: c.ref,
  });
  await exactReplyReview(page);
  await expect(
    chat.getByRole("button", { name: "Send", exact: true }),
  ).toBeDisabled();
  await expect(chat.getByRole("status")).toContainText("Provider unavailable");
  await expect(input).toHaveValue(
    "Keep this question when the connection locks",
  );
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await revealControl(
    page,
    chat.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await chat
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption("");
  await exactReplyReview(page);
  await chat.getByRole("button", { name: "Send", exact: true }).click();
  await chat.getByRole("button", { name: "Stop", exact: true }).click();
  await expect(chat.getByText("cancelled", { exact: true })).toBeVisible();
  const view = await privateCLI(["agents"]);
  expect(view.aiRequests[0].state).toBe("cancelled");
  expect(view.aiRequests[0].connectionRef || "").toBe("");
  expect(view.aiRequests[0].spentMicrousd).toBe(0);
});

test("periodic refresh follows the requested profile while a selection is pending", async ({
  page,
}) => {
  test.setTimeout(45000);
  const writes: string[] = [];
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
    if (new URL(r.url()).pathname === "/api/v1/agents" && r.method() !== "GET")
      writes.push(r.method());
  });
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const readAll = () =>
    Promise.all(
      ["wen-operator", "trading-analyst", "news-researcher"].map((id) =>
        privateCLI(["agents", "--profile", id]),
      ),
    );
  const before = await readAll();
  const profile = (name: string) => page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  let releaseSelection = () => {};
  let releasePoll = () => {};
  const selectionGate = new Promise<void>((resolve) => {
    releaseSelection = resolve;
  });
  const pollGate = new Promise<void>((resolve) => {
    releasePoll = resolve;
  });
  let selecting = false;
  let pollRequest: Request | undefined;
  const routeURL = "**/api/v1/agents?**";
  await page.route(routeURL, async (route) => {
    const request = route.request();
    const selected = new URL(request.url()).searchParams.get("profile");
    if (!selecting && selected === "news-researcher") {
      selecting = true;
      await selectionGate;
    } else if (selecting && !pollRequest) {
      pollRequest = request;
      await pollGate;
    }
    await route.continue();
  });
  try {
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: "News Researcher" });
    await expect.poll(() => selecting).toBe(true);
    await expect.poll(() => !!pollRequest, { timeout: 12000 }).toBe(true);
    const pollProfile = new URL(pollRequest!.url()).searchParams.get("profile");
    releaseSelection();
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("News Researcher");
    const response = pollRequest!.response();
    releasePoll();
    await (await response)!.finished();
    // Retain the visible regression as well as its incorrect request identity.
    if (pollProfile === "trading-analyst")
      await expect(
        page.getByLabel("Agent", { exact: true }).locator("option:checked"),
      ).toHaveText("Trading Analyst");
    expect(pollProfile).toBe("news-researcher");
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("News Researcher");
  } finally {
    releaseSelection();
    releasePoll();
    await page.unroute(routeURL);
  }
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("News Researcher");
  expect(await readAll()).toEqual(before);
  expect(writes).toEqual([]);
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  expect(
    await page.evaluate(
      (key) => localStorage.getItem(key),
      `fased.agent-profile.v1:${health.instanceId}`,
    ),
  ).toBe("news-researcher");
});

test("chat drafts stay with their conversation through search switching and a pending save", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  const external: string[] = [];
  const writes: Record<string, unknown>[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
    if (new URL(r.url()).pathname === "/api/v1/agents" && r.method() === "POST")
      writes.push(r.postDataJSON());
  });
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const read = (id: string) => privateCLI(["agents", "--profile", id]);
  const profiles = ["trading-analyst", "news-researcher", "wen-operator"];
  const readAll = () => Promise.all(profiles.map(read));
  const seed = async (profileId: string, name: string) => {
    const v = await privateCLI(["agents", "--request-stdin"], {
      action: "session-create",
      profileId,
      name,
      revision: (await read(profileId)).revision,
    });
    return v.sessions.at(-1).id as string;
  };
  await seed("trading-analyst", "Earlier thread");
  const laterId = await seed("trading-analyst", "Later thread");
  await seed("news-researcher", "News thread");
  const baseline = await readAll();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const threads = page.locator(".agent-session-list");
  const message = page.getByRole("textbox", { name: "Message", exact: true });
  const search = page.getByLabel("Search conversations", { exact: true });
  const earlierDraft = "Unsent fixture context for the earlier thread.";
  const laterDraft = "Fixture context saved only in the later thread.";
  const earlierEdited = earlierDraft + " Edited while another thread saves.";
  await openConversationList(page);
  await threads
    .getByRole("button", { name: "Earlier thread", exact: true })
    .click();
  await revealControl(page, message);
  await message.fill(earlierDraft);
  await openConversationList(page);
  await threads
    .getByRole("button", { name: "Later thread", exact: true })
    .click();
  await expect(message).toHaveValue("");
  await revealControl(page, message);
  await message.fill(laterDraft);
  await openConversationList(page);
  await threads
    .getByRole("button", { name: "Earlier thread", exact: true })
    .click();
  await expect(message).toHaveValue(earlierDraft);
  await revealControl(page, search);
  await search.fill("Later thread");
  await expect(
    page.getByRole("heading", { name: "Later thread", exact: true }),
  ).toBeVisible();
  await expect(message).toHaveValue(laterDraft);
  expect(await readAll()).toEqual(baseline);
  let release = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  let saving = false;
  const routeURL = url + "/api/v1/agents";
  await page.route(routeURL, async (route) => {
    if (
      route.request().method() === "POST" &&
      route.request().postDataJSON().action === "message-add"
    ) {
      saving = true;
      await gate;
    }
    await route.continue();
  });
  try {
    await revealControl(
      page,
      page.getByRole("button", {
        name: "Save message",
        exact: true,
        includeHidden: true,
      }),
    );
    if (isMobile)
      await expect(
        page.getByRole("button", { name: "Conversations", exact: true }),
      ).toHaveAttribute("aria-expanded", "false");
    await page
      .getByRole("button", {
        name: "Save message",
        exact: true,
        includeHidden: true,
      })
      .click();
    await expect.poll(() => saving).toBe(true);
    await revealControl(page, search);
    await search.fill("Earlier thread");
    await expect(message).toHaveValue(earlierDraft);
    await revealControl(page, message);
    await message.fill(earlierEdited);
    release();
    await revealControl(
      page,
      page.getByRole("button", {
        name: "Save message",
        exact: true,
        includeHidden: true,
      }),
    );
    await expect(
      page.getByRole("button", {
        name: "Save message",
        exact: true,
        includeHidden: true,
      }),
    ).toBeEnabled();
    await expect(message).toHaveValue(earlierEdited);
  } finally {
    release();
    await page.unroute(routeURL);
  }
  await revealControl(page, search);
  await search.fill("");
  await openConversationList(page);
  await threads
    .getByRole("button", { name: "Later thread", exact: true })
    .click();
  await expect(message).toHaveValue("");
  await expect(
    page.locator(".local-messages").getByText(laterDraft, { exact: true }),
  ).toBeVisible();
  await openConversationList(page);
  await threads
    .getByRole("button", { name: "Earlier thread", exact: true })
    .click();
  await expect(message).toHaveValue(earlierEdited);
  await expect(
    page.locator(".local-messages").getByText(laterDraft, { exact: true }),
  ).toHaveCount(0);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await expect(
    page.getByRole("heading", { name: "News thread", exact: true }),
  ).toBeVisible();
  await expect(message).toHaveValue("");
  const after = await readAll();
  expect(after[0].messages).toEqual([
    expect.objectContaining({
      profileId: "trading-analyst",
      sessionId: laterId,
      role: "user",
      text: laterDraft,
    }),
    expect.objectContaining({
      profileId: "trading-analyst",
      sessionId: laterId,
      role: "system",
    }),
  ]);
  for (let i = 0; i < after.length; i++) {
    if (i > 0) expect(after[i].messages).toEqual(baseline[i].messages);
    for (const field of [
      "profiles",
      "sessions",
      "memories",
      "tasks",
      "runs",
      "notices",
      "aiRequests",
    ])
      expect(after[i][field]).toEqual(baseline[i][field]);
  }
  expect(writes).toEqual([
    expect.objectContaining({
      action: "message-add",
      profileId: "trading-analyst",
      id: laterId,
      text: laterDraft,
    }),
  ]);
  expect(external).toEqual([]);
  const preferences = await page.evaluate(() =>
    Object.values(localStorage).join("\n"),
  );
  expect(preferences).not.toContain(earlierDraft);
  expect(preferences).not.toContain(laterDraft);
  await captureScreenshot(page, {
    path: `.evidence/chat-drafts-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("agent selection survives reload and core restart with scoped preference fallback", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  const external: string[] = [];
  const writes: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
    if (new URL(r.url()).pathname === "/api/v1/agents" && r.method() !== "GET")
      writes.push(r.method());
  });
  await page.goto(url + "/#agents/memory");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = async () => {
    const process = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    process.stdin!.end(fixturePassword + "\n");
    expect((await once(process, "exit"))[0]).toBe(0);
  };
  await login();
  const read = () =>
    Promise.all(
      ["wen-operator", "trading-analyst", "news-researcher"].map((id) =>
        privateCLI(["agents", "--profile", id]),
      ),
    );
  const baseline = await read();
  const profile = (name: string) => page.getByLabel("Agent", { exact: true });
  const choose = async (name: string) => {
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: name });
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText(name);
  };
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Operator");
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  const prefix = "fased.agent-profile.v1:";
  const key = prefix + health.instanceId;
  await page.evaluate(
    (key) => localStorage.setItem(key, "trading-analyst"),
    prefix + "another-instance",
  );
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Operator");
  await choose("Trading Analyst");
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  expect(await read()).toEqual(baseline);
  await choose("News Researcher");
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("News Researcher");
  const originalURL = url;
  await stop();
  await start(false, new URL(originalURL).host);
  expect(url).toBe(originalURL);
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("News Researcher");
  await login();
  expect(await read()).toEqual(baseline);
  const unknownRequests: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("removed-profile")) unknownRequests.push(r.url());
  });
  await page.evaluate(
    (key) => localStorage.setItem(key, "removed-profile"),
    key,
  );
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Operator");
  expect(unknownRequests).toEqual([]);
  await page.evaluate((key) => localStorage.removeItem(key), key);
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Operator");
  await page.addInitScript((prefix) => {
    const read = Storage.prototype.getItem;
    const write = Storage.prototype.setItem;
    Storage.prototype.getItem = function (this: Storage, key: string) {
      if (key.startsWith(prefix))
        throw new DOMException("Fixture storage denied", "SecurityError");
      return read.call(this, key);
    };
    Storage.prototype.setItem = function (
      this: Storage,
      key: string,
      value: string,
    ) {
      if (key.startsWith(prefix))
        throw new DOMException("Fixture storage denied", "SecurityError");
      write.call(this, key, value);
    };
  }, prefix);
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Operator");
  await choose("Trading Analyst");
  expect(await read()).toEqual(baseline);
  expect(external).toEqual([]);
  expect(writes).toEqual([]);
  await captureScreenshot(page, {
    path: `.evidence/profile-continuity-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("conversation search follows matching records and closes hidden conversation actions", async ({
  page,
  isMobile,
}) => {
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#agents/chat");
  const profile = page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  const create = async (name: string, text: string) => {
    await revealControl(
      page,
      page.getByRole("textbox", { name: "Conversation name", exact: true }),
      "Conversation name",
    );
    await page
      .getByRole("textbox", { name: "Conversation name", exact: true })
      .fill(name);
    await page
      .getByRole("button", { name: "Create conversation", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name, exact: true }),
    ).toBeVisible();
    await revealControl(
      page,
      page.getByRole("textbox", { name: "Message", exact: true }),
      "",
    );
    await page
      .getByRole("textbox", { name: "Message", exact: true })
      .fill(text);
    await revealControl(
      page,
      page.getByRole("button", {
        name: "Save message",
        exact: true,
        includeHidden: true,
      }),
    );
    await page
      .getByRole("button", {
        name: "Save message",
        exact: true,
        includeHidden: true,
      })
      .click();
    await expect(page.locator(".local-messages")).toContainText(text);
  };
  await create(
    "Older saved research",
    "Orchid evidence belongs to the older conversation",
  );
  await create(
    "Recent separate notes",
    "Recent context must not remain open for an unrelated match",
  );
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const read = () => privateCLI(["agents", "--profile", "trading-analyst"]);
  const snapshot = await read();
  const search = page.getByRole("searchbox", {
    name: "Search conversations",
    exact: true,
    includeHidden: true,
  });
  for (const query of ["older saved", "ORCHID EVIDENCE"]) {
    await revealControl(page, search);
    await search.fill(query);
    await expect(
      page.locator(".agent-session-list").getByRole("button"),
    ).toHaveCount(1);
    await expect(
      page.getByRole("heading", { name: "Older saved research", exact: true }),
    ).toBeVisible();
    await expect(page.locator(".local-messages")).toContainText(
      "Orchid evidence",
    );
    await expect(page.locator(".local-messages")).not.toContainText(
      "Recent context",
    );
  }
  await revealControl(page, search);
  await search.fill("no-such-local-conversation");
  await expect(
    page.getByText("No matching conversations.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue("");
  await expect(
    page.getByRole("region", {
      name: "Optional AI request review",
      exact: true,
    }),
  ).toHaveCount(0);
  await expect(page.locator(".local-messages")).toHaveCount(0);
  await revealControl(page, search);
  await search.fill("");
  await expect(
    page.getByRole("heading", { name: "Recent separate notes", exact: true }),
  ).toBeVisible();
  expect(await read()).toEqual(snapshot);
  const cli = await privateCLI([
    "agents",
    "--profile",
    "trading-analyst",
    "--search",
    "ORCHID EVIDENCE",
  ]);
  expect(cli.sessions.map((s: { name: string }) => s.name)).toEqual([
    "Older saved research",
  ]);
  const foreign = await privateCLI([
    "agents",
    "--profile",
    "news-researcher",
    "--search",
    "ORCHID EVIDENCE",
  ]);
  expect(foreign.sessions).toHaveLength(0);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await mkdir(".evidence/usability", { recursive: true });
  await captureScreenshot(page, {
    path: `.evidence/usability/search-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("local agents share UI and CLI records, isolate profiles and survive core restart", async ({
  page,
}) => {
  test.setTimeout(60000);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/chat");
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  await revealControl(page, page.getByLabel("Name", { exact: true }), "");
  await page
    .getByLabel("Name", { exact: true })
    .fill("Local acceptance researcher");
  await page.getByRole("button", { name: "Create agent", exact: true }).click();
  await openConversationList(page);
  await expect(
    page.getByRole("heading", {
      name: "Conversations",
      exact: true,
    }),
  ).toBeVisible();
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page.getByLabel("Conversation name").fill("Saved thesis");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await revealControl(page, page.getByLabel("Message", { exact: true }), "");
  await page
    .getByLabel("Message", { exact: true })
    .fill("Our local thesis survives restart");
  await revealControl(
    page,
    page.getByRole("button", {
      name: "Save message",
      exact: true,
      includeHidden: true,
    }),
  );
  await page
    .getByRole("button", {
      name: "Save message",
      exact: true,
      includeHidden: true,
    })
    .click();
  await expect(
    page.getByText("Our local thesis survives restart", { exact: true }),
  ).toBeVisible();
  const savedNotice = page.getByText(
    "Saved locally. No AI provider was called.",
    { exact: false },
  );
  await expect(savedNotice).not.toBeVisible();
  await page.locator(".chat-system summary").click();
  await expect(savedNotice).toBeVisible();
  await page.locator(".chat-system summary").click();
  let view = await privateCLI(["agents"]);
  const p = view.profiles.find(
    (x: { name: string }) => x.name === "Local acceptance researcher",
  ).id;
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.messages).toHaveLength(2);
  const localCommand = async (command: object) => {
    const prior = await privateCLI(["agents", "--profile", p]);
    return privateCLI(["agents", "--request-stdin"], {
      revision: prior.revision,
      profileId: p,
      ...command,
    });
  };
  view = await localCommand({
    action: "memory-put",
    text: "Concise answers",
    source: "Owner preference",
  });
  const memoryID = view.memories[0].id;
  await page.evaluate(() => {
    window.location.hash = "agents/memory";
  });
  await expect(
    page.getByText("Concise answers", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await revealControl(page, page.getByLabel("Note", { exact: true }), "Fact");
  await page.getByLabel("Note", { exact: true }).fill("Detailed answers");
  await revealControl(page, page.getByLabel("Evidence or source"), "");
  await page
    .getByLabel("Evidence or source")
    .fill("Corrected owner preference");
  let releaseMemory!: () => void;
  let memoryCommitted!: () => void;
  const heldMemory = new Promise<void>((resolve) => (releaseMemory = resolve));
  const committedMemory = new Promise<void>(
    (resolve) => (memoryCommitted = resolve),
  );
  const holdMemoryResponse = async (route: Route) => {
    const request = route.request();
    if (
      request.method() === "POST" &&
      request.postDataJSON().action === "memory-put"
    ) {
      const response = await route.fetch();
      memoryCommitted();
      await heldMemory;
      await route.fulfill({ response });
    } else await route.continue();
  };
  await page.route("**/api/v1/agents", holdMemoryResponse);
  await page
    .getByRole("button", { name: "Save correction", exact: true })
    .click();
  await committedMemory;
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.memories[0].id).toBe(memoryID);
  expect(view.memories[0].history[0].text).toBe("Concise answers");
  await page.evaluate(() => {
    window.location.hash = "agents/settings";
  });
  try {
    await revealControl(page, page.getByLabel("Provider", { exact: true }), "");
    await expect(page.getByLabel("Provider", { exact: true })).toBeDisabled();
  } finally {
    releaseMemory();
  }
  await expect(page.getByLabel("Provider", { exact: true })).toBeEnabled();
  await page.unroute("**/api/v1/agents", holdMemoryResponse);
  await page.getByLabel("Provider", { exact: true }).selectOption("openai");
  await revealControl(page, page.getByLabel("Model", { exact: true }), "");
  await expect(page.getByLabel("Model", { exact: true })).toHaveValue(
    "gpt-6.1-terra",
  );
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", p])).profiles.find(
          (x: { id: string }) => x.id === p,
        ).provider,
    )
    .toBe("openai");
  view = await privateCLI(["agents", "--request-stdin"], {
    action: "bot-create",
    profileId: p,
    revision: (await privateCLI(["agents", "--profile", p])).revision,
    name: "Fixed example",
    quantity: 1,
    priceCents: 100,
    budgetCents: 500,
  });
  expect(view.bots[0].name).toBe("Fixed example");
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
  await revealControl(
    page,
    page.getByLabel("Task name", { exact: true }),
    "Task name",
  );
  await page
    .getByLabel("Task name", { exact: true })
    .fill("One paper evaluation");
  view = await privateCLI(["agents", "--profile", p]);
  await revealControl(page, page.getByLabel("Task kind"), "");
  await page.getByLabel("Task kind").selectOption(view.bots[0].id);
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await page
    .getByRole("button", { name: "Run now: One paper evaluation", exact: true })
    .click();
  await page.getByRole("button", { name: "Results", exact: true }).click();
  await expect(page.getByText("paper-filled", { exact: true })).toBeVisible();
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.runs).toHaveLength(1);
  expect(view.bots[0].spentCents).toBe(100);
  expect(view.financialExecution).toBe(false);
  expect(view.providerCallsEnabled).toBe(true);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.messages).toHaveLength(0);
  expect(other.memories).toHaveLength(0);
  expect(other.runs).toHaveLength(0);
  expect(
    await page
      .locator(".local-agents .memory-card")
      .first()
      .locator("p")
      .first()
      .evaluate((e) => e.getBoundingClientRect().width),
  ).toBeGreaterThan(150);
  await page.getByRole("button", { name: "Refresh", exact: true }).focus();
  await captureScreenshot(page, {
    path: resolve(
      ".evidence/p5-local-agents/tasks-" + test.info().project.name + ".png",
    ),
    fullPage: true,
  });
  await stop();
  await start();
  await page.goto(url + "/#agents/chat");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Local acceptance researcher" });
  await expect(
    page.getByText("Our local thesis survives restart", { exact: true }),
  ).toBeVisible();
  view = await privateCLI(["agents", "--profile", p, "--search", "thesis"]);
  expect(view.messages).toHaveLength(2);
  expect(view.runs).toHaveLength(1);
  expect(view.notices).toHaveLength(1);
  expect(view.memories[0].text).toBe("Detailed answers");
  await captureScreenshot(page, {
    path: resolve(
      ".evidence/p5-local-agents/chat-" + test.info().project.name + ".png",
    ),
    fullPage: true,
  });
});

test("new conversation keeps its selected account and model through first Send", async ({
  page,
  isMobile,
}) => {
  await stop();
  await start(true);
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const passphrase = "disposable-new-chat-vault";
  const first = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "First account",
    key: "fased-test-new-chat-first-key",
    passphrase,
  });
  const second = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "openai",
    name: "Default account",
    key: "fased-test-new-chat-second-key",
    passphrase,
  });
  expect(first.ref).not.toBe(second.ref);
  const firstModel = first.catalog.models.find(
    (m: { selectable: boolean; recommended: boolean }) =>
      m.selectable && m.recommended,
  ).id;
  const defaultModel = second.catalog.models.find(
    (m: { selectable: boolean; recommended: boolean }) =>
      m.selectable && m.recommended,
  ).id;
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation.getByLabel("Name", { exact: true }).fill("Selection agent");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(second.ref);
  await creation
    .getByLabel("Model", { exact: true })
    .selectOption(defaultModel);
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).not.toBeVisible();
  const view = await privateCLI(["agents"]);
  const profile = view.profiles.find((p: any) => p.name === "Selection agent");
  expect(profile.connectionRef).toBe(second.ref);
  const form = page.locator(".chat-message-form");
  await form.getByLabel("Message", { exact: true }).fill("Review this draft");
  await form.getByLabel("Reply model", { exact: true }).click();
  await form.getByLabel("Provider", { exact: true }).selectOption(first.ref);
  await form.getByLabel("Model", { exact: true }).selectOption(firstModel);
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption("trading-analyst");
  await page.getByLabel("Agent", { exact: true }).selectOption(profile.id);
  await expect(form.getByLabel("Message", { exact: true })).toHaveValue(
    "Review this draft",
  );
  await form.getByLabel("Reply model", { exact: true }).click();
  await expect(form.getByLabel("Provider", { exact: true })).toHaveValue(
    first.ref,
  );
  await expect(form.getByLabel("Model", { exact: true })).toHaveValue(
    firstModel,
  );
  await captureScreenshot(page, {
    path: test
      .info()
      .outputPath(`new-chat-model-${isMobile ? "mobile" : "desktop"}.png`),
  });
  const submitted = page.waitForRequest((request) => {
    if (
      request.method() !== "POST" ||
      !request.url().endsWith("/api/v1/agents")
    )
      return false;
    return request.postDataJSON()?.action === "chat-review";
  });
  let releaseCreation = () => {};
  let markCreationHeld = () => {};
  const creationHeld = new Promise<void>((resolve) => {
    markCreationHeld = resolve;
  });
  const resumeCreation = new Promise<void>((resolve) => {
    releaseCreation = resolve;
  });
  let held = false;
  await page.route("**/api/v1/agents", async (route) => {
    if (
      !held &&
      route.request().method() === "POST" &&
      route.request().postDataJSON()?.action === "session-create"
    ) {
      held = true;
      markCreationHeld();
      await resumeCreation;
    }
    await route.continue();
  });
  try {
    await form.getByRole("button", { name: "Send", exact: true }).click();
    await creationHeld;
    await form.getByLabel("Provider", { exact: true }).selectOption(second.ref);
  } finally {
    releaseCreation();
  }
  expect((await submitted).postDataJSON()).toMatchObject({
    connectionRef: first.ref,
    model: firstModel,
    useAgentDefaults: false,
  });
  await page
    .getByRole("dialog", { name: "Allow replies", exact: true })
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  const after = await privateCLI(["agents", "--profile", profile.id]);
  expect(after.aiRequests.at(-1)).toMatchObject({
    connectionRef: first.ref,
    model: firstModel,
    profileId: profile.id,
  });
  expect(
    after.profiles.find((p: any) => p.id === profile.id).connectionRef,
  ).toBe(second.ref);
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption("trading-analyst");
  await page.getByLabel("Agent", { exact: true }).selectOption(profile.id);
  const replyMenu = page.locator(".chat-composer-toolbar .reply-model-menu");
  await replyMenu.locator("summary").click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    first.ref,
  );
  await expect(replyMenu.getByLabel("Model", { exact: true })).toHaveValue(
    firstModel,
  );
  await openConversationList(page);
  await page
    .locator(".rail-heading")
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Second conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Second conversation", exact: true }),
  ).toBeVisible();
  if ((await replyMenu.getAttribute("open")) === null)
    await replyMenu.locator("summary").click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    second.ref,
  );
  await openConversationList(page);
  await page
    .locator(".agent-session-list")
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  if ((await replyMenu.getAttribute("open")) === null)
    await replyMenu.locator("summary").click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    first.ref,
  );
  await openConversationList(page);
  await page
    .locator(".agent-session-list")
    .getByRole("button", { name: "Second conversation", exact: true })
    .click();
  if ((await replyMenu.getAttribute("open")) === null)
    await replyMenu.locator("summary").click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    second.ref,
  );
  await openConversationList(page);
  await page
    .locator(".agent-session-list")
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  if ((await replyMenu.getAttribute("open")) === null)
    await replyMenu.locator("summary").click();
  await replyMenu
    .getByRole("button", { name: "Use agent default", exact: true })
    .click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    second.ref,
  );
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption("trading-analyst");
  await page.getByLabel("Agent", { exact: true }).selectOption(profile.id);
  await replyMenu.locator("summary").click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    second.ref,
  );
});

test("naming an empty conversation keeps its draft and selected account", async ({
  page,
}) => {
  await stop();
  await start(true);
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const passphrase = "disposable-named-chat-vault";
  const fallback = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Default account",
    key: "fased-test-named-chat-default",
    passphrase,
  });
  const request = (route: string, method?: string, body?: unknown) =>
    browserRequest(page, route, method, body);
  const defaultModel = fallback.catalog.models.find(
    (m: any) => m.selectable && m.recommended,
  ).id;
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Named conversation agent");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(fallback.ref);
  await creation
    .getByLabel("Model", { exact: true })
    .selectOption(defaultModel);
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const chosen = await installedConversationAccount(
    request,
    passphrase,
    fallback.ref,
  );
  expect(
    (await installedConversationAccount(request, passphrase, fallback.ref)).ref,
  ).toBe(chosen.ref);
  expect(chosen.testOnly).toBe(true);
  expect(chosen.liveCallsEnabled).toBe(false);
  expect(chosen.ref).not.toBe(fallback.ref);
  const selectedModel = chosen.catalog.models.find(
    (m: any) => m.selectable && m.recommended,
  ).id;
  expect(selectedModel).toBe(chosen.admissionPolicy.model);
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Named conversation agent",
  );
  await page.reload();
  await page.getByLabel("Agent", { exact: true }).selectOption(profile.id);
  const form = page.locator(".chat-message-form");
  await prepareNamedConversation(page, {
    name: "Named draft",
    draft: "Keep this draft",
    ref: chosen.ref,
    model: selectedModel,
  });
  let rejected = false;
  const refuseOnce = async (route: Route) => {
    if (
      !rejected &&
      route.request().method() === "POST" &&
      route.request().postDataJSON()?.action === "session-create"
    ) {
      rejected = true;
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: "Fixture refused creation" }),
      });
    } else await route.continue();
  };
  await page.route("**/api/v1/agents", refuseOnce);
  const refused = page.waitForResponse(
    (response) =>
      response.status() === 409 &&
      response.request().method() === "POST" &&
      response.request().postDataJSON()?.action === "session-create",
  );
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await refused;
  await expect(
    page.getByRole("button", { name: "Create conversation", exact: true }),
  ).toBeEnabled();
  await expect(form.getByLabel("Message", { exact: true })).toHaveValue(
    "Keep this draft",
  );
  await page.unroute("**/api/v1/agents", refuseOnce);
  await finishNamedConversation(page, expect, {
    name: "Named draft",
    draft: "Keep this draft",
    ref: chosen.ref,
    model: selectedModel,
  });
  const replyMenu = page.locator(".chat-composer-toolbar .reply-model-menu");
  await openConversationList(page);
  await page
    .locator(".rail-heading")
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Another conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Another conversation", exact: true }),
  ).toBeVisible();
  await expect(form.getByLabel("Message", { exact: true })).toHaveValue("");
  if ((await replyMenu.getAttribute("open")) === null)
    await replyMenu.locator("summary").click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    fallback.ref,
  );
  await openConversationList(page);
  await page
    .locator(".agent-session-list")
    .getByRole("button", { name: "Named draft", exact: true })
    .click();
  await expect(form.getByLabel("Message", { exact: true })).toHaveValue(
    "Keep this draft",
  );
  if ((await replyMenu.getAttribute("open")) === null)
    await replyMenu.locator("summary").click();
  await expect(replyMenu.getByLabel("Provider", { exact: true })).toHaveValue(
    chosen.ref,
  );
  await form.getByRole("button", { name: "Send", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Allow replies", exact: true })
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(
          -1,
        )?.state,
    )
    .toBe("completed");
  expect(
    (await privateCLI(["agents", "--profile", profile.id])).aiRequests.at(-1),
  ).toMatchObject({
    connectionRef: chosen.ref,
    model: selectedModel,
  });
});

test("configured agent joins account Chat task result and memory without partial setup", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
  const external: string[] = [];
  const origins = new Set([new URL(url).origin]);
  page.on("request", (r) => {
    if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
  });
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const passphrase = "disposable-configured-agent-vault";
  const primary = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Primary",
    key: "fased-test-configured-primary-key",
    passphrase,
  });
  const account = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "openai",
    name: "Research",
    key: "fased-test-configured-research-key",
    passphrase,
  });
  const before = await privateCLI(["agents"]);
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption("news-researcher");
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Configured researcher");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await creation
    .getByLabel("Model", { exact: true })
    .selectOption("gpt-6.1-terra");
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).not.toBeVisible();
  const current = await privateCLI(["agents"]);
  const profile = current.profiles.find(
    (p: any) => p.name === "Configured researcher",
  );
  expect(current.profiles.length).toBe(before.profiles.length + 1);
  expect(profile).toMatchObject({
    provider: "openai",
    model: "gpt-6.1-terra",
    connectionRef: account.ref,
  });
  expect(profile.connectionRef).not.toBe(primary.ref);
  const read = () => privateCLI(["agents", "--profile", profile.id]);
  expect((await read()).aiRequests).toHaveLength(0);
  expect((await read()).tasks).toHaveLength(0);
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(
    profile.id,
  );
  await page.waitForResponse(
    (r) =>
      r.request().method() === "GET" &&
      new URL(r.url()).pathname === "/api/v1/agents",
  );
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(
    profile.id,
  );
  await revealControl(
    page,
    page.getByLabel("Conversation name", { exact: true }),
    "Conversation name",
  );
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("New conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Help me draft a reminder to inspect SOL risk");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Allow replies", exact: true })
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  const request = (await read()).aiRequests.at(-1);
  expect(request).toMatchObject({
    connectionRef: account.ref,
    model: profile.model,
    profileId: profile.id,
  });
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  await page
    .getByLabel("Reminder task name", { exact: true })
    .fill("Configured risk review");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Inspect the saved SOL risk limits");
  await page
    .getByRole("button", { name: "Review schedule", exact: true })
    .click();
  await page.getByRole("checkbox", { name: /reviewed.*reminder/i }).check();
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await expect.poll(async () => (await read()).tasks.length).toBe(1);
  await page.getByRole("button", { name: "Open Tasks", exact: true }).click();
  await page
    .getByRole("button", {
      name: "Run now: Configured risk review",
      exact: true,
    })
    .click();
  await expect.poll(async () => (await read()).runs.length).toBe(1);
  expect((await read()).runs[0].outcome).toBe("reminded");
  await page.getByRole("button", { name: "Discuss", exact: true }).click();
  const source = (await read()).messages.at(-1);
  expect(source.text).toContain((await read()).runs[0].id);
  await page
    .getByRole("button", { name: "Remember this", exact: true })
    .last()
    .click();
  await page
    .getByLabel("Note", { exact: true })
    .fill("Prefer daily SOL risk summaries");
  await page.getByRole("button", { name: "Save memory", exact: true }).click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page
    .getByRole("navigation", { name: "More pages", exact: true })
    .getByRole("link", { name: "Memory", exact: true })
    .click();
  await expect(
    page.getByText("Prefer daily SOL risk summaries", { exact: true }),
  ).toBeVisible();
  const retained = await read();
  expect(JSON.stringify(retained.memories[0])).toContain(source.id);
  expect(
    (await privateCLI(["agents", "--profile", "news-researcher"])).memories,
  ).toHaveLength(0);
  await captureScreenshot(page, {
    path: test
      .info()
      .outputPath(`joined-memory-${isMobile ? "mobile" : "desktop"}.png`),
  });
  await stop();
  await start(true);
  origins.add(new URL(url).origin);
  const restartLogin = spawn(binary, [
    "login",
    "--data-dir",
    dir,
    "--password-stdin",
  ]);
  restartLogin.stdin!.end(fixturePassword + "\n");
  expect((await once(restartLogin, "exit"))[0]).toBe(0);
  const restored = await read();
  for (const field of ["profiles", "tasks", "runs", "memories", "aiRequests"])
    expect(restored[field]).toEqual(retained[field]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const name of [
    "aiConnections",
    "walletSigner",
    "financialExecution",
    "updater",
  ])
    expect(health.capabilities[name]).toBe(false);
  expect(external).toEqual([]);
});

test("first Send retains its exact draft when conversation changes during account loading", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const binding = await fixtureProviderSelection("openai");
  let view = await privateCLI(["agents"]);
  const profileId = view.profileId;
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (fields: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...fields,
    });
  const profile = view.profiles.find((p: any) => p.id === profileId);
  await command({
    action: "profile-update",
    name: profile.name,
    text: profile.persona,
    ...binding,
  });
  await command({ action: "session-create", name: "Previous conversation" });
  await page.reload();
  const input = page.getByLabel("Message", { exact: true });
  await input.fill("Preserved previous draft");
  await openConversationList(page);
  await page
    .getByLabel("Search conversations", { exact: true })
    .fill("no such conversation");
  await input.fill("New explicit first-send draft");
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let blocked = 0;
  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "GET") {
      blocked++;
      await held;
    }
    await route.continue();
  });
  try {
    await page.getByRole("button", { name: "Send", exact: true }).click();
    await expect.poll(() => blocked).toBeGreaterThan(0);
    await openConversationList(page);
    await page
      .getByRole("button", { name: "Previous conversation", exact: true })
      .click();
  } finally {
    release();
  }
  await page.unrouteAll({ behavior: "wait" });
  await expect(input).toHaveValue("Preserved previous draft");
  await expect(page.locator(".reply-model-menu > summary")).toHaveText(
    binding.model,
  );
  await expect(
    page.getByRole("dialog", { name: "Allow replies", exact: true }),
  ).toHaveCount(0);
  expect((await read()).aiRequests).toHaveLength(0);
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .filter({ hasNot: page.locator("svg") })
    .click();
  await expect(input).toHaveValue("New explicit first-send draft");
  await expect(page.locator(".reply-model-menu > summary")).toHaveText(
    binding.model,
  );
  await expect(
    page.getByRole("dialog", { name: "Allow replies", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "Allow replies", exact: true }),
  ).toBeVisible();
  expect((await read()).aiRequests).toHaveLength(0);
});

test("Chat Options closes on Escape before the native toggle event without losing its draft", async ({
  page,
}) => {
  const link = (
    await exec(binary, ["setup-link", "--data-dir", dir])
  ).stdout.trim();
  await page.goto(link);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const initial = await privateCLI(["agents"]);
  await privateCLI(["agents", "--request-stdin"], {
    action: "session-create",
    profileId: initial.profileId,
    revision: initial.revision,
    name: "Options keyboard",
  });
  await page.goto(url + "/#agents/chat");
  const form = page.locator(".chat-message-form");
  const input = page.getByLabel("Message", { exact: true });
  await input.fill("Preserve this keyboard draft");
  const before = await privateCLI(["agents"]);
  // Exercise both native actions synchronously, before the queued details
  // toggle event can hydrate the controlled disclosure state.
  await form
    .locator(".message-options > summary")
    .evaluate((summary: HTMLElement) => {
      summary.click();
      if (!(summary.parentElement as HTMLDetailsElement).open)
        throw Error("Native summary did not open");
      summary.dispatchEvent(
        new KeyboardEvent("keydown", {
          key: "Escape",
          bubbles: true,
          cancelable: true,
        }),
      );
    });
  await expect(form.locator(".message-options")).not.toHaveAttribute(
    "open",
    "",
  );
  await expect(form.locator(".message-options-panel")).not.toBeVisible();
  await expect(form.getByLabel("Options", { exact: true })).toBeFocused();
  await expect(input).toHaveValue("Preserve this keyboard draft");
  const after = await privateCLI(["agents"]);
  expect(after).toEqual(before);
});
