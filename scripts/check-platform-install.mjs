// Native OS service acceptance on declared generated fixture data only.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import {
  mkdir,
  readFile,
  writeFile,
  rm,
  readdir,
  stat,
} from "node:fs/promises";
import { createHash, randomBytes } from "node:crypto";
import { resolve, join } from "node:path";
import { createServer } from "node:net";
const root = resolve(".evidence/platform-install");
const state = join(root, "state"),
  app = join(root, "app");
await mkdir(root, { mode: 0o700 });
await writeFile(
  join(root, "fixture.json"),
  JSON.stringify({ nonauthoritative: true, app, state }),
  { mode: 0o600 },
);
let binary = resolve(
  `.evidence/bin/fased-app${process.platform === "win32" ? ".exe" : ""}`,
);
let bundleDir;
const archive = process.env.FASED_PACKAGE_ARCHIVE;
if (archive) {
  const unpack = join(root, "unpacked");
  await mkdir(unpack, { mode: 0o700 });
  execFileSync(process.platform === "win32" ? "tar.exe" : "tar", [
    "-xf",
    resolve(archive),
    "-C",
    unpack,
  ]);
  const arch = process.arch === "x64" ? "amd64" : process.arch;
  bundleDir =
    process.platform === "darwin"
      ? join(unpack, "Fased App.app", "Contents", "MacOS")
      : join(
          unpack,
          `fased-app-${process.platform === "win32" ? "windows" : "linux"}-${arch}`,
        );
  binary = join(
    bundleDir,
    process.platform === "win32" ? "fased-app.exe" : "fased-app",
  );
}
const flags = ["--prefix", app, "--data-dir", state];
const cli = (args, input) =>
  execFileSync(binary, args, { input, encoding: "utf8", timeout: 40000 });
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
const report = { platform: process.platform, result: "FAIL", checks: {} };
let installed = false;
async function hashes(dir) {
  const result = {};
  for (const n of await readdir(dir)) {
    if ((await stat(join(dir, n))).isFile())
      result[n] = createHash("sha256")
        .update(await readFile(join(dir, n)))
        .digest("hex");
  }
  return result;
}
try {
  const probe = createServer();
  await new Promise((r) => probe.listen(0, "127.0.0.1", r));
  const port = probe.address().port;
  await new Promise((r) => probe.close(r));
  const args = [
    "install",
    ...flags,
    "--listen",
    `127.0.0.1:${port}`,
    ...(bundleDir ? ["--bundle", bundleDir] : []),
  ];
  const result = JSON.parse(cli(args));
  installed = true;
  const url = result.url;
  const health = await (await fetch(url + "/api/v1/health")).json();
  assert.equal(health.capabilities.walletSigner, false);
  const password = "Generated-Fixture-" + randomBytes(24).toString("base64url");
  const code = JSON.parse(
    await readFile(join(state, "claim-code.json"), "utf8"),
  ).code;
  const response = await fetch(url + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: url, "Content-Type": "application/json" },
    body: JSON.stringify({ code, password }),
  });
  assert.equal(response.status, 200);
  const auth = await response.json();
  assert.ok(auth.sessionToken);
  assert.equal((await fetch(url + "/api/v1/setup")).status, 401);
  cli(["login", "--data-dir", state, "--password-stdin"], password + "\n");
  cli(["setup", "--data-dir", state, "--host", "vps"]);
  const expected = JSON.parse(cli(["setup", "--data-dir", state]));
  const manifest = JSON.parse(
    await readFile(join(app, "install.json"), "utf8"),
  );
  const installedCore = join(
    app,
    process.platform === "win32" ? "fased-app.exe" : "fased-app",
  );
  const before = (await stat(installedCore)).mtimeMs;
  cli(args);
  assert.equal((await stat(installedCore)).mtimeMs, before);
  report.checks.installRetry = "PASS";
  const duplicate = spawn(
    binary,
    ["serve", "--data-dir", state, "--listen", "127.0.0.1:0"],
    { stdio: "ignore" },
  );
  const exit = await new Promise((r, e) => {
    duplicate.on("exit", r);
    duplicate.on("error", e);
  });
  assert.notEqual(exit, 0);
  report.checks.singleEngine = "PASS";
  cli(["service", "stop", ...flags]);
  for (let i = 0; i < 50; i++) {
    if (!JSON.parse(cli(["service", "status", ...flags])).active) break;
    await pause(100);
  }
  cli(["service", "start", ...flags]);
  assert.deepEqual(JSON.parse(cli(["setup", "--data-dir", state])), expected);
  assert.equal(
    JSON.parse(cli(["status", "--data-dir", state])).instanceId,
    health.instanceId,
  );
  report.checks.restartOwnershipSettings = "PASS";
  const replay = await fetch(url + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: url, "Content-Type": "application/json" },
    body: JSON.stringify({ code, password }),
  });
  assert.notEqual(replay.status, 200);
  report.checks.claimReplayRejected = "PASS";
  cli(["service", "stop", ...flags]);
  const preserved = await hashes(state);
  await writeFile(
    join(app, "owner-note.txt"),
    "generated unknown-file fixture",
    { mode: 0o600 },
  );
  cli(["uninstall", ...flags]);
  installed = false;
  assert.deepEqual(await hashes(state), preserved);
  assert.equal(
    await readFile(join(app, "owner-note.txt"), "utf8"),
    "generated unknown-file fixture",
  );
  report.checks.uninstallPreservesAllData = "PASS";
  report.instanceId = health.instanceId;
  report.binarySHA256 = createHash("sha256")
    .update(await readFile(binary))
    .digest("hex");
  report.result = "PASS";
} catch (error) {
  report.error = String(error);
  throw error;
} finally {
  let receiptRemains = false;
  try {
    const receipt = JSON.parse(
      await readFile(join(app, "install.json"), "utf8"),
    );
    receiptRemains = receipt.root === app && receipt.data === state;
  } catch {}
  if (installed || receiptRemains) {
    try {
      cli(["uninstall", ...flags]);
    } catch (error) {
      report.cleanupError = String(error);
    }
  }
  await writeFile(
    ".evidence/platform-install-result.json",
    JSON.stringify(report, null, 2) + "\n",
  );
  if (!report.cleanupError) await rm(root, { recursive: true });
}
