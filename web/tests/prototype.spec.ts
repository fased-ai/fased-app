import { expect, test } from "@playwright/test";
test("setup resumes after reload and never creates signing authority", async ({
  page,
  isMobile,
}) => {
  await page.goto("/");
  await page.screenshot({
    path: `.evidence/overview-${test.info().project.name}.png`,
    fullPage: true,
  });
  await expect(
    page.getByText("INTERACTIVE PROTOTYPE", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Set up workspace" }).click();
  await page.getByRole("button", { name: /Private VPS Always-on/ }).click();
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page
    .getByRole("button", { name: /Automation wallet Dedicated/ })
    .click();
  await page.getByRole("button", { name: "Close dialog" }).click();
  await page.reload();
  await page.getByRole("button", { name: "Set up workspace" }).click();
  await expect(
    page.getByRole("heading", { name: "Choose your wallet capability" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: /Automation wallet Dedicated/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByText("No financial authority is created.", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Save prototype preferences" })
    .click();
  await expect(
    page.getByRole("button", { name: "Review setup", exact: true }),
  ).toBeVisible();
  await page.goto("/#wallets/security");
  await expect(
    page.getByText("This prototype holds no keys.", { exact: true }),
  ).toBeVisible();
  if (isMobile)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
});
test("profiles keep separate model preferences and memories", async ({
  page,
}) => {
  await page.goto("/#agents/settings");
  await page.getByLabel("Preferred AI connection").selectOption("openai");
  await page.getByRole("button", { name: /TA Trading Analyst/ }).click();
  await expect(page.getByLabel("Preferred AI connection")).toHaveValue("none");
  await page.getByRole("button", { name: "memory", exact: true }).click();
  await expect(
    page.getByText("Compare strategy versions after fees", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByText("Keep mining allocation reviews separate", { exact: false }),
  ).toHaveCount(0);
  await page
    .getByLabel("Add a draft note")
    .fill("Use a conservative research baseline.");
  await page.getByRole("button", { name: "Save note" }).click();
  await page.reload();
  await expect(
    page.getByText("Use a conservative research baseline.", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", {
      name: "Delete memory: Use a conservative research baseline.",
    })
    .click();
  await expect(
    page.getByText("Use a conservative research baseline.", { exact: true }),
  ).toHaveCount(0);
});
test("terminal mockups cannot execute and make no remote requests", async ({
  page,
}) => {
  const external: string[] = [];
  page.on("request", (request) => {
    if (!new URL(request.url()).hostname.match(/^(127\.0\.0\.1|localhost)$/))
      external.push(request.url());
  });
  await page.goto("/#terminals/asset");
  await expect(
    page.getByText("WEN Asset Mining is a design prototype.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Execution unavailable" }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "WEN Loss Mining", exact: true })
    .click();
  await expect(
    page.getByText("WEN Loss Mining is a design prototype.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Available funds", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Not loaded", { exact: true })).toBeVisible();
  expect(external).toEqual([]);
});
test("chat creates an explicit local review preview without financial authority", async ({
  page,
}) => {
  await page.goto("/#agents/chat");
  await page
    .getByLabel("Message your agent")
    .fill("Draft a bounded allocation review.");
  await page.getByRole("button", { name: "Send prototype message" }).click();
  await expect(
    page.getByText(/This prototype has not contacted an AI provider/),
  ).toBeVisible();
  await page.getByRole("button", { name: "Preview a review card" }).click();
  await expect(page.getByText("Not granted", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Record preview" }).click();
  await expect(page.getByRole("status")).toHaveText(/Action preview recorded/);
  await page.getByRole("button", { name: /Notifications, \d+ unread/ }).click();
  await expect(
    page.getByText("An illustrative read-only review was recorded.", {
      exact: false,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Mark all read" }).click();
  await page.getByRole("button", { name: "Close dialog" }).click();
  await expect(
    page.getByRole("button", { name: "Notifications, 0 unread" }),
  ).toBeVisible();
});
test("mobile navigation, keyboard escape and unknown routes are usable", async ({
  page,
  isMobile,
}) => {
  await page.goto("/#not-a-real-route");
  await expect(
    page.getByRole("heading", {
      name: "A clear view. A considered next move.",
    }),
  ).toBeVisible();
  if (isMobile)
    await page.getByRole("button", { name: "Open navigation" }).click();
  await page.getByRole("link", { name: "Wallets", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Wallets", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Explore wallet setup" }).click();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});
