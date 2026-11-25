import { createHash } from "node:crypto";
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { buildDesktopReleaseManifest } from "./build-dual-track-cli.mjs";
import { verifyChecksumsAndManifest } from "./smoke-dual-track-installers.mjs";

const sha256Hex = (buf) => createHash("sha256").update(buf).digest("hex");

function inspectFile(filePath) {
  const bytes = readFileSync(filePath);
  return { sha256: sha256Hex(bytes), bytes: bytes.length };
}

function inferArchFromNames(filename, metaFiles = {}, fallback = "amd64") {
  const lower = filename.toLowerCase();
  if (lower.includes("arm64") || lower.includes("aarch64")) return "arm64";
  if (
    lower.includes("amd64") ||
    lower.includes("x86_64") ||
    lower.includes("x64")
  ) {
    return "amd64";
  }
  for (const name of Object.keys(metaFiles)) {
    const m =
      /^fased-app-(?:linux|darwin|windows)-(amd64|arm64)\.(?:zip|tar\.gz)$/.exec(
        name,
      );
    if (m) return m[1];
  }
  return fallback;
}

function classifyDesktopFile(filename, platform, metaFiles) {
  if (/\.AppImage$/i.test(filename) || /\.deb$/i.test(filename)) {
    return {
      outputName: filename,
      os: "linux",
      arch: inferArchFromNames(filename, metaFiles, "amd64"),
      kind: "desktop",
    };
  }
  if (/^fased-app-macos.*\.dmg$/i.test(filename)) {
    return {
      outputName: filename,
      os: "darwin",
      arch: inferArchFromNames(filename, metaFiles, "arm64"),
      kind: "desktop",
    };
  }
  if (/^fased-app-windows-setup.*\.exe$/i.test(filename)) {
    return {
      outputName: filename,
      os: "windows",
      arch: inferArchFromNames(filename, metaFiles, "amd64"),
      kind: "desktop",
    };
  }
  const portable =
    /^fased-app-(linux|darwin|windows)-(amd64|arm64)\.(tar\.gz|zip)$/.exec(
      filename,
    );
  if (portable) {
    const [, os, arch, ext] = portable;
    return {
      outputName: `fased-desktop-${os}-${arch}.${ext}`,
      os,
      arch,
      kind: "desktop",
    };
  }
  if (platform) {
    return null;
  }
  return null;
}

function collectDesktopDirs(desktopRoot, explicitDirs = []) {
  const dirs = [];
  for (const d of explicitDirs) {
    if (d && existsSync(d)) dirs.push(path.resolve(d));
  }
  if (desktopRoot && existsSync(desktopRoot)) {
    const directMeta = path.join(desktopRoot, "native-package-result.json");
    if (existsSync(directMeta)) {
      dirs.push(path.resolve(desktopRoot));
    } else {
      for (const entry of readdirSync(desktopRoot).sort()) {
        const full = path.join(desktopRoot, entry);
        if (
          statSync(full).isDirectory() &&
          existsSync(path.join(full, "native-package-result.json"))
        ) {
          dirs.push(path.resolve(full));
        }
      }
    }
  }
  return dirs;
}

/**
 * Merges headless CLI archives + rendered install scripts + 3-OS native desktop
 * installers into one verified release directory with a unified
 * desktop-release-manifest.json and SHA256SUMS.txt.
 */
