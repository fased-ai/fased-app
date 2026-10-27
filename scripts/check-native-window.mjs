import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import { once } from "node:events";

if (process.platform !== "linux")
  throw new Error("Native window smoke uses the declared Linux/Xvfb fixture");

const fixture = resolve(".evidence/native-smoke");
const state = join(fixture, "state");
await mkdir(fixture, { recursive: true, mode: 0o700 });
await writeFile(
  join(fixture, "fixture.json"),
  JSON.stringify({ nonauthoritative: true }),
  { mode: 0o600 },
);
let output = "";
let smoke;
const core = spawn(resolve(".evidence/bin/fased-app"), [
  "serve",
  "--data-dir",
  state,
  "--listen",
  "127.0.0.1:0",
]);
core.stdout.on("data", (data) => (output += String(data)));
const pause = (ms) => new Promise((done) => setTimeout(done, ms));
try {
  for (let i = 0; i < 100 && !output.includes('"event":"listening"'); i++)
    await pause(100);
  assert.ok(
    output.includes('"event":"listening"'),
    "fixture core did not start",
  );
  const { url } = JSON.parse(output.trim().split("\n")[0]);
  const initial = await (await fetch(url + "/api/v1/health")).json();
  assert.equal(initial.capabilities.walletSigner, false);
  const result = await new Promise((done, reject) => {
    let stdout = "",
      stderr = "";
    smoke = spawn(
      "xvfb-run",
      ["-a", resolve(".evidence/bin/fased-native-smoke")],
      {
        detached: true,
        env: {
          ...process.env,
          FASED_NATIVE_SMOKE_DATA: state,
          WEBKIT_DISABLE_DMABUF_RENDERER: "1",
        },
      },
    );
    smoke.stdout.on("data", (data) => (stdout += String(data)));
    smoke.stderr.on("data", (data) => (stderr += String(data)));
    smoke.on("error", reject);
    const timer = setTimeout(() => {
      try {
        process.kill(-smoke.pid, "SIGTERM");
      } catch {}
      reject(new Error("Native window timed out"));
    }, 45000);
    smoke.on("exit", (code) => {
      clearTimeout(timer);
      code === 0
        ? done({ stdout, stderr })
        : reject(new Error(`Native window failed: ${stdout}\n${stderr}`));
    });
  });
  assert.match(result.stdout, /"ok":true/);
  const after = await (await fetch(url + "/api/v1/health")).json();
  assert.equal(after.instanceId, initial.instanceId);
  await writeFile(
    ".evidence/native-window-result.json",
    JSON.stringify(
      {
        result: "PASS",
        nativeOutput: result.stdout.trim(),
        closeWindowKeepsSeparateCore: true,
      },
      null,
      2,
    ) + "\n",
  );
} catch (error) {
  await writeFile(
    ".evidence/native-window-result.json",
    JSON.stringify({ result: "FAIL", error: error.message }, null, 2) + "\n",
  );
  throw error;
} finally {
  if (smoke?.pid)
    try {
      process.kill(-smoke.pid, "SIGTERM");
    } catch {}
  if (core.exitCode === null) {
    const stopped = once(core, "exit");
    core.kill("SIGTERM");
    await stopped;
  }
  await rm(fixture, { recursive: true, force: true });
}
