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

test("Network workspace Stage 5 CEX/Equities & Stage 6 Hub/Swarm with WEN hold", async ({
  page,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(15000);
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

  // Navigate to #network -> Hub tab default, WEN SAT hold visible.
  await page.goto(`${url}/#network`);
  await expect(
    page.getByRole("heading", { name: "Network", exact: true, level: 1 }),
  ).toBeVisible();
  await expect(page.getByText("WEN SAT Held", { exact: true })).toBeVisible();
  await expect(page.getByText("Momentum RSI Filter")).toBeVisible();

  await page.getByRole("button", { name: "Install package" }).first().click();
  await expect(page.getByText("Installed", { exact: true })).toBeVisible();

  // Switch to Swarm tab, enable opt-in Swarm, switch to Paper, add Tailscale peer & broadcast signal + chat.
  await page.getByRole("tab", { name: "Swarm", exact: true }).click();
  await expect(page.getByText("Opt-in Off", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Enable Swarm" }).click();
  await expect(page.getByText("Enabled", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Broadcast signal" }).click();
  await expect(page.getByText("untrusted=true").first()).toBeVisible();

  await page.getByLabel("Kind", { exact: true }).selectOption("chat");
  await page
    .getByLabel("Thesis", { exact: true })
    .fill("Desk chat relay check");
  await page.getByRole("button", { name: "Broadcast signal" }).click();
  await expect(page.getByText("Desk chat relay check")).toBeVisible();

  await page.getByText("Add peer", { exact: true }).first().click();
  await page.getByLabel("Peer ID", { exact: true }).fill("peer-tailnet-b");
  await page.getByLabel("Alias", { exact: true }).fill("Desk Beta");
  await page
    .getByLabel("Public key", { exact: true })
    .fill("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef");
  await page
    .getByLabel("Endpoint", { exact: true })
    .fill("https://desk-beta.tailnet.ts.net");
  await page.getByRole("button", { name: "Add peer" }).click();
  await expect(
    page.getByText("https://desk-beta.tailnet.ts.net"),
  ).toBeVisible();

  // Switch to Execution tab, verify Stage 5 CEX & Equities + OCC chain & submit paper orders.
  await page.getByRole("tab", { name: "Execution", exact: true }).click();
  await expect(page.getByText("Mainnet Locked", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Withdrawal-disabled", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Paper CEX order" }).click();
  await expect(page.getByText("paper_simulated").first()).toBeVisible();
  await page.getByRole("button", { name: "Paper equities order" }).click();

  await auditPageAccessibility(page, "network-workspace", { theme: "dark" });

  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
