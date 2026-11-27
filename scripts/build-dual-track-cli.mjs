import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  chmodSync,
  copyFileSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import {
  renderDualTrackCliEntry,
  renderWindowsEntry,
} from "./render-install-entry.mjs";

export const CLI_TARGETS = [
  { os: "linux", arch: "amd64" },
  { os: "linux", arch: "arm64" },
  { os: "darwin", arch: "arm64" },
  { os: "darwin", arch: "amd64" },
  { os: "windows", arch: "amd64" },
  { os: "windows", arch: "arm64" },
];

export const LINUX_PLATFORM_FILES = {
  "bwrap-userns-restrict": "packaging/linux/apparmor/bwrap-userns-restrict",
  "APPARMOR-COPYING": "packaging/linux/apparmor/COPYING",
  "APPARMOR-COPYRIGHT": "packaging/linux/apparmor/ubuntu-copyright",
  "APPARMOR-SOURCE.md": "packaging/linux/apparmor/README.md",
};

const sha256Hex = (buf) => createHash("sha256").update(buf).digest("hex");

export function buildBundleManifest({
  platform,
  arch,
  version,
  source,
  files,
}) {
  return {
    schema: 1,
    platform,
    arch,
    version,
    source,
    desktop: false,
    unsignedEngineering: true,
    files,
  };
}

export function buildDesktopReleaseManifest({
  version,
  releaseTag,
  source,
  signed = false,
  assets,
}) {
  if (!/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version ?? "")) {
    throw new Error(`Canonical X.Y.Z version required, got: ${version}`);
  }
  if (releaseTag !== `v${version}-desktop`) {
    throw new Error(`Release tag mismatch: ${releaseTag}`);
  }
  if (!/^[a-f0-9]{40}$/.test(source ?? "")) {
    throw new Error(`40-hex source commit required, got: ${source}`);
  }
  const normalizedAssets = {};
  for (const [name, meta] of Object.entries(assets)) {
    normalizedAssets[name] = {
      url: `https://github.com/fased-ai/fased-app/releases/download/${releaseTag}/${name}`,
      sha256: meta.sha256,
      bytes: meta.bytes,
      os: meta.os,
      arch: meta.arch,
      kind: meta.kind,
    };
  }
  return {
    schemaVersion: 1,
    version,
    releaseTag,
    source,
    signed: Boolean(signed),
    assets: normalizedAssets,
  };
}

function inspectFile(filePath) {
  const bytes = readFileSync(filePath);
  return { sha256: sha256Hex(bytes), bytes: bytes.length };
}

function createUnixZipFromStage(outDir, folderName, zipPath) {
  const py = [
    "import os, stat, sys, time, zipfile",
    "root_dir, folder, dest = sys.argv[1], sys.argv[2], sys.argv[3]",
    "epoch = int(os.environ.get('SOURCE_DATE_EPOCH', '1704067200'))",
    "dt = time.gmtime(max(epoch, 315532800))[:6]",
    "base = os.path.join(root_dir, folder)",
    "with zipfile.ZipFile(dest, 'w', compression=zipfile.ZIP_DEFLATED) as zf:",
    "  for dirpath, dirnames, filenames in os.walk(base):",
    "    dirnames.sort()",
    "    filenames.sort()",
    "    for fn in filenames:",
    "      full = os.path.join(dirpath, fn)",
    "      rel = os.path.relpath(full, root_dir)",
    "      st = os.stat(full)",
    "      zi = zipfile.ZipInfo(rel, date_time=dt)",
    "      zi.create_system = 3",
    "      mode = 0o755 if (st.st_mode & 0o111) else 0o644",
    "      zi.external_attr = ((stat.S_IFREG | mode) << 16)",
    "      with open(full, 'rb') as fh:",
    "        zf.writestr(zi, fh.read(), compress_type=zipfile.ZIP_DEFLATED)",
  ].join("\n");
  execFileSync("python3", ["-c", py, outDir, folderName, zipPath], {
    stdio: "inherit",
  });
}

