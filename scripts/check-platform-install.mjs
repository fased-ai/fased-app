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
  open,
  readlink,
} from "node:fs/promises";
import { createHash, randomBytes } from "node:crypto";
import { resolve, join } from "node:path";
import { createServer } from "node:net";
import { checkUbuntuPreparation } from "./ubuntu-preparation.mjs";
const startedAt = Date.now();
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
const flags = ["--prefix", app, "--data-dir", state];
const cli = (args, input) =>
  execFileSync(binary, args, { input, encoding: "utf8", timeout: 40000 });
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
const report = { platform: process.platform, result: "FAIL", checks: {} };
let installed = false;
const freshFetch = (url, options = {}) =>
  fetch(url, {
    ...options,
    headers: { Connection: "close", ...options.headers },
  });
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
async function waitForSQLiteClose(dir) {
  // launchctl bootout can return before the terminated process closes SQLite.
  // Keep every data-file hash check; wait for graceful checkpoint/sidecar removal.
  for (let i = 0; i < 100; i++) {
    const live = await Promise.all(
      ["core.sqlite-wal", "core.sqlite-shm"].map(async (name) => {
        try {
          await stat(join(dir, name));
          return true;
        } catch (error) {
          if (error.code === "ENOENT") return false;
          throw error;
        }
      }),
    );
    if (!live.some(Boolean)) return;
    await pause(50);
  }
  throw new Error(
    "SQLite did not close after service stop; preservation snapshot refused",
  );
}

