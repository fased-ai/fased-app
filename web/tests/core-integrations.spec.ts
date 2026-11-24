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

test("Settings Diagnostics TypeSafe Jev & External MCP client flows, zero secret leakage, and Dark/Light AA contrast", async ({
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

  const openDiagnostics = async () => {
    if (isMobile) {
      await page
        .getByRole("button", { name: "Preferences", exact: true })
        .click();
    } else {
      await page
        .locator("#shell-rail")
        .getByRole("button", { name: "Settings", exact: true })
        .click();
    }
    const dialog = page.getByRole("dialog", { name: /Settings \// });
    await expect(dialog).toBeVisible();
    await dialog
      .locator(".settings-modal-tabs")
      .getByRole("button", { name: "Diagnostics", exact: true })
      .click();
    return dialog;
  };

  await page.goto(`${url}/`);
  let dialog = await openDiagnostics();

  // 1. TypeSafe Jev offline deterministic fallback triage + masked secret save/clear.
  const jevCard = dialog.getByTestId("jev-card");
  await expect(jevCard).toBeVisible();
  await expect(jevCard).toContainText("Not set");

  await jevCard
    .getByRole("button", { name: "Test triage", exact: true })
    .click();
  await expect(jevCard.getByTestId("jev-triage-result")).toContainText(
    "deterministic-fallback",
  );

  const jevSecret = "jev-e2e-secret-998877";
  await jevCard.getByLabel("Model", { exact: true }).fill("jev-latest");
  await jevCard.getByLabel("API key", { exact: true }).fill(jevSecret);
  await jevCard.getByRole("button", { name: "Save", exact: true }).click();
  await expect(jevCard).toContainText("Saved");
  expect(await page.evaluate(() => document.body.innerText)).not.toContain(
    jevSecret,
  );

  await jevCard.getByRole("button", { name: "Clear key", exact: true }).click();
  await expect(jevCard).toContainText("Not set");

  // 2. External MCP client save -> sync (fixture.invalid) -> inspect tools -> remove.
  const mcpCard = dialog.getByTestId("external-mcp-card");
  await expect(mcpCard).toBeVisible();

  const mcpSecret = "mcp-e2e-token-554433";
  await mcpCard.getByLabel("Server ID", { exact: true }).fill("lab-py");
  await mcpCard.getByLabel("Name", { exact: true }).fill("Python Lab");
  await mcpCard
    .getByLabel("Endpoint", { exact: true })
    .fill("https://lab.mcp.invalid/mcp");
  await mcpCard.getByLabel("Bearer token", { exact: true }).fill(mcpSecret);
  await mcpCard
    .getByRole("button", { name: "Save client", exact: true })
    .click();

  const row = mcpCard.getByTestId("mcp-client-lab-py");
  await expect(row).toBeVisible();
  await expect(row).toContainText("Python Lab");
  await expect(row).toContainText("Token");
  expect(await page.evaluate(() => document.body.innerText)).not.toContain(
    mcpSecret,
  );

  await row.getByRole("button", { name: "Sync", exact: true }).click();
  await expect(row).toContainText("fixture_quote_lookup");

  await row.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(row).toHaveCount(0);

  // 3. Dark + Light WCAG AA audit & zero overflow across themes with Diagnostics open.
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((chosenTheme) => {
      localStorage.setItem(
        "fased.ui-preferences.v1",
        JSON.stringify({ theme: chosenTheme, language: "en", rail: true }),
      );
    }, theme);
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);
    dialog = await openDiagnostics();
    await expect(dialog.getByTestId("jev-card")).toBeVisible();
    await expect(dialog.getByTestId("external-mcp-card")).toBeVisible();

    const report = await auditPageAccessibility(page, { checkContrast: true });
    expect(
      report.violations,
      `[${theme}] Settings Diagnostics a11y violations:\n${JSON.stringify(report.violations, null, 2)}`,
    ).toEqual([]);
    const dialogOverflow = await dialog.evaluate(
      (el) => el.scrollWidth - el.clientWidth,
    );
    expect(
      dialogOverflow,
      `[${theme}] Settings Diagnostics dialog overflow on mobile=${isMobile}`,
    ).toBeLessThanOrEqual(1);
    await page.keyboard.press("Escape");
  }

  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
