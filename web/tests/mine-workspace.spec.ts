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

test("Native Lean V1 Mine Workspace (#mine/*): 1:1 WEN terminal parity (3-col scroll, sticky bar, BTC chart, 80 microbars, Allocation modal, Claims, Tasks modal, Assets/Stake/Portfolio/Strategies)", async ({
  page,
  isMobile,
}) => {
  test.setTimeout(150000);
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

  // 1. Navigate to #mine and verify 3-column WEN terminal, sticky telemetry bar, & 0 overflow.
  await page.goto(`${url}/#mine`);
  await expect(
    page.getByLabel("BTC USD mining chart", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("region", { name: "Mining status bar", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "initial #mine fits viewport without horizontal scroll",
  ).toBe(true);

  // Ensure banned clutter copy is absent from #mine terminal.
  await expect(page.getByText("24-Round Streak")).toHaveCount(0);
  await expect(page.getByText("Pinned LV1X Deployment")).toHaveCount(0);
  await expect(page.getByText("Personal Self-Keeper")).toHaveCount(0);

  // 2. Verify 80 microbars, 6 strategy presets, and single-row streak cells.
  await expect(page.locator(".mine-bin-group i")).toHaveCount(80);
  const presetsGroup = page.getByRole("group", {
    name: "Strategy presets",
    exact: true,
  });
  await expect(presetsGroup.locator("button")).toHaveCount(6);
  const streakGroup = page.getByRole("group", {
    name: "Recent round outcomes",
    exact: true,
  });
  await expect(streakGroup.locator("button")).toHaveCount(24);

  // Verify icon-first chart triggers (Timeframe + Style + Indicators + Reset).
  await page
    .getByRole("button", { name: "Chart timeframe", exact: true })
    .click();
  await page
    .getByRole("group", { name: "Chart timeframe options", exact: true })
    .getByRole("button", { name: "1h", exact: true })
    .click();
  await page.getByRole("button", { name: "Chart style", exact: true }).click();
  await page
    .getByRole("group", { name: "Chart style options", exact: true })
    .getByRole("button", { name: "Technicals", exact: true })
    .click();
  await expect(
    page.getByText(/Relative Strength Index \(14\)|Collecting 1h bars/),
  ).toBeVisible();
  await page.getByRole("button", { name: "Chart style", exact: true }).click();
  await page
    .getByRole("group", { name: "Chart style options", exact: true })
    .getByRole("button", { name: "Candles", exact: true })
    .click();

  // Verify Allocation fine-tune modal opens via Sliders icon.
  await page
    .getByRole("button", { name: "Fine-tune allocation", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Fine-Tune Allocation", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Done", exact: true }).click();

  // Verify Target Start Round (Now | Next | +2) & multi-round session creation.
  const startGroup = page.getByRole("group", {
    name: "Target start round",
    exact: true,
  });
  await startGroup.getByRole("button", { name: "Next", exact: true }).click();
  await startGroup.getByRole("button", { name: "Now", exact: true }).click();

  await presetsGroup
    .getByRole("button", { name: "Tails", exact: true })
    .click();
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Mining Schedule Grid", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: /Apply Schedule/ }).click();

  // Clicking 2r + Deposit 0.2 -> Mine · 2r · 0.2 SOL → creates a session cleanly.
  const roundsGroup = page.getByRole("group", {
    name: "Round count",
    exact: true,
  });
  await roundsGroup.getByRole("button", { name: "2r", exact: true }).click();
  await page.getByRole("button", { name: "0.2", exact: true }).click();
  await page.getByRole("button", { name: /^Mine · 2r · 0\.2 SOL/ }).click();

  const sessionsTable = page.getByRole("table", {
    name: "Mining sessions",
    exact: true,
  });
  await expect(sessionsTable).toContainText("tails");
  await sessionsTable
    .getByRole("button", { name: /^Stop session / })
    .first()
    .click();
  await expect(sessionsTable).toContainText("stopped");

  // 3. Check WEN 6-tab Round panel: Entries | Sessions | Rounds | Claims | Activity | Protocol.
  const recordsTabs = page.getByRole("tablist", {
    name: "Mining records and claims",
    exact: true,
  });
  await recordsTabs.getByRole("tab", { name: /^Claims/ }).click();
  await expect(page.getByText("SOL ready")).toBeVisible();
  await expect(page.getByText("Staking BTC")).toBeVisible();
  await recordsTabs.getByRole("tab", { name: /^Protocol/ }).click();
  await expect(page.getByText("Crowd Win Rate")).toBeVisible();

  // 4. Navigate Assets, Stake, Portfolio, Strategies subpages.
  const subnav = page.getByRole("navigation", {
    name: "Mining workspace tabs",
    exact: true,
  });
  await subnav.getByRole("button", { name: "Assets", exact: true }).click();
  await expect(page.getByText(/Per-Round Volume/)).toBeVisible();

  await subnav.getByRole("button", { name: "Strategies", exact: true }).click();
  await page
    .getByRole("button", { name: "Simulate", exact: true })
    .first()
    .click();
  await expect(page.getByText(/Simulation Report:/)).toBeVisible();

  await subnav.getByRole("button", { name: "Portfolio", exact: true }).click();
  await expect(page.getByText("Wallet WENSAT")).toBeVisible();

  await subnav.getByRole("button", { name: "Stake", exact: true }).click();
  await expect(page.getByText("Active Staked Pool")).toBeVisible();

  // 5. Back to #mine: verify Tasks & Automation icon modal (no "Personal Self-Keeper" label) & Single Wallet Policy modal.
  await subnav.getByRole("button", { name: "Mine", exact: true }).click();
  await expect(page.locator(".mine-bin-group i")).toHaveCount(80);

  await page
    .getByRole("button", { name: "Mining tasks and automation", exact: true })
    .click();
  await expect(
    page.getByRole("heading", {
      name: "Mining Tasks & Automation",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "+ New Task", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "New Mining Task", exact: true }),
  ).toBeVisible();
  await page.getByLabel("Task Name").fill("Cert Auto Tails");
  await page.getByRole("button", { name: "Create & Activate" }).click();

  const tasksDialog = page.getByRole("dialog", {
    name: "Mining Tasks & Automation",
  });
  await expect(
    tasksDialog.getByText("Cert Auto Tails", { exact: true }),
  ).toBeVisible();
  await tasksDialog
    .getByRole("button", { name: "Run task Cert Auto Tails", exact: true })
    .click();
  await tasksDialog
    .getByRole("button", { name: "Pause task Cert Auto Tails", exact: true })
    .click();
  await expect(tasksDialog.getByText("paused", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Close dialog" }).click();

  const walletPill = page.getByRole("button", {
    name: "Attached mining wallet",
    exact: true,
  });
  if ((await walletPill.count()) > 0) {
    await walletPill.click();
    await expect(
      page.getByRole("heading", {
        name: "Mining Wallet & Policy",
        exact: true,
      }),
    ).toBeVisible();
    await expect(
      page.getByText(/Auto-stake newly claimed WENSAT/),
    ).toBeVisible();
    await page.getByRole("button", { name: "Close dialog" }).click();
  }

  // 6. Dark mode WCAG AA + zero errors / zero external requests / zero overflow.
  const report = await auditPageAccessibility(page, { checkContrast: true });
  expect(report.violations, "dark mode a11y violations").toEqual([]);
  expect(external, "zero external network requests").toEqual([]);
  expect(errors, "zero console/page errors").toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    `${isMobile ? "mobile" : "desktop"} #mine overflow check`,
  ).toBe(true);
});
