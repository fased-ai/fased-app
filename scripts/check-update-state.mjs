// Ordinary-user, reconstructible local core fixture; no installation or wallet.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { randomBytes, createHash } from "node:crypto";
import {
  mkdir,
  mkdtemp,
  readFile,
  writeFile,
  rm,
  stat,
  rename,
} from "node:fs/promises";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";

assert(process.getuid?.() !== 0, "project code must not run as root");
const root = path.resolve(
  process.env.FASED_STATE_EVIDENCE ?? ".evidence/p4-intents",
);
const fixtures = path.join(root, "core-fixtures");
await mkdir(fixtures, { recursive: true });
const dir = await mkdtemp(path.join(fixtures, "core-"));
const binary = path.resolve(
  process.env.FASED_UPDATE_CORE ?? ".evidence/bin/fased-app",
);
const password = "Disposable snapshot owner " + randomBytes(8).toString("hex");
const probe = randomBytes(16).toString("hex");
const report = {
  result: "FAIL",
  class: "compiled ordinary-user private core/CLI fixture; no installation",
  fixtureOnly: true,
  financialExecution: false,
  checks: {},
  binarySha256: createHash("sha256")
    .update(await readFile(binary))
    .digest("hex"),
};
let child,
  endpoint,
  log = "";
const run = (args, input) => {
  const r = spawnSync(binary, [...args, "--data-dir", dir], {
    input,
    encoding: "utf8",
    timeout: 10000,
  });
  if (r.error) throw r.error;
  if (r.status !== 0) throw new Error(r.stderr);
  return r.stdout;
};
const check = (name, condition) => {
  assert(condition, name);
  report.checks[name] = true;
  log += `PASS ${name}\n`;
};
const stop = async (signal = "SIGTERM") => {
  if (!child || child.exitCode !== null) return;
  const current = child;
  await new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error("fixture core did not stop")),
      5000,
    );
    current.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
    current.kill(signal);
  });
};
const start = async () => {
  await rm(path.join(dir, "endpoint.json"), { force: true });
  child = spawn(
    binary,
    ["serve", "--data-dir", dir, "--listen", "127.0.0.1:0"],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  child.stdout.on("data", (b) => {
    log += b;
  });
  child.stderr.on("data", (b) => {
    log += b;
  });
  for (let n = 0; n < 100; n++) {
    if (child.exitCode !== null) throw new Error("fixture core exited");
    try {
      endpoint = JSON.parse(
        await readFile(path.join(dir, "endpoint.json"), "utf8"),
      );
      return;
    } catch {}
    await delay(50);
  }
  throw new Error("core readiness timeout");
};
try {
  await start();
  const unauth = await fetch(
    endpoint.url + "/api/v1/updates/inventory?probe=" + probe,
  );
  check("unauthenticatedInventoryDenied", unauth.status === 401);
  const code = run(["claim-code"]).trim();
  const claimed = await fetch(endpoint.url + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: endpoint.url, "Content-Type": "application/json" },
    body: JSON.stringify({ code, password }),
  });
  assert.equal(claimed.status, 200);
  const session = await claimed.json();
  run(["login", "--password-stdin"], password + "\n");
  const snapshot = () =>
    JSON.parse(run(["update-inventory", "--probe-id", probe]));
  const a = snapshot(),
    b = snapshot();
  check(
    "actualPrivateCoreSnapshot",
    a.version === 1 &&
      a.probeId === probe &&
      a.state.name === "core-db" &&
      a.state.version === 29 &&
      a.state.features.includes("agents-task-analysis-v1") &&
      a.state.features.includes("agents-playbooks-v1") &&
      a.state.features.includes("agents-cron-v1") &&
      a.state.features.includes("agents-history-v1") &&
      a.state.features.includes("agents-recall-v1") &&
      a.state.features.includes("agents-chat-permission-v1") &&
      a.state.features.includes("agents-calendar-v1") &&
      a.state.features.includes("agents-ai-accounts-v1") &&
      a.state.features.includes("agents-current-models-v1") &&
      a.state.features.includes("agents-native-providers-v1") &&
      a.state.features.includes("agents-signin-plan-v2") &&
      a.state.features.includes("agents-local-v1") &&
      a.state.features.includes("agents-wen-paper-v1"),
  );
  check("stableReadOnlySnapshot", JSON.stringify(a) === JSON.stringify(b));
  check(
    "noCredentialInInventory",
    !JSON.stringify(a).includes(password) &&
      !JSON.stringify(a).includes("passwordHash"),
  );
  run([
    "setup",
    "--host",
    "vps",
    "--wallet-mode",
    "skip",
    "--provider",
    "none",
  ]);
  const changed = snapshot();
  check(
    "cliMetadataChangeBound",
    changed.state.anchor !== a.state.anchor && changed.bootId === a.bootId,
  );
  await stop();
  await start();
  const restarted = snapshot();
  check(
    "actualCoreRestartChangesBoot",
    restarted.bootId !== changed.bootId &&
      restarted.state.anchor === changed.state.anchor &&
      restarted.instanceId === changed.instanceId,
  );
  const command = {
    action: "pause",
    probeId: probe,
    request: {
      version: 1,
      intentId: randomBytes(16).toString("hex"),
      sequence: "1",
      manifestSha256: createHash("sha256")
        .update("disposable metadata fence, no signer authority")
        .digest("hex"),
      expected: restarted,
    },
  };
  const control = (c) =>
    JSON.parse(run(["update-pause", "--request-stdin"], JSON.stringify(c)));
  const publicAttempt = await fetch(endpoint.url + "/api/v1/updates/pause", {
    method: "POST",
    headers: {
      Origin: endpoint.url,
      "Content-Type": "application/json",
      Authorization: "Bearer " + session.sessionToken,
      "X-CSRF-Token": session.csrfToken,
    },
    body: JSON.stringify(command),
  });
  check("browserCannotAdministerCorePause", publicAttempt.status === 403);
  const paused = control(command);
  check(
    "privateCLICreatesExclusivePause",
    paused.state === "paused" &&
      paused.request.intentId === command.request.intentId,
  );
  let denied = false;
  try {
    run(["setup", "--host", "local"]);
  } catch (e) {
    denied = /core metadata paused/.test(e.message);
  }
  check("pausedSetupWriteDenied", denied);
  run(["login", "--password-stdin"], password + "\n");
  check(
    "ownerLoginRemainsAvailableWhilePaused",
    control({ action: "status", probeId: probe }).state === "paused",
  );
  await stop("SIGKILL");
  await start();
  const recovered = control(command);
  check(
    "actualHardRestartRetainsPause",
    recovered.state === "paused" &&
      recovered.pauseId === paused.pauseId &&
      recovered.core.bootId !== paused.core.bootId &&
      recovered.core.state.anchor === paused.core.state.anchor,
  );
  // Synthetic public preflight exercises app metadata only. It does not
  // impersonate signer authority; the actual owner/signer proof runs on Ubuntu.
  const digest = (kind, value) =>
    createHash("sha256")
      .update("fased-update-snapshot:v1:" + kind + "\0")
      .update(JSON.stringify(value))
      .digest("hex");
  const proof = (pause) => {
    const p = {
      version: 1,
      intentId: command.request.intentId,
      sequence: "1",
      manifestSha256: command.request.manifestSha256,
      pause,
      signer: {
        version: 1,
        probeId: probe,
        bootId: probe,
        authoritySha256: command.request.manifestSha256,
        states: [
          {
            name: "vault",
            version: 1,
            features: [],
            anchor: command.request.manifestSha256,
          },
          {
            name: "operations",
            version: 1,
            features: [],
            anchor: command.request.manifestSha256,
          },
          {
            name: "mock-receipts",
            version: 1,
            features: [],
            anchor: command.request.manifestSha256,
          },
        ],
        spent: "0",
        held: "0",
        pending: 0,
        unknown: 0,
        recoveryHold: false,
        sequenceFloor: "1",
        activeIntentId: command.request.intentId,
      },
      files: ["app", "signer"].map((file) => ({
        artifact: { file, size: 4, sha256: command.request.manifestSha256 },
        bytes: 4,
      })),
      bindingSha256: "",
      verified: true,
      installAllowed: false,
      resumeAllowed: false,
      financialExecution: false,
    };
    p.bindingSha256 = digest("preflight:v1", p);
    return p;
  };
  const backup = (p = proof(control({ action: "status", probeId: probe }))) =>
    JSON.parse(run(["update-backup", "--request-stdin"], JSON.stringify(p)));
  const blocked = await fetch(endpoint.url + "/api/v1/updates/backup", {
    method: "POST",
    headers: {
      Origin: endpoint.url,
      "Content-Type": "application/json",
      Authorization: "Bearer " + session.sessionToken,
      "X-CSRF-Token": session.csrfToken,
    },
    body: JSON.stringify(proof(recovered)),
  });
  check("browserCannotCreateMetadataBackup", blocked.status === 403);
  const index = backup(),
    backupDir = path.join(dir, "lifecycle", command.request.intentId);
  const snapshotBytes = await readFile(path.join(backupDir, "metadata.sqlite"));
  check(
    "compiledBackupPublishesVerifiedSQLiteIndex",
    index.phase === "metadata-backed-up" &&
      index.snapshot.sha256 ===
        createHash("sha256").update(snapshotBytes).digest("hex") &&
      index.snapshot.size === snapshotBytes.length,
  );
  check(
    "backupCannotInstallResumeOrExecute",
    !index.installAllowed && !index.resumeAllowed && !index.financialExecution,
  );
  check(
    "privateMetadataAndIndexModes",
    (await stat(backupDir)).mode % 512 === 0o700 &&
      (await stat(path.join(backupDir, "metadata.sqlite"))).mode % 512 ===
        0o600 &&
      (await stat(path.join(backupDir, "recovery.json"))).mode % 512 === 0o600,
  );
  check(
    "publicReceiptContainsNoCredentials",
    !JSON.stringify(index).includes(password) &&
      !JSON.stringify(index).includes("passwordHash") &&
      !JSON.stringify(index).includes("password_salt"),
  );
  // Reconstruct exact crash boundaries only in this declared disposable core.
  await rename(
    path.join(backupDir, "recovery.json"),
    path.join(backupDir, "recovery.json.pending"),
  );
  check(
    "compiledIndexPublicationRetry",
    JSON.stringify(backup()) === JSON.stringify(index),
  );
  await rm(path.join(backupDir, "recovery.json"));
  await rename(
    path.join(backupDir, "metadata.sqlite"),
    path.join(backupDir, "metadata.sqlite.pending"),
  );
  await writeFile(
    path.join(backupDir, "metadata.sqlite.pending"),
    snapshotBytes.subarray(0, 4096),
    { mode: 0o600 },
  );
  await stop("SIGKILL");
  await start();
  check(
    "compiledPartialSnapshotRecoversAcrossHardRestart",
    JSON.stringify(backup()) === JSON.stringify(index),
  );
  let stale = false;
  try {
    backup(proof(recovered));
  } catch {
    stale = true;
  }
  check("compiledBackupRejectsStaleBoot", stale);
  const outcomes = await Promise.all(
    Array.from(
      { length: 3 },
      () =>
        new Promise((resolve) => {
          const c = spawn(
            binary,
            ["update-backup", "--data-dir", dir, "--request-stdin"],
            { stdio: ["pipe", "pipe", "pipe"] },
          );
          let output = "";
          c.stdout.on("data", (b) => (output += b));
          c.on("error", () => resolve(false));
          c.on("exit", (code) =>
            resolve(
              code === 0
                ? JSON.stringify(JSON.parse(output)) === JSON.stringify(index)
                : true,
            ),
          );
          c.stdin.end(
            JSON.stringify(
              proof(control({ action: "status", probeId: probe })),
            ),
          );
        }),
    ),
  );
  check(
    "concurrentCompiledRetriesPreserveOneIndex",
    outcomes.every(Boolean) &&
      JSON.stringify(backup()) === JSON.stringify(index),
  );
  report.metadataBackup = {
    bindingSha256: index.bindingSha256,
    snapshot: index.snapshot,
  };
  const released = control({
    action: "release",
    probeId: probe,
    aborted: {
      version: 1,
      id: command.request.intentId,
      sequence: "1",
      manifestSha256: command.request.manifestSha256,
      core: command.request.expected,
      signer: {
        version: 1,
        probeId: probe,
        bootId: probe,
        authoritySha256: command.request.manifestSha256,
        states: [],
        spent: "0",
        held: "0",
        pending: 0,
        unknown: 0,
        recoveryHold: false,
        sequenceFloor: "0",
        activeIntentId: "",
      },
      createdAt: 1,
      state: "aborted",
    },
  });
  check(
    "metadataReleaseRetainsPauseTombstone",
    released.state === "released" && released.pauseId === paused.pauseId,
  );
  denied = false;
  try {
    control(command);
  } catch (e) {
    denied = /cannot replay/.test(e.message);
  }
  check("releasedPauseCannotReplay", denied);
  let releasedBackup = false;
  try {
    backup();
  } catch {
    releasedBackup = true;
  }
  check("releasedPauseCannotReacknowledgeBackup", releasedBackup);
  run(["setup", "--host", "local"]);
  check(
    "explicitMetadataReleaseRestoresSetupWrites",
    snapshot().state.anchor !== recovered.core.state.anchor,
  );
  const health = JSON.parse(run(["status"]));
  check(
    "realCapabilitiesRemainDisabled",
    health.capabilities.walletSigner === false &&
      health.capabilities.financialExecution === false &&
      health.capabilities.updater === false,
  );
  const bad = spawnSync(
    binary,
    ["update-inventory", "--data-dir", dir, "--probe-id", "invalid"],
    { encoding: "utf8", timeout: 10000 },
  );
  check(
    "invalidProbeFailsWithoutSnapshot",
    bad.status !== 0 && bad.stdout === "",
  );
  if (process.env.FASED_PREVIOUS_CORE) {
    await stop();
    const older = spawnSync(
      path.resolve(process.env.FASED_PREVIOUS_CORE),
      ["serve", "--data-dir", dir, "--listen", "127.0.0.1:0"],
      { encoding: "utf8", timeout: 10000 },
    );
    check(
      "previousCoreRefusesNewerSchema",
      older.status !== 0 && /schema is newer/.test(older.stderr),
    );
  }
  report.result = "PASS";
} catch (error) {
  report.error = error.message;
  log += error.stack + "\n";
  process.exitCode = 1;
} finally {
  try {
    await stop();
  } catch (error) {
    child?.kill("SIGKILL");
    report.result = "FAIL";
    report.cleanup = error.message;
    process.exitCode = 1;
  }
  if (!report.cleanup) {
    await rm(dir, { recursive: true });
    report.cleanup =
      "PASS: generated core/session fixture removed; no owner installation";
  }
  await writeFile(
    path.join(root, "core-cli-result.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  await writeFile(path.join(root, "core-cli.log"), log);
  console.log(
    `${report.result}: ${Object.keys(report.checks).length} compiled core snapshot predicates`,
  );
}