try {
  if (archive) {
    const unpack = join(root, "unpacked");
    await mkdir(unpack, { mode: 0o700 });
    execFileSync(
      process.platform === "win32"
        ? join(process.env.SystemRoot || "C:\\Windows", "System32", "tar.exe")
        : "tar",
      ["-xf", resolve(archive), "-C", unpack],
    );
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
  if (process.env.FASED_TEST_HOST_PREPARE === "1")
    await checkUbuntuPreparation(binary, args, state, report);
  const result = JSON.parse(cli(args));
  installed = true;
  const url = result.url;
  if (process.platform === "linux") {
    const group = execFileSync(
      "systemctl",
      [
        "--user",
        "show",
        "fased-app.service",
        "--property=ControlGroup",
        "--value",
      ],
      { encoding: "utf8" },
    ).trim();
    assert.match(group, /^\/user.slice\//);
    const candidates = (
      await readFile(`/sys/fs/cgroup${group}/cgroup.procs`, "utf8")
    )
      .trim()
      .split(/\s+/);
    const engines = [];
    for (const candidate of candidates) {
      if (!/^\d+$/.test(candidate)) continue;
      try {
        if (
          (await readlink(`/proc/${candidate}/exe`)) === join(app, "fased-app")
        )
          engines.push(Number(candidate));
      } catch (error) {
        if (error.code !== "ENOENT") throw error;
      }
    }
    assert.equal(
      engines.length,
      1,
      "expected one actual Go engine in the owned service cgroup",
    );
    const pid = engines[0];
    const info = await readFile(`/proc/${pid}/status`, "utf8");
    assert.match(info, /^NoNewPrivs:\s+1$/m);
    assert.match(info, new RegExp(`^Uid:\\s+${process.getuid()}\\s`, "m"));
    let denied = false;
    try {
      const fd = await open(
        `/proc/${pid}/root${join(app, "fased-app.service")}`,
        "r+",
      );
      await fd.close();
    } catch (error) {
      if (error.code !== "EROFS") throw error;
      denied = true;
    }
    assert.ok(denied, "kernel did not enforce read-only application files");
    const fd = await open(
      `/proc/${pid}/root${join(state, "core.sqlite")}`,
      "r+",
    );
    await fd.close();
    report.checks.kernelIsolation =
      "PASS: ordinary UID, NoNewPrivs, read-only application definition and writable private state";
  }

  const health = await (await freshFetch(url + "/api/v1/health")).json();
  assert.equal(health.capabilities.walletSigner, false);
  const password = "Generated-Fixture-" + randomBytes(24).toString("base64url");
  const code = JSON.parse(
    await readFile(join(state, "claim-code.json"), "utf8"),
  ).code;
  const response = await freshFetch(url + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: url, "Content-Type": "application/json" },
    body: JSON.stringify({ code, password }),
  });
  assert.equal(response.status, 200);
  const auth = await response.json();
  assert.ok(auth.sessionToken);
  assert.equal((await freshFetch(url + "/api/v1/setup")).status, 401);
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
  const replay = await freshFetch(url + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: url, "Content-Type": "application/json" },
    body: JSON.stringify({ code, password }),
  });
  assert.notEqual(replay.status, 200);
  report.checks.claimReplayRejected = "PASS";
  cli(["service", "stop", ...flags]);
  if (process.platform === "darwin") await waitForSQLiteClose(state);
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
  if (process.platform === "win32") {
    // Keep the primary acceptance failure. Queries inspect only this generated
    // task; the fresh direct process below is a diagnostic, never a fallback
    // install or a reason to report PASS. Project code remains ordinary-user.
    report.windowsDiagnostic = {};
    try {
      const bytes = await readFile(join(app, "fased-app.task.xml"));
      const xml =
        bytes[0] === 0xff && bytes[1] === 0xfe
          ? bytes.subarray(2).toString("utf16le")
          : bytes.toString("utf8");
      const sid = xml.match(/<UserId>(S-[0-9-]+)<\/UserId>/)?.[1];
      if (!sid) throw new Error("no declared fixture task principal");
      report.windowsDiagnostic.task = execFileSync(
        "schtasks.exe",
        ["/Query", "/TN", "FasedApp-" + sid, "/V", "/FO", "LIST"],
        { encoding: "utf8", timeout: 10000, maxBuffer: 64 * 1024 },
      ).slice(0, 8192);
    } catch (diagnostic) {
      report.windowsDiagnostic.taskError = String(diagnostic);
    }
    // A separate generated data directory distinguishes a scheduler problem
    // from a core initialization failure without touching the failed instance.
    // execFileSync terminates this single foreground process at the deadline.
    try {
      report.windowsDiagnostic.directCore = execFileSync(
        binary,
        [
          "serve",
          "--data-dir",
          join(root, "diagnostic-state"),
          "--listen",
          "127.0.0.1:0",
        ],
        { encoding: "utf8", timeout: 8000, maxBuffer: 64 * 1024 },
      );
    } catch (diagnostic) {
      report.windowsDiagnostic.directCore = {
        code: diagnostic.code,
        status: diagnostic.status,
        signal: diagnostic.signal,
        stdout: String(diagnostic.stdout || "").slice(0, 8192),
        stderr: String(diagnostic.stderr || "").slice(0, 8192),
      };
    }
    console.error(
      "WINDOWS FIXTURE DIAGNOSTIC",
      JSON.stringify(report.windowsDiagnostic),
    );
  }
  if (process.platform === "linux") {
    try {
      const fragment = execFileSync(
        "systemctl",
        [
          "--user",
          "show",
          "fased-app.service",
          "--property=FragmentPath",
          "--value",
        ],
        { encoding: "utf8" },
      ).trim();
      report.serviceFragment = fragment;
      if (
        fragment === join(app, "fased-app.service") ||
        String(error).includes("core did not become ready")
      ) {
        report.serviceDiagnostic = execFileSync(
          "journalctl",
          [
            "--user",
            "-u",
            "fased-app.service",
            "--since",
            `@${Math.floor(startedAt / 1000)}`,
            "--no-pager",
            "-n",
            "30",
          ],
          { encoding: "utf8" },
        );
      }
    } catch (diagnostic) {
      report.diagnosticError = String(diagnostic);
    }
  }
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