export function assembleDualTrackRelease({
  cliDir,
  desktopRoot = "",
  desktopDirs = [],
  outDir,
}) {
  if (!cliDir || !existsSync(cliDir)) {
    throw new Error(`CLI archive directory not found: ${cliDir}`);
  }
  if (!outDir) {
    throw new Error("Output directory is required");
  }

  const cliManifestPath = path.join(cliDir, "desktop-release-manifest.json");
  if (!existsSync(cliManifestPath)) {
    throw new Error(`Missing CLI manifest: ${cliManifestPath}`);
  }
  const cliManifest = JSON.parse(readFileSync(cliManifestPath, "utf8"));
  const { version, releaseTag, source } = cliManifest;
  if (!version || !releaseTag || !source) {
    throw new Error("CLI manifest is missing version, releaseTag, or source");
  }

  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true, mode: 0o755 });

  const assets = {};

  // 1. Copy and re-verify all CLI track assets (preserving exact SHA-256 pins in install-cli.sh & install.ps1).
  for (const [name, meta] of Object.entries(cliManifest.assets || {})) {
    if (path.basename(name) !== name) {
      throw new Error(`Unsafe CLI asset name: ${name}`);
    }
    const src = path.join(cliDir, name);
    if (!existsSync(src)) {
      throw new Error(`Missing CLI asset referenced by manifest: ${name}`);
    }
    const dst = path.join(outDir, name);
    copyFileSync(src, dst);
    const actual = inspectFile(dst);
    if (actual.sha256 !== meta.sha256 || actual.bytes !== meta.bytes) {
      throw new Error(`CLI asset integrity mismatch for ${name}`);
    }
    assets[name] = {
      sha256: actual.sha256,
      bytes: actual.bytes,
      os: meta.os,
      arch: meta.arch,
      kind: meta.kind,
    };
  }

  // 2. Merge native desktop packages from each OS artifact directory.
  const resolvedDesktopDirs = collectDesktopDirs(desktopRoot, desktopDirs);
  const platformSigning = new Map();

  for (const dir of resolvedDesktopDirs) {
    const metaPath = path.join(dir, "native-package-result.json");
    const meta = JSON.parse(readFileSync(metaPath, "utf8"));
    if (meta.source !== source) {
      throw new Error(
        `Source commit mismatch in ${metaPath}: expected ${source}, got ${meta.source}`,
      );
    }
    platformSigning.set(meta.platform, {
      signed: Boolean(meta.signed),
      notarized: Boolean(meta.notarized),
    });

    const packagesDir = existsSync(path.join(dir, "packages"))
      ? path.join(dir, "packages")
      : dir;

    for (const [filename, expected] of Object.entries(meta.files || {})) {
      if (path.basename(filename) !== filename) {
        throw new Error(`Unsafe desktop artifact filename: ${filename}`);
      }
      const classified = classifyDesktopFile(
        filename,
        meta.platform,
        meta.files,
      );
      if (!classified) continue;

      const src = path.join(packagesDir, filename);
      if (!existsSync(src)) {
        throw new Error(
          `Missing desktop artifact ${filename} in ${packagesDir}`,
        );
      }
      if (classified.outputName in assets) {
        throw new Error(
          `Duplicate assembled release asset: ${classified.outputName}`,
        );
      }
      const dst = path.join(outDir, classified.outputName);
      copyFileSync(src, dst);
      const actual = inspectFile(dst);
      if (
        actual.sha256 !== expected.sha256 ||
        actual.bytes !== expected.bytes
      ) {
        throw new Error(
          `Desktop artifact integrity mismatch for ${filename} (${dir})`,
        );
      }
      assets[classified.outputName] = {
        sha256: actual.sha256,
        bytes: actual.bytes,
        os: classified.os,
        arch: classified.arch,
        kind: classified.kind,
      };
    }
  }

  // Only mark the unified desktop release as signed when both macOS (signed + notarized)
  // and Windows (Authenticode signed) native desktop packages are verified present.
  const darwinSign = platformSigning.get("darwin");
  const winSign = platformSigning.get("win32");
  const signed = Boolean(
    darwinSign?.signed && darwinSign?.notarized && winSign?.signed,
  );

  // 3. Emit unified desktop-release-manifest.json and SHA256SUMS.txt.
  const sortedAssets = {};
  for (const key of Object.keys(assets).sort()) {
    sortedAssets[key] = assets[key];
  }
  const unifiedManifest = buildDesktopReleaseManifest({
    version,
    releaseTag,
    source,
    signed,
    assets: sortedAssets,
  });
  unifiedManifest.signingByPlatform = {
    darwin: {
      signed: Boolean(darwinSign?.signed),
      notarized: Boolean(darwinSign?.notarized),
    },
    windows: {
      signed: Boolean(winSign?.signed),
    },
    linux: {
      signed: false,
      attested: true,
    },
  };
  writeFileSync(
    path.join(outDir, "desktop-release-manifest.json"),
    JSON.stringify(unifiedManifest, null, 2) + "\n",
    { mode: 0o644 },
  );

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

  const verification = verifyChecksumsAndManifest(outDir);
  return {
    version,
    releaseTag,
    source,
    signed,
    assetCount: Object.keys(sortedAssets).length,
    desktopPlatforms: [...platformSigning.keys()].sort(),
    verification,
  };
}

function parseArgs(argv) {
  const opts = {
    cliDir: ".evidence/dual-track-cli",
    desktopRoot: ".evidence/desktop-artifacts",
    desktopDirs: [],
    outDir: ".evidence/dual-track-release",
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--cli-dir" && i + 1 < argv.length) {
      opts.cliDir = argv[++i];
    } else if (arg === "--desktop-root" && i + 1 < argv.length) {
      opts.desktopRoot = argv[++i];
    } else if (arg === "--desktop-dir" && i + 1 < argv.length) {
      opts.desktopDirs.push(argv[++i]);
    } else if (arg === "--out-dir" && i + 1 < argv.length) {
      opts.outDir = argv[++i];
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  return opts;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const summary = assembleDualTrackRelease(parseArgs(process.argv.slice(2)));
  console.log(JSON.stringify(summary, null, 2));
}
