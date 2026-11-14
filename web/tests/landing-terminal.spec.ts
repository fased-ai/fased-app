import { test, expect } from "@playwright/test";
import { mkdirSync } from "node:fs";
test("landing shares terminal design and keeps fixture data explicit", async ({
  page,
}, info) => {
  const host = info.project.metadata.host;
  await page.goto(`http://127.0.0.1:${host === "wen" ? 5188 : 5189}`);
  await expect(
    page.getByRole("region", { name: "Illustrative terminal preview" }),
  ).toBeVisible();
  await expect(
    page.getByText("Illustrative fixture · No execution", { exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  expect(
    await page
      .locator("html")
      .evaluate((e) => getComputedStyle(e).backgroundColor),
  ).toBe("rgb(21, 20, 28)");
  expect(await page.locator("body").innerText()).not.toContain("WENSAT");
  if (host === "wen") {
    await expect(
      page.getByText("Unavailable", { exact: true }).first(),
    ).toBeVisible();
  }
  mkdirSync(".evidence/f2-terminal/screenshots", { recursive: true });
  await page.screenshot({
    path: `.evidence/f2-terminal/screenshots/${info.project.name}-landing.png`,
    fullPage: true,
  });
});
