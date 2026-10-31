import { expect, test, type Request, type Route } from "@playwright/test";
import {
  refreshFixtureAccounts,
  revealFixtureAccountEntry,
} from "../../scripts/fixture-account-journey.mjs";

test.use({ colorScheme: "dark" });
import { spawn, type ChildProcess } from "node:child_process";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { once } from "node:events";
import { createHash, randomBytes } from "node:crypto";

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
      name: "I reviewed this context and these fixture limits",
      exact: true,
    })
    .check();
  await card
    .getByRole("button", { name: "Run development fixture", exact: true })
    .click();
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
  await page.screenshot({
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
    name: "Correct a memory",
    exact: true,
  });
  await expect(edit).toBeVisible();
  await expect(edit.getByLabel("Evidence or source")).not.toBeVisible();
  await edit
    .getByLabel("Fact", { exact: true })
    .fill("My SOL risk budget is 250 units");
  await edit
    .getByRole("button", { name: "Save correction", exact: true })
    .click();
  await expect(memoryCard).toContainText("250 units");
  await page.screenshot({
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

test("reviewed recurring tasks preserve named zones bounds CLI results pause and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(75000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");
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
  const before = await privateCLI(["agents"]);
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await page.getByLabel("Task name", { exact: true }).fill("Weekday journal");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review saved positions and task results");
  await page.getByLabel("Schedule", { exact: true }).selectOption("cron");
  await page.getByLabel("Recurrence", { exact: true }).selectOption("weekdays");
  await page.getByLabel("Time", { exact: true }).fill("09:30");
  await page
    .getByLabel("Time zone", { exact: true })
    .selectOption("Asia/Seoul");
  await page.getByLabel("Maximum evaluations", { exact: true }).fill("3");
  await page.screenshot({
    path: `.evidence/cron-editor-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  const review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Asia/Seoul");
  await expect(review).toContainText(
    "Missing times skipped; repeated times run once.",
  );
  expect(await privateCLI(["agents"])).toEqual(before);
  await page.screenshot({
    path: `.evidence/cron-review-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks.length)
    .toBe(1);
  let v = await privateCLI(["agents"]);
  const task = v.tasks[0];
  expect(task.calendar).toMatchObject({
    version: "cron-v1",
    expression: "30 9 * * 1-5",
    timeZone: "Asia/Seoul",
  });
  expect(task.maxRuns).toBe(3);
  expect(task.nextAt).toBe(task.calendar.at);
  await page
    .getByRole("button", { name: "Run now: Weekday journal", exact: true })
    .first()
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].runs)
    .toBe(1);
  v = await privateCLI(["agents"]);
  expect(v.runs).toHaveLength(1);
  expect(v.tasks[0].nextAt).toBeGreaterThan(task.nextAt);
  await page
    .getByRole("button", { name: "Pause: Weekday journal", exact: true })
    .first()
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].state)
    .toBe("paused");
  v = await privateCLI(["agents"]);
  await page.screenshot({
    path: `.evidence/cron-result-${test.info().project.name}.png`,
    fullPage: false,
  });
  const address = new URL(url);
  await stop();
  await start(false, address.host);
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  expect(await privateCLI(["agents"])).toEqual(v);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.tasks).toHaveLength(0);
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

test("existing pages localize controls and keep owner drafts records and Meteora panes", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
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
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Owner conversation — 250 SOL");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Owner draft — 250 SOL");
  const before = await privateCLI(["agents"]);
  const cases = [
    [
      "ru",
      "Интерфейс",
      "Провайдеры",
      "Нет новостей",
      "В этом профиле нет заметок.",
      "Кошельки",
    ],
    [
      "es",
      "Preferencias",
      "Proveedores",
      "Sin titulares",
      "Este perfil no tiene notas.",
      "Billeteras",
    ],
    ["zh", "偏好设置", "模型服务", "暂无新闻", "此档案暂无记忆。", "钱包"],
    [
      "ko",
      "환경설정",
      "제공업체",
      "헤드라인 없음",
      "이 프로필에 메모가 없습니다.",
      "지갑",
    ],
    [
      "en",
      "Preferences",
      "Providers",
      "No headlines",
      "No notes in this profile.",
      "Wallets",
    ],
  ];
  let preferences = "Preferences";
  for (const [
    locale,
    nextPreferences,
    connection,
    news,
    memory,
    accounts,
  ] of cases) {
    await page.getByRole("button", { name: preferences, exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.locator("select").nth(1).selectOption(locale);
    await dialog
      .locator("select")
      .first()
      .selectOption(locale === "es" || locale === "ko" ? "light" : "dark");
    await page.keyboard.press("Escape");
    preferences = nextPreferences;
    await expect(page.locator("html")).toHaveAttribute(
      "lang",
      locale === "zh" ? "zh-Hans" : locale,
    );
    for (const [route, expected] of [
      ["settings", connection],
      ["agents/news", news],
      ["agents/memory", memory],
      ["wallets/overview", accounts],
      ["overview", null],
      ["agents/tasks", null],
      ["agents/chat", null],
    ] as const) {
      await page.evaluate((hash) => {
        location.hash = hash;
      }, route);
      if (expected) await expect(page.locator("main")).toContainText(expected);
      if (route === "settings")
        await expect(page.locator(".settings-primary > section")).toHaveCount(
          2,
        );
      if (route === "agents/chat")
        await expect(page.locator(".chat-input textarea")).toHaveValue(
          "Owner draft — 250 SOL",
        );
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
        `${locale}/${route} has no page overflow`,
      ).toBe(true);
      if (
        route === "settings" ||
        route === "agents/chat" ||
        route === "agents/tasks"
      )
        await page.screenshot({
          path: `.evidence/pages-${locale}-${route.replaceAll("/", "-")}-${test.info().project.name}.png`,
          fullPage: false,
        });
    }
    await expect(page.locator(".conversation-rail")).toContainText(
      "Owner conversation — 250 SOL",
    );
  }
  expect(await privateCLI(["agents"])).toEqual(before);
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  if (!isMobile)
    expect((await page.locator(".shell-header").boundingBox())!.height).toBe(
      50,
    );
});

