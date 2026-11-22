import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test, type Page } from "@playwright/test";
import { revealFixtureAccountEntry } from "../../scripts/fixture-account-journey.mjs";
import { spawn } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { once } from "node:events";
import {
  installCoreFixture,
  captureScreenshot,
  openProviders,
  openNewsSources,
  closeNewsSources,
  exactReplyReview,
  openConversationList,
  revealControl,
  openEvidence,
  openUpdateSettings,
  exec,
  binary,
  fixturePassword,
  ownedFixtureOrigins,
  dir,
  url,
  start,
  stop,
  privateCLI,
} from "./helpers/core-fixture.ts";

installCoreFixture();

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
  await page.goto(url + "/#agents/review");
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
      ["agents/review", null],
    ] as const) {
      await page.evaluate((hash) => {
        location.hash = hash;
      }, route);
      if (expected) await expect(page.locator("main")).toContainText(expected);
      if (route === "settings")
        await expect(page.locator(".settings-primary > section")).toHaveCount(
          2,
        );
      if (route === "agents/review")
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
        route === "agents/review" ||
        route === "agents/tasks"
      )
        await captureScreenshot(page, {
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
  expect(health.capabilities.aiConnections).toBe(true);
  for (const key of ["financialExecution", "walletSigner", "updater"])
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
  await page.goto(url + "/#agents/review");
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
  await expect(
    page.getByRole("button", { name: "Preferences", exact: true }),
  ).toBeVisible();
  await openConversationList(page);
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
  for (const hidden of ["News", "Mining", "Onchain", "Hyperliquid"]) {
    await expect(
      rail.getByRole("link", {
        name: hidden,
        exact: true,
        includeHidden: true,
      }),
    ).toHaveCount(0);
  }
  await rail.getByRole("link", { name: "Memory", exact: true }).focus();
  expect(
    await rail
      .getByRole("link", { name: "Memory", exact: true })
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
  for (const hidden of ["Mining", "Onchain", "Hyperliquid"]) {
    await expect(
      more.getByRole("link", { name: hidden, exact: true }),
    ).toHaveCount(0);
  }
  await expect(
    more.getByRole("link", { name: "News", exact: true }),
  ).toBeVisible();
  await expect(
    more.getByRole("link", { name: "Memory", exact: true }),
  ).toBeVisible();
  if (isMobile) {
    await more.getByRole("link", { name: "Tasks", exact: true }).click();
    await expect(page.locator(".shell-more-toggle")).toHaveCSS(
      "color",
      "rgb(255, 79, 124)",
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
  await more.getByRole("link", { name: "News", exact: true }).click();
  await page
    .getByRole("navigation", { name: "Main navigation", exact: true })
    .getByRole("link", { name: "Chat", exact: true })
    .click();
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
  await expect(message).toHaveValue(
    "Do not translate or discard this unsent draft.",
  );
  await page.getByRole("button", { name: "Preferences", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Appearance", { exact: true }).selectOption("light");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await captureScreenshot(page, {
    path: `.evidence/shell-light-dialog-${test.info().project.name}.png`,
    fullPage: false,
  });
  await page.keyboard.press("Escape");
  await captureScreenshot(page, {
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
  await captureScreenshot(page, {
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
  expect(health.capabilities.aiConnections).toBe(true);
  for (const key of ["financialExecution", "walletSigner", "updater"])
    expect(health.capabilities[key]).toBe(false);
  expect(external).toEqual([]);
});

test("R1 workspace uses a compact branded shell and on-demand chat task memory controls", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/review");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
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
  await openConversationList(page);
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
  await captureScreenshot(page, {
    path: `.evidence/r1-tasks-${test.info().project.name}.png`,
    fullPage: true,
  });
  await page.evaluate(() => {
    window.location.hash = "agents/review";
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
      name: "Run preview",
      exact: true,
    }),
  ).toBeDisabled();
  await revealControl(
    page,
    review.getByLabel("I reviewed this context and these limits", {
      exact: true,
    }),
    "",
  );
  await review
    .getByLabel("I reviewed this context and these limits", {
      exact: true,
    })
    .check();
  await review
    .getByRole("button", { name: "Run preview", exact: true })
    .click();
  await expect(review).toContainText("Development fixture response");
  await captureScreenshot(page, {
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
  await captureScreenshot(page, {
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
  await stop();
  await start(true);
  await page.goto(url + "/#overview");
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
      "rgb(23, 22, 30)",
    );
    await expect(
      layout
        .locator(
          selector === ".dashboard-grid" ? ".dashboard-widget" : ".panel",
        )
        .first(),
    ).toHaveCSS("background-color", "rgb(34, 32, 45)");
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
    await captureScreenshot(page, {
      path: `.evidence/wen-${name}-${isMobile ? "mobile" : "desktop"}.png`,
      fullPage: true,
    });
  };
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".hero-card, .hero-art")).toHaveCount(0);
  await expect(page.locator('[data-widget="wallets"]')).toContainText(
    "No wallets",
  );
  await expect(page.locator('[data-widget="mining"]')).toHaveCount(0);
  await checkLayout("dashboard", ".dashboard-grid");
  await page.locator('.shell-top-nav>a[href="#agents/chat"]').click();
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
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
  await expect(
    page.getByText("Live execution disabled", { exact: true }),
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
    await captureScreenshot(page, {
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
  await page.goto(url + "/#overview");
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
    ).toBe(isMobile || name === "strategies" ? 1 : 2);
    await expect(page.locator("html")).toHaveCSS(
      "background-color",
      "rgb(23, 22, 30)",
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
    await captureScreenshot(page, {
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
  await captureScreenshot(page, {
    path: `.evidence/news-feed-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  const paperView = await privateCLI([
    "agents",
    "--profile",
    "trading-analyst",
  ]);
  const paperSeed = await privateCLI(["agents", "--request-stdin"], {
    action: "bot-create",
    profileId: "trading-analyst",
    revision: paperView.revision,
    name: "V3 paper budget",
    quantity: 1,
    priceCents: 100,
    budgetCents: 500,
  });
  expect(paperSeed.bots[0]).toMatchObject({
    name: "V3 paper budget",
    spentCents: 0,
    budgetCents: 500,
  });
  await page.goto(url + "/#agents/bots");
  await expect(page).toHaveURL(/#agents\/tasks$/);
  await expect(
    page.getByRole("button", { name: "Strategies", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("Paper bot name")).toHaveCount(0);
  await layout("strategies", ".tasks-screen");
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
    await captureScreenshot(page, {
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
    activity.getByRole("heading", { name: "Trading Analyst", exact: true }),
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
  await captureScreenshot(page, {
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
  await page.goto(url + "/#overview");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
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
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
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
  expect(geometry.gridGap).toBe("12px");
  expect(geometry.surface).toBe("rgb(34, 32, 45)");
  expect(geometry.border).toBe("rgb(52, 49, 63)");
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
  await captureScreenshot(page, {
    path: `.evidence/dashboard-home-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});

test("compact agent pages share one provider editor and keep feed settings off the page", async ({
  page,
  isMobile,
}) => {
  page.setDefaultTimeout(10000);
  await stop();
  await start(true);
  await page.goto(url + "/#agents/news");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
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
  await provider.getByRole("button", { name: "API key", exact: true }).click();
  await expect(provider.getByLabel("API key", { exact: true })).toBeVisible();
  await expect(provider.getByLabel("API key", { exact: true })).toHaveValue("");
  await captureScreenshot(page, {
    path: `.evidence/providers-account-first-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await revealControl(
    page,
    provider.getByLabel("Vault passphrase", { exact: true }),
    "",
  );
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
  await provider.getByRole("button", { name: "API key", exact: true }).click();
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
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await expect(page.getByLabel("Task name", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Schedule", { exact: true })).not.toBeVisible();
  await page.getByLabel("Task name", { exact: true }).fill("Draft only");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Keep this task draft");
  await captureScreenshot(page, {
    path: `.evidence/tasks-progressive-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await revealControl(page, page.getByLabel("Schedule", { exact: true }));
  await expect(page.getByLabel("First run in", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel task", exact: true }).click();
  await page.goto(url + "/#agents/memory");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button", { name: "Remember", exact: true }).click();
  await expect(page.getByLabel("Note", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Close dialog", exact: true }).click();
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
  await page.goto(url + "/#overview");
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
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
  await captureScreenshot(page, {
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
  await editor.getByRole("button", { name: "API key", exact: true }).click();
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
  await editor.getByRole("button", { name: "API key", exact: true }).click();
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
    (a: any) => a.ref !== primary.ref,
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
  await page.goto(url + "/#agents/review");
  await expect(
    page.getByRole("button", { name: "Start chat", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByLabel("Message", { exact: true })).toBeVisible();
  expect((await read()).sessions).toHaveLength(0);
  await captureScreenshot(page, {
    path: `.evidence/chat-empty-composer-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: false,
  });
  const input = page.getByLabel("Message", { exact: true });
  const assertCompactComposer = async () => {
    const form = page.locator(".chat-message-form");
    const send = form.getByRole("button", { name: "Send", exact: true });
    const model = form.locator(".reply-model-menu>summary");
    const [formBox, sendBox, modelBox, inputBox] = await Promise.all([
      form.boundingBox(),
      send.boundingBox(),
      model.boundingBox(),
      input.boundingBox(),
    ]);
    expect(formBox && sendBox && modelBox && inputBox).toBeTruthy();
    expect(
      Math.abs(
        sendBox!.y + sendBox!.height / 2 - modelBox!.y - modelBox!.height / 2,
      ),
    ).toBeLessThan(5);
    expect(sendBox!.width).toBeLessThanOrEqual(44);
    expect(formBox!.y + formBox!.height).toBeLessThanOrEqual(
      page.viewportSize()!.height,
    );
    expect(sendBox!.y + sendBox!.height).toBeLessThanOrEqual(
      page.viewportSize()!.height,
    );
    expect(sendBox!.x + sendBox!.width).toBeLessThanOrEqual(
      formBox!.x + formBox!.width,
    );
    expect(sendBox!.y + sendBox!.height).toBeLessThanOrEqual(
      formBox!.y + formBox!.height,
    );
    expect(inputBox!.y + inputBox!.height).toBeLessThanOrEqual(sendBox!.y + 1);
    expect(await send.textContent()).toBe("");
  };
  await assertCompactComposer();
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
  await page.evaluate(() => {
    window.location.hash = "agents/review";
  });
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
  await assertCompactComposer();
  const form = page.locator(".chat-message-form");
  const beforeOptions = await form.boundingBox();
  await form.getByLabel("Options", { exact: true }).click();
  const options = form.locator(
    ".message-options[open] > .message-options-panel",
  );
  await expect(options).toBeVisible();
  const optionsBox = await options.boundingBox();
  expect(optionsBox!.y).toBeGreaterThanOrEqual(0);
  expect(optionsBox!.x).toBeGreaterThanOrEqual(0);
  expect(optionsBox!.x + optionsBox!.width).toBeLessThanOrEqual(
    page.viewportSize()!.width,
  );
  expect((await form.boundingBox())!.y).toBe(beforeOptions!.y);
  await form.getByLabel("Options", { exact: true }).click();
  await expect(options).not.toBeVisible();
  await form.getByLabel("Options", { exact: true }).click();
  await form.getByLabel("Options", { exact: true }).press("Escape");
  await expect(options).not.toBeVisible();
  await expect(form.getByLabel("Options", { exact: true })).toBeFocused();
  await captureScreenshot(page, {
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
  const reminderRunId = (await read()).runs[0].id;
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
    .getByLabel("Note", { exact: true })
    .fill("Prefer daily SOL risk summaries");
  await page.getByRole("button", { name: "Save memory", exact: true }).click();
  await expect.poll(async () => (await read()).memories.length).toBe(1);
  expect(JSON.stringify((await read()).memories[0])).toContain(
    resultMessage.id,
  );
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page
    .getByRole("navigation", { name: "More pages", exact: true })
    .getByRole("link", { name: "Memory", exact: true })
    .click();
  await expect(
    page.getByText("Prefer daily SOL risk summaries", { exact: true }),
  ).toBeVisible();
  const note = page
    .locator(".memory-card")
    .filter({ hasText: "Prefer daily SOL risk summaries" });
  await note.getByRole("button", { name: "Source", exact: true }).click();
  const memorySource = note.getByRole("region", {
    name: "Memory source",
    exact: true,
  });
  await expect(
    memorySource.getByRole("button", { name: "Open Chat", exact: true }),
  ).toBeVisible();
  await expect(
    memorySource.getByText("Inspect the saved SOL risk limits", {
      exact: true,
    }),
  ).toBeVisible();
  await expect(memorySource.locator(".task-inspection > p")).not.toBeVisible();
  await expect(memorySource.locator("details small")).not.toBeVisible();
  await memorySource.getByText("Details", { exact: true }).click();
  await expect(memorySource.locator("details small")).toContainText("SHA-256");
  await memorySource.getByText("Details", { exact: true }).click();
  await captureScreenshot(page, {
    path: `.evidence/connected-memory-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
  await memorySource
    .getByRole("button", { name: "Open Chat", exact: true })
    .click();
  await openEvidence(page);
  const inspection = page.getByRole("region", {
    name: "Local agent evidence inspection",
    exact: true,
  });
  await inspection
    .getByRole("combobox", { name: "Evidence type", exact: true })
    .selectOption("memory");
  const savedNote = (await read()).memories[0];
  await inspection
    .getByRole("combobox", { name: "Saved record", exact: true })
    .selectOption(JSON.stringify([savedNote.id, ""]));
  await inspection
    .getByRole("button", { name: "Inspect evidence", exact: true })
    .click();
  await inspection
    .getByRole("button", { name: "Use in AI review", exact: true })
    .click();
  const attached = page
    .locator(".composer-context")
    .filter({ has: page.getByText("Selected evidence", { exact: true }) });
  await expect(attached).toBeVisible();
  await expect(attached).not.toHaveAttribute("open");
  await assertCompactComposer();
  await attached.locator(":scope > summary").click();
  await expect(attached.locator("fieldset")).toBeVisible();
  await assertCompactComposer();
  await attached.locator(":scope > summary").click();
  await page.locator(".shell-brand").click();
  await expect(page.locator('[data-widget="tasks"]')).toContainText(
    "Daily SOL review",
  );
  await captureScreenshot(page, {
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
    page
      .getByRole("region", { name: "Run results", exact: true })
      .getByRole("article", {
        name: `Run ${reminderRunId}`,
        exact: true,
      }),
  ).toBeVisible();
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
    page
      .getByRole("region", { name: "Run results", exact: true })
      .getByRole("article", {
        name: `Run ${reminderRunId}`,
        exact: true,
      }),
  ).toBeVisible();
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

test("ordinary customer pages exclude test tools and connect Dashboard accounts", async ({
  page,
}) => {
  test.setTimeout(60000);
  const external: string[] = [];
  await page.route("**/*", (route) => {
    if (!ownedFixtureOrigins.has(new URL(route.request().url()).origin)) {
      external.push(route.request().url());
      return route.abort();
    }
    return route.continue();
  });
  const link = (
    await exec(binary, ["setup-link", "--data-dir", dir])
  ).stdout.trim();
  await page.goto(link);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const read = () => privateCLI(["agents"]);
  const initial = await read();
  expect(initial.aiFixtureAvailable).toBeFalsy();
  await page.goto(url + "/#agents/tasks");
  await expect(
    page.getByRole("button", { name: "Templates", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.locator(".schedule-behavior,.scheduled-ai-disclosure"),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "New task", exact: true }).click();
  await expect(
    page.getByRole("complementary", { name: "Create a task", exact: true }),
  ).toBeVisible();
  await expect(page.locator(".scheduled-ai")).toHaveCount(0);
  await page.getByRole("button", { name: "Cancel task", exact: true }).click();
  for (const route of ["terminals/satcoin", "terminals/hyperliquid"]) {
    await page.goto(url + "/#" + route);
    await expect(page.locator(".deferred-workspace")).toBeVisible();
    await expect(
      page.locator(
        ".terminal-card,.terminal-workspace,.wen-paper-journey,.session-rehearsal,.session-automatic",
      ),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: /mine|claim|run now/i }),
    ).toHaveCount(0);
  }
  await page.goto(url + "/#terminals");
  await expect(page).toHaveURL(/#terminals\/jupiter$/);
  await expect(
    page.getByRole("region", { name: "Onchain swaps", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#wallets/overview");
  await expect(page.locator(".account-advanced")).toHaveCount(0);
  await expect(
    page.getByLabel("Disposable public key (64 lowercase hex)"),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Add wallet", exact: true }).click();
  const setup = page.getByRole("dialog", { name: "Add wallet", exact: true });
  await expect(
    setup.getByRole("button", { name: "Watch", exact: true }),
  ).toBeEnabled();
  await expect(
    setup.getByRole("button", { name: "Create", exact: true }),
  ).toBeDisabled();
  await expect(
    setup.getByRole("button", { name: "Import", exact: true }),
  ).toBeDisabled();
  await expect(setup.locator(".wallet-terminal")).toHaveCount(0);
  await expect(setup).not.toContainText("--disposable-fixture");
  await setup
    .getByRole("button", { name: "Close dialog", exact: true })
    .click();
  const record = (await privateCLI(["accounts"])).record;
  const account = {
    version: 1,
    id: "4".repeat(32),
    name: "Saved account",
    mode: "watch",
    network: "disposable-fixture:v1",
    publicKey: "5".repeat(64),
  };
  await privateCLI(["accounts", "--request-stdin"], {
    ...record,
    accounts: [...record.accounts, account],
  });
  await page.goto(url + "/#overview");
  // Add the optional Wallets card through the same saved Dashboard layout.
  await page.getByRole("button", { name: "Add widgets", exact: true }).click();
  const add = page.getByRole("dialog");
  const wallet = add.getByRole("button", { name: "Wallets", exact: true });
  if (await wallet.isEnabled()) await wallet.click();
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-widget="wallets"]')).toContainText(
    "Saved account",
  );
  await page.locator('[data-widget="wallets"] .dashboard-row').click();
  await expect(
    page.getByRole("heading", { name: "Saved account", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#agents/bots");
  await expect(page).toHaveURL(/#agents\/tasks$/);
  await expect(
    page.getByRole("button", { name: "Strategies", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("Paper bot name")).toHaveCount(0);
  for (const [name, route] of [
    ["dashboard", "overview"],
    ["providers", "providers"],
    ["agents", "agents/settings"],
    ["chat", "agents/chat"],
    ["tasks", "agents/tasks"],
    ["memory", "agents/memory"],
    ["news", "agents/news"],
    ["wallets", "wallets/overview"],
    ["onchain", "terminals/jupiter"],
    ["mining", "terminals/satcoin"],
    ["hyperliquid", "terminals/hyperliquid"],
    ["activity", "activity"],
    ["settings", "settings"],
  ]) {
    await page.goto(url + "/#" + route);
    await expect(page.locator("main")).toBeVisible();
    if (route === "agents/tasks")
      await page.getByRole("button", { name: "Tasks", exact: true }).click();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await captureScreenshot(page, {
      path: `.evidence/customer-ui-closure/${test.info().project.name}-${name}.png`,
      fullPage: true,
    });
  }
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByText("Saved account", { exact: true })).toHaveCount(0);
  const after = await read();
  expect(after.tasks).toEqual(initial.tasks);
  expect(after.memories).toEqual(initial.memories);
  expect(after.aiRequests).toEqual(initial.aiRequests);
  expect(external).toEqual([]);
});
