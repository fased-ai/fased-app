import { expect, test } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createHash } from "node:crypto";
import { refreshFixtureAccounts } from "../../scripts/fixture-account-journey.mjs";

test.use({ colorScheme: "dark" });
test("wallet Refresh joins encrypted RPC and CLI without changing saved data", async ({
  page,
}, info) => {
  test.setTimeout(90000);
  page.setDefaultTimeout(10000);
  const evidence = resolve(".evidence/b05-wallet-read", info.project.name);
  await mkdir(evidence, { recursive: true, mode: 0o700 });
  // Reconstructible isolated fixture state; persist results, never a real key.
  const dir = await mkdtemp(join(tmpdir(), "fased-wread-"));
  const binary = resolve(
    process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
  );
  const seed = resolve(
    process.env.FASED_WALLET_BROWSER_BINARY ?? ".evidence/bin/wallet-core.test",
  );
  const origins = new Set<string>();
  let child: ChildProcess | undefined,
    log = "",
    passed = false,
    url = "";
  const run = async (args: string[], input?: unknown) => {
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
    return { code, out, err };
  };
  const start = async () => {
    await rm(join(dir, "wallet-read-ready.json"), { force: true });
    child = spawn(
      seed,
      ["-test.run=^TestWalletReadUIFixture$", "-test.timeout=100s"],
      {
        cwd: resolve("."),
        env: { ...process.env, FASED_WALLET_READ_UI_DIR: dir },
      },
    );
    child.stdout.on("data", (b) => (log += b));
    child.stderr.on("data", (b) => (log += b));
    let ready: any;
    await expect
      .poll(async () => {
        if (child?.exitCode !== null)
          throw new Error(log || "fixture process exited");
        try {
          ready = JSON.parse(
            await readFile(join(dir, "wallet-read-ready.json"), "utf8"),
          );
          return true;
        } catch {
          return false;
        }
      })
      .toBe(true);
    url = ready.url;
    origins.add(url);
    return ready;
  };
  const stop = async () => {
    if (child?.exitCode === null && child.signalCode === null) {
      const exit = once(child, "exit");
      child.kill("SIGTERM");
      await exit;
    }
  };
  try {
    const ready = await start();
    const external: string[] = [];
    await page.route("**/*", (route) => {
      if (!origins.has(new URL(route.request().url()).origin)) {
        external.push(route.request().url());
        return route.abort();
      }
      return route.continue();
    });
    await page.goto(url + "/#wallets/accounts");
    await page
      .getByLabel("Owner password", { exact: true })
      .fill(ready.ownerPassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    const account = page.getByRole("article", {
      name: "Account Disposable watch",
    });
    await expect(account).toBeVisible();
    const before = await run(["accounts"]); // Session is CLI-owned, sign in separately.
    expect(before.code).not.toBe(0);
    expect(
      (await run(["login", "--password-stdin"], ready.ownerPassword + "\n"))
        .code,
    ).toBe(0);
    const saved = (await run(["accounts"])).out;
    const vault = join(dir, `rpc-credential-${ready.input.accountId}.json`);
    const hash = () =>
      readFile(vault).then((v) => createHash("sha256").update(v).digest("hex"));
    const original = await hash();
    const refresh = async () => {
      await account
        .getByRole("button", { name: "Refresh", exact: true })
        .click();
      const dialog = page.getByRole("dialog", {
        name: "Unlock RPC",
        exact: true,
      });
      await dialog
        .getByLabel("Vault password", { exact: true })
        .fill(ready.input.passphrase);
      await dialog
        .getByRole("button", { name: "Refresh", exact: true })
        .click();
      return dialog;
    };
    await account.getByRole("button", { name: "Refresh", exact: true }).click();
    await page
      .getByRole("dialog", { name: "Unlock RPC", exact: true })
      .getByRole("button", { name: "Cancel", exact: true })
      .click();
    await expect(account).toContainText("Unavailable");
    const dialog = await refresh();
    await expect(dialog).not.toBeVisible();
    await expect(account).toContainText("9007199.254740993 SOL");
    await expect(account).toContainText("9007199254.741");
    await account.locator("summary").filter({ hasText: "Activity" }).click();
    await expect(account).toContainText("Finalized");
    const cli = await run(["accounts", "--read-stdin"], ready.input);
    expect(cli.code, cli.err).toBe(0);
    const result = JSON.parse(cli.out);
    expect(result.lamports).toBe("9007199254740993");
    expect(result.activity).toHaveLength(1);
    expect((await run(["accounts"])).out).toBe(saved);
    expect(await hash()).toBe(original);
    await writeFile(join(evidence, "snapshot.json"), cli.out);
    await page.screenshot({
      path: join(evidence, "wallets.png"),
      fullPage: true,
    });
    await refreshFixtureAccounts(page);
    await expect(
      account.getByRole("region", { name: "Balance", exact: true }),
    ).toContainText("Unavailable");
    await writeFile(join(dir, "read-mode"), "failure");
    const failed = await refresh();
    await expect(failed.getByRole("alert")).toHaveText("Unavailable");
    await failed.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(account).not.toContainText("9007199.254740993 SOL");
    await writeFile(join(dir, "read-mode"), "zero");
    await refresh();
    await expect(
      page.getByRole("dialog", { name: "Unlock RPC", exact: true }),
    ).not.toBeVisible();
    await expect(account).toContainText("0 SOL");
    await account.locator("summary").filter({ hasText: "Activity" }).click();
    await expect(account).toContainText("No recent activity");
    expect(await hash()).toBe(original);
    expect(external).toEqual([]);
    await stop();
    const restarted = await start();
    const resumed = await run(["accounts", "--read-stdin"], {
      ...ready.input,
      revision: restarted.input.revision,
    });
    expect(resumed.code, resumed.err).toBe(0);
    expect(JSON.parse(resumed.out).lamports).toBe("0");
    expect(await hash()).toBe(original);
    await writeFile(
      join(evidence, "result.json"),
      JSON.stringify({
        result: "PASS",
        fixtureOnly: true,
        liveRPC: false,
        externalRequests: external,
        uiCliAndRestart: true,
        accountsAndCiphertextPreserved: true,
      }),
    );
    passed = true;
  } finally {
    await stop();
    await writeFile(
      join(evidence, "fixture.log"),
      log.replaceAll(
        /Disposable provider vault password 123/g,
        "[fixture password]",
      ),
    );
    // Unique results are retained above; this isolated state is reproducible.
    await rm(dir, { recursive: true, force: true });
    await writeFile(
      join(evidence, "cleanup.json"),
      JSON.stringify({
        result: "PASS",
        fixtureStateRemoved: true,
        journeyPassed: passed,
      }),
    );
  }
});
