import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test, type Request, type Page } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { once } from "node:events";
import { createHash } from "node:crypto";
import {
  installCoreFixture,
  captureScreenshot,
  openNewsSources,
  closeNewsSources,
  newsFetchControl,
  exactReplyReview,
  openConversationList,
  choosePasswordSignIn,
  revealControl,
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");

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
  await page.getByRole("button", { name: "Templates", exact: true }).click();
  const panel = page.getByRole("region", {
    name: "Agent playbooks",
    exact: true,
  });
  await panel
    .getByRole("button", { name: "Add test news source", exact: true })
    .click();
  await expect.poll(async () => (await read()).newsSources.length).toBe(1);
  await panel
    .getByRole("button", { name: "Fetch test headlines", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).newsFetches[0]?.state)
    .toBe("completed");
  await expect(
    panel.getByRole("button", { name: "Fetch test headlines", exact: true }),
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
  await page.getByRole("button", { name: "Templates", exact: true }).click();
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
    name: "Approve this context, source policy and one test run",
    exact: true,
  });
  const runButton = panel.getByRole("button", {
    name: "Run test digest once",
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
  await captureScreenshot(page, {
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
  await expect(
    result.getByRole("button", { name: "Discuss digest", exact: true }),
  ).toHaveCount(0);
  await result
    .getByRole("button", { name: "Open in Chat", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  await expect(page.locator(".v2-message-user").last()).toContainText(
    savedResult.runs[0].news.fetchId,
    { timeout: 12000 },
  );
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
  await memoryDialog
    .getByRole("textbox", { name: "Note", exact: true })
    .fill("Fixture digest sources must be checked before reuse.");
  await memoryDialog
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect(memoryDialog).toHaveCount(0);
  await expect
    .poll(async () => {
      const sessionToken = await page.evaluate(() =>
        sessionStorage.getItem("fased.core.session.v1"),
      );
      const res = await page.request.get(
        `${url}/api/v1/agents/v2/memories?profileId=${encodeURIComponent(profileId)}&scope=chat`,
        {
          headers: sessionToken
            ? { Authorization: `Bearer ${sessionToken}` }
            : undefined,
        },
      );
      expect(res.ok()).toBe(true);
      return (await res.json()).memories.length;
    })
    .toBe(1);
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
  await page.getByRole("button", { name: "Templates", exact: true }).click();
  await stop();
  await start(false, listen);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Templates", exact: true }),
  ).toHaveCount(0);
  await expect(panel).toHaveCount(0);
  const ordinary = await read();
  const ordinaryReview = await command({
    action: "playbook-run-review",
    id,
    playbookVersion: 2,
    sessionId,
  });
  await expect(command(ordinaryReview.playbookReview.command)).rejects.toThrow(
    /explicit development AI fixture/,
  );
  expect(await read()).toEqual(ordinary);
  // Resume only the explicit regression constructor. A new boot must still
  // require fresh unchecked consent; receiving the old result grants nothing.
  await stop();
  await start(true, listen);
  await page.reload();
  await page.getByRole("button", { name: "Templates", exact: true }).click();
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
  await expect(consent).toBeEnabled();
  await expect(consent).not.toBeChecked();
  await expect(runButton).toBeDisabled();
  expect((await read()).runs).toHaveLength(1);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
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
    await page.getByRole("button", { name: "More", exact: true }).click();
    await page
      .getByRole("navigation", { name: "More pages", exact: true })
      .getByRole("link", { name: tab, exact: true })
      .click();
    await expect(
      page.getByLabel("Agent", { exact: true }).locator("option:checked"),
    ).toHaveText("Trading Analyst");
  };
  await page.goto(url + "/#agents/news");
  await chooseProfile(/^Trading Analyst /);
  const news = page.getByRole("region", { name: "News", exact: true });
  await openNewsSources(page, true);
  await revealControl(page, news.getByLabel("Name", { exact: true }), "");
  await news.getByLabel("Name", { exact: true }).fill("Joined controlled news");
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
    name: "Source Joined controlled news",
    exact: true,
  });
  await expect(source).toBeVisible();
  expect(
    (await read()).newsSources.map((s: { name: string }) => s.name),
  ).toEqual(["Joined controlled news"]);
  await source.getByRole("button", { name: "Fetch", exact: true }).click();
  await expect
    .poll(async () => (await read()).newsFetches[0]?.state)
    .toBe("completed");
  await closeNewsSources(page);
  await news.getByRole("button", { name: "Refresh", exact: true }).click();
  const fetched = await read();
  const fetch = fetched.newsFetches[0],
    item = fetch.items[0];
  const headlineRow = news
    .locator(".news-headline")
    .filter({ hasText: item.title });
  await expect(
    headlineRow.getByRole("button", { name: "Discuss", exact: true }),
  ).toHaveCount(0);
  await expect(
    headlineRow.getByRole("button", { name: "Ask in Chat", exact: true }),
  ).toBeVisible();
  await command({ action: "news-item-explain", id: fetch.id, source: item.id });
  await page.goto(url + "/#agents/review");
  await expect(page.locator(".local-messages")).toContainText(
    "Historical news headline",
    { timeout: 15000 },
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
  await exactReplyReview(page);
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
    name: "Run preview",
    exact: true,
  });
  await expect(runFixture).toBeDisabled();
  await revealControl(
    page,
    request.getByLabel("I reviewed this context and these limits"),
    "",
  );
  await request.getByLabel("I reviewed this context and these limits").check();
  await runFixture.click();
  await expect
    .poll(async () => (await read()).aiRequests[0]?.state)
    .toBe("completed");
  const card = request.getByRole("region", {
    name: "Review paper journal",
    exact: true,
  });
  await card.getByRole("button", { name: "Keep draft", exact: true }).click();
  await card.getByRole("button", { name: "Review task", exact: true }).click();
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
  await reminder.getByLabel("Unit", { exact: true }).selectOption("60");
  await reminder.getByLabel("First run in", { exact: true }).fill("1");
  await revealControl(page, reminder.getByLabel("Maximum reminder runs"), "");
  await reminder.getByLabel("Maximum reminder runs").fill("1");
  await reminder
    .getByRole("button", { name: "Review schedule", exact: true })
    .click();
  const create = reminder.getByRole("button", {
    name: "Create task",
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
  await page.getByRole("button", { name: "Results", exact: true }).click();
  const runCard = page
    .locator(".memory-card")
    .filter({ hasText: `Run ${run.id}` });
  await expect(
    runCard.getByRole("button", { name: "Discuss", exact: true }),
  ).toHaveCount(0);
  await runCard
    .getByRole("button", { name: "Open thread", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  const runReply = page.locator(".v2-message-assistant").last();
  await expect(runReply).toContainText(run.id);
  await expect(runReply).toContainText(item.title);
  v = await read();
  expect({
    tasks: v.tasks,
    runs: v.runs,
    notices: v.notices,
    aiRequests: v.aiRequests,
  }).toEqual(outcomes);
  await runReply
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const memoryDialog = page.getByRole("dialog", {
    name: "Memory",
    exact: true,
  });
  const noteText =
    "Review retained headline provenance before planning the next reminder";
  await memoryDialog.getByLabel("Note", { exact: true }).fill(noteText);
  await memoryDialog
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect(memoryDialog).toHaveCount(0);
  await expect
    .poll(async () => {
      const sessionToken = await page.evaluate(() =>
        sessionStorage.getItem("fased.core.session.v1"),
      );
      const res = await page.request.get(
        `${url}/api/v1/agents/v2/memories?profileId=${encodeURIComponent(profileId)}`,
        {
          headers: sessionToken
            ? { Authorization: `Bearer ${sessionToken}` }
            : undefined,
        },
      );
      expect(res.ok()).toBe(true);
      return (await res.json()).memories.length;
    })
    .toBe(1);
  const sessionToken = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  const noteRes = await page.request.get(
    `${url}/api/v1/agents/v2/memories?profileId=${encodeURIComponent(profileId)}`,
    {
      headers: sessionToken
        ? { Authorization: `Bearer ${sessionToken}` }
        : undefined,
    },
  );
  expect(noteRes.ok()).toBe(true);
  const note = (await noteRes.json()).memories[0];
  expect(note.content).toBe(noteText);
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page
    .getByRole("navigation", { name: "More pages", exact: true })
    .getByRole("link", { name: "Memory", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/memory$/);
  await expect(page.locator(".memory-library").first()).toContainText(noteText);
  const v2Search = page
    .getByRole("region", { name: "Workspace index", exact: true })
    .locator("form");
  await v2Search
    .getByRole("searchbox", { name: "Search saved drafts", exact: true })
    .fill("HEADLINE PROVENANCE");
  await v2Search.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.locator(".memory-library").first()).toContainText(noteText);

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
      id: run.id,
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
    page.locator(".memory-library").filter({ hasText: noteText }),
  ).toHaveCount(0);
  await chooseProfile(/^Trading Analyst /);
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    text: "Review saved sources",
    ...(await fixtureProviderSelection("xai")),
  });
  const frozen = await read();
  await stop();
  await start();
  origins.add(new URL(url).origin);
  await login();
  await page.goto(url + "/#agents/review");
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
  expect(restarted.providerCallsEnabled).toBe(true);
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
      name: "Run preview",
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
  await captureScreenshot(page, {
    path: `.evidence/joined-agent/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await open("Data & Feeds");
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
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await expect.poll(() => captured).toBe(true);
    // The existing five-second poll supplies the newer failed read. A second
    // click could instead wait forever on the now-disabled refresh control.
    await expect(page.getByRole("alert")).toContainText(
      "Controlled agent read unavailable",
      { timeout: 8000 },
    );
    await newsFetchControl(page, false);
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
  await newsFetchControl(page, false);
  await page.unroute(agentsRead);
  await page
    .getByRole("button", { name: "Refresh records", exact: true })
    .click();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await newsFetchControl(page, true);
  expect(await read()).toEqual(restarted);
  // Keep the app loaded while the real fixture core disappears.
  await stop();
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await newsFetchControl(page, false);
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
        runMessageId: run.id,
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
  await page.goto(url + "/#agents/news");
  const panel = page.getByRole("region", { name: "News", exact: true });
  await openNewsSources(page, true);
  await revealControl(page, panel.getByLabel("Name", { exact: true }), "");
  await panel.getByLabel("Name", { exact: true }).fill("Controlled headlines");
  await revealControl(page, panel.getByLabel("Feed URL", { exact: true }), "");
  await panel
    .getByLabel("Feed URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml");
  await revealControl(
    page,
    panel.getByLabel("Freshness (minutes)", { exact: true }),
    "",
  );
  await panel.getByLabel("Freshness (minutes)", { exact: true }).fill("10");
  await panel.getByRole("button", { name: "Add", exact: true }).click();
  const source = panel.getByRole("group", {
    name: "Source Controlled headlines",
    exact: true,
  });
  await source.getByRole("button", { name: "Fetch", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).newsFetches[0]?.state)
    .toBe("completed");
  await closeNewsSources(page);
  await panel.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(panel.locator(".news-headlines")).toContainText(
    "Controlled paper-news headline",
  );
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
    page
      .getByRole("combobox", {
        name: "News source",
        exact: true,
        includeHidden: true,
      })
      .first(),
  );
  await page
    .getByRole("combobox", {
      name: "News source",
      exact: true,
      includeHidden: true,
    })
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
  await page.getByRole("button", { name: "Review task", exact: true }).click();
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
  await closeNewsSources(page);
  await panel.getByRole("button", { name: "Refresh", exact: true }).click();
  await openNewsSources(page, true);
  await revealControl(page, panel.getByLabel("Name", { exact: true }), "");
  await panel.getByLabel("Name", { exact: true }).fill("Unavailable headlines");
  await revealControl(page, panel.getByLabel("Feed URL", { exact: true }), "");
  await panel
    .getByLabel("Feed URL", { exact: true })
    .fill("https://news.fased.invalid/unavailable.xml");
  await panel.getByRole("button", { name: "Add", exact: true }).click();
  await expect(
    panel.getByRole("group", {
      name: "Source Unavailable headlines",
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
  await openNewsSources(page);
  await expect(source).toContainText(first.bodySha256);
  await expect(
    panel.getByRole("group", {
      name: "Source Unavailable headlines",
      exact: true,
    }),
  ).toContainText("Failed");
  v = await privateCLI(["agents"]);
  expect(v.tasks.map((t: { state: string }) => t.state)).toEqual([
    "completed",
    "held",
    "completed",
  ]);
  expect(v.newsFetches[0].bodySha256).toBe(first.bodySha256);
  expect(v.providerCallsEnabled).toBe(true);
  expect(v.financialExecution).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await captureScreenshot(page, {
    path: `.evidence/p5-news/${test.info().project.name}.png`,
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
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#terminals/satcoin");
  await expect(
    page.getByRole("heading", {
      name: "Mining is not available yet",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("region", { name: "WEN paper journey" }),
  ).toHaveCount(0);
  const ordinary = await privateCLI(["agents"]);
  expect(ordinary.providerCallsEnabled).toBe(true);
  expect(ordinary.financialExecution).toBe(false);
  const listen = new URL(url).host;
  await stop();
  await start(true, listen);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const fixtureView = await privateCLI(["agents"]);
  expect(fixtureView.tasks).toEqual(ordinary.tasks);
  expect(fixtureView.runs).toEqual(ordinary.runs);
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
  await start(true);
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
  await start(true);
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
  await captureScreenshot(page, {
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
  const external: string[] = [];
  const fixtureOrigins = new Set([new URL(url).origin]);
  page.on("request", (r) => {
    if (!fixtureOrigins.has(new URL(r.url()).origin)) external.push(r.url());
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
  const messageInput = page.getByLabel("Message", { exact: true });
  await expect(messageInput).toHaveValue(/Frozen WEN paper context v1/, {
    timeout: 10000,
  });
  const draftedPrompt = await messageInput.inputValue();
  expect(draftedPrompt).toContain("Frozen WEN paper context v1");
  expect(draftedPrompt).toContain(receiptHash);
  expect(draftedPrompt).toContain(task.wen.digest);
  expect(draftedPrompt).toContain("not current state");
  v = await privateCLI(["agents"]);
  expect(v.aiRequests).toHaveLength(0);
  expect(v.tasks[0].state).toBe("completed");
  expect(v.runs).toHaveLength(1);
  await command({ action: "wen-paper-explain", id: task.id });
  await page.goto(url + "/#agents/review");
  const snapshot = page
    .locator(".local-messages article")
    .filter({ hasText: "Frozen WEN paper context v1" });
  await expect(snapshot).toContainText(receiptHash);
  await expect(snapshot).toContainText("not current state");
  v = await privateCLI(["agents"]);
  const originalText = v.messages[0].text;
  expect(await snapshot.locator("p").textContent()).toBe(originalText);
  const review = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Explain these frozen paper facts and their limitations");
  await exactReplyReview(page);
  await review.getByRole("button", { name: /Send|Prepare/ }).click();
  let request = review.locator(".ai-request").last();
  await request.locator("summary").first().click();
  await expect(request.locator("pre")).toContainText(task.wen.digest);
  await expect(
    request.getByRole("button", {
      name: "Run preview",
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
  await exactReplyReview(page);
  await review.getByRole("button", { name: /Send|Prepare/ }).click();
  request = review.locator(".ai-request").last();
  await revealControl(
    page,
    request.getByLabel("I reviewed this context and these limits"),
    "",
  );
  await request.getByLabel("I reviewed this context and these limits").check();
  await request
    .getByRole("button", { name: "Run preview", exact: true })
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
    ...(await fixtureProviderSelection("xai")),
  });
  await stop();
  await start(true);
  // The old page may still issue an in-flight read during restart. Only the
  // exact two disposable core origins belong to this fixture, never arbitrary
  // loopback or external services.
  fixtureOrigins.add(new URL(url).origin);
  await page.goto(url + "/#agents/review");
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
  await captureScreenshot(page, {
    path: `.evidence/wen-paper-chat/${isMobile ? "mobile" : "desktop"}.png`,
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/review");
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
  await page.evaluate(() => {
    window.location.hash = "agents/news";
  });
  const panel = page.getByRole("region", { name: "News", exact: true });
  await openNewsSources(page, true);
  await revealControl(page, panel.getByLabel("Name", { exact: true }), "");
  await panel
    .getByLabel("Name", { exact: true })
    .fill("Controlled headline source");
  await revealControl(page, panel.getByLabel("Feed URL", { exact: true }), "");
  await panel
    .getByLabel("Feed URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml");
  await revealControl(
    page,
    panel.getByLabel("Freshness (minutes)", { exact: true }),
    "",
  );
  await panel.getByLabel("Freshness (minutes)", { exact: true }).fill("10");
  await panel.getByRole("button", { name: "Add", exact: true }).click();
  const source = panel.getByRole("group", {
    name: "Source Controlled headline source",
    exact: true,
  });
  await source.getByRole("button", { name: "Fetch", exact: true }).click();
  await expect
    .poll(async () => (await read()).newsFetches[0]?.state)
    .toBe("completed");
  await closeNewsSources(page);
  await panel.getByRole("button", { name: "Refresh", exact: true }).click();
  await captureScreenshot(page, {
    path: `.evidence/news-journey-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Light", exact: true })
    .click();
  await page.keyboard.press("Escape");
  await expect(panel.locator(".news-headline")).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await captureScreenshot(page, {
    path: `.evidence/news-light-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Dark", exact: true })
    .click();
  await page.keyboard.press("Escape");
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
  const newsRow = panel
    .locator(".news-headline")
    .filter({ hasText: item.title });
  await expect(
    newsRow.getByRole("button", { name: "Discuss", exact: true }),
  ).toHaveCount(0);
  await expect(
    newsRow.getByRole("button", { name: "Ask in Chat", exact: true }),
  ).toBeVisible();
  await command({ action: "news-item-explain", id: fetch.id, source: item.id });
  await page.goto(url + "/#agents/review");
  await page.reload();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  const explainedView = await read();
  const explainedSession = explainedView.sessions.find(
    (s: { id: string }) => s.id === explainedView.messages.at(-1).sessionId,
  );
  await openConversationList(page);
  await page
    .locator("aside.conversation-rail")
    .getByRole("button", { name: explainedSession.name, exact: true })
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
  await revealControl(page, review.getByLabel("Note", { exact: true }), "");
  await review
    .getByLabel("Note", { exact: true })
    .fill(
      "Check the recorded age and original source before interpreting news",
    );
  await review
    .getByRole("button", { name: "Save memory", exact: true })
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
    ...(await fixtureProviderSelection("xai")),
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
  await page.goto(url + "/#agents/review");
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
  expect(v.providerCallsEnabled).toBe(true);
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
  await captureScreenshot(page, {
    path: `.evidence/news-chat/${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("provider-free News watch reviews rules deduplicates across restart and joins Chat memory through UI CLI", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");
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
  const profileId = "trading-analyst";
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  await command({
    action: "news-source-add",
    name: "Paper feed",
    source: "https://news.fased.invalid/headlines.xml",
    newsMaxAgeSeconds: 600,
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.getByRole("button", { name: "Watch news", exact: true }).click();
  const editor = page.getByRole("complementary", {
    name: "Create a task",
    exact: true,
  });
  await editor.getByLabel("Task name", { exact: true }).fill("Paper headlines");
  await editor
    .getByLabel("News source", { exact: true })
    .selectOption({ label: "Paper feed" });
  await editor.getByLabel("Contains", { exact: true }).fill("paper");
  await revealControl(page, editor.getByLabel("Schedule", { exact: true }));
  await editor.getByLabel("Schedule", { exact: true }).selectOption("repeat");
  await editor.getByLabel("Maximum evaluations", { exact: true }).fill("3");
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  const review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Contains: paper");
  await expect(review).toContainText("Paper feed");
  expect((await read()).tasks).toHaveLength(0);
  await review
    .getByRole("button", { name: "Create task", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks.length).toBe(1);
  const run = page.getByRole("button", {
    name: "Run now: Paper headlines",
    exact: true,
  });
  await run.click();
  await expect.poll(async () => (await read()).runs.length).toBe(1);
  let v = await read();
  expect(v.runs[0].watch.newIds).toHaveLength(1);
  expect(v.notices).toHaveLength(1);
  await expect(
    page.getByRole("article", { name: `Run ${v.runs[0].id}`, exact: true }),
  ).toContainText("New matches: 1");
  await expect(
    page.getByRole("link", {
      name: "Controlled paper-news headline",
      exact: true,
    }),
  ).toBeVisible();
  await run.click();
  await expect.poll(async () => (await read()).runs.length).toBe(2);
  v = await read();
  expect(v.runs[1].watch.newIds).toHaveLength(0);
  expect(v.notices).toHaveLength(1);
  await expect(
    page.getByRole("article", { name: `Run ${v.runs[1].id}`, exact: true }),
  ).toContainText("New matches: 0");
  await page
    .getByRole("button", { name: "Pause: Paper headlines", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks[0].state).toBe("paused");
  const state = await read();
  await captureScreenshot(page, {
    path: `.evidence/b04-news-watch/watch-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await stop();
  await start(true);
  await page.goto(url + "/#agents/tasks");
  await choosePasswordSignIn(page);
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const relogin = spawn(binary, [
    "login",
    "--data-dir",
    dir,
    "--password-stdin",
  ]);
  relogin.stdin!.end(fixturePassword + "\n");
  expect((await once(relogin, "exit"))[0]).toBe(0);
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  v = await read();
  expect(v.tasks).toEqual(state.tasks);
  expect(v.runs).toEqual(state.runs);
  await page
    .getByRole("button", { name: "Resume: Paper headlines", exact: true })
    .click();
  await run.click();
  await expect
    .poll(async () => (await read()).tasks[0].state)
    .toBe("completed");
  v = await read();
  expect(v.runs).toHaveLength(3);
  expect(v.runs[2].watch.newIds).toHaveLength(0);
  expect(v.tasks[0].watch.seen).toHaveLength(1);
  expect(v.notices).toHaveLength(1);
  const result = page.getByRole("article", {
    name: `Run ${v.runs[2].id}`,
    exact: true,
  });
  await expect(result).toContainText("New matches: 0");
  await page.getByRole("button", { name: "Results", exact: true }).click();
  const watchRun = page.getByRole("article", {
    name: `Run ${v.runs[0].id}`,
    exact: true,
  });
  await expect(
    watchRun.getByRole("button", { name: "Discuss", exact: true }),
  ).toHaveCount(0);
  await watchRun
    .getByRole("button", { name: "Open thread", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat$/);
  const message = page
    .locator(".v2-message-assistant")
    .filter({ hasText: v.runs[0].id })
    .last();
  await expect(message).toContainText("New matches: 1");
  await expect(message).toContainText("Controlled paper-news headline");
  await message
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const memory = page.getByRole("dialog", { name: "Memory", exact: true });
  await memory
    .getByLabel("Note", { exact: true })
    .fill("Paper feed: repeated headlines do not notify again.");
  await memory
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect(memory).toHaveCount(0);
  await expect
    .poll(async () => {
      const sessionToken = await page.evaluate(() =>
        sessionStorage.getItem("fased.core.session.v1"),
      );
      const res = await page.request.get(
        `${url}/api/v1/agents/v2/memories?profileId=${encodeURIComponent(profileId)}`,
        {
          headers: sessionToken
            ? { Authorization: `Bearer ${sessionToken}` }
            : undefined,
        },
      );
      expect(res.ok()).toBe(true);
      return (await res.json()).memories.length;
    })
    .toBe(1);
  const final = await read();
  expect(final.tasks).toEqual(v.tasks);
  expect(final.runs).toEqual(v.runs);
  expect(final.aiRequests).toHaveLength(0);
  expect(final.providerCallsEnabled).toBe(false);
  expect(final.financialExecution).toBe(false);
  const foreign = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(foreign.tasks).toHaveLength(0);
  expect(foreign.memories).toHaveLength(0);
  await captureScreenshot(page, {
    path: `.evidence/b04-news-watch/chat-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  expect(external).toEqual([]);
});
