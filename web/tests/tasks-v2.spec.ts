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

test("Universal Task Engine Command Center (#tasks): libraries, single task, Swarm DAG, run inspector & a11y", async ({
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

  // 1. Navigate to #tasks and verify shell layout without overflow.
  await page.goto(`${url}/#tasks`);
  await expect(
    page.getByRole("heading", { name: "Tasks", exact: true, level: 1 }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "initial #tasks fits viewport without horizontal scroll",
  ).toBe(true);

  // 2. Open Libraries drawer, inspect Templates/Personas/Skills, instantiate template.
  await page.getByRole("button", { name: "Libraries", exact: true }).click();
  const drawer = page.getByRole("complementary", {
    name: "Task libraries",
    exact: true,
  });
  await expect(drawer).toBeVisible();
  await drawer.getByRole("tab", { name: /Personas/ }).click();
  await expect(drawer).toContainText("Quant Miner");
  await drawer.getByRole("tab", { name: /Skills/ }).click();
  await expect(drawer).toContainText("BTC 10m Regime Classifier");
  await drawer.getByRole("tab", { name: /Templates/ }).click();
  await drawer
    .getByRole("button", {
      name: "Instantiate: Breaking News & Sentiment Sentinel",
      exact: true,
    })
    .click();
  await expect(drawer).toBeHidden();

  const tasksTable = page.getByRole("table", { name: "Tasks", exact: true });
  await expect(tasksTable).toContainText("Breaking News & Sentiment Sentinel");

  // 3. Create a custom Hybrid Mining task via modal and run immediately.
  await page.getByRole("button", { name: "New Task", exact: true }).click();
  const modal = page.getByRole("dialog", { name: "New Task", exact: true });
  await expect(modal).toBeVisible();
  await modal
    .getByLabel("Task name", { exact: true })
    .fill("Devnet Miner Alpha");
  await modal
    .getByLabel("Objective", { exact: true })
    .fill("Track BTC 10m trend and pause on whipsaw");
  await modal.getByRole("button", { name: "Save Draft", exact: true }).click();
  await expect(modal).toBeHidden();

  await expect(tasksTable).toContainText("Devnet Miner Alpha");
  await tasksTable
    .getByRole("button", { name: "Run now: Devnet Miner Alpha", exact: true })
    .click();
  const inspector = page.getByRole("complementary", {
    name: "Task inspector",
    exact: true,
  });
  await expect(inspector).toContainText("Recent runs (1)");

  // 4. Create a 2-node Swarm DAG and verify topology view renders root + child + condition.
  await page.getByRole("button", { name: "New Swarm", exact: true }).click();
  await expect(modal).toBeVisible();
  await modal
    .getByLabel("Task name", { exact: true })
    .fill("Macro Catalyst Root");
  await modal
    .getByRole("button", { name: "Create Swarm DAG", exact: true })
    .click();
  await expect(modal).toBeHidden();

  const dag = page.getByLabel("Task DAG Topology", { exact: true });
  await expect(dag).toBeVisible();
  await expect(dag).toContainText("Macro Catalyst Root");
  await expect(dag).toContainText("Risk Breaker");
  await expect(dag).toContainText("on_signal");

  // Switch back to List view and filter by Mining goal.
  await page.getByRole("button", { name: "List", exact: true }).click();
  const filters = page.getByRole("group", {
    name: "Goal filters",
    exact: true,
  });
  await filters.getByRole("button", { name: "Mining", exact: true }).click();
  await expect(tasksTable).toContainText("Devnet Miner Alpha");
  await filters.getByRole("button", { name: "All", exact: true }).click();

  // Delete Devnet Miner Alpha cleanly.
  await tasksTable
    .getByRole("button", { name: "Delete: Devnet Miner Alpha", exact: true })
    .click();
  await expect(tasksTable).not.toContainText("Devnet Miner Alpha");

  // 5. Contrast & accessibility audit in Dark/Light modes.
  const report = await auditPageAccessibility(page, { checkContrast: true });
  expect(report.violations, "dark mode a11y violations").toEqual([]);

  expect(external, "zero external network requests").toEqual([]);
  expect(errors, "zero console/page errors").toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    `${isMobile ? "mobile" : "desktop"} #tasks overflow check`,
  ).toBe(true);
});
