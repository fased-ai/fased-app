import { expect, test } from "@playwright/test";
test.beforeEach(async ({}, testInfo) => {
  test.skip(testInfo.project.metadata.host !== "fased", "Fased host workflow");
});

test("Fased fixture research walkthrough is local and cannot execute", async ({
  page,
}) => {
  const remote: string[] = [];
  const writes: string[] = [];
  page.on("request", (request) => {
    if (!["localhost", "127.0.0.1"].includes(new URL(request.url()).hostname))
      remote.push(request.url());
    if (!["GET", "HEAD"].includes(request.method())) writes.push(request.url());
  });
  await page.goto("/#terminals/satcoin");
  await page.locator(".fased-mining-host > .wt").first().getByRole("button", { name: "Research", exact: true }).click();
  const review = page.getByRole("region", {
    name: "Fixture research workflow",
  });
  await expect(review).toContainText("FIXTURE · LOCAL ONLY");
  await expect(review).toContainText("Private history included");
  await review.getByRole("button", { name: "Discuss fixture" }).click();
  await expect(review).toContainText("Scripted example · no AI call");
  await review.getByRole("button", { name: "Inspect inert draft" }).click();
  await expect(
    review.getByRole("heading", { name: "Inert strategy draft" }),
  ).toBeVisible();
  await expect(review.locator("tbody tr")).toHaveCount(5);
  await expect(review).not.toContainText(/lamports|bps/);
  await expect(review).toContainText("SOL");
  await review.getByRole("button", { name: "Compare allocation" }).click();
  await expect(review).toContainText("Wide fixture");
  await expect(review).toContainText("allocation only");
  await review.getByRole("button", { name: "Review fixture session" }).click();
  await expect(
    review.getByRole("button", { name: "Start session unavailable" }),
  ).toBeDisabled();
  await expect(review).toContainText("Not assigned");
  await review.getByRole("button", { name: "Inspect result boundary" }).click();
  await expect(review).toContainText("No actual sessions or receipts");
  await review.getByRole("button", { name: "Close review" }).click();
  await expect(review).toHaveCount(0);
  await expect(
    page.getByRole("region", { name: "WEN paper journey" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(remote).toEqual([]);
  expect(writes).toEqual([]);
});

test("Fased host shares terminal palette across existing workspaces", async ({
  page,
}) => {
  for (const route of [
    "overview",
    "agents/chat",
    "agents/tasks",
    "settings",
    "terminals/satcoin",
  ]) {
    await page.goto(`/#${route}`);
    const tokens = await page.evaluate(() => {
      const style = getComputedStyle(document.documentElement);
      return [
        "--wen-bg",
        "--wen-surface",
        "--wen-raised",
        "--wen-border",
        "--wen-text",
        "--wen-muted",
        "--wen-primary",
      ].map((key) => style.getPropertyValue(key).trim().toUpperCase());
    });
    expect(tokens).toEqual([
      "#15141C",
      "#1E1D29",
      "#292738",
      "#373447",
      "#F3F2F7",
      "#B7B2C8",
      "#FF647C",
    ]);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
  }
});
