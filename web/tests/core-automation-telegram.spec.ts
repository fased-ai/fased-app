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

test("Event triggers, Telegram controller, Alerts widget, zero secret leakage, and Dark/Light AA contrast", async ({
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

  // 1. Navigate to #agents/tasks -> Event triggers tab, create World Intel & Price triggers, evaluate, toggle pause/kill.
  await page.goto(`${url}/#agents/tasks`);
  await page
    .locator(".task-views")
    .getByRole("button", { name: "Event triggers", exact: true })
    .click();

  const panel = page.getByTestId("event-triggers-panel");
  await expect(panel).toBeVisible();
  await expect(panel.getByTestId("automation-state-badge")).toContainText(
    "Active",
  );

  // Create a World Intel news trigger (matches energy fixture event).
  await panel
    .getByLabel("Trigger name", { exact: true })
    .fill("Energy Shock Monitor");
  await panel.getByLabel("Trigger kind", { exact: true }).selectOption("news");
  await panel.getByLabel("Category", { exact: true }).selectOption("energy");
  await panel.getByLabel("Min urgency", { exact: true }).fill("0.5");
  await panel
    .getByRole("button", { name: "Save trigger", exact: true })
    .click();
  await expect(panel.getByTestId("triggers-list")).toContainText(
    "Energy Shock Monitor",
  );

  // Create a Price trigger (Hyperliquid BTC above 10).
  await panel
    .getByLabel("Trigger name", { exact: true })
    .fill("BTC Floor Guard");
  await panel.getByLabel("Trigger kind", { exact: true }).selectOption("price");
  await panel.getByLabel("Threshold", { exact: true }).fill("10");
  await panel
    .getByRole("button", { name: "Save trigger", exact: true })
    .click();
  await expect(panel.getByTestId("triggers-list")).toContainText(
    "BTC Floor Guard",
  );

  // Evaluate now and verify alerts + unread counter.
  await panel
    .getByRole("button", { name: "Evaluate now", exact: true })
    .click();
  await expect(panel.getByTestId("notifications-list")).toContainText(
    "Energy Shock Monitor",
  );
  await expect(panel.getByTestId("automation-unread-badge")).not.toContainText(
    "0 unread",
  );

  await panel
    .getByRole("button", { name: "Mark all read", exact: true })
    .click();
  await expect(panel.getByTestId("automation-unread-badge")).toContainText(
    "0 unread",
  );

  // Create a FasedScript trigger with safe dry-run Test (must not increment notifications or fire outbound).
  await panel
    .getByLabel("Trigger name", { exact: true })
    .fill("EMA Script Desk Watch");
  await panel
    .getByLabel("Trigger kind", { exact: true })
    .selectOption("fasedscript");
  await panel.getByLabel("Action", { exact: true }).selectOption("desk_cycle");
  await panel.getByLabel("Interval", { exact: true }).selectOption("1h");
  await panel
    .getByRole("button", { name: "Save trigger", exact: true })
    .click();
  const scriptRow = panel.getByTestId("trigger-row-ema-script-desk-watch");
  await expect(scriptRow).toBeVisible();
  await scriptRow.getByRole("button", { name: "Test", exact: true }).click();
  await expect(scriptRow).toContainText("0x · fasedscript");

  // Test Pause / Resume and Kill switch / Clear kill switch controls.
  await panel.getByRole("button", { name: "Pause", exact: true }).click();
  await expect(panel.getByTestId("automation-state-badge")).toContainText(
    "Paused",
  );
  await panel.getByRole("button", { name: "Resume", exact: true }).click();
  await expect(panel.getByTestId("automation-state-badge")).toContainText(
    "Active",
  );

  await panel.getByRole("button", { name: "Kill switch", exact: true }).click();
  await expect(panel.getByTestId("automation-state-badge")).toContainText(
    "Kill switch",
  );
  await panel
    .getByRole("button", { name: "Clear kill switch", exact: true })
    .click();
  await expect(panel.getByTestId("automation-state-badge")).toContainText(
    "Active",
  );

  // 2. Settings -> Notifications: save Telegram token (masked), test ping, command simulator, clear token.
  const openNotificationsTab = async () => {
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
      .getByRole("button", { name: "Notifications", exact: true })
      .click();
    return dialog;
  };

  let dialog = await openNotificationsTab();
  const tgCard = dialog.getByTestId("telegram-card");
  await expect(tgCard).toBeVisible();

  const tgSecret = "123456789:TelegramSecretTokenValue_9988776655";
  await tgCard.getByLabel("Enable Telegram bot", { exact: true }).check();
  await tgCard.getByLabel("Bot token", { exact: true }).fill(tgSecret);
  await tgCard.getByLabel("Allowed chat IDs", { exact: true }).fill("777001");
  await tgCard.getByRole("button", { name: "Save", exact: true }).click();

  await expect(tgCard).toContainText("Enabled");
  await expect(tgCard).toContainText("Saved");
  expect(await page.evaluate(() => document.body.innerText)).not.toContain(
    tgSecret,
  );

  await tgCard.getByRole("button", { name: "Send test", exact: true }).click();
  await expect(tgCard.getByTestId("telegram-deliveries")).toContainText(
    "fixture-sent",
  );

  // Unauthorized chat rejected; authorized chat /status, /kill, /unkill CONFIRM succeed.
  await tgCard.getByLabel("Simulator chat ID", { exact: true }).fill("999999");
  await tgCard.getByLabel("Telegram command", { exact: true }).fill("/status");
  await tgCard
    .getByRole("button", { name: "Run command", exact: true })
    .click();
  await expect(tgCard.getByTestId("telegram-sim-result")).toContainText(
    "Rejected",
  );

  await tgCard.getByLabel("Simulator chat ID", { exact: true }).fill("777001");
  await tgCard.getByLabel("Telegram command", { exact: true }).fill("/status");
  await tgCard
    .getByRole("button", { name: "Run command", exact: true })
    .click();
  await expect(tgCard.getByTestId("telegram-sim-result")).toContainText(
    "Authorized",
  );
  await expect(tgCard.getByTestId("telegram-sim-result")).toContainText(
    "Fased Status",
  );

  await tgCard
    .getByLabel("Telegram command", { exact: true })
    .fill("/kill emergency");
  await tgCard
    .getByRole("button", { name: "Run command", exact: true })
    .click();
  await expect(tgCard.getByTestId("telegram-sim-result")).toContainText(
    "KILL SWITCH ACTIVE",
  );

  await tgCard
    .getByLabel("Telegram command", { exact: true })
    .fill("/unkill CONFIRM");
  await tgCard
    .getByRole("button", { name: "Run command", exact: true })
    .click();
  await expect(tgCard.getByTestId("telegram-sim-result")).toContainText(
    "requires owner step-up in the local UI",
  );

  await tgCard
    .getByRole("button", { name: "Clear token", exact: true })
    .click();
  await expect(tgCard).toContainText("Not set");

  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);

  // 3. Overview modular Alerts widget.
  await page.goto(`${url}/#overview`);
  await page.getByRole("button", { name: "Add widgets", exact: true }).click();
  const widgetDialog = page.getByRole("dialog", { name: "Widgets" });
  await widgetDialog
    .getByRole("button", { name: "Alerts", exact: true })
    .click();
  await widgetDialog.getByRole("button", { name: "Done", exact: true }).click();
  const alertsWidget = page.getByTestId("overview-alerts-widget");
  await expect(alertsWidget).toBeVisible();
  await expect(alertsWidget).toContainText("Kill switch");
  await alertsWidget
    .getByRole("button", { name: "Clear kill switch", exact: true })
    .click();
  await expect(alertsWidget).toContainText("Active");

  // 4. Dark + Light WCAG AA audit & zero overflow across Event triggers & Notifications tabs.
  for (const theme of ["dark", "light"] as const) {
    await page.evaluate((chosenTheme) => {
      localStorage.setItem(
        "fased.ui-preferences.v1",
        JSON.stringify({ theme: chosenTheme, language: "en", rail: true }),
      );
    }, theme);
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", theme);

    await page.goto(`${url}/#agents/tasks`);
    await page
      .locator(".task-views")
      .getByRole("button", { name: "Event triggers", exact: true })
      .click();
    await expect(page.getByTestId("event-triggers-panel")).toBeVisible();

    const pageReport = await auditPageAccessibility(page, {
      checkContrast: true,
    });
    expect(
      pageReport.violations,
      `[${theme}] Event triggers a11y violations:\n${JSON.stringify(pageReport.violations, null, 2)}`,
    ).toEqual([]);
    const pageOverflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    );
    expect(
      pageOverflow,
      `[${theme}] Event triggers overflow on mobile=${isMobile}`,
    ).toBeLessThanOrEqual(1);

    dialog = await openNotificationsTab();
    await expect(dialog.getByTestId("telegram-card")).toBeVisible();
    await expect(dialog.getByTestId("webhook-card")).toBeVisible();
    const dlgReport = await auditPageAccessibility(page, {
      checkContrast: true,
    });
    expect(
      dlgReport.violations,
      `[${theme}] Notifications settings a11y violations:\n${JSON.stringify(dlgReport.violations, null, 2)}`,
    ).toEqual([]);
    const dlgOverflow = await dialog.evaluate(
      (el) => el.scrollWidth - el.clientWidth,
    );
    expect(
      dlgOverflow,
      `[${theme}] Notifications dialog overflow on mobile=${isMobile}`,
    ).toBeLessThanOrEqual(1);
    await page.keyboard.press("Escape");
  }

  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
