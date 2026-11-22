import {
  openOwnerSetup,
  openPasswordSignIn,
} from "../../scripts/fixture-owner-setup.mjs";
import { expect, test } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { revealControl } from "./helpers/core-fixture.ts";

test.use({ colorScheme: "dark" });

test("portable wallet config and v3 RPC credential restore across fresh instance and locked restart", async ({
  page,
}) => {
  test.setTimeout(120000);
  page.setDefaultTimeout(15000);

  const binary = resolve(
    process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
  );
  const root = await mkdtemp(join(tmpdir(), "fased-portable-e2e-"));
  const dirA = join(root, "instance-a");
  const dirB = join(root, "instance-b");
  await mkdir(dirA, { recursive: true, mode: 0o700 });
  await mkdir(dirB, { recursive: true, mode: 0o700 });

  const children = new Set<ChildProcess>();
  const stopChild = async (child: ChildProcess | undefined) => {
    if (!child || child.exitCode !== null || child.signalCode !== null) return;
    const exited = once(child, "exit");
    child.kill("SIGTERM");
    const timer = setTimeout(() => {
      if (child.exitCode === null) child.kill("SIGKILL");
    }, 3000);
    try {
      await exited;
    } finally {
      clearTimeout(timer);
    }
  };
  const startInstance = async (dataDir: string) => {
    let out = "";
    const child = spawn(
      binary,
      [
        "serve",
        "--require-password",
        "--listen",
        "127.0.0.1:0",
        "--data-dir",
        dataDir,
      ],
      {
        env: {
          ...process.env,
          // Keep OS keyring disabled in CI/desktop so passphrase flow is deterministic.
          DBUS_SESSION_BUS_ADDRESS: "",
        },
      },
    );
    children.add(child);
    child.stdout?.on("data", (d) => (out += String(d)));
    child.stderr?.on("data", (d) => (out += String(d)));
    await expect
      .poll(() => out.includes('"event":"listening"'), { timeout: 15000 })
      .toBe(true);
    const line = out
      .trim()
      .split("\n")
      .find((l) => l.includes('"event":"listening"'));
    const url = JSON.parse(line!).url as string;
    return { child, url };
  };

  const ownerPassword = "Portable Browser Owner Password 123";
  const vaultPassword = "Portable Browser Vault Passphrase 456";
  const rpcKey = "helius-devnet-portable-test-api-key-987654";
  const devnetAddress = "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU";

  let instA: { child: ChildProcess; url: string } | undefined;
  let instB: { child: ChildProcess; url: string } | undefined;

  try {
    // 1. Instance A: claim workspace, create Devnet watch wallet + Helius RPC + save key.
    instA = await startInstance(dirA);
    const healthA = await (
      await page.request.get(instA.url + "/api/v1/health")
    ).json();
    await page.goto(instA.url + "/#wallets/accounts");
    const claimA = JSON.parse(
      await readFile(join(dirA, "claim-code.json"), "utf8"),
    );
    await openOwnerSetup(page, claimA.code);
    await revealControl(
      page,
      page.getByLabel("Create owner password", { exact: true }),
      "",
    );
    await page
      .getByLabel("Create owner password", { exact: true })
      .fill(ownerPassword);
    await page
      .getByRole("button", { name: "Claim workspace", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();

    await page.getByRole("button", { name: "Add wallet", exact: true }).click();
    const addDialog = page.getByRole("dialog", {
      name: "Add wallet",
      exact: true,
    });
    await addDialog
      .getByLabel("Wallet name", { exact: true })
      .fill("Devnet Treasury");
    await addDialog
      .getByLabel("Network", { exact: true })
      .selectOption("devnet");
    await addDialog
      .getByLabel("Public address", { exact: true })
      .fill(devnetAddress);
    await addDialog.getByRole("button", { name: "Save", exact: true }).click();
    const accountA = page.getByRole("article", {
      name: "Account Devnet Treasury",
    });
    await expect(accountA).toBeVisible();

    await accountA.getByRole("button", { name: "RPC", exact: true }).click();
    const rpcDialogA = page.getByRole("dialog", {
      name: "RPC · Devnet Treasury",
      exact: true,
    });
    await rpcDialogA
      .getByRole("button", { name: "Helius", exact: true })
      .click();
    await rpcDialogA.getByRole("button", { name: "Save", exact: true }).click();

    const fillPassphrase = async (
      dialog: import("@playwright/test").Locator,
      secret: string,
    ) => {
      const field = dialog
        .getByLabel("Backup password", { exact: true })
        .or(dialog.getByLabel("Vault password", { exact: true }))
        .first();
      await field.fill(secret);
    };

    await rpcDialogA.getByText("API key", { exact: true }).first().click();
    await rpcDialogA.getByLabel("RPC API key", { exact: true }).fill(rpcKey);
    const saveVaultPw = rpcDialogA.getByLabel("Vault password", {
      exact: true,
    });
    if (await saveVaultPw.isVisible()) await saveVaultPw.fill(vaultPassword);
    await rpcDialogA
      .getByRole("button", { name: "Save key", exact: true })
      .click();
    await expect(
      rpcDialogA.getByRole("button", { name: "Remove key", exact: true }),
    ).toBeVisible();

    // Download portable v3 RPC backup.
    await rpcDialogA.getByText("Recovery", { exact: true }).click();
    await fillPassphrase(rpcDialogA, vaultPassword);
    const rpcDownloadPromise = page.waitForEvent("download");
    await rpcDialogA
      .getByRole("button", { name: "Download portable backup", exact: true })
      .click();
    const rpcDownload = await rpcDownloadPromise;
    expect(rpcDownload.suggestedFilename()).toBe(
      "fased-rpc-portable-backup.json",
    );
    const portableRPCPath = join(root, "fased-rpc-portable-backup.json");
    await rpcDownload.saveAs(portableRPCPath);
    const rpcBackupRaw = await readFile(portableRPCPath, "utf8");
    const rpcBackup = JSON.parse(rpcBackupRaw);
    expect(rpcBackup.version).toBe(3);
    expect(rpcBackup.portable).toBe(true);
    expect(rpcBackup.network).toBe("devnet");
    expect(rpcBackup.endpoint).toBe("https://devnet.helius-rpc.com");
    expect(rpcBackup.instanceId).toBeUndefined();
    expect(rpcBackupRaw).not.toContain(rpcKey);
    expect(rpcBackupRaw).not.toContain(vaultPassword);
    expect(rpcBackupRaw).not.toContain(healthA.instanceId);

    await rpcDialogA
      .getByRole("button", { name: "Close dialog", exact: true })
      .click();

    // Download portable wallet + RPC config JSON.
    await page.getByRole("button", { name: "Backup", exact: true }).click();
    const walletDialogA = page.getByRole("dialog", {
      name: "Wallet backup",
      exact: true,
    });
    const walletsDownloadPromise = page.waitForEvent("download");
    await walletDialogA
      .getByRole("button", { name: "Export wallets", exact: true })
      .click();
    const walletsDownload = await walletsDownloadPromise;
    expect(walletsDownload.suggestedFilename()).toBe(
      "fased-wallets-config.json",
    );
    const walletsBundlePath = join(root, "fased-wallets-config.json");
    await walletsDownload.saveAs(walletsBundlePath);
    const walletsBundle = JSON.parse(await readFile(walletsBundlePath, "utf8"));
    expect(walletsBundle.version).toBe(1);
    expect(walletsBundle.wallets).toHaveLength(1);
    expect(walletsBundle.wallets[0].address).toBe(devnetAddress);
    expect(walletsBundle.wallets[0].rpcEndpoint).toBe(
      "https://devnet.helius-rpc.com",
    );
    await stopChild(instA.child);
    instA = undefined;

    // 2. Instance B: fresh data dir with distinct instanceId, import wallet bundle + restore RPC backup.
    instB = await startInstance(dirB);
    const healthB = await (
      await page.request.get(instB.url + "/api/v1/health")
    ).json();
    expect(healthB.instanceId).not.toBe(healthA.instanceId);

    await page.goto(instB.url + "/#wallets/accounts");
    const claimB = JSON.parse(
      await readFile(join(dirB, "claim-code.json"), "utf8"),
    );
    await openOwnerSetup(page, claimB.code);
    await revealControl(
      page,
      page.getByLabel("Create owner password", { exact: true }),
      "",
    );
    await page
      .getByLabel("Create owner password", { exact: true })
      .fill(ownerPassword);
    await page
      .getByRole("button", { name: "Claim workspace", exact: true })
      .click();
    await expect(
      page.getByRole("button", { name: "Sign out", exact: true }),
    ).toBeVisible();

    await page.getByRole("button", { name: "Backup", exact: true }).click();
    const walletDialogB = page.getByRole("dialog", {
      name: "Wallet backup",
      exact: true,
    });
    await walletDialogB
      .getByLabel("Wallet backup file", { exact: true })
      .setInputFiles(walletsBundlePath);
    await walletDialogB
      .getByRole("button", { name: "Import wallets", exact: true })
      .click();
    const accountB = page.getByRole("article", {
      name: "Account Devnet Treasury",
    });
    await expect(accountB).toBeVisible();

    await accountB.getByRole("button", { name: "RPC", exact: true }).click();
    const rpcDialogB = page.getByRole("dialog", {
      name: "RPC · Devnet Treasury",
      exact: true,
    });
    await expect(rpcDialogB.getByLabel("RPC URL", { exact: true })).toHaveValue(
      "https://devnet.helius-rpc.com",
    );

    await rpcDialogB.getByText("API key", { exact: true }).first().click();
    await rpcDialogB.getByText("Recovery", { exact: true }).click();
    await rpcDialogB
      .getByLabel("Backup file", { exact: true })
      .setInputFiles(portableRPCPath);
    await fillPassphrase(rpcDialogB, vaultPassword);
    await rpcDialogB
      .getByRole("button", { name: "Verify backup", exact: true })
      .click();
    await expect(
      rpcDialogB.getByText("Backup verified", { exact: true }),
    ).toBeVisible();

    await fillPassphrase(rpcDialogB, vaultPassword);
    await rpcDialogB
      .getByRole("button", { name: "Restore", exact: true })
      .click();
    await expect(rpcDialogB.getByText("Saved", { exact: true })).toBeVisible();
    await expect(
      rpcDialogB.getByRole("button", { name: "Remove key", exact: true }),
    ).toBeVisible();

    // 3. Stop and restart Instance B to prove restored credentials remain persisted locked at rest.
    await stopChild(instB.child);
    instB = await startInstance(dirB);
    await page.goto(instB.url + "/#wallets/accounts");
    await openPasswordSignIn(page);
    await page
      .getByLabel("Owner password", { exact: true })
      .fill(ownerPassword);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();

    const reopenedAccount = page.getByRole("article", {
      name: "Account Devnet Treasury",
    });
    await expect(reopenedAccount).toBeVisible();
    await reopenedAccount
      .getByRole("button", { name: "RPC", exact: true })
      .click();
    const reopenedRPC = page.getByRole("dialog", {
      name: "RPC · Devnet Treasury",
      exact: true,
    });
    await reopenedRPC.getByText("API key", { exact: true }).first().click();
    await expect(reopenedRPC.getByText("Saved", { exact: true })).toBeVisible();
    await expect(
      reopenedRPC.getByRole("button", { name: "Remove key", exact: true }),
    ).toBeVisible();
  } finally {
    await stopChild(instA?.child);
    await stopChild(instB?.child);
    for (const c of children) await stopChild(c);
    await rm(root, { recursive: true, force: true });
  }
});
