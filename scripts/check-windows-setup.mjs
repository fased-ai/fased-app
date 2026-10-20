// Actual unsigned Inno setup/uninstall, with generated profile paths on the
// disposable Windows runner. This does not prove Windows 11 normal-user/UAC.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, readFile, writeFile, rm, stat } from "node:fs/promises";
import { join, resolve } from "node:path";
import { randomBytes, createHash } from "node:crypto";
assert.equal(process.env.GITHUB_ACTIONS, "true");
assert.equal(process.platform, "win32");
const root = resolve(".evidence/windows-setup");
await mkdir(root);
const payload = join(root, "payload"),
  home = join(root, "profile"),
  roaming = join(root, "roaming"),
  data = join(roaming, "fased-app"),
  app = join(home, "AppData", "Local", "Fased App", "app");
await mkdir(home);
await mkdir(roaming);
const env = { ...process.env, USERPROFILE: home, APPDATA: roaming };
const setup = resolve(
  ".evidence/packages/fased-app-windows-setup-unsigned.exe",
);
const uninstaller = join(payload, "unins000.exe");
const binary = join(payload, "fased-app.exe");
const cli = (args, input) =>
  execFileSync(binary, args, { env, input, encoding: "utf8", timeout: 40000 });
const report = {
  result: "FAIL",
  class:
    "Windows Server actual unsigned setup, generated profile paths; normal-user Windows 11 remains unproven",
  checks: {},
};
let installed = false;
try {
  execFileSync(
    setup,
    [
      "/VERYSILENT",
      "/SUPPRESSMSGBOXES",
      "/NORESTART",
      `/DIR=${payload}`,
      `/LOG=${join(root, "install.log")}`,
    ],
    { env, timeout: 120000 },
  );
  installed = true;
  const state = JSON.parse(cli(["service", "status"]));
  assert.equal(state.active, true);
  const instance = JSON.parse(cli(["status"]));
  const pw = "Fixture-" + randomBytes(24).toString("hex");
  const code = JSON.parse(
    await readFile(join(data, "claim-code.json"), "utf8"),
  ).code;
  // Claim through the same loopback API the native/browser client uses.
  const url = state.url;
  const response = await fetch(url + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: url, "Content-Type": "application/json" },
    body: JSON.stringify({ code, password: pw }),
  });
  assert.equal(response.status, 200);
  cli(["login", "--password-stdin"], pw + "\n");
  cli(["setup", "--host", "local"]);
  const expected = JSON.parse(cli(["setup"]));
  await writeFile(join(data, "keep-owner-note"), "preserve instance data");
  cli(["service", "stop"]);
  const manifest = JSON.parse(
    await readFile(join(app, "install.json"), "utf8"),
  );
  const name = Object.keys(manifest.files).find((n) => n.endsWith(".xml"));
  assert.ok(name);
  const managedBinary = join(app, name);
  const original = await readFile(managedBinary);
  try {
    await writeFile(
      managedBinary,
      Buffer.concat([original, Buffer.from("changed fixture inventory")]),
    );
    assert.throws(() =>
      execFileSync(
        uninstaller,
        ["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
        { env, timeout: 30000 },
      ),
    );
    assert.ok((await stat(uninstaller)).isFile());
    assert.ok((await stat(join(app, "install.json"))).isFile());
    report.checks.foreignInventoryAbortsUninstall = "PASS";
  } finally {
    await writeFile(managedBinary, original);
  }
  const before = {};
  for (const name of ["core.sqlite", "keep-owner-note"]) {
    before[name] = createHash("sha256")
      .update(await readFile(join(data, name)))
      .digest("hex");
  }
  execFileSync(
    uninstaller,
    ["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
    { env, timeout: 120000 },
  );
  installed = false;
  for (const [name, hash] of Object.entries(before))
    assert.equal(
      createHash("sha256")
        .update(await readFile(join(data, name)))
        .digest("hex"),
      hash,
    );
  await assert.rejects(stat(join(app, "install.json")), { code: "ENOENT" });
  report.result = "PASS";
  report.instanceId = instance.instanceId;
  Object.assign(report.checks, {
    unsignedSetupStartsCore: "PASS",
    sameCLISetup: expected.host,
    uninstallPreservesStoppedState: "PASS",
    managedInstallationRemoved: "PASS",
  });
} catch (e) {
  report.error = String(e);
  throw e;
} finally {
  if (installed) {
    try {
      execFileSync(
        uninstaller,
        ["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
        { env, timeout: 120000 },
      );
    } catch (e) {
      report.cleanupError = String(e);
    }
  }
  await writeFile(
    ".evidence/windows-setup-result.json",
    JSON.stringify(report, null, 2) + "\n",
  );
  if (!report.cleanupError) await rm(root, { recursive: true });
}
