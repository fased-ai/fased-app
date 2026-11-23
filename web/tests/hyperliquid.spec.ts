import { expect, test } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { openPasswordSignIn } from "../../scripts/fixture-owner-setup.mjs";
import { auditPageAccessibility } from "./helpers/a11y-audit.ts";

test.use({ colorScheme: "dark" });

test("Controlled Hyperliquid terminal enforces review-before-submit, EIP-712 signing, reconciliation, cancellation, CLI parity, and a11y", async ({
  page,
}) => {
  test.setTimeout(90000);
  const dir = await mkdtemp(join(tmpdir(), "fased-hyperliquid-"));
  const driver = resolve(
    process.env.FASED_WALLET_BROWSER_BINARY ?? ".evidence/bin/wallet-core.test",
  );
  const binary = resolve(
    process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
  );
  let child: ChildProcess | undefined;
  let log = "";
  let url = "";
  let ownerPassword = "";

  const events = () =>
    log.split("\n").flatMap((line) => {
      try {
        return [JSON.parse(line)];
      } catch {
        return [];
      }
    });

  const start = async () => {
    log = "";
    child = spawn(driver, ["-test.run=^TestHyperliquidBrowserServer$"], {
      env: {
        ...process.env,
        FASED_FAST_TEST_KDF: "1",
        FASED_HYPERLIQUID_BROWSER_DIR: dir,
      },
    });
    child.stdout!.on("data", (b) => (log += b));
    child.stderr!.on("data", (b) => (log += b));
    await expect
      .poll(() => events().find((x) => x.event === "listening"), {
        timeout: 15000,
        message: () => `hyperliquid driver log:\n${log}`,
      })
      .toBeTruthy();
    const started = events().find((x) => x.event === "listening");
    url = started.url;
    ownerPassword = started.ownerPassword;
    return started;
  };

  const stop = async () => {
    if (child && child.exitCode === null && child.signalCode === null) {
      const done = once(child, "exit");
      child.kill("SIGTERM");
      await Promise.race([
        done,
        new Promise((_, rej) =>
          setTimeout(() => {
            child?.kill("SIGKILL");
            rej(new Error("timeout waiting for browser driver stop"));
          }, 5000),
        ),
      ]).catch(() => undefined);
    }
  };

  const cli = async (args: string[], input?: unknown) => {
    const p = spawn(binary, [...args, "--data-dir", dir]);
    let out = "",
      err = "";
    p.stdout.on("data", (b) => (out += b));
    p.stderr.on("data", (b) => (err += b));
    p.stdin.end(
      typeof input === "string"
        ? input
        : input === undefined
          ? undefined
          : JSON.stringify(input),
    );
    const [code] = await Promise.race([
      once(p, "exit"),
      new Promise<never>((_, rej) =>
        setTimeout(() => {
          p.kill("SIGKILL");
          rej(new Error("CLI command timed out"));
        }, 10000),
      ),
    ]);
    expect(code, err).toBe(0);
    return out.trim().startsWith("{") ? JSON.parse(out) : out;
  };

  try {
    await start();
    await cli(["login", "--password-stdin"], ownerPassword + "\n");

    await page.goto(url + "/#terminals/hyperliquid");
    await openPasswordSignIn(page);
    await page
      .getByLabel("Owner password", { exact: true })
      .fill(ownerPassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();

    await expect(
      page.getByRole("heading", { name: "Hyperliquid", exact: true, level: 1 }),
    ).toBeVisible();
    await expect(
      page.locator("span.badge", { hasText: /^Execution disabled$/ }),
    ).toBeVisible();
    await expect(
      page.locator("span.badge", { hasText: /^Testnet$/ }),
    ).toBeVisible();

    // Markets selector shows BTC, ETH, SOL; click ETH
    await expect(page.getByRole("button", { name: /^BTC\b/ })).toBeVisible();
    await page.getByRole("button", { name: /^ETH\b/ }).click();
    await expect(page.getByLabel("Limit price", { exact: true })).toHaveValue(
      "3200.0",
    );

    // Fill bounded ETH order -> Review -> Submit
    await page.getByLabel("Limit price", { exact: true }).fill("3200.5");
    await page.getByLabel("Size", { exact: true }).fill("0.5");
    await page.getByLabel("Leverage", { exact: true }).fill("5");
    await page.getByRole("button", { name: "Review", exact: true }).click();

    await expect(
      page.getByRole("button", { name: "Submit", exact: true }),
    ).toBeVisible();
    await expect(
      page.getByLabel("Order review", { exact: true }),
    ).toContainText("$1,600.25");

    await page.getByRole("button", { name: "Submit", exact: true }).click();
    await expect(
      page.locator("span.badge", { hasText: /^Resting$/ }).first(),
    ).toBeVisible();

    // CLI parity & safety flags
    const cliView = await cli(["agents"]);
    expect(cliView.financialExecution).toBe(false);
    expect(cliView.hyperliquid?.activationEnabled).toBe(false);
    expect(cliView.hyperliquid?.liveExecution).toBe(false);
    expect(cliView.hyperliquid?.signerAddress).toMatch(/^0x[0-9a-fA-F]{40}$/);
    expect(cliView.hyperliquid?.orders).toHaveLength(1);
    expect(cliView.hyperliquid?.orders[0].signatureHex).toMatch(
      /^0x[0-9a-f]{130}$/,
    );
    expect(cliView.hyperliquid?.orders[0].state).toBe("resting");

    // Reconcile & Cancel
    await page.getByRole("button", { name: "Reconcile", exact: true }).click();
    await expect(
      page.locator("span.badge", { hasText: /^Resting$/ }).first(),
    ).toBeVisible();

    await page.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(
      page.locator("span.badge", { hasText: /^Cancelled$/ }).first(),
    ).toBeVisible();

    // Accessibility + zero horizontal overflow
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    const a11y = await auditPageAccessibility(page, { checkContrast: true });
    expect(
      a11y.violations,
      `hyperliquid a11y violations:\n${JSON.stringify(a11y.violations, null, 2)}`,
    ).toEqual([]);
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
