import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { buildDesktopReleaseManifest } from "./build-dual-track-cli.mjs";
import { assembleDualTrackRelease } from "./assemble-dual-track-release.mjs";

const sha256 = (buf) => createHash("sha256").update(buf).digest("hex");

function writeAsset(dir, name, content) {
  const buf = Buffer.from(content);
  writeFileSync(path.join(dir, name), buf);
  return { sha256: sha256(buf), bytes: buf.length };
}

function createFixtureDirs(root, { signed = false, badSource = false } = {}) {
  const source = "a".repeat(40);
  const cliDir = path.join(root, "cli");
  const desktopRoot = path.join(root, "desktop");
  const outDir = path.join(root, "release");
  mkdirSync(cliDir, { recursive: true });

  const cliAssets = {
    "fased-app-linux-amd64.tar.gz": {
      ...writeAsset(cliDir, "fased-app-linux-amd64.tar.gz", "cli-linux-amd64"),
      os: "linux",
      arch: "amd64",
      kind: "cli",
    },
    "fased-app-darwin-arm64.zip": {
      ...writeAsset(cliDir, "fased-app-darwin-arm64.zip", "cli-darwin-arm64"),
      os: "darwin",
      arch: "arm64",
      kind: "cli",
    },
    "fased-app-windows-amd64.zip": {
      ...writeAsset(cliDir, "fased-app-windows-amd64.zip", "cli-windows-amd64"),
      os: "windows",
      arch: "amd64",
      kind: "cli",
    },
    "install-cli.sh": {
      ...writeAsset(cliDir, "install-cli.sh", "#!/bin/bash\n"),
      os: "all",
      arch: "all",
      kind: "installer",
    },
    "install.ps1": {
      ...writeAsset(cliDir, "install.ps1", "# ps1\n"),
      os: "windows",
      arch: "all",
      kind: "installer",
    },
  };
  writeFileSync(
    path.join(cliDir, "desktop-release-manifest.json"),
    JSON.stringify(
      buildDesktopReleaseManifest({
        version: "0.1.38",
        releaseTag: "v0.1.38-desktop",
        source,
        signed: false,
        assets: cliAssets,
      }),
      null,
      2,
    ) + "\n",
  );

  // Linux desktop artifact dir
  const linuxDir = path.join(desktopRoot, "ubuntu-24.04");
  const linuxPkgs = path.join(linuxDir, "packages");
  mkdirSync(linuxPkgs, { recursive: true });
  const linuxMetaFiles = {
    "fased-app-linux-amd64.tar.gz": writeAsset(
      linuxPkgs,
      "fased-app-linux-amd64.tar.gz",
      "desktop-linux-portable",
    ),
    "fased-app_0.1.38_amd64.deb": writeAsset(
      linuxPkgs,
      "fased-app_0.1.38_amd64.deb",
      "deb-bytes",
    ),
    "fased-app-0.1.38-x86_64.AppImage": writeAsset(
      linuxPkgs,
      "fased-app-0.1.38-x86_64.AppImage",
      "appimage-bytes",
    ),
  };
  writeFileSync(
    path.join(linuxDir, "native-package-result.json"),
    JSON.stringify({
      source,
      platform: "linux",
      files: linuxMetaFiles,
      signed: false,
      notarized: false,
    }),
  );

  // macOS desktop artifact dir
  const macDir = path.join(desktopRoot, "macos-15");
  const macPkgs = path.join(macDir, "packages");
  mkdirSync(macPkgs, { recursive: true });
  const dmgName = signed
    ? "fased-app-macos.dmg"
    : "fased-app-macos-unsigned.dmg";
  const macMetaFiles = {
    "fased-app-darwin-arm64.zip": writeAsset(
      macPkgs,
      "fased-app-darwin-arm64.zip",
      "desktop-mac-portable",
    ),
    [dmgName]: writeAsset(macPkgs, dmgName, "dmg-bytes"),
  };
  writeFileSync(
    path.join(macDir, "native-package-result.json"),
    JSON.stringify({
      source: badSource ? "b".repeat(40) : source,
      platform: "darwin",
      files: macMetaFiles,
      signed,
      notarized: signed,
    }),
  );

  // Windows desktop artifact dir
  const winDir = path.join(desktopRoot, "windows-2025");
  const winPkgs = path.join(winDir, "packages");
  mkdirSync(winPkgs, { recursive: true });
  const setupName = signed
    ? "fased-app-windows-setup.exe"
    : "fased-app-windows-setup-unsigned.exe";
  const winMetaFiles = {
    "fased-app-windows-amd64.zip": writeAsset(
      winPkgs,
      "fased-app-windows-amd64.zip",
      "desktop-win-portable",
    ),
    [setupName]: writeAsset(winPkgs, setupName, "setup-exe-bytes"),
  };
  writeFileSync(
    path.join(winDir, "native-package-result.json"),
    JSON.stringify({
      source,
      platform: "win32",
      files: winMetaFiles,
      signed,
      notarized: false,
    }),
  );

  return { cliDir, desktopRoot, outDir };
}