export function main() {
  const repoRoot = process.cwd();
  const ubuntuCfg = JSON.parse(
    readFileSync(path.join(repoRoot, "release/ubuntu.json"), "utf8"),
  );
  const version = (
    process.env.FASED_RELEASE_VERSION ||
    ubuntuCfg.version ||
    ""
  ).trim();
  if (!/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version)) {
    throw new Error(`Invalid base version: ${version}`);
  }
  const source = execFileSync("git", ["rev-parse", "HEAD"], {
    encoding: "utf8",
  }).trim();
  const desktopVersion = `${version}-desktop`;
  const releaseTag = `v${desktopVersion}`;
  const outDir = path.join(repoRoot, ".evidence/dual-track-cli");
  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true, mode: 0o755 });

  const ldflags = `-s -w -X github.com/fased-ai/fased-app/internal/core.Version=${version}`;
  const assets = {};
  const windowsTargets = [];
  const unixCliTargets = [];

  for (const { os, arch } of CLI_TARGETS) {
    const folder = `fased-app-${os}-${arch}`;
    const stage = path.join(outDir, folder);
    mkdirSync(stage, { recursive: true, mode: 0o755 });
    const ext = os === "windows" ? ".exe" : "";
    const engineName = `fased-app${ext}`;
    const env = { ...process.env, GOOS: os, GOARCH: arch, CGO_ENABLED: "0" };

    execFileSync(
      "go",
      [
        "build",
        "-buildvcs=false",
        "-trimpath",
        "-ldflags",
        ldflags,
        "-o",
        path.join(stage, engineName),
        "./cmd/fased-app",
      ],
      { env, stdio: "inherit" },
    );
    chmodSync(path.join(stage, engineName), 0o755);

    const files = {};
    files[engineName] = inspectFile(path.join(stage, engineName));

    if (os === "linux") {
      execFileSync(
        "go",
        [
          "build",
          "-buildvcs=false",
          "-trimpath",
          "-ldflags",
          ldflags,
          "-o",
          path.join(stage, "fased-signer"),
          "./cmd/fased-signer",
        ],
        { env, stdio: "inherit" },
      );
      chmodSync(path.join(stage, "fased-signer"), 0o755);
      files["fased-signer"] = inspectFile(path.join(stage, "fased-signer"));
      for (const [dstName, srcRel] of Object.entries(LINUX_PLATFORM_FILES)) {
        const dstPath = path.join(stage, dstName);
        copyFileSync(path.join(repoRoot, srcRel), dstPath);
        chmodSync(dstPath, 0o644);
        files[dstName] = inspectFile(dstPath);
      }
    }

    for (const legal of ["LICENSE", "NOTICE"]) {
      const dstPath = path.join(stage, legal);
      copyFileSync(path.join(repoRoot, legal), dstPath);
      chmodSync(dstPath, 0o644);
      files[legal] = inspectFile(dstPath);
    }

    const bundleManifest = buildBundleManifest({
      platform: os,
      arch,
      version,
      source,
      files,
    });
    writeFileSync(
      path.join(stage, "bundle.json"),
      JSON.stringify(bundleManifest, null, 2) + "\n",
      { mode: 0o644 },
    );

    if (os === "windows") {
      const zipName = `${folder}.zip`;
      const zipPath = path.join(outDir, zipName);
      createUnixZipFromStage(outDir, folder, zipPath);
      const buf = readFileSync(zipPath);
      assets[zipName] = {
        sha256: sha256Hex(buf),
        bytes: buf.length,
        os,
        arch,
        kind: "cli",
      };
      windowsTargets.push({ arch, archive: buf });
    } else if (os === "darwin") {
      const tgzName = `${folder}.tar.gz`;
      const tgzPath = path.join(outDir, tgzName);
      execFileSync("tar", ["-C", outDir, "-czf", tgzPath, folder], {
        stdio: "inherit",
      });
      const tgzBuf = readFileSync(tgzPath);
      assets[tgzName] = {
        sha256: sha256Hex(tgzBuf),
        bytes: tgzBuf.length,
        os,
        arch,
        kind: "cli",
      };

      const zipName = `${folder}.zip`;
      const zipPath = path.join(outDir, zipName);
      createUnixZipFromStage(outDir, folder, zipPath);
      const zipBuf = readFileSync(zipPath);
      assets[zipName] = {
        sha256: sha256Hex(zipBuf),
        bytes: zipBuf.length,
        os,
        arch,
        kind: "cli",
      };
      unixCliTargets.push({ os, arch, archive: zipBuf });
    } else {
      const tgzName = `${folder}.tar.gz`;
      const tgzPath = path.join(outDir, tgzName);
      execFileSync("tar", ["-C", outDir, "-czf", tgzPath, folder], {
        stdio: "inherit",
      });
      const tgzBuf = readFileSync(tgzPath);
      assets[tgzName] = {
        sha256: sha256Hex(tgzBuf),
        bytes: tgzBuf.length,
        os,
        arch,
        kind: "cli",
      };
      unixCliTargets.push({ os, arch, archive: tgzBuf });
    }

    rmSync(stage, { recursive: true, force: true });
  }

  const psTemplate = readFileSync(path.join(repoRoot, "install.ps1"), "utf8");
  const renderedPs = renderWindowsEntry(psTemplate, {
    version: desktopVersion,
    targets: windowsTargets,
  });
  const psPath = path.join(outDir, "install.ps1");
  writeFileSync(psPath, renderedPs, { mode: 0o644 });
  const psMeta = inspectFile(psPath);
  assets["install.ps1"] = {
    ...psMeta,
    os: "windows",
    arch: "all",
    kind: "installer",
  };

  const cliTemplate = readFileSync(
    path.join(repoRoot, "install-cli.sh"),
    "utf8",
  );
  const renderedCli = renderDualTrackCliEntry(cliTemplate, {
    version: desktopVersion,
    targets: unixCliTargets,
  });
  const cliPath = path.join(outDir, "install-cli.sh");
  writeFileSync(cliPath, renderedCli, { mode: 0o755 });
  const cliMeta = inspectFile(cliPath);
  assets["install-cli.sh"] = {
    ...cliMeta,
    os: "all",
    arch: "all",
    kind: "installer",
  };

  // Also preserve reference install.sh template for release inspection if needed,
  // but keep attested Ubuntu install.sh exclusive to release.yml.
  const desktopManifest = buildDesktopReleaseManifest({
    version,
    releaseTag,
    source,
    signed: false,
    assets,
  });
  const manifestPath = path.join(outDir, "desktop-release-manifest.json");
  writeFileSync(manifestPath, JSON.stringify(desktopManifest, null, 2) + "\n", {
    mode: 0o644,
  });

  const sumTargets = readdirSync(outDir)
    .filter(
      (f) => f !== "SHA256SUMS.txt" && statSync(path.join(outDir, f)).isFile(),
    )
    .sort();
  const sums =
    sumTargets
      .map((f) => `${sha256Hex(readFileSync(path.join(outDir, f)))}  ${f}`)
      .join("\n") + "\n";
  writeFileSync(path.join(outDir, "SHA256SUMS.txt"), sums, { mode: 0o644 });
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  main();
}
