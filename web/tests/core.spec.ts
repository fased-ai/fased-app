import { expect, test, type Request } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { once } from "node:events";
import { createHash, randomBytes } from "node:crypto";

test("normal Chat connects a fixture provider, reviews one reply and retains scoped history without live calls", async ({
  page,
}) => {
  test.setTimeout(75000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url + "/#settings");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  const provider = page.getByRole("region", {
    name: "OpenAI connection",
    exact: true,
  });
  await expect(provider).toBeVisible();
  await provider.getByText("Connection settings", { exact: true }).click();
  await provider
    .getByLabel("Disposable API key")
    .fill("fased-test-normal-chat-disposable-key");
  await provider
    .getByLabel("Vault passphrase")
    .fill("normal-chat-disposable-passphrase");
  await provider
    .getByRole("button", { name: "Save test connection", exact: true })
    .click();
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
    name: "AI connection",
    exact: true,
  });
  await expect(picker).toBeVisible();
  await picker.selectOption(connection.ref);
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
  await expect(
    chat.locator(".ai-request").last().getByText("prepared", { exact: true }),
  ).toBeVisible();
  let view = await privateCLI(["agents"]);
  const q = view.aiRequests[0];
  expect(q.state).toBe("prepared");
  expect(q.connectionRef).toBe(connection.ref);
  expect(q.answer).toBe("");
  const card = chat.locator(".ai-request").last();
  await expect(
    card.getByRole("button", { name: "Run development fixture", exact: true }),
  ).toBeDisabled();
  await card
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await card
    .getByRole("button", { name: "Run development fixture", exact: true })
    .click();
  await expect(card.locator(".ai-answer")).toContainText(
    "Development fixture response",
    { timeout: 12000 },
  );
  await expect(card.locator(".ai-answer")).toBeInViewport();
  await expect(
    card.getByText("Exact context disclosure", { exact: true }),
  ).toBeVisible();
  await expect(
    card.getByRole("button", { name: "Use as draft", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: `.evidence/providers-chat/${test.info().project.name}-chat.png`,
    fullPage: true,
  });
  view = await privateCLI(["agents"]);
  expect(view.aiRequests[0].state).toBe("completed");
  expect(view.providerCallsEnabled).toBe(false);
  const other = view.profiles.find((p: any) => p.id !== view.profileId).id;
  const isolated = await privateCLI(["agents", "--profile", other]);
  expect(isolated.aiRequests).toHaveLength(0);
  const port = new URL(url).port;
  await stop();
  await start(true, `127.0.0.1:${port}`);
  await page.reload();
  await expect(chat.locator(".ai-answer")).toContainText(
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
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  await page.getByRole("button", { name: "Start chat", exact: true }).click();
  const chat = page.getByRole("region", { name: "Optional AI request review" });
  await chat
    .getByRole("combobox", { name: "AI connection", exact: true })
    .selectOption(c.ref);
  const input = chat.getByLabel("Message", { exact: true });
  await input.fill("Keep this question when the connection locks");
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "lock",
    ref: c.ref,
  });
  await chat.getByRole("button", { name: "Send", exact: true }).click();
  await expect(chat.getByRole("alert")).toContainText("locked");
  await expect(input).toHaveValue(
    "Keep this question when the connection locks",
  );
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await chat
    .getByRole("combobox", { name: "AI connection", exact: true })
    .selectOption("");
  await chat.getByRole("button", { name: "Send", exact: true }).click();
  await chat.getByRole("button", { name: "Stop", exact: true }).click();
  await expect(chat.getByText("cancelled", { exact: true })).toBeVisible();
  const view = await privateCLI(["agents"]);
  expect(view.aiRequests[0].state).toBe("cancelled");
  expect(view.aiRequests[0].connectionRef || "").toBe("");
  expect(view.aiRequests[0].spentMicrousd).toBe(0);
});

async function openConversationList(page: import("@playwright/test").Page) {
  const toggle = page.getByRole("button", {
    name: "Conversations",
    exact: true,
  });
  if (
    (await toggle.isVisible()) &&
    (await toggle.getAttribute("aria-expanded")) !== "true"
  )
    await toggle.click();
}

async function revealControl(
  page: import("@playwright/test").Page,
  control: import("@playwright/test").Locator,
  hint = "",
) {
  if (await control.isVisible()) return;
  if ((await control.count()) === 0) {
    if (hint.includes("Conversation name")) {
      const toggle = page.getByRole("button", {
        name: "Conversations",
        exact: true,
      });
      if (
        (await toggle.isVisible()) &&
        !(await page
          .getByRole("button", { name: "New conversation", exact: true })
          .isVisible())
      )
        await toggle.click();
      await page
        .getByRole("button", { name: "New conversation", exact: true })
        .click();
    } else if (hint.includes("Memory note"))
      await page
        .getByRole("button", { name: "New memory", exact: true })
        .click();
    else if (hint.includes("Task name"))
      await page.getByRole("button", { name: "New task", exact: true }).click();
  }
  // Let navigation attach its control before choosing the owning disclosure.
  await expect(control).toBeAttached();
  if (
    !(await control.isVisible()) &&
    (await control
      .locator("xpath=ancestor::aside[@aria-label='Conversations']")
      .count())
  )
    await openConversationList(page);
  // Keep click targets stable as disclosures open; outermost first.
  const disclosures = control.locator("xpath=ancestor::details");
  for (let i = 0; i < (await disclosures.count()); i++) {
    const section = disclosures.nth(i);
    if ((await section.getAttribute("open")) === null)
      await section.locator(":scope > summary").click();
  }
  await expect(control).toBeVisible();
}

async function openUpdateSettings(page: import("@playwright/test").Page) {
  const section = page.locator("details.update-settings");
  await expect(section).toBeVisible();
  if ((await section.getAttribute("open")) === null)
    await section.locator(":scope > summary").click();
}

async function openAISchedules(page: import("@playwright/test").Page) {
  const pane = page.locator(".scheduled-ai-disclosure");
  if ((await pane.getAttribute("open")) === null)
    await pane.locator(":scope > summary").click();
}

const exec = promisify(execFile);
const binary = resolve(
  process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
);
const fixturePassword = "generated-browser-fixture-password";
let dir: string;
let child: ChildProcess | undefined;
let url: string;
let output = "";

async function start(
  aiFixture = false,
  listen = "127.0.0.1:0",
  location = "local",
) {
  output = "";
  child = spawn(binary, [
    "serve",
    ...(aiFixture ? ["--ai-fixture"] : []),
    ...(location === "remote" ? ["--instance-location", "remote"] : []),
    "--listen",
    listen,
    "--data-dir",
    dir,
  ]);
  child.stdout!.on("data", (data) => (output += String(data)));
  child.stderr!.on("data", (data) => (output += String(data)));
  try {
    await expect.poll(() => output.includes('"event":"listening"')).toBe(true);
  } catch (error) {
    await writeFile(
      resolve(
        ".evidence/core-browser-fixtures",
        `${basename(dir)}-startup.log`,
      ),
      output,
    );
    throw Error(`Fixture core startup failed:\n${output}\n${String(error)}`);
  }
  url = JSON.parse(output.trim().split("\n")[0]).url;
}
async function stop() {
  if (!child || child.exitCode !== null) return;
  const done = once(child, "exit");
  child.kill("SIGTERM");
  await done;
  child = undefined;
}
async function privateCLI(args: string[], input?: unknown) {
  if (input === undefined)
    return JSON.parse(
      (await exec(binary, [...args, "--data-dir", dir])).stdout,
    );
  const child = spawn(binary, [...args, "--data-dir", dir]);
  let output = "",
    error = "";
  child.stdout!.on("data", (b) => (output += b));
  child.stderr!.on("data", (b) => (error += b));
  child.stdin!.end(JSON.stringify(input));
  const [code] = await once(child, "exit");
  if (code !== 0) throw new Error(error);
  return JSON.parse(output);
}
test.beforeEach(async () => {
  const root = resolve(".evidence/core-browser-fixtures");
  await mkdir(root, { recursive: true, mode: 0o700 });
  // Disposable, reconstructible state; keep IPC independent of checkout length.
  dir = await mkdtemp(join(tmpdir(), "fcu-"));
  await start();
});
test.afterEach(async () => {
  try {
    await stop();
  } finally {
    if (dir) await rm(dir, { recursive: true, force: true });
  }
});

test("reviewed playbooks keep versions and fresh consent through one fixture digest, CLI, restart and memory", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(12000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");

  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  const profileId = "news-researcher";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await page.getByRole("button", { name: "playbooks", exact: true }).click();
  const panel = page.getByRole("region", {
    name: "Agent playbooks",
    exact: true,
  });
  await panel
    .getByRole("button", { name: "Add fixture news source", exact: true })
    .click();
  await expect.poll(async () => (await read()).newsSources.length).toBe(1);
  await panel
    .getByRole("button", { name: "Fetch fixture headlines", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).newsFetches[0]?.state)
    .toBe("completed");
  await expect(
    panel.getByRole("button", { name: "Fetch fixture headlines", exact: true }),
  ).toBeEnabled();
  let v = await read();
  await panel
    .getByRole("button", { name: "New playbook", exact: true })
    .click();
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Playbook name", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Playbook name", exact: true })
    .fill("Fixture morning digest");
  await panel
    .getByRole("button", { name: "Save playbook", exact: true })
    .click();
  await expect.poll(async () => (await read()).playbooks.length).toBe(1);
  v = await read();
  const id = v.playbooks[0].id;
  const original = v.playbooks[0].versions[0];
  expect(v.tasks).toHaveLength(0);
  expect(v.aiRequests).toHaveLength(0);
  expect(v.newsFetches).toHaveLength(1);
  await panel
    .getByRole("button", { name: "Edit: Fixture morning digest", exact: true })
    .click();
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Digest prompt", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Digest prompt", exact: true })
    .fill(
      "Summarize the fixture headlines and explain uncertainty without trading advice.",
    );
  await panel
    .getByRole("button", { name: "Save new version", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).playbooks[0].versions.length)
    .toBe(2);
  v = await read();
  expect(v.playbooks[0].versions[0]).toEqual(original);
  const d = await privateCLI([
    "agents",
    "--profile",
    profileId,
    "--playbook-draft",
    id,
    "--playbook-version",
    "1",
  ]);
  expect(d.selected).toEqual(original);
  expect(d.command.action).toBe("playbook-run-review");
  await expect(
    privateCLI([
      "agents",
      "--profile",
      "trading-analyst",
      "--playbook-draft",
      id,
      "--playbook-version",
      "1",
    ]),
  ).rejects.toThrow();
  await expect(
    privateCLI([
      "agents",
      "--profile",
      profileId,
      "--playbook-draft",
      id,
      "--playbook-version",
      "1",
      "--search",
      "",
    ]),
  ).rejects.toThrow();
  const beforeRestart = await read();
  const listen = new URL(url).host;
  await stop();
  await start(true, listen);
  await page.reload();
  await page.getByRole("button", { name: "playbooks", exact: true }).click();
  await expect(
    panel.getByRole("button", {
      name: "Review: Fixture morning digest",
      exact: true,
    }),
  ).toBeVisible();
  expect((await read()).playbooks).toEqual(beforeRestart.playbooks);
  await panel
    .getByRole("button", {
      name: "Review: Fixture morning digest",
      exact: true,
    })
    .click();
  await panel
    .getByRole("button", { name: "Create digest conversation", exact: true })
    .click();
  await expect.poll(async () => (await read()).sessions.length).toBe(1);
  const sessionId = (await read()).sessions[0].id;
  await revealControl(
    page,
    panel.getByRole("combobox", { name: "Digest conversation", exact: true }),
  );
  await panel
    .getByRole("combobox", { name: "Digest conversation", exact: true })
    .selectOption(sessionId);
  const beforeReview = await read();
  await panel
    .getByRole("button", { name: "Prepare run review", exact: true })
    .click();
  await expect(
    panel.getByText("Controlled paper-news headline", { exact: true }),
  ).toBeVisible();
  const consent = panel.getByRole("checkbox", {
    name: "Approve this context, source policy and one fixture run",
    exact: true,
  });
  const runButton = panel.getByRole("button", {
    name: "Run fixture digest once",
    exact: true,
  });
  await expect(runButton).toBeDisabled();
  expect(await read()).toEqual(beforeReview);
  await panel
    .getByRole("button", { name: "Cancel run review", exact: true })
    .click();
  expect(await read()).toEqual(beforeReview);
  await panel
    .getByRole("button", {
      name: "Review: Fixture morning digest",
      exact: true,
    })
    .click();
  await revealControl(
    page,
    panel.getByRole("combobox", { name: "Digest conversation", exact: true }),
  );
  await panel
    .getByRole("combobox", { name: "Digest conversation", exact: true })
    .selectOption(sessionId);
  await panel
    .getByRole("button", { name: "Prepare run review", exact: true })
    .click();
  await expect(consent).toBeVisible();
  await command({
    action: "memory-put",
    text: "Fixture context correction",
    source: "owner",
  });
  await expect(panel.getByRole("alert")).toContainText("records changed");
  await expect(runButton).toBeDisabled();
  await panel
    .getByRole("button", { name: "Prepare run review", exact: true })
    .click();
  await expect(panel.getByRole("alert")).toHaveCount(0);
  await revealControl(page, consent);
  await consent.check();
  await expect(runButton).toBeEnabled();
  await page.screenshot({
    path: resolve(
      `.evidence/playbook-review-${isMobile ? "mobile" : "desktop"}.png`,
    ),
    fullPage: true,
  });
  await runButton.click();
  await expect
    .poll(async () => (await read()).tasks[0]?.state)
    .toBe("completed");
  const result = panel.getByRole("article", {
    name: "Digest run Fixture morning digest",
    exact: true,
  });
  await expect(result).toContainText("completed · 1/1 runs");
  await expect(result).toContainText("Development fixture response");
  v = await read();
  expect(v.tasks).toHaveLength(1);
  expect(v.tasks[0].playbook).toMatchObject({ id, version: 2 });
  expect(v.runs).toHaveLength(1);
  expect(v.notices).toHaveLength(1);
  expect(v.aiRequests).toHaveLength(1);
  expect(v.aiRequests[0].context).toContain("Controlled paper-news headline");
  expect(v.aiRequests[0].news.fetchId).not.toBe(v.newsFetches[0].id);
  await expect(
    command({ action: "task-run", id: v.tasks[0].id }),
  ).rejects.toThrow();
  const savedResult = {
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  };
  await result
    .getByRole("button", { name: "Discuss digest", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  v = await read();
  const message = v.messages.at(-1);
  expect(message.text).toContain(savedResult.runs[0].news.fetchId);
  const article = page
    .locator(".local-messages article")
    .filter({ hasText: message.text });
  await article
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const memory = article.getByRole("region", {
    name: "Chat memory review",
    exact: true,
  });
  await revealControl(
    page,
    memory.getByRole("textbox", { name: "Reviewed memory note", exact: true }),
    "",
  );
  await memory
    .getByRole("textbox", { name: "Reviewed memory note", exact: true })
    .fill("Fixture digest sources must be checked before reuse.");
  expect((await read()).memories).toHaveLength(1);
  await memory
    .getByRole("button", { name: "Save reviewed memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(2);
  v = await read();
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(savedResult);
  const foreign = await privateCLI(["agents", "--profile", "trading-analyst"]);
  expect(foreign.playbooks).toHaveLength(0);
  expect(foreign.runs).toHaveLength(0);
  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "playbooks", exact: true }).click();
  await stop();
  await start(false, listen);
  await page.reload();
  await page.getByRole("button", { name: "playbooks", exact: true }).click();
  await panel
    .getByRole("button", {
      name: "Review: Fixture morning digest",
      exact: true,
    })
    .click();
  await revealControl(
    page,
    panel.getByRole("combobox", { name: "Digest conversation", exact: true }),
  );
  await panel
    .getByRole("combobox", { name: "Digest conversation", exact: true })
    .selectOption(sessionId);
  await panel
    .getByRole("button", { name: "Prepare run review", exact: true })
    .click();
  await expect(consent).toBeDisabled();
  await expect(runButton).toBeDisabled();
  expect((await read()).runs).toHaveLength(1);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
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
    if (new URL(request.url()).origin !== new URL(url).origin)
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
    page.getByRole("textbox", { name: "Memory note", exact: true }),
  ).toHaveValue(original.text);
  expect(await read()).toEqual(before);
  expect(writes).toHaveLength(0);
  await page.screenshot({
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
    provider: "xai",
    model: "preference-only",
  });
  const cliRestore = await draft(original.id, 1);
  expect(cliRestore.selected.text).toBe("Current owner preference");
  v = await privateCLI(["agents", "--request-stdin"], cliRestore.command);
  expect(v.memories[0].text).toBe("Current owner preference");
  expect(v.memories[0].history).toHaveLength(4);
  // Explicitly refresh after the CLI write; do not race the five-second poll.
  await page
    .getByRole("button", { name: "Search memory", exact: true })
    .click();
  await expect(card(original.id).locator(":scope > p")).toHaveText(
    "Current owner preference",
  );
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  expect(v.tasks).toHaveLength(0);
  expect(v.runs).toHaveLength(0);
  expect(external).toEqual([]);
  expect(writes).toHaveLength(2);
});

