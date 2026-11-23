import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  LINUX_PLATFORM_FILES,
  buildBundleManifest,
  buildDesktopReleaseManifest,
} from "./build-dual-track-cli.mjs";
import {
  renderDualTrackCliEntry,
  renderWindowsEntry,
} from "./render-install-entry.mjs";

const sha256 = (buf) => createHash("sha256").update(buf).digest("hex");

test("buildBundleManifest emits valid Schema-1 bundle inventory per platform", () => {
  const linux = buildBundleManifest({
    platform: "linux",
    arch: "amd64",
    version: "0.1.37",
    source: "a".repeat(40),
    files: {
      "fased-app": { sha256: "b".repeat(64), bytes: 100 },
      "fased-signer": { sha256: "c".repeat(64), bytes: 200 },
      LICENSE: { sha256: "d".repeat(64), bytes: 10 },
      NOTICE: { sha256: "e".repeat(64), bytes: 10 },
      "bwrap-userns-restrict": { sha256: "f".repeat(64), bytes: 10 },
      "APPARMOR-COPYING": { sha256: "1".repeat(64), bytes: 10 },
      "APPARMOR-COPYRIGHT": { sha256: "2".repeat(64), bytes: 10 },
      "APPARMOR-SOURCE.md": { sha256: "3".repeat(64), bytes: 10 },
    },
  });
  assert.equal(linux.schema, 1);
  assert.equal(linux.desktop, false);
  assert.equal(linux.unsignedEngineering, true);
  assert.equal(Object.keys(LINUX_PLATFORM_FILES).length, 4);
  for (const key of Object.keys(LINUX_PLATFORM_FILES)) {
    assert.ok(key in linux.files, `missing ${key}`);
  }
});

test("rendered install.ps1 pins desktop release URLs and SHA-256s for amd64 and arm64", () => {
  const template = readFileSync(
    new URL("../install.ps1", import.meta.url),
    "utf8",
  );
  const amd64Archive = Buffer.from("windows-amd64-zip-bytes");
  const arm64Archive = Buffer.from("windows-arm64-zip-bytes");
  const rendered = renderWindowsEntry(template, {
    version: "0.1.37-desktop",
    targets: [
      { arch: "amd64", archive: amd64Archive },
      { arch: "arm64", archive: arm64Archive },
    ],
  });
  assert.equal(/@[A-Z0-9_]+@/.test(rendered), false);
  assert.match(rendered, /\$EntryVersion = '0\.1\.37-desktop'/);
  assert.ok(
    rendered.includes(
      "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/fased-app-windows-amd64.zip",
    ),
  );
  assert.ok(
    rendered.includes(
      "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/fased-app-windows-arm64.zip",
    ),
  );
  assert.ok(rendered.includes(sha256(amd64Archive)));
  assert.ok(rendered.includes(sha256(arm64Archive)));
  assert.match(rendered, /function Invoke-DetachedUninstall/);
  assert.match(
    rendered,
    /Invoke-DetachedUninstall -BinaryPath \$AppBin -Arguments @\('uninstall'\)/,
  );
  assert.match(rendered, /Remove-Item -LiteralPath \$stageDir -Recurse -Force/);
});

test("rendered install-cli.sh passes syntax checks, pins 4 Unix targets, and rejects tampering/bad options", () => {
  const template = readFileSync(
    new URL("../install-cli.sh", import.meta.url),
    "utf8",
  );
  // Unstamped template must fail closed.
  const unstamped = spawnSync("bash", [
    "-c",
    template,
    "install-cli.sh",
    "--local",
  ]);
  assert.equal(unstamped.status, 1);
  assert.match(unstamped.stderr.toString(), /No trusted installer release/);

  const samples = [
    { os: "darwin", arch: "arm64", archive: Buffer.from("darwin-arm64-zip") },
    { os: "darwin", arch: "amd64", archive: Buffer.from("darwin-amd64-zip") },
    { os: "linux", arch: "amd64", archive: Buffer.from("linux-amd64-tgz") },
    { os: "linux", arch: "arm64", archive: Buffer.from("linux-arm64-tgz") },
  ];
  const rendered = renderDualTrackCliEntry(template, {
    version: "0.1.37-desktop",
    targets: samples,
  });
  assert.equal(/@[A-Z0-9_]+@/.test(rendered), false);

  const dir = mkdtempSync(path.join(tmpdir(), "fased-cli-entry-"));
  try {
    const file = path.join(dir, "install-cli.sh");
    writeFileSync(file, rendered);
    execFileSync("bash", ["-n", file]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }

  for (const s of samples) {
    const ext = s.os === "darwin" ? "zip" : "tar.gz";
    assert.ok(
      rendered.includes(
        `https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/fased-app-${s.os}-${s.arch}.${ext}`,
      ),
    );
    assert.ok(rendered.includes(sha256(s.archive)));
  }

  for (const flag of ["--url", "--insecure", "--skip-verify", "--remote"]) {
    const r = spawnSync("bash", ["-c", rendered, "install-cli.sh", flag]);
    assert.equal(r.status, 1);
  }
  const serverNoAuto = spawnSync("bash", [
    "-c",
    rendered,
    "install-cli.sh",
    "--server",
    "--no-autostart",
  ]);
  assert.equal(serverNoAuto.status, 1);
  assert.match(serverNoAuto.stderr.toString(), /--no-autostart/);
  const statusNoAuto = spawnSync("bash", [
    "-c",
    rendered,
    "install-cli.sh",
    "--local",
    "--status",
    "--no-autostart",
  ]);
  assert.equal(statusNoAuto.status, 1);
});

test("buildDesktopReleaseManifest matches update.ParseDesktopManifest structure", () => {
  const manifest = buildDesktopReleaseManifest({
    version: "0.1.37",
    releaseTag: "v0.1.37-desktop",
    source: "a".repeat(40),
    signed: false,
    assets: {
      "install.ps1": {
        sha256: "b".repeat(64),
        bytes: 8192,
        os: "windows",
        arch: "all",
        kind: "installer",
      },
      "install-cli.sh": {
        sha256: "c".repeat(64),
        bytes: 6144,
        os: "all",
        arch: "all",
        kind: "installer",
      },
    },
  });
  assert.equal(manifest.schemaVersion, 1);
  assert.equal(manifest.releaseTag, "v0.1.37-desktop");
  assert.equal(
    manifest.assets["install-cli.sh"].url,
    "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/install-cli.sh",
  );
});
