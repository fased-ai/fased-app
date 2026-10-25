import { expect, test } from "@playwright/test";

test.use({ colorScheme: "dark" });
test("setup resumes after reload and never creates signing authority", async ({
  page,
  isMobile,
}) => {
  await page.goto("/");
  await page.screenshot({
    path: `.evidence/overview-${test.info().project.name}.png`,
    fullPage: true,
  });
  await expect(page.getByLabel("Instance location")).toHaveText("Preview");
  await page.getByRole("button", { name: "Setup" }).click();
  await page.getByRole("button", { name: /Remote Always-on/ }).click();
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page
    .getByRole("button", { name: /Automation wallet Dedicated/ })
    .click();
  await page.getByRole("button", { name: "Close dialog" }).click();
  await page.reload();
  await page.getByRole("button", { name: "Setup" }).click();
  await expect(
    page.getByRole("heading", { name: "Choose your wallet capability" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: /Automation wallet Dedicated/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByText("No financial authority is created.", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Save prototype preferences" })
    .click();
  await expect(
    page.getByRole("button", { name: "Setup", exact: true }),
  ).toBeVisible();
  await page.goto("/#wallets/security");
  await expect(
    page.getByText("This prototype holds no keys.", { exact: true }),
  ).toBeVisible();
  if (isMobile)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
});
test("profiles keep separate model preferences and memories", async ({
  page,
}) => {
  await page.goto("/#agents/settings");
  await page.getByLabel("Provider", { exact: true }).selectOption("openai");
  await page.getByRole("button", { name: /TA Trading Analyst/ }).click();
  await expect(page.getByLabel("Provider", { exact: true })).toHaveValue(
    "none",
  );
  await page.evaluate(() => {
    window.location.hash = "agents/memory";
  });
  await expect(
    page.getByText("Compare strategy versions after fees", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByText("Keep mining allocation reviews separate", { exact: false }),
  ).toHaveCount(0);
  await page
    .getByLabel("Add a draft note")
    .fill("Use a conservative research baseline.");
  await page.getByRole("button", { name: "Save note" }).click();
  await page.reload();
  await expect(
    page.getByText("Use a conservative research baseline.", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", {
      name: "Delete memory: Use a conservative research baseline.",
    })
    .click();
  await expect(
    page.getByText("Use a conservative research baseline.", { exact: true }),
  ).toHaveCount(0);
});
test("terminal mockups cannot execute and make no remote requests", async ({
  page,
  isMobile,
}) => {
  const external: string[] = [];
  const mutations: string[] = [];
  page.on("request", (request) => {
    if (!new URL(request.url()).hostname.match(/^(127\.0\.0\.1|localhost)$/))
      external.push(request.url());
    if (!["GET", "HEAD"].includes(request.method()))
      mutations.push(request.url());
  });
  await page.goto("/#terminals");
  await expect(page.locator(".terminal-card")).toHaveCount(3);
  await expect(
    page.getByRole("heading", { name: "Mining", exact: true }),
  ).toHaveCount(1);
  await page.getByRole("button", { name: "Mining", exact: true }).click();
  for (const route of ["satcoin", "asset", "loss"]) {
    await page.goto(`/#terminals/${route}`);
    await expect(page).toHaveURL(/#terminals\/satcoin$/);
    await expect(
      page.getByRole("heading", { name: "Mining", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText("Mining is a design prototype.", {
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByText("All values below are illustrative.", { exact: false }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Execution unavailable" }),
    ).toBeDisabled();
    await expect(page.getByText("Not loaded", { exact: true })).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Results & claims", exact: true }),
    ).toBeVisible();
    await expect(page.locator("main")).not.toContainText(
      /Asset Mining|Loss Mining|district|arena|eligible loss|paid winnings/i,
    );
    if (isMobile)
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
  }
  await page.reload();
  await expect(page).toHaveURL(/#terminals\/satcoin$/);
  // In-page hash changes use the same aliases as initial entry, without adding a redirect entry.
  await page.evaluate(() => {
    window.location.hash = "terminals/asset";
  });
  await expect(page).toHaveURL(/#terminals\/satcoin$/);
  await page.getByRole("button", { name: "Hyperliquid", exact: true }).click();
  await expect(page).toHaveURL(/#terminals\/hyperliquid$/);
  await expect(
    page.getByRole("button", { name: "Execution unavailable" }),
  ).toBeDisabled();
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "Mining", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Onchain", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Execution unavailable" }),
  ).toBeDisabled();
  await page.goto("/#overview");
  await page.getByRole("button", { name: "Add widgets", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Widgets", exact: true })
    .getByRole("button", { name: "Mining", exact: true })
    .click();
  await page.getByRole("button", { name: "Done", exact: true }).click();
  const mining = page.locator('[data-widget="mining"]');
  await expect(mining).toContainText("Unavailable");
  await mining.locator("button.dashboard-title").click();
  await expect(page).toHaveURL(/#terminals\/satcoin$/);
  await page.getByRole("heading", { name: "Mining", exact: true }).click();
  await page.screenshot({
    path: `.evidence/wen-satcoin-prototype/${test.info().project.name}.png`,
    fullPage: true,
  });
  expect(external).toEqual([]);
  expect(mutations).toEqual([]);
});
test("chat creates an explicit local review preview without financial authority", async ({
  page,
  isMobile,
}) => {
  await page.goto("/#agents/chat");
  await page
    .getByLabel("Message your agent")
    .fill("Draft a bounded allocation review.");
  await page.getByRole("button", { name: "Send prototype message" }).click();
  await expect(
    page.getByText(/This prototype has not contacted an AI provider/),
  ).toBeVisible();
  await page.getByRole("button", { name: "Preview a review card" }).click();
  await expect(page.getByText("Not granted", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Record preview" }).click();
  await expect(page.getByRole("status")).toHaveText(/Action preview recorded/);
  if (isMobile)
    await page.getByRole("button", { name: "More", exact: true }).click();
  await page.getByRole("button", { name: /Notifications, \d+ unread/ }).click();
  await expect(
    page.getByText("An illustrative read-only review was recorded.", {
      exact: false,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Mark all read" }).click();
  await page.getByRole("button", { name: "Close dialog" }).click();
  if (isMobile)
    await page.getByRole("button", { name: "More", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Notifications, 0 unread" }),
  ).toBeVisible();
});
test("mobile navigation, keyboard escape and unknown routes are usable", async ({
  page,
  isMobile,
}) => {
  await page.goto("/#not-a-real-route");
  await expect(
    page.getByRole("heading", {
      name: "Dashboard",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page
    .getByRole("navigation", { name: "More pages" })
    .getByRole("link", { name: "Wallets", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Wallets", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Explore wallet setup" }).click();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});

test("shared WEN Activity distinguishes browser demo notices", async ({
  page,
  isMobile,
}) => {
  await page.goto("/#agents/bots");
  await page.getByRole("button", { name: /Pause demo|Resume demo/ }).click();
  await page.goto("/#activity");
  const activity = page.getByRole("region", {
    name: "Activity records",
    exact: true,
  });
  await expect(
    activity.getByRole("heading", {
      name: "Recent prototype activity",
      exact: true,
    }),
  ).toBeVisible();
  const event = activity
    .getByRole("listitem")
    .filter({ hasText: /Demo bot paused|Demo bot resumed/ });
  await expect(event).toBeVisible();
  await expect(event.getByText("DEMO", { exact: true })).toBeVisible();
  await expect(activity.getByText("LOCAL ONLY", { exact: true })).toBeVisible();
  await expect(activity).toHaveCSS("background-color", "rgb(30, 30, 42)");
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/wen-v3-activity-prototype-${isMobile ? "mobile" : "desktop"}.png`,
    fullPage: true,
  });
});
