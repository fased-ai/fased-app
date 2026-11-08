import { expect, test } from "@playwright/test";
import { initialState, reducer, STORAGE_KEY } from "../src/state";

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
test("deferred trading pages contain no imitation controls and make no remote requests", async ({
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
  await expect(page).toHaveURL(/#terminals\/jupiter$/);
  await expect(page.locator(".terminal-card,.terminal-workspace")).toHaveCount(
    0,
  );
  for (const route of ["satcoin", "asset", "loss"]) {
    await page.goto(`/#terminals/${route}`);
    await expect(page).toHaveURL(/#terminals\/satcoin$/);
    await expect(
      page.getByRole("heading", { name: "Mining", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByRole("heading", {
        name: "Mining is not available yet",
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.locator(
        ".terminal-workspace,.demo-chart,.session-rehearsal,.session-automatic",
      ),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Execution unavailable" }),
    ).toHaveCount(0);
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
  await page.getByRole("link", { name: "Hyperliquid", exact: true }).click();
  await expect(page).toHaveURL(/#terminals\/hyperliquid$/);
  await expect(
    page.getByRole("heading", {
      name: "Hyperliquid is not available yet",
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.locator(".terminal-workspace,.demo-chart")).toHaveCount(0);
  await page.goBack();
  await expect(
    page.getByRole("heading", { name: "Mining", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Onchain", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Quote", exact: true }),
  ).toBeDisabled();
  await page.goto("/#overview");
  await page.getByRole("button", { name: "Add widgets", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Widgets", exact: true })
    .getByRole("button", { name: "Mining", exact: true })
    .click();
  await page.getByRole("button", { name: "Done", exact: true }).click();
  const mining = page.locator('[data-widget="mining"]');
  await expect(mining).toContainText("Mining is not available yet");
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
  const notice = {
    id: "controlled-browser-notice",
    title: "Browser preview notice",
    detail: "Stored in this browser. No core action was requested.",
    read: false,
    tone: "info" as const,
  };
  const fixture = reducer(
    { ...initialState(), notices: [] },
    { type: "notice", notice },
  );
  await page.goto("/#activity");
  await page.evaluate(
    ({ key, state }) => localStorage.setItem(key, JSON.stringify(state)),
    { key: STORAGE_KEY, state: fixture },
  );
  await page.reload();
  const mutations: string[] = [],
    external: string[] = [];
  page.on("request", (request) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(request.url()).hostname))
      external.push(request.url());
    if (!["GET", "HEAD"].includes(request.method()))
      mutations.push(request.url());
  });
  const activity = page.getByRole("region", {
    name: "Activity records",
    exact: true,
  });
  await expect(
    activity.getByRole("heading", {
      name: fixture.profiles.find((p) => p.id === fixture.selectedProfile)!
        .name,
      exact: true,
    }),
  ).toBeVisible();
  const event = activity
    .getByRole("listitem")
    .filter({ hasText: notice.title });
  await expect(event).toBeVisible();
  await expect(event.getByText("DEMO", { exact: true })).toBeVisible();
  await expect(event).toContainText(notice.detail);
  await expect(page.getByLabel("Instance location")).toHaveText("Preview");
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toHaveCount(0);
  await expect(activity).toHaveCSS("background-color", "rgb(34, 32, 45)");
  expect(
    await page.evaluate(
      (key) => JSON.parse(localStorage.getItem(key)!).notices,
      STORAGE_KEY,
    ),
  ).toEqual(fixture.notices);
  expect(mutations).toEqual([]);
  expect(external).toEqual([]);
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

for (const capability of [
  { name: "held", canVerify: false, canCheck: false },
  { name: "local verification", canVerify: true, canCheck: false },
  { name: "compiled check", canVerify: true, canCheck: true },
  { name: "older core" },
]) {
  test(`RPC credential capabilities ${capability.name}`, async ({ page }) => {
    const actions: string[] = [];
    await page.route("**/api/v1/**", async (route) => {
      const path = new URL(route.request().url()).pathname;
      if (path.endsWith("accounts/rpc-credential")) {
        const body = route.request().postDataJSON();
        actions.push(body.action);
        await route.fulfill({
          json: {
            ref: "fixture-ref",
            saved: true,
            stale: false,
            locked: true,
            testOnly: true,
            liveRPC: false,
            ...capability,
          },
        });
      } else
        await route.fulfill({
          status: 503,
          json: { error: "fixture unavailable" },
        });
    });
    await page.goto("/");
    await page.evaluate(async () => {
      const entry = await (await fetch("/src/main.tsx")).text();
      const reactURL = entry.match(/from "([^"]*\/react\.js[^"]*)"/)?.[1];
      const domURL = entry.match(
        /from "([^"]*\/react-dom_client\.js[^"]*)"/,
      )?.[1];
      if (!reactURL || !domURL) throw Error("Vite entry imports unavailable");
      const React = (await import(reactURL)).default;
      const { createRoot } = (await import(domURL)).default;
      const { CoreProvider } = await import("/src/core.tsx");
      const { UIProvider } = await import("/src/ui-preferences.tsx");
      const { RPCCredential } = await import("/src/pages/RPCCredential.tsx");
      const host = document.createElement("div");
      host.id = "rpc-capability-harness";
      document.body.append(host);
      createRoot(host).render(
        React.createElement(
          UIProvider,
          null,
          React.createElement(
            CoreProvider,
            null,
            React.createElement(RPCCredential, {
              accountId: "fixture-account",
              revision: 1,
              configured: true,
            }),
          ),
        ),
      );
    });
    const form = page.locator("#rpc-capability-harness");
    await form
      .locator("summary")
      .filter({ hasText: /^API key$/ })
      .click();
    await expect.poll(() => actions.includes("status")).toBe(true);
    await expect(form.getByText("Saved", { exact: true })).toBeVisible();
    await form
      .getByLabel("Vault password", { exact: true })
      .fill("disposable vault password");
    await expect(
      form.getByRole("button", { name: "Verify key", exact: true }),
    ).toHaveCount(capability.canVerify ? 1 : 0);
    await expect(
      form.getByRole("button", { name: "Check connection", exact: true }),
    ).toHaveCount(capability.canCheck ? 1 : 0);
    if (capability.canVerify) {
      await form
        .getByRole("button", { name: "Verify key", exact: true })
        .click();
      await expect(form.getByRole("status")).toHaveText("Key verified");
    }
    expect(actions.filter((action) => action !== "status")).toEqual(
      capability.canVerify ? ["verify"] : [],
    );
  });
}
