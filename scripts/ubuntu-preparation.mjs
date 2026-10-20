// Privileged OS-fixture checks are restricted to a disposable GitHub Ubuntu
// runner. They never run on the owner's Fedora machine or an ordinary install.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { readFile, mkdir, stat, lstat, writeFile } from "node:fs/promises";
import { watch } from "node:fs";
import { join } from "node:path";
import { createHash } from "node:crypto";
const target = "/etc/apparmor.d/fased-app-bwrap-v1";
const sha = "11d39094f044f0cda0febb3ad517b830301da6b2ce929664af09ee9e4dd264f9";
const hash = (b) => createHash("sha256").update(b).digest("hex");
const os = (args, input) =>
  execFileSync("/usr/bin/sudo", ["-n", "--", ...args], {
    input,
    encoding: "utf8",
    timeout: 30000,
  });
export async function checkUbuntuPreparation(binary, args, state, report) {
  assert.equal(process.env.GITHUB_ACTIONS, "true");
  assert.equal(process.platform, "linux");
  assert.notEqual(process.getuid(), 0);
  assert.match(await readFile("/etc/os-release", "utf8"), /VERSION_ID="24.04"/);
  await assert.rejects(lstat(target), { code: "ENOENT" });
  const foreign = Buffer.from(
    "# Fased acceptance fixture: foreign administrator policy\n",
  );
  // Node's spawnSync stdin is a socket on Linux: tee reads fd 0 directly,
  // unlike reopening /dev/stdin. The Go product uses an actual os.Pipe.
  os(["/usr/bin/tee", "--", target], foreign);
  try {
    let denied = false;
    try {
      execFileSync(binary, [...args, "--prepare-host"], {
        encoding: "utf8",
        timeout: 30000,
        stdio: ["ignore", "pipe", "pipe"],
      });
    } catch (e) {
      assert.match(String(e.stderr), /foreign or changed/);
      denied = true;
    }
    assert.ok(denied);
    assert.deepEqual(await readFile(target), foreign);
    report.checks.foreignPolicyPreserved = "PASS";
  } finally {
    assert.deepEqual(await readFile(target), foreign);
    os(["/usr/bin/rm", "--", target]);
  }
  let denied = false;
  try {
    execFileSync(binary, args, {
      encoding: "utf8",
      timeout: 30000,
      stdio: ["ignore", "pipe", "pipe"],
    });
  } catch (e) {
    assert.match(String(e.stderr), /bubblewrap package|sandbox prerequisite/);
    denied = true;
  }
  assert.ok(denied, "missing host prerequisites were silently accepted");
  await mkdir(state, { recursive: true, mode: 0o700 });
  const child = spawn(binary, [...args, "--prepare-host"], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  let output = "",
    killed = false,
    checking = false;
  child.stdout.on("data", (b) => (output += String(b)));
  child.stderr.on("data", (b) => (output += String(b)));
  const watcher = watch(state, async () => {
    if (checking || killed) return;
    checking = true;
    try {
      const r = JSON.parse(
        await readFile(join(state, "host-preparation.json"), "utf8"),
      );
      if (r.phase === "published") {
        killed = child.kill("SIGKILL");
      }
    } catch {
    } finally {
      checking = false;
    }
  });
  const timeout = setTimeout(() => child.kill("SIGKILL"), 8 * 60 * 1000);
  let code;
  try {
    code = await new Promise((resolve, reject) => {
      child.on("exit", (c, s) => resolve([c, s]));
      child.on("error", reject);
    });
  } finally {
    clearTimeout(timeout);
    watcher.close();
    await writeFile(".evidence/ubuntu-preparation-interruption.log", output, {
      mode: 0o600,
    });
  }
  assert.ok(
    killed,
    "did not observe real policy-publication interruption: " +
      JSON.stringify(code) +
      " " +
      output.slice(-1200),
  );
  // The trusted OS loader may finish after its unprivileged parent died.
  await new Promise((r) => setTimeout(r, 500));
  execFileSync(binary, [...args, "--prepare-host"], {
    encoding: "utf8",
    timeout: 60000,
    stdio: ["ignore", "pipe", "pipe"],
  });
  const receipt = JSON.parse(
    await readFile(join(state, "host-preparation.json"), "utf8"),
  );
  assert.equal(receipt.phase, "ready");
  assert.equal(hash(await readFile(target)), sha);
  assert.equal((await lstat(target)).uid, 0);
  assert.equal((await lstat(target)).mode & 0o022, 0);
  const before = (await stat(target)).mtimeMs;
  execFileSync(binary, [...args, "--prepare-host"], {
    encoding: "utf8",
    timeout: 30000,
    stdio: ["ignore", "pipe", "pipe"],
  });
  assert.equal((await stat(target)).mtimeMs, before);
  assert.throws(() =>
    execFileSync(
      "/usr/bin/bwrap",
      [
        "--unshare-user",
        "--disable-userns",
        "--cap-drop",
        "ALL",
        "--ro-bind",
        "/",
        "/",
        "--",
        "/usr/bin/unshare",
        "--user",
        "/usr/bin/true",
      ],
      { stdio: "ignore" },
    ),
  );
  report.checks.automaticUbuntuPreparation =
    "PASS: missing OS runtime, policy setup, real interruption/retry, no-rewrite retry, nested namespace refusal";
}
export async function cleanupUbuntuPreparation() {
  assert.equal(process.env.GITHUB_ACTIONS, "true");
  const active = execFileSync(
    "systemctl",
    [
      "--user",
      "show",
      "fased-app.service",
      "--property=ActiveState",
      "--value",
    ],
    { encoding: "utf8" },
  ).trim();
  assert.notEqual(
    active,
    "active",
    "owned core must stop before sandbox-policy cleanup",
  );
  let b;
  try {
    b = await readFile(target);
  } catch (e) {
    if (e.code === "ENOENT") return;
    throw e;
  }
  assert.equal(hash(b), sha, "changed policy preserved for inspection");
  os(["/usr/sbin/apparmor_parser", "-R", target]);
  os(["/usr/bin/rm", "--", target]);
}
