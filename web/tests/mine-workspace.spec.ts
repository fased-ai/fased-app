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

test("Native Lean V1 Mine Workspace (#mine): 80 microbars, 6 presets, 24 streak chips, Mainnet lock & Devnet sessions", async ({
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

  // 1. Navigate to #mine and verify WEN Muted Pro geometry & 0 overflow.
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

  // 2. Verify 80 microbars, 6 strategy presets, and 24 streak chips.
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

  // 3. Verify Mainnet hard-gate disables Commit CTA and shows gate note.
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

  // Switch back to Devnet and select Trend up + Tails presets.
  await clusterGroup
    .getByRole("button", { name: "Devnet", exact: true })
    .click();
  await presetsGroup
    .getByRole("button", { name: "Trend up", exact: true })
    .click();
  await expect(
    presetsGroup.getByRole("button", { name: "Trend up", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await presetsGroup
    .getByRole("button", { name: "Tails", exact: true })
    .click();
  await expect(
    presetsGroup.getByRole("button", { name: "Tails", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");

  // 4. Start and stop a Semi-Auto Devnet session cleanly via /api/v1/mining/sessions.
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

  // 5. Contrast & accessibility audit + zero errors / overflow.
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
