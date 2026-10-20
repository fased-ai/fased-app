// Ordinary-user, reconstructible local core fixture; no installation or wallet.
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { randomBytes, createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, writeFile, rm } from "node:fs/promises";
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
const stop = async () => {
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
    current.kill("SIGTERM");
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
      a.state.version === 2,
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
