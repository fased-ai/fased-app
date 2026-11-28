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

test("Native Lean V1 Mine Workspace (#mine/*): 100% WEN parity (BTC chart, 80 microbars, Schedule Grid, Claims, Assets, Strategies, Portfolio, Stake)", async ({
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

  // 1. Navigate to #mine and verify 3-column WEN terminal + 0 overflow.
  await page.goto(`${url}/#mine`);
  await expect(
    page.getByRole("heading", { name: "Mine", exact: true, level: 1 }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "initial #mine fits viewport without horizontal scroll",
  ).toBe(true);

  // 2. Verify BTC market chart, 80 microbars, 6 strategy presets, and 24 streak chips.
  await expect(
    page.getByLabel("BTC USD mining chart", { exact: true }),
  ).toBeVisible();
  await expect(page.locator(".mine-bin-group i")).toHaveCount(80);
  const presetsGroup = page.getByRole("group", {
    name: "Strategy presets",
    exact: true,
  });
  await expect(presetsGroup.locator("button")).toHaveCount(6);
  const streakList = page.getByRole("list", {
    name: "24-round streak",
    exact: true,
  });
  await expect(streakList.getByRole("listitem")).toHaveCount(24);

  // Toggle BTC chart timeframe & Technicals style via compact dropdown triggers.
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

  // Verify Target Start Round (Now | Next | +2) controls in Composer.
  const startGroup = page.getByRole("group", {
    name: "Target start round",
    exact: true,
  });
  await startGroup.getByRole("button", { name: "Next", exact: true }).click();
  await startGroup.getByRole("button", { name: "Now", exact: true }).click();

  // 3. Verify Mainnet hard-gate disables Commit CTA.
  const clusterGroup = page.getByRole("group", {
    name: "Mining cluster",
    exact: true,
  });
  await clusterGroup
    .getByRole("button", { name: "Mainnet", exact: true })
    .click();
  await expect(page.getByText("Mainnet Gated")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Commit Round", exact: true }),
  ).toBeDisabled();

  // Switch back to Devnet, test Schedule Grid modal and Semi-Auto session runner.
  await clusterGroup
    .getByRole("button", { name: "Devnet", exact: true })
    .click();
  await presetsGroup
    .getByRole("button", { name: "Tails", exact: true })
    .click();
  await page.getByRole("button", { name: "Edit", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Mining Schedule Grid", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: /Apply Schedule/ }).click();

  const modeGroup = page.getByRole("group", {
    name: "Execution mode",
    exact: true,
  });
  await modeGroup
    .getByRole("button", { name: "Semi-Auto", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Start Session", exact: true })
    .click();

  const sessionsTable = page.getByRole("table", {
    name: "Mining sessions",
    exact: true,
  });
  await expect(sessionsTable).toContainText("tails");
  await expect(sessionsTable).toContainText("semi_auto");
  await sessionsTable
    .getByRole("button", { name: /^Stop session / })
    .first()
    .click();
  await expect(sessionsTable).toContainText("stopped");

  // 4. Check Claims breakdown tab and Swap tab.
  const recordsTabs = page.getByRole("tablist", {
    name: "Mining records and claims",
    exact: true,
  });
  await recordsTabs.getByRole("tab", { name: /^Claims/ }).click();
  await expect(page.getByText("Bootstrap Vesting Schedule")).toBeVisible();

  // 5. Navigate Assets (#mine/assets), Strategies (#mine/strategies + near-miss sim), Portfolio (#mine/portfolio), Stake (#mine/stake).
  const subnav = page.getByRole("navigation", {
    name: "Mining workspace tabs",
    exact: true,
  });
  await subnav.getByRole("button", { name: "Assets", exact: true }).click();
  await expect(page.getByText(/Per-Round Volume/)).toBeVisible();
  await expect(page.getByText("Personal vs. Crowd Benchmark")).toBeVisible();

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
  await expect(page.getByText("BTC Yield / 1k SAT (24h)")).toBeVisible();

  // Back to main Mine view, create & control a GoalMining task from #mine Automate overlay, and run contrast audit.
  await subnav.getByRole("button", { name: "Mine", exact: true }).click();
  await expect(page.locator(".mine-bin-group i")).toHaveCount(80);

  await page.getByRole("button", { name: "Self-Keeper and Manifest" }).click();
  await expect(
    page.getByRole("heading", {
      name: "Personal Self-Keeper & Signed Manifest",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Close dialog" }).click();

  const policyBtn = page.getByRole("button", { name: "Policy", exact: true });
  if (await policyBtn.isEnabled()) {
    await policyBtn.click();
    await expect(
      page.getByRole("heading", { name: "Mining Wallet Policy", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByText(/Auto-stake newly claimed WENSAT/),
    ).toBeVisible();
    await expect(
      page.getByText(/Allow Tier 2 fallback settlement crank/),
    ).toBeVisible();
    await page.getByRole("button", { name: "Close dialog" }).click();
  }

  await page.getByRole("button", { name: "Automate mining task" }).click();
  await expect(
    page.getByRole("heading", { name: "Automate Mining Task", exact: true }),
  ).toBeVisible();
  await page.getByLabel("Task Name").fill("Cert Auto Tails");
  await page.getByRole("button", { name: "Create & Activate" }).click();

  const tasksDrawer = page.getByRole("region", {
    name: "Mining tasks",
    exact: true,
  });
  await expect(tasksDrawer).toContainText("Cert Auto Tails");
  await tasksDrawer
    .getByRole("button", { name: "Run task Cert Auto Tails", exact: true })
    .click();
  await tasksDrawer
    .getByRole("button", { name: "Pause task Cert Auto Tails", exact: true })
    .click();
  await expect(tasksDrawer).toContainText("paused");

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
