// Actual packaged desktop -> native consent dialog -> OS terminal -> CLI host
// preparation -> background core. Generated home is disposable CI state only.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdir, readFile, writeFile, rm, stat } from "node:fs/promises";
import { join, resolve } from "node:path";
import { cleanupUbuntuPreparation } from "./ubuntu-preparation.mjs";
assert.equal(process.env.GITHUB_ACTIONS, "true");
assert.equal(process.platform, "linux");
assert.notEqual(process.getuid(), 0);
assert.ok(process.env.DISPLAY);
const root = resolve(".evidence/native-first-launch");
await mkdir(root, { mode: 0o700 });
const home = join(root, "home"),
  config = join(home, ".config"),
  app = join(home, ".local", "share", "fased-app"),
  data = join(config, "fased-app"),
  unpack = join(root, "unpacked");
await mkdir(home, { mode: 0o700 });
await mkdir(config, { mode: 0o700 });
await mkdir(unpack, { mode: 0o700 });
const archive = resolve(
  `.evidence/packages/fased-app-linux-${process.arch === "x64" ? "amd64" : process.arch}.tar.gz`,
);
execFileSync("tar", ["-xf", archive, "-C", unpack]);
const payload = join(
    unpack,
    `fased-app-linux-${process.arch === "x64" ? "amd64" : process.arch}`,
  ),
  core = join(payload, "fased-app"),
  gui = join(payload, "fased-desktop");
const env = {
  ...process.env,
  HOME: home,
  XDG_CONFIG_HOME: config,
  LANG: "C.UTF-8",
  LC_ALL: "C.UTF-8",
  WEBKIT_DISABLE_DMABUF_RENDERER: "1",
};
const cli = (args) =>
  execFileSync(core, args, { env, encoding: "utf8", timeout: 30000 });
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
const report = {
  result: "FAIL",
  class:
    "real Ubuntu packaged first launch with CI sudo authorization; generated HOME/XDG_CONFIG_HOME",
  checks: {},
};
let child,
  childExited,
  childDone = false,
  output = "";
async function stopClient() {
  if (!child || childDone) return;
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
  try {
    await childExited;
  } finally {
    clearTimeout(timer);
  }
}
function windows(name) {
  try {
    return execFileSync(
      "xdotool",
      ["search", "--onlyvisible", "--name", name],
      { env, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] },
    )
      .trim()
      .split(/\s+/)
      .filter(Boolean);
  } catch {
    return [];
  }
}
try {
  // Runtime libraries are present after the preceding automatic CLI check;
  // remove only that check's verified owned profile to force GUI preparation.
  await cleanupUbuntuPreparation();
  child = spawn(gui, [], { env, stdio: ["ignore", "pipe", "pipe"] });
  childExited = new Promise((resolve) => {
    child.once("exit", () => {
      childDone = true;
      resolve();
    });
    child.once("error", (e) => {
      childDone = true;
      output += String(e);
      resolve();
    });
  });
  child.stdout.on("data", (b) => (output += String(b)));
  child.stderr.on("data", (b) => (output += String(b)));
  let dialog;
  for (let i = 0; i < 150; i++) {
    dialog = windows("Set up Fased")[0];
    if (dialog) break;
    if (childDone) throw new Error("desktop exited before consent: " + output);
    await pause(100);
  }
  assert.ok(dialog, "native installation consent dialog missing: " + output);
  execFileSync("scrot", [resolve(".evidence/native-first-consent.png")], {
    env,
  });
  execFileSync("xdotool", ["windowfocus", "--sync", dialog], { env });
  execFileSync("xdotool", ["key", "--window", dialog, "alt+y"], { env });
  let status;
  for (let i = 0; i < 1800; i++) {
    try {
      status = JSON.parse(cli(["status", "--data-dir", data]));
      break;
    } catch {}
    if (childDone)
      throw new Error("desktop exited during preparation: " + output);
    await pause(100);
  }
  assert.ok(
    status?.instanceId,
    "terminal preparation did not start core: " + output,
  );
  const receipt = JSON.parse(
    await readFile(join(data, "host-preparation.json"), "utf8"),
  );
  assert.equal(receipt.phase, "ready");
  const installed = JSON.parse(
    await readFile(join(app, "install.json"), "utf8"),
  );
  assert.equal(installed.autostart, false);
  await pause(1200);
  execFileSync("scrot", [resolve(".evidence/native-first-launch.png")], {
    env,
  });
  await stopClient();
  assert.equal(
    JSON.parse(cli(["status", "--data-dir", data])).instanceId,
    status.instanceId,
  );
  report.result = "PASS";
  report.instanceId = status.instanceId;
  report.checks = {
    nativeConsentDialog: "PASS",
    systemTerminalHostPreparation: "PASS",
    backgroundCoreAfterClientExit: "PASS",
    loginAutostartRemainsOff: "PASS",
  };
} catch (e) {
  report.error = String(e);
  throw e;
} finally {
  if (report.result !== "PASS") {
    try {
      execFileSync("scrot", [resolve(".evidence/native-first-launch.png")], {
        env,
      });
    } catch {}
    for (const file of [
      "host-preparation.json",
      "../.local/share/fased-app/install.json",
    ]) {
      try {
        report[file] = JSON.parse(await readFile(join(data, file), "utf8"));
      } catch {}
    }
  }
  await stopClient();
  try {
    await stat(join(app, "install.json"));
    cli(["uninstall", "--prefix", app, "--data-dir", data]);
  } catch (e) {
    if (e.code !== "ENOENT") report.cleanupError = String(e);
  }
  await writeFile(
    ".evidence/native-first-launch-result.json",
    JSON.stringify(report, null, 2) + "\n",
  );
  await writeFile(".evidence/native-first-launch.log", output, { mode: 0o600 });
  if (!report.cleanupError) await rm(root, { recursive: true });
}