test("assembleDualTrackRelease merges CLI + 3-OS desktop packages without overwriting CLI pins", () => {
  const root = mkdtempSync(path.join(tmpdir(), "fased-assemble-"));
  try {
    const { cliDir, desktopRoot, outDir } = createFixtureDirs(root, {
      signed: false,
    });
    const summary = assembleDualTrackRelease({ cliDir, desktopRoot, outDir });
    assert.equal(summary.version, "0.1.38");
    assert.equal(summary.releaseTag, "v0.1.38-desktop");
    assert.equal(summary.signed, false);
    assert.deepEqual(summary.desktopPlatforms, ["darwin", "linux", "win32"]);

    const manifest = JSON.parse(
      readFileSync(path.join(outDir, "desktop-release-manifest.json"), "utf8"),
    );
    assert.equal(manifest.signed, false);

    // CLI archives remain untouched (no collision with desktop portable bundles).
    assert.equal(
      readFileSync(path.join(outDir, "fased-app-darwin-arm64.zip"), "utf8"),
      "cli-darwin-arm64",
    );
    assert.equal(
      readFileSync(path.join(outDir, "fased-desktop-darwin-arm64.zip"), "utf8"),
      "desktop-mac-portable",
    );
    assert.equal(
      manifest.assets["fased-app-0.1.38-x86_64.AppImage"].kind,
      "desktop",
    );
    assert.equal(manifest.assets["fased-app_0.1.38_amd64.deb"].os, "linux");
    assert.equal(manifest.assets["fased-app-macos-unsigned.dmg"].arch, "arm64");
    assert.equal(
      manifest.assets["fased-app-windows-setup-unsigned.exe"].os,
      "windows",
    );
    assert.ok(existsSync(path.join(outDir, "SHA256SUMS.txt")));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("assembleDualTrackRelease marks signed=true only when macOS signed+notarized and Windows signed", () => {
  const root = mkdtempSync(path.join(tmpdir(), "fased-assemble-signed-"));
  try {
    const { cliDir, desktopRoot, outDir } = createFixtureDirs(root, {
      signed: true,
    });
    const summary = assembleDualTrackRelease({ cliDir, desktopRoot, outDir });
    assert.equal(summary.signed, true);
    const manifest = JSON.parse(
      readFileSync(path.join(outDir, "desktop-release-manifest.json"), "utf8"),
    );
    assert.equal(manifest.signed, true);
    assert.equal(manifest.signingByPlatform?.darwin?.signed, true);
    assert.equal(manifest.signingByPlatform?.windows?.signed, true);
    assert.equal(manifest.signingByPlatform?.linux?.signed, false);
    assert.equal(manifest.signingByPlatform?.linux?.attested, true);
    assert.ok("fased-app-macos.dmg" in manifest.assets);
    assert.ok("fased-app-windows-setup.exe" in manifest.assets);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("assembleDualTrackRelease fails closed on mixed source commits", () => {
  const root = mkdtempSync(path.join(tmpdir(), "fased-assemble-bad-"));
  try {
    const { cliDir, desktopRoot, outDir } = createFixtureDirs(root, {
      badSource: true,
    });
    assert.throws(
      () => assembleDualTrackRelease({ cliDir, desktopRoot, outDir }),
      /Source commit mismatch/,
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
