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

test("Quant Lab IDE, 4-Gate validation, Multi-Seat Paper/Shadow Desk, Overview widget & Dark/Light WCAG AA", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(150000);
  page.setDefaultTimeout(15000);
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
  let allowNextBadRequest = 0;
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console", (m) => {
    if (m.type() === "error") {
      const text = m.text();
      if (
        allowNextBadRequest > 0 &&
        text.includes("status of 400 (Bad Request)")
      ) {
        allowNextBadRequest--;
        return;
      }
      errors.push(text);
    }
  });

  // 1. Navigate #strategies alias -> resolves cleanly to #lab ("Quant Lab") with Paper/Shadow badge.
  await page.goto(`${url}/#strategies`);
  await expect(
    page.getByRole("heading", { name: "Quant Lab", exact: true, level: 1 }),
  ).toBeVisible();
  await expect(page.getByText("Paper / Shadow", { exact: true })).toBeVisible();

  // 2. Reject look-ahead shift(close, -1) on backtest, save custom FasedScript, run backtest + 4-Gate, then delete custom script.
  await page.getByLabel("Script ID", { exact: true }).fill("ui-ema-cross");
  await page.getByLabel("Name", { exact: true }).fill("UI EMA Cross");
  await page
    .getByLabel("Source code", { exact: true })
    .fill('plot("bad", shift(close, -1));');
  allowNextBadRequest++;
  await page.getByRole("button", { name: "Run backtest", exact: true }).click();
  await expect(page.getByRole("alert")).toBeVisible();

  await page
    .getByLabel("Source code", { exact: true })
    .fill(
      'const f = input("fast", 6, 4, 10, 2);\nconst ef = ema(close, f);\nconst es = ema(close, 16);\nplot("EF", ef, "accent", "overlay");\nstrategy({ long: crossover(ef, es), exitLong: crossunder(ef, es), stopLossPct: 2.0 });',
    );
  await page.getByRole("button", { name: "Save script", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "UI EMA Cross", exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Run backtest", exact: true }).click();
  await expect(page.getByTestId("lab-backtest-metrics")).toBeVisible();
  await expect(
    page.getByTestId("lab-backtest-metrics").locator(".lab-equity-line"),
  ).toBeVisible();

  await page.getByRole("button", { name: "Run 4-Gate", exact: true }).click();
  await expect(page.getByTestId("lab-four-gate")).toBeVisible();
  await expect(page.getByTestId("lab-four-gate")).toContainText(
    "Gate 1: Plateau",
  );
  await expect(
    page.getByTestId("lab-four-gate").locator(".lab-histogram").first(),
  ).toBeVisible();

  await page
    .getByRole("button", { name: "Delete UI EMA Cross", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "UI EMA Cross", exact: true }),
  ).toHaveCount(0);

  // 3. Switch to Trading Desk tab: verify 8-seat roster, Shadow cycle, Paper PM approval + Close, Kill switch CONFIRM gate.
  await page.getByTestId("lab-tab-desk").click();
  const roster = page.getByTestId("desk-seat-roster");
  await expect(roster).toContainText("Chief of Staff");
  await expect(roster).toContainText("Skeptic");
  await expect(roster).toContainText("Paper Executor");
  await expect(roster).toContainText("Paper only");

  await page
    .getByRole("button", { name: "Run 8-Seat Cycle", exact: true })
    .click();
  await expect(roster).toContainText("SHADOWED");

  // Switch limits & cycle mode to paper -> cycle -> Approve -> Close position.
  await page.getByLabel("Mode", { exact: true }).selectOption("paper");
  await page.getByRole("button", { name: "Save limits", exact: true }).click();
  await page
    .getByRole("button", { name: "Run 8-Seat Cycle", exact: true })
    .click();

  const ticketsList = page.getByTestId("desk-tickets-list");
  await expect(
    ticketsList.getByRole("button", { name: "Approve", exact: true }),
  ).toBeVisible();
  await ticketsList
    .getByRole("button", { name: "Approve", exact: true })
    .click();

  const positionsList = page.getByTestId("desk-positions-list");
  await expect(
    positionsList.getByRole("button", { name: "Close", exact: true }),
  ).toBeVisible();
  await positionsList
    .getByRole("button", { name: "Close", exact: true })
    .click();
  await expect(positionsList).toContainText("No open positions");

  // Kill switch requires CONFIRM to clear.
  const killBar = page.getByTestId("desk-kill-bar");
  await killBar
    .getByRole("button", { name: "Kill switch", exact: true })
    .click();
  await expect(killBar).toContainText("Kill switch");
  allowNextBadRequest++;
  await killBar
    .getByRole("button", { name: "Clear kill switch", exact: true })
    .click();
  await expect(page.getByRole("alert")).toContainText("CONFIRM");
  await killBar.getByLabel("Confirm", { exact: true }).fill("CONFIRM");
  await killBar
    .getByRole("button", { name: "Clear kill switch", exact: true })
    .click();
  await expect(killBar).toContainText("Active");

  // 4. Switch to Journal & Lessons tab: verify journal entries + custom lesson add/delete.
  await page.getByTestId("lab-tab-journal").click();
  await expect(page.getByTestId("desk-journal-list")).toContainText("BTC");
  await page
    .getByLabel("Rule", { exact: true })
    .fill("Stay flat during FOMC window");
  await page.getByRole("button", { name: "Add lesson", exact: true }).click();
  const lessonsList = page.getByTestId("desk-lessons-list");
  await expect(lessonsList).toContainText("Stay flat during FOMC window");
  await lessonsList
    .getByRole("button", {
      name: "Delete Stay flat during FOMC window",
      exact: true,
    })
    .click();
  await expect(lessonsList).not.toContainText("Stay flat during FOMC window");

  // 5. Verify #desk deep-link opens Trading Desk tab directly, and add Trading Desk widget on #overview cleanly.
  await page.goto(`${url}/#desk`);
  await expect(page.getByTestId("desk-seat-roster")).toBeVisible();

  // Plot on chart jumps straight to #trade with active script selected on tile-1.
  await page.getByTestId("lab-tab-scripts").click();
  await page
    .getByRole("button", { name: "Plot on chart", exact: true })
    .click();
  await expect(page).toHaveURL(/#trade$/);
  await expect(
    page
      .getByTestId("chart-tile-tile-1")
      .getByRole("button", { name: "Indicator script", exact: true }),
  ).toHaveAttribute("data-script-id", "ema-rsi-trend");

  await page.goto(`${url}/#overview`);
  await page.getByRole("button", { name: "Add widgets", exact: true }).click();
  const dlg = page.getByRole("dialog", { name: "Widgets", exact: true });
  await dlg.getByRole("button", { name: "Trading Desk", exact: true }).click();
  await dlg.getByRole("button", { name: "Done", exact: true }).click();
  await expect(page.getByTestId("overview-desk-widget")).toContainText("paper");

  // 6. Dark & Light WCAG AA contrast + zero horizontal overflow across #lab and #overview.
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((chosenTheme) => {
      localStorage.setItem(
        "fased.ui-preferences.v1",
        JSON.stringify({ theme: chosenTheme, language: "en", rail: true }),
      );
    }, theme);
    await page.reload();

    await page.goto(`${url}/#lab`);
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    for (const tabTestId of [
      "lab-tab-scripts",
      "lab-tab-desk",
      "lab-tab-journal",
    ]) {
      await page.getByTestId(tabTestId).click();
      const report = await auditPageAccessibility(page, {
        checkContrast: true,
      });
      expect(
        report.violations,
        `[${theme}] #lab (${tabTestId}) a11y violations:\n${JSON.stringify(report.violations, null, 2)}`,
      ).toEqual([]);
      const overflow = await page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      );
      expect(
        overflow,
        `[${theme}] #lab (${tabTestId}) horizontal overflow on mobile=${isMobile}`,
      ).toBeLessThanOrEqual(1);
      if (tabTestId === "lab-tab-scripts") {
        await expect(page.locator(".lab-row-title").first()).toBeVisible();
        const titles = await page.evaluate(() =>
          Array.from(document.querySelectorAll(".lab-row-title")).map((el) => ({
            text: (el.textContent || "").slice(0, 60),
            whiteSpace: getComputedStyle(el).whiteSpace,
            scrollWidth: el.scrollWidth,
            clientWidth: el.clientWidth,
          })),
        );
        for (const t of titles) {
          expect(t.whiteSpace, `lab title wrap failed: ${t.text}`).toBe(
            "normal",
          );
          expect(
            t.scrollWidth,
            `lab title overflow (${t.text}): ${t.scrollWidth} > ${t.clientWidth}`,
          ).toBeLessThanOrEqual(t.clientWidth + 1);
        }
      }
    }
  }

  expect(external).toEqual([]);
  expect(errors).toEqual([]);
  expect(allowNextBadRequest).toBe(0);
});
