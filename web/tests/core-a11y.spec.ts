import { expect, test } from "@playwright/test";
import { auditPageAccessibility } from "./helpers/a11y-audit.ts";
import {
  installCoreFixture,
  start,
  stop,
  url,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("WCAG 2.1 AA, ARIA landmarks, contrast, and keyboard Escape focus return hold across views and themes", async ({
  page,
}) => {
  test.setTimeout(90000);
  await stop();
  await start(true, "127.0.0.1:0", "local", false);

  const routes = [
    { hash: "#overview", name: "Dashboard" },
    { hash: "#trade", name: "Trade" },
    { hash: "#intel", name: "World Intel" },
    { hash: "#agents/chat", name: "Chat" },
    { hash: "#agents/tasks", name: "Tasks" },
    { hash: "#agents/memory", name: "Memory" },
    { hash: "#agents/news", name: "News" },
    { hash: "#wallets/overview", name: "Wallets" },
  ];

  await page.goto(`${url}/#overview`);
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((chosenTheme) => {
      localStorage.setItem(
        "fased.ui-preferences.v1",
        JSON.stringify({ theme: chosenTheme, language: "en", rail: true }),
      );
    }, theme);
    await page.reload();

    for (const r of routes) {
      await page.goto(`${url}/${r.hash}`);
      await expect(
        page.getByRole("heading", { name: r.name, exact: true, level: 1 }),
      ).toBeVisible();
      await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
      const report = await auditPageAccessibility(page, {
        checkContrast: true,
      });
      expect(
        report.violations,
        `[${theme}] ${r.hash} a11y violations:\n${JSON.stringify(report.violations, null, 2)}`,
      ).toEqual([]);
      expect(report.checkedElements).toBeGreaterThan(3);
      expect(report.contrastChecked).toBeGreaterThan(3);
    }

    // Open Settings modal -> Providers -> Add provider wizard in current theme
    await page.goto(`${url}/#overview`);
    const settingsBtn = page
      .getByRole("button", { name: "Preferences", exact: true })
      .first();
    await expect(settingsBtn).toBeVisible();
    await settingsBtn.click();
    const settingsDialog = page.getByRole("dialog", {
      name: /Settings/i,
    });
    await expect(settingsDialog).toBeVisible();
    let modalReport = await auditPageAccessibility(page, {
      checkContrast: true,
    });
    expect(
      modalReport.violations,
      `[${theme}] Settings General modal violations:\n${JSON.stringify(modalReport.violations, null, 2)}`,
    ).toEqual([]);

    await settingsDialog
      .getByRole("button", { name: "Providers", exact: true })
      .click();
    await expect(
      settingsDialog.getByRole("button", { name: "Add provider", exact: true }),
    ).toBeVisible();
    modalReport = await auditPageAccessibility(page, { checkContrast: true });
    expect(
      modalReport.violations,
      `[${theme}] Settings Providers modal violations:\n${JSON.stringify(modalReport.violations, null, 2)}`,
    ).toEqual([]);

    await settingsDialog
      .getByRole("button", { name: "Add provider", exact: true })
      .click();
    const wizardDialog = page.getByRole("dialog", {
      name: "Add provider",
      exact: true,
    });
    await expect(wizardDialog).toBeVisible();
    const wizardReport = await auditPageAccessibility(page, {
      checkContrast: true,
    });
    expect(
      wizardReport.violations,
      `[${theme}] Add provider wizard violations:\n${JSON.stringify(wizardReport.violations, null, 2)}`,
    ).toEqual([]);

    await page.keyboard.press("Escape");
    await expect(wizardDialog).toHaveCount(0);
    await page.keyboard.press("Escape");
    await expect(settingsDialog).toHaveCount(0);
  }

  // Verify keyboard activation + Escape focus restoration on Settings trigger.
  await page.goto(`${url}/#overview`);
  const trigger = page
    .getByRole("button", { name: "Preferences", exact: true })
    .first();
  await trigger.focus();
  await page.keyboard.press("Enter");
  const settingsDialog = page.getByRole("dialog", { name: /Settings/i });
  await expect(settingsDialog).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(settingsDialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
});
