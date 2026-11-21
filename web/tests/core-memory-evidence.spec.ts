import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test, type Request } from "@playwright/test";
import { spawn } from "node:child_process";
import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { once } from "node:events";
import { createHash } from "node:crypto";
import {
  installCoreFixture,
  captureScreenshot,
  exactReplyReview,
  openConversationList,
  revealControl,
  openEvidence,
  binary,
  fixturePassword,
  ownedFixtureOrigins,
  dir,
  url,
  output,
  start,
  stop,
  privateCLI,
  fixtureProviderSelection,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("agent memory and archives keep recall sources journals and results through UI CLI restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
  const p = "trading-analyst";
  await page.getByLabel("Agent", { exact: true }).selectOption(p);
  const read = () => privateCLI(["agents", "--profile", p]);
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Agent workbench");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Remember a fact", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("/remember My SOL risk budget is 500 units");
  await page.getByRole("button", { name: "Remember", exact: true }).click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  let v = await read();
  const note = v.memories[0];
  expect(v.aiRequests).toHaveLength(0);
  await page
    .getByLabel("Message", { exact: true })
    .fill("What is my SOL risk budget?");
  await exactReplyReview(page);
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(async () => (await read()).aiRequests.length).toBe(1);
  v = await read();
  const prepared = v.aiRequests[0];
  expect(JSON.parse(prepared.context).memorySelection.mode).toBe("relevant");
  expect(prepared.context).toContain(note.text);
  const card = page.locator(".ai-request").last();
  await card
    .getByRole("checkbox", {
      name: "I reviewed this context and these limits",
      exact: true,
    })
    .check();
  await card.getByRole("button", { name: "Run preview", exact: true }).click();
  await expect
    .poll(async () => (await read()).aiRequests[0].state)
    .toBe("completed");
  v = await read();
  const answer = v.aiRequests[0];
  await expect(card).toContainText(answer.answer);
  await page
    .getByRole("button", { name: "Archive conversation", exact: true })
    .click();
  const archive = page.getByRole("dialog", {
    name: "Archived conversations",
    exact: true,
  });
  await expect(archive).toBeVisible();
  await archive
    .locator(".memory-row")
    .filter({ hasText: "Agent workbench" })
    .click();
  await expect(archive).toContainText(answer.answer);
  const downloaded = page.waitForEvent("download");
  await archive
    .getByRole("button", { name: "Export JSON", exact: true })
    .click();
  const exported = JSON.parse(
    await readFile((await (await downloaded).path())!, "utf8"),
  );
  expect(exported.entry.aiRequests[0].digest).toBe(answer.digest);
  expect(JSON.stringify(exported)).not.toContain(fixturePassword);
  await captureScreenshot(page, {
    path: `.evidence/conversation-archive-${test.info().project.name}.png`,
    fullPage: false,
  });
  await archive
    .getByRole("button", { name: "Close dialog", exact: true })
    .click();
  v = await read();
  expect(v.sessions).toHaveLength(0);
  expect(v.aiRequests).toHaveLength(0);
  expect(v.archivedAISpentMicrousd).toBe(answer.spentMicrousd);
  const source = await privateCLI([
    "agents",
    "--profile",
    p,
    "--memory-source",
    note.id,
  ]);
  expect(source.source.status).toBe("matched");
  const cliArchive = await privateCLI([
    "agents",
    "--profile",
    p,
    "--history",
    "--history-kind",
    "conversations",
    "--history-search",
    "SOL RISK",
  ]);
  expect(cliArchive.records).toHaveLength(1);
  await page.goto(url + "/#agents/memory");
  const memoryCard = page.getByRole("article", {
    name: `Memory ${note.id}`,
    exact: true,
  });
  await expect(memoryCard).toContainText(note.text);
  await memoryCard.getByRole("button", { name: "Edit", exact: true }).click();
  const edit = page.getByRole("dialog", {
    name: "Edit",
    exact: true,
  });
  await expect(edit).toBeVisible();
  await expect(edit.getByLabel("Evidence or source")).not.toBeVisible();
  await edit
    .getByLabel("Note", { exact: true })
    .fill("My SOL risk budget is 250 units");
  await edit
    .getByRole("button", { name: "Save correction", exact: true })
    .click();
  await expect(memoryCard).toContainText("250 units");
  await captureScreenshot(page, {
    path: `.evidence/memory-library-${test.info().project.name}.png`,
    fullPage: false,
  });
  const saved = await read(),
    address = new URL(url);
  await stop();
  await start(true, address.host);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  expect(await read()).toEqual(saved);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId: p,
      revision: (await read()).revision,
      ...c,
    });
  v = await command({ action: "session-create", name: "Current preferences" });
  const session = v.sessions[0].id;
  v = await command({
    action: "ai-prepare",
    id: session,
    text: "Explain my SOL risk budget",
    memorySelection: { mode: "relevant" },
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  expect(v.aiRequests[0].context).toContain("250 units");
  expect(v.aiRequests[0].context).not.toContain("500 units");
  v = await command({
    action: "task-create",
    name: "Archive reminder result",
    text: "Read the retained journal",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  const task = v.tasks[0];
  await command({ action: "task-run", id: task.id });
  await page.goto(url + "/#agents/tasks");
  await page
    .getByRole("button", {
      name: "Result: Archive reminder result",
      exact: true,
    })
    .first()
    .click();
  await page.getByRole("button", { name: "Tasks", exact: true }).click();
  await page
    .getByRole("button", { name: "Archive reminder result", exact: true })
    .click();
  await page.getByRole("button", { name: "Archive task", exact: true }).click();
  const tasks = page.getByRole("region", {
    name: "Archived tasks",
    exact: true,
  });
  await expect(tasks.locator(".memory-row")).toContainText(
    "Archive reminder result",
  );
  const old = await privateCLI(["agents", "--profile", p, "--history"]);
  expect(old.records).toHaveLength(1);
  expect((await read()).tasks).toHaveLength(0);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  if (isMobile)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
      JSON.stringify(
        await page.evaluate(() => ({
          width: innerWidth,
          scroll: document.documentElement.scrollWidth,
          offenders: [...document.querySelectorAll("body *")]
            .filter(
              (el) =>
                el.getBoundingClientRect().right > innerWidth + 1 &&
                getComputedStyle(el).display !== "none",
            )
            .slice(0, 6)
            .map((el) => ({
              tag: el.tagName,
              classes: el.className,
              right: el.getBoundingClientRect().right,
            })),
        })),
      ),
    ).toBe(true);
  expect(external).toEqual([]);
});

test("reviewed memory restoration shares inert UI CLI drafts, expiry, current-only context and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  const writes: Record<string, unknown>[] = [];
  page.on("request", (request) => {
    if (!ownedFixtureOrigins.has(new URL(request.url()).origin))
      external.push(request.url());
    if (
      request.method() === "POST" &&
      new URL(request.url()).pathname === "/api/v1/agents"
    )
      writes.push(request.postDataJSON());
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
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  const draft = (id: string, index = 0, profile = profileId) =>
    privateCLI([
      "agents",
      "--profile",
      profile,
      "--memory-restore-draft",
      id,
      "--memory-revision",
      String(index),
    ]);
  let v = await command({
    action: "session-create",
    name: "Restoration source",
  });
  const sessionId = v.sessions[0].id;
  v = await command({
    action: "message-add",
    id: sessionId,
    text: "Original retained evidence, claims unverified.",
  });
  const futureExpiry = (Math.floor(Date.now() / 60000) + 61) * 60 + 17;
  v = await command({
    action: "memory-chat-save",
    id: v.messages[0].id,
    source: "message",
    text: "Original concise preference",
    expiresAt: futureExpiry,
  });
  const original = v.memories[0];
  await command({
    action: "memory-put",
    id: original.id,
    text: "Current owner preference",
    source: "Owner current",
  });
  v = await command({
    action: "memory-put",
    text: "Expired retained preference",
    source: "Owner expired",
    expiresAt: 1,
  });
  const expiredID = v.memories.find(
    (m: { text: string }) => m.text === "Expired retained preference",
  ).id;
  await command({
    action: "memory-put",
    id: expiredID,
    text: "Current expiry preference",
    source: "Owner expiry current",
  });
  await privateCLI(["agents", "--request-stdin"], {
    action: "memory-put",
    profileId: "news-researcher",
    revision: (await read()).revision,
    text: "Foreign profile secret",
    source: "Foreign owner",
  });
  const before = await read();
  const foreignBefore = await privateCLI([
    "agents",
    "--profile",
    "news-researcher",
  ]);
  const cliDraft = await draft(original.id);
  expect(cliDraft.version).toBe("memory-restore-draft-v1");
  expect(cliDraft.command).toMatchObject({
    action: "memory-put",
    revision: before.revision,
    id: original.id,
    profileId,
    text: original.text,
    source: original.source,
    expiresAt: futureExpiry,
  });
  expect(await read()).toEqual(before);
  for (const args of [
    ["--memory-restore-draft", original.id],
    ["--memory-revision", "0"],
    [
      "--memory-restore-draft",
      original.id,
      "--memory-revision",
      "0",
      "--memory-search",
      "",
    ],
    ["--memory-restore-draft", original.id, "--memory-revision", "8"],
  ])
    await expect(
      privateCLI(["agents", "--profile", profileId, ...args]),
    ).rejects.toThrow();
  await expect(draft(original.id, 0, "news-researcher")).rejects.toThrow();
  const trading = () => page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const card = (id: string) =>
    page.getByRole("article", { name: `Memory ${id}`, exact: true });
  async function review(id: string) {
    await expect(card(id)).toBeVisible();
    if ((await card(id).locator("details").getAttribute("open")) === null)
      await card(id).locator("summary").click();
    await card(id)
      .getByRole("region", { name: "Memory revision 1", exact: true })
      .getByRole("button", { name: "Review restoration", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Restore a memory", exact: true }),
    ).toBeVisible();
  }
  await review(original.id);
  await expect(
    page.getByRole("region", { name: "Restoration source evidence" }),
  ).toContainText("Original retained evidence, claims unverified.");
  await expect(
    page.getByRole("article", { name: "Current memory revision", exact: true }),
  ).toContainText("Current owner preference");
  await expect(
    page.getByRole("textbox", { name: "Note", exact: true }),
  ).toHaveValue(original.text);
  expect(await read()).toEqual(before);
  expect(writes).toHaveLength(0);
  await captureScreenshot(page, {
    path: `.evidence/memory-restore-review-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Cancel restoration", exact: true })
    .click();
  expect(await read()).toEqual(before);
  await review(original.id);
  await command({
    action: "memory-put",
    id: original.id,
    text: "Newer owner correction",
    source: "Owner current",
  });
  await expect(
    page.getByRole("button", { name: "Save restored memory", exact: true }),
  ).toBeDisabled();
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "Workspace changed. Review the restoration again." }),
  ).toBeVisible();
  await expect(
    privateCLI(["agents", "--request-stdin"], cliDraft.command),
  ).rejects.toThrow(/conflict|changed|refresh/i);
  const changed = await read();
  expect(writes).toHaveLength(0);
  await page
    .getByRole("button", { name: "Cancel restoration", exact: true })
    .click();
  await review(original.id);
  await page
    .getByRole("button", { name: "Save restored memory", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await read()).memories.find(
          (m: { id: string }) => m.id === original.id,
        ).text,
    )
    .toBe(original.text);
  let restored = (await read()).memories.find(
    (m: { id: string }) => m.id === original.id,
  );
  expect(restored).toMatchObject({
    source: original.source,
    expiresAt: futureExpiry,
    expired: false,
  });
  expect(restored.history.slice(0, 2)).toEqual(changed.memories[0].history);
  expect(restored.history[2].text).toBe("Newer owner correction");
  expect(writes).toHaveLength(1);
  expect(writes[0]).toMatchObject({
    action: "memory-put",
    revision: changed.revision,
    id: original.id,
    expiresAt: futureExpiry,
  });
  await expect(
    page.getByRole("heading", { name: "Add a memory", exact: true }),
  ).not.toBeVisible();
  await review(expiredID);
  await expect(
    page.getByRole("status").filter({ hasText: "Expired revision" }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Save restored memory", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await read()).memories.find((m: { id: string }) => m.id === expiredID)
          .text,
    )
    .toBe("Expired retained preference");
  restored = (await read()).memories.find(
    (m: { id: string }) => m.id === expiredID,
  );
  expect(restored).toMatchObject({
    source: "Owner expired",
    expiresAt: 1,
    expired: true,
  });
  expect(writes).toHaveLength(2);
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: "Review the current notes",
    memorySelection: { mode: "all" },
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const context = JSON.parse(v.aiRequests[0].context).context;
  expect(context.memories).toHaveLength(1);
  expect(context.memories[0]).toMatchObject({
    id: original.id,
    text: original.text,
    source: original.source,
    expiresAt: futureExpiry,
  });
  expect(context.memories[0]).not.toHaveProperty("history");
  const retained = (await read()).memories;
  const address = new URL(url).host;
  await stop();
  await start(false, address);
  await page.reload();
  await expect(card(original.id)).toBeVisible();
  expect((await read()).memories).toEqual(retained);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await expect(card(original.id)).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Save restored memory", exact: true }),
  ).toHaveCount(0);
  expect(
    (await privateCLI(["agents", "--profile", "news-researcher"])).memories,
  ).toEqual(foreignBefore.memories);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(card(original.id)).toBeVisible();
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    ...(await fixtureProviderSelection("xai")),
  });
  const cliRestore = await draft(original.id, 1);
  expect(cliRestore.selected.text).toBe("Current owner preference");
  v = await privateCLI(["agents", "--request-stdin"], cliRestore.command);
  expect(v.memories[0].text).toBe("Current owner preference");
  expect(v.memories[0].history).toHaveLength(4);
  // Explicitly refresh after the CLI write; do not race the five-second poll.
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(card(original.id).locator(":scope > p")).toHaveText(
    "Current owner preference",
  );
  expect(v.providerCallsEnabled).toBe(true);
  expect(v.financialExecution).toBe(false);
  expect(v.tasks).toHaveLength(0);
  expect(v.runs).toHaveLength(0);
  expect(external).toEqual([]);
  expect(writes).toHaveLength(2);
});

test("chat memory requires explicit source-backed UI and CLI save and survives restart without AI", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
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
  const login = async () => {
    const c = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
    c.stdin!.end(fixturePassword + "\n");
    expect((await once(c, "exit"))[0]).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  let v = await command({ action: "session-create", name: "Memory review" });
  const sessionId = v.sessions[0].id;
  v = await command({
    action: "message-add",
    id: sessionId,
    text: "Original preference: compare sources before decisions",
  });
  const message = v.messages[0];
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/chat");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const source = page
    .locator(".local-messages article")
    .filter({ hasText: message.text });
  await source
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const review = source.getByRole("region", { name: "Chat memory review" });
  await expect(review.getByLabel("Note", { exact: true })).toHaveValue(
    message.text,
  );
  await revealControl(page, review.getByLabel("Note", { exact: true }), "");
  await review.getByLabel("Note", { exact: true }).fill("Cancelled draft");
  expect((await read()).memories).toHaveLength(0);
  await review
    .getByRole("button", { name: "Cancel memory", exact: true })
    .click();
  expect((await read()).memories).toHaveLength(0);
  await source
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  await command({
    action: "memory-put",
    text: "Concurrent owner note",
    source: "owner",
  });
  await expect(
    review.getByText(
      "Workspace changed. Cancel and select the source again before saving.",
    ),
  ).toBeVisible({ timeout: 12000 });
  await expect(
    review.getByRole("button", { name: "Save memory", exact: true }),
  ).toBeDisabled();
  await review
    .getByRole("button", { name: "Cancel memory", exact: true })
    .click();
  await source
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  await revealControl(page, review.getByLabel("Note", { exact: true }), "");
  await review
    .getByLabel("Note", { exact: true })
    .fill("Owner-reviewed preference: concise sourced answers");
  const expiry = new Date(Date.now() + 3600000).toISOString().slice(0, 16);
  await revealControl(
    page,
    review.getByLabel("Memory expiry (optional)", { exact: true }),
    "",
  );
  await review
    .getByLabel("Memory expiry (optional)", { exact: true })
    .fill(expiry);
  await review
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(2);
  v = await read();
  const note = v.memories.find(
    (m: { text: string }) =>
      m.text === "Owner-reviewed preference: concise sourced answers",
  );
  expect(note.source).toContain(`id=${message.id}`);
  expect(note.source).toContain(
    `sha256=${createHash("sha256").update(message.text).digest("hex")}`,
  );
  expect(note.expiresAt).toBeGreaterThan(0);
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: "Explain my preferences",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  let q = v.aiRequests.at(-1);
  expect(JSON.parse(q.context).memories).toBeDefined();
  expect(q.context).toContain(note.text);
  await command({ action: "ai-start", id: q.id, consentDigest: q.digest });
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  await page.reload();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const request = page.locator(".ai-request").last();
  await request
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const answerReview = request.getByRole("region", {
    name: "Chat memory review",
  });
  await revealControl(
    page,
    answerReview.getByLabel("Note", { exact: true }),
    "",
  );
  await answerReview
    .getByLabel("Note", { exact: true })
    .fill("Owner interpretation of the fixture answer");
  await answerReview
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(3);
  v = await read();
  q = v.aiRequests.at(-1);
  expect(v.memories.at(-1).source).toContain("kind=answer");
  expect(v.memories.at(-1).source).toContain(
    `sha256=${createHash("sha256").update(q.answer).digest("hex")}`,
  );
  await command({ action: "memory-delete", id: v.memories[0].id });
  await stop();
  await start();
  await login();
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    ...(await fixtureProviderSelection("xai")),
    text: "Research",
  });
  v = await read();
  const revision = v.revision;
  await expect(
    privateCLI(["agents", "--request-stdin"], {
      action: "memory-chat-save",
      profileId: "news-researcher",
      revision,
      id: message.id,
      source: "message",
      text: "Foreign attempt",
    }),
  ).rejects.toThrow();
  expect((await read()).revision).toBe(revision);
  await expect(
    privateCLI(["agents", "--request-stdin"], {
      action: "memory-chat-save",
      profileId,
      revision: revision - 1,
      id: message.id,
      source: "message",
      text: "Stale attempt",
    }),
  ).rejects.toThrow();
  v = await command({
    action: "memory-chat-save",
    id: message.id,
    source: "message",
    text: "CLI-reviewed preference",
  });
  expect(v.memories.at(-1).source).toBe(note.source);
  v = await command({
    action: "memory-put",
    id: note.id,
    text: "Corrected owner preference",
    source: note.source,
    expiresAt: note.expiresAt,
  });
  expect(
    v.memories.find((m: { id: string }) => m.id === note.id).history[0].text,
  ).toBe(note.text);
  await page.goto(url + "/#agents/memory");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByText("Corrected owner preference", { exact: true }),
  ).toBeVisible();
  const memory = page
    .locator(".memory-card")
    .filter({ hasText: "CLI-reviewed preference" });
  await memory.getByRole("button", { name: "Forget", exact: true }).click();
  await expect(memory).toHaveCount(0);
  v = await read();
  expect(v.messages[0]).toEqual(message);
  expect(v.aiRequests).toHaveLength(1);
  expect(v.tasks).toHaveLength(0);
  expect(v.runs).toHaveLength(0);
  expect(v.notices).toHaveLength(0);
  expect(v.providerCallsEnabled).toBe(true);
  expect(v.financialExecution).toBe(false);
  expect(v.aiFixtureAvailable).toBe(false);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.memories).toHaveLength(0);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await captureScreenshot(page, {
    path: `.evidence/chat-memory/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("profile memory recall shares read-only UI and CLI search, source inspection and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
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
  const login = async () => {
    const c = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
    c.stdin!.end(fixturePassword + "\n");
    expect((await once(c, "exit"))[0]).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  const recall = (query = "", id?: string) =>
    privateCLI([
      "agents",
      "--profile",
      profileId,
      "--memory-search",
      query,
      ...(id ? ["--memory-source", id] : []),
    ]);
  let v = await command({
    action: "session-create",
    name: "Source conversation",
  });
  const sessionId = v.sessions[0].id;
  v = await command({
    action: "message-add",
    id: sessionId,
    text: "Original retained claim, still unverified",
  });
  const message = v.messages[0];
  v = await command({
    action: "memory-chat-save",
    id: message.id,
    source: "message",
    text: "Edited concise preference",
  });
  const note = v.memories[0];
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: "Explain the retained claim",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const request = v.aiRequests[0];
  await command({
    action: "ai-start",
    id: request.id,
    consentDigest: request.digest,
  });
  await expect
    .poll(async () => (await read()).aiRequests[0]?.state)
    .toBe("completed");
  const answer = (await read()).aiRequests[0];
  v = await command({
    action: "memory-chat-save",
    id: answer.id,
    source: "answer",
    text: "Answer evidence preference",
  });
  const answerNote = v.memories[1];
  await command({
    action: "memory-put",
    text: "Expired calendar preference",
    source: "https://never-fetch.invalid/calendar",
    expiresAt: 1,
  });
  await command({
    action: "memory-put",
    text: "Invalid reference note",
    source: "chat-memory-v1 bogus",
  });
  await command({
    action: "memory-put",
    text: "Changed reference note",
    source: note.source.replace(
      /sha256=[0-9a-f]{64}/,
      "sha256=" + "0".repeat(64),
    ),
  });
  await command({ action: "session-create", name: "Other conversation" });
  const external: string[] = [];
  const fixtureOrigins = new Set([new URL(url).origin]);
  page.on("request", (r) => {
    if (!fixtureOrigins.has(new URL(r.url()).origin)) external.push(r.url());
  });
  await page.goto(url + "/#agents/chat");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Other conversation", exact: true })
    .click();
  await revealControl(
    page,
    page.getByRole("searchbox", {
      name: "Search conversations",
      exact: true,
      includeHidden: true,
    }),
  );
  await page
    .getByRole("searchbox", {
      name: "Search conversations",
      exact: true,
      includeHidden: true,
    })
    .fill("stale selection");
  await page.evaluate(() => {
    window.location.hash = "agents/memory";
  });
  const search = async (q: string) => {
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("Trading Analyst");
    await revealControl(
      page,
      page.getByRole("searchbox", { name: "Search", exact: true }),
    );
    await page.getByRole("searchbox", { name: "Search", exact: true }).fill(q);
    await page.getByRole("button", { name: "Search", exact: true }).click();
    const result = await recall(q);
    await expect(page.locator("article[aria-label^='Memory ']")).toHaveCount(
      result.memories.length,
    );
    return result;
  };
  const stable = await read();
  const result = await search("  CONCISE  ");
  expect(result.memories.map((m: { id: string }) => m.id)).toEqual([note.id]);
  await page.getByRole("button", { name: "Source", exact: true }).click();
  const inspection = page.getByRole("region", {
    name: "Memory source",
    exact: true,
  });
  await expect(inspection).toContainText(
    "Matched retained text · Claims unverified",
  );
  await expect(inspection).toContainText(message.text);
  const source = (await recall("", note.id)).source;
  expect(source.sha256).toBe(
    createHash("sha256").update(message.text).digest("hex"),
  );
  expect(source.text).toBe(message.text);
  expect(source.kind).toBe("message");
  await inspection
    .getByRole("button", { name: "Open Chat", exact: true })
    .click();
  await expect(
    page.getByRole("searchbox", {
      name: "Search conversations",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveValue("");
  await expect(
    page.getByRole("heading", { name: "Source conversation", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".local-messages")).toContainText(message.text);
  expect(await read()).toEqual(stable);
  await expect(
    privateCLI([
      "agents",
      "--profile",
      "news-researcher",
      "--memory-source",
      note.id,
    ]),
  ).rejects.toThrow();
  await expect(
    privateCLI([
      "agents",
      "--profile",
      profileId,
      "--memory-search",
      "x",
      "--search",
      "y",
    ]),
  ).rejects.toThrow();
  await expect(recall("x".repeat(201))).rejects.toThrow();
  await page.evaluate(() => {
    window.location.hash = "agents/memory";
  });
  await search("Edited concise preference");
  v = await command({
    action: "memory-put",
    id: note.id,
    text: "Corrected source preference",
    source: note.source,
  });
  await search("Edited concise preference");
  await expect(
    page.getByText("No matching memories.", { exact: true }),
  ).toBeVisible();
  await search("Corrected source preference");
  await page
    .locator(".memory-card > details > summary")
    .filter({ hasText: "History" })
    .click();
  await expect(page.locator("article[aria-label^='Memory ']")).toContainText(
    "Edited concise preference",
  );
  await page.getByRole("button", { name: "Source", exact: true }).click();
  await expect(inspection).toContainText(message.text);
  expect((await recall("", note.id)).source).toEqual(source);
  for (const [query, status] of [
    ["Expired calendar", "Free-form source"],
    ["Invalid reference", "Invalid source reference"],
    ["Changed reference", "Recorded source text changed"],
  ]) {
    await search(query);
    await page.getByRole("button", { name: "Source", exact: true }).click();
    await expect(inspection).toContainText(status);
    await expect(
      inspection.getByRole("button", {
        name: "Open Chat",
        exact: true,
      }),
    ).toHaveCount(0);
    if (query.startsWith("Expired"))
      await expect(
        page.locator("article[aria-label^='Memory ']"),
      ).toContainText("Expired");
  }
  await search("Answer evidence");
  await page.getByRole("button", { name: "Source", exact: true }).click();
  await expect(inspection).toContainText(answer.answer);
  expect((await recall("", answerNote.id)).source.kind).toBe("answer");
  const beforeRestart = await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Updated persona",
    ...(await fixtureProviderSelection("xai")),
  });
  await stop();
  await start();
  fixtureOrigins.add(new URL(url).origin);
  await login();
  await page.goto(url + "/#agents/memory");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await search("Answer evidence");
  await page.getByRole("button", { name: "Source", exact: true }).click();
  await expect(inspection).toContainText(answer.answer);
  expect((await recall("", note.id)).source).toEqual(source);
  expect((await read()).aiRequests).toEqual(beforeRestart.aiRequests);
  expect((await read()).providerCallsEnabled).toBe(true);
  expect((await read()).financialExecution).toBe(false);
  await captureScreenshot(page, {
    path: `.evidence/memory-recall/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await search("Corrected source preference");
  let release!: () => void, began!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const requested = new Promise<void>((resolve) => {
    began = resolve;
  });
  const intercept = async (route: import("@playwright/test").Route) => {
    if (
      new URL(route.request().url()).searchParams.get("memory-source") !==
      note.id
    )
      return route.continue();
    const response = await route.fetch();
    began();
    await held;
    await route.fulfill({ response });
  };
  await page.route("**/api/v1/agents?**", intercept);
  try {
    await page.getByRole("button", { name: "Source", exact: true }).click();
    await requested;
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: "News Researcher" });
    await expect(
      page.getByRole("searchbox", { name: "Search", exact: true }),
    ).toHaveValue("");
    release();
    await expect(
      page.getByText("No notes in this profile.", { exact: true }),
    ).toBeVisible();
    await expect(inspection).toHaveCount(0);
    await expect(page.locator("article[aria-label^='Memory ']")).toHaveCount(0);
  } finally {
    release();
    await page.unroute("**/api/v1/agents?**", intercept);
  }
  v = await command({ action: "memory-delete", id: note.id });
  expect((await recall("Corrected source preference")).memories).toHaveLength(
    0,
  );
  await expect(recall("", note.id)).rejects.toThrow();
  expect(v.messages[0]).toEqual(message);
  expect(v.aiRequests).toEqual(beforeRestart.aiRequests);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
});

test("manual AI memory selection shares exact UI/CLI disclosure, consent and restart isolation", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const origins = new Set([new URL(url).origin]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
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
  const login = async () => {
    const proc = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    let error = "";
    proc.stderr!.on("data", (b) => (error += b));
    proc.stdin!.end(fixturePassword + "\n");
    const [code] = await once(proc, "exit");
    expect(code, error).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  let v = await command({
    action: "session-create",
    name: "Selection conversation",
  });
  const sessionId = v.sessions[0].id;
  v = await command({
    action: "message-add",
    id: sessionId,
    text: "Conversation remains disclosed",
  });
  v = await command({
    action: "memory-put",
    text: "Use concise answers",
    source: "owner-source-one",
    expiresAt: Math.floor(Date.now() / 1000) + 3600,
  });
  const note = v.memories[0];
  v = await command({
    action: "memory-put",
    text: "Private second preference",
    source: "owner-source-two",
  });
  const second = v.memories[1];
  await command({
    action: "memory-put",
    text: "Expired note",
    source: "expired",
    expiresAt: 1,
  });
  await command({
    action: "memory-put",
    profileId: "news-researcher",
    text: "Foreign private preference",
    source: "foreign",
  });
  await page.goto(url + "/#agents/chat");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Selection conversation", exact: true })
    .click();
  const review = page.getByRole("region", {
    name: "Optional AI request review",
    exact: true,
  });
  const prepare = async (mode: string, prompt: string) => {
    await revealControl(
      page,
      review.getByRole("combobox", {
        name: "Memory",
        exact: true,
        includeHidden: true,
      }),
    );
    await review
      .getByRole("combobox", {
        name: "Memory",
        exact: true,
        includeHidden: true,
      })
      .selectOption(mode);
    await revealControl(
      page,
      review.getByLabel("Message", { exact: true }),
      "",
    );
    await review.getByLabel("Message", { exact: true }).fill(prompt);
    await exactReplyReview(page);
    await review.getByRole("button", { name: "Send", exact: true }).click();
    await expect
      .poll(async () =>
        (await read()).aiRequests.some(
          (q: { prompt: string }) => q.prompt === prompt,
        ),
      )
      .toBe(true);
    const q = (await read()).aiRequests.find(
      (q: { prompt: string }) => q.prompt === prompt,
    );
    expect(q.state).toBe("prepared");
    expect(q.heldMicrousd).toBe(0);
    const card = review.locator("article.ai-request").filter({
      has: page.getByRole("heading", { name: prompt, exact: true }),
    });
    await card.getByText("Details", { exact: true }).click();
    await expect(card.locator("pre").first()).toHaveText(q.disclosure);
    const wire = JSON.parse(q.disclosure);
    expect(q.disclosure).toContain("Conversation remains disclosed");
    expect(wire).toBeTruthy();
    return { q, card, context: JSON.parse(q.context) };
  };
  const all = await prepare("all", "Review every active note");
  expect(all.context.version).toBe("manual-ai-memory-v1");
  expect(
    all.context.context.memories.map((m: { id: string }) => m.id).sort(),
  ).toEqual([note.id, second.id].sort());
  expect(all.q.context).not.toContain("Expired note");
  expect(all.q.context).not.toContain("Foreign private preference");
  const none = await prepare("none", "Review without saved notes");
  expect(none.context.context.memories).toEqual([]);
  expect(none.q.context).not.toContain(note.text);
  expect(none.q.context).not.toContain(second.text);
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Memory", exact: true, includeHidden: true })
    .selectOption("selected");
  await expect(
    review.getByRole("button", { name: "Send", exact: true }),
  ).toBeDisabled();
  await expect(
    review.getByRole("checkbox", {
      name: "Expired note (expired)",
      exact: true,
    }),
  ).toBeDisabled();
  await expect(
    review.getByText("Foreign private preference", { exact: true }),
  ).toHaveCount(0);
  await revealControl(
    page,
    review.getByRole("checkbox", { name: note.text, exact: true }),
  );
  await review.getByRole("checkbox", { name: note.text, exact: true }).check();
  const selected = await prepare("selected", "Review only concise preference");
  expect(selected.context.memorySelection).toEqual({
    mode: "selected",
    ids: [note.id],
  });
  expect(selected.context.context.memories).toEqual([
    {
      id: note.id,
      text: note.text,
      source: note.source,
      updatedAt: note.updatedAt,
      expiresAt: note.expiresAt,
    },
  ]);
  expect(selected.q.context).not.toContain(second.text);
  await expect(
    selected.card.getByRole("button", {
      name: "Run preview",
      exact: true,
    }),
  ).toBeDisabled();
  const beforeConsent = await read();
  expect(
    beforeConsent.aiRequests.every(
      (q: { state: string }) => q.state === "prepared",
    ),
  ).toBe(true);
  // CLI uses the same resolver and canonical ordering, not a separate UI policy.
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: selected.q.prompt,
    memorySelection: { mode: "selected", ids: [note.id] },
    limits: selected.q.limits,
  });
  expect(v.aiRequests.at(-1).context).toBe(selected.q.context);
  expect(v.aiRequests.at(-1).disclosure).toBe(selected.q.disclosure);
  const stable = await read();
  await expect(
    command({
      action: "ai-prepare",
      id: sessionId,
      text: "Foreign selection",
      memorySelection: {
        mode: "selected",
        ids: [
          (await privateCLI(["agents", "--profile", "news-researcher"]))
            .memories[0].id,
        ],
      },
      limits: selected.q.limits,
    }),
  ).rejects.toThrow();
  expect(await read()).toEqual(stable);
  // Correct a selected note through CLI. Frozen disclosure stays unchanged;
  // stale consent refuses atomically rather than silently sending the correction.
  await command({
    action: "memory-put",
    id: note.id,
    text: "Owner corrected concise preference",
    source: note.source,
    expiresAt: note.expiresAt,
  });
  const corrected = await read();
  await expect(
    command({
      action: "ai-start",
      id: selected.q.id,
      consentDigest: selected.q.digest,
    }),
  ).rejects.toThrow();
  expect(await read()).toEqual(corrected);
  // A fresh explicit UI consent is the only execution in this journey.
  await revealControl(
    page,
    review.getByRole("checkbox", {
      name: "Owner corrected concise preference",
      exact: true,
      includeHidden: true,
    }),
  );
  await expect(
    review.getByRole("checkbox", {
      name: "Owner corrected concise preference",
      exact: true,
    }),
  ).toBeVisible();
  const fresh = await prepare("selected", "Review corrected selection");
  expect(fresh.context.context.memories[0].text).toBe(
    "Owner corrected concise preference",
  );
  await revealControl(
    page,
    fresh.card.getByRole("checkbox", {
      name: "I reviewed this context and these limits",
      exact: true,
    }),
  );
  await fresh.card
    .getByRole("checkbox", {
      name: "I reviewed this context and these limits",
      exact: true,
    })
    .check();
  await fresh.card
    .getByRole("button", { name: "Run preview", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await read()).aiRequests.find(
          (q: { id: string }) => q.id === fresh.q.id,
        ).state,
    )
    .toBe("completed");
  v = await read();
  const beforeRestart = v.aiRequests;
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: v.profiles.find((p: { id: string }) => p.id === profileId).persona,
    provider: "openai",
  });
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await login();
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
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Selection conversation", exact: true })
    .click();
  expect((await read()).aiRequests).toEqual(beforeRestart);
  expect((await read()).providerCallsEnabled).toBe(true);
  expect((await read()).financialExecution).toBe(false);
  await expect(
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveValue("relevant");
  await expect(
    command({
      action: "ai-start",
      id: none.q.id,
      consentDigest: none.q.digest,
    }),
  ).rejects.toThrow();
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Memory", exact: true, includeHidden: true })
    .selectOption("selected");
  await revealControl(
    page,
    review.getByRole("checkbox", {
      name: "Owner corrected concise preference",
      exact: true,
    }),
  );
  await review
    .getByRole("checkbox", {
      name: "Owner corrected concise preference",
      exact: true,
    })
    .check();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await expect(
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveCount(0);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Selection conversation", exact: true })
    .click();
  await expect(
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveValue("relevant");
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Memory", exact: true, includeHidden: true })
    .selectOption("selected");
  await expect(
    review.getByRole("checkbox", {
      name: "Owner corrected concise preference",
      exact: true,
    }),
  ).not.toBeChecked();
  await captureScreenshot(page, {
    path: `.evidence/ai-memory-selection/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
});

test("saved AI request draft shares editable UI/CLI settings, fresh consent and restart isolation", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const origins = new Set([new URL(url).origin]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
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
  const login = async () => {
    const p = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
    p.stdin!.end(fixturePassword + "\n");
    expect((await once(p, "exit"))[0]).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  let v = await command({
    action: "session-create",
    name: "Saved draft conversation",
  });
  const sessionId = v.sessions[0].id;
  const limits = {
    maxInputTokens: 24000,
    maxOutputTokens: 512,
    deadlineSeconds: 4,
    budgetMicrousd: 28000,
  };
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: "Saved legacy question",
    limits,
  });
  const legacy = v.aiRequests[0];
  v = await command({
    action: "memory-put",
    text: "Original selected preference",
    source: "owner",
  });
  const note = v.memories[0];
  v = await command({
    action: "news-source-add",
    name: "Draft fixture news",
    source: "https://news.fased.invalid/headlines.xml",
    newsMaxAgeSeconds: 600,
  });
  const source = v.newsSources[0];
  await command({ action: "news-fetch", id: source.id });
  await expect
    .poll(async () => (await read()).newsFetches[0].state)
    .toBe("completed");
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: "Saved original question",
    memorySelection: { mode: "selected", ids: [note.id] },
    newsSourceId: source.id,
    newsMaxAgeSeconds: 300,
    limits,
  });
  const original = v.aiRequests.at(-1);
  await command({ action: "session-create", name: "Other draft conversation" });
  await command({ action: "memory-delete", id: note.id });
  await command({ action: "news-source-disable", id: source.id });
  await page.goto(url + "/#agents/chat");
  const profile = page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Saved draft conversation", exact: true })
    .click();
  const review = page.getByRole("region", {
    name: "Optional AI request review",
    exact: true,
  });
  const card = (prompt: string) =>
    review.locator("article.ai-request").filter({
      has: page.getByRole("heading", { name: prompt, exact: true }),
    });
  const sourceCard = card(original.prompt);
  await revealControl(page, sourceCard.getByRole("checkbox"));
  await sourceCard.getByRole("checkbox").check();
  const baseline = await read();
  const response = page.waitForResponse((r) => r.url().includes("&ai-draft="));
  await sourceCard
    .getByRole("button", { name: "Use as draft", exact: true })
    .click();
  const shared = await (await response).json();
  expect(
    await privateCLI([
      "agents",
      "--profile",
      profileId,
      "--ai-draft",
      original.id,
    ]),
  ).toEqual(shared);
  expect(shared.memorySelection).toEqual({ mode: "selected", ids: [note.id] });
  expect(shared.newsSourceId).toBe(source.id);
  expect(shared.newsMaxAgeSeconds).toBe(300);
  for (const name of [
    "digest",
    "context",
    "disclosure",
    "state",
    "answer",
    "cards",
    "policy",
    "admission",
    "heldMicrousd",
  ])
    expect(shared).not.toHaveProperty(name);
  await expect(
    review.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue(original.prompt);
  await expect(
    review.getByLabel("Maximum output tokens", { exact: true }),
  ).toHaveValue("512");
  await expect(
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveValue("selected");
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  );
  await expect(
    review.getByRole("checkbox", {
      name: `Unavailable note: ${note.id}`,
      exact: true,
    }),
  ).toBeChecked();
  await expect(
    review.getByRole("combobox", { name: "News source", exact: true }),
  ).toHaveValue(source.id);
  await expect(
    review.getByRole("option", {
      name: "Draft fixture news · disabled",
      exact: true,
    }),
  ).toHaveAttribute("disabled", "");
  await expect(sourceCard.getByRole("checkbox")).not.toBeChecked();
  await revealControl(
    page,
    review.getByRole("textbox", { name: "Message", exact: true }),
    "",
  );
  await review
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Transient edit only");
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "selected memory is missing",
  );
  expect(await read()).toEqual(baseline);
  await review
    .getByRole("button", { name: "Cancel draft", exact: true })
    .click();
  await expect(
    review.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue("");
  expect(await read()).toEqual(baseline);
  await sourceCard
    .getByRole("button", { name: "Use as draft", exact: true })
    .click();
  await expect(
    review.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue(original.prompt);
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Memory", exact: true, includeHidden: true })
    .selectOption("none");
  await revealControl(
    page,
    review.getByRole("combobox", { name: "News source", exact: true }),
  );
  await review
    .getByRole("combobox", { name: "News source", exact: true })
    .selectOption("");
  await revealControl(
    page,
    review.getByRole("textbox", { name: "Message", exact: true }),
    "",
  );
  await review
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("New corrected draft question");
  await revealControl(
    page,
    review.getByLabel("Maximum input tokens", { exact: true }),
    "",
  );
  await review
    .getByLabel("Maximum input tokens", { exact: true })
    .fill("12000");
  await revealControl(
    page,
    review.getByLabel("Maximum output tokens", { exact: true }),
    "",
  );
  await review.getByLabel("Maximum output tokens", { exact: true }).fill("256");
  await revealControl(
    page,
    review.getByLabel("Request deadline (seconds)", { exact: true }),
    "",
  );
  await review
    .getByLabel("Request deadline (seconds)", { exact: true })
    .fill("2");
  await revealControl(
    page,
    review.getByLabel("Spending limit ($)", { exact: true }),
    "",
  );
  await review.getByLabel("Spending limit ($)", { exact: true }).fill("0.015");
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(async () => (await read()).aiRequests.length).toBe(3);
  v = await read();
  const fresh = v.aiRequests.at(-1);
  expect(fresh.id).not.toBe(original.id);
  expect(fresh.digest).not.toBe(original.digest);
  expect(fresh.state).toBe("prepared");
  expect(fresh.heldMicrousd).toBe(0);
  expect(fresh.answer).toBe("");
  expect(fresh.limits).toEqual({
    maxInputTokens: 12000,
    maxOutputTokens: 256,
    deadlineSeconds: 2,
    budgetMicrousd: 15000,
  });
  expect(JSON.parse(fresh.context).memorySelection).toEqual({ mode: "none" });
  expect(
    v.aiRequests.find((q: { id: string }) => q.id === original.id),
  ).toEqual(original);
  const freshCard = card(fresh.prompt);
  await freshCard.getByText("Details", { exact: true }).click();
  await expect(freshCard.locator("pre").first()).toHaveText(fresh.disclosure);
  await expect(
    freshCard.getByRole("button", {
      name: "Run preview",
      exact: true,
    }),
  ).toBeDisabled();
  await revealControl(page, freshCard.getByRole("checkbox"));
  await freshCard.getByRole("checkbox").check();
  await freshCard
    .getByRole("button", { name: "Run preview", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).aiRequests.at(-1).state)
    .toBe("completed");
  await card(legacy.prompt)
    .getByRole("button", { name: "Use as draft", exact: true })
    .click();
  await expect(
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveValue("legacy");
  await revealControl(
    page,
    review.getByRole("textbox", { name: "Message", exact: true }),
    "",
  );
  await review
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Fresh legacy draft question");
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(async () => (await read()).aiRequests.length).toBe(4);
  const freshLegacy = (await read()).aiRequests.at(-1);
  expect(JSON.parse(freshLegacy.context)).not.toHaveProperty("version");
  expect(JSON.parse(freshLegacy.context)).not.toHaveProperty("memorySelection");
  const stable = await read();
  // A completed read may arrive after its component/session has disappeared.
  async function delayedRead(switchScope: () => Promise<void>) {
    let release!: () => void, arrive!: () => void;
    const gate = new Promise<void>((r) => (release = r)),
      arrived = new Promise<void>((r) => (arrive = r));
    const pattern = "**/api/v1/agents?*ai-draft=*";
    await page.route(
      pattern,
      async (route) => {
        const r = await route.fetch();
        arrive();
        await gate;
        await route.fulfill({ response: r });
      },
      { times: 1 },
    );
    const finished = page.waitForResponse((r) =>
      r.url().includes("&ai-draft="),
    );
    try {
      await card(legacy.prompt)
        .getByRole("button", { name: "Use as draft", exact: true })
        .click();
      await arrived;
      await switchScope();
    } finally {
      release();
    }
    await finished;
  }
  await delayedRead(async () => {
    await review
      .getByRole("button", { name: "Cancel draft", exact: true })
      .click();
  });
  await expect(
    review.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue("");
  await expect(
    review.getByRole("heading", { name: "Request draft", exact: true }),
  ).toHaveCount(0);
  await delayedRead(async () => {
    await openConversationList(page);
    await page
      .getByRole("button", { name: "Other draft conversation", exact: true })
      .click();
    await expect(
      page.getByRole("heading", {
        name: "Other draft conversation",
        exact: true,
      }),
    ).toBeVisible();
  });
  await expect(
    review.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue("");
  await expect(
    review.getByRole("heading", { name: "Request draft", exact: true }),
  ).toHaveCount(0);
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Saved draft conversation", exact: true })
    .click();
  await delayedRead(async () => {
    const other = page.getByLabel("Agent", { exact: true });
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: "News Researcher" });
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("News Researcher");
  });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Saved draft conversation", exact: true })
    .click();
  await expect(
    review.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue("");
  expect(await read()).toEqual(stable);
  const p = stable.profiles.find((p: { id: string }) => p.id === profileId);
  await command({
    action: "profile-update",
    name: p.name,
    text: p.persona,
    provider: "openai",
  });
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await login();
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
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Saved draft conversation", exact: true })
    .click();
  await sourceCard
    .getByRole("button", { name: "Use as draft", exact: true })
    .click();
  await expect(
    review.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue(original.prompt);
  const restored = await privateCLI([
    "agents",
    "--profile",
    profileId,
    "--ai-draft",
    original.id,
  ]);
  expect({ ...restored, revision: shared.revision }).toEqual(shared);
  await expect(
    sourceCard.getByRole("button", {
      name: "Run preview",
      exact: true,
    }),
  ).toBeDisabled();
  expect((await read()).providerCallsEnabled).toBe(true);
  expect((await read()).financialExecution).toBe(false);
  await captureScreenshot(page, {
    path: resolve(
      `.evidence/ai-request-draft/${isMobile ? "mobile" : "desktop"}.png`,
    ),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  expect(external).toEqual([]);
});

test("local agent evidence inspection shares UI/CLI sources, inert reads and restart isolation", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const origins = new Set([new URL(url).origin]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
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
  const login = async () => {
    const p = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
    p.stdin!.end(fixturePassword + "\n");
    expect((await once(p, "exit"))[0]).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  let v = await command({ action: "session-create", name: "Evidence chat" });
  const sessionId = v.sessions[0].id;
  v = await command({
    action: "message-add",
    id: sessionId,
    text: "Retained memory source",
  });
  v = await command({
    action: "memory-chat-save",
    source: "message",
    id: v.messages[0].id,
    text: "Inspect my current preference",
  });
  let note = v.memories[0];
  await command({ action: "session-create", name: "Other evidence chat" });
  v = await command({
    action: "task-create",
    name: "Evidence reminder",
    text: "Inspect saved run receipt",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  v = await command({ action: "task-run", id: v.tasks[0].id });
  const run = v.runs[0];
  v = await command({
    action: "news-source-add",
    name: "Evidence fixture news",
    source: "https://news.fased.invalid/headlines.xml",
    newsMaxAgeSeconds: 600,
  });
  const source = v.newsSources[0];
  await command({ action: "news-fetch", id: source.id });
  await expect
    .poll(async () => (await read()).newsFetches[0].state)
    .toBe("completed");
  const fetch = (await read()).newsFetches[0];
  const item = fetch.items[0];
  await command({ action: "news-source-disable", id: source.id });
  await command({
    action: "memory-put",
    profileId: "news-researcher",
    text: "FOREIGN-PRIVATE-NOTE",
    source: "foreign",
  });
  // Source generation is complete. The complete inspection journey uses normal mode.
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await login();
  const openChat = async (signIn = false) => {
    await page.goto(url + "/#agents/chat");
    if (signIn) {
      await revealControl(
        page,
        page.getByLabel("Owner password", { exact: true }),
        "",
      );
      await page
        .getByLabel("Owner password", { exact: true })
        .fill(fixturePassword);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
    }
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    const p = page.getByLabel("Agent", { exact: true });
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: "Trading Analyst" });
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("Trading Analyst");
    await openConversationList(page);
    await page
      .getByRole("button", { name: "Evidence chat", exact: true })
      .click();
  };
  await openChat(true);
  await openEvidence(page);
  const inspector = page.getByRole("region", {
    name: "Local agent evidence inspection",
    exact: true,
  });
  const choose = async (kind: string, id: string, item = "") => {
    await openEvidence(page);
    await revealControl(
      page,
      inspector.getByRole("combobox", { name: "Evidence type", exact: true }),
    );
    await inspector
      .getByRole("combobox", { name: "Evidence type", exact: true })
      .selectOption(kind);
    await revealControl(
      page,
      inspector.getByRole("combobox", { name: "Saved record", exact: true }),
    );
    await inspector
      .getByRole("combobox", { name: "Saved record", exact: true })
      .selectOption(JSON.stringify([id, item]));
  };
  const baseline = await read();
  const inspectionDraft = "Keep this draft while inspecting sources";
  await page.getByLabel("Message", { exact: true }).fill(inspectionDraft);
  const hashes: Record<string, string> = {};
  for (const q of [
    { kind: "memory", id: note.id, item: "" },
    { kind: "headline", id: fetch.id, item: item.id },
    { kind: "reminder-run", id: run.id, item: "" },
  ]) {
    await choose(q.kind, q.id, q.item);
    const response = page.waitForResponse((r) =>
      r.url().includes("&evidence="),
    );
    await inspector
      .getByRole("button", { name: "Inspect evidence", exact: true })
      .click();
    const data = await (await response).json();
    const args = [
      "agents",
      "--profile",
      profileId,
      "--evidence",
      q.kind,
      "--evidence-id",
      q.id,
    ];
    if (q.item) args.push("--evidence-item", q.item);
    const cli = await privateCLI(args);
    expect({ ...cli, observedAt: data.observedAt }).toEqual(data);
    expect(data.untrusted).toBe(true);
    expect(data.historical).toBe(q.kind !== "memory");
    expect(data.sha256).toMatch(/^[a-f0-9]{64}$/);
    hashes[q.kind] = data.sha256;
    expect(data.freshness).toBe(
      q.kind === "memory"
        ? "active"
        : q.kind === "headline"
          ? "disabled"
          : "recorded",
    );
    const card = inspector.getByRole("article", {
      name: "Inspected evidence",
      exact: true,
    });
    await expect(card).toContainText("Untrusted data");
    await card.getByText("Complete evidence response", { exact: true }).click();
    await expect(card.locator("pre")).toHaveText(JSON.stringify(data, null, 2));
    expect(JSON.stringify(data)).not.toContain("FOREIGN-PRIVATE-NOTE");
    for (const key of [
      "context",
      "disclosure",
      "connectionRef",
      "consentDigest",
      "heldMicrousd",
      "aiRequests",
    ])
      expect(data).not.toHaveProperty(key);
    if (q.kind === "memory") {
      expect(data.data.text).toBe(note.text);
      expect(data.data.provenance.text).toBe("Retained memory source");
      expect(data.data).not.toHaveProperty("history");
    }
    if (q.kind === "headline") {
      expect(data.data.headline).toEqual(item);
      expect(data.data.evidence.bodySha256).toBe(fetch.bodySha256);
    }
    if (q.kind === "reminder-run")
      expect(data.data.task.text).toBe("Inspect saved run receipt");
    await inspector
      .getByRole("button", { name: "Cancel inspection", exact: true })
      .click();
    await expect(card).toHaveCount(0);
    expect(await read()).toEqual(baseline);
    await expect(page.getByLabel("Message", { exact: true })).toHaveValue(
      inspectionDraft,
    );
  }
  await choose("memory", note.id);
  await inspector
    .getByRole("button", { name: "Inspect evidence", exact: true })
    .click();
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toContainText(note.text);
  v = await command({
    action: "memory-put",
    id: note.id,
    text: "Owner corrected current preference",
    source: note.source,
  });
  note = v.memories.find((m: { id: string }) => m.id === note.id);
  await expect(inspector.getByRole("status")).toHaveText(
    "Workspace changed. Inspect again.",
    { timeout: 10000 },
  );
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toHaveCount(0);
  await inspector
    .getByRole("button", { name: "Inspect evidence", exact: true })
    .click();
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toContainText(note.text);
  const corrected = await privateCLI([
    "agents",
    "--profile",
    profileId,
    "--evidence",
    "memory",
    "--evidence-id",
    note.id,
  ]);
  expect(corrected.sha256).not.toBe(hashes.memory);
  // Each source-writing command remains below the existing 16 KiB envelope;
  // the complete note + retained provenance exceeds the read response limit.
  v = await command({
    action: "message-add",
    id: sessionId,
    text: "<".repeat(1500),
  });
  v = await command({
    action: "memory-chat-save",
    source: "message",
    id: v.messages.find(
      (m: { role: string; text: string; sessionId: string }) =>
        m.role === "user" &&
        m.text === "<".repeat(1500) &&
        m.sessionId === sessionId,
    ).id,
    text: "<".repeat(1500),
  });
  const oversized = v.memories.find(
    (m: { text: string }) => m.text === "<".repeat(1500),
  );
  await page.reload();
  await openChat();
  await choose("memory", oversized.id);
  const beforeBound = await read();
  await inspector
    .getByRole("button", { name: "Inspect evidence", exact: true })
    .click();
  await expect(inspector.getByRole("alert")).toContainText("exceeds 16 KiB");
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toHaveCount(0);
  expect(await read()).toEqual(beforeBound);
  await inspector
    .getByRole("button", { name: "Cancel inspection", exact: true })
    .click();
  async function delayedRead(switchScope: () => Promise<void>) {
    await choose("memory", note.id);
    let release!: () => void, arrive!: () => void;
    const gate = new Promise<void>((r) => (release = r)),
      arrived = new Promise<void>((r) => (arrive = r));
    await page.route(
      "**/api/v1/agents?*evidence=*",
      async (route) => {
        const r = await route.fetch();
        arrive();
        await gate;
        await route.fulfill({ response: r });
      },
      { times: 1 },
    );
    const finished = page.waitForResponse((r) =>
      r.url().includes("&evidence="),
    );
    try {
      await inspector
        .getByRole("button", { name: "Inspect evidence", exact: true })
        .click();
      await arrived;
      await switchScope();
    } finally {
      release();
    }
    await finished;
  }
  const stable = await read();
  await delayedRead(async () => {
    await inspector
      .getByRole("button", { name: "Cancel inspection", exact: true })
      .click();
  });
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toHaveCount(0);
  await delayedRead(async () => {
    await openConversationList(page);
    await page
      .getByRole("button", { name: "Other evidence chat", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Other evidence chat", exact: true }),
    ).toBeVisible();
  });
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toHaveCount(0);
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Evidence chat", exact: true })
    .click();
  await delayedRead(async () => {
    const p = page.getByLabel("Agent", { exact: true });
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: "News Researcher" });
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("News Researcher");
  });
  await expect(inspector).toHaveCount(0);
  await openChat();
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toHaveCount(0);
  expect(await read()).toEqual(stable);
  const p = stable.profiles.find((p: { id: string }) => p.id === profileId);
  await command({
    action: "profile-update",
    name: p.name,
    text: p.persona,
    ...(await fixtureProviderSelection("xai")),
  });
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await login();
  await openChat(true);
  const restored = await privateCLI([
    "agents",
    "--profile",
    profileId,
    "--evidence",
    "memory",
    "--evidence-id",
    note.id,
  ]);
  expect(restored.sha256).toBe(corrected.sha256);
  await choose("headline", fetch.id, item.id);
  await inspector
    .getByRole("button", { name: "Inspect evidence", exact: true })
    .click();
  await expect(
    inspector.getByRole("article", { name: "Inspected evidence", exact: true }),
  ).toContainText("Historical record · disabled · Untrusted data");
  await inspector
    .getByText("Complete evidence response", { exact: true })
    .click();
  v = await read();
  expect(v.aiRequests).toEqual([]);
  expect(v.newsFetches).toHaveLength(1);
  expect(v.runs).toHaveLength(1);
  expect(v.providerCallsEnabled).toBe(true);
  expect(v.financialExecution).toBe(false);
  await captureScreenshot(page, {
    path: resolve(
      `.evidence/agent-evidence/${isMobile ? "mobile" : "desktop"}.png`,
    ),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  expect(external).toEqual([]);
});

test("reviewed AI evidence shares UI/CLI disclosure, explicit consent and scoped draft recovery", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const origins = new Set([new URL(url).origin]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
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
  const login = async () => {
    const p = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
    p.stdin!.end(fixturePassword + "\n");
    expect((await once(p, "exit"))[0]).toBe(0);
  };
  await login();
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  let v = await command({
    action: "session-create",
    name: "Evidence review chat",
  });
  const sessionId = v.sessions.find(
    (s: any) => s.name === "Evidence review chat",
  ).id;
  await command({ action: "session-create", name: "Other review chat" });
  v = await command({
    action: "memory-put",
    text: "Reviewed source preference",
    source: "owner",
  });
  const note = v.memories.find(
    (m: any) => m.text === "Reviewed source preference",
  );
  v = await command({
    action: "task-create",
    name: "Saved evidence reminder",
    text: "Inspect this completed reminder",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  v = await command({
    action: "task-run",
    id: v.tasks.find((t: any) => t.name === "Saved evidence reminder").id,
  });
  const run = v.runs[0];
  v = await command({
    action: "news-source-add",
    name: "Saved evidence news",
    source: "https://news.fased.invalid/headlines.xml",
    newsMaxAgeSeconds: 600,
  });
  const source = v.newsSources[0];
  await command({ action: "news-fetch", id: source.id });
  await expect
    .poll(async () => (await read()).newsFetches[0].state)
    .toBe("completed");
  const fetch = (await read()).newsFetches[0];
  const item = fetch.items[0];
  await command({ action: "news-source-disable", id: source.id });
  await command({
    action: "memory-put",
    profileId: "news-researcher",
    text: "FOREIGN-EVIDENCE-PRIVATE",
    source: "foreign",
  });
  const openChat = async (signIn = false) => {
    await page.goto(url + "/#agents/chat");
    if (signIn) {
      await revealControl(
        page,
        page.getByLabel("Owner password", { exact: true }),
        "",
      );
      await page
        .getByLabel("Owner password", { exact: true })
        .fill(fixturePassword);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
    }
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();
    const profile = page.getByLabel("Agent", { exact: true });
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: "Trading Analyst" });
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("Trading Analyst");
    await openConversationList(page);
    await page
      .getByRole("button", { name: "Evidence review chat", exact: true })
      .click();
  };
  await openChat();
  await openEvidence(page);
  const inspector = page.getByRole("region", {
    name: "Local agent evidence inspection",
    exact: true,
  });
  const review = page.getByRole("region", {
    name: "Optional AI request review",
    exact: true,
  });
  const selected = review.getByRole("group", {
    name: "AI evidence selection",
    exact: true,
    includeHidden: true,
  });
  const choose = async (kind: string, id: string, itemId = "") => {
    await openEvidence(page);
    await revealControl(
      page,
      inspector.getByRole("combobox", { name: "Evidence type", exact: true }),
    );
    await inspector
      .getByRole("combobox", { name: "Evidence type", exact: true })
      .selectOption(kind);
    await revealControl(
      page,
      inspector.getByRole("combobox", { name: "Saved record", exact: true }),
    );
    await inspector
      .getByRole("combobox", { name: "Saved record", exact: true })
      .selectOption(JSON.stringify([id, itemId]));
    const response = page.waitForResponse((r) =>
      r.url().includes("&evidence="),
    );
    await inspector
      .getByRole("button", { name: "Inspect evidence", exact: true })
      .click();
    return (await response).json();
  };
  const prepareUI = async () => {
    const response = page.waitForResponse(
      (r) =>
        r.request().method() === "POST" &&
        r.url().includes("/api/v1/agents") &&
        r.request().postDataJSON()?.action === "ai-prepare",
    );
    await exactReplyReview(page);
    await review.getByRole("button", { name: "Send", exact: true }).click();
    const r = await response;
    expect(r.status()).toBe(200);
    return r.json();
  };
  const sourcesBefore = await read();
  const original: any[] = [];
  for (const q of [
    { kind: "memory", id: note.id, itemId: "" },
    { kind: "headline", id: fetch.id, itemId: item.id },
    { kind: "reminder-run", id: run.id, itemId: "" },
  ]) {
    const baseline = await read();
    const data = await choose(q.kind, q.id, q.itemId);
    await openEvidence(page);
    await inspector
      .getByRole("button", { name: "Use in AI review", exact: true })
      .click();
    await expect(
      review.getByRole("textbox", { name: "Message", exact: true }),
    ).toBeFocused();
    await revealControl(page, selected);
    await expect(selected).toContainText(data.sha256);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    if (q.kind === "memory")
      await captureScreenshot(page, {
        path: resolve(
          `.evidence/ai-evidence/${isMobile ? "mobile" : "desktop"}-selected.png`,
        ),
        fullPage: true,
      });
    await revealControl(
      page,
      review.getByRole("combobox", {
        name: "Memory",
        exact: true,
        includeHidden: true,
      }),
    );
    await review
      .getByRole("combobox", {
        name: "Memory",
        exact: true,
        includeHidden: true,
      })
      .selectOption("none");
    await review.locator(".message-options > summary").click();
    await revealControl(
      page,
      review.getByRole("textbox", { name: "Message", exact: true }),
      "",
    );
    await review
      .getByRole("textbox", { name: "Message", exact: true })
      .fill("Transient inspected question");
    expect(await read()).toEqual(baseline);
    await selected
      .getByRole("button", { name: "Remove evidence", exact: true })
      .click();
    await expect(selected).toHaveCount(0);
    expect(await read()).toEqual(baseline);
    await openEvidence(page);
    await inspector
      .getByRole("button", { name: "Use in AI review", exact: true })
      .click();
    const prompt = "UI evidence " + q.kind;
    await revealControl(
      page,
      review.getByRole("textbox", { name: "Message", exact: true }),
      "",
    );
    await review
      .getByRole("textbox", { name: "Message", exact: true })
      .fill(prompt);
    const prepared = await prepareUI();
    const request = prepared.aiRequests.find((r: any) => r.prompt === prompt);
    original.push(request);
    const context = JSON.parse(request.context);
    expect(context.version).toBe("manual-ai-evidence-v1");
    expect(context.evidence.data).toEqual(data.data);
    expect(context.evidence.sha256).toBe(data.sha256);
    expect(context.evidence.historical).toBe(q.kind !== "memory");
    expect(context.evidence.untrusted).toBe(true);
    expect(context.evidence.observedAt).toBe(request.createdAt);
    expect(Buffer.byteLength(request.context)).toBeLessThanOrEqual(8000);
    expect(context.context.memorySelection.mode).toBe("none");
    expect(request.news).toBeUndefined();
    expect(request.context).not.toContain("FOREIGN-EVIDENCE-PRIVATE");
    const wire = JSON.parse(request.disclosure);
    expect(wire.tools).toEqual([]);
    expect(wire.tool_choice).toBe("none");
    expect(wire.input).toBe(request.context);
    expect(
      createHash("sha256").update(request.disclosure).digest("hex"),
    ).toHaveLength(64);
    const card = review.getByRole("article").filter({
      has: page.getByRole("heading", { name: prompt, exact: true }),
    });
    await card.getByText("Details", { exact: true }).click();
    await expect(card.locator("pre")).toHaveText(request.disclosure);
    await expect(
      card.getByRole("button", {
        name: "Run preview",
        exact: true,
      }),
    ).toBeDisabled();
    expect(
      (await read()).aiRequests.find((r: any) => r.id === request.id).state,
    ).toBe("prepared");
    await revealControl(
      page,
      card.getByRole("checkbox", {
        name: "I reviewed this context and these limits",
        exact: true,
      }),
    );
    await card
      .getByRole("checkbox", {
        name: "I reviewed this context and these limits",
        exact: true,
      })
      .check();
    await card
      .getByRole("button", { name: "Run preview", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (await read()).aiRequests.find((r: any) => r.id === request.id).state,
      )
      .toBe("completed");
    const cli = await command({
      action: "ai-prepare",
      id: sessionId,
      text: "CLI evidence " + q.kind,
      evidenceSelection: context.evidenceSelection,
      memorySelection: { mode: "none" },
      limits: request.limits,
    });
    const cliRequest = cli.aiRequests.find(
      (r: any) => r.prompt === "CLI evidence " + q.kind,
    );
    const cliContext = JSON.parse(cliRequest.context);
    expect(cliContext.evidenceSelection).toEqual(context.evidenceSelection);
    expect(cliContext.evidence.data).toEqual(context.evidence.data);
    expect(cliContext.evidence.sha256).toBe(context.evidence.sha256);
    await page.reload();
    await openChat();
  }
  const after = await read();
  expect(after.tasks).toEqual(sourcesBefore.tasks);
  expect(after.runs).toEqual(sourcesBefore.runs);
  expect(after.memories).toEqual(sourcesBefore.memories);
  expect(after.newsFetches).toEqual(sourcesBefore.newsFetches);
  const memoryRequest = original[0];
  // A later source correction is not silently substituted into a saved draft.
  await command({
    action: "memory-put",
    id: note.id,
    text: "Owner corrected evidence",
    source: "owner",
  });
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: after.profiles.find((p: any) => p.id === profileId).persona,
    provider: "openai",
    model: "gpt-6.2-sol",
  });
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await login();
  await openChat(true);
  const memoryCard = () =>
    review.getByRole("article").filter({
      has: page.getByRole("heading", {
        name: memoryRequest.prompt,
        exact: true,
      }),
    });
  const baseline = await read();
  await memoryCard()
    .getByRole("button", { name: "Use as draft", exact: true })
    .click();
  await revealControl(page, selected);
  await expect(selected).toContainText(
    JSON.parse(memoryRequest.context).evidenceSelection.sha256,
  );
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "inspected evidence changed",
  );
  expect(await read()).toEqual(baseline);
  await review
    .getByRole("button", { name: "Cancel draft", exact: true })
    .click();
  await expect(selected).toHaveCount(0);
  expect(await read()).toEqual(baseline);
  const pattern = /\/api\/v1\/agents\?.*ai-draft=/;
  for (const mode of ["cancel", "session", "profile"]) {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    let started!: () => void;
    const seen = new Promise<void>((r) => (started = r));
    let finished!: () => void;
    const delivered = new Promise<void>((r) => (finished = r));
    await page.route(pattern, async (route) => {
      const response = await route.fetch();
      started();
      await gate;
      await route.fulfill({ response });
      finished();
    });
    await memoryCard()
      .getByRole("button", { name: "Use as draft", exact: true })
      .click();
    await seen;
    if (mode === "cancel")
      await review
        .getByRole("button", { name: "Cancel draft", exact: true })
        .click();
    else if (mode === "session") {
      await openConversationList(page);
      await page
        .getByRole("button", { name: "Other review chat", exact: true })
        .click();
    } else {
      const profile = page.getByLabel("Agent", { exact: true });
      await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
      await page
        .getByLabel("Agent", { exact: true })
        .selectOption({ label: "News Researcher" });
      await expect(
        page.getByLabel("Agent", { exact: true }).locator("option:checked"),
      ).toHaveText("News Researcher");
    }
    release();
    await delivered;
    await page.unroute(pattern);
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await expect(selected).toHaveCount(0);
    await openChat();
    await expect(
      review.getByRole("textbox", { name: "Message", exact: true }),
    ).toHaveValue("");
    await expect(selected).toHaveCount(0);
    expect(await read()).toEqual(baseline);
  }
  const chooseDemo = async () => {
    // Unsupported agent defaults must not silently become Demo after navigation.
    await expect(review.getByRole("status")).toContainText(
      "Provider unavailable",
    );
    await expect(
      review.getByRole("button", { name: "Send", exact: true }),
    ).toBeDisabled();
    const provider = review.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    });
    await revealControl(page, provider);
    await provider.selectOption("");
    await expect(
      review.getByRole("combobox", {
        name: "Model",
        exact: true,
        includeHidden: true,
      }),
    ).toHaveValue("");
    await review.locator(".reply-model-menu > summary").click();
  };
  await chooseDemo();
  const current = await choose("memory", note.id);
  expect(current.sha256).not.toBe(
    JSON.parse(memoryRequest.context).evidenceSelection.sha256,
  );
  await inspector
    .getByRole("button", { name: "Use in AI review", exact: true })
    .click();
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Memory", exact: true, includeHidden: true })
    .selectOption("none");
  await revealControl(
    page,
    review.getByRole("textbox", { name: "Message", exact: true }),
    "",
  );
  await review
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Reviewed correction");
  const corrected = await prepareUI();
  const correctedRequest = corrected.aiRequests.find(
    (r: any) => r.prompt === "Reviewed correction",
  );
  expect(JSON.parse(correctedRequest.context).evidence.sha256).toBe(
    current.sha256,
  );
  const correctedCard = review.getByRole("article").filter({
    has: page.getByRole("heading", {
      name: "Reviewed correction",
      exact: true,
    }),
  });
  await expect(
    correctedCard.getByRole("button", {
      name: "Run preview",
      exact: true,
    }),
  ).toBeDisabled();
  // The 16 KiB inspector is not additional AI context capacity.
  const largeText = "<".repeat(1400);
  v = await command({ action: "memory-put", text: largeText, source: "owner" });
  const large = v.memories.find((m: any) => m.text === largeText);
  await page.reload();
  await openChat();
  await chooseDemo();
  const oversized = await choose("memory", large.id);
  expect(Buffer.byteLength(JSON.stringify(oversized))).toBeLessThan(16384);
  await inspector
    .getByRole("button", { name: "Use in AI review", exact: true })
    .click();
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Memory", exact: true, includeHidden: true })
    .selectOption("none");
  await revealControl(
    page,
    review.getByRole("textbox", { name: "Message", exact: true }),
    "",
  );
  await review
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Too much complete evidence");
  const beforeOversize = await read();
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("exceed 8000 bytes");
  expect(await read()).toEqual(beforeOversize);
  await revealControl(page, selected);
  await selected
    .getByRole("button", { name: "Remove evidence", exact: true })
    .click();
  await openEvidence(page);
  await inspector
    .getByRole("button", { name: "Cancel inspection", exact: true })
    .click();
  await command({ action: "memory-delete", id: large.id });
  await page.reload();
  await openChat();
  const final = await read();
  const retained = final.aiRequests.find((r: any) => r.id === memoryRequest.id);
  expect(retained.context).toBe(memoryRequest.context);
  expect(retained.disclosure).toBe(memoryRequest.disclosure);
  expect(final.tasks).toEqual(sourcesBefore.tasks);
  expect(final.runs).toEqual(sourcesBefore.runs);
  expect(final.newsFetches).toEqual(sourcesBefore.newsFetches);
  await captureScreenshot(page, {
    path: resolve(
      `.evidence/ai-evidence/${isMobile ? "mobile" : "desktop"}.png`,
    ),
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  expect(external).toEqual([]);
});
