// Unprivileged supporting proof. Installed UID proof stays in check-enrollment-install.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "@playwright/test";
import { enrollmentAPI } from "./fixture-enrollment-state.mjs";
import {
  walletReadJourney,
  refusedWalletRead,
} from "./fixture-wallet-read-journey.mjs";

assert.ok(process.getuid() > 0);
assert.equal(
  process.argv.length,
  4,
  "explicit compiled core test and CLI binaries required",
);
const fixture = resolve(process.argv[2]),
  binary = resolve(process.argv[3]);
const evidence = resolve(".evidence/b05-installed-wallet-read");
await mkdir(evidence, { recursive: true, mode: 0o700 });
// Keep AF_UNIX paths below the kernel limit; results stay in the named evidence.
const stateRoot = resolve(".evidence/core-fixtures");
await mkdir(stateRoot, { recursive: true, mode: 0o700 });
const report = {
  result: "FAIL",
  class:
    "unprivileged Local/Remote core/API/CLI/UI; not installed UID acceptance",
  checks: {},
  inputs: {},
  fixtureOnly: true,
  live: false,
};
const sha = (b) => createHash("sha256").update(b).digest("hex");
for (const file of [fixture, binary])
  report.inputs[file] = sha(await readFile(file));
const check = (name, value) => {
  assert.equal(value, true, name);
  report.checks[name] = true;
};
const browser = await chromium.launch({ headless: true });
try {
  for (const location of ["local", "remote"]) {
    const root = await mkdtemp(join(stateRoot, "wr-"));
    let child,
      log = "",
      ready;
    const stop = async () => {
      if (child && child.exitCode === null && child.signalCode === null) {
        const done = once(child, "exit");
        child.kill("SIGTERM");
        await done;
      }
    };
    const start = async () => {
      await rm(join(root, "read-ready.json"), { force: true });
      child = spawn(
        fixture,
        ["-test.run=^TestOrdinaryWalletReadUIFixture$", "-test.timeout=90s"],
        {
          env: {
            ...process.env,
            FASED_ORDINARY_READ_UI_DIR: root,
            FASED_ORDINARY_READ_LOCATION: location,
          },
        },
      );
      child.stdout.on("data", (b) => (log += b));
      child.stderr.on("data", (b) => (log += b));
      const until = Date.now() + 20000;
      while (Date.now() < until) {
        if (child.exitCode !== null) throw Error(log || "fixture exited");
        try {
          ready = JSON.parse(
            await readFile(join(root, "read-ready.json"), "utf8"),
          );
          return;
        } catch (e) {
          if (e.code !== "ENOENT") throw e;
        }
        await new Promise((r) => setTimeout(r, 40));
      }
      throw Error("fixture readiness deadline");
    };
    const cli = async (args, input) => {
      const p = spawn(binary, [...args, "--data-dir", ready.dataDir]);
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
      if (code !== 0) throw Error(err || "CLI refused");
      return args[0] === "login" ? out : JSON.parse(out);
    };
    let session = "";
    const api = (path, method = "GET", body) =>
      enrollmentAPI(ready.url, session, path, method, body);
    const login = async () => {
      session = (
        await api("/api/v1/auth/login", "POST", {
          password: ready.ownerPassword,
        })
      ).sessionToken;
      assert.ok(session);
      await cli(["login", "--password-stdin"], ready.ownerPassword + "\n");
    };
    const attach = async (name) => {
      let view = await api("/api/v1/accounts");
      view = await api("/api/v1/accounts/setup", "PUT", {
        revision: view.record.revision,
        action: "attach",
        name,
        network: "devnet",
        reference: ready.reference,
      });
      const id = view.record.accounts.at(-1).id;
      view = await api("/api/v1/accounts/rpc", "PUT", {
        revision: view.record.revision,
        accountId: id,
        network: "devnet",
        endpoint: "https://devnet.helius-rpc.com",
      });
      await api("/api/v1/accounts/rpc-credential", "PUT", {
        revision: view.record.revision,
        accountId: id,
        action: "save",
        key: "fased-test-installed-read",
        passphrase: ready.vaultPassword,
      });
      return { view, id };
    };
    const read = (view, id, label) =>
      walletReadJourney({
        view,
        accountId: id,
        password: ready.vaultPassword,
        ownerPassword: ready.ownerPassword,
        url: ready.url,
        api,
        cliRead: (input) => cli(["accounts", "--read-stdin"], input),
        browser,
        evidence,
        label,
        check,
      });
    try {
      await start();
      await login();
      const { view, id } = await attach("Disposable " + location);
      const vault = join(ready.dataDir, `rpc-credential-${id}.json`),
        original = sha(await readFile(vault));
      const observation = await read(view, id, location + "-initial");
      const reference = ready.reference;
      await stop();
      await start();
      await login();
      assert.deepEqual(ready.reference, reference);
      await read(await api("/api/v1/accounts"), id, location + "-restart");
      check(
        location + ":restartPreservesCiphertext",
        sha(await readFile(vault)) === original,
      );
      await stop();
      await writeFile(join(root, "recover"), "disposable recovery", {
        mode: 0o600,
      });
      await start();
      await login();
      check(
        location + ":recoveryFreshEpoch",
        ready.reference.epoch !== reference.epoch &&
          ready.reference.publicKey === reference.publicKey,
      );
      let stale = await api("/api/v1/accounts");
      check(location + ":oldAccountStale", stale.accountStates[id] === "stale");
      for (const [suffix, selection] of [
        ["balance", undefined],
        ["detail", observation.selection],
      ])
        await refusedWalletRead({
          input: {
            ...observation.input,
            revision: stale.record.revision,
            selection,
          },
          api,
          cliRead: (input) => cli(["accounts", "--read-stdin"], input),
          check,
          label: location + "-recovery-" + suffix,
        });
      const fresh = await attach("Recovered " + location);
      assert.notEqual(fresh.id, id);
      await read(fresh.view, fresh.id, location + "-reconnected");
      check(
        location + ":oldCiphertextPreserved",
        sha(await readFile(vault)) === original,
      );
      check(
        location + ":custodyHeld",
        ready.status.locked &&
          ready.status.recoveryRequired &&
          ready.status.admissionHeld &&
          !ready.status.walletSigner &&
          !ready.status.financialExecution,
      );
    } finally {
      await stop();
      await writeFile(join(evidence, location + "-fixture.log"), log, {
        mode: 0o600,
      });
      await rm(root, { recursive: true, force: true });
    }
  }
  report.result = "PASS";
} finally {
  await browser.close();
  await writeFile(
    join(evidence, "joined-acceptance.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  console.log(
    JSON.stringify({
      result: report.result,
      checks: Object.keys(report.checks).length,
    }),
  );
}
