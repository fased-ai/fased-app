import { test, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";
const screenDir = ".evidence/f2-terminal/screenshots";
const open = async (page: any, host: unknown) =>
  page.goto(host === "wen" ? "/#/markets/satcoin" : "/#terminals/satcoin");
async function tab(page: any, name: string) {
  const b = page.getByRole("button", { name, exact: true });
  for (let i = 0; i < (await b.count()); i++)
    if (await b.nth(i).isVisible()) {
      await b.nth(i).click();
      return;
    }
}
test("shared terminal validates allocations and bounded review without execution", async ({
  page,
}, info) => {
  const remote: string[] = [],
    writes: string[] = [];
  page.on("request", (r) => {
    if (!["127.0.0.1", "localhost"].includes(new URL(r.url()).hostname))
      remote.push(r.url());
    if (!["GET", "HEAD"].includes(r.method())) writes.push(r.url());
  });
  await open(page, info.project.metadata.host);
  const app = page.getByTestId("strategy-terminal");
  await expect(app).toBeVisible();
  await expect(app).toContainText("Fixture workspace");
  if (info.project.use.isMobile) await tab(page, "Allocate");
  await app.getByRole("button", { name: "Load fixture", exact: true }).click();
  await expect(app.getByLabel("SOL per round")).toHaveValue("5");
  for (const x of "ABCDE")
    await expect(app.getByLabel(`Range ${x} allocation percent`)).toHaveValue(
      "20",
    );
  await app.getByLabel("SOL per round").fill("0.01");
  await expect(
    app.getByRole("button", { name: "Review fixture draft", exact: true }),
  ).toBeDisabled();
  await app.getByLabel("SOL per round").fill("5");
  await app.getByRole("button", { name: "Trend up", exact: true }).click();
  await expect(app.getByLabel("Range D allocation percent")).toHaveValue("45");
  await app.getByRole("button", { name: "Wide", exact: true }).click();
  await app.getByLabel("Range A allocation percent").fill("20.001");
  await expect(app.getByLabel("Range A allocation percent")).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await expect(
    app.getByRole("button", { name: "Review fixture draft", exact: true }),
  ).toBeDisabled();
  await app.getByLabel("Range A allocation percent").fill("19");
  await expect(
    app.getByRole("button", { name: "Review fixture draft", exact: true }),
  ).toBeDisabled();
  await app.getByRole("button", { name: "Wide", exact: true }).click();
  if (info.project.use.isMobile) await tab(page, "Schedule");
  await app.getByText("Inspect conditional example", { exact: true }).click();
  await expect(app).toContainText("Missing or stale inputs skip the round");
  await app
    .getByRole("combobox", { name: "Horizon", exact: true })
    .selectOption("12");
  await app
    .getByRole("combobox", { name: "Participate", exact: true })
    .selectOption("6");
  await app.getByLabel("Maximum rounds", { exact: true }).fill("12");
  await app.getByLabel("Turnover cap · SOL", { exact: true }).fill("60");
  await app
    .getByRole("button", { name: "Review fixture draft", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "Review fixture draft" });
  await expect(dialog).toContainText("60 SOL");
  await expect(dialog).toContainText("12");
  await expect(
    dialog.getByRole("button", { name: "Execution unavailable", exact: true }),
  ).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  expect(writes).toEqual([]);
  expect(remote).toEqual([]);
});
test("shared chart views and canonical fixture remain separate and responsive", async ({
  page,
}, info) => {
  await open(page, info.project.metadata.host);
  const app = page.getByTestId("strategy-terminal");
  await app
    .getByRole("button", { name: "Conditional result", exact: true })
    .click();
  await expect(app).toContainText("No canonical payout fixture");
  await app.getByRole("button", { name: "Load 5 SOL Wide fixture" }).click();
  await expect(app).toContainText("Outcome C");
  await expect(app).toContainText("0.99");
  await app.getByRole("button", { name: "Outlook", exact: true }).click();
  await expect(app).toContainText("Illustrative model distribution");
  await app.getByRole("button", { name: "Price", exact: true }).click();
  await page.evaluate(() => {
    (document.activeElement as HTMLElement)?.blur();
    window.scrollTo(0, 0);
  });
  mkdirSync(screenDir, { recursive: true });
  await page.screenshot({
    path: `${screenDir}/${info.project.name}-app.png`,
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  const tokens = await app.evaluate((e) => {
    const s = getComputedStyle(e);
    return { background: s.backgroundColor, font: s.fontFamily };
  });
  expect(tokens.background).toBe("rgb(21, 20, 28)");
  if (info.project.use.isMobile) {
    await tab(page, "Allocate");
    await page.screenshot({
      path: `${screenDir}/${info.project.name}-allocate.png`,
      fullPage: true,
    });
  }
});

test("discovery search and favorites lead to the shared terminal", async ({
  page,
}, info) => {
  await open(page, info.project.metadata.host);
  const app = page.getByTestId("strategy-terminal");
  await app.getByRole("button", { name: "Discover", exact: true }).click();
  await app.getByLabel("Search assets").fill("missing");
  await expect(
    app.getByRole("button", { name: "Explore fixture ↗", exact: true }),
  ).toHaveCount(0);
  await app.getByLabel("Search assets").fill("sat");
  await app
    .getByRole("button", { name: "Favorite Satcoin", exact: true })
    .click();
  await app.getByRole("button", { name: "Favorites", exact: true }).click();
  await app
    .getByRole("button", { name: "Explore fixture ↗", exact: true })
    .click();
  await expect(
    app.getByRole("button", { name: "Price", exact: true }),
  ).toBeVisible();
});
