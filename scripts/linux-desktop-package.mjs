import { execFileSync } from "node:child_process";
import {
  chmod,
  copyFile,
  lstat,
  mkdir,
  readdir,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { join, resolve } from "node:path";

const EXECUTABLE_FILES = new Set([
  "fased-app",
  "fased-desktop",
  "fased-signer",
]);

export function linuxArch(arch) {
  if (arch === "x64" || arch === "amd64") return "amd64";
  if (arch === "arm64") return "arm64";
  throw Error(`Unsupported Linux architecture: ${arch}`);
}

export function debianVersion(raw) {
  const normalized = String(raw || "")
    .trim()
    .replace(/^v/i, "")
    .replace(/[^A-Za-z0-9.+~-]/g, "~");
  const hyphenToTilde = normalized.replace(/-/g, "~");
  if (!/^[0-9][A-Za-z0-9.+~]*$/.test(hyphenToTilde)) {
    return "0.0.0~dev";
  }
  return hyphenToTilde;
}

export function commandExists(name) {
  try {
    execFileSync("which", [name], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

async function copyPayloadDir(srcDir, dstDir) {
  await mkdir(dstDir, { recursive: true, mode: 0o755 });
  for (const name of await readdir(srcDir)) {
    const src = join(srcDir, name);
    const dst = join(dstDir, name);
    const info = await lstat(src);
    if (!info.isFile()) continue;
    await copyFile(src, dst);
    const executable = EXECUTABLE_FILES.has(name) || (info.mode & 0o111) !== 0;
    await chmod(dst, executable ? 0o755 : 0o644);
  }
}

export async function stageLinuxAppDir({
  payloadDir,
  appDir,
  desktopFile = resolve("packaging/linux/fased-app.desktop"),
  appRunFile = resolve("packaging/linux/AppRun"),
  iconFile = resolve("web/src/assets/fased-logo.svg"),
}) {
  const libDir = join(appDir, "usr", "lib", "fased-app");
  const appsDir = join(appDir, "usr", "share", "applications");
  const iconsDir = join(
    appDir,
    "usr",
    "share",
    "icons",
    "hicolor",
    "scalable",
    "apps",
  );
  await Promise.all([
    copyPayloadDir(payloadDir, libDir),
    mkdir(appsDir, { recursive: true, mode: 0o755 }),
    mkdir(iconsDir, { recursive: true, mode: 0o755 }),
  ]);
  await Promise.all([
    copyFile(desktopFile, join(appDir, "fased-app.desktop")),
    copyFile(desktopFile, join(appsDir, "fased-app.desktop")),
    copyFile(iconFile, join(appDir, "fased-app.svg")),
    copyFile(iconFile, join(appDir, ".DirIcon")),
    copyFile(iconFile, join(iconsDir, "fased-app.svg")),
    copyFile(appRunFile, join(appDir, "AppRun")),
  ]);
  await chmod(join(appDir, "AppRun"), 0o755);
  await chmod(join(appDir, "fased-app.desktop"), 0o644);
  await chmod(join(appsDir, "fased-app.desktop"), 0o644);
  return { appDir, libDir };
}

export async function stageLinuxDebTree({
  payloadDir,
  debRoot,
  version,
  arch,
  desktopFile = resolve("packaging/linux/fased-app.desktop"),
  iconFile = resolve("web/src/assets/fased-logo.svg"),
}) {
  const debArch = linuxArch(arch);
  const debVer = debianVersion(version);
  const optDir = join(debRoot, "opt", "fased-app");
  const binDir = join(debRoot, "usr", "bin");
  const appsDir = join(debRoot, "usr", "share", "applications");
  const iconsDir = join(
    debRoot,
    "usr",
    "share",
    "icons",
    "hicolor",
    "scalable",
    "apps",
  );
  const debianDir = join(debRoot, "DEBIAN");
  await Promise.all([
    copyPayloadDir(payloadDir, optDir),
    mkdir(binDir, { recursive: true, mode: 0o755 }),
    mkdir(appsDir, { recursive: true, mode: 0o755 }),
    mkdir(iconsDir, { recursive: true, mode: 0o755 }),
    mkdir(debianDir, { recursive: true, mode: 0o755 }),
  ]);
  await Promise.all([
    symlink("/opt/fased-app/fased-app", join(binDir, "fased-app")),
    symlink("/opt/fased-app/fased-desktop", join(binDir, "fased-desktop")),
    copyFile(desktopFile, join(appsDir, "fased-app.desktop")),
    copyFile(iconFile, join(iconsDir, "fased-app.svg")),
  ]);
  const control = [
    "Package: fased-app",
    `Version: ${debVer}`,
    "Section: utils",
    "Priority: optional",
    `Architecture: ${debArch}`,
    "Maintainer: Fased AI <maintainers@fased.ai>",
    "Description: Self-hosted personal and financial AI agent",
    " Shared Fased Go core, isolated signer, and native desktop wrapper.",
    "",
  ].join("\n");
  await writeFile(join(debianDir, "control"), control, { mode: 0o644 });
  return { debArch, debVer, control };
}

export async function buildLinuxDesktopPackages({
  payloadDir,
  outputDir,
  version = "0.0.0",
  arch = process.arch,
  desktopFile = resolve("packaging/linux/fased-app.desktop"),
  appRunFile = resolve("packaging/linux/AppRun"),
  iconFile = resolve("web/src/assets/fased-logo.svg"),
  requireTools = process.env.FASED_REQUIRE_LINUX_DESKTOP_PACKAGES === "1",
}) {
  const debArch = linuxArch(arch);
  const appImageArch = debArch === "amd64" ? "x86_64" : "aarch64";
  const hasDesktopValidate = commandExists("desktop-file-validate");
  const hasDpkgDeb = commandExists("dpkg-deb");
  const hasAppImageTool = commandExists("appimagetool");

  if (requireTools && (!hasDpkgDeb || !hasAppImageTool)) {
    throw Error(
      `Missing required Linux packaging tools: dpkg-deb=${hasDpkgDeb}, appimagetool=${hasAppImageTool}`,
    );
  }

  const workRoot = join(outputDir, `.linux-desktop-stage-${debArch}`);
  const result = {
    arch: debArch,
    desktopValidated: false,
    deb: null,
    appImage: null,
    skipped: [],
  };

  await rm(workRoot, { recursive: true, force: true });
  await mkdir(workRoot, { recursive: true, mode: 0o700 });
  try {
    const appDir = join(workRoot, "FasedApp.AppDir");
    await stageLinuxAppDir({
      payloadDir,
      appDir,
      desktopFile,
      appRunFile,
      iconFile,
    });
    if (hasDesktopValidate) {
      execFileSync(
        "desktop-file-validate",
        [join(appDir, "fased-app.desktop")],
        {
          stdio: "inherit",
        },
      );
      result.desktopValidated = true;
    }

    if (hasAppImageTool) {
      const appImageName = `fased-app-linux-${debArch}.AppImage`;
      const appImagePath = join(outputDir, appImageName);
      execFileSync("appimagetool", [appDir, appImagePath], {
        stdio: "inherit",
        env: {
          ...process.env,
          ARCH: appImageArch,
          APPIMAGE_EXTRACT_AND_RUN: "1",
        },
      });
      result.appImage = appImageName;
    } else {
      result.skipped.push("appimagetool not installed");
    }

    if (hasDpkgDeb) {
      const debRoot = join(workRoot, "deb");
      const rawVersion =
        version ||
        JSON.parse(await readFile("package.json", "utf8")).version ||
        "0.0.0";
      await stageLinuxDebTree({
        payloadDir,
        debRoot,
        version: rawVersion,
        arch: debArch,
        desktopFile,
        iconFile,
      });
      const debName = `fased-app-linux-${debArch}.deb`;
      const debPath = join(outputDir, debName);
      execFileSync(
        "dpkg-deb",
        ["--build", "--root-owner-group", debRoot, debPath],
        {
          stdio: "inherit",
        },
      );
      result.deb = debName;
    } else {
      result.skipped.push("dpkg-deb not installed");
    }
  } finally {
    await rm(workRoot, { recursive: true, force: true });
  }
  return result;
}