test("expired owner access returns to sign-in without replay or stale-session restoration", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  const writes: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).origin !== new URL(url).origin)
      external.push(request.url());
    if (request.method() !== "GET")
      writes.push(`${request.method()} ${new URL(request.url()).pathname}`);
  });
  await page.goto(url + "/#agents/tasks");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  const signOut = page.getByRole("button", { name: "Sign out", exact: true });
  const signIn = page.getByRole("button", { name: "Sign in", exact: true });
  const password = page.getByLabel("Owner password", { exact: true });
  await expect(signOut).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const readAll = () =>
    Promise.all(
      ["wen-operator", "trading-analyst", "news-researcher"].map((id) =>
        privateCLI(["agents", "--profile", id]),
      ),
    );
  const initial = await readAll();
  await privateCLI(["agents", "--request-stdin"], {
    action: "task-create",
    profileId: "trading-analyst",
    revision: initial[1].revision,
    name: "Owner-only fixture reminder",
    text: "Retain this fixture record through authentication changes.",
    intervalSeconds: 3600,
    maxRuns: 1,
  });
  const baseline = await readAll();
  const trading = () => page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByText("Owner-only fixture reminder", { exact: true }),
  ).toBeVisible();
  const headers = async () => {
    const token = await page.evaluate(() =>
      sessionStorage.getItem("fased.core.session.v1"),
    );
    expect(token).toBeTruthy();
    return { Authorization: `Bearer ${token}`, Origin: url };
  };
  const revoke = async () => {
    const h = await headers();
    const state = await page.request.get(url + "/api/v1/auth/state", {
      headers: h,
    });
    expect(state.status()).toBe(200);
    const response = await page.request.post(url + "/api/v1/auth/logout", {
      headers: { ...h, "X-CSRF-Token": (await state.json()).csrfToken },
      data: {},
    });
    expect(response.status()).toBe(200);
  };
  const enter = async () => {
    await revealControl(page, password);
    await password.fill(fixturePassword);
    await signIn.click();
    await expect(signOut).toBeEnabled();
  };
  await revoke();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("alert")).toHaveText(
    "Your session ended. Sign in again.",
  );
  await expect(password).toBeFocused();
  await expect(
    page.getByText("Owner-only fixture reminder", { exact: true }),
  ).toHaveCount(0);
  expect(
    await page.evaluate(() => sessionStorage.getItem("fased.core.session.v1")),
  ).toBeNull();
  expect(writes.filter((s) => s.endsWith("/auth/login"))).toHaveLength(0);
  await page.screenshot({
    path: `.evidence/owner-sign-in-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await revealControl(page, password);
  await password.fill("incorrect-disposable-password");
  await signIn.click();
  await expect(page.getByRole("alert")).toContainText("authentication failed");
  await expect(signIn).toBeEnabled();
  await enter();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  expect(await readAll()).toEqual(baseline);

  // A committed write and an old 401 may arrive after the owner signs in again.
  let releaseSetup = () => {};
  let releaseOldRead = () => {};
  const setupGate = new Promise<void>((resolve) => {
    releaseSetup = resolve;
  });
  const oldReadGate = new Promise<void>((resolve) => {
    releaseOldRead = resolve;
  });
  let setupHeld = false;
  let setupDelivered = false;
  let oldReadStarted = false;
  let oldReadHeld = false;
  let oldReadDelivered = false;
  const setupURL = url + "/api/v1/setup";
  const agentsURL = "**/api/v1/agents?**";
  await page.route(setupURL, async (route) => {
    if (route.request().method() !== "PUT") return route.continue();
    const response = await route.fetch();
    expect(response.status()).toBe(200);
    setupHeld = true;
    await setupGate;
    await route.fulfill({ response });
    setupDelivered = true;
  });
  try {
    await page.goto(url + "/#settings");
    await page.getByRole("button", { name: "Setup", exact: true }).click();
    await page
      .getByRole("dialog", { name: "Instance", exact: true })
      .getByRole("button", { name: "Open Chat", exact: true })
      .click();
    await expect.poll(() => setupHeld).toBe(true);
    await page.keyboard.press("Escape");
    await expect(
      page.getByRole("dialog", { name: "Instance", exact: true }),
    ).toHaveCount(0);
    await exec(binary, ["setup", "--data-dir", dir, "--host", "vps"]);
    const current = await page.request.get(setupURL, {
      headers: await headers(),
    });
    const currentSetup = await current.json();
    expect(currentSetup.settings.host).toBe("vps");
    await page.goto(url + "/#agents/tasks");
    await page.route(agentsURL, async (route) => {
      if (oldReadStarted) return route.continue();
      oldReadStarted = true;
      oldReadHeld = true;
      await oldReadGate;
      const response = await route.fetch();
      expect(response.status()).toBe(401);
      await route.fulfill({ response });
      oldReadDelivered = true;
    });
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect.poll(() => oldReadHeld).toBe(true);
    await revoke();
    // A subsequent protected poll ends the session; do not click the old UI.
    await expect(
      page.getByRole("heading", { name: "Sign in", exact: true }),
    ).toBeVisible({ timeout: 12000 });
    await expect(signIn).toBeEnabled();
    await enter();
    await page.goto(url + "/#settings");
    await page.getByText("Diagnostics", { exact: true }).click();
    await expect(page.getByTestId("saved-deployment-preference")).toHaveText(
      "Remote",
    );
    releaseOldRead();
    releaseSetup();
    await expect.poll(() => setupDelivered && oldReadDelivered).toBe(true);
    await page.evaluate(
      () =>
        new Promise<void>((resolve) =>
          requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
        ),
    );
    await expect(signOut).toBeEnabled();
    await expect(password).toHaveCount(0);
    await expect(page.getByTestId("saved-deployment-preference")).toHaveText(
      "Remote",
    );
    const after = await page.request.get(setupURL, {
      headers: await headers(),
    });
    expect(await after.json()).toEqual(currentSetup);
  } finally {
    releaseOldRead();
    releaseSetup();
    if (!page.isClosed()) {
      await page.unroute(agentsURL);
      await page.unroute(setupURL);
    }
  }
  await page.route(agentsURL, (route) =>
    route.fulfill({
      status: 403,
      json: { error: "fixture permission denied" },
    }),
  );
  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "fixture permission denied",
  );
  await expect(signOut).toBeVisible();
  await expect(password).toHaveCount(0);
  await page.unroute(agentsURL);
  await page
    .getByRole("button", { name: "Refresh records", exact: true })
    .click();
  await expect(trading()).toBeEnabled();
  expect(await readAll()).toEqual(baseline);
  expect(writes.filter((s) => s === "PUT /api/v1/setup")).toHaveLength(1);
  expect(writes.filter((s) => s === "POST /api/v1/auth/login")).toHaveLength(3);
  expect(writes.filter((s) => s === "POST /api/v1/agents")).toEqual([]);
  expect(external).toEqual([]);
});

test("periodic refresh follows the requested profile while a selection is pending", async ({
  page,
}) => {
  test.setTimeout(45000);
  const writes: string[] = [];
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
    if (new URL(r.url()).pathname === "/api/v1/agents" && r.method() !== "GET")
      writes.push(r.method());
  });
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
    if (new URL(r.url()).pathname === "/api/v1/agents" && r.method() === "POST")
      writes.push(r.postDataJSON());
  });
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  await page.screenshot({
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
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
    if (new URL(r.url()).pathname === "/api/v1/agents" && r.method() !== "GET")
      writes.push(r.method());
  });
  await page.goto(url + "/#agents/memory");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  ).toHaveText("WEN Operator");
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
  ).toHaveText("WEN Operator");
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
  ).toHaveText("WEN Operator");
  expect(unknownRequests).toEqual([]);
  await page.evaluate((key) => localStorage.removeItem(key), key);
  await page.reload();
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("WEN Operator");
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
  ).toHaveText("WEN Operator");
  await choose("Trading Analyst");
  expect(await read()).toEqual(baseline);
  expect(external).toEqual([]);
  expect(writes).toEqual([]);
  await page.screenshot({
    path: `.evidence/profile-continuity-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("task inbox opens the exact named run without writes across profiles and reload", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  for (const name of ["Earlier saved reminder", "Later saved reminder"]) {
    const v = await command({
      action: "task-create",
      name,
      text: `Review ${name}`,
      intervalSeconds: 3600,
      maxRuns: 1,
    });
    await command({ action: "task-run", id: v.tasks.at(-1).id });
  }
  let baseline = await read();
  await page.goto(url + "/#agents/tasks");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await page.getByRole("button", { name: "tasks", exact: true }).click();
  const runCard = (id: string) =>
    page.locator(".memory-card").filter({ hasText: `Run ${id}` });
  for (const i of [1, 0]) {
    const run = baseline.runs[i];
    const task = baseline.tasks.find((t: any) => t.id === run.taskId);
    await page.getByRole("button", { name: "history", exact: true }).click();
    await expect(
      runCard(run.id).getByText(task.name, { exact: true }),
    ).toBeVisible();
    const notice = page.getByRole("article", {
      name: `Task notification ${run.id}`,
      exact: true,
    });
    await page.getByRole("button", { name: "inbox", exact: true }).click();
    await notice
      .getByRole("button", { name: "View result", exact: true })
      .click();
    await expect(runCard(run.id)).toBeFocused();
    expect(await read()).toEqual(baseline);
  }
  const firstRun = baseline.runs[0];
  const firstNotice = page.getByRole("article", {
    name: `Task notification ${firstRun.id}`,
    exact: true,
  });
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "Mark read", exact: true })
    .click();
  await expect.poll(async () => (await read()).notices[0].read).toBe(true);
  const afterRead = await read();
  expect(afterRead.runs).toEqual(baseline.runs);
  expect(afterRead.tasks).toEqual(baseline.tasks);
  baseline = afterRead;
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "View result", exact: true })
    .click();
  expect(await read()).toEqual(baseline);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await expect(
    page.getByText("No task notifications.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "View result", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText("Earlier saved reminder", { exact: true }),
  ).toHaveCount(0);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await expect(firstNotice).toBeVisible();
  let fault: "missing" | "foreign" = "missing";
  const routePattern = /\/api\/v1\/agents\?/;
  await page.route(routePattern, async (route) => {
    const response = await route.fetch();
    const v = await response.json();
    if (v.profileId === profileId) {
      if (fault === "missing") v.notices[0].runId = "unavailable-run";
      else v.runs[0].profileId = "news-researcher";
    }
    await route.fulfill({ response, json: v });
  });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", {
      name: "Result unavailable",
      exact: true,
    }),
  ).toBeDisabled();
  fault = "foreign";
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "history", exact: true }).click();
  await expect(
    runCard(firstRun.id).getByText("Task unavailable", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", {
      name: "Result unavailable",
      exact: true,
    }),
  ).toBeDisabled();
  expect(await read()).toEqual(baseline);
  await page.unroute(routePattern);
  await page.reload();
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", { name: "View result", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "View result", exact: true })
    .click();
  await expect(runCard(firstRun.id)).toBeFocused();
  expect(await read()).toEqual(baseline);
  expect(external).toEqual([]);
  await page.screenshot({
    path: `.evidence/task-inbox-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("reminder task draft shares inert inspection edited review and bounded creation across UI CLI and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const origins = new Set([new URL(url).origin]);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!origins.has(new URL(r.url()).origin)) external.push(r.url());
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = async () => {
    const child = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    child.stdin!.end(fixturePassword + "\n");
    expect((await once(child, "exit"))[0]).toBe(0);
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
    action: "task-create",
    name: "Saved daily review",
    text: "Read retained results",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  v = await command({ action: "task-run", id: v.tasks[0].id });
  const original = v.tasks[0];
  const snapshot = await privateCLI([
    "agents",
    "--profile",
    profileId,
    "--reminder-draft",
    original.id,
  ]);
  expect(snapshot.source).toEqual(original);
  expect(snapshot.sourceSha256).toBe(
    createHash("sha256").update(JSON.stringify(original)).digest("hex"),
  );
  expect(await read()).toEqual(v);
  await page.goto(url + "/#agents/tasks");
  const profile = page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  const reuse = page.getByRole("button", {
    name: "Reuse: Saved daily review",
    exact: true,
  });
  await reuse.click();
  const panel = page.getByRole("region", {
    name: "Reuse reminder",
    exact: true,
  });
  await expect(
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
  ).toHaveValue(original.name);
  await panel.getByText("Saved reminder source", { exact: true }).click();
  await expect(panel.locator("pre")).toContainText(snapshot.sourceSha256);
  await panel.getByRole("button", { name: "Discard reminder draft" }).click();
  expect(await read()).toEqual(v);
  await reuse.click();
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder name", exact: true })
    .fill("Reused saved review");
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder text", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder text", exact: true })
    .fill("Review the saved journal again");
  await panel
    .getByRole("button", { name: "Review new reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("button", {
      name: "Create reviewed reminder",
      exact: true,
    }),
  ).toBeVisible();
  expect(await read()).toEqual(v);
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder text", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder text", exact: true })
    .fill("Edited after the first review");
  await expect(
    panel.getByRole("button", { name: "Create reviewed reminder" }),
  ).toHaveCount(0);
  await panel
    .getByRole("button", { name: "Review new reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("button", { name: "Create reviewed reminder" }),
  ).toBeVisible();
  v = await command({
    action: "profile-update",
    name: "Trading Analyst",
    provider: "xai",
    model: "preference-only",
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(panel.getByRole("alert")).toHaveText(
    "Records changed. Reload the reminder to review again.",
  );
  await expect(
    panel.getByRole("button", { name: "Create reviewed reminder" }),
  ).toHaveCount(0);
  await panel
    .getByRole("button", { name: "Reload reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
  ).toHaveValue(original.name);
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder name", exact: true })
    .fill("Reused saved review");
  await revealControl(
    page,
    panel.getByRole("textbox", { name: "Reminder text", exact: true }),
    "",
  );
  await panel
    .getByRole("textbox", { name: "Reminder text", exact: true })
    .fill("Review the saved journal again");
  await revealControl(
    page,
    panel.getByRole("spinbutton", { name: "Interval in seconds", exact: true }),
  );
  await panel
    .getByRole("spinbutton", { name: "Interval in seconds", exact: true })
    .fill("120");
  await revealControl(
    page,
    panel.getByRole("spinbutton", { name: "Maximum evaluations", exact: true }),
  );
  await panel
    .getByRole("spinbutton", { name: "Maximum evaluations", exact: true })
    .fill("1");
  await panel
    .getByRole("button", { name: "Review new reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("button", { name: "Create reviewed reminder" }),
  ).toBeVisible();
  expect(await read()).toEqual(v);
  await panel
    .getByRole("button", { name: "Create reviewed reminder", exact: true })
    .click();
  await expect(panel).toHaveCount(0);
  v = await read();
  expect(v.tasks).toHaveLength(2);
  expect(v.tasks[0]).toEqual(original);
  const copy = v.tasks[1];
  expect(copy).toMatchObject({
    name: "Reused saved review",
    text: "Review the saved journal again",
    intervalSeconds: 120,
    maxRuns: 1,
    runs: 0,
    state: "scheduled",
    botId: "",
  });
  expect(copy.ai).toBeUndefined();
  expect(copy.news).toBeUndefined();
  expect(copy.wen).toBeUndefined();
  expect(v.runs).toHaveLength(1);
  await page
    .getByRole("button", { name: "Run now: Reused saved review", exact: true })
    .click();
  await expect.poll(async () => (await read()).runs.length).toBe(2);
  v = await read();
  expect(v.tasks[0]).toEqual(original);
  expect(v.tasks[1]).toMatchObject({ state: "completed", runs: 1, maxRuns: 1 });
  const foreign = page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("News Researcher");
  await expect(
    page.getByRole("button", { name: "Reuse: Saved daily review" }),
  ).toHaveCount(0);
  await expect(
    privateCLI([
      "agents",
      "--profile",
      "news-researcher",
      "--reminder-draft",
      original.id,
    ]),
  ).rejects.toThrow();
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await page.goto(url + "/#agents/tasks");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await login();
  const reopened = await read();
  for (const field of ["tasks", "runs", "notices", "aiRequests"])
    expect(reopened[field]).toEqual(v[field]);
  expect(reopened.aiRequests).toHaveLength(0);
  expect(reopened.providerCallsEnabled).toBe(false);
  expect(reopened.financialExecution).toBe(false);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await page
    .getByRole("button", { name: "Reuse: Reused saved review", exact: true })
    .click();
  await expect(
    panel.getByRole("textbox", { name: "Reminder name", exact: true }),
  ).toHaveValue(copy.name);
  await expect(
    panel.getByRole("button", { name: "Create reviewed reminder" }),
  ).toHaveCount(0);
  await panel.getByText("Saved reminder source", { exact: true }).click();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/reminder-task-draft-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await panel.getByRole("button", { name: "Discard reminder draft" }).click();
  expect(external).toEqual([]);
  expect((await read()).tasks).toEqual(v.tasks);
});

test("conversation search follows matching records and closes hidden conversation actions", async ({
  page,
  isMobile,
}) => {
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  ).toHaveCount(0);
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
  await page.screenshot({
    path: `.evidence/usability/search-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("joined agent workspace preserves news chat reminder run memory and reviewed AI across UI CLI and restart", async ({
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  const chooseProfile = async (name: RegExp) => {
    const profile = page.getByLabel("Agent", { exact: true });
    await expect(profile).toBeVisible();
    const chosen = (await profile.locator("option").allTextContents()).find(
      (n) => name.test(n + " "),
    );
    expect(chosen).toBeTruthy();
    await profile.selectOption({ label: chosen! });
    await expect(profile.locator("option:checked")).toHaveText(chosen!);
  };
  const open = async (tab: string) => {
    await page.getByRole("button", { name: tab, exact: true }).click();
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("Trading Analyst");
  };
  await page.goto(url + "/#agents/news");
  await chooseProfile(/^Trading Analyst /);
  const news = page.getByRole("region", { name: "News inputs", exact: true });
  await revealControl(
    page,
    news.getByLabel("News source name", { exact: true }),
    "",
  );
  await news
    .getByLabel("News source name", { exact: true })
    .fill("Joined controlled news");
  await revealControl(
    page,
    news.getByLabel("Public RSS URL", { exact: true }),
    "",
  );
  await news
    .getByLabel("Public RSS URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml");
  await revealControl(
    page,
    news.getByLabel("Source freshness limit (seconds)", { exact: true }),
    "",
  );
  await news
    .getByLabel("Source freshness limit (seconds)", { exact: true })
    .fill("600");
  await news
    .getByRole("button", { name: "Add news source", exact: true })
    .click();
  const source = news.getByRole("article", {
    name: "News source Joined controlled news",
    exact: true,
  });
  await expect(source).toBeVisible();
  expect(
    (await read()).newsSources.map((s: { name: string }) => s.name),
  ).toEqual(["Joined controlled news"]);
  await source
    .getByRole("button", { name: "Fetch headlines", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).newsFetches[0]?.state)
    .toBe("completed");
  await news
    .getByRole("button", { name: "Refresh news records", exact: true })
    .click();
  const fetched = await read();
  const fetch = fetched.newsFetches[0],
    item = fetch.items[0];
  await source
    .locator(".memory-record")
    .filter({ hasText: item.title })
    .getByRole("button", { name: "Discuss headline", exact: true })
    .click();
  await expect(page.locator(".local-messages")).toContainText(
    "Historical news headline",
  );
  let v = await read();
  const headlineMessage = v.messages.at(-1);
  const headlineSession = v.sessions.find(
    (s: any) => s.id === headlineMessage.sessionId,
  );
  const [body, hash] = headlineMessage.text.split("\nSnapshot SHA-256: ");
  expect(hash).toBe(createHash("sha256").update(body).digest("hex"));
  expect(JSON.parse(body.slice(body.indexOf("\n") + 1)).Evidence.fetchId).toBe(
    fetch.id,
  );
  expect(v.tasks).toHaveLength(0);
  expect(v.aiRequests).toHaveLength(0);
  expect(v.newsFetches).toEqual(fetched.newsFetches);

  // Optional fixture AI is a separate reviewed branch, never a prerequisite grant.
  const review = page.getByRole("region", {
    name: "Optional AI request review",
    exact: true,
  });
  const prompt = "Draft a reminder to review this saved headline";
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review.getByLabel("Message", { exact: true }).fill(prompt);
  await review.getByRole("button", { name: "Send", exact: true }).click();
  await expect.poll(async () => (await read()).aiRequests.length).toBe(1);
  const prepared = (await read()).aiRequests[0];
  expect(prepared.state).toBe("prepared");
  expect(JSON.parse(prepared.context).context.messages).toEqual([
    { role: "user", text: headlineMessage.text },
  ]);
  expect(prepared.context).toContain(fetch.id);
  expect((await read()).tasks).toHaveLength(0);
  const request = review.locator(".ai-request").last();
  const runFixture = request.getByRole("button", {
    name: "Run development fixture",
    exact: true,
  });
  await expect(runFixture).toBeDisabled();
  await revealControl(
    page,
    request.getByLabel("I reviewed this context and these fixture limits"),
    "",
  );
  await request
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await runFixture.click();
  await expect
    .poll(async () => (await read()).aiRequests[0]?.state)
    .toBe("completed");
  const card = request
    .locator(".ai-action-card")
    .filter({ hasText: "reminder" })
    .first();
  await card.getByRole("button", { name: "Keep draft", exact: true }).click();
  await card
    .getByRole("button", { name: "Review reminder task", exact: true })
    .click();
  const reminder = card.getByRole("region", { name: /Reminder task review:/ });
  const reminderName = "Joined saved headline review";
  await revealControl(
    page,
    reminder.getByLabel("Reminder task name", { exact: true }),
    "",
  );
  await reminder
    .getByLabel("Reminder task name", { exact: true })
    .fill(reminderName);
  await revealControl(
    page,
    reminder.getByLabel("Reminder text", { exact: true }),
    "",
  );
  await reminder
    .getByLabel("Reminder text", { exact: true })
    .fill(`Review saved headline: ${item.title}`);
  await revealControl(page, reminder.getByLabel("Repeat every (seconds)"), "");
  await reminder.getByLabel("Repeat every (seconds)").fill("60");
  await revealControl(page, reminder.getByLabel("Maximum reminder runs"), "");
  await reminder.getByLabel("Maximum reminder runs").fill("1");
  await reminder
    .getByRole("button", { name: "Review reminder schedule", exact: true })
    .click();
  const create = reminder.getByRole("button", {
    name: "Create reviewed reminder",
    exact: true,
  });
  await expect(create).toBeDisabled();
  expect((await read()).tasks).toHaveLength(0);
  await revealControl(
    page,
    reminder.getByLabel("I reviewed this reminder text and schedule"),
    "",
  );
  await reminder
    .getByLabel("I reviewed this reminder text and schedule")
    .check();
  await create.click();
  await expect(card.getByRole("status")).toContainText(
    `Reminder task created: ${reminderName}`,
  );
  v = await read();
  expect(v.tasks).toHaveLength(1);
  expect(v.runs).toHaveLength(0);
  const task = v.tasks[0];
  expect(task.text).toContain(item.title);
  expect(task.maxRuns).toBe(1);
  expect(task.ai).toBeUndefined();
  expect(task.news).toBeUndefined();
  expect(task.wen).toBeUndefined();
  await card.getByRole("button", { name: "Open Tasks", exact: true }).click();
  await expect(page).toHaveURL(/#agents\/tasks$/);
  expect((await read()).runs).toHaveLength(0);
  await page
    .getByRole("button", { name: `Run now: ${reminderName}`, exact: true })
    .click();
  await expect.poll(async () => (await read()).runs.length).toBe(1);
  v = await read();
  const run = v.runs[0];
  const outcomes = {
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  };
  await page.getByRole("button", { name: "history", exact: true }).click();
  await page
    .locator(".memory-card")
    .filter({ hasText: `Run ${run.id}` })
    .getByRole("button", { name: "Discuss run", exact: true })
    .click();
  await expect(page.locator(".local-messages")).toContainText(
    "Historical reminder run",
  );
  v = await read();
  const runMessage = v.messages.at(-1);
  expect(runMessage.text).toContain(run.id);
  expect(runMessage.text).toContain(item.title);
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(outcomes);
  const runArticle = page
    .locator(".local-messages article")
    .filter({ hasText: runMessage.text });
  await runArticle
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const memoryReview = runArticle.getByRole("region", {
    name: "Chat memory review",
    exact: true,
  });
  const noteText =
    "Review retained headline provenance before planning the next reminder";
  await revealControl(
    page,
    memoryReview.getByLabel("Reviewed memory note", { exact: true }),
    "",
  );
  await memoryReview
    .getByLabel("Reviewed memory note", { exact: true })
    .fill(noteText);
  expect((await read()).memories).toHaveLength(0);
  await memoryReview
    .getByRole("button", { name: "Save reviewed memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  await expect(runArticle.getByRole("status")).toHaveText("Memory saved.");
  v = await read();
  const note = v.memories[0];
  expect(note.text).toBe(noteText);
  expect(note.source).toContain(`id=${runMessage.id}`);
  expect(note.source).toContain(
    `sha256=${createHash("sha256").update(runMessage.text).digest("hex")}`,
  );
  const recall = await privateCLI([
    "agents",
    "--profile",
    profileId,
    "--memory-search",
    "HEADLINE PROVENANCE",
    "--memory-source",
    note.id,
  ]);
  expect(recall.memories.map((m: any) => m.id)).toEqual([note.id]);
  await runArticle
    .getByRole("button", { name: "Open Memory", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/memory$/);
  expect((await read()).memories).toHaveLength(1);
  await revealControl(
    page,
    page.getByRole("searchbox", { name: "Search memories", exact: true }),
  );
  await page
    .getByRole("searchbox", { name: "Search memories", exact: true })
    .fill("HEADLINE PROVENANCE");
  await page
    .getByRole("button", { name: "Search memory", exact: true })
    .click();
  await expect(
    page.getByRole("article", { name: `Memory ${note.id}`, exact: true }),
  ).toContainText(noteText);

  const retained = await read();
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.memories).toHaveLength(0);
  expect(other.tasks).toHaveLength(0);
  expect(other.newsFetches).toHaveLength(0);
  for (const c of [
    { action: "task-run-explain", id: run.id },
    { action: "news-item-explain", id: fetch.id, source: item.id },
    {
      action: "memory-chat-save",
      id: runMessage.id,
      source: "message",
      text: "Foreign source refused",
    },
    { action: "task-run", id: task.id },
  ])
    await expect(
      command({ ...c, profileId: "news-researcher" }),
    ).rejects.toThrow();
  await expect(command({ action: "task-run", id: task.id })).rejects.toThrow();
  expect(await read()).toEqual(retained);
  await chooseProfile(/^News Researcher /);
  await expect(
    page.getByRole("article", { name: `Memory ${note.id}`, exact: true }),
  ).toHaveCount(0);
  await chooseProfile(/^Trading Analyst /);
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Review saved sources",
    provider: "xai",
    model: "preference-only",
  });
  const frozen = await read();
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
  await chooseProfile(/^Trading Analyst /);
  const restarted = await read();
  for (const key of [
    "sessions",
    "messages",
    "memories",
    "tasks",
    "runs",
    "newsSources",
    "newsFetches",
    "notices",
    "aiRequests",
  ])
    expect(restarted[key]).toEqual(frozen[key]);
  expect(restarted.profiles.find((p: any) => p.id === profileId).provider).toBe(
    "xai",
  );
  expect(restarted.aiFixtureAvailable).toBe(false);
  expect(restarted.providerCallsEnabled).toBe(false);
  expect(restarted.financialExecution).toBe(false);
  for (const query of [
    prompt.toUpperCase(),
    "NO MODEL, MARKET FEED OR WALLET WAS CONTACTED.",
  ]) {
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
      .fill(query);
    await expect(
      page
        .locator(".agent-session-list")
        .getByRole("button", { name: headlineSession.name, exact: true }),
    ).toBeVisible();
    const found = await privateCLI([
      "agents",
      "--profile",
      profileId,
      "--search",
      query,
    ]);
    expect(found.sessions.map((s: any) => s.id)).toEqual([headlineSession.id]);
    const isolated = await privateCLI([
      "agents",
      "--profile",
      "news-researcher",
      "--search",
      query,
    ]);
    expect(isolated.sessions).toHaveLength(0);
    expect(isolated.aiRequests).toHaveLength(0);
  }
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
    .fill("");
  await page
    .getByRole("button", { name: headlineSession.name, exact: true })
    .click();
  await expect(page.locator(".local-messages")).toContainText(
    headlineMessage.text,
  );
  await expect(review.locator(".ai-request")).toContainText(prepared.prompt);
  await expect(
    review.getByRole("button", {
      name: "Run development fixture",
      exact: true,
    }),
  ).toHaveCount(0);
  expect(await read()).toEqual(restarted);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await mkdir(".evidence/joined-agent", { recursive: true });
  await page.screenshot({
    path: `.evidence/joined-agent/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await open("news");
  const agentsRead = "**/api/v1/agents?*";
  let captured = false,
    release!: () => void;
  let olderRequest: Request | undefined;
  const releaseOlder = new Promise<void>((r) => (release = r));
  const olderResponse = page.waitForResponse(
    (r) => r.request() === olderRequest,
    { timeout: 15000 },
  );
  void olderResponse.catch(() => {});
  let reads = 0;
  // A response captured before the failed read cannot restore connectivity.
  await page.route(agentsRead, async (route) => {
    if (reads++ === 0) {
      olderRequest = route.request();
      const response = await route.fetch();
      captured = true;
      await releaseOlder;
      await route.fulfill({ response });
    } else
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: "Controlled agent read unavailable" }),
      });
  });
  try {
    await page
      .getByRole("button", { name: "Refresh news records", exact: true })
      .click();
    await expect.poll(() => captured).toBe(true);
    // The existing five-second poll supplies the newer failed read. A second
    // click could instead wait forever on the now-disabled refresh control.
    await expect(page.getByRole("alert")).toContainText(
      "Controlled agent read unavailable",
      { timeout: 8000 },
    );
    await expect(
      page.getByRole("button", { name: "Fetch headlines", exact: true }),
    ).toBeDisabled();
  } finally {
    release();
  }
  await (await olderResponse).finished();
  await page.evaluate(
    () =>
      new Promise<void>((r) =>
        requestAnimationFrame(() => requestAnimationFrame(() => r())),
      ),
  );
  await expect(page.getByRole("alert")).toContainText(
    "Controlled agent read unavailable",
    { timeout: 1000 },
  );
  await expect(
    page.getByRole("button", { name: "Fetch headlines", exact: true }),
  ).toBeDisabled();
  await page.unroute(agentsRead);
  await page
    .getByRole("button", { name: "Refresh records", exact: true })
    .click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Fetch headlines", exact: true }),
  ).toBeEnabled();
  expect(await read()).toEqual(restarted);
  // Keep the app loaded while the real fixture core disappears.
  await stop();
  await page
    .getByRole("button", { name: "Refresh news records", exact: true })
    .click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Fetch headlines", exact: true }),
  ).toBeDisabled();
  await writeFile(
    `.evidence/joined-agent/${isMobile ? "mobile" : "desktop"}.json`,
    JSON.stringify(
      {
        result: "PASS",
        fixtureOnly: true,
        profileId,
        sourceId: fetch.sourceId,
        fetchId: fetch.id,
        headlineId: item.id,
        headlineMessageId: headlineMessage.id,
        requestId: prepared.id,
        taskId: task.id,
        runId: run.id,
        runMessageId: runMessage.id,
        memoryId: note.id,
        headlineSnapshotSHA256: hash,
        sourceBackedMemory: true,
        distinctTasks: restarted.tasks.length,
        distinctRuns: restarted.runs.length,
        restartProviderSearchIsolation: true,
        externalBrowserRequests: external,
        realAI: false,
        wallets: false,
        automaticPublicUpdates: false,
        disconnectedCore: true,
      },
      null,
      2,
    ) + "\n",
  );
});

test("update preferences and review-only proposals share revisions, survive restart and obey the metadata pause", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#settings");
  await openUpdateSettings(page);
  const panel = page.getByRole("region", {
    name: "Update preferences & proposals",
  });
  await expect(
    panel.getByLabel("Show notices for pending proposals"),
  ).toBeChecked();
  await revealControl(
    page,
    panel.getByLabel("Pin a release identity for review"),
    "",
  );
  await panel.getByLabel("Pin a release identity for review").check();
  await revealControl(
    page,
    panel.getByLabel("Pinned sequence", { exact: true }),
    "",
  );
  await panel.getByLabel("Pinned sequence", { exact: true }).fill("7");
  await revealControl(
    page,
    panel.getByLabel("Pinned manifest SHA-256", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Pinned manifest SHA-256", { exact: true })
    .fill("a".repeat(64));
  await revealControl(
    page,
    panel.getByLabel("Set a preferred maintenance window"),
    "",
  );
  await panel.getByLabel("Set a preferred maintenance window").check();
  await revealControl(page, panel.getByLabel("IANA timezone"), "");
  await panel.getByLabel("IANA timezone").fill("America/Chicago");
  await revealControl(page, panel.getByLabel("Window start"), "");
  await panel.getByLabel("Window start").fill("23:00");
  await revealControl(page, panel.getByLabel("Window end"), "");
  await panel.getByLabel("Window end").fill("01:00");
  await panel
    .getByRole("button", { name: "Save update preferences", exact: true })
    .click();
  await expect(panel.getByRole("status")).toContainText(
    "Saved app metadata only",
  );
  const initial = await privateCLI(["updates", "preferences"]);
  expect(initial.policy.window.timeZone).toBe("America/Chicago");
  expect(initial.policy.pin.sequence).toBe("7");
  await revealControl(
    page,
    panel.getByLabel("Release label", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Release label", { exact: true })
    .fill("Disposable review");
  await revealControl(
    page,
    panel.getByLabel("Claimed sequence", { exact: true }),
    "",
  );
  await panel.getByLabel("Claimed sequence", { exact: true }).fill("7");
  await revealControl(
    page,
    panel.getByLabel("Claimed manifest SHA-256", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Claimed manifest SHA-256", { exact: true })
    .fill("a".repeat(64));
  await panel
    .getByRole("button", { name: "Add review-only proposal", exact: true })
    .click();
  const card = panel.getByRole("article", {
    name: "Update proposal Disposable review",
  });
  await expect(card.getByText("UNVERIFIED", { exact: true })).toBeVisible();
  await expect(
    panel.getByRole("heading", { name: "Update notices (1)", exact: true }),
  ).toBeVisible();
  const proposed = await privateCLI(["updates", "preferences"]);
  const duplicate = await privateCLI(
    ["updates", "preferences", "--request-stdin"],
    {
      revision: proposed.revision,
      action: "propose",
      candidate: proposed.proposals[0].candidate,
    },
  );
  expect(duplicate.revision).toBe(proposed.revision);
  expect(duplicate.notifications).toEqual(proposed.notifications);
  await privateCLI(["updates", "preferences", "--request-stdin"], {
    revision: proposed.revision,
    action: "policy",
    policy: { ...proposed.policy, notify: false },
  });
  await card
    .getByRole("button", { name: "Mark reviewed", exact: true })
    .click();
  await expect(panel.getByRole("alert")).toContainText(
    "settings changed in another client",
  );
  await expect(card).toHaveCount(0);
  await panel
    .getByRole("button", { name: "Refresh update preferences", exact: true })
    .click();
  await expect(
    panel.getByRole("heading", { name: "Update notices (0)", exact: true }),
  ).toBeVisible();
  await card
    .getByRole("button", { name: "Mark reviewed", exact: true })
    .click();
  await expect(card).toContainText("reviewed");
  const reviewed = await privateCLI(["updates", "preferences"]);
  expect(reviewed.proposals[0].verified).toBe(false);
  expect(reviewed.installAllowed).toBe(false);
  expect(reviewed.resumeAllowed).toBe(false);
  expect(reviewed.automaticUpdates).toBe(false);
  expect(reviewed.financialExecution).toBe(false);
  await page.screenshot({
    path: `.evidence/p4-policy/preferences-${test.info().project.name}.png`,
    fullPage: true,
  });
  const probe = randomBytes(16).toString("hex"),
    snapshot = await privateCLI(["update-inventory", "--probe-id", probe]);
  const pause = {
    version: 1,
    intentId: randomBytes(16).toString("hex"),
    sequence: "1",
    manifestSha256: createHash("sha256")
      .update("disposable policy pause")
      .digest("hex"),
    expected: snapshot,
  };
  await privateCLI(["update-pause", "--request-stdin"], {
    action: "pause",
    probeId: probe,
    request: pause,
  });
  await revealControl(
    page,
    panel.getByLabel("Show notices for pending proposals"),
    "",
  );
  await panel.getByLabel("Show notices for pending proposals").check();
  await panel
    .getByRole("button", { name: "Save update preferences", exact: true })
    .click();
  await expect(panel.getByRole("alert")).toContainText(
    "core metadata paused for update",
  );
  expect((await privateCLI(["updates", "preferences"])).revision).toBe(
    reviewed.revision,
  );
  await stop();
  await start();
  await page.goto(url + "/#settings");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await openUpdateSettings(page);
  await expect(panel.getByLabel("IANA timezone")).toHaveValue(
    "America/Chicago",
  );
  await expect(card).toContainText("reviewed");
  expect((await privateCLI(["updates", "preferences"])).policy).toEqual(
    reviewed.policy,
  );
  await privateCLI(["update-pause", "--request-stdin"], {
    action: "release",
    probeId: probe,
    aborted: {
      id: pause.intentId,
      sequence: "1",
      manifestSha256: pause.manifestSha256,
      core: snapshot,
      state: "aborted",
    },
  });
});

test("update status shares UI and private CLI observations across pause, release and restart", async ({
  page,
}) => {
  await expect(
    exec(binary, ["updates", "status", "--data-dir", dir]),
  ).rejects.toThrow();
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await page.goto(url + "/#settings");
  await openUpdateSettings(page);
  const panel = page.getByRole("region", { name: "Updates & recovery" });
  await expect(
    panel.getByRole("heading", { name: "Signer not configured" }),
  ).toBeVisible();
  await expect(panel.getByText("Unknown", { exact: true })).toHaveCount(3);
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const cli = async (args: string[], input?: unknown) => {
    if (input === undefined)
      return JSON.parse(
        (await exec(binary, [...args, "--data-dir", dir])).stdout,
      );
    const child = spawn(binary, [...args, "--data-dir", dir]);
    let output = "",
      error = "";
    child.stdout!.on("data", (b) => (output += b));
    child.stderr!.on("data", (b) => (error += b));
    child.stdin!.end(JSON.stringify(input));
    expect((await once(child, "exit"))[0], error).toBe(0);
    return JSON.parse(output);
  };
  const before = await cli(["updates", "status"]);
  expect(before.core.phase).toBe("idle");
  expect(before.automaticUpdates).toBe(false);
  const probe = randomBytes(16).toString("hex");
  const snapshot = await cli(["update-inventory", "--probe-id", probe]);
  const request = {
    version: 1,
    intentId: randomBytes(16).toString("hex"),
    sequence: "1",
    manifestSha256: createHash("sha256")
      .update("disposable browser fence; no signer authority")
      .digest("hex"),
    expected: snapshot,
  };
  await cli(["update-pause", "--request-stdin"], {
    action: "pause",
    probeId: probe,
    request,
  });
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(
    panel.getByRole("heading", { name: "Metadata paused", exact: true }),
  ).toBeVisible();
  await expect(
    panel.getByText(request.intentId, { exact: true }),
  ).toBeVisible();
  const held = await cli(["updates", "status"]);
  expect(held.core).toEqual({ phase: "paused", intentId: request.intentId });
  expect(held.signer).toEqual(before.signer);
  expect(held.guidance.some((g: { code: string }) => g.code === "paused")).toBe(
    true,
  );
  await page.screenshot({
    path: `.evidence/p4-status/paused-${test.info().project.name}.png`,
    fullPage: true,
  });
  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  expect(
    (
      await page.request.post(`${url}/api/v1/updates/pause`, {
        headers: { Authorization: "Bearer " + token, Origin: url },
        data: {},
      })
    ).status(),
  ).toBe(403);
  expect(
    (
      await page.request.post(`${url}/api/v1/updates/status`, {
        headers: { Authorization: "Bearer " + token },
        data: {},
      })
    ).status(),
  ).toBe(405);
  await stop();
  await start();
  await page.goto(url + "/#settings");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await openUpdateSettings(page);
  await expect(
    panel.getByRole("heading", { name: "Metadata paused", exact: true }),
  ).toBeVisible();
  await cli(["update-pause", "--request-stdin"], {
    action: "release",
    probeId: probe,
    aborted: {
      id: request.intentId,
      sequence: "1",
      manifestSha256: request.manifestSha256,
      core: snapshot,
      state: "aborted",
    },
  });
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(
    panel.getByRole("heading", { name: "Signer not configured", exact: true }),
  ).toBeVisible();
  await expect(panel.getByText("released", { exact: true })).toBeVisible();
  expect((await cli(["updates", "status"])).core.phase).toBe("released");
});

test("update recovery view rejects unsupported or lost status without retaining a healthy observation (UI mock only)", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  let fail = false,
    unsupported = false;
  const observation = {
    version: 1,
    observedAt: new Date().toISOString(),
    readOnly: true,
    state: "recovery-required",
    core: { phase: "paused" },
    signer: {
      state: "available",
      locked: true,
      updateHeld: true,
      recoveryRequired: true,
    },
    guidance: [
      {
        code: "recovery-required",
        message: "Preserve keys, original records, counters and holds.",
        command:
          "fased-app custody updates inventory --disposable-fixture --prepare-host",
      },
    ],
    walletSigner: false,
    financialExecution: false,
    automaticUpdates: false,
  };
  await page.route("**/api/v1/updates/status", async (route) => {
    if (fail) return route.abort("failed");
    await route.fulfill({
      json: { ...observation, automaticUpdates: unsupported },
    });
  });
  await page.goto(url + "/#settings");
  await openUpdateSettings(page);
  const panel = page.getByRole("region", { name: "Updates & recovery" });
  await expect(
    panel.getByRole("heading", {
      name: "Recovery review required",
      exact: true,
    }),
  ).toBeVisible();
  await expect(panel.getByLabel("Owner inventory command")).toContainText(
    "custody updates inventory",
  );
  await expect(panel.getByText("Locked", { exact: true })).toBeVisible();
  await expect(panel.getByRole("button")).toHaveCount(1);
  await page.screenshot({
    path: `.evidence/p4-status/recovery-mock-${test.info().project.name}.png`,
    fullPage: true,
  });
  unsupported = true;
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(panel.getByRole("alert")).toContainText(
    "Update status unavailable",
  );
  await expect(
    panel.getByText("Recovery review required", { exact: true }),
  ).toHaveCount(0);
  fail = true;
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(panel.getByRole("alert")).toBeVisible();
  fail = false;
  unsupported = false;
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(
    panel.getByRole("heading", {
      name: "Recovery review required",
      exact: true,
    }),
  ).toBeVisible();
});

test("claim, UI/CLI setup, restart and logout preserve one Local instance", async ({
  page,
}) => {
  await page.goto(url);
  await expect(
    page.getByRole("heading", { name: "Claim this workspace" }),
  ).toBeVisible();
  await page.screenshot({
    path: `.evidence/claim-${test.info().project.name}.png`,
    fullPage: true,
  });
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const health = await (await page.request.get(`${url}/api/v1/health`)).json();
  expect(health.capabilities.walletSigner).toBe(false);
  expect(health.capabilities.financialExecution).toBe(false);
  expect(
    await page.evaluate(() =>
      Object.keys(localStorage).some((key) => key.includes("session")),
    ),
  ).toBe(false);
  expect(output).not.toContain(claim.code);
  expect(output).not.toContain(fixturePassword);
  await page
    .getByRole("button", { name: "Set up workspace", exact: true })
    .click();
  const instance = page.getByRole("dialog", { name: "Instance", exact: true });
  await expect(instance.getByText("Local", { exact: true })).toBeVisible();
  await expect(
    instance.getByRole("button", { name: /Remote Always-on/ }),
  ).toHaveCount(0);
  await instance.getByText("Instance details", { exact: true }).click();
  await expect(
    instance.getByText(health.instanceId, { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close dialog" }).click();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  // CLI credentials use the protected socket. Saved preferences cannot move
  // this runtime or grant wallet/provider authority.
  await new Promise<void>((resolveLogin, reject) => {
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin.end(fixturePassword + "\n");
    login.on("error", reject);
    login.on("exit", (code) =>
      code === 0
        ? resolveLogin()
        : reject(new Error("fixture CLI login failed")),
    );
  });
  const current = JSON.parse(
    (await exec(binary, ["setup", "--data-dir", dir])).stdout,
  );
  expect(current.settings.host).toBe("local");
  await exec(binary, [
    "setup",
    "--data-dir",
    dir,
    "--host",
    "vps",
    "--wallet-mode",
    "watch",
  ]);
  await page.reload();
  await page
    .getByRole("button", { name: "Set up workspace", exact: true })
    .click();
  await expect(instance.getByText("Local", { exact: true })).toBeVisible();
  const saveSetup = instance.getByRole("button", { name: /Open chat/i });
  // Preserve the existing stale-review contract through the compact setup UI.
  await exec(binary, ["setup", "--data-dir", dir, "--provider", "anthropic"]);
  await saveSetup.click();
  await expect(instance.getByRole("alert")).toHaveText(
    /settings changed in another client/,
  );
  expect(
    JSON.parse((await exec(binary, ["setup", "--data-dir", dir])).stdout)
      .settings.complete,
  ).toBe(false);
  await saveSetup.click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const saved = JSON.parse(
    (await exec(binary, ["setup", "--data-dir", dir])).stdout,
  ).settings;
  expect(saved).toMatchObject({
    complete: true,
    host: "vps",
    walletMode: "watch",
    provider: "anthropic",
  });
  await page.goto(url + "/#settings");
  await page.getByText("Diagnostics", { exact: true }).click();
  await expect(page.getByTestId("saved-deployment-preference")).toHaveText(
    "Remote",
  );
  await expect(page.getByLabel("Instance location")).toHaveText("Local");
  const unchangedHealth = await (
    await page.request.get(`${url}/api/v1/health`)
  ).json();
  expect(unchangedHealth.instanceId).toBe(health.instanceId);
  expect(unchangedHealth.connection).toEqual({
    location: "local",
    transport: "loopback",
  });
  for (const key of [
    "aiConnections",
    "walletSigner",
    "financialExecution",
    "updater",
  ])
    expect(unchangedHealth.capabilities[key]).toBe(false);
  const duplicate = await exec(binary, [
    "serve",
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    dir,
  ]).then(
    () => "unexpected",
    (e: Error) => e.message,
  );
  expect(duplicate).toContain("already owns");

  await stop();
  await start();
  const restarted = JSON.parse(
    (await exec(binary, ["status", "--data-dir", dir])).stdout,
  );
  expect(restarted.instanceId).toBe(health.instanceId);
  expect(
    JSON.parse((await exec(binary, ["setup", "--data-dir", dir])).stdout)
      .settings.walletMode,
  ).toBe("watch");
  await page.goto(url);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  expect((await page.request.get(`${url}/api/v1/setup`)).status()).toBe(401);
});

test("Satcoin prototype preserves legacy links and disabled authority across core restart", async ({
  page,
  isMobile,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const before = await (await page.request.get(`${url}/api/v1/health`)).json();
  const external: string[] = [];
  const mutations: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).origin !== new URL(url).origin)
      external.push(request.url());
    if (!["GET", "HEAD"].includes(request.method()))
      mutations.push(request.url());
  });
  for (const route of ["asset", "loss", "satcoin"]) {
    await page.goto(`${url}/#terminals/${route}`);
    await expect(page).toHaveURL(/#terminals\/satcoin$/);
    await expect(
      page.getByRole("heading", { name: "WEN Satcoin Mining", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Execution unavailable" }),
    ).toBeDisabled();
    await expect(page.getByText("Not loaded", { exact: true })).toBeVisible();
    await expect(page.locator("main")).not.toContainText(
      /Asset Mining|Loss Mining|district|arena/i,
    );
    if (isMobile)
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
  }
  expect(external).toEqual([]);
  expect(mutations).toEqual([]);
  await stop();
  await start();
  await page.goto(`${url}/#terminals/loss`);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page).toHaveURL(/#terminals\/satcoin$/);
  await expect(
    page.getByRole("heading", { name: "WEN Satcoin Mining", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Execution unavailable" }),
  ).toBeDisabled();
  const after = await (await page.request.get(`${url}/api/v1/health`)).json();
  expect(after.instanceId).toBe(before.instanceId);
  expect(after.capabilities.walletSigner).toBe(false);
  expect(after.capabilities.financialExecution).toBe(false);
});

test("production UI fails closed when the core API is unavailable", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/state", (route) =>
    route.fulfill({
      status: 503,
      contentType: "text/html",
      body: "unavailable",
    }),
  );
  await page.goto(url);
  await expect(
    page.getByRole("heading", { name: "Connection unavailable" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Set up workspace" }),
  ).toHaveCount(0);
});

test("public account records share UI/CLI state without wallet authority", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await page.goto(url + "/#wallets/accounts");
  await expect(
    page.getByRole("heading", { name: "Public account records" }),
  ).toBeVisible();
  await revealControl(page, page.getByLabel("Name", { exact: true }), "");
  await page.getByLabel("Name", { exact: true }).fill("Disposable watch");
  await revealControl(
    page,
    page.getByLabel("Disposable public key (64 lowercase hex)"),
    "",
  );
  await page
    .getByLabel("Disposable public key (64 lowercase hex)")
    .fill("4".repeat(64));
  await page
    .getByRole("button", { name: "Save public account", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Disposable watch", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Record disposable signer", exact: true }),
  ).toBeDisabled();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin.end(fixturePassword + "\n");
  await once(login, "exit");
  expect(login.exitCode).toBe(0);
  let state = JSON.parse(
    (await exec(binary, ["accounts", "--data-dir", dir])).stdout,
  );
  expect(state.record.accounts[0].name).toBe("Disposable watch");
  expect(state.fixture.available).toBe(false);
  const stale = structuredClone(state.record);
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  await revealControl(page, page.getByLabel("New account name"), "");
  await page.getByLabel("New account name").fill("Shared watch");
  await page
    .getByRole("button", { name: "Save account name", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Shared watch", exact: true }),
  ).toBeVisible();
  const rejected = spawn(binary, [
    "accounts",
    "--data-dir",
    dir,
    "--request-stdin",
  ]);
  rejected.stdin.end(JSON.stringify(stale));
  await once(rejected, "exit");
  expect(rejected.exitCode).not.toBe(0);
  state = JSON.parse(
    (await exec(binary, ["accounts", "--data-dir", dir])).stdout,
  );
  state.record.accounts.push({
    version: 1,
    id: "b".repeat(32),
    name: "External manual fixture",
    mode: "external",
    network: "disposable-fixture:v1",
    publicKey: "5".repeat(64),
  });
  const change = spawn(binary, [
    "accounts",
    "--data-dir",
    dir,
    "--request-stdin",
  ]);
  change.stdin.end(JSON.stringify(state.record));
  await once(change, "exit");
  expect(change.exitCode).toBe(0);
  await page
    .getByRole("button", { name: "Refresh accounts and approvals" })
    .click();
  await expect(
    page.getByRole("heading", { name: "External manual fixture", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("REAL WALLETS DISABLED", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: `.evidence/p3-accounts/public-accounts-${test.info().project.name}.png`,
    fullPage: true,
  });
  await page.evaluate(() => sessionStorage.removeItem("fased.core.session.v1"));
  await stop();
  await start();
  await page.goto(url + "/#wallets/accounts");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Shared watch", exact: true }),
  ).toBeVisible();
});

test("proposal form uses explicit fixture labels (UI mock only)", async ({
  page,
}) => {
  const ref = {
    version: 1,
    signerId: "1".repeat(32),
    walletId: "2".repeat(32),
    epoch: "3".repeat(32),
    network: "disposable-fixture:v1",
    publicKey: "4".repeat(64),
  };
  let record = {
    version: 1,
    revision: 1,
    accounts: [
      {
        version: 1,
        id: "d".repeat(32),
        name: "Mock disposable reference",
        mode: "fixture-managed",
        network: ref.network,
        publicKey: ref.publicKey,
        signer: ref,
      },
    ],
    proposals: [] as {
      accountId: string;
      proposal: {
        id: string;
        policy: { budget: string };
        grant: { budget: string; account: typeof ref };
      };
    }[],
  };
  await page.route("**/api/v1/accounts", async (route) => {
    if (route.request().method() === "PUT") {
      record = route.request().postDataJSON();
      record.revision++;
    }
    await route.fulfill({
      json: {
        record,
        fixture: {
          available: true,
          status: {
            account: ref,
            locked: true,
            recoveryRequired: false,
            walletSigner: false,
            financialExecution: false,
          },
        },
        accountStates: { [record.accounts[0].id]: "current-fixture" },
        proposals: record.proposals.map((d) => ({
          id: d.proposal.id,
          digest: "c".repeat(64),
          receipt: { digest: "", state: "not-approved" },
        })),
      },
    });
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  await page.goto(url + "/#wallets/accounts");
  await expect(page.getByLabel("Fixture account", { exact: true })).toBeVisible(
    { timeout: 1000 },
  );
  await revealControl(
    page,
    page.getByLabel("Fixture account", { exact: true }),
    "",
  );
  await page
    .getByLabel("Fixture account", { exact: true })
    .selectOption("d".repeat(32));
  await page
    .getByRole("button", { name: "Create permission proposal", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Download public proposal", exact: true }),
  ).toBeVisible();
  expect(record.proposals[0].proposal.policy.budget).toBe("100");
  expect(record.proposals[0].proposal.grant.budget).toBe("50");
  expect(record.proposals[0].proposal.grant.account).toEqual(ref);
  await expect(page.getByText("not-approved", { exact: true })).toBeVisible();
});

test("local agents share UI and CLI records, isolate profiles and survive core restart", async ({
  page,
}) => {
  test.setTimeout(60000);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/chat");
  await page.getByRole("button", { name: "New profile", exact: true }).click();
  await revealControl(
    page,
    page.getByLabel("Profile name", { exact: true }),
    "",
  );
  await page
    .getByLabel("Profile name", { exact: true })
    .fill("Local acceptance researcher");
  await page
    .getByRole("button", { name: "Create profile", exact: true })
    .click();
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
  await page.getByRole("button", { name: "memory", exact: true }).click();
  await expect(
    page.getByText("Concise answers", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Correct note", exact: true }).click();
  await revealControl(page, page.getByLabel("Memory note"), "Memory note");
  await page.getByLabel("Memory note").fill("Detailed answers");
  await revealControl(page, page.getByLabel("Evidence or source"), "");
  await page
    .getByLabel("Evidence or source")
    .fill("Corrected owner preference");
  await page
    .getByRole("button", { name: "Save correction", exact: true })
    .click();
  await expect(
    page.getByText("Detailed answers", { exact: true }),
  ).toBeVisible();
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.memories[0].id).toBe(memoryID);
  expect(view.memories[0].history[0].text).toBe("Concise answers");
  await page.getByRole("button", { name: "settings", exact: true }).click();
  await revealControl(page, page.getByLabel("Provider preference"), "");
  await page.getByLabel("Provider preference").selectOption("openai");
  await revealControl(page, page.getByLabel("Model preference"), "");
  await page.getByLabel("Model preference").fill("reference-only");
  await page.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", p])).profiles.find(
          (x: { id: string }) => x.id === p,
        ).provider,
    )
    .toBe("openai");
  await page.getByRole("button", { name: "bots", exact: true }).click();
  await revealControl(page, page.getByLabel("Paper bot name"), "");
  await page.getByLabel("Paper bot name").fill("Fixed example");
  await page
    .getByRole("button", { name: "Create paper bot", exact: true })
    .click();
  await expect(page.getByText("Fixed example", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "tasks", exact: true }).click();
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
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await page
    .getByRole("button", { name: "Run now: One paper evaluation", exact: true })
    .click();
  await page.getByRole("button", { name: "history", exact: true }).click();
  await expect(page.getByText("paper-filled", { exact: true })).toBeVisible();
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.runs).toHaveLength(1);
  expect(view.bots[0].spentCents).toBe(100);
  expect(view.financialExecution).toBe(false);
  expect(view.providerCallsEnabled).toBe(false);
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
  await page.screenshot({
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
  await page.screenshot({
    path: resolve(
      ".evidence/p5-local-agents/chat-" + test.info().project.name + ".png",
    ),
    fullPage: true,
  });
});

test("bounded news inputs retain provenance across UI CLI and restart, hold dependent failures and isolate profiles", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/news");
  const panel = page.getByRole("region", { name: "News inputs", exact: true });
  await revealControl(
    page,
    panel.getByLabel("News source name", { exact: true }),
    "",
  );
  await panel
    .getByLabel("News source name", { exact: true })
    .fill("Controlled headlines");
  await revealControl(
    page,
    panel.getByLabel("Public RSS URL", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Public RSS URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml");
  await revealControl(
    page,
    panel.getByLabel("Source freshness limit (seconds)", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Source freshness limit (seconds)", { exact: true })
    .fill("600");
  await panel
    .getByRole("button", { name: "Add news source", exact: true })
    .click();
  const source = panel.getByRole("article", {
    name: "News source Controlled headlines",
    exact: true,
  });
  await source
    .getByRole("button", { name: "Fetch headlines", exact: true })
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).newsFetches[0]?.state)
    .toBe("completed");
  await panel
    .getByRole("button", { name: "Refresh news records", exact: true })
    .click();
  await expect(source).toContainText("Controlled paper-news headline");
  let v = await privateCLI(["agents"]);
  const sourceID = v.newsSources[0].id;
  const first = v.newsFetches[0];
  expect(first.bodySha256).toMatch(/^[a-f0-9]{64}$/);
  expect(first.items).toHaveLength(1);
  expect(first.fetchedAt).toBeGreaterThanOrEqual(first.items[0].publishedAt);
  expect(
    (await privateCLI(["agents", "--profile", "trading-analyst"])).newsFetches,
  ).toHaveLength(0);
  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await revealControl(
    page,
    page.getByRole("combobox", { name: "News input", exact: true }).first(),
  );
  await page
    .getByRole("combobox", { name: "News input", exact: true })
    .first()
    .selectOption(sourceID);
  await revealControl(
    page,
    page.getByLabel("Task name", { exact: true }),
    "Task name",
  );
  await page
    .getByLabel("Task name", { exact: true })
    .fill("Sourced UI reminder");
  await revealControl(
    page,
    page.getByLabel("Reminder text", { exact: true }),
    "",
  );
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review the retained headlines");
  await revealControl(
    page,
    page.getByLabel("Maximum evaluations", { exact: true }),
    "",
  );
  await page.getByLabel("Maximum evaluations", { exact: true }).fill("1");
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await page
    .getByRole("button", { name: "Run now: Sourced UI reminder", exact: true })
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0]?.state)
    .toBe("completed");
  v = await privateCLI(["agents"]);
  expect(v.runs[0].news.sourceId).toBe(sourceID);
  expect(v.runs[0].news.fetchId).not.toBe(first.id);
  expect(v.aiRequests).toHaveLength(0);
  await page.goto(url + "/#agents/news");
  await panel
    .getByRole("button", { name: "Refresh news records", exact: true })
    .click();
  await revealControl(
    page,
    panel.getByLabel("News source name", { exact: true }),
    "",
  );
  await panel
    .getByLabel("News source name", { exact: true })
    .fill("Unavailable headlines");
  await revealControl(
    page,
    panel.getByLabel("Public RSS URL", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Public RSS URL", { exact: true })
    .fill("https://news.fased.invalid/unavailable.xml");
  await panel
    .getByRole("button", { name: "Add news source", exact: true })
    .click();
  await expect(
    panel.getByRole("article", {
      name: "News source Unavailable headlines",
      exact: true,
    }),
  ).toBeVisible();
  v = await privateCLI(["agents"]);
  const bad = v.newsSources.find(
    (s: { name: string }) => s.name === "Unavailable headlines",
  );
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-create",
    name: "Dependent failure",
    text: "Hold this reminder",
    intervalSeconds: 60,
    maxRuns: 1,
    newsSourceId: bad.id,
    newsMaxAgeSeconds: 300,
  });
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-run",
    id: v.tasks[1].id,
  });
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[1]?.state)
    .toBe("held");
  v = await privateCLI(["agents"]);
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-create",
    name: "Independent success",
    text: "No news needed",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-run",
    id: v.tasks[2].id,
  });
  await stop();
  await start();
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/news");
  await expect(source).toContainText(first.bodySha256);
  await expect(
    panel.getByRole("article", {
      name: "News source Unavailable headlines",
      exact: true,
    }),
  ).toContainText("failed");
  v = await privateCLI(["agents"]);
  expect(v.tasks.map((t: { state: string }) => t.state)).toEqual([
    "completed",
    "held",
    "completed",
  ]);
  expect(v.newsFetches[0].bodySha256).toBe(first.bodySha256);
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/p5-news/${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("OpenAI test connection shares encrypted storage, bound consent and restart lock across UI and CLI", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/settings");
  const panel = page.getByRole("region", {
    name: "OpenAI connection",
    exact: true,
  });
  await expect(panel.getByRole("status")).toHaveText("No connection saved");
  const key = "fased-test-browser-disposable-key";
  const passphrase = "disposable-browser-vault-passphrase";
  await revealControl(page, panel.getByLabel("Disposable API key"), "");
  await panel.getByLabel("Disposable API key").fill(key);
  await revealControl(page, panel.getByLabel("Vault passphrase"), "");
  await panel.getByLabel("Vault passphrase").fill(passphrase);
  await panel.getByRole("button", { name: "Save test connection" }).click();
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await expect(panel.getByLabel("Disposable API key")).toHaveValue("");
  await expect(panel.getByLabel("Vault passphrase")).toHaveValue("");
  let connection = await privateCLI(["ai-connection"]);
  expect(connection.locked).toBe(false);
  expect(connection.liveCallsEnabled).toBe(false);
  const firstRef = connection.ref;
  const ciphertext = await readFile(join(dir, "ai-credentials.json"), "utf8");
  expect(ciphertext).not.toContain(key);
  expect(ciphertext).not.toContain(passphrase);
  await page.goto(url + "/#agents/chat");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page.getByLabel("Conversation name").fill("Connected fixture review");
  await page.getByRole("button", { name: "Create conversation" }).click();
  const review = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(
    page,
    review.getByRole("combobox", { name: "AI connection", exact: true }),
  );
  await review
    .getByRole("combobox", { name: "AI connection", exact: true })
    .selectOption(firstRef);
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Analyze fixed paper inputs using the test connection");
  await review.getByRole("button", { name: "Send" }).click();
  await review.getByText("Exact context disclosure", { exact: true }).click();
  await expect(review).toContainText(firstRef);
  await revealControl(
    page,
    review.getByLabel("I reviewed this context and these fixture limits"),
    "",
  );
  await review
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await review.getByRole("button", { name: "Run development fixture" }).click();
  await expect(review.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  let agents = await privateCLI(["agents"]);
  expect(agents.aiRequests[0].connectionRef).toBe(firstRef);
  expect(agents.aiRequests[0].heldMicrousd).toBe(0);
  expect(JSON.stringify(agents)).not.toContain(key);
  expect(
    await page.evaluate(() => JSON.stringify([localStorage, sessionStorage])),
  ).not.toContain(key);
  await stop();
  await start();
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/settings");
  await expect(panel.getByRole("status")).toContainText("Locked");
  connection = await privateCLI(["ai-connection"]);
  expect(connection.ref).toBe(firstRef);
  expect(connection.locked).toBe(true);
  connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "unlock",
    ref: firstRef,
    passphrase,
  });
  expect(connection.locked).toBe(false);
  await page.reload();
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await panel.getByText("Connection settings", { exact: true }).click();
  await panel
    .getByRole("button", { name: "Lock connection", exact: true })
    .click();
  await expect(panel.getByRole("status")).toContainText("Locked");
  expect((await privateCLI(["ai-connection"])).locked).toBe(true);
  connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    ref: firstRef,
    key: key + "-replacement",
    passphrase,
  });
  expect(connection.ref).not.toBe(firstRef);
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "remove",
    ref: connection.ref,
    passphrase,
  });
  await page.reload();
  await expect(panel.getByRole("status")).toHaveText("No connection saved");
  agents = await privateCLI(["agents"]);
  expect(agents.aiRequests[0].state).toBe("completed");
  expect(agents.providerCallsEnabled).toBe(false);
  expect(output).not.toContain(key);
  await page.screenshot({
    path: `.evidence/p5-openai-connection/${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("optional provider fixture requires scoped disclosure and budgets, preserves review and never creates a task", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/chat");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page
    .getByLabel("Conversation name")
    .fill("Provider fixture conversation");
  await page.getByRole("button", { name: "Create conversation" }).click();
  await expect(
    page.getByRole("heading", {
      name: "Provider fixture conversation",
      exact: true,
    }),
  ).toBeVisible();
  const panel = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(page, panel.getByLabel("Message", { exact: true }), "");
  await panel
    .getByLabel("Message", { exact: true })
    .fill("Compare a bounded paper strategy");
  await panel.getByRole("button", { name: "Send" }).click();
  await expect(panel.getByText("prepared", { exact: true })).toBeVisible();
  const run = panel.getByRole("button", { name: "Run development fixture" });
  await expect(run).toBeDisabled();
  await panel.getByText("Exact context disclosure", { exact: true }).click();
  await expect(panel.locator("pre")).toContainText('"store":false');
  await revealControl(
    page,
    panel.getByLabel("I reviewed this context and these fixture limits"),
    "",
  );
  await panel
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await run.click();
  await expect(panel.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  const card = panel.getByRole("region", { name: "Review paper journal" });
  await card.getByRole("button", { name: "Keep draft" }).click();
  await expect(card).toContainText("kept");
  const view = await privateCLI(["agents"]);
  expect(view.aiRequests).toHaveLength(1);
  expect(view.tasks).toHaveLength(0);
  expect(view.aiRequests[0].heldMicrousd).toBe(0);
  expect(view.providerCallsEnabled).toBe(false);
  const request = {
    revision: view.revision,
    action: "ai-prepare",
    profileId: view.profileId,
    id: view.sessions[0].id,
    text: "Cancelled preview",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  };
  const next = await privateCLI(["agents", "--request-stdin"], request);
  await privateCLI(["agents", "--request-stdin"], {
    revision: next.revision,
    action: "ai-cancel",
    profileId: view.profileId,
    id: next.aiRequests[1].id,
  });
  await stop();
  await start();
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/chat");
  await expect(page.getByText("completed", { exact: true })).toBeVisible();
  await expect(page.getByText("cancelled", { exact: true })).toBeVisible();
  const saved = await privateCLI(["agents"]);
  expect(saved.aiFixtureAvailable).toBe(false);
  expect(saved.aiRequests[0].cards[0].review).toBe("kept");
  expect(saved.tasks).toHaveLength(0);
  const profileName = saved.profiles.find(
    (p: { id: string }) => p.id === saved.profileId,
  ).name;
  const sessionName = "Provider fixture conversation";
  const question = saved.aiRequests[0].prompt;
  const answer = "No model, market feed or wallet was contacted.";
  const checkSearch = async () => {
    for (const query of [question.toUpperCase(), answer.toUpperCase()]) {
      await revealControl(page, page.getByLabel("Search conversations"), "");
      await page.getByLabel("Search conversations").fill(query);
      await expect(
        page.locator(".agent-session-list").getByRole("button", {
          name: sessionName,
          exact: true,
        }),
      ).toBeVisible();
      const hit = await privateCLI([
        "agents",
        "--profile",
        saved.profileId,
        "--search",
        query,
      ]);
      expect(hit.sessions.map((s: { id: string }) => s.id)).toEqual([
        saved.aiRequests[0].sessionId,
      ]);
      const other = await privateCLI([
        "agents",
        "--profile",
        "news-researcher",
        "--search",
        query,
      ]);
      expect(other.sessions).toHaveLength(0);
      expect(other.aiRequests).toHaveLength(0);
      expect(hit.providerCallsEnabled).toBe(false);
    }
  };
  await checkSearch();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await revealControl(page, page.getByLabel("Search conversations"), "");
  await page.getByLabel("Search conversations").fill(answer);
  await expect(page.locator(".agent-session-list button")).toHaveCount(0);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: profileName });
  await page.getByRole("button", { name: "settings", exact: true }).click();
  await revealControl(page, page.getByLabel("Provider preference"), "");
  await page.getByLabel("Provider preference").selectOption("anthropic");
  await revealControl(page, page.getByLabel("Model preference"), "");
  await page.getByLabel("Model preference").fill("preference-only");
  await page.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect
    .poll(async () => {
      const current = await privateCLI([
        "agents",
        "--profile",
        saved.profileId,
      ]);
      return current.profiles.find(
        (p: { id: string }) => p.id === saved.profileId,
      ).provider;
    })
    .toBe("anthropic");
  await page.getByRole("button", { name: "chat", exact: true }).click();
  await checkSearch();
  await revealControl(page, page.getByLabel("Search conversations"), "");
  await page.getByLabel("Search conversations").fill("no-such-phrase");
  await expect(page.locator(".agent-session-list button")).toHaveCount(0);
  await revealControl(page, page.getByLabel("Search conversations"), "");
  await page.getByLabel("Search conversations").fill("");
  await mkdir(".evidence/p5-ai-search", { recursive: true });
  await page.screenshot({
    path: `.evidence/p5-ai-search/search-${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("bounded AI schedules share consent and execution with CLI, survive restart and keep paper inputs fixed", async ({
  page,
}) => {
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/chat");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page.getByLabel("Conversation name").fill("Scheduled context");
  await page.getByRole("button", { name: "Create conversation" }).click();
  await expect(
    page.getByRole("heading", { name: "Scheduled context", exact: true }),
  ).toBeVisible();
  let v = await privateCLI(["agents"]);
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    profileId: v.profileId,
    action: "bot-create",
    name: "Fixed task paper",
    quantity: 2,
    priceCents: 5,
    budgetCents: 20,
  });
  const bot = v.bots[0].id;
  await page.goto(url + "/#agents/tasks");
  await openAISchedules(page);
  // Hash navigation preserves the UI's cache. Reload after this external CLI
  // write so the task review uses the same current records.
  await page.reload();
  await openAISchedules(page);
  await openAISchedules(page);
  const region = page.getByRole("region", {
    name: "Scheduled AI steps",
    exact: true,
  });
  await expect(
    region.getByLabel("Step after validated AI response").getByRole("option", {
      name: "Fixed paper evaluation · Fixed task paper",
    }),
  ).toHaveCount(1);
  await revealControl(page, region.getByLabel("AI task title"), "");
  await region.getByLabel("AI task title").fill("Two bounded AI evaluations");
  await revealControl(page, region.getByLabel("AI analysis prompt"), "");
  await region
    .getByLabel("AI analysis prompt")
    .fill("Compare fixed paper inputs");
  await revealControl(
    page,
    region.getByLabel("Step after validated AI response"),
    "",
  );
  await region.getByLabel("Step after validated AI response").selectOption(bot);
  await region.getByRole("button", { name: "Prepare AI task review" }).click();
  const task = region.getByRole("article", {
    name: "AI task Two bounded AI evaluations",
  });
  await expect(task.getByText("review", { exact: true })).toBeVisible();
  const authorize = task.getByRole("button", {
    name: "Authorize fixture schedule",
  });
  await expect(authorize).toBeDisabled();
  await task.getByText("Review AI task disclosure").click();
  await expect(task.locator("pre")).toContainText('"store":false');
  await revealControl(
    page,
    task.getByLabel("Allow this frozen context, schedule and limits"),
    "",
  );
  await task
    .getByLabel("Allow this frozen context, schedule and limits")
    .check();
  await authorize.click();
  await expect(task.getByText("scheduled", { exact: true })).toBeVisible();
  await task.getByRole("button", { name: "Evaluate now" }).click();
  await page.goto(url + "/#overview");
  await expect
    .poll(async () => {
      v = await privateCLI(["agents"]);
      return v.tasks[0].runs;
    })
    .toBe(1);
  expect(v.bots[0].spentCents).toBe(10);
  expect(v.aiRequests).toHaveLength(1);
  const taskId = v.tasks[0].id;
  await stop();
  await start(true);
  v = await privateCLI(["agents"]);
  await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    profileId: v.profileId,
    action: "task-run",
    id: taskId,
  });
  await expect
    .poll(async () => {
      v = await privateCLI(["agents"]);
      return v.tasks[0].state;
    })
    .toBe("completed");
  expect(v.bots[0].spentCents).toBe(20);
  expect(v.runs).toHaveLength(2);
  expect(v.aiRequests).toHaveLength(2);
  expect(v.aiRequests[0].id).not.toBe(v.aiRequests[1].id);
  expect(v.tasks).toHaveLength(1);
  expect(v.providerCallsEnabled).toBe(false);
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/tasks");
  await openAISchedules(page);
  await expect(task.getByText("completed", { exact: true })).toBeVisible();
  await expect(task).toContainText("2/2 evaluations");
  await expect(task).toContainText(
    "No model, market feed or wallet was contacted.",
  );
  await revealControl(page, region.getByLabel("AI task title"), "");
  await region.getByLabel("AI task title").fill("Cancelled schedule");
  await revealControl(page, region.getByLabel("AI analysis prompt"), "");
  await region.getByLabel("AI analysis prompt").fill("No call needed");
  await region.getByRole("button", { name: "Prepare AI task review" }).click();
  const cancelled = region.getByRole("article", {
    name: "AI task Cancelled schedule",
  });
  await cancelled.getByRole("button", { name: "Cancel AI schedule" }).click();
  await expect(cancelled.getByText("cancelled", { exact: true })).toBeVisible();
  await mkdir(".evidence/p5-ai-tasks", { recursive: true });
  await page.screenshot({
    path: `.evidence/p5-ai-tasks/tasks-${test.info().project.name}.png`,
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});

test("OpenAI model admission binds token and tariff evidence across UI CLI tasks and restart", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(page.getByText("Fixture", { exact: true })).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    key: "fased-test-model-admission-browser-key",
    passphrase: "disposable-admission-vault-passphrase",
  });
  expect(connection.testOnly).toBe(true);
  expect(connection.liveCallsEnabled).toBe(false);
  await page.goto(url + "/#agents/chat");
  await revealControl(
    page,
    page.getByLabel("Conversation name"),
    "Conversation name",
  );
  await page.getByLabel("Conversation name").fill("Admitted model review");
  await page.getByRole("button", { name: "Create conversation" }).click();
  const review = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(
    page,
    review.getByRole("combobox", { name: "AI connection", exact: true }),
  );
  await review
    .getByRole("combobox", { name: "AI connection", exact: true })
    .selectOption(connection.ref);
  await revealControl(
    page,
    review.getByRole("combobox", { name: "AI model", exact: true }),
  );
  await review
    .getByRole("combobox", { name: "AI model", exact: true })
    .selectOption(connection.admissionPolicy.model);
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Review the admitted model using controlled data");
  await review.getByRole("button", { name: "Send" }).click();
  // An intentionally expired compiled tariff must refuse, including on future CI dates.
  if (Date.now() >= connection.admissionPolicy.expiresAt * 1000) {
    await expect(
      page.getByText(/model, token or tariff policy unavailable/),
    ).toBeVisible();
    expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
    return;
  }
  await revealControl(
    page,
    review.getByText("Model and tariff admission", { exact: true }),
  );
  await review.getByText("Model and tariff admission", { exact: true }).click();
  await expect(review).toContainText(
    "input $0.40, cached input $0.10, output $1.60",
  );
  await expect(review).toContainText("All three API operations are simulated");
  await revealControl(
    page,
    review.getByLabel("I reviewed this context and these fixture limits"),
    "",
  );
  await review
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await review.getByRole("button", { name: "Run development fixture" }).click();
  await expect(review.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  let view = await privateCLI(["agents"]);
  const manual = view.aiRequests[0];
  expect(manual.model).toBe(connection.admissionPolicy.model);
  expect(manual.admission.inputTokens).toBe(256);
  expect(manual.admission.generationAttempted).toBe(true);
  expect(manual.admission.wireSha256).toMatch(/^[a-f0-9]{64}$/);
  expect(manual.spentMicrousd).toBe(308);
  expect(manual.heldMicrousd).toBe(0);
  expect(
    (await privateCLI(["agents", "--profile", "news-researcher"])).aiRequests,
  ).toHaveLength(0);
  await page.goto(url + "/#agents/tasks");
  await openAISchedules(page);
  const scheduled = page.getByRole("region", {
    name: "Scheduled AI steps",
    exact: true,
  });
  await revealControl(
    page,
    scheduled.getByRole("combobox", { name: "AI connection", exact: true }),
  );
  await scheduled
    .getByRole("combobox", { name: "AI connection", exact: true })
    .selectOption(connection.ref);
  await revealControl(
    page,
    scheduled.getByRole("combobox", { name: "AI model", exact: true }),
  );
  await scheduled
    .getByRole("combobox", { name: "AI model", exact: true })
    .selectOption(connection.admissionPolicy.model);
  await revealControl(
    page,
    scheduled.getByLabel("AI task title", { exact: true }),
    "",
  );
  await scheduled
    .getByLabel("AI task title", { exact: true })
    .fill("Admitted UI task");
  await revealControl(
    page,
    scheduled.getByLabel("AI analysis prompt", { exact: true }),
    "",
  );
  await scheduled
    .getByLabel("AI analysis prompt", { exact: true })
    .fill("Review the frozen controlled context");
  await revealControl(
    page,
    scheduled.getByLabel("AI run count", { exact: true }),
    "",
  );
  await scheduled.getByLabel("AI run count", { exact: true }).fill("1");
  await scheduled
    .getByRole("button", { name: "Prepare AI task review" })
    .click();
  const task = page.getByRole("article", {
    name: "AI task Admitted UI task",
    exact: true,
  });
  await expect(task).toBeVisible();
  await task.getByText("Review AI task disclosure", { exact: true }).click();
  await expect(task).toContainText(connection.admissionPolicy.model);
  await revealControl(
    page,
    task.getByLabel("Allow this frozen context, schedule and limits"),
    "",
  );
  await task
    .getByLabel("Allow this frozen context, schedule and limits")
    .check();
  await task
    .getByRole("button", { name: "Authorize fixture schedule" })
    .click();
  await task.getByRole("button", { name: "Evaluate now" }).click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].state)
    .toBe("completed");
  view = await privateCLI(["agents"]);
  expect(view.aiRequests).toHaveLength(2);
  expect(view.aiRequests[1].admission.state).toBe("admitted");
  expect(view.runs[0].aiRequestId).toBe(view.aiRequests[1].id);
  await stop();
  await start();
  await page.goto(url);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  view = await privateCLI(["agents"]);
  expect(view.aiRequests[0].admission).toEqual(manual.admission);
  expect(view.aiRequests[0].policy).toEqual(manual.policy);
  expect((await privateCLI(["ai-connection"])).locked).toBe(true);
  expect(view.providerCallsEnabled).toBe(false);
  expect(view.financialExecution).toBe(false);
  await page.goto(url + "/#agents/chat");
  await revealControl(
    page,
    page.getByText("Model and tariff admission", { exact: true }).first(),
  );
  await page
    .getByText("Model and tariff admission", { exact: true })
    .first()
    .click();
  await expect(
    page.getByRole("region", { name: "Optional AI request review" }),
  ).toContainText("Counted 256 input tokens");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await mkdir(resolve(".evidence/p5-admission"), { recursive: true });
  await page.screenshot({
    path: resolve(".evidence/p5-admission", test.info().project.name + ".png"),
    fullPage: true,
  });
});

test("WEN paper journey shares UI and CLI review, cancel and restart receipts without live authority", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url + "/#terminals/satcoin");
  const journey = page.getByRole("region", { name: "WEN paper journey" });
  await revealControl(page, journey.getByLabel("Paper entry name"), "");
  await journey.getByLabel("Paper entry name").fill("UI range C");
  await revealControl(page, journey.getByLabel("Paper SOL budget"), "");
  await journey.getByLabel("Paper SOL budget").fill("0.25");
  await journey
    .getByRole("button", { name: "Prepare paper review", exact: true })
    .click();
  const card = journey.getByRole("article", { name: "Paper entry UI range C" });
  await expect(card).toContainText("review · 0.25 SOL");
  let v = await privateCLI(["agents"]);
  expect(v.tasks).toHaveLength(1);
  expect(v.runs).toHaveLength(0);
  expect(v.tasks[0].wen.budgetSOL).toBe("0.25");
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  const command = async (c: object) => {
    const prior = await privateCLI(["agents"]);
    return privateCLI(["agents", "--request-stdin"], {
      revision: prior.revision,
      ...c,
    });
  };
  const task = v.tasks[0];
  v = await command({
    action: "wen-paper-approve",
    id: task.id,
    consentDigest: task.wen.digest,
  });
  v = await command({ action: "task-pause", id: task.id });
  expect(v.tasks[0].state).toBe("paused");
  await stop();
  await start();
  await page.goto(url + "/#terminals/satcoin");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(card).toContainText("paused · 0.25 SOL");
  await card
    .getByRole("button", { name: "Resume paper: UI range C", exact: true })
    .click();
  await card
    .getByRole("button", { name: "Run paper once: UI range C", exact: true })
    .click();
  await expect(card).toContainText("completed · 0.25 SOL");
  const receipt = journey.getByRole("article", { name: "WEN paper receipt" });
  await expect(receipt).toContainText("Paper pending · unsettled");
  await receipt.getByText("Receipt evidence", { exact: true }).click();
  v = await privateCLI(["agents"]);
  expect(v.runs).toHaveLength(1);
  expect(v.notices).toHaveLength(1);
  const result = v.runs[0].wen;
  expect(result.receipt.amount).toBe("250000000");
  expect(result.receipt.settled).toBe(false);
  expect(result.receipt.sat).toBe("0");
  expect(result.receipt.principal).toBe("0");
  expect(result.receipt.outcome).toBeNull();
  expect(result.planDigest).toBe(task.wen.digest);
  expect(
    result.instructions.map((i: { operation: string }) => i.operation),
  ).toEqual(["open", "commit", "reveal"]);
  await expect(receipt).toContainText(result.receiptSha256);
  v = await command({
    action: "wen-paper-prepare",
    name: "CLI cancel",
    wen: { budgetSOL: "0.01", selection: "0", validForSeconds: 300 },
  });
  await page.reload();
  await journey
    .getByRole("button", { name: "Cancel paper: CLI cancel", exact: true })
    .click();
  await expect(
    journey.getByRole("article", { name: "Paper entry CLI cancel" }),
  ).toContainText("cancelled");
  await revealControl(page, journey.getByLabel("Paper agent profile"), "");
  await journey
    .getByLabel("Paper agent profile")
    .selectOption("news-researcher");
  await expect(receipt).toHaveCount(0);
  await expect(card).toHaveCount(0);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.tasks).toHaveLength(0);
  expect(other.runs).toHaveLength(0);
  await revealControl(page, journey.getByLabel("Paper agent profile"), "");
  await journey.getByLabel("Paper agent profile").selectOption("wen-operator");
  await expect(card).toBeVisible();
  await stop();
  await start();
  await page.goto(url + "/#terminals/satcoin");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    journey.getByRole("article", { name: "Paper entry CLI cancel" }),
  ).toContainText("cancelled");
  await expect(receipt).toHaveCount(1);
  v = await privateCLI(["agents"]);
  expect(v.runs).toHaveLength(1);
  expect(v.runs[0].wen.receiptSha256).toBe(result.receiptSha256);
  await expect(
    page.getByRole("button", { name: "Execution unavailable", exact: true }),
  ).toBeDisabled();
  expect(
    await card
      .locator("p")
      .first()
      .evaluate((e) => e.getBoundingClientRect().width),
  ).toBeGreaterThan(180);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await mkdir(".evidence/wen-paper", { recursive: true });
  await page.screenshot({
    path: `.evidence/wen-paper/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("WEN paper context hands off UI and CLI to frozen same-profile chat with optional consented fixture AI", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  const command = async (c: object) => {
    const v = await privateCLI(["agents"]);
    return privateCLI(["agents", "--request-stdin"], {
      revision: v.revision,
      ...c,
    });
  };
  let v = await command({
    action: "wen-paper-prepare",
    name: "UI explained entry",
    wen: { budgetSOL: "0.25", selection: "2", validForSeconds: 300 },
  });
  const task = v.tasks[0];
  await command({
    action: "wen-paper-approve",
    id: task.id,
    consentDigest: task.wen.digest,
  });
  v = await command({ action: "task-run", id: task.id });
  const receiptHash = v.runs[0].wen.receiptSha256;
  await page.goto(url + "/#terminals/satcoin");
  await page
    .getByRole("button", {
      name: "Explain this paper entry: UI explained entry",
      exact: true,
    })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  const snapshot = page
    .locator(".local-messages article")
    .filter({ hasText: "Frozen WEN paper context v1" });
  await expect(snapshot).toContainText(receiptHash);
  await expect(snapshot).toContainText("not current state");
  v = await privateCLI(["agents"]);
  const originalText = v.messages[0].text;
  expect(await snapshot.locator("p").textContent()).toBe(originalText);
  expect(v.aiRequests).toHaveLength(0);
  expect(v.tasks[0].state).toBe("completed");
  expect(v.runs).toHaveLength(1);
  const review = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Explain these frozen paper facts and their limitations");
  await review.getByRole("button", { name: /Send|Prepare/ }).click();
  let request = review.locator(".ai-request").last();
  await request.locator("summary").first().click();
  await expect(request.locator("pre")).toContainText(task.wen.digest);
  await expect(
    request.getByRole("button", {
      name: "Run development fixture",
      exact: true,
    }),
  ).toBeDisabled();
  await request.getByRole("button", { name: "Stop", exact: true }).click();
  v = await privateCLI(["agents"]);
  expect(v.aiRequests[0].state).toBe("cancelled");
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill(
      "Explain the captured range and pending receipt, without predicted rewards",
    );
  await review.getByRole("button", { name: /Send|Prepare/ }).click();
  request = review.locator(".ai-request").last();
  await revealControl(
    page,
    request.getByLabel("I reviewed this context and these fixture limits"),
    "",
  );
  await request
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await request
    .getByRole("button", { name: "Run development fixture", exact: true })
    .click();
  await expect
    .poll(async () => {
      v = await privateCLI(["agents"]);
      return v.aiRequests.at(-1)?.state;
    })
    .toBe("completed");
  expect(v.messages[0].text).toBe(originalText);
  expect(v.runs).toHaveLength(1);
  expect(v.bots).toHaveLength(0);
  expect(v.tasks[0].wen.approved).toBe(true);
  v = await command({
    action: "wen-paper-prepare",
    name: "CLI explained entry",
    wen: { budgetSOL: "0.02", selection: "0", validForSeconds: 300 },
  });
  const pending = v.tasks[1];
  const stale = v.revision;
  v = await command({ action: "wen-paper-explain", id: pending.id });
  const cliText = v.messages.at(-2).text;
  await expect(
    privateCLI(["agents", "--request-stdin"], {
      revision: stale,
      action: "wen-paper-explain",
      id: pending.id,
    }),
  ).rejects.toThrow();
  await expect(
    command({
      action: "wen-paper-explain",
      id: pending.id,
      profileId: "news-researcher",
    }),
  ).rejects.toThrow();
  await command({ action: "wen-paper-cancel", id: pending.id });
  await command({
    action: "profile-update",
    name: "WEN snapshot operator",
    text: "Keep scoped history",
    provider: "xai",
    model: "preference-only",
  });
  await stop();
  await start(true);
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
  await expect(snapshot).toContainText("Captured task state: review");
  expect(await snapshot.locator("p").textContent()).toBe(cliText);
  v = await privateCLI(["agents"]);
  expect(v.tasks[1].state).toBe("cancelled");
  expect(v.messages[0].text).toBe(originalText);
  expect(v.aiRequests).toHaveLength(2);
  expect(v.runs).toHaveLength(1);
  expect(v.notices).toHaveLength(1);
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.messages).toHaveLength(0);
  expect(other.sessions).toHaveLength(0);
  expect(
    await snapshot
      .locator("p")
      .evaluate((e) => e.getBoundingClientRect().width),
  ).toBeGreaterThan(160);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  expect(external).toEqual([]);
  await mkdir(".evidence/wen-paper-chat", { recursive: true });
  await page.screenshot({
    path: `.evidence/wen-paper-chat/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("chat reminder drafts require shared UI and CLI review before scheduling and survive restart without AI", async ({
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) => {
    const v = await read();
    return privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: v.revision,
      ...c,
    });
  };
  let v = await command({
    action: "session-create",
    name: "Reminder planning",
  });
  const sessionId = v.sessions[0].id;
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url + "/#agents/chat");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const ai = page.getByRole("region", { name: "Optional AI request review" });
  await revealControl(page, ai.getByLabel("Message", { exact: true }), "");
  await ai
    .getByLabel("Message", { exact: true })
    .fill("Draft a reminder to review saved research");
  await ai.getByRole("button", { name: /Send|Prepare/ }).click();
  const request = ai.locator(".ai-request").last();
  await revealControl(
    page,
    request.getByLabel("I reviewed this context and these fixture limits"),
    "",
  );
  await request
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await request
    .getByRole("button", { name: "Run development fixture", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).aiRequests[0]?.state)
    .toBe("completed");
  const card = request.locator(".ai-action-card");
  await card.getByRole("button", { name: "Keep draft", exact: true }).click();
  expect((await read()).tasks).toHaveLength(0);
  await card
    .getByRole("button", { name: "Review reminder task", exact: true })
    .click();
  const review = card.getByRole("region", { name: /Reminder task review:/ });
  await revealControl(
    page,
    review.getByLabel("Reminder task name", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reminder task name", { exact: true })
    .fill("Review my research");
  await revealControl(
    page,
    review.getByLabel("Reminder text", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reminder text", { exact: true })
    .fill("Read the saved headlines and my notes");
  await revealControl(page, review.getByLabel("Repeat every (seconds)"), "");
  await review.getByLabel("Repeat every (seconds)").fill("3600");
  await revealControl(page, review.getByLabel("Maximum reminder runs"), "");
  await review.getByLabel("Maximum reminder runs").fill("2");
  await review
    .getByRole("button", { name: "Review reminder schedule", exact: true })
    .click();
  const create = review.getByRole("button", {
    name: "Create reviewed reminder",
    exact: true,
  });
  await expect(create).toBeDisabled();
  await expect(review).toContainText("No task has been created");
  expect((await read()).tasks).toHaveLength(0);
  await review
    .getByRole("button", { name: "Cancel reminder review", exact: true })
    .click();
  expect((await read()).tasks).toHaveLength(0);
  await card
    .getByRole("button", { name: "Review reminder task", exact: true })
    .click();
  await review
    .getByRole("button", { name: "Review reminder schedule", exact: true })
    .click();
  await expect(review).toContainText("No task has been created");
  await command({
    action: "memory-put",
    text: "An unrelated owner note",
    source: "owner",
  });
  await expect(review).toContainText("Workspace changed or review expired");
  await expect(create).toBeDisabled();
  await review
    .getByRole("button", { name: "Review reminder schedule", exact: true })
    .click();
  await revealControl(
    page,
    review.getByLabel("I reviewed this reminder text and schedule"),
    "",
  );
  await review.getByLabel("I reviewed this reminder text and schedule").check();
  await revealControl(
    page,
    review.getByLabel("Reminder text", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reminder text", { exact: true })
    .fill("Edited: read saved research, no external actions");
  await expect(create).toHaveCount(0);
  await review
    .getByRole("button", { name: "Review reminder schedule", exact: true })
    .click();
  await revealControl(
    page,
    review.getByLabel("I reviewed this reminder text and schedule"),
    "",
  );
  await review.getByLabel("I reviewed this reminder text and schedule").check();
  await create.click();
  await expect(card.getByRole("status")).toContainText(
    "Reminder task created: Review my research",
  );
  v = await read();
  expect(v.tasks).toHaveLength(1);
  expect(v.runs).toHaveLength(0);
  const first = v.tasks[0];
  expect(first.text).toBe("Edited: read saved research, no external actions");
  expect(first.intervalSeconds).toBe(3600);
  expect(first.maxRuns).toBe(2);
  expect(first.botId).toBe("");
  expect(first.ai).toBeUndefined();
  expect(first.news).toBeUndefined();
  expect(first.wen).toBeUndefined();
  await command({ action: "task-pause", id: first.id });
  await page.goto(url + "/#agents/tasks");
  await expect(
    page.getByRole("button", {
      name: "Resume: Review my research",
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Resume: Review my research", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Run now: Review my research", exact: true })
    .click();
  await expect.poll(async () => (await read()).runs.length).toBe(1);
  v = await command({ action: "task-pause", id: first.id });
  expect(v.notices).toHaveLength(1);
  expect(v.aiRequests).toHaveLength(1);
  v = await command({
    action: "ai-prepare",
    id: sessionId,
    text: "Draft another reminder",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const secondRequest = v.aiRequests.at(-1);
  await command({
    action: "ai-start",
    id: secondRequest.id,
    consentDigest: secondRequest.digest,
  });
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  v = await read();
  const source = v.aiRequests.at(-1).cards[0];
  const stale = v.revision;
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Scoped research",
    provider: "xai",
    model: "preference-only",
  });
  await stop();
  await start(false);
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
  const proposal = {
    action: "ai-reminder-review",
    id: source.id,
    name: "CLI reviewed reminder",
    text: "Follow up on saved notes",
    intervalSeconds: 600,
    maxRuns: 1,
  };
  await expect(
    privateCLI(["agents", "--request-stdin"], {
      ...proposal,
      profileId,
      revision: stale,
    }),
  ).rejects.toThrow();
  await expect(
    command({ ...proposal, profileId: "news-researcher" }),
  ).rejects.toThrow();
  const before = await read();
  v = await command(proposal);
  expect(v.revision).toBe(before.revision);
  expect(v.tasks).toHaveLength(1);
  const reviewed = v.reminderReview;
  const creation = {
    ...proposal,
    action: "ai-reminder-create",
    expiresAt: reviewed.expiresAt,
    consentDigest: reviewed.digest,
  };
  await expect(
    command({ ...creation, text: "unreviewed edit" }),
  ).rejects.toThrow();
  v = await command(creation);
  expect(v.tasks).toHaveLength(2);
  await expect(command(creation)).rejects.toThrow(/already created/);
  expect(v.tasks[1].state).toBe("scheduled");
  expect(v.tasks[1].text).toBe(proposal.text);
  // Synchronize the external CLI write through the existing profile refresh.
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByText("Reminder task created: CLI reviewed reminder.", {
      exact: true,
    }),
  ).toBeVisible();
  await command({ action: "task-run", id: v.tasks[1].id });
  await expect(
    command({ action: "task-run", id: v.tasks[1].id }),
  ).rejects.toThrow();
  v = await read();
  expect(v.runs).toHaveLength(2);
  expect(v.notices).toHaveLength(2);
  expect(v.aiRequests).toHaveLength(2);
  expect(v.aiFixtureAvailable).toBe(false);
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  expect(v.bots).toHaveLength(0);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.tasks).toHaveLength(0);
  expect(other.aiRequests).toHaveLength(0);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
  await mkdir(".evidence/chat-reminders", { recursive: true });
  await page.screenshot({
    path: `.evidence/chat-reminders/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
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
  await expect(
    review.getByLabel("Reviewed memory note", { exact: true }),
  ).toHaveValue(message.text);
  await revealControl(
    page,
    review.getByLabel("Reviewed memory note", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reviewed memory note", { exact: true })
    .fill("Cancelled draft");
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
    review.getByRole("button", { name: "Save reviewed memory", exact: true }),
  ).toBeDisabled();
  await review
    .getByRole("button", { name: "Cancel memory", exact: true })
    .click();
  await source
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  await revealControl(
    page,
    review.getByLabel("Reviewed memory note", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reviewed memory note", { exact: true })
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
    .getByRole("button", { name: "Save reviewed memory", exact: true })
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
    answerReview.getByLabel("Reviewed memory note", { exact: true }),
    "",
  );
  await answerReview
    .getByLabel("Reviewed memory note", { exact: true })
    .fill("Owner interpretation of the fixture answer");
  await answerReview
    .getByRole("button", { name: "Save reviewed memory", exact: true })
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
    provider: "xai",
    model: "preference-only",
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
  await memory
    .getByRole("button", { name: "Delete note", exact: true })
    .click();
  await expect(memory).toHaveCount(0);
  v = await read();
  expect(v.messages[0]).toEqual(message);
  expect(v.aiRequests).toHaveLength(1);
  expect(v.tasks).toHaveLength(0);
  expect(v.runs).toHaveLength(0);
  expect(v.notices).toHaveLength(0);
  expect(v.providerCallsEnabled).toBe(false);
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
  await page.screenshot({
    path: `.evidence/chat-memory/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("task run chat shares historical UI and CLI capture, explicit fixture explanation and restart", async ({
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  let v = await command({
    action: "task-create",
    name: "Journal review",
    text: "Compare retained sources",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  v = await command({ action: "task-run", id: v.tasks[0].id });
  const plain = v.runs[0];
  const source = {
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  };
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "history", exact: true }).click();
  await page
    .locator(".memory-card")
    .filter({ hasText: `Run ${plain.id}` })
    .getByRole("button", { name: "Discuss run", exact: true })
    .click();
  await expect(page.locator(".local-messages")).toContainText(
    "Historical reminder run",
  );
  v = await read();
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(source);
  const snapshot = v.messages.at(-1);
  const [body, hash] = snapshot.text.split("\nSnapshot SHA-256: ");
  expect(hash).toBe(createHash("sha256").update(body).digest("hex"));
  expect(snapshot.text).toContain(plain.id);
  expect(snapshot.text).toContain("Compare retained sources");
  expect(v.aiRequests).toHaveLength(0);
  const stale = {
    action: "task-run-explain",
    profileId,
    revision: v.revision - 1,
    id: plain.id,
  };
  await expect(
    privateCLI(["agents", "--request-stdin"], stale),
  ).rejects.toThrow();
  await expect(
    command({
      action: "task-run-explain",
      profileId: "news-researcher",
      id: plain.id,
    }),
  ).rejects.toThrow();
  expect((await read()).messages).toHaveLength(1);
  // The new ordinary message reuses reviewed memory; opening alone saves nothing.
  await page
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const review = page.getByRole("region", { name: "Chat memory review" });
  expect((await read()).memories).toHaveLength(0);
  await revealControl(
    page,
    review.getByLabel("Reviewed memory note", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reviewed memory note", { exact: true })
    .fill("Review saved reminder outcomes before planning");
  await review
    .getByRole("button", { name: "Save reviewed memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  // Prepare disclosure is inert; consent is a separate command.
  v = await command({
    action: "ai-prepare",
    id: snapshot.sessionId,
    text: "Explain this historical run",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const explanation = v.aiRequests.at(-1);
  expect(explanation.state).toBe("prepared");
  expect(explanation.context).toContain("Historical reminder run");
  await command({
    action: "ai-start",
    id: explanation.id,
    consentDigest: explanation.digest,
  });
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  // A completed fixture-AI reminder also has a discussable saved result.
  v = await command({
    action: "task-ai-prepare",
    id: snapshot.sessionId,
    name: "Fixture reminder",
    text: "Review the recorded journal",
    intervalSeconds: 60,
    maxRuns: 1,
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
    totalBudgetMicrousd: 30000,
    authorizationSeconds: 3600,
  });
  const task = v.tasks.at(-1);
  await command({
    action: "task-ai-authorize",
    id: task.id,
    consentDigest: task.ai.digest,
  });
  await command({ action: "task-run", id: task.id });
  await expect.poll(async () => (await read()).runs.length).toBe(2);
  v = await read();
  const run = v.runs.at(-1);
  const retained = {
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  };
  v = await command({ action: "task-run-explain", id: run.id });
  const captured = v.messages.at(-1);
  expect(captured.text).toContain(run.aiRequestId);
  expect(captured.text).toContain("Limits");
  expect(captured.text).not.toContain("ConnectionRef");
  expect(captured.text).not.toContain("Disclosure");
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(retained);
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Changed persona",
    provider: "xai",
    model: "preference-only",
  });
  await stop();
  await start();
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
  await expect(page.locator(".local-messages")).toContainText(captured.text);
  v = await read();
  expect(v.messages.at(-1)).toEqual(captured);
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(retained);
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.messages).toHaveLength(0);
  expect(other.sessions).toHaveLength(0);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/discuss-run/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("news chat shares saved headline UI and CLI capture, reviewed memory, explicit fixture explanation and restart", async ({
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
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
  await command({ action: "session-create", name: "Older conversation" });
  const external: string[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).origin !== new URL(url).origin) external.push(r.url());
  });
  await page.goto(url + "/#agents/chat");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "Older conversation", exact: true })
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
    .fill("old filter");
  await page.getByRole("button", { name: "news", exact: true }).click();
  const panel = page.getByRole("region", { name: "News inputs", exact: true });
  await revealControl(
    page,
    panel.getByLabel("News source name", { exact: true }),
    "",
  );
  await panel
    .getByLabel("News source name", { exact: true })
    .fill("Controlled headline source");
  await revealControl(
    page,
    panel.getByLabel("Public RSS URL", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Public RSS URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml");
  await revealControl(
    page,
    panel.getByLabel("Source freshness limit (seconds)", { exact: true }),
    "",
  );
  await panel
    .getByLabel("Source freshness limit (seconds)", { exact: true })
    .fill("600");
  await panel
    .getByRole("button", { name: "Add news source", exact: true })
    .click();
  const source = panel.getByRole("article", {
    name: "News source Controlled headline source",
    exact: true,
  });
  await source
    .getByRole("button", { name: "Fetch headlines", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).newsFetches[0]?.state)
    .toBe("completed");
  await panel
    .getByRole("button", { name: "Refresh news records", exact: true })
    .click();
  let v = await read();
  const fetch = v.newsFetches[0],
    item = fetch.items[0];
  const retained = {
    sources: v.newsSources,
    fetches: v.newsFetches,
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    ai: v.aiRequests,
  };
  await source
    .locator(".memory-record")
    .filter({ hasText: item.title })
    .getByRole("button", { name: "Discuss headline", exact: true })
    .click();
  await expect(page.locator(".local-messages")).toContainText(
    "Historical news headline",
  );
  await expect(
    page.getByRole("searchbox", {
      name: "Search conversations",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveValue("");
  v = await read();
  expect({
    sources: v.newsSources,
    fetches: v.newsFetches,
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    ai: v.aiRequests,
  }).toEqual(retained);
  const initial = v.messages.at(-1);
  const [body, hash] = initial.text.split("\nSnapshot SHA-256: ");
  expect(hash).toBe(createHash("sha256").update(body).digest("hex"));
  const snapshot = JSON.parse(body.slice(body.indexOf("\n") + 1));
  expect(snapshot.Headline).toEqual(item);
  expect(snapshot.Evidence.fetchId).toBe(fetch.id);
  expect(snapshot.Evidence.bodySha256).toBe(fetch.bodySha256);
  expect(snapshot.FreshAtCapture).toBe(true);
  expect(snapshot.RetrievalAgeSecondsAtCapture).toBeGreaterThanOrEqual(0);
  expect(snapshot.PublicationAgeSecondsAtCapture).toBeGreaterThanOrEqual(60);
  expect(v.aiRequests).toHaveLength(0);
  await expect(
    privateCLI(["agents", "--request-stdin"], {
      action: "news-item-explain",
      profileId,
      revision: v.revision - 1,
      id: fetch.id,
      source: item.id,
    }),
  ).rejects.toThrow();
  await expect(
    command({
      action: "news-item-explain",
      profileId: "news-researcher",
      id: fetch.id,
      source: item.id,
    }),
  ).rejects.toThrow();
  expect((await read()).messages).toHaveLength(1);
  // Disabling a source cannot erase the ability to discuss its saved history.
  v = await command({ action: "news-source-disable", id: fetch.sourceId });
  const disabled = {
    sources: v.newsSources,
    fetches: v.newsFetches,
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
  };
  v = await command({
    action: "news-item-explain",
    id: fetch.id,
    source: item.id,
  });
  const captured = v.messages.at(-1);
  const historical = JSON.parse(
    captured.text
      .split("\nSnapshot SHA-256: ")[0]
      .split("\n")
      .slice(1)
      .join("\n"),
  );
  expect(historical.FreshAtCapture).toBe(false);
  expect(historical.SourceDisabledAtCapture).toBe(true);
  expect({
    sources: v.newsSources,
    fetches: v.newsFetches,
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
  }).toEqual(disabled);
  await page.reload();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(page.locator(".local-messages")).toContainText(captured.text);
  await page
    .locator(".local-messages")
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const review = page.getByRole("region", { name: "Chat memory review" });
  expect((await read()).memories).toHaveLength(0);
  await revealControl(
    page,
    review.getByLabel("Reviewed memory note", { exact: true }),
    "",
  );
  await review
    .getByLabel("Reviewed memory note", { exact: true })
    .fill(
      "Check the recorded age and original source before interpreting news",
    );
  await review
    .getByRole("button", { name: "Save reviewed memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  v = await command({
    action: "ai-prepare",
    id: captured.sessionId,
    text: "Explain this historical headline",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const q = v.aiRequests.at(-1);
  expect(q.state).toBe("prepared");
  expect(q.context).toContain("Historical news headline");
  expect(q.news).toBeUndefined();
  await command({ action: "ai-start", id: q.id, consentDigest: q.digest });
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  v = await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Changed persona",
    provider: "xai",
    model: "preference-only",
  });
  const requests = v.aiRequests;
  expect({
    sources: v.newsSources,
    fetches: v.newsFetches,
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
  }).toEqual(disabled);
  await stop();
  await start();
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
  await expect(page.locator(".local-messages")).toContainText(captured.text);
  v = await read();
  expect(v.messages.at(-1)).toEqual(captured);
  expect(v.messages[0]).toEqual(initial);
  expect(v.aiRequests).toEqual(requests);
  expect({
    sources: v.newsSources,
    fetches: v.newsFetches,
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
  }).toEqual(disabled);
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.messages).toHaveLength(0);
  expect(other.memories).toHaveLength(0);
  expect(other.newsFetches).toHaveLength(0);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/news-chat/${isMobile ? "mobile" : "desktop"}.png`,
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  await page.getByRole("button", { name: "memory", exact: true }).click();
  const search = async (q: string) => {
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("Trading Analyst");
    await revealControl(
      page,
      page.getByRole("searchbox", { name: "Search memories", exact: true }),
    );
    await page
      .getByRole("searchbox", { name: "Search memories", exact: true })
      .fill(q);
    await page
      .getByRole("button", { name: "Search memory", exact: true })
      .click();
    const result = await recall(q);
    await expect(page.locator("article[aria-label^='Memory ']")).toHaveCount(
      result.memories.length,
    );
    return result;
  };
  const stable = await read();
  const result = await search("  CONCISE  ");
  expect(result.memories.map((m: { id: string }) => m.id)).toEqual([note.id]);
  await page
    .getByRole("button", { name: "Inspect source", exact: true })
    .click();
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
    .getByRole("button", { name: "Open conversation", exact: true })
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
  await page.getByRole("button", { name: "memory", exact: true }).click();
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
  await page.getByText("1 previous revision(s)", { exact: true }).click();
  await expect(page.locator("article[aria-label^='Memory ']")).toContainText(
    "Edited concise preference",
  );
  await page
    .getByRole("button", { name: "Inspect source", exact: true })
    .click();
  await expect(inspection).toContainText(message.text);
  expect((await recall("", note.id)).source).toEqual(source);
  for (const [query, status] of [
    ["Expired calendar", "Free-form source"],
    ["Invalid reference", "Invalid source reference"],
    ["Changed reference", "Recorded source text changed"],
  ]) {
    await search(query);
    await page
      .getByRole("button", { name: "Inspect source", exact: true })
      .click();
    await expect(inspection).toContainText(status);
    await expect(
      inspection.getByRole("button", {
        name: "Open conversation",
        exact: true,
      }),
    ).toHaveCount(0);
    if (query.startsWith("Expired"))
      await expect(
        page.locator("article[aria-label^='Memory ']"),
      ).toContainText("Expired");
  }
  await search("Answer evidence");
  await page
    .getByRole("button", { name: "Inspect source", exact: true })
    .click();
  await expect(inspection).toContainText(answer.answer);
  expect((await recall("", answerNote.id)).source.kind).toBe("answer");
  const beforeRestart = await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Updated persona",
    provider: "xai",
    model: "preference-only",
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
  await page
    .getByRole("button", { name: "Inspect source", exact: true })
    .click();
  await expect(inspection).toContainText(answer.answer);
  expect((await recall("", note.id)).source).toEqual(source);
  expect((await read()).aiRequests).toEqual(beforeRestart.aiRequests);
  expect((await read()).providerCallsEnabled).toBe(false);
  expect((await read()).financialExecution).toBe(false);
  await page.screenshot({
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
    await page
      .getByRole("button", { name: "Inspect source", exact: true })
      .click();
    await requested;
    await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption({ label: "News Researcher" });
    await expect(
      page.getByRole("searchbox", { name: "Search memories", exact: true }),
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
    await card.getByText("Exact context disclosure", { exact: true }).click();
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
      name: "Run development fixture",
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
      name: "I reviewed this context and these fixture limits",
      exact: true,
    }),
  );
  await fresh.card
    .getByRole("checkbox", {
      name: "I reviewed this context and these fixture limits",
      exact: true,
    })
    .check();
  await fresh.card
    .getByRole("button", { name: "Run development fixture", exact: true })
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
  expect((await read()).providerCallsEnabled).toBe(false);
  expect((await read()).financialExecution).toBe(false);
  await expect(
    review.getByRole("combobox", {
      name: "Memory",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveValue("all");
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
  ).toHaveValue("all");
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
  await page.screenshot({
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  await expect(
    review.getByRole("checkbox", {
      name: `Unavailable note: ${note.id}`,
      exact: true,
    }),
  ).toBeChecked();
  await expect(
    review.getByRole("combobox", { name: "News input", exact: true }),
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
    review.getByRole("combobox", { name: "News input", exact: true }),
  );
  await review
    .getByRole("combobox", { name: "News input", exact: true })
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
    review.getByLabel("Fixture budget (micro USD)", { exact: true }),
    "",
  );
  await review
    .getByLabel("Fixture budget (micro USD)", { exact: true })
    .fill("15000");
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
  await freshCard
    .getByText("Exact context disclosure", { exact: true })
    .click();
  await expect(freshCard.locator("pre").first()).toHaveText(fresh.disclosure);
  await expect(
    freshCard.getByRole("button", {
      name: "Run development fixture",
      exact: true,
    }),
  ).toBeDisabled();
  await revealControl(page, freshCard.getByRole("checkbox"));
  await freshCard.getByRole("checkbox").check();
  await freshCard
    .getByRole("button", { name: "Run development fixture", exact: true })
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
      name: "Run development fixture",
      exact: true,
    }),
  ).toBeDisabled();
  expect((await read()).providerCallsEnabled).toBe(false);
  expect((await read()).financialExecution).toBe(false);
  await page.screenshot({
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  await page.locator(".chat-evidence > summary").click();
  const inspector = page.getByRole("region", {
    name: "Local agent evidence inspection",
    exact: true,
  });
  const choose = async (kind: string, id: string, item = "") => {
    const evidence = page.locator(".chat-evidence");
    if ((await evidence.getAttribute("open")) === null)
      await evidence.locator(":scope > summary").click();
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
    provider: "xai",
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
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  await page.screenshot({
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
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
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
  await page.locator(".chat-evidence > summary").click();
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
  });
  const choose = async (kind: string, id: string, itemId = "") => {
    const evidence = page.locator(".chat-evidence");
    if ((await evidence.getAttribute("open")) === null)
      await evidence.locator(":scope > summary").click();
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
    await inspector
      .getByRole("button", { name: "Use in AI review", exact: true })
      .click();
    await expect(selected).toContainText(data.sha256);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth + 1,
      ),
    ).toBe(true);
    if (q.kind === "memory")
      await page.screenshot({
        path: resolve(
          `.evidence/ai-evidence/${isMobile ? "mobile" : "desktop"}-selected.png`,
        ),
        fullPage: true,
      });
    await expect(
      review.getByRole("textbox", { name: "Message", exact: true }),
    ).toBeFocused();
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
    await card.getByText("Exact context disclosure", { exact: true }).click();
    await expect(card.locator("pre")).toHaveText(request.disclosure);
    await expect(
      card.getByRole("button", {
        name: "Run development fixture",
        exact: true,
      }),
    ).toBeDisabled();
    expect(
      (await read()).aiRequests.find((r: any) => r.id === request.id).state,
    ).toBe("prepared");
    await revealControl(
      page,
      card.getByRole("checkbox", {
        name: "I reviewed this context and these fixture limits",
        exact: true,
      }),
    );
    await card
      .getByRole("checkbox", {
        name: "I reviewed this context and these fixture limits",
        exact: true,
      })
      .check();
    await card
      .getByRole("button", { name: "Run development fixture", exact: true })
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
    provider: "anthropic",
    model: "preference-only",
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
  await expect(selected).toContainText(
    JSON.parse(memoryRequest.context).evidenceSelection.sha256,
  );
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
      name: "Run development fixture",
      exact: true,
    }),
  ).toBeDisabled();
  // The 16 KiB inspector is not additional AI context capacity.
  const largeText = "<".repeat(1400);
  v = await command({ action: "memory-put", text: largeText, source: "owner" });
  const large = v.memories.find((m: any) => m.text === largeText);
  await page.reload();
  await openChat();
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
  await review.getByRole("button", { name: "Send", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("exceed 8000 bytes");
  expect(await read()).toEqual(beforeOversize);
  await selected
    .getByRole("button", { name: "Remove evidence", exact: true })
    .click();
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
  await page.screenshot({
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

test("R1 workspace uses a compact branded shell and on-demand chat task memory controls", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/chat");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.locator(isMobile ? ".mobile-brand img" : ".brand img"),
  ).toBeVisible();
  await expect(page.getByText("Private core", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("Instance location")).toContainText("Local");
  await expect(page.getByLabel("Agent", { exact: true })).toBeVisible();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption("trading-analyst");
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await revealControl(
    page,
    page.getByLabel("Conversation name", { exact: true }),
    "Conversation name",
  );
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("R1 fixture conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await expect(page.locator(".agent-chat textarea:visible")).toHaveCount(1);
  await expect(
    page.getByLabel("Maximum input tokens", { exact: true }),
  ).not.toBeVisible();
  await revealControl(page, page.getByLabel("Message", { exact: true }), "");
  await page
    .getByLabel("Message", { exact: true })
    .fill("Review the R1 fixture journal before each run.");
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
  await expect(page.locator(".local-messages")).toContainText(
    "Review the R1 fixture journal",
  );
  await expect(page.locator(".chat-system p")).not.toBeVisible();
  await expect(page.locator(".chat-system summary")).toHaveText(
    "Saved locally",
  );
  await page
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Save reviewed memory", exact: true })
    .click();
  await page.getByRole("button", { name: "memory", exact: true }).click();
  await expect(page.locator(".memory-library")).toContainText(
    "Review the R1 fixture journal",
  );
  await expect(page.locator(".memory-editor")).not.toBeVisible();
  await page.getByRole("button", { name: "Correct note", exact: true }).click();
  await expect(page.locator(".memory-editor")).toBeVisible();
  await page
    .getByRole("button", { name: "Cancel memory edit", exact: true })
    .click();
  await page.getByRole("button", { name: "tasks", exact: true }).click();
  await expect(page.locator(".task-composer")).not.toBeVisible();
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await revealControl(
    page,
    page.getByLabel("Task name", { exact: true }),
    "Task name",
  );
  await page
    .getByLabel("Task name", { exact: true })
    .fill("R1 fixture reminder");
  await revealControl(
    page,
    page.getByLabel("Reminder text", { exact: true }),
    "",
  );
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review the saved note.");
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await page
    .getByRole("button", { name: "Run now: R1 fixture reminder", exact: true })
    .click();
  await expect(page.getByRole("table", { name: "Automations" })).toContainText(
    "completed",
  );
  await expect(page.getByRole("table", { name: "Automations" })).toContainText(
    "1 / 1",
  );
  await page
    .getByRole("button", { name: "Result: R1 fixture reminder", exact: true })
    .click();
  await expect(page.getByLabel("Run results")).toContainText(
    "R1 fixture reminder",
  );
  await page.screenshot({
    path: `.evidence/r1-tasks-${test.info().project.name}.png`,
    fullPage: true,
  });
  await page.getByRole("button", { name: "chat", exact: true }).click();
  await revealControl(page, page.getByLabel("Message", { exact: true }), "");
  await page
    .getByLabel("Message", { exact: true })
    .fill("Explain this fixture result.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.locator(".ai-request").last();
  await expect(
    review.getByRole("button", {
      name: "Run development fixture",
      exact: true,
    }),
  ).toBeDisabled();
  await revealControl(
    page,
    review.getByLabel("I reviewed this context and these fixture limits", {
      exact: true,
    }),
    "",
  );
  await review
    .getByLabel("I reviewed this context and these fixture limits", {
      exact: true,
    })
    .check();
  await review
    .getByRole("button", { name: "Run development fixture", exact: true })
    .click();
  await expect(review).toContainText("Development fixture response");
  await page.screenshot({
    path: `.evidence/r1-chat-${test.info().project.name}.png`,
    fullPage: true,
  });
  await page.reload();
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(
    "trading-analyst",
  );
  await expect(page.locator(".local-messages")).toContainText(
    "Review the R1 fixture journal",
  );
  await expect(page.locator(".chat-system p")).not.toBeVisible();
  await expect(page.locator(".chat-system summary")).toHaveText(
    "Saved locally",
  );
  await page.getByRole("button", { name: "memory", exact: true }).click();
  await page.screenshot({
    path: `.evidence/r1-memory-${test.info().project.name}.png`,
    fullPage: true,
  });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption("news-researcher");
  await expect(page.locator(".memory-library")).not.toContainText(
    "Review the R1 fixture journal",
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  if (!isMobile)
    expect(
      (await page.locator(".sidebar").boundingBox())!.height,
    ).toBeLessThanOrEqual(52);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
});

test("shared WEN Overview Wallets and Settings preserve fixture authority and responsive controls", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const writes: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/") && request.method() !== "GET")
      writes.push(request.method() + " " + new URL(request.url()).pathname);
  });
  const checkLayout = async (name: string, selector: string) => {
    const layout = page.locator(selector);
    await expect(layout).toBeVisible();
    await expect(page.locator("html")).toHaveCSS(
      "background-color",
      "rgb(21, 20, 28)",
    );
    await expect(layout.locator(".panel").first()).toHaveCSS(
      "background-color",
      "rgb(30, 29, 41)",
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    const columns = await layout.evaluate(
      (el) => getComputedStyle(el).gridTemplateColumns.split(" ").length,
    );
    expect(columns).toBe(isMobile ? 1 : 2);
    if (isMobile) {
      const small = await layout
        .locator("button, select, input:not([type=checkbox])")
        .evaluateAll((els) =>
          els
            .filter(
              (el) =>
                el.getBoundingClientRect().width &&
                el.getBoundingClientRect().height < 44,
            )
            .map((el) => el.textContent),
        );
      expect(small).toEqual([]);
    }
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: `.evidence/wen-${name}-${isMobile ? "mobile" : "desktop"}.png`,
      fullPage: true,
    });
  };
  await expect(
    page.getByRole("heading", { name: "Overview", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".hero-card, .hero-art")).toHaveCount(0);
  await expect(
    page
      .getByRole("region", { name: "Terminals", exact: true })
      .getByText("DEFERRED", { exact: true }),
  ).toBeVisible();
  await checkLayout("overview", ".overview-workspace");
  await page.getByRole("button", { name: "Open Chat", exact: true }).click();
  await revealControl(
    page,
    page.getByLabel("Conversation name", { exact: true }),
    "Conversation name",
  );
  await expect(
    page.getByLabel("Conversation name", { exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#wallets/accounts");
  await expect(
    page.getByRole("heading", { name: "Public account records", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("REAL WALLETS DISABLED", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Record disposable signer", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", {
      name: "Create permission proposal",
      exact: true,
    }),
  ).toBeDisabled();
  await expect(page.locator(".account-safety")).toHaveCSS(
    "background-color",
    "rgb(30, 29, 41)",
  );
  await checkLayout("wallets", ".account-columns");
  await page.goto(url + "/#settings");
  await openUpdateSettings(page);
  await expect(
    page
      .getByRole("region", { name: "Updates & recovery" })
      .getByRole("button", { name: "Refresh update status" }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "Reset clears browser demo preferences. Saved core records are preserved.",
      { exact: true },
    ),
  ).toBeVisible();
  const notices = page.getByLabel("Show notices for pending proposals");
  await expect(notices).toBeChecked();
  const checkbox = await notices.evaluate((input) => {
    const box = input.getBoundingClientRect(),
      label = input.closest("label")!.getBoundingClientRect();
    return { left: box.x - label.x, height: label.height };
  });
  expect(checkbox.left).toBeLessThan(24);
  expect(checkbox.height).toBeGreaterThanOrEqual(isMobile ? 44 : 36);
  await checkLayout("settings", ".settings-workspace");
  await expect(
    page.getByRole("region", { name: "Update preferences & proposals" }),
  ).toBeVisible();
  if (!isMobile) {
    await page.setViewportSize({ width: 900, height: 1000 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: ".evidence/wen-settings-tablet.png",
      fullPage: true,
    });
  }
  expect(writes).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  expect(health.capabilities.walletSigner).toBe(false);
});

test("shared WEN remaining agent pages preserve news provenance paper budgets and disabled AI", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await revealControl(page, page.getByLabel("One-use claim code"), "");
  await page.getByLabel("One-use claim code").fill(claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await page.goto(url + "/#agents/news");
  const selected = page.getByLabel("Agent", { exact: true });
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  const layout = async (name: string, selector: string) => {
    const pane = page.locator(selector);
    await expect(pane).toBeVisible();
    expect(
      await pane.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns.split(" ").length,
      ),
    ).toBe(isMobile ? 1 : 2);
    await expect(page.locator("html")).toHaveCSS(
      "background-color",
      "rgb(21, 20, 28)",
    );
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    if (isMobile)
      expect(
        await pane
          .locator("button, select, input:not([type=checkbox])")
          .evaluateAll((els) =>
            els
              .filter(
                (el) =>
                  el.getBoundingClientRect().width &&
                  el.getBoundingClientRect().height < 44,
              )
              .map((el) => el.textContent),
          ),
      ).toEqual([]);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.screenshot({
      path: `.evidence/wen-v3-${name}-${isMobile ? "mobile" : "desktop"}.png`,
      fullPage: true,
    });
  };
  await expect(page.locator(".news-workspace")).toBeVisible();
  await revealControl(page, page.getByLabel("News source name"), "");
  await page.getByLabel("News source name").fill("V3 fixture headlines");
  await revealControl(page, page.getByLabel("Public RSS URL"), "");
  await page
    .getByLabel("Public RSS URL")
    .fill("https://news.fased.invalid/headlines.xml");
  await page
    .getByRole("button", { name: "Add news source", exact: true })
    .click();
  const feed = page.getByRole("article", {
    name: "News source V3 fixture headlines",
    exact: true,
  });
  await feed
    .getByRole("button", { name: "Fetch headlines", exact: true })
    .click();
  await expect(
    feed.getByRole("button", { name: "Discuss headline", exact: true }).first(),
    // Queued fetch: 3s fetch + 2s finalization + the existing 5s UI refresh.
  ).toBeVisible({ timeout: 12000 });
  await expect(feed).toContainText("SHA-256:");
  await layout("news", ".news-workspace");
  await page
    .locator(".tabs")
    .getByRole("button", { name: /^bots$/i })
    .click();
  await revealControl(page, page.getByLabel("Paper bot name"), "");
  await page.getByLabel("Paper bot name").fill("V3 paper budget");
  await page
    .getByRole("button", { name: "Create paper bot", exact: true })
    .click();
  await expect(page.locator(".paper-library")).toContainText("V3 paper budget");
  await expect(page.locator(".paper-library")).toContainText(
    "Used 0 / 500 fixture units",
  );
  await layout("bots", ".paper-workspace");
  await page
    .locator(".tabs")
    .getByRole("button", { name: /^settings$/i })
    .click();
  await expect(page.getByLabel("Profile name", { exact: true })).toHaveValue(
    "Trading Analyst",
  );
  const connection = page.getByRole("region", {
    name: "OpenAI connection",
    exact: true,
  });
  await expect(connection.getByRole("status")).toHaveText(
    "No connection saved",
  );
  await connection.locator(".connection-options > summary").click();
  await expect(
    connection.getByRole("button", {
      name: "Save test connection",
      exact: true,
    }),
  ).toBeDisabled();
  await expect(connection.getByLabel("Disposable API key")).toHaveValue("");
  await layout("settings", ".agent-settings-workspace");
  if (!isMobile) {
    await page.setViewportSize({ width: 900, height: 1000 });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: ".evidence/wen-v3-settings-tablet.png",
      fullPage: true,
    });
  }

  if (!isMobile) await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto(url + "/#activity");
  const activity = page.getByRole("region", {
    name: "Activity records",
    exact: true,
  });
  await expect(
    activity.getByRole("heading", { name: "Profile activity", exact: true }),
  ).toBeVisible();
  await expect(
    activity.getByText("No activity for this profile.", { exact: true }),
  ).toBeVisible();
  await expect(
    activity.getByRole("list", { name: "Activity events", exact: true }),
  ).toHaveCount(0);
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const command = async (body: object) => {
    const prior = await privateCLI(["agents", "--profile", "trading-analyst"]);
    return privateCLI(["agents", "--request-stdin"], {
      revision: prior.revision,
      profileId: "trading-analyst",
      ...body,
    });
  };
  let view = await command({
    action: "task-create",
    name: "V3 activity reminder",
    text: "Record the local fixture result",
    intervalSeconds: 3600,
    maxRuns: 1,
  });
  view = await command({ action: "task-run", id: view.tasks[0].id });
  expect(view.tasks[0].runs).toBe(1);
  await page.goto(url + "/#agents/tasks");
  await page.reload();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await expect(
    page.getByLabel("Agent", { exact: true }).locator("option:checked"),
  ).toHaveText("Trading Analyst");
  await page.goto(url + "/#activity");
  await expect(
    activity.getByRole("list", { name: "Activity events", exact: true }),
  ).toBeVisible();
  await expect(activity).toContainText("V3 activity reminder");
  await expect(activity).toContainText(
    "Saved by the local task engine. No financial execution.",
  );
  await expect(activity.getByText("UNREAD", { exact: true })).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/wen-v3-activity-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  view = await privateCLI(["agents", "--profile", "trading-analyst"]);
  expect(view.bots).toHaveLength(1);
  expect(view.bots[0].spentCents).toBe(0);
  expect(view.tasks).toHaveLength(1);
  expect(view.tasks[0].runs).toBe(1);
  expect(view.aiRequests).toHaveLength(0);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  expect(health.capabilities.walletSigner).toBe(false);
  expect(health.capabilities.financialExecution).toBe(false);
  // An unavailable scoped core read must not relabel browser demo or cached records as current.
  await page.route(/\/api\/v1\/agents(?:\?|$)/, (route) =>
    route.fulfill({
      status: 503,
      json: {
        error: {
          code: "fixture_unavailable",
          message: "Controlled activity read failure",
        },
      },
    }),
  );
  await page.reload();
  await expect(activity.getByRole("alert")).toBeVisible();
  await expect(
    activity.getByRole("list", { name: "Activity events", exact: true }),
  ).toHaveCount(0);
  await expect(activity).not.toContainText("V3 activity reminder");
});

test("installation connection uses runtime identity and opens Remote without changing Local data", async ({
  page,
  isMobile,
}) => {
  await page.goto(url + "/#overview");
  await expect(page.getByText("Local", { exact: true })).toBeVisible();
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Overview", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  await expect(page.locator("html")).toHaveCSS("font-family", /Adwaita Sans/);
  const rows = page.locator(".workspace-row-copy");
  await expect(rows.first()).toHaveCSS("display", "flex");
  const name = await rows.first().locator("strong").boundingBox();
  const role = await rows.first().locator("span").boundingBox();
  expect(name!.y + name!.height).toBeLessThanOrEqual(role!.y);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  const mutatePreference = await page.evaluate(async () => {
    const headers: Record<string, string> = {
      Authorization:
        "Bearer " + sessionStorage.getItem("fased.core.session.v1"),
    };
    const auth = await (await fetch("/api/v1/auth/state", { headers })).json();
    const setup = await (await fetch("/api/v1/setup", { headers })).json();
    const response = await fetch("/api/v1/setup", {
      method: "PUT",
      headers: {
        ...headers,
        "Content-Type": "application/json",
        "X-CSRF-Token": auth.csrfToken,
      },
      body: JSON.stringify({
        ...setup,
        settings: { ...setup.settings, host: "vps" },
      }),
    });
    return response.status;
  });
  expect(mutatePreference).toBe(200);
  await page.reload();
  await expect(page.getByLabel("Instance location")).toHaveText("Local");
  const before = await page.evaluate(async () => {
    const headers = {
      Authorization:
        "Bearer " + sessionStorage.getItem("fased.core.session.v1"),
    };
    return (await fetch("/api/v1/setup", { headers })).json();
  });
  await page.goto(url + "/#settings");
  await page.getByRole("button", { name: "Setup", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Instance", exact: true });
  await expect(dialog.getByText("Local", { exact: true })).toBeVisible();
  await expect(dialog.getByLabel("Wallet preference")).toHaveCount(0);
  await dialog.getByRole("button", { name: "Open Chat", exact: true }).click();
  await expect(
    page.getByRole("button", { name: /^chat$/i, exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#settings");
  await page.getByText("Connect to Remote", { exact: true }).click();
  const connect = page.getByRole("button", { name: "Connect", exact: true });
  await expect(connect).toHaveCSS("background-color", "rgb(255, 100, 124)");
  await connect.hover();
  await expect(connect).toHaveCSS("background-color", "rgb(255, 126, 146)");
  await page.getByLabel("Instance URL").fill("http://untrusted.example");
  await page.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("HTTPS");
  // A second ordinary process is a disposable remote-topology fixture, not a VPS.
  const remoteDir = await mkdtemp(join(tmpdir(), "fcr-"));
  let remoteOutput = "";
  const remote = spawn(binary, [
    "serve",
    "--instance-location",
    "remote",
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    remoteDir,
  ]);
  remote.stdout!.on("data", (data) => (remoteOutput += String(data)));
  remote.stderr!.on("data", (data) => (remoteOutput += String(data)));
  let popup: import("@playwright/test").Page | undefined;
  try {
    await expect
      .poll(() => remoteOutput.includes('"event":"listening"'))
      .toBe(true);
    const remoteURL = JSON.parse(
      remoteOutput
        .trim()
        .split("\n")
        .find((line) => line.includes('"event":"listening"'))!,
    ).url;
    const health = await (
      await page.request.get(remoteURL + "/api/v1/health")
    ).json();
    const inspected = JSON.parse(
      (
        await exec(binary, [
          "connect",
          "--url",
          remoteURL,
          "--instance-id",
          health.instanceId,
          "--print",
        ])
      ).stdout,
    );
    expect(inspected.installationCreated).toBe(false);
    expect(inspected.instanceId).toBe(health.instanceId);
    expect(inspected.location).toBe("remote");
    await page.getByLabel("Instance URL").fill(remoteURL);
    const opened = page.context().waitForEvent("page");
    await page.getByRole("button", { name: "Connect", exact: true }).click();
    popup = await opened;
    await expect(popup.getByText("Remote", { exact: true })).toBeVisible();
    await popup.getByText("Instance details", { exact: true }).click();
    await expect(
      popup.getByText(health.instanceId, { exact: true }),
    ).toBeVisible();
    expect(
      await popup.evaluate(() =>
        sessionStorage.getItem("fased.core.session.v1"),
      ),
    ).toBeNull();
    await expect(
      popup.getByRole("heading", { name: "Claim this workspace", exact: true }),
    ).toBeVisible();
    await popup.screenshot({
      path: `.evidence/installation-remote-${isMobile ? "mobile" : "desktop"}.png`,
      fullPage: true,
    });
    const after = await page.evaluate(async () => {
      const headers = {
        Authorization:
          "Bearer " + sessionStorage.getItem("fased.core.session.v1"),
      };
      return (await fetch("/api/v1/setup", { headers })).json();
    });
    expect(after.settings.host).toBe(before.settings.host);
    expect(after.settings.walletMode).toBe(before.settings.walletMode);
    expect(after.settings.provider).toBe(before.settings.provider);
    await expect(page.getByLabel("Instance location")).toHaveText("Local");
    await page.screenshot({
      path: `.evidence/installation-local-${isMobile ? "mobile" : "desktop"}.png`,
      fullPage: true,
    });
    for (const name of [
      "aiConnections",
      "financialExecution",
      "walletSigner",
      "updater",
    ])
      expect(health.capabilities[name]).toBe(false);
  } finally {
    await popup?.close();
    if (remote.exitCode === null) {
      const done = once(remote, "exit");
      remote.kill("SIGTERM");
      await done;
    }
    await rm(remoteDir, { recursive: true, force: true });
  }
  await stop();
  await start(false, "127.0.0.1:0", "remote");
  await page.goto(url);
  await expect(page.getByText("Remote", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Owner password")).toBeVisible();
});

test("installation connection refuses changed identity before sending an owner password", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  let sent = 0;
  page.on("request", (r) => {
    if (r.url().endsWith("/auth/login") && r.method() === "POST") sent++;
  });
  await page.route("**/api/v1/health", async (route) => {
    expect(route.request().headers()["authorization"]).toBeUndefined();
    expect(route.request().headers()["cookie"]).toBeUndefined();
    const response = await route.fetch();
    const h = await response.json();
    h.instanceId = "x".repeat(43);
    await route.fulfill({ response, json: h });
  });
  await page.getByLabel("Owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("identity changed");
  expect(sent).toBe(0);
});