test("Meteora shell preserves drafts and records through navigation theme language and restart", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(75000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(url + "/#chat");
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
  await expect(
    page.getByRole("button", { name: "Preferences", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Untranslated owner conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  const message = page.getByLabel("Message", { exact: true });
  await message.fill("Do not translate or discard this unsent draft.");
  const before = await privateCLI(["agents"]);
  const rail = page.getByRole("navigation", {
    name: "Page shortcuts",
    exact: true,
  });
  expect((await rail.textContent())?.trim()).toBe("");
  await rail.getByRole("link", { name: "Mining", exact: true }).focus();
  expect(
    await rail
      .getByRole("link", { name: "Mining", exact: true })
      .evaluate((el) => getComputedStyle(el, "::after").opacity),
  ).toBe("1");
  await page
    .getByRole("button", { name: "Hide navigation rail", exact: true })
    .click();
  await expect(rail).toBeHidden();
  await page.getByRole("button", { name: "More", exact: true }).click();
  const more = page.getByRole("navigation", {
    name: "More pages",
    exact: true,
  });
  await expect(
    more.getByRole("link", { name: "Onchain", exact: true }),
  ).toBeVisible();
  await expect(
    more.getByRole("link", { name: "Memory", exact: true }),
  ).toBeVisible();
  if (isMobile) {
    await more.getByRole("link", { name: "Tasks", exact: true }).click();
    await expect(page.locator(".shell-more-toggle")).toHaveCSS(
      "color",
      "rgb(255, 100, 124)",
    );
    await page.getByRole("button", { name: "More", exact: true }).click();
  }
  await more.getByRole("link", { name: "Memory", exact: true }).click();
  await expect(page).toHaveURL(/#agents\/memory$/);
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page.keyboard.press("Escape");
  await expect(
    page.getByRole("button", { name: "More", exact: true }),
  ).toBeFocused();
  await page.getByRole("button", { name: "More", exact: true }).click();
  await more.getByRole("link", { name: "Onchain", exact: true }).click();
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await expect(message).toHaveValue(
    "Do not translate or discard this unsent draft.",
  );
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Appearance", { exact: true }).selectOption("light");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.screenshot({
    path: `.evidence/shell-light-dialog-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.keyboard.press("Escape");
  await page.screenshot({
    path: `.evidence/shell-light-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  for (const [code, chat] of [
    ["ru", "Чат"],
    ["es", "Chat"],
    ["zh", "聊天"],
    ["ko", "채팅"],
  ]) {
    await dialog.locator("select").nth(1).selectOption(code);
    await expect(page.locator("html")).toHaveAttribute(
      "lang",
      code === "zh" ? "zh-Hans" : code,
    );
    await expect(
      page.locator('.shell-top-nav > a[href="#agents/chat"]'),
    ).toHaveText(chat);
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
  }
  await dialog.locator("select").nth(1).selectOption("en");
  await dialog.getByLabel("Appearance", { exact: true }).selectOption("system");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.emulateMedia({ colorScheme: "light" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await dialog.getByLabel("Appearance", { exact: true }).selectOption("dark");
  await page.keyboard.press("Escape");
  await expect(message).toHaveValue(
    "Do not translate or discard this unsent draft.",
  );
  await page
    .getByRole("button", { name: "Show navigation rail", exact: true })
    .click();
  await expect(rail).toBeVisible();
  await page.screenshot({
    path: `.evidence/shell-dark-${test.info().project.name}.png`,
    fullPage: false,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(await privateCLI(["agents"])).toEqual(before);
  const address = new URL(url);
  await stop();
  await start(false, address.host);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(rail).toBeVisible();
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(
    before.profileId,
  );
  expect(await privateCLI(["agents"])).toEqual(before);
  if (!isMobile)
    expect((await page.locator(".shell-header").boundingBox())!.height).toBe(
      50,
    );
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(external).toEqual([]);
});

test.describe("calendar reminders", () => {
  test.use({ timezoneId: "America/Chicago" });
  test("reviewed calendar reminder shares UI CLI date and survives paused restart", async ({
    page,
  }) => {
    test.setTimeout(75000);
    const external: string[] = [];
    page.on("request", (r) => {
      if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
        external.push(r.url());
    });
    await page.goto(url + "/#agents/tasks");
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
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin!.end(fixturePassword + "\n");
    expect((await once(login, "exit"))[0]).toBe(0);
    await page.goto(url + "/#agents/tasks");
    const newTask = page.getByRole("button", { name: "New task", exact: true });
    await newTask.hover();
    const contrast = await newTask.evaluate((el) => {
      const s = getComputedStyle(el);
      const luminance = (color: string) => {
        const channels = color
          .match(/[\d.]+/g)!
          .slice(0, 3)
          .map(Number)
          .map((x) => {
            const v = x / 255;
            return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
          });
        return (
          channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
        );
      };
      const a = luminance(s.color),
        b = luminance(s.backgroundColor);
      return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
    });
    expect(contrast).toBeGreaterThanOrEqual(4.5);
    await newTask.click();
    await page
      .getByLabel("Task name", { exact: true })
      .fill("Calendar journal");
    await page
      .getByLabel("Reminder text", { exact: true })
      .fill("Review the saved fixture journal.");
    await page
      .getByRole("combobox", { name: "Schedule", exact: true })
      .selectOption("calendar");
    await expect(page.getByLabel("Time zone", { exact: true })).toHaveValue(
      "America/Chicago",
    );
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
    await page.evaluate(() => scrollTo(0, 0));
    await page.screenshot({
      path: `.evidence/calendar-editor-${test.info().project.name}.png`,
      fullPage: false,
    });
    await page
      .getByLabel("Date & time", { exact: true })
      .fill("2027-03-14T02:30");
    await page
      .getByRole("button", { name: "Review task", exact: true })
      .click();
    await expect(page.getByRole("alert")).toContainText("does not exist");
    await page
      .getByLabel("Date & time", { exact: true })
      .fill("2026-11-01T01:30");
    await page
      .getByRole("button", { name: "Review task", exact: true })
      .click();
    await expect(page.getByRole("alert")).toContainText("occurs twice");
    const before = await privateCLI(["agents"]);
    expect(before.tasks).toHaveLength(0);
    await page.getByLabel("Time zone", { exact: true }).selectOption("UTC");
    const at = Math.floor((Date.now() / 1000 + 3600) / 60) * 60;
    await page
      .getByLabel("Date & time", { exact: true })
      .fill(new Date(at * 1000).toISOString().slice(0, 16));
    await page
      .getByRole("button", { name: "Review task", exact: true })
      .click();
    const review = page.getByRole("complementary", {
      name: "Review task",
      exact: true,
    });
    await expect(review).toContainText("UTC");
    await expect(
      review.getByText("No AI or wallet access", { exact: true }),
    ).toBeVisible();
    expect((await privateCLI(["agents"])).revision).toBe(before.revision);
    await page.evaluate(() => scrollTo(0, 0));
    await page.screenshot({
      path: `.evidence/calendar-review-${test.info().project.name}.png`,
      fullPage: false,
    });
    await review
      .getByRole("button", { name: "Create task", exact: true })
      .click();
    const table = page.getByRole("table", { name: "Tasks", exact: true });
    await expect(table).toContainText("Calendar journal");
    let v = await privateCLI(["agents"]);
    const id = v.tasks[0].id;
    expect(v.tasks[0]).toMatchObject({
      calendar: { version: "once-at-v1", at, timeZone: "UTC" },
      nextAt: at,
      intervalSeconds: 0,
      maxRuns: 1,
      runs: 0,
    });
    await page
      .getByRole("button", { name: "Pause: Calendar journal", exact: true })
      .click();
    await expect(table).toContainText("Paused");
    await stop();
    await start();
    await page.goto(url + "/#agents/tasks");
    await page.getByLabel("Owner password").fill(fixturePassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(table).toContainText("Paused");
    await page
      .getByRole("button", { name: "Resume: Calendar journal", exact: true })
      .click();
    await expect(table).toContainText("Scheduled");
    v = await privateCLI(["agents"]);
    expect(v.tasks[0].nextAt).toBe(at);
    expect(v.tasks[0].calendar.at).toBe(at);
    expect(v.runs).toHaveLength(0);
    await page
      .getByRole("button", { name: "Run now: Calendar journal", exact: true })
      .click();
    await expect(table).toContainText("Completed");
    await page
      .getByRole("button", { name: "Result: Calendar journal", exact: true })
      .click();
    await expect(page.getByLabel("Run results", { exact: true })).toContainText(
      "Calendar journal: Review the saved fixture journal.",
    );
    v = await privateCLI(["agents"]);
    expect(v.tasks[0]).toMatchObject({
      id,
      nextAt: at,
      runs: 1,
      state: "completed",
    });
    expect(v.runs).toHaveLength(1);
    expect(v.providerCallsEnabled).toBe(false);
    expect(v.financialExecution).toBe(false);
    const foreign = await privateCLI([
      "agents",
      "--profile",
      "news-researcher",
    ]);
    expect(foreign.tasks).toHaveLength(0);
    expect(foreign.runs).toHaveLength(0);
    expect(external).toEqual([]);
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
    await page.evaluate(() => scrollTo(0, 0));
    await page.screenshot({
      path: `.evidence/calendar-result-${test.info().project.name}.png`,
      fullPage: false,
    });
  });
});

test("compact Tasks reviews without writes and preserves bounded runs across UI CLI restart", async ({
  page,
}) => {
  test.setTimeout(75000);
  page.setDefaultTimeout(10000);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");
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
  await page.goto(url + "/#agents/tasks");
  await expect(page.getByText("No tasks", { exact: true })).toBeVisible();
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: `.evidence/tasks-empty-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await page.getByLabel("Task name", { exact: true }).fill("Journal review");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review the saved fixture results.");
  await page
    .getByRole("combobox", { name: "Schedule", exact: true })
    .selectOption("repeat");
  await page.getByLabel("Every", { exact: true }).fill("15");
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  const review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Every 15 minutes · 3 runs");
  const before = await privateCLI(["agents"]);
  expect(before.tasks).toHaveLength(0);
  expect(before.runs).toHaveLength(0);
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: `.evidence/tasks-review-${test.info().project.name}.png`,
    fullPage: false,
  });
  await review.getByRole("button", { name: "Edit task", exact: true }).click();
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review the corrected fixture results.");
  await expect(
    page.getByRole("button", { name: "Create task", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  await expect(review).toContainText("Review the corrected fixture results.");
  await privateCLI(["agents", "--request-stdin"], {
    action: "session-create",
    profileId: before.profileId,
    revision: before.revision,
    name: "Concurrent fixture conversation",
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(review.getByRole("alert")).toContainText("Records changed");
  await review
    .getByRole("button", { name: "Review again", exact: true })
    .click();
  await expect(review.getByRole("alert")).toHaveCount(0);
  await review
    .getByRole("button", { name: "Create task", exact: true })
    .click();
  const table = page.getByRole("table", { name: "Tasks", exact: true });
  await expect(table).toContainText("Journal review");
  let v = await privateCLI(["agents"]);
  const id = v.tasks[0].id;
  expect(v.tasks[0]).toMatchObject({
    text: "Review the corrected fixture results.",
    intervalSeconds: 900,
    maxRuns: 3,
    runs: 0,
    state: "scheduled",
  });
  expect(v.revision).toBe(before.revision + 2);
  await page
    .getByRole("button", { name: "Run now: Journal review", exact: true })
    .click();
  await expect(table).toContainText("1 / 3");
  const details = page.getByRole("complementary", {
    name: "Task details",
    exact: true,
  });
  await expect(details.locator(".task-run")).toContainText(
    "Journal review: Review the corrected fixture results.",
  );
  await page
    .getByRole("button", { name: "Pause: Journal review", exact: true })
    .click();
  await expect(table).toContainText("Paused");
  await expect(
    page.getByRole("button", { name: "Run now: Journal review", exact: true }),
  ).toBeDisabled();
  const paused = await privateCLI(["agents"]);
  expect(paused.runs).toHaveLength(1);
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: `.evidence/tasks-paused-${test.info().project.name}.png`,
    fullPage: false,
  });
  await stop();
  await start();
  await page.goto(url + "/#agents/tasks");
  await page.getByLabel("Owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(table).toContainText("Paused");
  expect((await privateCLI(["agents"])).runs).toEqual(paused.runs);
  await page
    .getByRole("button", { name: "Resume: Journal review", exact: true })
    .click();
  await expect(table).toContainText("Scheduled");
  v = await privateCLI(["agents"]);
  expect(v.tasks[0].nextAt).toBeGreaterThan(Date.now() / 1000 + 895);
  await page
    .getByRole("button", { name: "Result: Journal review", exact: true })
    .click();
  await expect(page.getByLabel("Run results", { exact: true })).toContainText(
    "Journal review: Review the corrected fixture results.",
  );
  await expect(
    page
      .getByLabel("Run results", { exact: true })
      .getByText("Run details", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Run results", { exact: true }).locator("details"),
  ).not.toHaveAttribute("open");
  const foreign = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(foreign.tasks).toHaveLength(0);
  expect(foreign.runs).toHaveLength(0);
  expect(v.tasks[0].id).toBe(id);
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: `.evidence/tasks-result-${test.info().project.name}.png`,
    fullPage: false,
  });
});

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
  await openProviders(page);
  const provider = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await expect(provider).toBeVisible();
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
  await page.screenshot({
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
  await page.screenshot({
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

async function openProviders(page: import("@playwright/test").Page) {
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  if (!(await providers.isVisible())) {
    let opened = false;
    for (const button of await page
      .getByRole("button", { name: "Providers", exact: true })
      .all()) {
      if (await button.isVisible()) {
        await button.click();
        opened = true;
        break;
      }
    }
    if (!opened) {
      await page.getByRole("button", { name: "More", exact: true }).click();
      await page
        .getByRole("navigation", { name: "More pages", exact: true })
        .getByRole("button", { name: "Providers", exact: true })
        .click();
    }
  }
  await expect(providers).toBeVisible();
}

async function openNewsSources(
  page: import("@playwright/test").Page,
  add = false,
) {
  const dialog = page.getByRole("dialog", { name: "Sources", exact: true });
  if (!(await dialog.isVisible()))
    await page.getByRole("button", { name: "Sources", exact: true }).click();
  if (
    add &&
    !(await dialog.getByLabel("Feed URL", { exact: true }).isVisible())
  )
    await dialog
      .getByRole("button", { name: "Add source", exact: true })
      .click();
}
async function closeNewsSources(page: import("@playwright/test").Page) {
  const dialog = page.getByRole("dialog", { name: "Sources", exact: true });
  if (await dialog.isVisible())
    await dialog
      .getByRole("button", { name: "Close dialog", exact: true })
      .click();
}
async function newsFetchControl(
  page: import("@playwright/test").Page,
  enabled: boolean,
) {
  await openNewsSources(page);
  const control = page
    .getByRole("dialog", { name: "Sources", exact: true })
    .getByRole("button", { name: "Fetch", exact: true })
    .first();
  if (enabled) await expect(control).toBeEnabled();
  else await expect(control).toBeDisabled();
  await closeNewsSources(page);
}

async function exactReplyReview(page: import("@playwright/test").Page) {
  const control = page.getByRole("checkbox", {
    name: "Review each reply",
    exact: true,
    includeHidden: true,
  });
  await revealControl(page, control, "Options");
  await control.check();
}

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
    } else if (hint.includes("Fact"))
      await page.getByRole("button", { name: "Add fact", exact: true }).click();
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
    if (await control.isVisible()) break;
    const section = disclosures.nth(i);
    if ((await section.getAttribute("open")) === null)
      await section.locator(":scope > summary").click();
  }
  await expect(control).toBeVisible();
}

async function openEvidence(page: import("@playwright/test").Page) {
  const options = page.locator(".message-options");
  if ((await options.getAttribute("open")) === null)
    await options.locator(":scope > summary").click();
  const evidence = page.locator(".chat-evidence");
  if ((await evidence.getAttribute("open")) === null)
    await evidence.locator(":scope > summary").click();
}

async function openUpdateSettings(page: import("@playwright/test").Page) {
  const section = page.locator("details.update-settings");
  await expect(section).toBeVisible();
  if ((await section.getAttribute("open")) === null)
    await section.locator(":scope > summary").click();
}

async function openAISchedules(page: import("@playwright/test").Page) {
  const pane = page.locator(".scheduled-ai-disclosure");
  await expect(pane).toBeAttached();
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
// Only this test's spawned cores are trusted. Keep their earlier ports during
// restart; unrelated loopback and external origins remain disallowed.
const ownedFixtureOrigins = new Set<string>();

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
  ownedFixtureOrigins.add(new URL(url).origin);
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
// Explicit fixture setup; never normalize production/negative request payloads.
async function fixtureProviderSelection(
  provider: "openai" | "anthropic" | "xai",
) {
  const account = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider,
    name: "Preference fixture",
    key: `fased-test-${provider}-browser-preference-key`,
    passphrase: "disposable-browser-preference-vault",
  });
  const model = account.catalog.models.find(
    (m: { recommended: boolean; provider: string }) =>
      m.recommended && m.provider === provider,
  );
  expect(model).toBeTruthy();
  expect(account.liveCallsEnabled).toBe(false);
  return { provider, model: model.id, accountRef: account.ref };
}

test.beforeEach(async () => {
  ownedFixtureOrigins.clear();
  const root = resolve(".evidence/core-browser-fixtures");
  await mkdir(root, { recursive: true, mode: 0o700 });
  // Disposable, reconstructible state; keep IPC independent of checkout length.
  dir = await mkdtemp(join(tmpdir(), "fcu-"));
  await start();
});
test.afterEach(async ({ page }) => {
  try {
    // Drain intercepted reads before stopping the fixture they forward to.
    await page.unrouteAll({ behavior: "wait" });
  } finally {
    try {
      await stop();
    } finally {
      if (dir) await rm(dir, { recursive: true, force: true });
    }
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
  await page.getByRole("button", { name: "Templates", exact: true }).click();
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
    memory.getByRole("textbox", { name: "Fact", exact: true }),
    "",
  );
  await memory
    .getByRole("textbox", { name: "Fact", exact: true })
    .fill("Fixture digest sources must be checked before reuse.");
  expect((await read()).memories).toHaveLength(1);
  await memory
    .getByRole("button", { name: "Save memory", exact: true })
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
  await page.getByRole("button", { name: "Templates", exact: true }).click();
  await stop();
  await start(false, listen);
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
    page.getByRole("textbox", { name: "Fact", exact: true }),
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
    if (!ownedFixtureOrigins.has(new URL(request.url()).origin))
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
  const runCard = (id: string) =>
    page.locator(".memory-card").filter({ hasText: `Run ${id}` });
  for (const i of [1, 0]) {
    const run = baseline.runs[i];
    const task = baseline.tasks.find((t: any) => t.id === run.taskId);
    await page.getByRole("button", { name: "Results", exact: true }).click();
    await expect(
      runCard(run.id).getByText(task.name, { exact: true }),
    ).toBeVisible();
    const notice = page.getByRole("article", {
      name: `Task notification ${run.id}`,
      exact: true,
    });
    await page.getByRole("button", { name: "Inbox", exact: true }).click();
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
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "Mark read", exact: true })
    .click();
  await expect.poll(async () => (await read()).notices[0].read).toBe(true);
  const afterRead = await read();
  expect(afterRead.runs).toEqual(baseline.runs);
  expect(afterRead.tasks).toEqual(baseline.tasks);
  baseline = afterRead;
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await firstNotice
    .getByRole("button", { name: "View result", exact: true })
    .click();
  expect(await read()).toEqual(baseline);
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "News Researcher" });
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
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
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
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
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
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
  await page.getByRole("button", { name: "Results", exact: true }).click();
  await expect(
    runCard(firstRun.id).getByText("Task unavailable", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", {
      name: "Result unavailable",
      exact: true,
    }),
  ).toBeDisabled();
  expect(await read()).toEqual(baseline);
  await page.unroute(routePattern);
  await page.reload();
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
  await expect(
    firstNotice.getByRole("button", { name: "View result", exact: true }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Inbox", exact: true }).click();
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
      name: "Create task",
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
  await expect(panel.getByRole("button", { name: "Create task" })).toHaveCount(
    0,
  );
  await panel
    .getByRole("button", { name: "Review new reminder", exact: true })
    .click();
  await expect(
    panel.getByRole("button", { name: "Create task" }),
  ).toBeVisible();
  v = await command({
    action: "profile-update",
    name: "Trading Analyst",
    ...(await fixtureProviderSelection("xai")),
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(panel.getByRole("alert")).toHaveText(
    "Records changed. Reload the reminder to review again.",
  );
  await expect(panel.getByRole("button", { name: "Create task" })).toHaveCount(
    0,
  );
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
    panel.getByRole("button", { name: "Create task" }),
  ).toBeVisible();
  expect(await read()).toEqual(v);
  await panel.getByRole("button", { name: "Create task", exact: true }).click();
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
  await expect(panel.getByRole("button", { name: "Create task" })).toHaveCount(
    0,
  );
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
    await page
      .getByRole("navigation", { name: "Page shortcuts", exact: true })
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
  await news
    .locator(".news-headline")
    .filter({ hasText: item.title })
    .getByRole("button", { name: "Discuss", exact: true })
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
  await revealControl(page, reminder.getByLabel("Repeat every (seconds)"), "");
  await reminder.getByLabel("Repeat every (seconds)").fill("60");
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
  await page
    .locator(".memory-card")
    .filter({ hasText: `Run ${run.id}` })
    .getByRole("button", { name: "Discuss", exact: true })
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
    memoryReview.getByLabel("Fact", { exact: true }),
    "",
  );
  await memoryReview.getByLabel("Fact", { exact: true }).fill(noteText);
  expect((await read()).memories).toHaveLength(0);
  await memoryReview
    .getByRole("button", { name: "Save memory", exact: true })
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
    page.getByRole("searchbox", { name: "Search", exact: true }),
  );
  await page
    .getByRole("searchbox", { name: "Search", exact: true })
    .fill("HEADLINE PROVENANCE");
  await page.getByRole("button", { name: "Search", exact: true }).click();
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
    ...(await fixtureProviderSelection("xai")),
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
  await open("News");
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

test("Updates keeps manual and automatic preferences without activating releases", async ({
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#settings");
  const panel = page.getByRole("region", { name: "Updates", exact: true });
  await expect(panel.getByLabel("Update mode")).toHaveValue("manual");
  await expect(
    panel.getByRole("button", { name: "Check", exact: true }),
  ).toBeDisabled();
  await expect(panel).toContainText("Release channel unavailable");
  await expect(page.getByLabel("Pinned sequence", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByLabel("Manifest SHA-256", { exact: true }),
  ).toHaveCount(0);
  await panel.getByLabel("Update mode").selectOption("automatic");
  await expect
    .poll(
      async () => (await privateCLI(["updates", "preferences"])).policy.mode,
    )
    .toBe("automatic");
  await panel.getByText("Schedule and version", { exact: true }).click();
  await panel.getByLabel("Maintenance window", { exact: true }).check();
  await expect
    .poll(
      async () =>
        (await privateCLI(["updates", "preferences"])).policy.window?.start,
    )
    .toBe("02:00");
  const v = await privateCLI(["updates", "status"]);
  expect(v.controllerAvailable).toBe(false);
  expect(v.activationEnabled).toBe(false);
  expect(v.operation.phase).toBe("idle");
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
  await expect(panel.getByLabel("Update mode")).toHaveValue("automatic");
  await expect(panel).toContainText("Automatic installation is not active.");
  expect(
    (await privateCLI(["updates", "preferences"])).policy.window.start,
  ).toBe("02:00");
});

test("update status shares UI and private CLI observations across pause, release and restart", async ({
  page,
}) => {
  await expect(
    exec(binary, ["update-status", "--data-dir", dir]),
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
  const before = await cli(["update-status"]);
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
  const held = await cli(["update-status"]);
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
  expect((await cli(["update-status"])).core.phase).toBe("released");
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
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
  await page.getByRole("button", { name: "Setup", exact: true }).click();
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
  await page.getByRole("button", { name: "Setup", exact: true }).click();
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
    if (!ownedFixtureOrigins.has(new URL(request.url()).origin))
      external.push(request.url());
    if (!["GET", "HEAD"].includes(request.method()))
      mutations.push(request.url());
  });
  for (const route of ["asset", "loss", "satcoin"]) {
    await page.goto(`${url}/#terminals/${route}`);
    await expect(page).toHaveURL(/#terminals\/satcoin$/);
    await expect(
      page.getByRole("heading", { name: "Mining", exact: true }),
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
    page.getByRole("heading", { name: "Mining", exact: true }),
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
  await expect(page.getByRole("button", { name: "Setup" })).toHaveCount(0);
});

test("managed account entry supports current and predecessor layouts", async ({
  page,
}) => {
  page.setDefaultTimeout(1500);
  await page.setContent(
    `<details><summary>Advanced</summary><label>Name<input></label></details><button onclick="this.dataset.refreshed='yes'">Refresh</button>`,
  );
  await revealFixtureAccountEntry(page);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  await expect(page.locator("details")).toHaveAttribute("open", "");
  await refreshFixtureAccounts(page);
  await expect(
    page.getByRole("button", { name: "Refresh", exact: true }),
  ).toHaveAttribute("data-refreshed", "yes");
  await page.setContent(
    `<label>Name<input></label><details><summary>Advanced</summary><p>Other controls</p></details><button onclick="this.dataset.refreshed='yes'">Refresh accounts and approvals</button>`,
  );
  await revealFixtureAccountEntry(page);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  await expect(page.locator("details")).not.toHaveAttribute("open", "");
  await refreshFixtureAccounts(page);
  await expect(
    page.getByRole("button", {
      name: "Refresh accounts and approvals",
      exact: true,
    }),
  ).toHaveAttribute("data-refreshed", "yes");
  await page.setContent("<main></main>");
  await page.evaluate(() => {
    window.setTimeout(() => {
      document.querySelector("main")!.innerHTML = "<label>Name<input></label>";
    }, 300);
  });
  await revealFixtureAccountEntry(page);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
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
  await expect(page.getByRole("heading", { name: "Wallets" })).toBeVisible();
  await revealFixtureAccountEntry(page);
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
  await refreshFixtureAccounts(page);
  expect(state.record.accounts[0].name).toBe("Disposable watch");
  expect(state.fixture.available).toBe(false);
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  const rpcBounds = await page.getByRole("dialog").boundingBox();
  const inputBounds = await page
    .getByLabel("RPC URL", { exact: true })
    .boundingBox();
  expect(inputBounds!.x - rpcBounds!.x).toBeGreaterThanOrEqual(16);
  await page.getByRole("button", { name: "Check", exact: true }).click();
  await expect(
    page.getByText("Verified (test)", { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel("RPC URL", { exact: true })
    .fill("https://127.0.0.1/private");
  await page.getByRole("button", { name: "Check", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "only the disposable RPC",
  );
  await expect(page.getByText("Verified (test)", { exact: true })).toHaveCount(
    0,
  );
  await page.getByRole("button", { name: "Close dialog" }).click();
  const rpc = await privateCLI(["accounts", "--check-rpc-stdin"], {
    endpoint: "https://rpc.fased.invalid/solana",
    network: "disposable-fixture:v1",
    accountId: state.record.accounts[0].id,
    accountRevision: state.record.revision,
  });
  expect(rpc.account.id).toBe(state.record.accounts[0].id);
  expect(rpc.accountRevision).toBe(state.record.revision);
  expect(rpc.accountState).toBe("public-record-only");
  expect(rpc.fixtureOnly).toBe(true);
  expect(rpc.liveRPC).toBe(false);
  expect(rpc.genesisHash).toBe("11111111111111111111111111111112");
  expect((await privateCLI(["accounts"])).record).toEqual(state.record);
  await expect(
    privateCLI(["accounts", "--check-rpc-stdin"], {
      endpoint: "https://rpc.fased.invalid/solana",
      network: "disposable-fixture:v1",
      accountId: state.record.accounts[0].id,
      accountRevision: state.record.revision + 1,
    }),
  ).rejects.toThrow("wallet changed");
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove", exact: true }),
  ).toBeVisible();
  state = await privateCLI(["accounts"]);
  expect(state.record.accounts[0].rpc.endpoint).toBe(
    "https://rpc.fased.invalid/solana",
  );
  const savedCheck = await privateCLI(["accounts", "--check-rpc-stdin"], {
    accountId: state.record.accounts[0].id,
    accountRevision: state.record.revision,
  });
  expect(savedCheck.account.rpc).toEqual(state.record.accounts[0].rpc);
  await page.reload();
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  await expect(page.getByLabel("RPC URL", { exact: true })).toHaveValue(
    state.record.accounts[0].rpc.endpoint,
  );
  await expect(page.getByText("Verified (test)", { exact: true })).toHaveCount(
    0,
  );
  await page.getByText("API key", { exact: true }).first().click();
  await page
    .getByLabel("RPC API key", { exact: true })
    .fill("fased-rpc-test-disposable-browser-123456");
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page.getByRole("button", { name: "Save key", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove key", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("RPC API key", { exact: true })).toHaveValue("");
  await page.screenshot({
    path: `.evidence/b05-wallet-setup/rpc-layout-${test.info().project.name}.png`,
  });
  const credential = await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "status",
    ref: "",
  });
  expect(credential.saved).toBe(true);
  expect(credential.locked).toBe(true);
  expect(credential.liveRPC).toBe(false);
  expect(JSON.stringify(credential)).not.toContain("disposable-browser");
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page.getByRole("button", { name: "Verify key", exact: true }).click();
  await expect(page.getByText("Key verified", { exact: true })).toBeVisible();
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page
    .getByRole("button", { name: "Check connection", exact: true })
    .click();
  await expect(
    page.getByText("Connected (demo)", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("Vault password", { exact: true })).toHaveValue(
    "",
  );
  const connection = await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "check",
    ref: credential.ref,
    passphrase: "Disposable browser vault password 123",
  });
  expect(connection.check.fixtureOnly).toBe(true);
  expect(connection.check.liveRPC).toBe(false);
  expect(connection.locked).toBe(true);
  expect(JSON.stringify(connection)).not.toContain("disposable-browser");

  const backupPath = join(dir, "rpc-backup.json");
  const backupStatus = await privateCLI(
    ["accounts", "--rpc-credential-stdin", "--rpc-backup-out", backupPath],
    {
      accountId: state.record.accounts[0].id,
      revision: state.record.revision,
      action: "backup",
      ref: credential.ref,
      passphrase: "Disposable browser vault password 123",
    },
  );
  expect(backupStatus.backup).toBeUndefined();
  expect(await readFile(backupPath, "utf8")).not.toContain(
    "disposable-browser",
  );
  await page.getByText("Recovery", { exact: true }).click();
  await page
    .getByLabel("Backup file", { exact: true })
    .setInputFiles(backupPath);
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page
    .getByRole("button", { name: "Verify backup", exact: true })
    .click();
  await expect(
    page.getByText("Backup verified", { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page.getByRole("button", { name: "Remove key", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove key", exact: true }),
  ).toHaveCount(0);
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page.getByRole("button", { name: "Restore", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove key", exact: true }),
  ).toBeVisible();
  const recovered = await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "status",
    ref: "",
  });
  expect(recovered.ref).not.toBe(credential.ref);
  expect(recovered.locked).toBe(true);
  await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "remove",
    ref: recovered.ref,
    passphrase: "Disposable browser vault password 123",
  });
  await page.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove", exact: true }),
  ).toHaveCount(0);
  state = await privateCLI(["accounts"]);
  expect(state.record.accounts[0].rpc).toBeUndefined();
  state = await privateCLI(["accounts", "--rpc-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    endpoint: "https://rpc.fased.invalid/solana",
    network: "disposable-fixture:v1",
  });
  await expect(
    privateCLI(["accounts", "--rpc-stdin"], {
      accountId: state.record.accounts[0].id,
      revision: state.record.revision,
      endpoint: "https://rpc.fased.invalid/solana",
      network: "disposable-fixture:v1",
      apiKey: "disposable-refused",
    }),
  ).rejects.toThrow("no credentials accepted");
  await page.reload();
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
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(
    page.getByRole("heading", { name: "External manual fixture", exact: true }),
  ).toBeVisible();
  const heldCapabilities = await (
    await page.request.get(url + "/api/v1/health")
  ).json();
  expect(heldCapabilities.capabilities.walletSigner).toBe(false);
  expect(heldCapabilities.capabilities.financialExecution).toBe(false);
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#wallets/accounts");
  await revealFixtureAccountEntry(page);
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
  await revealControl(page, page.getByLabel("Fact", { exact: true }), "Fact");
  await page.getByLabel("Fact", { exact: true }).fill("Detailed answers");
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
  await page.evaluate(() => {
    window.location.hash = "agents/bots";
  });
  await revealControl(page, page.getByLabel("Paper bot name"), "");
  await page.getByLabel("Paper bot name").fill("Fixed example");
  await page
    .getByRole("button", { name: "Create paper bot", exact: true })
    .click();
  await expect(page.getByText("Fixed example", { exact: true })).toBeVisible();
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/settings");
  await openProviders(page);
  const panel = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await expect(panel.getByRole("status")).toHaveText("No connection saved");
  const key = "fased-test-browser-disposable-key";
  const passphrase = "disposable-browser-vault-passphrase";
  await revealControl(page, panel.getByLabel("API key"), "");
  await panel.getByLabel("API key").fill(key);
  await revealControl(page, panel.getByLabel("Vault passphrase"), "");
  await panel.getByLabel("Vault passphrase").fill(passphrase);
  await panel.getByRole("button", { name: "Save" }).click();
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await expect(panel.getByLabel("API key")).toHaveValue("");
  await expect(panel.getByLabel("Vault passphrase")).toHaveValue("");
  let connection = await privateCLI(["ai-connection"]);
  expect(connection.locked).toBe(false);
  expect(connection.liveCallsEnabled).toBe(false);
  const firstRef = connection.ref;
  const ciphertext = await readFile(join(dir, "ai-credentials.json"), "utf8");
  expect(ciphertext).not.toContain(key);
  expect(ciphertext).not.toContain(passphrase);
  expect(connection.credentialMode).toBe("test");
  await expect(
    privateCLI(["ai-connection", "--request-stdin"], {
      action: "save",
      credentialMode: "openai",
      ref: firstRef,
      key: "disposable-api-input-not-issued-by-any-provider",
      passphrase,
    }),
  ).rejects.toThrow();
  expect(await readFile(join(dir, "ai-credentials.json"), "utf8")).toBe(
    ciphertext,
  );

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
    review.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption(firstRef);
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Analyze fixed paper inputs using the test connection");
  await exactReplyReview(page);
  await review.getByRole("button", { name: "Send" }).click();
  await review.getByText("Details", { exact: true }).click();
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
  await openProviders(page);
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
  await openProviders(page);
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await panel.getByText("Credentials", { exact: true }).click();
  await panel.getByRole("button", { name: "Lock", exact: true }).click();
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
  await openProviders(page);
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
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
  await exactReplyReview(page);
  await panel.getByRole("button", { name: "Send" }).click();
  await expect(panel.getByText("prepared", { exact: true })).toBeVisible();
  const run = panel.getByRole("button", { name: "Run development fixture" });
  await expect(run).toBeDisabled();
  await panel.getByText("Details", { exact: true }).click();
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
  await expect(card).toContainText("Saved");
  const view = await privateCLI(["agents"]);
  expect(view.aiRequests).toHaveLength(1);
  expect(
    view.aiRequests[0].cards.find(
      (c: { title: string; review: string }) =>
        c.title === "Review paper journal",
    )?.review,
  ).toBe("kept");
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
  await page.evaluate(() => {
    window.location.hash = "agents/settings";
  });
  await revealControl(page, page.getByLabel("Provider", { exact: true }), "");
  // Switch the current model preference without changing retained history.
  const beforeProvider = await privateCLI([
    "agents",
    "--profile",
    saved.profileId,
  ]);
  await privateCLI(["agents", "--request-stdin"], {
    action: "profile-update",
    revision: beforeProvider.revision,
    profileId: saved.profileId,
    name: profileName,
    provider: "openai",
    model: "gpt-6.1-sol",
  });
  await page.reload();
  await revealControl(page, page.getByLabel("Provider", { exact: true }), "");
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue(
    "openai",
  );
  await revealControl(page, page.getByLabel("Model", { exact: true }), "");
  await expect(page.getByLabel("Model", { exact: true })).toHaveValue(
    "gpt-6.1-sol",
  );
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
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
    .toBe("openai");
  await page.evaluate(() => {
    window.location.hash = "agents/chat";
  });
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
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
  const registry = JSON.parse(
    await readFile("internal/core/provider_models.json", "utf8"),
  );
  const currentModel = registry.models.find(
    (m: { id: string }) => m.id === connection.admissionPolicy.model,
  );
  expect(currentModel).toBeTruthy();
  expect(connection.admissionPolicy).toMatchObject({
    inputMicrousdPerMillion: currentModel.input,
    cachedMicrousdPerMillion: currentModel.cached,
    outputMicrousdPerMillion: currentModel.output,
  });
  const price = (n: number) => `$${(n / 1000000).toFixed(2)}`;
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
    review.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption(connection.ref);
  await revealControl(
    page,
    review.getByRole("combobox", {
      name: "Model",
      exact: true,
      includeHidden: true,
    }),
  );
  await review
    .getByRole("combobox", { name: "Model", exact: true, includeHidden: true })
    .selectOption(connection.admissionPolicy.model);
  await revealControl(page, review.getByLabel("Message", { exact: true }), "");
  await review
    .getByLabel("Message", { exact: true })
    .fill("Review the admitted model using controlled data");
  await exactReplyReview(page);
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
    `input ${price(currentModel.input)}, cached input ${price(currentModel.cached)}, output ${price(currentModel.output)}`,
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
  expect(manual.inputTokens).toBe(256);
  expect(manual.outputTokens).toBe(128);
  expect(manual.cachedInputTokens || 0).toBe(0);
  expect(manual.spentMicrousd).toBe(
    Math.ceil((256 * currentModel.input + 128 * currentModel.output) / 1000000),
  );
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
    scheduled.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    }),
  );
  await scheduled
    .getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    })
    .selectOption(connection.ref);
  await revealControl(
    page,
    scheduled.getByRole("combobox", {
      name: "Model",
      exact: true,
      includeHidden: true,
    }),
  );
  await scheduled
    .getByRole("combobox", { name: "Model", exact: true, includeHidden: true })
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
  await expect(
    task.getByRole("button", { name: "Evaluate now" }),
  ).toBeEnabled();
  view = await privateCLI(["agents"]);
  const access = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "task-access-review",
    ref: connection.ref,
    revision: view.revision,
    profileId: view.profileId,
    taskIds: [view.tasks[0].id],
    authorizationSeconds: 600,
  });
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "task-access-allow",
    ref: connection.ref,
    allowance: access.accessReview,
    consentDigest: access.accessReview.id,
    passphrase: "disposable-admission-vault-passphrase",
  });
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
  await exactReplyReview(page);
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
  await exactReplyReview(page);
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
    ...(await fixtureProviderSelection("xai")),
  });
  await stop();
  await start(true);
  // The old page may still issue an in-flight read during restart. Only the
  // exact two disposable core origins belong to this fixture, never arbitrary
  // loopback or external services.
  fixtureOrigins.add(new URL(url).origin);
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
  await exactReplyReview(page);
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
  await card.getByRole("button", { name: "Review task", exact: true }).click();
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
    .getByRole("button", { name: "Review schedule", exact: true })
    .click();
  const create = review.getByRole("button", {
    name: "Create task",
    exact: true,
  });
  await expect(create).toBeDisabled();
  await expect(review).toContainText("No task has been created");
  expect((await read()).tasks).toHaveLength(0);
  await review
    .getByRole("button", { name: "Cancel reminder review", exact: true })
    .click();
  expect((await read()).tasks).toHaveLength(0);
  await card.getByRole("button", { name: "Review task", exact: true }).click();
  await review
    .getByRole("button", { name: "Review schedule", exact: true })
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
    .getByRole("button", { name: "Review schedule", exact: true })
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
    .getByRole("button", { name: "Review schedule", exact: true })
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
    ...(await fixtureProviderSelection("xai")),
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
  await expect(review.getByLabel("Fact", { exact: true })).toHaveValue(
    message.text,
  );
  await revealControl(page, review.getByLabel("Fact", { exact: true }), "");
  await review.getByLabel("Fact", { exact: true }).fill("Cancelled draft");
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
  await revealControl(page, review.getByLabel("Fact", { exact: true }), "");
  await review
    .getByLabel("Fact", { exact: true })
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
    answerReview.getByLabel("Fact", { exact: true }),
    "",
  );
  await answerReview
    .getByLabel("Fact", { exact: true })
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#agents/tasks");
  await revealControl(page, page.getByLabel("Agent", { exact: true }), "");
  await page
    .getByLabel("Agent", { exact: true })
    .selectOption({ label: "Trading Analyst" });
  await page.getByRole("button", { name: "Results", exact: true }).click();
  await page
    .locator(".memory-card")
    .filter({ hasText: `Run ${plain.id}` })
    .getByRole("button", { name: "Discuss", exact: true })
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
  await revealControl(page, review.getByLabel("Fact", { exact: true }), "");
  await review
    .getByLabel("Fact", { exact: true })
    .fill("Review saved reminder outcomes before planning");
  await review
    .getByRole("button", { name: "Save memory", exact: true })
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
    ...(await fixtureProviderSelection("xai")),
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
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
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
  await page.screenshot({
    path: `.evidence/news-journey-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  await page
    .getByRole("dialog")
    .locator("select")
    .first()
    .selectOption("light");
  await page.keyboard.press("Escape");
  await expect(panel.locator(".news-headline")).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/news-light-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  await page.getByRole("dialog").locator("select").first().selectOption("dark");
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
  await panel
    .locator(".news-headline")
    .filter({ hasText: item.title })
    .getByRole("button", { name: "Discuss", exact: true })
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
  await revealControl(page, review.getByLabel("Fact", { exact: true }), "");
  await review
    .getByLabel("Fact", { exact: true })
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
  await page.getByText("1 previous revision(s)", { exact: true }).click();
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
    await card.getByText("Details", { exact: true }).click();
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
    provider: "openai",
    model: "gpt-6.1-sol",
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
  await expect(page.locator(".shell-brand img")).toBeVisible();
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
  await page.getByRole("button", { name: "Save memory", exact: true }).click();
  await page.evaluate(() => {
    window.location.hash = "agents/memory";
  });
  await expect(page.locator(".memory-library")).toContainText(
    "Review the R1 fixture journal",
  );
  await expect(page.locator(".memory-editor")).not.toBeVisible();
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await expect(page.locator(".memory-editor")).toBeVisible();
  await page
    .getByRole("button", { name: "Cancel correction", exact: true })
    .click();
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
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
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await page
    .getByRole("button", { name: "Run now: R1 fixture reminder", exact: true })
    .click();
  await expect(page.getByRole("table", { name: "Tasks" })).toContainText(
    "Completed",
  );
  await expect(page.getByRole("table", { name: "Tasks" })).toContainText(
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
  await page.evaluate(() => {
    window.location.hash = "agents/chat";
  });
  await revealControl(page, page.getByLabel("Message", { exact: true }), "");
  await page
    .getByLabel("Message", { exact: true })
    .fill("Explain this fixture result.");
  await exactReplyReview(page);
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
  await page.evaluate(() => {
    window.location.hash = "agents/memory";
  });
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
      (await page.locator(".shell-header").boundingBox())!.height,
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
      "rgb(25, 25, 34)",
    );
    await expect(
      layout
        .locator(
          selector === ".dashboard-grid" ? ".dashboard-widget" : ".panel",
        )
        .first(),
    ).toHaveCSS("background-color", "rgb(30, 30, 42)");
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
    if (selector === ".wallet-workspace") {
      await expect(layout).toHaveCSS("display", "block");
    } else {
      const columns = await layout.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns.split(" ").length,
      );
      expect(columns).toBe(
        isMobile ? 1 : selector === ".dashboard-grid" ? 3 : 2,
      );
    }
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
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".hero-card, .hero-art")).toHaveCount(0);
  await expect(page.locator('[data-widget="wallets"]')).toContainText(
    "Unavailable",
  );
  await expect(page.locator('[data-widget="mining"]')).toHaveCount(0);
  await checkLayout("dashboard", ".dashboard-grid");
  await page.locator('.shell-top-nav>a[href="#agents/chat"]').click();
  await revealControl(
    page,
    page.getByLabel("Conversation name", { exact: true }),
    "Conversation name",
  );
  await expect(
    page.getByLabel("Conversation name", { exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#wallets/accounts");
  await revealFixtureAccountEntry(page);
  const walletCapabilities = await (
    await page.request.get(url + "/api/v1/health")
  ).json();
  expect(walletCapabilities.capabilities.walletSigner).toBe(false);
  expect(walletCapabilities.capabilities.financialExecution).toBe(false);
  await expect(
    page.getByRole("heading", { name: "Wallets", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Test only", { exact: true })).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Record disposable signer", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", {
      name: "Create permission proposal",
      exact: true,
    }),
  ).toBeDisabled();
  await checkLayout("wallets", ".wallet-workspace");
  await page.goto(url + "/#settings");
  await openUpdateSettings(page);
  await expect(
    page
      .getByRole("region", { name: "Updates & recovery" })
      .getByRole("button", { name: "Refresh update status" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Reset prototype", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "Browser demo data", exact: true }),
  ).toHaveCount(0);
  const updates = page.getByRole("region", { name: "Updates", exact: true });
  const notices = updates.getByLabel("Show in inbox");
  await expect(notices).toBeChecked();
  const checkbox = await notices.evaluate((input) => {
    const box = input.getBoundingClientRect(),
      label = input.closest("label")!.getBoundingClientRect();
    return { left: box.x - label.x, height: label.height };
  });
  expect(checkbox.left).toBeLessThan(24);
  expect(checkbox.height).toBeGreaterThanOrEqual(isMobile ? 44 : 36);
  await checkLayout("settings", ".settings-workspace");
  await expect(updates).toBeVisible();
  await expect(
    updates.getByRole("button", { name: "Check", exact: true }),
  ).toBeDisabled();
  if (!isMobile) {
    await page.setViewportSize({ width: 900, height: 1000 });
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
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const newsLogin = spawn(binary, [
    "login",
    "--data-dir",
    dir,
    "--password-stdin",
  ]);
  newsLogin.stdin!.end(fixturePassword + "\n");
  expect((await once(newsLogin, "exit"))[0]).toBe(0);
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
    ).toBe(isMobile || name === "settings" ? 1 : 2);
    await expect(page.locator("html")).toHaveCSS(
      "background-color",
      "rgb(25, 25, 34)",
    );
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
  await expect(page.locator(".news-feed")).toBeVisible();
  await openNewsSources(page, true);
  await revealControl(page, page.getByLabel("Name"), "");
  await page.getByLabel("Name").fill("V3 fixture headlines");
  await revealControl(page, page.getByLabel("Feed URL"), "");
  await page
    .getByLabel("Feed URL")
    .fill("https://news.fased.invalid/headlines.xml");
  await page.getByRole("button", { name: "Add", exact: true }).click();
  const feed = page.getByRole("group", {
    name: "Source V3 fixture headlines",
    exact: true,
  });
  await feed.getByRole("button", { name: "Fetch", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", "trading-analyst"]))
          .newsFetches[0]?.state,
    )
    .toBe("completed");
  await closeNewsSources(page);
  await page
    .getByRole("region", { name: "News", exact: true })
    .getByRole("button", { name: "Refresh", exact: true })
    .click();
  await expect(page.locator(".news-headline")).toContainText(
    "Controlled paper-news headline",
  );
  await page.locator(".news-details > summary").click();
  await expect(page.locator(".news-details")).toContainText(
    (await privateCLI(["agents", "--profile", "trading-analyst"]))
      .newsFetches[0].bodySha256,
  );
  await expect(
    page.getByRole("textbox", { name: "Feed URL", exact: true }),
  ).toHaveCount(0);
  await page.screenshot({
    path: `.evidence/news-feed-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.evaluate(() => {
    window.location.hash = "agents/bots";
  });
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
  await page.evaluate(() => {
    window.location.hash = "agents/settings";
  });
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
    "Trading Analyst",
  );
  await openProviders(page);
  const connection = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await expect(connection.getByRole("status")).toHaveText(
    "No connection saved",
  );
  await revealControl(page, connection.getByLabel("API key"), "");
  await expect(
    connection.getByRole("button", {
      name: "Save",
      exact: true,
    }),
  ).toBeDisabled();
  await expect(connection.getByLabel("API key")).toHaveValue("");
  await page
    .getByRole("region", { name: "Providers", exact: true })
    .getByRole("button", { name: "Back", exact: true })
    .click();
  await layout("settings", ".agent-settings-workspace");
  if (!isMobile) {
    await page.setViewportSize({ width: 900, height: 1000 });
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
  await expect(
    activity.getByRole("button", { name: "Result", exact: true }),
  ).toBeVisible();
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
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  await expect(page.locator("html")).toHaveCSS("font-family", /Fased Inter/);
  const rows = page.locator('[data-widget="agents"] .dashboard-row');
  await expect(rows.first()).toHaveCSS("display", "flex");
  const name = await rows.first().locator("strong").boundingBox();
  const card = await page.locator('[data-widget="agents"]').boundingBox();
  expect(name!.x + name!.width).toBeLessThanOrEqual(card!.x + card!.width);
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
  await expect(page).toHaveURL(/#agents\/chat$/);
  await expect(
    page
      .getByRole("navigation", { name: "Main navigation", exact: true })
      .getByRole("link", { name: "Chat", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#settings");
  await page.getByText("Connect to Remote", { exact: true }).click();
  const connect = page.getByRole("button", { name: "Connect", exact: true });
  await expect(connect).toHaveCSS("background-color", "rgb(255, 100, 124)");
  await connect.hover();
  await expect(connect).toHaveCSS("background-color", "rgb(255, 122, 142)");
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

test("reviewed task credential allowance is scoped, finite and revocable in Providers and CLI", async ({
  page,
}) => {
  test.setTimeout(75000);
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
  const passphrase = "disposable-task-access-passphrase";
  const connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    key: "fased-test-browser-task-access-key",
    passphrase,
  });
  let v = await privateCLI(["agents"]);
  const change = async (c: unknown) => {
    v = await privateCLI(["agents", "--request-stdin"], {
      profileId: v.profileId,
      revision: v.revision,
      ...(c as object),
    });
    return v;
  };
  await change({ action: "session-create", name: "Task access source" });
  const session = v.sessions[0].id;
  await change({
    action: "task-ai-prepare",
    id: session,
    name: "Selected fixture reminder",
    text: "Review the fixed context",
    connectionRef: connection.ref,
    intervalSeconds: 3600,
    maxRuns: 2,
    authorizationSeconds: 7200,
    totalBudgetMicrousd: 60000,
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  });
  const task = v.tasks[0];
  await change({
    action: "task-ai-authorize",
    id: task.id,
    consentDigest: task.ai.digest,
  });
  await page.goto(url + "/#agents/settings");
  await page.reload();
  await openProviders(page);
  const provider = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await provider.getByText("Background access", { exact: true }).click();
  await provider
    .getByRole("checkbox", { name: task.name, exact: true })
    .check();
  await provider
    .getByRole("button", { name: "Review access", exact: true })
    .click();
  await expect(
    provider.getByText(
      "Selected tasks only. Closing the browser keeps them running. Lock, sign-out, expiry or restart stops credential access.",
      { exact: true },
    ),
  ).toBeVisible();
  expect((await privateCLI(["ai-connection"])).taskAccess).toBeUndefined();
  await provider
    .getByLabel("Vault passphrase", { exact: true })
    .first()
    .fill(passphrase);
  await provider
    .getByRole("button", { name: "Allow task access", exact: true })
    .click();
  await expect
    .poll(
      async () => (await privateCLI(["ai-connection"])).taskAccess?.tasks[0].id,
    )
    .toBe(task.id);
  await change({ action: "task-run", id: task.id });
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].runs, {
      timeout: 12000,
    })
    .toBe(1);
  await expect
    .poll(
      async () =>
        (await privateCLI(["ai-connection"])).taskAccess?.usedRequests,
    )
    .toBe(1);
  await provider.getByRole("button", { name: "Revoke", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).taskAccess)
    .toBeUndefined();
  v = await privateCLI(["agents"]);
  await change({ action: "task-run", id: task.id });
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0].state, {
      timeout: 12000,
    })
    .toBe("held");
  const held = await privateCLI(["agents"]);
  expect(held.tasks[0].runs).toBe(2);
  expect(
    held.runs.filter((r: any) => r.strategy === "ai-step-v1/reminder-v1"),
  ).toHaveLength(1);
  expect(held.runs.at(-1).outcome).toBe("ai-cancelled");
  expect(held.aiRequests.at(-1).spentMicrousd).toBe(0);
  const status = await privateCLI(["ai-connection"]);
  expect(status.liveCallsEnabled).toBe(false);
  await page.screenshot({
    path: `.evidence/provider-task-access-${test.info().project.name}.png`,
    fullPage: false,
  });
});

test("Dashboard drag and resize autosave without a duplicate home rail shortcut", async ({
  page,
  isMobile,
}) => {
  page.setDefaultTimeout(10000);
  let unavailable = true;
  await page.route("**/api/v1/agents?**", async (route) => {
    if (unavailable)
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: "Controlled workspace outage" }),
      });
    else await route.continue();
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  const board = page.locator(".dashboard-grid");
  await expect(board).toBeVisible();
  const dashboard = page.locator(".dashboard-screen");
  await expect(dashboard.getByRole("alert").first()).toContainText(
    "Controlled workspace outage",
  );
  await expect(board.locator(".dashboard-row")).toHaveCount(0);
  unavailable = false;
  await dashboard
    .getByRole("button", { name: "Reload", exact: true })
    .first()
    .click();
  await expect(board.locator(".dashboard-row").first()).toBeVisible();
  await expect(
    page.locator('.shell-top-nav > a[href="#overview"]'),
  ).toHaveCount(0);
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const original = await privateCLI(["agents"]);
  const tasks = board.locator('[data-widget="tasks"]');
  if (isMobile) {
    await tasks.locator("summary").click();
    await tasks.getByRole("button", { name: "Move up", exact: true }).click();
    await tasks.locator("summary").click();
  } else {
    await tasks
      .getByRole("button", { name: "Move Tasks", exact: true })
      .dragTo(board.locator('[data-widget="agents"]'));
  }
  await expect(board.locator("[data-widget]").first()).toHaveAttribute(
    "data-widget",
    "tasks",
  );
  await tasks.locator("summary").click();
  await tasks.getByLabel("Tasks size", { exact: true }).selectOption("tall");
  await tasks.locator("summary").click();
  await page.reload();
  await expect(board.locator("[data-widget]").first()).toHaveAttribute(
    "data-widget",
    "tasks",
  );
  await expect(tasks).toHaveClass(/size-tall/);
  expect((await privateCLI(["agents"])).revision).toBe(original.revision);
  const rail = page.locator("#shell-rail");
  await expect(
    rail.getByRole("link", {
      name: "Dashboard",
      exact: true,
      includeHidden: true,
    }),
  ).toHaveCount(0);
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await page.getByRole("link", { name: "Fased", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  await expect(board.locator("[data-widget]").first()).toHaveAttribute(
    "data-widget",
    "tasks",
  );
  const geometry = await page.evaluate(() => {
    const style = (selector: string) =>
      getComputedStyle(document.querySelector(selector)!);
    return {
      viewport: { width: innerWidth, height: innerHeight },
      headerHeight: document
        .querySelector(".shell-header")!
        .getBoundingClientRect().height,
      navFontSize: style(".shell-top-nav > a").fontSize,
      gridGap: style(".dashboard-grid").gap,
      surface: style(".dashboard-widget").backgroundColor,
      border: style(".dashboard-widget").borderTopColor,
      font: style(".shell-header").fontFamily,
    };
  });
  expect(geometry.gridGap).toBe("8px");
  expect(geometry.surface).toBe("rgb(30, 30, 42)");
  expect(geometry.border).toBe("rgb(46, 46, 62)");
  if (!isMobile) {
    expect(geometry.headerHeight).toBe(50);
    expect(geometry.navFontSize).toBe("14px");
  }
  await mkdir(resolve(".evidence/b02-workspace"), { recursive: true });
  await writeFile(
    resolve(
      `.evidence/b02-workspace/geometry-${isMobile ? "mobile" : "desktop"}.json`,
    ),
    JSON.stringify(geometry, null, 2),
  );
  await page.screenshot({
    path: `.evidence/dashboard-home-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("compact agent pages share one provider editor and keep feed settings off the page", async ({
  page,
}) => {
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/news");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "News", exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: "Feed URL", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByText("PUBLIC RSS · UNTRUSTED", { exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Sources", exact: true }).click();
  const sources = page.getByRole("dialog", { name: "Sources", exact: true });
  await expect(sources).toBeVisible();
  await sources
    .getByRole("button", { name: "Add source", exact: true })
    .click();
  await expect(
    sources.getByRole("textbox", { name: "Feed URL", exact: true }),
  ).toBeVisible();
  await sources
    .getByLabel("Name", { exact: true })
    .fill("Invalid controlled source");
  await sources
    .getByLabel("Feed URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml?test=1");
  await sources.getByRole("button", { name: "Add", exact: true }).click();
  await expect(sources.getByRole("alert")).toBeVisible();
  await expect(sources.getByLabel("Name", { exact: true })).toHaveValue(
    "Invalid controlled source",
  );
  await expect(sources.locator(".news-source")).toHaveCount(0);
  await page.getByRole("button", { name: "Close dialog", exact: true }).click();
  await page.goto(url + "/#agents/settings");
  const instructions = page.getByRole("textbox", {
    name: "Instructions",
    exact: true,
  });
  await instructions.fill("Preserve these unsaved agent instructions");
  await expect(page.locator(".ai-connection")).toHaveCount(0);
  await page
    .getByRole("button", { name: "Providers", exact: true })
    .last()
    .click();
  const provider = page.getByRole("region", { name: "Providers", exact: true });
  await expect(provider.locator(".ai-connection")).toHaveCount(1);
  await revealControl(
    page,
    provider.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await provider
    .getByLabel("API key", { exact: true })
    .fill("fased-test-discarded-editor");
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page
    .getByRole("button", { name: "Providers", exact: true })
    .last()
    .click();
  await revealControl(
    page,
    provider.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await expect(provider.getByLabel("API key", { exact: true })).toHaveValue("");
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await expect(instructions).toHaveValue(
    "Preserve these unsaved agent instructions",
  );
  await page.goto(url + "/#agents/tasks");
  await expect(
    page
      .getByRole("navigation", { name: "Task views", exact: true })
      .getByRole("button", { name: "Tasks", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "playbooks", exact: true }),
  ).toHaveCount(0);
  await page.goto(url + "/#agents/memory");
  await expect(page.getByText("PROFILE ONLY", { exact: true })).toHaveCount(0);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const capability of [
    "aiConnections",
    "walletSigner",
    "financialExecution",
    "updater",
  ])
    expect(health.capabilities[capability]).toBe(false);
});

test("Dashboard joins provider agent Chat reviewed task result and memory without live authority", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  const fixtureOrigins = new Set([new URL(url).origin]);
  page.on("request", (r) => {
    if (!fixtureOrigins.has(new URL(r.url()).origin)) external.push(r.url());
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  const p = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", p]);
  const original = await read();
  const news = page.locator('[data-widget="news"]');
  await news.locator("summary").click();
  await news.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(news).toHaveCount(0);
  await page.getByRole("button", { name: "Add widgets", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Widgets", exact: true })
    .getByRole("button", { name: "News", exact: true })
    .click();
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await news.locator("summary").click();
  await news.getByLabel("News size", { exact: true }).selectOption("tall");
  await news.getByRole("button", { name: "Move up", exact: true }).click();
  await news.locator("summary").click();
  const order = await page
    .locator("[data-widget]")
    .evaluateAll((els) => els.map((el) => el.getAttribute("data-widget")));
  await page.reload();
  await expect(news).toHaveClass(/size-tall/);
  expect(
    await page
      .locator("[data-widget]")
      .evaluateAll((els) => els.map((el) => el.getAttribute("data-widget"))),
  ).toEqual(order);
  expect((await read()).revision).toBe(original.revision);
  await news.locator("summary").click();
  await news.getByRole("button", { name: "Expand", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "News", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close dialog", exact: true }).click();
  await page.screenshot({
    path: `.evidence/dashboard-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  const openSetup = async () => {
    await page.locator(".shell-brand").click();
    await page.getByRole("button", { name: "Setup", exact: true }).click();
    return page.getByRole("dialog", { name: "Instance", exact: true });
  };
  const instance = await openSetup();
  await instance
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await expect(instance).not.toBeVisible();
  await revealControl(
    page,
    editor.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await editor
    .getByLabel("API key", { exact: true })
    .fill("fased-test-dashboard-disposable-key");
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-dashboard-vault-password");
  await editor.getByRole("button", { name: "Save", exact: true }).click();
  await expect(editor.getByRole("status")).toContainText("OpenAI");
  const primary = await privateCLI(["ai-connection"]);
  expect(primary.ref).not.toBe("");
  await editor
    .getByRole("button", { name: "Add provider", exact: true })
    .click();
  await editor.getByLabel("Name", { exact: true }).fill("Research");
  await editor
    .getByLabel("API key", { exact: true })
    .fill("fased-test-second-dashboard-key");
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-dashboard-vault-password");
  await editor.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(2);
  const providerAccounts = await privateCLI(["ai-connection"]);
  const connection = providerAccounts.connections.find(
    (a: any) => a.name === "Research",
  );
  expect(providerAccounts.ref).toBe(primary.ref);
  expect(connection.ref).not.toBe(primary.ref);
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await (
    await openSetup()
  )
    .getByRole("button", { name: "Agents", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Agents", exact: true }),
  ).toBeVisible();
  await page
    .locator(".agent-header-actions")
    .getByRole("combobox", { name: "Agent", exact: true })
    .selectOption(p);
  const provider = page.getByLabel("Provider", { exact: true });
  await revealControl(page, provider);
  const agentModel = page.getByLabel("Model", { exact: true });
  await expect(agentModel).toHaveJSProperty("tagName", "SELECT");
  await provider.selectOption("openai");
  await expect(agentModel).toHaveValue("gpt-6.1-terra");
  await provider.selectOption("none");
  await expect(agentModel).toHaveValue("");
  await provider.selectOption("openai");
  await expect(agentModel).toHaveValue("gpt-6.1-terra");
  await page
    .getByLabel("Account", { exact: true })
    .selectOption(connection.ref);
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await read()).profiles.find((x: { id: string }) => x.id === p).model,
    )
    .toBe("gpt-6.1-terra");
  await page.goto(url + "/#agents/chat");
  await page.getByRole("button", { name: "Start chat", exact: true }).click();
  const input = page.getByLabel("Message", { exact: true });
  await expect(page.locator(".reply-model-menu>summary")).toHaveText(
    "gpt-6.1-terra",
  );
  await expect(page.locator(".reply-model-menu>summary")).toHaveAttribute(
    "title",
    "gpt-6.1-terra",
  );
  await expect(page.getByLabel("Provider", { exact: true })).not.toBeVisible();
  await input.fill("Preserve this draft across Dashboard");
  await page.locator(".shell-brand").click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  await page.locator('.shell-top-nav>a[href="#agents/chat"]').click();
  await expect(input).toHaveValue("Preserve this draft across Dashboard");
  await input.fill("Help me draft a reminder to inspect SOL risk");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Allow replies", exact: true })
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(async () => (await read()).aiRequests.at(-1)?.state)
    .toBe("completed");
  let v = await read();
  const q = v.aiRequests.at(-1);
  expect(q.connectionRef).toBe(connection.ref);
  expect(q.model).toBe("gpt-6.1-terra");
  await page.screenshot({
    path: `.evidence/chat-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await page.getByRole("button", { name: "Review task", exact: true }).click();
  await page
    .getByLabel("Reminder task name", { exact: true })
    .fill("Daily SOL review");
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
    .getByRole("button", { name: "Run now: Daily SOL review", exact: true })
    .click();
  await expect.poll(async () => (await read()).runs.length).toBe(1);
  expect((await read()).runs[0].outcome).toBe("reminded");
  await page.getByRole("button", { name: "Discuss", exact: true }).click();
  await expect(page.locator(".local-messages")).toContainText(
    "Inspect the saved SOL risk limits",
  );
  const resultMessage = (await read()).messages.at(-1);
  expect(resultMessage.text).toContain((await read()).runs[0].id);
  await page
    .getByRole("button", { name: "Remember this", exact: true })
    .last()
    .click();
  await page
    .getByLabel("Fact", { exact: true })
    .fill("Prefer daily SOL risk summaries");
  await page.getByRole("button", { name: "Save memory", exact: true }).click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  expect(JSON.stringify((await read()).memories[0])).toContain(
    resultMessage.id,
  );
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await expect(
    page.getByText("Prefer daily SOL risk summaries", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: `.evidence/connected-memory-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.locator(".shell-brand").click();
  await expect(page.locator('[data-widget="tasks"]')).toContainText(
    "Daily SOL review",
  );
  await page.screenshot({
    path: `.evidence/dashboard-populated-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page
    .locator('[data-widget="tasks"] button.dashboard-row')
    .filter({ hasText: "Daily SOL review" })
    .click();
  await expect(
    page.getByRole("complementary", { name: "Task details", exact: true }),
  ).toContainText("Daily SOL review");
  if (isMobile)
    await page.getByRole("button", { name: "More", exact: true }).click();
  await page.getByRole("button", { name: /Notifications, \d+ unread/ }).click();
  const inbox = page.getByRole("dialog", {
    name: "Notification inbox",
    exact: true,
  });
  await expect(inbox).not.toContainText("Durable prototype events");
  await inbox
    .getByRole("button", { name: "Result", exact: true })
    .last()
    .click();
  await expect(inbox).toBeHidden();
  await expect(
    page.getByRole("complementary", { name: "Task details", exact: true }),
  ).toContainText("Daily SOL review");
  await page.evaluate(() => {
    location.hash = "activity";
  });
  await page
    .locator("main")
    .getByRole("button", { name: "Result", exact: true })
    .last()
    .click();
  await expect(page).toHaveURL(/#agents\/tasks$/);
  await expect(
    page.getByRole("complementary", { name: "Task details", exact: true }),
  ).toContainText("Daily SOL review");
  await (
    await openSetup()
  )
    .getByRole("button", { name: "Wallets", exact: true })
    .click();
  await revealControl(page, page.getByLabel("Name", { exact: true }), "");
  await page.getByLabel("Name", { exact: true }).fill("Joined watch");
  await page
    .getByLabel("Disposable public key (64 lowercase hex)")
    .fill("4".repeat(64));
  await page
    .getByRole("button", { name: "Save public account", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Joined watch", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  await expect(page.getByLabel("Network", { exact: true })).toHaveValue("Demo");
  await page.getByRole("button", { name: "Check", exact: true }).click();
  await expect(
    page.getByText("Verified (test)", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["accounts"])).record.accounts[0]?.rpc?.endpoint,
    )
    .toBe("https://rpc.fased.invalid/solana");
  const accounts = (await privateCLI(["accounts"])).record;
  await page.getByRole("button", { name: "Close dialog", exact: true }).click();
  // Read-only network-bound views must show their real network, never Demo.
  for (const network of ["devnet", "mainnet-beta"]) {
    await page.route("**/api/v1/accounts", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      body.record.accounts[0].network = network;
      await route.fulfill({ response, json: body });
    });
    await page.reload();
    await page.getByRole("button", { name: "RPC", exact: true }).click();
    await expect(page.getByLabel("Network", { exact: true })).toHaveValue(
      network,
    );
    await page
      .getByRole("button", { name: "Close dialog", exact: true })
      .click();
    await page.unroute("**/api/v1/accounts");
  }
  expect((await privateCLI(["accounts"])).record).toEqual(accounts);
  await (
    await openSetup()
  )
    .getByRole("button", { name: "Open Chat", exact: true })
    .click();
  await expect(page.getByLabel("Message", { exact: true })).toBeVisible();
  v = await read();
  const retained = {
    tasks: v.tasks,
    runs: v.runs,
    memories: v.memories,
    requests: v.aiRequests,
  };
  await stop();
  await start(true);
  fixtureOrigins.add(new URL(url).origin);
  const nextLogin = spawn(binary, [
    "login",
    "--data-dir",
    dir,
    "--password-stdin",
  ]);
  nextLogin.stdin!.end(fixturePassword + "\n");
  expect((await once(nextLogin, "exit"))[0]).toBe(0);
  v = await read();
  expect((await privateCLI(["accounts"])).record).toEqual(accounts);
  expect({
    tasks: v.tasks,
    runs: v.runs,
    memories: v.memories,
    requests: v.aiRequests,
  }).toEqual(retained);
  expect(
    (await privateCLI(["agents", "--profile", "news-researcher"])).memories,
  ).toHaveLength(0);
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

test("provider editor follows core credential admission without enabling live calls", async ({
  page,
}) => {
  await stop();
  await start();
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  const status = await page.request.get(url + "/api/v1/ai/connection", {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(status.ok()).toBe(true);
  const connection = await status.json();
  expect(connection.liveCallsEnabled).toBe(false);
  expect(connection.credentialModes).toEqual(["test"]);
  let saved: Record<string, unknown> | undefined;
  // UI-only capability injection. POST is intercepted and never reaches a vault,
  // model endpoint or live admission path. Ordinary server remains closed.
  await page.route("**/api/v1/ai/connection", async (route) => {
    if (route.request().method() === "POST") {
      saved = route.request().postDataJSON();
      await route.fulfill({
        json: {
          credentialMode: "openai",
          credentialModes: ["openai"],
          ref: "",
          model: "gpt-6.1-terra",
          locked: true,
          liveCallsEnabled: false,
        },
      });
      return;
    }
    // Reuse the immutable core status; refreshing the editor can cancel GETs.
    // Do not leave a second upstream fetch racing that UI cancellation.
    await route.fulfill({
      json: { ...connection, credentialModes: ["openai"] },
    });
  });
  await page.getByRole("button", { name: "Setup", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Instance", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await revealControl(
    page,
    editor.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await editor
    .getByLabel("API key", { exact: true })
    .fill("disposable-api-input-not-issued-by-any-provider");
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-admission-vault-password");
  await editor.getByRole("button", { name: "Save", exact: true }).click();
  await expect.poll(() => saved?.credentialMode).toBe("openai");
  await expect(editor.getByLabel("API key", { exact: true })).toHaveValue("");
  await page.unrouteAll({ behavior: "wait" });
  await page.reload();
  await expect(page).toHaveURL(/#providers$/);
  await expect(editor).toBeVisible();
  await revealControl(
    page,
    editor.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await expect(editor.getByLabel("API key", { exact: true })).toBeVisible();
  await expect(editor.getByRole("status")).toContainText("No connection saved");
});

test("multiple accounts keep scheduled agent selection and refuse a stale provider editor", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/tasks");
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
  const passphrase = "disposable-multi-account-password";
  const a = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Primary",
    key: "fased-test-primary-account-key",
    passphrase,
  });
  const b = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    name: "Research",
    key: "fased-test-research-account-key",
    passphrase,
  });
  const profileId = "trading-analyst";
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  await command({
    action: "profile-update",
    name: "Analyst",
    provider: "openai",
    model: "gpt-6.1-sol",
    accountRef: b.ref,
  });
  await command({ action: "session-create", name: "Account schedule" });
  await page.reload();
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  await openAISchedules(page);
  const perCall = page.getByLabel("Per-call cost cap (micro USD)", {
    exact: true,
  });
  const total = page.getByLabel("AI total cost cap (micro USD)", {
    exact: true,
  });
  await revealControl(page, perCall, "");
  await expect(perCall).toHaveValue("58240");
  await perCall.fill("100");
  await total.fill("200");
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "models-refresh",
    ref: b.ref,
  });
  await page.evaluate(() =>
    window.dispatchEvent(new Event("ai-connection-changed")),
  );
  await expect(perCall).toHaveValue("100");
  await expect(total).toHaveValue("200");
  await perCall.fill("58240");
  await total.fill("116480");
  await revealControl(page, page.getByLabel("AI task title"), "");
  await page.getByLabel("AI task title").fill("Research account only");
  await page
    .getByLabel("AI analysis prompt")
    .fill("Summarize the saved fixture context");
  await page
    .getByRole("button", { name: "Prepare AI task review", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks.length).toBe(1);
  expect((await read()).tasks[0].ai.connectionRef).toBe(b.ref);
  expect((await read()).tasks[0].ai.model).toBe("gpt-6.1-sol");
  const retainedTask = (await read()).tasks[0];
  const refreshed = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "models-refresh",
    ref: b.ref,
  });
  expect(refreshed.catalog.policy).toBe("latest-only");
  expect(
    refreshed.catalog.models
      .filter((m: any) => m.selectable)
      .map((m: any) => m.id),
  ).toEqual(["gpt-6.1-terra", "gpt-6.1-sol", "gpt-6-astra"]);
  expect((await read()).tasks[0]).toEqual(retainedTask);
  expect((await read()).tasks[0].state).toBe("review");
  await page.goto(url + "/#agents/settings");
  await page
    .getByRole("region", { name: "Agent settings", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await editor.locator(`[data-account="${b.ref}"]`).click();
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "remove",
    ref: b.ref,
    passphrase,
  });
  await page.evaluate(() =>
    window.dispatchEvent(new Event("ai-connection-changed")),
  );
  await expect(editor.getByRole("alert")).toContainText(
    "Previous connection unavailable",
  );
  await revealControl(
    page,
    editor.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
  await expect(
    editor.getByRole("button", { name: "Lock", exact: true }),
  ).toBeDisabled();
  expect((await privateCLI(["ai-connection"])).ref).toBe(a.ref);
  expect((await privateCLI(["ai-connection"])).locked).toBe(false);
  expect((await read()).tasks[0].ai.connectionRef).toBe(b.ref);
});

for (const location of ["local", "remote"]) {
  test(`provider sign-in ${location} keeps its account route through configured Chat`, async ({
    page,
  }) => {
    test.setTimeout(90000);
    await stop();
    await start(true);
    // Runtime fixtures stay standalone/Local. Simulate only the public SSH
    // Remote descriptor to verify its UI consumer; this is not VPS acceptance.
    if (location === "remote")
      await page.route("**/api/v1/health", async (route) => {
        const response = await route.fetch();
        const body = await response.json();
        body.connection = { location: "remote", transport: "loopback" };
        await route.fulfill({ response, json: body });
      });
    const external: string[] = [];
    const signInStarts: { remote?: boolean }[] = [];
    page.on("request", (r) => {
      const host = new URL(r.url()).hostname;
      if (host !== "127.0.0.1" && host !== "localhost") external.push(r.url());
      if (
        r.method() === "POST" &&
        new URL(r.url()).pathname === "/api/v1/ai/connection"
      ) {
        const body = r.postDataJSON();
        if (body.action === "sign-in-start") signInStarts.push(body);
      }
    });
    await page.goto(url + "/#agents/settings");
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
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin!.end(fixturePassword + "\n");
    expect((await once(login, "exit"))[0]).toBe(0);
    const passphrase = "disposable-sign-in-ui-vault";
    const original = await privateCLI(["ai-connection", "--request-stdin"], {
      action: "save",
      key: "fased-test-sign-in-ui-primary",
      passphrase,
    });
    const before = await privateCLI(["agents"]);
    await page.goto(url + "/#agents/chat");
    await page.getByRole("button", { name: "New agent", exact: true }).click();
    const creation = page.getByRole("dialog", {
      name: "New agent",
      exact: true,
    });
    await creation.getByLabel("Name", { exact: true }).fill("Plan researcher");
    await creation
      .getByRole("button", { name: "Connect provider", exact: true })
      .click();
    const editor = page.getByRole("region", { name: "Providers", exact: true });
    await editor
      .getByRole("button", { name: "Add provider", exact: true })
      .click();
    await editor.getByRole("button", { name: "Sign in", exact: true }).click();
    await editor.getByLabel("Name", { exact: true }).fill("ChatGPT research");
    await editor
      .getByRole("button", { name: "Continue with ChatGPT", exact: true })
      .click();
    if (location === "remote") expect(signInStarts.at(-1)?.remote).toBe(true);
    const link = editor.getByRole("link", {
      name: "Open sign-in",
      exact: true,
    });
    await expect(link).toBeVisible();
    if (location === "remote") {
      await editor
        .getByLabel("Redirect URL", { exact: true })
        .fill((await link.getAttribute("href"))!);
    } else {
      const opened = page.waitForEvent("popup");
      await link.click();
      const popup = await opened;
      await popup.waitForLoadState("domcontentloaded");
      await popup.close();
      await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    }
    await editor
      .getByLabel("Vault passphrase", { exact: true })
      .fill(passphrase);
    await expect(
      editor.getByRole("button", { name: "Finish", exact: true }),
    ).toBeEnabled();
    await editor.getByRole("button", { name: "Finish", exact: true }).click();
    await expect
      .poll(
        async () => (await privateCLI(["ai-connection"])).connections.length,
      )
      .toBe(2);
    const accounts = await privateCLI(["ai-connection"]);
    expect(accounts.ref).toBe(original.ref);
    const account = accounts.connections.find(
      (a: any) => a.authMethod === "sign-in",
    );
    expect(account).toMatchObject({
      name: "ChatGPT research",
      provider: "openai",
      authMethod: "sign-in",
      planUse: true,
      liveCallsEnabled: false,
    });
    expect((await privateCLI(["agents"])).revision).toBe(before.revision);
    await page.getByRole("button", { name: "Back", exact: true }).click();
    await expect(creation).toBeVisible();
    await expect(creation.getByLabel("Name", { exact: true })).toHaveValue(
      "Plan researcher",
    );
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
    const profile = (await privateCLI(["agents"])).profiles.find(
      (p: any) => p.name === "Plan researcher",
    );
    expect(profile.connectionRef).toBe(account.ref);
    const read = () => privateCLI(["agents", "--profile", profile.id]);
    // Model a completed worker journal arriving before the browser's next
    // snapshot. CLI completion alone must not make a stale UI ready to write.
    let delayCompletedView = location === "local";
    await page.route("**/api/v1/agents?**", async (route) => {
      const response = await route.fetch();
      const body = await response.json();
      if (
        delayCompletedView &&
        body.profileId === profile.id &&
        body.aiRequests?.some((q: { state: string }) => q.state === "completed")
      )
        await new Promise((resolve) => setTimeout(resolve, 1000));
      await route.fulfill({ response, json: body });
    });
    await page.getByRole("button", { name: "Start chat", exact: true }).click();
    await page
      .getByLabel("Message", { exact: true })
      .fill("Summarize this synthetic note");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    const replyReview = page.getByRole("dialog", {
      name: "Allow replies",
      exact: true,
    });
    await expect(
      replyReview.getByText("App limits", { exact: true }),
    ).toBeVisible();
    await expect(
      replyReview.getByText("Maximum cost", { exact: true }),
    ).toHaveCount(0);
    await replyReview
      .getByRole("button", { name: "Allow and send", exact: true })
      .click();
    await expect
      .poll(async () => (await read()).aiRequests.at(-1)?.state)
      .toBe("completed");
    const reply = (await read()).aiRequests.at(-1);
    // The backend may finish between browser polls. Await the UI's completed
    // view before creating a task; never rebase/retry a stale mutation.
    await expect(
      page.getByRole("button", { name: "Stop", exact: true }),
    ).toHaveCount(0);
    await expect(page.locator(".ai-answer").last()).toHaveText(reply.answer);
    delayCompletedView = false;
    expect(reply).toMatchObject({
      connectionRef: account.ref,
      costBasis: "chatgpt-plan-reported-usage",
      spentMicrousd: 0,
      heldMicrousd: 0,
      limits: { budgetMicrousd: 0 },
      policy: { wireVersion: "openai-signin-v2" },
    });
    expect(reply.inputTokens).toBeGreaterThan(0);
    expect(reply.outputTokens).toBeGreaterThan(0);
    if (location === "local") {
      await page.goto(url + "/#agents/tasks");
      await openAISchedules(page);
      const schedule = page.getByRole("region", {
        name: "Scheduled AI steps",
        exact: true,
      });
      await revealControl(page, schedule.getByLabel("AI task title"), "");
      await schedule.getByLabel("AI task title").fill("Plan summary");
      await schedule
        .getByLabel("AI context conversation")
        .selectOption((await read()).sessions[0].id);
      await schedule
        .getByLabel("AI analysis prompt")
        .fill("Summarize one fictional event");
      await schedule.getByLabel("AI run count").fill("1");
      await expect(
        schedule.getByLabel("Per-call cost cap (micro USD)"),
      ).toHaveCount(0);
      await schedule
        .getByRole("button", { name: "Prepare AI task review", exact: true })
        .click();
      const task = schedule.getByRole("article", {
        name: "AI task Plan summary",
        exact: true,
      });
      await expect(task).toBeVisible();
      const savedTask = (await read()).tasks.at(-1);
      expect(savedTask.ai).toMatchObject({
        connectionRef: account.ref,
        budgetMicrousd: 0,
        limits: { budgetMicrousd: 0 },
        policy: { wireVersion: "openai-signin-v2" },
      });
      await task
        .getByText("Review AI task disclosure", { exact: true })
        .click();
      await task
        .getByLabel("Allow this frozen context, schedule and limits")
        .check();
      await task
        .getByRole("button", { name: "Authorize task", exact: true })
        .click();
      await expect(task.getByText("scheduled", { exact: true })).toBeVisible();
      await page.goto(url + "/#agents/settings");
      await page
        .getByRole("region", { name: "Agent settings", exact: true })
        .getByRole("button", { name: "Providers", exact: true })
        .click();
      const accessEditor = page.getByRole("region", {
        name: "Providers",
        exact: true,
      });
      await accessEditor.locator(`[data-account="${account.ref}"]`).click();
      const background = accessEditor
        .getByText("Background access", { exact: true })
        .locator("..");
      await background.locator(":scope > summary").click();
      await background.getByLabel("Plan summary", { exact: true }).check();
      await background
        .getByRole("button", { name: "Review access", exact: true })
        .click();
      await background
        .getByLabel("Vault passphrase", { exact: true })
        .fill(passphrase);
      await background
        .getByRole("button", { name: "Allow task access", exact: true })
        .click();
      await expect
        .poll(
          async () =>
            (await privateCLI(["ai-connection"])).connections.find(
              (c: any) => c.ref === account.ref,
            )?.taskAccess?.budgetMicrousd,
        )
        .toBe(0);
      await page.getByRole("button", { name: "Back", exact: true }).click();
      await page.goto(url + "/#agents/tasks");
      await openAISchedules(page);
      await schedule
        .getByRole("article", { name: "AI task Plan summary", exact: true })
        .getByRole("button", { name: "Evaluate now", exact: true })
        .click();
      await expect
        .poll(async () => (await read()).tasks.at(-1)?.state)
        .toBe("completed");
      const result = (await read()).aiRequests.at(-1);
      expect(result).toMatchObject({
        taskId: savedTask.id,
        connectionRef: account.ref,
        costBasis: "chatgpt-plan-reported-usage",
        spentMicrousd: 0,
        heldMicrousd: 0,
      });
      await page
        .getByRole("navigation", { name: "Task views", exact: true })
        .getByRole("button", { name: "Results", exact: true })
        .click();
      await page
        .getByRole("region", { name: "Run results", exact: true })
        .getByRole("button", { name: "Discuss", exact: true })
        .click();
      const source = (await read()).messages.at(-1);
      await page
        .getByRole("button", { name: "Remember this", exact: true })
        .last()
        .click();
      await page
        .getByLabel("Fact", { exact: true })
        .fill("Keep fictional event summaries brief");
      await page
        .getByRole("button", { name: "Save memory", exact: true })
        .click();
      await expect.poll(async () => (await read()).memories.length).toBe(1);
      expect(JSON.stringify((await read()).memories[0])).toContain(source.id);
      expect(
        (await privateCLI(["agents", "--profile", "news-researcher"])).memories,
      ).toHaveLength(0);
    }
    expect(external).toEqual([]);
  });
}

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
  await page.getByRole("button", { name: "Start chat", exact: true }).click();
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
    .getByLabel("Fact", { exact: true })
    .fill("Prefer daily SOL risk summaries");
  await page.getByRole("button", { name: "Save memory", exact: true }).click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await expect(
    page.getByText("Prefer daily SOL risk summaries", { exact: true }),
  ).toBeVisible();
  const retained = await read();
  expect(JSON.stringify(retained.memories[0])).toContain(source.id);
  expect(
    (await privateCLI(["agents", "--profile", "news-researcher"])).memories,
  ).toHaveLength(0);
  await page.screenshot({
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

test("shared Add Provider binds native accounts and latest models across Chat and tasks", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/settings");
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
  const passphrase = "disposable-native-provider-password";
  const primary = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Primary",
    key: "fased-test-primary-preserved",
    passphrase,
  });
  const refs: Record<string, string> = {};
  await page
    .getByRole("region", { name: "Agent settings", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  for (const provider of ["anthropic", "xai"]) {
    await editor
      .getByRole("button", { name: "Add provider", exact: true })
      .click();
    await editor.locator(`[data-provider=${provider}] > button`).click();
    await expect(editor.getByLabel("API key", { exact: true })).toBeVisible();
    await expect(
      editor.getByRole("button", { name: "Unlock", exact: true }),
    ).toHaveCount(0);
    await expect(
      editor.getByRole("button", { name: "Lock", exact: true }),
    ).toHaveCount(0);
    await expect(
      editor.getByRole("button", { name: "Remove", exact: true }),
    ).toHaveCount(0);

    await editor
      .getByLabel("Name", { exact: true })
      .fill(provider === "anthropic" ? "Claude research" : "Grok research");
    await editor
      .getByLabel("API key", { exact: true })
      .fill(`fased-test-${provider}-disposable-ui-key`);
    await editor
      .getByLabel("Vault passphrase", { exact: true })
      .fill(passphrase);
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await privateCLI(["ai-connection"])).connections.find(
            (a: any) => a.provider === provider,
          )?.locked,
      )
      .toBe(false);
    const account = (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.provider === provider,
    );
    refs[provider] = account.ref;
    await expect(editor.getByLabel("Name", { exact: true })).toHaveValue(
      account.name,
    );
    await revealControl(page, editor.getByLabel("Name", { exact: true }), "");
    await editor
      .getByLabel("Name", { exact: true })
      .fill("Unsaved provider name");
    const refreshButton = editor.getByRole("button", {
      name: "Refresh models",
      exact: true,
    });
    const refreshed = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/v1/ai/connection") &&
        r.request().method() === "POST",
    );
    await refreshButton.click();
    expect((await refreshed).status()).toBe(200);
    await expect(refreshButton).toBeEnabled();
    await expect(editor.getByLabel("Name", { exact: true })).toHaveValue(
      "Unsaved provider name",
    );
    await editor.getByLabel("Name", { exact: true }).fill(account.name);
    expect(account.liveCallsEnabled).toBe(false);
    expect(
      account.catalog.models
        .filter((m: any) => m.selectable)
        .map((m: any) => m.id),
    ).toEqual(
      provider === "anthropic"
        ? [
            "claude-haiku-5-5",
            "claude-sonnet-5-5",
            "claude-opus-5-5",
            "claude-fable-5-1",
          ]
        : ["grok-4.7"],
    );
  }
  expect((await privateCLI(["ai-connection"])).ref).toBe(primary.ref);
  await page.screenshot({
    path: `.evidence/provider-adapters/add-provider-${isMobile ? "mobile" : "desktop"}.png`,
  });
  await page.getByRole("button", { name: "Back", exact: true }).click();
  const profileId = "trading-analyst",
    read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: Record<string, unknown>) =>
    privateCLI(["agents", "--request-stdin"], {
      revision: (await read()).revision,
      profileId,
      ...c,
    });
  await command({ action: "session-create", name: "Native providers" });
  const retained: any[] = [];
  for (const provider of ["anthropic", "xai"]) {
    await page.goto(url + "/#agents");
    await page
      .locator(".agent-header-actions")
      .getByRole("combobox", { name: "Agent", exact: true })
      .selectOption(profileId);
    const providerSelect = page.getByLabel("Provider", { exact: true });
    await revealControl(page, providerSelect);
    await providerSelect.selectOption(provider);
    await expect(page.getByLabel("Account", { exact: true })).toHaveValue(
      refs[provider],
    );
    const model = provider === "anthropic" ? "claude-sonnet-5-5" : "grok-4.7";
    await page.getByLabel("Model", { exact: true }).selectOption(model);
    await page.getByRole("button", { name: "Save agent", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await read()).profiles.find((p: any) => p.id === profileId).model,
      )
      .toBe(model);
    await page.goto(url + "/#agents/chat");
    await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
    await page
      .getByLabel("Message", { exact: true })
      .fill("Summarize my disposable fixture note");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    const review = page.getByRole("dialog", {
      name: "Allow replies",
      exact: true,
    });
    await expect(
      review.getByText(provider === "anthropic" ? "Anthropic" : "xAI", {
        exact: true,
      }),
    ).toBeVisible();
    if (provider === "xai") {
      await expect(
        review.getByText("Output reserve", { exact: true }),
      ).toBeVisible();
      await expect(
        review.getByTitle("Includes reasoning tokens", { exact: true }),
      ).toContainText("500,000");
    }
    await review
      .getByRole("button", { name: "Allow and send", exact: true })
      .click();
    await expect
      .poll(async () => {
        const requests = (await read()).aiRequests;
        return { count: requests.length, state: requests.at(-1)?.state };
      })
      .toEqual({ count: retained.length + 1, state: "completed" });
    const result = (await read()).aiRequests.at(-1);
    expect(result.model).toBe(model);
    expect(result.connectionRef).toBe(refs[provider]);
    expect(result.policy.provider).toBe(provider);
    if (provider === "xai")
      expect(result.policy.billableOutputTokens).toBe(500000);
    expect(result.answer).toContain("Development fixture response");
    retained.push(result);
    await page.goto(url + "/#agents/tasks");
    await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
    await openAISchedules(page);
    const title = page.getByLabel("AI task title");
    await revealControl(page, title, "");
    await title.fill(provider + " fixture task");
    await page
      .getByLabel("AI analysis prompt")
      .fill("Keep this account and model");
    await page
      .getByRole("button", { name: "Prepare AI task review", exact: true })
      .click();
    await expect
      .poll(async () => (await read()).tasks.length)
      .toBe(retained.length);
    const task = (await read()).tasks.at(-1);
    expect(task.ai.model).toBe(model);
    expect(task.ai.connectionRef).toBe(refs[provider]);
  }
  const final = await read();
  expect(final.aiRequests).toEqual(retained);
  expect(final.tasks[0].ai.model).toBe("claude-sonnet-5-5");
  expect(final.tasks[1].ai.model).toBe("grok-4.7");
  await page.screenshot({
    path: `.evidence/provider-adapters/native-task-${isMobile ? "mobile" : "desktop"}.png`,
  });
});

test("scoped pilot controls keep review allow and revoke exact while ordinary core stays closed (UI fixture)", async ({
  page,
  isMobile,
}) => {
  await stop();
  await start(true);
  await page.goto(url + "/#agents/settings");
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
  const passphrase = "disposable-pilot-browser-password";
  const account = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    name: "Pilot fixture",
    key: "fased-test-pilot-browser-key",
    passphrase,
  });
  const before = await readFile(join(dir, "ai-credentials.json"));
  await page
    .getByRole("region", { name: "Agent settings", exact: true })
    .getByRole("button", { name: "Providers", exact: true })
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await expect(editor.getByText("Pilot", { exact: true })).toHaveCount(0);
  let permit: any;
  let state = "";
  const commands: any[] = [];
  await page.route("**/api/v1/ai/connection", async (route) => {
    const decorate = (value: any): any => ({
      ...value,
      ...(value.ref === account.ref
        ? {
            pilotAvailable: true,
            pilotEnrollment: true,
            pilot: state ? permit : undefined,
            pilotState: state || undefined,
          }
        : {}),
      ...(value.connections
        ? { connections: value.connections.map(decorate) }
        : {}),
    });
    if (route.request().method() === "GET") {
      const response = await route.fetch();
      return route.fulfill({ response, json: decorate(await response.json()) });
    }
    const command = route.request().postDataJSON();
    if (!command.action.startsWith("pilot-")) return route.continue();
    commands.push(command);
    if (command.action === "pilot-review") {
      const now = Math.floor(Date.now() / 1000);
      permit = {
        id: "fixture-reviewed-pilot",
        instanceId: "fixture-instance",
        bootId: "fixture-boot",
        connectionRef: account.ref,
        connectionEpoch: 1,
        accessEpoch: 0,
        profileId: command.profileId,
        model: "gpt-6.1-terra",
        policyId: "fixture-current-policy",
        revision: command.revision,
        createdAt: now,
        expiresAt: now + 900,
        maxRequests: 4,
        budgetMicrousd: 500000,
        laneBudgetMicrousd: 250000,
        limits: {
          maxInputTokens: 2000,
          maxOutputTokens: 256,
          deadlineSeconds: 5,
          budgetMicrousd: 100000,
        },
        usedRequests: 0,
        allocatedMicrousd: 0,
      };
      return route.fulfill({
        json: { ...decorate(account), pilotReview: permit },
      });
    }
    if (command.action === "pilot-allow") {
      expect(command.pilot).toEqual(permit);
      expect(command.consentDigest).toBe(permit.id);
      expect(command.passphrase).toBe(passphrase);
      state = "active";
    } else {
      expect(command).toEqual({ action: "pilot-revoke", ref: account.ref });
      state = "revoked";
    }
    return route.fulfill({ json: decorate(account) });
  });
  await page.evaluate(() =>
    window.dispatchEvent(new Event("ai-connection-changed")),
  );
  await expect(
    editor.getByRole("button", { name: "Add provider", exact: true }),
  ).toBeDisabled();
  const credentials = editor
    .locator("details.connection-options")
    .filter({ has: page.locator("summary", { hasText: /^Credentials$/ }) });
  await credentials.getByText("Credentials", { exact: true }).click();
  await credentials
    .getByLabel("API key", { exact: true })
    .fill("fased-test-pilot-replacement-refused");
  await credentials
    .getByLabel("Vault passphrase", { exact: true })
    .fill(passphrase);
  await expect(
    credentials.getByRole("button", { name: "Replace", exact: true }),
  ).toBeDisabled();
  await credentials.getByLabel("API key", { exact: true }).fill("");
  await credentials.getByLabel("Vault passphrase", { exact: true }).fill("");
  await credentials.getByText("Credentials", { exact: true }).click();
  await editor.getByText("Pilot", { exact: true }).click();
  const pilot = editor
    .locator("details")
    .filter({ has: page.locator("summary", { hasText: /^Pilot$/ }) });
  await pilot.getByRole("button", { name: "Review", exact: true }).click();
  await expect(pilot).toContainText("$0.50");
  await expect(pilot).toContainText("4 attempts");
  await pilot.getByLabel("Vault passphrase", { exact: true }).fill(passphrase);
  await pilot.getByRole("button", { name: "Allow pilot", exact: true }).click();
  await expect(pilot.getByRole("status")).toHaveText("Active");
  await expect(
    pilot.getByLabel("Vault passphrase", { exact: true }),
  ).toHaveCount(0);
  await page.screenshot({
    path: `.evidence/openai-pilot-gate/controls-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await pilot.getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(pilot.getByRole("status")).toHaveText("Stopped");
  expect(commands.map((c) => c.action)).toEqual([
    "pilot-review",
    "pilot-allow",
    "pilot-revoke",
  ]);
  expect(await readFile(join(dir, "ai-credentials.json"))).toEqual(before);
  const ordinary = await privateCLI(["ai-connection"]);
  expect(ordinary.pilot).toBeUndefined();
  expect(ordinary.liveCallsEnabled).toBe(false);
});

test("local provider connects in UI and shares configured agent Chat without API billing", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
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
  await fixtureProviderSelection("openai");
  if (isMobile) await page.goto(url + "/#agents/settings");
  await page
    .getByRole("button", { name: "Providers", exact: true })
    .first()
    .click();
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  await editor.locator('[data-provider="local"] > button').click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
    "http://127.0.0.1:11434/v1",
  );
  await editor.getByLabel("Name", { exact: true }).fill("Browser local");
  await expect(
    editor.getByLabel("API key (optional)", { exact: true }),
  ).toHaveValue("");
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-local-browser-vault");
  await editor.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["ai-connection"])).connections?.find(
          (a: any) => a.provider === "local",
        )?.locked,
    )
    .toBe(false);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "local",
  );
  expect(account.authMethod).toBe("local");
  expect(
    account.catalog.models.find((m: any) => m.id === "gemma4:e2b")?.selectable,
  ).toBe(true);
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Local browser analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "gemma4:e2b",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const view = await privateCLI(["agents"]);
  const created = view.profiles.find(
    (p: any) => p.name === "Local browser analyst",
  );
  const profile = created.id;
  await expect(page.getByLabel("Agent", { exact: true })).toHaveValue(profile);
  expect(created).toMatchObject({
    provider: "local",
    connectionRef: account.ref,
    model: "gemma4:e2b",
  });
  if (isMobile)
    await page.getByRole("button", { name: "More", exact: true }).click();
  await page
    .getByRole("button", { name: "Providers", exact: true })
    .first()
    .click();
  await page.reload();
  await expect(
    editor.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    editor.getByRole("heading", { name: "Local server", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Local browser conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Explain this synthetic local note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("gemma4:e2b");
  await expect(review).not.toContainText("$");
  await review
    .getByRole("button", { name: "Allow and send", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", profile])).aiRequests.at(-1)
          ?.state,
    )
    .toBe("completed");
  const result = (
    await privateCLI(["agents", "--profile", profile])
  ).aiRequests.at(-1);
  expect(result.connectionRef).toBe(account.ref);
  expect(result.costBasis).toBe("local-reported-usage");
  expect(result.spentMicrousd).toBe(0);
});

test("Providers page preserves agent drafts and shares account models without generating during setup", async ({
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
  await page.goto(url + "/#agents/settings");
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
  // An empty provider draft may return to No AI without resurrecting the
  // previous selection while account/model fields reset in the same event.
  await page.locator("details.profile-defaults > summary").click();
  await page.getByLabel("Provider", { exact: true }).selectOption("xai");
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue("xai");
  await page.getByLabel("Provider", { exact: true }).selectOption("none");
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue(
    "none",
  );
  await page.locator("details.profile-defaults > summary").click();
  await page
    .getByRole("textbox", { name: "Instructions", exact: true })
    .fill("Preserve this unsaved agent draft");
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Research account draft");
  await creation
    .getByRole("button", { name: "Connect provider", exact: true })
    .click();
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await expect(page).toHaveURL(/#providers$/);
  await expect(
    page.getByRole("dialog", { name: "Providers", exact: true }),
  ).toHaveCount(0);
  await expect(
    providers.getByRole("heading", {
      name: "Providers",
      level: 1,
      exact: true,
    }),
  ).toBeVisible();
  await expect(providers.locator(".provider-editor")).toHaveCSS(
    "background-color",
    "rgb(30, 30, 42)",
  );
  await expect(providers.locator(".provider-editor")).toHaveCSS(
    "border-radius",
    "8px",
  );

  await providers.locator('[data-provider="anthropic"] > button').click();
  await expect(
    providers.getByRole("heading", { name: "Anthropic", exact: true }),
  ).toBeVisible();
  await providers.getByLabel("Name", { exact: true }).fill("Claude research");
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-anthropic-page-disposable");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-full-page-vault-password");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(1);
  const account = (await privateCLI(["ai-connection"])).connections[0];
  await expect(
    providers.getByRole("region", { name: "Models", exact: true }),
  ).toContainText(account.catalog.models.find((m: any) => m.selectable).name);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  if (isMobile)
    expect(
      await providers
        .locator(".provider-brand-button")
        .first()
        .evaluate((e) => e.getBoundingClientRect().height),
    ).toBeGreaterThanOrEqual(44);
  await page.evaluate(() => scrollTo(0, 0));
  await page.screenshot({
    path: `.evidence/b02-agent-use/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await expect(creation).toBeVisible();
  await expect(creation.getByLabel("Name", { exact: true })).toHaveValue(
    "Research account draft",
  );
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
  );
  await creation
    .getByRole("button", { name: "Close dialog", exact: true })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Preserve this unsaved agent draft");
  const selectedAgent = page.getByLabel("Agent", { exact: true });
  const originalAgent = await selectedAgent.inputValue();
  const otherAgent =
    originalAgent === "news-researcher" ? "trading-analyst" : "news-researcher";
  const draftName = "Unsaved account research agent";
  await page
    .getByRole("textbox", { name: "Name", exact: true })
    .fill(draftName);
  await page.locator("details.profile-defaults > summary").click();
  await page.getByLabel("Provider", { exact: true }).selectOption("anthropic");
  await page
    .getByLabel("Model", { exact: true })
    .selectOption(
      account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
    );
  const draftModel = await page
    .getByLabel("Model", { exact: true })
    .inputValue();
  await selectedAgent.selectOption(otherAgent);
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).not.toHaveValue(draftName);
  await page
    .getByRole("textbox", { name: "Instructions", exact: true })
    .fill("Separate unsaved news instructions");
  await selectedAgent.selectOption(originalAgent);
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue(draftName);
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Preserve this unsaved agent draft");
  await page.locator("details.profile-defaults > summary").click();
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue(
    "anthropic",
  );
  await expect(page.getByLabel("Account", { exact: true })).toHaveValue(
    account.ref,
  );
  await expect(page.getByLabel("Model", { exact: true })).toHaveValue(
    draftModel,
  );
  await selectedAgent.selectOption(otherAgent);
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Separate unsaved news instructions");
  await selectedAgent.selectOption(originalAgent);

  const readOriginal = () => privateCLI(["agents", "--profile", originalAgent]);
  const beforeConcurrentEdit = await readOriginal();
  const savedOriginal = beforeConcurrentEdit.profiles.find(
    (p: any) => p.id === originalAgent,
  );
  expect(savedOriginal.name).not.toBe(draftName);
  await privateCLI(["agents", "--request-stdin"], {
    action: "profile-update",
    profileId: originalAgent,
    revision: beforeConcurrentEdit.revision,
    name: "Saved in another window",
    text: savedOriginal.persona,
    provider: savedOriginal.provider,
    model: savedOriginal.model,
    accountRef: savedOriginal.connectionRef || "",
  });
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
  await expect(page.locator(".storage-warning[role=alert]")).toContainText(
    /changed|reload|review/i,
  );
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue(draftName);
  expect(
    (await readOriginal()).profiles.find((p: any) => p.id === originalAgent)
      .name,
  ).toBe("Saved in another window");
  await page.getByRole("button", { name: "Reload", exact: true }).click();
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue("Saved in another window");
  await page
    .getByRole("textbox", { name: "Name", exact: true })
    .fill("Saved research agent");
  await page.locator("details.profile-defaults > summary").click();
  await page.getByLabel("Provider", { exact: true }).selectOption("anthropic");
  await page
    .getByLabel("Model", { exact: true })
    .selectOption(
      account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
    );
  await page.getByRole("button", { name: "Save agent", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await readOriginal()).profiles.find((p: any) => p.id === originalAgent)
          .connectionRef,
    )
    .toBe(account.ref);
  expect(
    (await readOriginal()).profiles.find((p: any) => p.id === originalAgent)
      .model,
  ).toBe(
    account.catalog.models.find((m: any) => m.recommended && m.selectable).id,
  );
  await selectedAgent.selectOption(otherAgent);
  await expect(
    page.getByRole("textbox", { name: "Instructions", exact: true }),
  ).toHaveValue("Separate unsaved news instructions");
  await selectedAgent.selectOption(originalAgent);
  await expect(
    page.getByRole("textbox", { name: "Name", exact: true }),
  ).toHaveValue("Saved research agent");
  await page.screenshot({
    path: `.evidence/b02-agent-use/agent-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await page.evaluate(() => {
    window.location.hash = "agents/chat";
  });
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Saved research conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Keep this unsent research question with its agent.");
  await page.evaluate(() => {
    window.location.hash = "providers";
  });
  await expect(
    page.getByRole("region", { name: "Providers", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("region", { name: "Providers", exact: true })
    .getByRole("button", { name: "Back", exact: true })
    .click();
  await page.evaluate(() => {
    window.location.hash = "agents/chat";
  });
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toHaveValue("Keep this unsent research question with its agent.");
  expect((await readOriginal()).aiRequests).toHaveLength(0);

  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(external).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});

test("New cloud accounts use the Providers page and shared agent Chat without generation at setup", async ({
  page,
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
  await page.goto(url + "/#providers");
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
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  const accounts: any[] = [];
  for (const provider of [
    "moonshot",
    "google",
    "openrouter",
    "qwen",
    "opencode",
  ]) {
    await providers.locator(`[data-provider="${provider}"] > button`).click();
    await providers
      .getByLabel("Name", { exact: true })
      .fill(provider + " browser");
    await providers
      .getByLabel("API key", { exact: true })
      .fill(`fased-test-${provider}-disposable-ui-key`);
    await providers
      .getByLabel("Vault passphrase", { exact: true })
      .fill("disposable-five-provider-vault");
    await providers.getByRole("button", { name: "Save", exact: true }).click();
    await expect
      .poll(
        async () => (await privateCLI(["ai-connection"])).connections?.length,
      )
      .toBe(accounts.length + 1);
    const account = (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.provider === provider,
    );
    accounts.push(account);
    const models = providers.getByRole("region", {
      name: "Models",
      exact: true,
    });
    await expect(models).toContainText(account.catalog.models[0].name);
    if (provider === "moonshot") {
      await expect(models).toContainText("Pricing unavailable");
      expect(account.catalog.models[0].selectable).toBe(false);
    } else expect(account.catalog.models[0].selectable).toBe(true);
    expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  }
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Google browser analyst");
  const account = accounts.find((a) => a.provider === "google");
  for (const a of accounts.filter((a) => a.provider !== "moonshot")) {
    await creation.getByLabel("Account", { exact: true }).selectOption(a.ref);
    await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
      a.catalog.models[0].id,
    );
    await expect(
      creation.getByRole("button", { name: "Create agent", exact: true }),
    ).toBeEnabled();
  }
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Google browser analyst",
  );
  expect(profile).toMatchObject({
    provider: "google",
    connectionRef: account.ref,
    model: account.catalog.models[0].id,
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Google conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Explain this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText(account.catalog.models[0].id);
  await review
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
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.connectionRef).toBe(account.ref);
  expect(request.heldMicrousd).toBe(0);
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled();
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
});

test("OpenRouter Remote sign-in accepts a code and returns an ordinary encrypted API account", async ({
  page,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  // Only the public Remote descriptor is simulated; fixture runtime stays Local.
  await page.route("**/api/v1/health", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    body.connection = { location: "remote", transport: "loopback" };
    await route.fulfill({ response, json: body });
  });
  await page.goto(url + "/#providers");
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
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers.locator('[data-provider="openrouter"] > button').click();
  await providers
    .getByRole("button", { name: "Sign in", exact: true })
    .first()
    .click();
  await providers
    .getByRole("button", { name: "Continue", exact: true })
    .click();
  await expect(providers.getByLabel("Code", { exact: true })).toBeVisible();
  await providers
    .getByLabel("Code", { exact: true })
    .fill("fixture-router-code");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-router-browser-vault");
  await providers.getByRole("button", { name: "Finish", exact: true }).click();
  await expect(
    providers.getByRole("region", { name: "Models", exact: true }),
  ).toContainText("Gemini 3.8 Flash");
  const account = (await privateCLI(["ai-connection"])).connections[0];
  expect(account).toMatchObject({
    provider: "openrouter",
    authMethod: "api-key",
    credentialMode: "test",
    liveCallsEnabled: false,
    locked: false,
  });
  expect(account.planUse).not.toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
});

test("Named local servers share setup models and agent Chat without retargeting locked accounts", async ({
  page,
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
  await page.goto(url + "/#providers");
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
  const editor = page.getByRole("region", { name: "Providers", exact: true });
  const accounts: any[] = [];
  for (const [name, port] of [
    ["Ollama", 11434],
    ["LM Studio", 1234],
    ["vLLM", 8000],
  ] as const) {
    await editor
      .locator('[data-provider="local"] > .provider-brand-button')
      .click();
    await editor.getByRole("button", { name, exact: true }).click();
    await expect(
      editor.getByRole("button", { name, exact: true }),
    ).toHaveAttribute("aria-pressed", "true");
    await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
      `http://127.0.0.1:${port}/v1`,
    );
    await editor
      .getByLabel("Vault passphrase", { exact: true })
      .fill("disposable-local-preset-vault");
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    await expect
      .poll(
        async () => (await privateCLI(["ai-connection"])).connections?.length,
      )
      .toBe(accounts.length + 1);
    const account = (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.name === name,
    );
    accounts.push(account);
    expect(account).toMatchObject({
      provider: "local",
      name,
      endpoint: `http://127.0.0.1:${port}/v1`,
      authMethod: "local",
      locked: false,
      liveCallsEnabled: false,
    });
  }
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  const account = accounts[1],
    model = "google/gemma-4-e2b";
  await editor.locator(`[data-account="${account.ref}"]`).click();
  await editor.getByText("Credentials", { exact: true }).click();
  await editor.getByRole("button", { name: "Lock", exact: true }).click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue("");
  await expect(
    editor.getByRole("button", { name: "Replace", exact: true }),
  ).toBeDisabled();
  await editor
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-local-preset-vault");
  await editor.getByRole("button", { name: "Unlock", exact: true }).click();
  await expect(editor.getByLabel("Endpoint", { exact: true })).toHaveValue(
    "http://127.0.0.1:1234/v1",
  );
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation.getByLabel("Name", { exact: true }).fill("LM Studio analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await creation.getByLabel("Model", { exact: true }).selectOption(model);
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "LM Studio analyst",
  );
  expect(profile).toMatchObject({
    provider: "local",
    connectionRef: account.ref,
    model,
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Named local conversation");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic local note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText(model);
  await expect(review).not.toContainText("$");
  await review
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
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request).toMatchObject({
    model,
    connectionRef: account.ref,
    costBasis: "local-reported-usage",
    spentMicrousd: 0,
    heldMicrousd: 0,
  });
  await page.screenshot({
    path: `.evidence/b03-local-servers/local-${test.info().project.name}.png`,
    fullPage: true,
  });
  expect(external).toEqual([]);
});

test("Qwen API keeps its region and model through Providers, agent and Chat", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
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
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="qwen"] > .provider-brand-button')
    .click();
  await expect(
    providers.getByText("ModelStudio · Singapore", { exact: true }),
  ).toBeVisible();
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers.getByLabel("Name", { exact: true }).fill("Qwen Singapore");
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-qwen-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-qwen-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(1);
  const account = (await privateCLI(["ai-connection"])).connections[0];
  expect(account.catalog.source).toBe(
    "https://dashscope-intl.aliyuncs.com/api/v1/models?page_no=1&page_size=100&providers=qwen",
  );
  expect(
    account.catalog.models.find((m: any) => m.id === "qwen3.8-flash")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await expect(
    providers.getByText("ModelStudio · Singapore", { exact: true }),
  ).toBeVisible();
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Qwen regional analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "qwen3.8-flash",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Qwen regional analyst",
  );
  expect(profile).toMatchObject({
    provider: "qwen",
    connectionRef: account.ref,
    model: "qwen3.8-flash",
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Qwen note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("qwen3.8-flash");
  await review
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
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request).toMatchObject({
    model: "qwen3.8-flash",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    providers.getByText("ModelStudio · Singapore", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: `.evidence/b03-qwen-api/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Kimi regional USD account shares agent Chat and preserves mainland records", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
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
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "moonshot",
    name: "Existing CNY account",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(false);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="moonshot"] > .provider-brand-button')
    .click();
  await providers
    .getByRole("button", { name: "International · USD", exact: true })
    .click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "International · USD",
  );
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("Name", { exact: true })
    .fill("Kimi International");
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "moonshot-intl",
  );
  expect(account.catalog.source).toBe("https://api.moonshot.ai/v1/models");
  expect(
    account.catalog.models.find(
      (m: any) => m.id === "moonshot-intl/kimi-k3.1-pro",
    ).selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "International · USD",
  );
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Kimi international analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "moonshot-intl/kimi-k3.1-pro",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Kimi international analyst",
  );
  expect(profile).toMatchObject({
    provider: "moonshot-intl",
    connectionRef: account.ref,
    model: "moonshot-intl/kimi-k3.1-pro",
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("moonshot-intl/kimi-k3.1-pro");
  await review
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
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request).toMatchObject({
    model: "moonshot-intl/kimi-k3.1-pro",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(providers.locator(".provider-route")).toBeVisible();
  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("moonshot");
  await page.screenshot({
    path: `.evidence/b03-kimi/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Google API sign-in uses project models in agent Chat and preserves its API key account", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
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
  const old = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "google",
    name: "Preserved API account",
    key: "fased-test-google-original-browser",
    passphrase: "disposable-google-original-vault",
  });
  await page.reload();
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="google"] > .provider-brand-button')
    .click();
  await providers.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(providers.getByLabel("API key", { exact: true })).toHaveCount(0);
  await providers
    .getByLabel("Name", { exact: true })
    .fill("Google OAuth project");
  await providers
    .getByLabel("Project", { exact: true })
    .fill("fased-fixture-project");
  await providers
    .getByRole("button", { name: "Continue with Google", exact: true })
    .click();
  const link = providers.getByRole("link", {
    name: "Open sign-in",
    exact: true,
  });
  await expect(link).toBeVisible();
  const callback = await link.getAttribute("href");
  expect(callback).toMatch(/^http:\/\/127\.0\.0\.1:/);
  expect((await page.request.get(callback!)).status()).toBe(200);
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(providers.getByText("Ready", { exact: true })).toBeVisible();
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-google-oauth-vault");
  await providers.getByRole("button", { name: "Finish", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(2);
  const all = (await privateCLI(["ai-connection"])).connections;
  const account = all.find((a: any) => a.authMethod === "oauth");
  expect(account).toMatchObject({
    provider: "google",
    googleProject: "fased-fixture-project",
    planUse: false,
    liveCallsEnabled: false,
  });
  expect(all.find((a: any) => a.ref === old.ref).authMethod).toBe("api-key");
  expect(
    account.catalog.models.find((m: any) => m.id === "gemini-3.8-flash"),
  ).toMatchObject({ method: "oauth", selectable: true });
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await expect(providers.locator(".provider-route")).toHaveText(
    "fased-fixture-project",
  );
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Google OAuth analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "gemini-3.8-flash",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Google OAuth analyst",
  );
  expect(profile).toMatchObject({
    provider: "google",
    connectionRef: account.ref,
    model: "gemini-3.8-flash",
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Google OAuth note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("gemini-3.8-flash");
  await expect(review).not.toContainText("ChatGPT");
  await review
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
  const result = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(result).toMatchObject({
    connectionRef: account.ref,
    model: "gemini-3.8-flash",
    heldMicrousd: 0,
  });
  expect(result.spentMicrousd).toBeGreaterThan(0);
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(providers.locator(".provider-route")).toHaveText(
    "fased-fixture-project",
  );
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/b03-google-oauth/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Kimi Code membership connects models agent and normal Chat without task authority", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
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
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "moonshot",
    name: "Existing CNY account",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(false);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="moonshot"] > .provider-brand-button')
    .click();
  await providers
    .getByRole("button", { name: "Code · Interactive", exact: true })
    .click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "Code · Interactive",
  );
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("Name", { exact: true })
    .fill("Personal Kimi Code");
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "kimi-code",
  );
  expect(account.catalog.source).toBe("https://api.kimi.ai/coding/v1/models");
  expect(
    account.catalog.models.find((m: any) => m.id === "kimi-code/k3.1-pro")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await expect(providers.locator(".provider-route")).toHaveText(
    "Code · Interactive",
  );
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation
    .getByLabel("Name", { exact: true })
    .fill("Personal Kimi analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "kimi-code/k3.1-pro",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Personal Kimi analyst",
  );
  expect(profile).toMatchObject({
    provider: "kimi-code",
    connectionRef: account.ref,
    model: "kimi-code/k3.1-pro",
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("kimi-code/k3.1-pro");
  await expect(review).toContainText("Kimi Code");
  await expect(review).not.toContainText("Maximum cost");
  await review
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
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.spentMicrousd).toBe(0);
  expect(request.costBasis).toBe("kimi-code-reported-usage");
  expect(request).toMatchObject({
    model: "kimi-code/k3.1-pro",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(providers.locator(".provider-route")).toBeVisible();
  await expect(
    providers.getByText("Background access", { exact: true }),
  ).toHaveCount(0);
  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("moonshot");
  await page.screenshot({
    path: `.evidence/b03-kimi-code/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Anthropic workspace account joins models agent Chat and preserves existing keys", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
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
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "anthropic",
    name: "Existing workspace-scoped key",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(true);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="anthropic"] > .provider-brand-button')
    .click();
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("Name", { exact: true })
    .fill("Anthropic workspace");
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers.getByText("Options", { exact: true }).click();
  await providers
    .getByLabel("Workspace (optional)", { exact: true })
    .fill("wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "anthropic" && a.ref !== mainland.ref,
  );
  expect(account.workspace).toBe("wrkspc_01JwQvzr7rXLA5AGx3HKfFUJ");
  expect(account.catalog.source).toBe("https://api.anthropic.com/v1/models");
  expect(
    account.catalog.models.find((m: any) => m.id === "claude-haiku-5-5")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation.getByLabel("Name", { exact: true }).fill("Workspace analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "claude-haiku-5-5",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Workspace analyst",
  );
  expect(profile).toMatchObject({
    provider: "anthropic",
    connectionRef: account.ref,
    model: "claude-haiku-5-5",
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("claude-haiku-5-5");
  await expect(review).toContainText("Anthropic");
  await review
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
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.spentMicrousd).toBeGreaterThan(0);
  expect(request).toMatchObject({
    model: "claude-haiku-5-5",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });
  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${account.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");

  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("anthropic");
  await page.screenshot({
    path: `.evidence/b03-methods/providers-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("Ollama Cloud key joins models agent Chat and task access without an invented tariff", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(60000);
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  const external: string[] = [];
  page.on("request", (r) => {
    if (!ownedFixtureOrigins.has(new URL(r.url()).origin))
      external.push(r.url());
  });
  await page.goto(url + "/#providers");
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
  const mainland = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "moonshot",
    name: "Existing CNY account",
    key: "fased-test-kimi-mainland-preserved",
    passphrase: "disposable-kimi-mainland-vault",
  });
  expect(mainland.catalog.models[0].selectable).toBe(false);
  const providers = page.getByRole("region", {
    name: "Providers",
    exact: true,
  });
  await providers
    .locator('[data-provider="ollama-cloud"] > .provider-brand-button')
    .click();
  await expect(providers.locator(".provider-brand-button")).toHaveCount(10);
  await expect(
    providers.getByRole("button", { name: "Sign in", exact: true }),
  ).toHaveCount(0);
  await providers
    .getByLabel("Name", { exact: true })
    .fill("Ollama Cloud account");
  await providers
    .getByLabel("API key", { exact: true })
    .fill("fased-test-kimi-region-disposable-key");
  await providers
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-kimi-region-vault");
  await providers.getByRole("button", { name: "Save", exact: true }).click();
  await expect
    .poll(async () => (await privateCLI(["ai-connection"])).connections.length)
    .toBe(2);
  const account = (await privateCLI(["ai-connection"])).connections.find(
    (a: any) => a.provider === "ollama-cloud",
  );
  expect(account.catalog.source).toBe("https://ollama.com/v1/models");
  expect(
    account.catalog.models.find((m: any) => m.id === "ollama-cloud/gemma4:31b")
      .selectable,
  ).toBe(true);
  expect((await privateCLI(["agents"])).aiRequests).toHaveLength(0);
  await page.reload();
  // No agent is assigned yet; inspect the saved account rather than assuming
  // that account creation silently changed the workspace's default account.
  await providers.locator(`[data-account="${account.ref}"]`).click();
  await providers.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const creation = page.getByRole("dialog", { name: "New agent", exact: true });
  await creation.getByLabel("Name", { exact: true }).fill("Cloud analyst");
  await creation
    .getByLabel("Account", { exact: true })
    .selectOption(account.ref);
  await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
    "ollama-cloud/gemma4:31b",
  );
  await creation
    .getByRole("button", { name: "Create agent", exact: true })
    .click();
  await expect(creation).toBeHidden();
  const profile = (await privateCLI(["agents"])).profiles.find(
    (p: any) => p.name === "Cloud analyst",
  );
  expect(profile).toMatchObject({
    provider: "ollama-cloud",
    connectionRef: account.ref,
    model: "ollama-cloud/gemma4:31b",
  });
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await openConversationList(page);
  await page
    .getByRole("button", { name: "New conversation", exact: true })
    .click();
  await page
    .getByLabel("Conversation name", { exact: true })
    .fill("Synthetic Kimi note");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Summarize this synthetic note.");
  await page.getByRole("button", { name: "Send", exact: true }).click();
  const review = page.getByRole("dialog", {
    name: "Allow replies",
    exact: true,
  });
  await expect(review).toContainText("ollama-cloud/gemma4:31b");
  await expect(review).toContainText("Ollama Cloud");
  await expect(review).not.toContainText("Maximum cost");
  await review
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
  const request = (
    await privateCLI(["agents", "--profile", profile.id])
  ).aiRequests.at(-1);
  expect(request.spentMicrousd).toBe(0);
  expect(request.costBasis).toBe("ollama-cloud-reported-usage");
  expect(request).toMatchObject({
    model: "ollama-cloud/gemma4:31b",
    connectionRef: account.ref,
    heldMicrousd: 0,
  });

  const relayAccount = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "add",
    provider: "local",
    server: "ollama",
    name: "Owned Ollama server",
    passphrase: "disposable-ollama-server-vault",
  });
  expect(
    relayAccount.catalog.models.find((m: any) => m.id === "gemma4:cloud")
      .selectable,
  ).toBe(true);
  await page.goto(url + "/#providers");
  {
    await providers.getByRole("button", { name: "Back", exact: true }).click();
    await page.getByRole("button", { name: "New agent", exact: true }).click();
    const creation = page.getByRole("dialog", {
      name: "New agent",
      exact: true,
    });
    await creation
      .getByLabel("Name", { exact: true })
      .fill("Ollama server cloud analyst");
    await creation
      .getByLabel("Account", { exact: true })
      .selectOption(relayAccount.ref);
    await creation
      .getByLabel("Model", { exact: true })
      .selectOption("gemma4:cloud");
    await expect(creation.getByLabel("Model", { exact: true })).toHaveValue(
      "gemma4:cloud",
    );
    await creation
      .getByRole("button", { name: "Create agent", exact: true })
      .click();
    await expect(creation).toBeHidden();
    const profile = (await privateCLI(["agents"])).profiles.find(
      (p: any) => p.name === "Ollama server cloud analyst",
    );
    expect(profile).toMatchObject({
      provider: "local",
      connectionRef: relayAccount.ref,
      model: "gemma4:cloud",
    });
    await page
      .getByRole("navigation", { name: "Main navigation", exact: true })
      .getByRole("link", { name: "Chat", exact: true })
      .click();
    await openConversationList(page);
    await page
      .getByRole("button", { name: "New conversation", exact: true })
      .click();
    await page
      .getByLabel("Conversation name", { exact: true })
      .fill("Synthetic Kimi note");
    await page
      .getByRole("button", { name: "Create conversation", exact: true })
      .click();
    await page
      .getByLabel("Message", { exact: true })
      .fill("Summarize this synthetic note.");
    await page.getByRole("button", { name: "Send", exact: true }).click();
    const review = page.getByRole("dialog", {
      name: "Allow replies",
      exact: true,
    });
    await expect(review).toContainText("gemma4:cloud");
    await expect(review).toContainText("Ollama Cloud");
    await expect(review).not.toContainText("Maximum cost");
    await review
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
    const request = (
      await privateCLI(["agents", "--profile", profile.id])
    ).aiRequests.at(-1);
    expect(request.spentMicrousd).toBe(0);
    expect(request.costBasis).toBe("ollama-cloud-reported-usage");
    expect(request).toMatchObject({
      model: "gemma4:cloud",
      connectionRef: relayAccount.ref,
      heldMicrousd: 0,
    });
  }

  expect(external).toEqual([]);
  const health = await (await page.request.get(url + "/api/v1/health")).json();
  for (const key of [
    "aiConnections",
    "financialExecution",
    "walletSigner",
    "updater",
  ])
    expect(health.capabilities[key]).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.goto(url + "/#providers");
  await expect(
    providers.locator(`[data-account="${relayAccount.ref}"]`),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    providers.getByText("Background access", { exact: true }),
  ).toHaveCount(1);
  expect(
    (await privateCLI(["ai-connection"])).connections.find(
      (a: any) => a.ref === mainland.ref,
    ).provider,
  ).toBe("moonshot");
  await page.screenshot({
    path: `.evidence/b03-methods/providers-${isMobile ? "mobile" : "desktop"}.png`,
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
  await page.screenshot({
    path: `.evidence/b04-news-watch/watch-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await stop();
  await start(true);
  await page.goto(url + "/#agents/tasks");
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
  await page
    .getByRole("article", { name: `Run ${v.runs[0].id}`, exact: true })
    .getByRole("button", { name: "Discuss", exact: true })
    .click();
  await expect(page).toHaveURL(/#agents\/chat/);
  const message = page
    .locator(".chat-message")
    .filter({ hasText: "Paper headlines" })
    .last();
  await expect(message).toContainText("New matches: 1");
  await expect(message.locator(".watch-headlines")).toContainText(
    "Controlled paper-news headline",
  );
  await message
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  const memory = page.getByRole("dialog", { name: "Memory", exact: true });
  await memory
    .getByLabel("Fact", { exact: true })
    .fill("Paper feed: repeated headlines do not notify again.");
  await memory
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  const final = await read();
  expect(final.tasks).toEqual(v.tasks);
  expect(final.runs).toEqual(v.runs);
  expect(final.aiRequests).toHaveLength(0);
  expect(final.providerCallsEnabled).toBe(false);
  expect(final.financialExecution).toBe(false);
  const foreign = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(foreign.tasks).toHaveLength(0);
  expect(foreign.memories).toHaveLength(0);
  await page.screenshot({
    path: `.evidence/b04-news-watch/chat-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  expect(external).toEqual([]);
});

test("normal Tasks joins optional News analysis and granted analysis-only results across UI CLI restart and memory", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(120000);
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
  const profileId = "trading-analyst";
  await page.getByLabel("Agent", { exact: true }).selectOption(profileId);
  const read = () => privateCLI(["agents", "--profile", profileId]);
  const command = async (c: object) =>
    privateCLI(["agents", "--request-stdin"], {
      profileId,
      revision: (await read()).revision,
      ...c,
    });
  const selected = await fixtureProviderSelection("openai");
  await command({
    action: "profile-update",
    name: "Trading Analyst",
    provider: selected.provider,
    model: selected.model,
    accountRef: selected.accountRef,
  });
  await command({
    action: "news-source-add",
    name: "Paper feed",
    source: "https://news.fased.invalid/headlines.xml",
    newsMaxAgeSeconds: 600,
  });
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.getByRole("button", { name: "New task", exact: true }).click();
  let editor = page.getByRole("complementary", {
    name: "Create a task",
    exact: true,
  });
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Keep reminder draft");
  await editor
    .getByLabel("Reminder text", { exact: true })
    .fill("Preserve this unfinished reminder");
  // Leaving Tasks must preserve input without retaining a reviewed action.
  await page.evaluate(() => {
    window.location.hash = "agents/chat";
  });
  await expect(editor).toHaveCount(0);
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
  await expect(editor).toBeVisible();
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Keep reminder draft",
  );
  await expect(editor.getByLabel("Reminder text", { exact: true })).toHaveValue(
    "Preserve this unfinished reminder",
  );
  await editor.getByLabel("Type", { exact: true }).selectOption("analysis");
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Keep analysis draft");
  await editor
    .getByLabel("Prompt", { exact: true })
    .fill("Summarize synthetic evidence");
  const agentPicker = page.getByLabel("Agent", { exact: true });
  await agentPicker.selectOption("news-researcher");
  await expect(editor).toHaveCount(0);
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Other agent draft");
  await editor
    .getByLabel("Reminder text", { exact: true })
    .fill("Separate profile input");
  await agentPicker.selectOption(profileId);
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Keep analysis draft",
  );
  await expect(editor.getByLabel("Prompt", { exact: true })).toHaveValue(
    "Summarize synthetic evidence",
  );
  await agentPicker.selectOption("news-researcher");
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Other agent draft",
  );
  await editor
    .getByRole("button", { name: "Cancel task", exact: true })
    .click();
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue("");
  await editor
    .getByRole("button", { name: "Cancel task", exact: true })
    .click();
  await agentPicker.selectOption(profileId);
  await editor.getByLabel("Type", { exact: true }).selectOption("reminder");
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Keep reminder draft",
  );
  await expect(editor.getByLabel("Reminder text", { exact: true })).toHaveValue(
    "Preserve this unfinished reminder",
  );
  await page.getByRole("button", { name: "Watch news", exact: true }).click();
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Optional paper analysis");
  await editor
    .getByLabel("News source", { exact: true })
    .selectOption({ label: "Paper feed" });
  await editor.getByLabel("Contains", { exact: true }).fill("paper");
  await editor.getByRole("checkbox", { name: "Analyze", exact: true }).check();
  await editor
    .getByLabel("Prompt", { exact: true })
    .fill("Summarize the selected synthetic headlines");
  await editor
    .getByLabel("If analysis fails", { exact: true })
    .selectOption("optional");
  const before = await read();
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  let review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Keep news result");
  let v = await read();
  expect(v.tasks).toEqual(before.tasks);
  expect(v.sessions).toEqual(before.sessions);
  // Completed reviews cannot survive a page or agent round trip.
  await page.evaluate(() => {
    window.location.hash = "providers";
  });
  await expect(
    page.getByRole("region", { name: "Providers", exact: true }),
  ).toBeVisible();
  await page.evaluate(() => {
    window.location.hash = "agents/tasks";
  });
  await expect(editor).toBeVisible();
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Optional paper analysis",
  );
  await expect(editor.getByLabel("Contains", { exact: true })).toHaveValue(
    "paper",
  );
  await expect(review).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Create task", exact: true }),
  ).toHaveCount(0);
  await expect(
    editor.getByRole("checkbox", { name: "Analyze", exact: true }),
  ).toBeChecked();
  expect((await read()).tasks).toEqual(before.tasks);
  expect((await read()).aiRequests).toHaveLength(0);
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  await expect(review).toBeVisible();
  await agentPicker.selectOption("news-researcher");
  await expect(review).toHaveCount(0);
  await agentPicker.selectOption(profileId);
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue(
    "Optional paper analysis",
  );
  await expect(review).toHaveCount(0);
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  await expect(review).toBeVisible();
  await review
    .getByRole("button", { name: "Create task", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks.length).toBe(1);
  await page
    .getByRole("button", {
      name: "Run now: Optional paper analysis",
      exact: true,
    })
    .click();
  await expect
    .poll(async () => (await read()).tasks[0].state)
    .toBe("completed");
  v = await read();
  expect(v.aiRequests).toHaveLength(0);
  expect(v.runs[0].analysis.state).toBe("skipped");
  expect(v.runs[0].watch.newIds).toHaveLength(1);
  let result = page.getByRole("article", {
    name: `Run ${v.runs[0].id}`,
    exact: true,
  });
  await expect(result).toContainText("Analysis skipped");
  await expect(result).toContainText("New matches: 1");
  await page.screenshot({
    path: `.evidence/b04-task-analysis/optional-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await result.getByRole("button", { name: "Discuss", exact: true }).click();
  await expect(page).toHaveURL(/#agents\/chat/);
  let message = page
    .locator(".chat-message")
    .filter({ hasText: "Optional paper analysis" })
    .last();
  await expect(message).toContainText("Analysis skipped");
  await message
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  let memory = page.getByRole("dialog", { name: "Memory", exact: true });
  await memory
    .getByLabel("Fact", { exact: true })
    .fill(
      "Paper watch keeps its result when optional analysis is unavailable.",
    );
  await memory
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  await page.goto(url + "/#agents/tasks");
  await page.getByRole("button", { name: "New task", exact: true }).click();
  editor = page.getByRole("complementary", {
    name: "Create a task",
    exact: true,
  });
  await expect(editor.getByLabel("Task name", { exact: true })).toHaveValue("");
  await editor.getByLabel("Type", { exact: true }).selectOption("analysis");
  await editor
    .getByLabel("Task name", { exact: true })
    .fill("Routine analysis");
  await editor
    .getByLabel("Prompt", { exact: true })
    .fill("Summarize the saved synthetic evidence");
  await expect(
    editor.getByLabel("Step after validated AI response", { exact: true }),
  ).not.toBeVisible();
  const beforeAnalysis = await read();
  await editor
    .getByRole("button", { name: "Review task", exact: true })
    .click();
  review = page.getByRole("complementary", {
    name: "Review task",
    exact: true,
  });
  await expect(review).toContainText("Routine analysis");
  v = await read();
  expect(v.tasks).toEqual(beforeAnalysis.tasks);
  expect(v.sessions).toEqual(beforeAnalysis.sessions);
  await review
    .getByRole("button", { name: "Create task", exact: true })
    .click();
  await expect.poll(async () => (await read()).tasks.length).toBe(2);
  const run = page.getByRole("button", {
    name: "Run now: Routine analysis",
    exact: true,
  });
  await expect(run).toBeDisabled();
  const detail = page.locator(".task-detail");
  await detail.getByText("Background access", { exact: true }).click();
  await detail
    .getByRole("checkbox", { name: "Routine analysis", exact: true })
    .check();
  await detail
    .getByRole("button", { name: "Review access", exact: true })
    .click();
  await detail
    .getByLabel("Vault passphrase", { exact: true })
    .fill("disposable-browser-preference-vault");
  await detail
    .getByRole("button", { name: "Allow task access", exact: true })
    .click();
  await expect(run).toBeEnabled();
  await run.click();
  await expect
    .poll(async () => (await read()).tasks[1].state)
    .toBe("completed");
  v = await read();
  expect(v.aiRequests).toHaveLength(1);
  expect(v.aiRequests[0].connectionRef).toBe(selected.accountRef);
  expect(v.aiRequests[0].model).toBe(selected.model);
  expect(v.runs[1].strategy).toBe("analysis-v1");
  expect(v.runs[1].analysis.state).toBe("completed");
  result = page.getByRole("article", {
    name: `Run ${v.runs[1].id}`,
    exact: true,
  });
  await expect(result).toContainText(v.aiRequests[0].answer);
  await result.getByRole("button", { name: "Discuss", exact: true }).click();
  message = page
    .locator(".chat-message")
    .filter({ hasText: "Routine analysis" })
    .last();
  await expect(message).toContainText(v.aiRequests[0].answer);
  await message
    .getByRole("button", { name: "Remember this", exact: true })
    .click();
  memory = page.getByRole("dialog", { name: "Memory", exact: true });
  await memory
    .getByLabel("Fact", { exact: true })
    .fill(
      "Synthetic analysis is a saved result, not a reminder or trading authority.",
    );
  await memory
    .getByRole("button", { name: "Save memory", exact: true })
    .click();
  await expect.poll(async () => (await read()).memories.length).toBe(2);
  const saved = await read();
  expect(saved.tasks).toEqual(v.tasks);
  expect(saved.runs).toEqual(v.runs);
  expect(saved.providerCallsEnabled).toBe(false);
  expect(saved.financialExecution).toBe(false);
  await page.screenshot({
    path: `.evidence/b04-task-analysis/analysis-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  await stop();
  await start(true);
  await page.goto(url + "/#agents/tasks");
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
  const restarted = await read();
  expect(restarted.tasks).toEqual(saved.tasks);
  expect(restarted.runs).toEqual(saved.runs);
  expect(restarted.memories).toEqual(saved.memories);
  expect(restarted.aiRequests).toEqual(saved.aiRequests);
  const foreign = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(foreign.tasks).toHaveLength(0);
  expect(foreign.messages).toHaveLength(0);
  expect(foreign.memories).toHaveLength(0);
  expect(external).toEqual([]);
});

for (const method of ["create", "import"] as const) {
  test(`Wallets network ${method} joins protected owner public reference RPC and restart`, async ({
    page,
    isMobile,
  }) => {
    test.skip(
      !process.env.FASED_WALLET_BROWSER_BINARY ||
        !process.env.FASED_WALLET_OWNER_BINARY,
      "explicit compiled test-only wallet drivers required",
    );
    test.setTimeout(90000);
    page.setDefaultTimeout(10000);
    await stop();
    const fixtureBase = resolve(".evidence/b5-fixtures");
    await mkdir(fixtureBase, { recursive: true, mode: 0o700 });
    const root = await mkdtemp(join(fixtureBase, "f-"));
    await writeFile(
      join(root, "disposable.json"),
      "unfunded-reconstructible-wallet-fixture\n",
      { mode: 0o600 },
    );
    const dataDir = join(root, "core");
    const coreDriver = resolve(process.env.FASED_WALLET_BROWSER_BINARY!);
    const ownerDriver = resolve(process.env.FASED_WALLET_OWNER_BINARY!);
    let fixture: ChildProcess | undefined;
    let fixtureLog = "";
    let endpoint = "";
    const forbidden: string[] = [];
    const bodies: string[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).hostname !== "127.0.0.1")
        forbidden.push(request.url());
      if (request.method() === "PUT" || request.method() === "POST")
        bodies.push(request.postData() ?? "");
    });
    const startFixture = async () => {
      await rm(join(root, "ready.json"), { force: true });
      fixture = spawn(
        coreDriver,
        ["-test.run=^TestWalletBrowserFixtureDriver$", "-test.timeout=3m"],
        {
          cwd: resolve("internal/core"),
          env: {
            ...process.env,
            FASED_WALLET_BROWSER_FIXTURE: "1",
            FASED_WALLET_FIXTURE_ROOT: root,
          },
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      fixture.stdout!.on("data", (b) => {
        fixtureLog += b.toString();
      });
      fixture.stderr!.on("data", (b) => {
        fixtureLog += b.toString();
      });
      await expect
        .poll(async () => {
          if (fixture!.exitCode !== null) throw new Error(fixtureLog);
          try {
            endpoint = JSON.parse(
              await readFile(join(root, "ready.json"), "utf8"),
            ).url;
            return endpoint;
          } catch {
            return "";
          }
        })
        .not.toBe("");
    };
    const stopFixture = async () => {
      if (!fixture || fixture.exitCode !== null) return;
      const exiting = once(fixture, "exit");
      fixture.kill("SIGTERM");
      const result = await Promise.race([
        exiting,
        new Promise<never>((_, reject) =>
          setTimeout(
            () => reject(new Error("wallet fixture failed to stop")),
            8000,
          ),
        ),
      ]);
      expect(result[0]).toBe(0);
      fixture = undefined;
    };
    const cli = async (args: string[], input?: unknown) => {
      const child = spawn(binary, [...args, "--data-dir", dataDir], {
        stdio: ["pipe", "pipe", "pipe"],
      });
      let output = "",
        error = "";
      child.stdout!.on("data", (b) => {
        output += b.toString();
      });
      child.stderr!.on("data", (b) => {
        error += b.toString();
      });
      child.stdin!.end(input === undefined ? "" : JSON.stringify(input));
      const result = await once(child, "exit");
      if (result[0] !== 0) throw new Error(error);
      return JSON.parse(output);
    };
    const owner = async (action: string, ownerDir = "owner") =>
      exec(ownerDriver, ["-test.run=^TestWalletOwnerFixtureDriver$"], {
        cwd: resolve("internal/signer"),
        env: {
          ...process.env,
          FASED_WALLET_OWNER_FIXTURE: "1",
          FASED_WALLET_FIXTURE_ROOT: root,
          FASED_WALLET_OWNER_ACTION: action,
          FASED_WALLET_OWNER_DIR: ownerDir,
        },
      });
    try {
      await startFixture();
      await page.goto(endpoint + "/#wallets/accounts");
      const claim = JSON.parse(
        await readFile(join(dataDir, "claim-code.json"), "utf8"),
      );
      await page.getByLabel("One-use claim code").fill(claim.code);
      await page.getByLabel("Create owner password").fill(fixturePassword);
      await page
        .getByRole("button", { name: "Claim workspace", exact: true })
        .click();
      await expect(
        page.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();
      const login = spawn(binary, [
        "login",
        "--data-dir",
        dataDir,
        "--password-stdin",
      ]);
      login.stdin!.end(fixturePassword + "\n");
      expect((await once(login, "exit"))[0]).toBe(0);
      await page.goto(endpoint + "/#wallets/accounts");
      await expect(
        page.getByRole("heading", { name: "Wallets", exact: true }),
      ).toBeVisible();
      await page
        .getByRole("button", { name: "Add wallet", exact: true })
        .click();
      let dialog = page.getByRole("dialog", {
        name: "Add wallet",
        exact: true,
      });
      await dialog.getByLabel("Wallet name").fill("Watch");
      await dialog
        .getByLabel("Network", { exact: true })
        .selectOption("devnet");
      await dialog.getByLabel("Public address").fill("0".repeat(32));
      await dialog.getByRole("button", { name: "Save", exact: true }).click();
      await expect(dialog.getByRole("alert")).toContainText(
        "valid Solana public address",
      );
      expect((await cli(["accounts"])).record.accounts).toHaveLength(0);
      await dialog.getByLabel("Public address").fill("1".repeat(32));
      await dialog.getByRole("button", { name: "Save", exact: true }).click();
      await expect(dialog).toHaveCount(0);
      const original = (await cli(["accounts"])).record.accounts[0];
      expect(original.version).toBe(2);
      expect(original.mode).toBe("watch");
      await page
        .getByRole("button", { name: "Add wallet", exact: true })
        .click();
      dialog = page.getByRole("dialog", { name: "Add wallet", exact: true });
      await dialog
        .getByRole("button", {
          name: method === "create" ? "Create" : "Import",
          exact: true,
        })
        .click();
      await dialog.getByLabel("Wallet name").fill(`Vault ${method}`);
      await expect(
        dialog.getByLabel("Network", { exact: true }),
      ).toBeDisabled();
      await expect(dialog).toContainText(
        `fased-app custody ${method === "create" ? "setup" : "import"}`,
      );
      expect(await dialog.locator('input[type="password"]').count()).toBe(0);
      await expect(
        dialog.getByRole("button", { name: "Attach", exact: true }),
      ).toBeDisabled();
      expect((await cli(["accounts"])).record.accounts).toHaveLength(1);
      await owner(method);
      await dialog
        .getByRole("button", { name: "Refresh status", exact: true })
        .click();
      await expect(dialog.getByLabel("Wallet name")).toHaveValue(
        `Vault ${method}`,
      );
      await expect(dialog).toContainText("Recovery required");
      await dialog.getByRole("button", { name: "Attach", exact: true }).click();
      await expect(dialog).toHaveCount(0);
      let state = await cli(["accounts"]);
      expect(state.record.accounts[0]).toEqual(original);
      expect(state.record.accounts).toHaveLength(2);
      const managed = state.record.accounts.find((a: any) => a.version === 3);
      expect(managed.signer.version).toBe(2);
      expect(managed.network).toBe("devnet");
      expect(state.fixture.status.locked).toBe(true);
      expect(state.fixture.status.recoveryRequired).toBe(true);
      const row = page.getByRole("article", {
        name: `Account Vault ${method}`,
        exact: true,
      });
      await row.getByRole("button", { name: "RPC", exact: true }).click();
      const rpc = page.getByRole("dialog", {
        name: `RPC · Vault ${method}`,
        exact: true,
      });
      await expect(rpc.getByLabel("RPC URL", { exact: true })).toHaveValue("");
      await rpc
        .getByLabel("RPC URL", { exact: true })
        .fill("https://devnet.helius-rpc.com");
      await rpc.getByRole("button", { name: "Check", exact: true }).click();
      await expect(
        rpc.getByText("Verified (test)", { exact: true }),
      ).toBeVisible();
      await rpc.getByRole("button", { name: "Save", exact: true }).click();
      await expect(
        rpc.getByRole("button", { name: "Remove", exact: true }),
      ).toBeVisible();
      state = await cli(["accounts"]);
      const saved = state.record.accounts.find((a: any) => a.id === managed.id);
      expect(saved.rpc.network).toBe("devnet");
      const check = await cli(["accounts", "--check-rpc-stdin"], {
        accountId: managed.id,
        accountRevision: state.record.revision,
      });
      expect(check.fixtureOnly).toBe(true);
      expect(check.liveRPC).toBe(false);
      await rpc
        .locator("summary")
        .filter({ hasText: /^API key$/ })
        .click();
      const rpcKey = "disposable-provider-input-not-issued-123456";
      await rpc.getByLabel("RPC API key", { exact: true }).fill(rpcKey);
      await rpc
        .getByLabel("Vault password", { exact: true })
        .fill("Disposable wallet RPC password 123");
      await rpc.getByRole("button", { name: "Save key", exact: true }).click();
      await expect(
        rpc.getByRole("button", { name: "Remove key", exact: true }),
      ).toBeVisible();
      expect(
        await readFile(
          join(dataDir, `rpc-credential-${managed.id}.json`),
          "utf8",
        ),
      ).not.toContain(rpcKey);
      await page.screenshot({
        path: `.evidence/b05-wallet-setup/${method}-${isMobile ? "mobile" : "desktop"}.png`,
      });
      const beforeRestart = state.record;
      await stopFixture();
      await startFixture();
      await page.goto(endpoint + "/#wallets/accounts");
      await page
        .getByLabel("Owner password", { exact: true })
        .fill(fixturePassword);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
      await expect(
        page.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();
      await page.goto(endpoint + "/#wallets/accounts");
      await expect(
        page.getByRole("article", {
          name: `Account Vault ${method}`,
          exact: true,
        }),
      ).toBeVisible();
      expect((await cli(["accounts"])).record).toEqual(beforeRestart);
      const credential = await cli(["accounts", "--rpc-credential-stdin"], {
        accountId: managed.id,
        revision: state.record.revision,
        action: "status",
        ref: "",
      });
      expect(credential.saved).toBe(true);
      expect(credential.locked).toBe(true);
      expect(credential.liveRPC).toBe(false);
      await owner("recover", "recovered");
      const recovered = await cli(["accounts"]);
      expect(recovered.accountStates[managed.id]).toBe("stale");
      expect(recovered.fixture.status.recoveryRequired).toBe(true);
      expect(recovered.fixture.status.account.publicKey).toBe(
        managed.publicKey,
      );
      expect(recovered.fixture.status.account.epoch).not.toBe(
        managed.signer.epoch,
      );
      const stale = await cli(["accounts", "--rpc-credential-stdin"], {
        accountId: managed.id,
        revision: state.record.revision,
        action: "status",
        ref: "",
      });
      expect(stale.stale).toBe(true);
      expect(stale.locked).toBe(true);
      await refreshFixtureAccounts(page);
      await expect(
        page
          .getByRole("article", {
            name: `Account Vault ${method}`,
            exact: true,
          })
          .getByText("Reconnect", { exact: true }),
      ).toBeVisible();
      await page
        .getByRole("button", { name: "Add wallet", exact: true })
        .click();
      const recoveryDialog = page.getByRole("dialog", {
        name: "Add wallet",
        exact: true,
      });
      await recoveryDialog
        .getByRole("button", {
          name: method === "create" ? "Create" : "Import",
          exact: true,
        })
        .click();
      await recoveryDialog.getByLabel("Wallet name").fill("Recovered wallet");
      await recoveryDialog
        .getByRole("button", { name: "Attach", exact: true })
        .click();
      await expect(recoveryDialog).toHaveCount(0);
      const rebound = await cli(["accounts"]);
      expect(rebound.record.accounts).toHaveLength(3);
      expect(
        rebound.record.accounts.find((a: any) => a.id === managed.id),
      ).toEqual(saved);
      expect(rebound.fixture.status.recoveryRequired).toBe(true);
      expect(forbidden).toEqual([]);
      expect(bodies.join("\n")).not.toContain("disposable-wallet-owner-unlock");
      expect(bodies.join("\n")).not.toContain("disposable-wallet-owner-backup");
      expect(bodies.join("\n")).not.toContain("42".repeat(32));
    } finally {
      await writeFile(
        `.evidence/b05-wallet-setup/${method}-${isMobile ? "mobile" : "desktop"}-fixture.log`,
        fixtureLog,
      );
      await stopFixture();
      await rm(root, { recursive: true, force: true });
    }
  });
}
