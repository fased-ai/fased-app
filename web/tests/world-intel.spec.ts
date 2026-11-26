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

test("World Intel map, prediction arbitrage, chart event pins, Overview widgets, and Dark/Light AA contrast", async ({
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

  // 1. Navigate #intel, verify World Map pins & MENA region filter, inspect event.
  await page.goto(`${url}/#intel`);
  await expect(
    page.getByRole("heading", { name: "World Intel", exact: true, level: 1 }),
  ).toBeVisible();
  await expect(
    page.getByRole("img", { name: "World map", exact: true }),
  ).toBeVisible();

  const regionGroup = page.getByRole("group", {
    name: "World map",
    exact: true,
  });
  await regionGroup.getByRole("button", { name: /^MENA/ }).click();
  await regionGroup.getByRole("button", { name: "1.5×", exact: true }).click();
  await page.getByTestId("map-pin-fix-mena-oil").click();
  await expect(page.getByTestId("intel-inspector")).toContainText("Hormuz");
  await regionGroup.getByRole("button", { name: "1×", exact: true }).click();
  await regionGroup.getByRole("button", { name: /^All/ }).click();

  // 2. Verify right-rail Prediction Markets arbitrage spread renders.
  const predList = page.getByTestId("intel-predictions-list");
  await expect(predList).toContainText("Polymarket 62%");
  await expect(predList).toContainText("Kalshi 48%");

  // 3. Switch News Wire -> Macro & Filings tab and open Data & Feeds settings modal.
  await page.getByRole("tab", { name: /Macro & Filings/ }).click();
  await page.getByRole("tab", { name: /News Wire/ }).click();
  await expect(page.getByTestId("intel-events-list")).toBeVisible();
  await page.getByRole("button", { name: "Data & Feeds", exact: true }).click();
  const settingsDialog = page.getByRole("dialog", { name: "Settings" });
  await expect(settingsDialog).toBeVisible();
  await page.keyboard.press("Escape");

  // 4. Verify Chart Event Pins in #trade and toggle Events button.
  await page.goto(`${url}/#trade`);
  const eventsBtn = page.getByRole("button", { name: "Events", exact: true });
  await expect(eventsBtn).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByTestId("chart-pins-tile-1")).toContainText("pins");
  await eventsBtn.click();
  await expect(page.getByTestId("chart-pins-tile-1")).toHaveCount(0);
  await eventsBtn.click();
  await expect(page.getByTestId("chart-pins-tile-1")).toContainText("pins");

  // 5. Add World Intel & Predictions widgets to #overview and verify live summaries.
  await page.goto(`${url}/#overview`);
  await page.getByRole("button", { name: "Add widgets", exact: true }).click();
  const dlg = page.getByRole("dialog", { name: "Widgets", exact: true });
  await dlg.getByRole("button", { name: "World Intel", exact: true }).click();
  await dlg.getByRole("button", { name: "Predictions", exact: true }).click();
  await dlg.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.locator('[data-widget="intel"]')).toContainText(
    "Federal Reserve",
  );
  await expect(page.locator('[data-widget="predictions"]')).toContainText(
    "Arbitrage:",
  );

  // 6. Dark & Light WCAG AA contrast + zero overflow across #intel and #overview.
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((chosenTheme) => {
      localStorage.setItem(
        "fased.ui-preferences.v1",
        JSON.stringify({ theme: chosenTheme, language: "en", rail: true }),
      );
    }, theme);
    await page.reload();
    for (const hash of ["#intel", "#overview"]) {
      await page.goto(`${url}/${hash}`);
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      const report = await auditPageAccessibility(page, {
        checkContrast: true,
      });
      expect(
        report.violations,
        `[${theme}] ${hash} a11y violations:\n${JSON.stringify(report.violations, null, 2)}`,
      ).toEqual([]);
      const overflow = await page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      );
      expect(
        overflow,
        `[${theme}] ${hash} horizontal overflow on mobile=${isMobile}`,
      ).toBeLessThanOrEqual(0);
      if (hash === "#intel") {
        await expect(page.locator(".intel-card-title").first()).toBeVisible();
        const wrapCheck = await page.evaluate(() => {
          const list = document.querySelector(".intel-list");
          const titles = Array.from(
            document.querySelectorAll(".intel-card-title"),
          ).map((el) => ({
            text: (el.textContent || "").slice(0, 60),
            whiteSpace: getComputedStyle(el).whiteSpace,
            scrollWidth: el.scrollWidth,
            clientWidth: el.clientWidth,
          }));
          return {
            listOverflow: list ? list.scrollWidth - list.clientWidth : 0,
            titles,
          };
        });
        expect(wrapCheck.listOverflow).toBeLessThanOrEqual(1);
        for (const t of wrapCheck.titles) {
          expect(t.whiteSpace, `title wrap failed: ${t.text}`).toBe("normal");
          expect(
            t.scrollWidth,
            `title overflow (${t.text}): ${t.scrollWidth} > ${t.clientWidth}`,
          ).toBeLessThanOrEqual(t.clientWidth + 1);
        }
      }
    }
  }

  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
