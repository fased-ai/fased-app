import test from "node:test";
import assert from "node:assert/strict";
import { renderEntry } from "./render-install-entry.mjs";
import { spawnSync } from "node:child_process";
import { readFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const template = readFileSync(
  new URL("../install.sh", import.meta.url),
  "utf8",
);
test("entry refuses missing trust and uncontrolled download roots", () => {
  assert.throws(() => renderEntry(template, {}));
  assert.throws(() =>
    renderEntry(template, {
      version: "1.0.0",
      installer: new Uint8Array([1]),
      seed: Buffer.from("{}"),
      url: "http://attacker.invalid",
    }),
  );
});

const seed = Buffer.from(
  JSON.stringify({
    version: 3,
    repository: "fased-ai/fased-app",
    target: { os: "linux", arch: "amd64" },
    developmentOnly: false,
    channel: "stable",
    provenance: {},
  }),
);
const config = {
  version: "1.0.0",
  installer: Buffer.from("disposable verifier bytes"),
  seed,
};
test("public entry pins exact GitHub verifier and public trust; no runtime bypass", () => {
  const script = renderEntry(template, config);
  assert.match(
    script,
    /https:\/\/github.com\/fased-ai\/fased-app\/releases\/download\/v1.0.0\/fased-install-linux-amd64/,
  );
  assert.ok(!script.includes("@INSTALLER_SHA256@"));
  assert.equal(spawnSync("bash", ["-n"], { input: script }).status, 0);
  for (const flag of [
    "--url",
    "--insecure",
    "--skip-verify",
    "--development",
  ]) {
    const r = spawnSync("bash", ["-c", script, "entry", flag]);
    assert.equal(r.status, 1);
    assert.match(r.stderr.toString(), /Unknown option/);
  }
  assert.throws(() =>
    renderEntry(template, { ...config, ca: Buffer.from("untrusted CA") }),
  );
});
test("unstamped source cannot modify a host", () => {
  const r = spawnSync("bash", ["-c", template, "entry", "--remote"]);
  assert.equal(r.status, 1);
  assert.match(r.stderr.toString(), /No trusted installer release/);
});
test("controlled fixture has explicit isolated trust", () => {
  const fixtureSeed = Buffer.from(
    JSON.stringify({
      version: 1,
      repository: "fased-ai/fased-app",
      target: { os: "linux", arch: "amd64" },
      developmentOnly: true,
    }),
  );
  const fixture = {
    ...config,
    seed: fixtureSeed,
    fixture: true,
    url: "https://127.0.0.1:24443/fased-install",
    ca: Buffer.from("disposable public CA"),
  };
  const script = renderEntry(template, fixture);
  assert.equal(spawnSync("bash", ["-n"], { input: script }).status, 0);
  assert.throws(() =>
    renderEntry(template, {
      ...fixture,
      url: "https://outside.example/fased-install",
    }),
  );
  assert.throws(() => renderEntry(template, { ...fixture, fixture: false }));
});

test("root receipts reject malformed phases and preserve managed lineage", () => {
  const declaration = template.match(/^operator_record\(\).*$/m)[0];
  for (const record of [
    "pending:" + "a".repeat(32),
    "ready:1001:1001:external",
    "ready:1002:1002:" + "b".repeat(32),
  ])
    assert.equal(
      spawnSync("bash", [
        "-c",
        declaration + '; operator_record "$1"',
        "record",
        record,
      ]).status,
      0,
    );
  for (const record of [
    "unknown:1001:1001",
    "ready:0:0:external",
    "ready:1001:1001",
    "pending:bad",
    "ready:1001:1001:external:extra",
    "ready:1001:1001:external\nforeign",
  ])
    assert.equal(
      spawnSync("bash", [
        "-c",
        declaration + '; operator_record "$1"',
        "record",
        record,
      ]).status,
      1,
    );
});

// The owner inherits root's working directory after runuser. Its first operations
// must enter the owner's home before cleanup/find tries to restore that directory.
test("ordinary phase leaves the administrator working directory", () => {
  const home = mkdtempSync(join(tmpdir(), "fased-entry-home-"));
  try {
    const prefix = template
      .split("owner_install() {\n")[1]
      .split("  umask 077")[0];
    const r = spawnSync("bash", ["-c", prefix + "pwd"], {
      cwd: "/",
      env: { ...process.env, HOME: home },
    });
    assert.equal(r.status, 0, r.stderr.toString());
    assert.equal(r.stdout.toString().trim(), home);
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

test("one release selects authenticated amd64 and arm64 tuples with update/local menu", () => {
  const armSeed = Buffer.from(
    JSON.stringify({
      ...JSON.parse(seed.toString()),
      target: { os: "linux", arch: "arm64" },
    }),
  );
  const script = renderEntry(template, {
    version: "1.0.0",
    targets: [
      config,
      { ...config, seed: armSeed, installer: Buffer.from("arm verifier") },
    ],
  });
  for (const value of [
    "linux-amd64)",
    "linux-arm64)",
    "ubuntu:22.04)",
    "ubuntu:24.04)",
    "ubuntu:26.04)",
    "ubuntu:26.10)",
    "--local)",
    "--update|--setup)",
    "  Update",
  ]) {
    assert.ok(script.includes(value), value);
  }
  assert.equal(spawnSync("bash", ["-n"], { input: script }).status, 0);
  assert.throws(() =>
    renderEntry(template, { version: "1.0.0", targets: [config, config] }),
  );
  assert.throws(() =>
    renderEntry(template, {
      version: "1.0.0",
      targets: [config, { ...config, version: "2.0.0", seed: armSeed }],
    }),
  );
});
