import { openPasswordSignIn } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

test.use({ colorScheme: "dark" });

test("Controlled WEN mining shares reviewed authority, PM1 receipt, uncertain reconciliation, and restart without duplicate execution across UI and CLI", async ({
  page,
}) => {
  test.setTimeout(90000);
  const dir = await mkdtemp(join(tmpdir(), "fased-wen-mining-"));
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
    child = spawn(driver, ["-test.run=^TestWENMiningBrowserServer$"], {
      env: { ...process.env, FASED_WEN_MINING_BROWSER_DIR: dir },
    });
    child.stdout!.on("data", (b) => (log += b));
    child.stderr!.on("data", (b) => (log += b));
    await expect
      .poll(() => events().find((x) => x.event === "listening"), {
        timeout: 15000,
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
      await done;
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
    const [code] = await once(p, "exit");
    expect(code, err).toBe(0);
    return out.trim().startsWith("{") ? JSON.parse(out) : out;
  };

  try {
    await start();
    await cli(["login", "--password-stdin"], ownerPassword + "\n");

    await page.goto(url + "/#terminals/satcoin");
    await openPasswordSignIn(page);
    await page
      .getByLabel("Owner password", { exact: true })
      .fill(ownerPassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();

    // 1. Confirmed path: Review -> Submit -> Confirmed PM1 receipt shared across UI & CLI
    await expect(
      page.getByText("Mining activation disabled", { exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Review", exact: true }).click();
    await expect(
      page.getByRole("button", { name: "Submit", exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Submit", exact: true }).click();

    await expect
      .poll(
        async () => {
          const v = await cli(["agents", "--profile", "wen-operator"]);
          return v.wenMining?.runs?.[0]?.wenMining?.state;
        },
        { timeout: 15000 },
      )
      .toBe("confirmed");
    await expect(
      page.locator("span.badge", { hasText: /^Confirmed$/ }).first(),
    ).toBeVisible();

    // Verify shared visibility on Tasks -> Results
    await page.goto(url + "/#agents/tasks");
    await expect(
      page.getByText("WEN Devnet Range Mining").first(),
    ).toBeVisible();

    // 2. Uncertain path + Restart without duplicate execution + Explicit Reconcile
    await writeFile(join(dir, "venue-mode"), "uncertain");
    await page.goto(url + "/#terminals/satcoin");
    await page.getByLabel("Amount (SOL)", { exact: true }).fill("0.2");
    await page.getByRole("button", { name: "Review", exact: true }).click();
    await page.getByRole("button", { name: "Submit", exact: true }).click();

    await expect
      .poll(
        async () => {
          const v = await cli(["agents", "--profile", "wen-operator"]);
          return v.wenMining?.runs?.[1]?.wenMining?.state;
        },
        { timeout: 15000 },
      )
      .toBe("unknown");
    await expect(
      page.locator("span.badge", { hasText: /^Awaiting receipt$/ }).first(),
    ).toBeVisible();

    // Restart core -> must remain unknown (no duplicate submit) -> finalize venue -> Reconcile
    await stop();
    await writeFile(join(dir, "venue-mode"), "confirmed");
    await start();
    await cli(["login", "--password-stdin"], ownerPassword + "\n");

    const afterRestart = await cli(["agents", "--profile", "wen-operator"]);
    expect(afterRestart.wenMining.runs).toHaveLength(2);
    expect(afterRestart.wenMining.runs[1].wenMining.state).toBe("unknown");
    expect(afterRestart.wenMining.miningActivation).toBe(false);
    expect(afterRestart.wenMining.liveExecution).toBe(false);

    await page.goto(url + "/#terminals/satcoin");
    await openPasswordSignIn(page);
    await page
      .getByLabel("Owner password", { exact: true })
      .fill(ownerPassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await page.getByRole("button", { name: "Reconcile", exact: true }).click();

    await expect
      .poll(
        async () => {
          const v = await cli(["agents", "--profile", "wen-operator"]);
          return v.wenMining?.runs?.[1]?.wenMining?.state;
        },
        { timeout: 15000 },
      )
      .toBe("confirmed");
  } finally {
    await stop();
    await rm(dir, { recursive: true, force: true });
  }
});
