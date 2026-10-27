// Explicit owner-authorized, ordinary-user service acceptance on this host.
// Never points at an existing owner installation or financial data.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { randomBytes, createHash } from "node:crypto";
import { readFile, writeFile, readdir, rm, open } from "node:fs/promises";
import { resolve, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

if (
  process.env.FASED_LOCAL_ACCEPTANCE !== "1" ||
  process.platform !== "linux" ||
  process.getuid() === 0
)
  throw new Error(
    "Requires explicit FASED_LOCAL_ACCEPTANCE=1 as an ordinary Linux user",
  );
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const live = join(root, ".evidence/live");
const app = join(live, "app");
const state = join(live, "state");
const binary = join(root, ".evidence/bin/fased-app");
const flags = ["--prefix", app, "--data-dir", state];
const report = {
  host: "ordinary-user Linux",
  uid: process.getuid(),
  result: "FAIL",
  checks: {},
};
const cli = (args, input) =>
  execFileSync(binary, args, { input, encoding: "utf8", timeout: 35000 });
const service = () => JSON.parse(cli(["service", "status", ...flags]));
const setup = () => JSON.parse(cli(["setup", "--data-dir", state]));
let browser;
let owned = false;
let uninstalled = false;
async function snapshot(dir) {
  const entries = {};
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (!entry.isFile()) continue;
    entries[entry.name] = createHash("sha256")
      .update(await readFile(join(dir, entry.name)))
      .digest("hex");
  }
  return entries;
}
async function canWriteOpen(pid, path) {
  try {
    const fd = await open(`/proc/${pid}/root${path}`, "r+");
    await fd.close();
    return true;
  } catch (error) {
    if (error.code === "EROFS") return false;
    throw error;
  }
}
try {
  const manifest = JSON.parse(
    await readFile(join(app, "install.json"), "utf8"),
  );
  assert.equal(manifest.root, app);
  assert.equal(manifest.data, state);
  assert.equal(manifest.phase, "ready");
  let current = service();
  assert.equal(current.active, true);
  assert.equal(current.autostart, false);
  const auth = await (await fetch(current.url + "/api/v1/auth/state")).json();
  assert.equal(
    auth.claimed,
    false,
    "Refusing already-claimed state; this check uses only fresh generated fixtures",
  );
  await writeFile(
    join(live, "fixture.json"),
    JSON.stringify({
      nonauthoritative: true,
      app,
      state,
      createdAt: new Date().toISOString(),
    }),
    { mode: 0o600 },
  );
  owned = true;
  report.binarySHA256 = createHash("sha256")
    .update(await readFile(binary))
    .digest("hex");
  const health = await (await fetch(current.url + "/api/v1/health")).json();
  report.instanceId = health.instanceId;
  assert.equal(health.capabilities.walletSigner, false);
  assert.equal(health.capabilities.financialExecution, false);
  const pid = Number(
    execFileSync(
      "systemctl",
      ["--user", "show", "fased-app.service", "--property=MainPID", "--value"],
      { encoding: "utf8" },
    ).trim(),
  );
  const processStatus = await readFile(`/proc/${pid}/status`, "utf8");
  assert.match(
    processStatus,
    new RegExp(`^Uid:\\s+${process.getuid()}\\s`, "m"),
  );
  assert.match(processStatus, /^NoNewPrivs:\s+1$/m);
  assert.equal(await canWriteOpen(pid, join(app, "fased-app.service")), false);
  assert.equal(await canWriteOpen(pid, join(state, "core.sqlite")), true);
  report.checks.actualServiceIsolation =
    "PASS: current UID, NoNewPrivs, read-only application mount and writable private state";
  const password = "FixtureOnly-" + randomBytes(24).toString("base64url");
  cli(["claim-code", "--data-dir", state]); // Captured, never printed or sent to a journal.
  const code = JSON.parse(
    await readFile(join(state, "claim-code.json"), "utf8"),
  ).code;
  browser = await chromium.launch({
    headless: true,
    executablePath: process.env.FASED_TEST_BROWSER || undefined,
  });
  const page = await browser.newPage({
    viewport: { width: 1440, height: 940 },
  });
  await page.goto(current.url);
  await page.getByLabel("One-use claim code").fill(code);
  await page.getByLabel("Create owner password").fill(password);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await page.getByRole("button", { name: "Sign out", exact: true }).waitFor();
  await page.screenshot({
    path: join(root, ".evidence/installed-overview.png"),
    fullPage: true,
  });
  cli(["login", "--data-dir", state, "--password-stdin"], password + "\n");
  cli([
    "setup",
    "--data-dir",
    state,
    "--host",
    "vps",
    "--wallet-mode",
    "watch",
  ]);
  await page.reload();
  await page
    .getByRole("button", { name: "Set up workspace", exact: true })
    .click();
  assert.equal(
    await page
      .getByRole("button", { name: /Private VPS Always-on/ })
      .getAttribute("aria-pressed"),
    "true",
  );
  await browser.close();
  browser = undefined;
  assert.equal(service().active, true);
  report.checks.browserCloseKeepsEngine = "PASS";
  const journal = execFileSync(
    "journalctl",
    ["--user", "-u", "fased-app.service", "--no-pager", "-n", "50"],
    { encoding: "utf8" },
  );
  assert.equal(journal.includes(password), false);
  assert.equal(journal.includes(code), false);
  report.checks.secretRedaction = "PASS";
  const saved = setup();
  cli(["service", "stop", ...flags]);
  assert.equal(service().active, false);
  cli(["service", "start", ...flags]);
  assert.deepEqual(setup(), saved);
  current = service();
  assert.equal(
    (await (await fetch(current.url + "/api/v1/health")).json()).instanceId,
    health.instanceId,
  );
  report.checks.restartPersistence = "PASS";
  cli(["service", "stop", ...flags]);
  const before = await snapshot(state);
  await writeFile(
    join(app, "operator-note.txt"),
    "generated unknown-file preservation fixture",
    { mode: 0o600 },
  );
  cli(["uninstall", ...flags]);
  uninstalled = true;
  assert.deepEqual(await snapshot(state), before);
  assert.equal(
    await readFile(join(app, "operator-note.txt"), "utf8"),
    "generated unknown-file preservation fixture",
  );
  const unit = execFileSync(
    "systemctl",
    ["--user", "show", "fased-app.service", "--property=LoadState,ActiveState"],
    { encoding: "utf8" },
  );
  assert.match(unit, /LoadState=not-found/);
  assert.match(unit, /ActiveState=inactive/);
  report.checks.uninstallPreservesDataAndUnknownFiles = "PASS";
  report.preservedStateHashes = before;
  report.result = "PASS";
} catch (error) {
  report.error = error.message;
  throw error;
} finally {
  await browser?.close();
  if (owned && !uninstalled) {
    try {
      cli(["uninstall", ...flags]);
      uninstalled = true;
    } catch {
      report.cleanup =
        "Retained fixture and service receipt for recovery; do not delete while service ownership is unresolved";
    }
  }
  await writeFile(
    join(root, ".evidence/local-install-result.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  if (report.result === "PASS" && uninstalled)
    await rm(live, { recursive: true, force: true });
  console.log(
    JSON.stringify({
      result: report.result,
      checks: report.checks,
      cleanup: uninstalled
        ? report.result === "PASS"
          ? "owned service removed; state preservation proved before fixture cleanup"
          : "owned service removed; failed fixture retained for recovery"
        : "retained for recovery",
    }),
  );
}
