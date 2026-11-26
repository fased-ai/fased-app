import { expect, test } from "@playwright/test";
import { auditPageAccessibility } from "./helpers/a11y-audit.ts";
import {
  installCoreFixture,
  ownedFixtureOrigins,
  start,
  stop,
  url,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("Trade workspace renders 1x1..2x2 Lightweight Charts, DOM & Tape dock, Unified Ticket, persistent Watchlist, Dark/Light AA contrast, and zero external calls", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(12000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  const external: string[] = [];
  page.on("request", (r) => {
    const parsed = new URL(r.url());
    if (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      !ownedFixtureOrigins.has(parsed.origin)
    ) {
      external.push(r.url());
    }
  });
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });

  await page.goto(`${url}/#trade`);

  await expect(
    page.getByRole("heading", { name: "Trade", exact: true, level: 1 }),
  ).toBeVisible();
  await expect(page.getByText("Paper", { exact: true }).first()).toBeVisible();

  const grid = page.getByTestId("trade-chart-grid");
  await expect(grid.locator("article.trade-tile")).toHaveCount(4);

  // Verify Lightweight Charts v5 canvas mounted inside tile-1.
  await expect(
    page.getByTestId("chart-tile-tile-1").locator("canvas").first(),
  ).toBeVisible();

  // Switch grid 2x2 -> 1x2 (2 tiles) -> 1x1 (1 tile).
  await page.getByRole("button", { name: "1×2", exact: true }).click();
  await expect(grid.locator("article.trade-tile")).toHaveCount(2);
  await page.getByRole("button", { name: "1×1", exact: true }).click();
  await expect(grid.locator("article.trade-tile")).toHaveCount(1);

  // Switch chart style to Renko on tile-1 via Style popover.
  const tile1 = page.getByTestId("chart-tile-tile-1");
  await tile1.getByRole("button", { name: "Style", exact: true }).click();
  await tile1.getByRole("button", { name: "Renko", exact: true }).click();
  await expect(tile1.locator(".trade-ohlc-strip")).toContainText("fixture");

  // Place a horizontal line & clear drawings via Drawing tool popover.
  await tile1
    .getByRole("button", { name: "Drawing tool", exact: true })
    .click();
  await tile1.getByRole("button", { name: "H-Line", exact: true }).click();
  const chartHost = tile1.locator(".trade-chart-host");
  await chartHost.scrollIntoViewIfNeeded();
  await chartHost.click({ position: { x: 90, y: 70 } });
  await expect(tile1.locator(".trade-ohlc-strip")).toContainText("1 overlays");
  await tile1
    .getByRole("button", { name: "Clear drawings", exact: true })
    .click();
  await expect(tile1.locator(".trade-ohlc-strip")).not.toContainText(
    "overlays",
  );

  // Select FasedScript overlay on tile-1 via Indicator script popover.
  await tile1
    .getByRole("button", { name: "Indicator script", exact: true })
    .click();
  await tile1
    .locator(".trade-tile-popover button")
    .filter({ hasText: /EMA/i })
    .first()
    .click();
  await expect(
    tile1.getByRole("button", { name: "Indicator script", exact: true }),
  ).toHaveAttribute("data-script-id", "ema-rsi-trend");

  // Bottom dock tabs: DOM & Tape (Footprint -> DOM -> Tape -> Intel).
  const bottomDock = page.getByTestId("trade-bottom-dock");
  await bottomDock.getByRole("tab", { name: "DOM & Tape" }).click();
  const orderflow = bottomDock.getByRole("complementary", {
    name: "Orderflow",
    exact: true,
  });
  await expect(orderflow).toBeVisible();
  await orderflow.getByRole("tab", { name: "DOM", exact: true }).click();
  await expect(orderflow).toContainText("Spread:");
  await orderflow.getByRole("tab", { name: "Tape", exact: true }).click();
  await expect(orderflow.locator(".trade-tape-row").first()).toBeVisible();

  // Unified Execution Ticket (right panel): submit paper Bracket order and verify Open Orders.
  const ticketPane = page.getByTestId("trade-ticket-pane");
  await expect(ticketPane).toBeVisible();
  await ticketPane.getByRole("button", { name: "Bracket" }).click();
  await ticketPane.getByLabel("Entry price", { exact: true }).fill("100");
  await ticketPane.getByLabel("Stop price", { exact: true }).fill("95");
  await ticketPane.getByLabel("Target price", { exact: true }).fill("115");
  await ticketPane.locator('button[type="submit"]').click();

  await bottomDock.getByRole("tab", { name: /Open Orders/ }).click();
  await expect(bottomDock).toContainText("BRACKET");

  // Exercise Kill switch from toolbar.
  await page.getByRole("button", { name: "Kill switch", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Clear kill switch", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Clear kill switch", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Kill switch", exact: true }),
  ).toBeVisible();

  // Persistent Watchlist add -> reload -> click picks symbol into active tile.
  const watchlist = page.getByRole("complementary", {
    name: "Watchlist",
    exact: true,
  });
  await watchlist.getByLabel("Add ticker", { exact: true }).fill("DOGE");
  await watchlist
    .getByRole("button", { name: "Add to watchlist", exact: true })
    .click();
  await expect(watchlist).toContainText("DOGE");

  await page.reload();
  await expect(
    page.getByRole("complementary", { name: "Watchlist", exact: true }),
  ).toContainText("DOGE");
  await page
    .getByRole("complementary", { name: "Watchlist", exact: true })
    .locator("button.trade-watchlist-pick", { hasText: "DOGE" })
    .click();
  await expect(tile1.locator("button.trade-symbol-pill")).toHaveAttribute(
    "data-symbol",
    "DOGE",
  );

  // Dark + Light WCAG AA contrast & zero horizontal overflow.
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((chosenTheme) => {
      localStorage.setItem(
        "fased.ui-preferences.v1",
        JSON.stringify({ theme: chosenTheme, language: "en", rail: true }),
      );
    }, theme);
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    const report = await auditPageAccessibility(page, { checkContrast: true });
    expect(
      report.violations,
      `[${theme}] #trade a11y violations:\n${JSON.stringify(report.violations, null, 2)}`,
    ).toEqual([]);
    const overflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    );
    expect(
      overflow,
      `[${theme}] horizontal overflow on mobile=${isMobile}`,
    ).toBeLessThanOrEqual(0);
  }

  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
