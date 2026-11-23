import { execFileSync } from "node:child_process";
import { mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";
import { createMacDiskImage } from "./macos-dmg.mjs";
import {
  buildLinuxDesktopPackages,
  linuxArch,
} from "./linux-desktop-package.mjs";
import {
  buildMacOSCodesignAppArgs,
  buildMacOSCodesignDmgArgs,
  buildMacOSNotarySubmitArgs,
  buildWindowsSigntoolSignArgs,
  buildWindowsSigntoolVerifyArgs,
  findWindowsSigntool,
  resolveMacOSSigningConfig,
  resolveWindowsSigningConfig,
} from "./signing-helpers.mjs";
const output = resolve(".evidence/packages");
await mkdir(output, { recursive: true });
const windows = process.platform === "win32";
const desktop = resolve(`.evidence/bin/fased-desktop${windows ? ".exe" : ""}`);
const source = execFileSync("git", ["rev-parse", "HEAD"], {
  encoding: "utf8",
}).trim();
// The owner client carries a matching Linux core/signer pair. The server app
// package and old native inventories remain supported without this optional file.
const signer = resolve(".evidence/bin/fased-signer");
if (process.platform === "linux") {
  execFileSync(
    "go",
    ["build", "-trimpath", "-o", signer, "./cmd/fased-signer"],
    { stdio: "inherit" },
  );
}
execFileSync(
  "go",
  [
    "run",
    "./cmd/fased-package",
    "--engine",
    resolve(`.evidence/bin/fased-app${windows ? ".exe" : ""}`),
    "--desktop",
    desktop,
    ...(process.platform === "linux" ? ["--signer", signer] : []),
    "--out",
    output,
    "--source",
    source,
  ],
  { stdio: "inherit" },
);
// Native installers are unsigned engineering outputs unless explicit signing
// credentials are provided in CI environment secrets.
let signed = false;
let notarized = false;
if (windows) {
  const winSigning = resolveWindowsSigningConfig(process.env);
  const signtool = winSigning.enabled ? findWindowsSigntool() : "";
  const fixture = resolve(".evidence/windows-package-stage");
  await mkdir(fixture, { mode: 0o700 });
  try {
    let pfxPath = winSigning.pfxPath;
    if (winSigning.enabled && !pfxPath && winSigning.pfxBase64) {
      pfxPath = join(fixture, "temp-codesign.pfx");
      await writeFile(pfxPath, Buffer.from(winSigning.pfxBase64, "base64"), {
        mode: 0o600,
      });
    }
    const signExe = (targetExe) => {
      execFileSync(
        signtool,
        buildWindowsSigntoolSignArgs({
          pfxPath,
          password: winSigning.password,
          timestampUrl: winSigning.timestampUrl,
          targetExe,
        }),
        { stdio: "inherit" },
      );
      execFileSync(signtool, buildWindowsSigntoolVerifyArgs(targetExe), {
        stdio: "inherit",
      });
    };
    const archive = join(
      output,
      `fased-app-windows-${process.arch === "x64" ? "amd64" : process.arch}.zip`,
    );
    execFileSync(
      join(process.env.SystemRoot || "C:\\Windows", "System32", "tar.exe"),
      ["-xf", archive, "-C", fixture],
    );
    const payload = join(
      fixture,
      `fased-app-windows-${process.arch === "x64" ? "amd64" : process.arch}`,
    );
    if (winSigning.enabled) {
      signExe(join(payload, "fased-app.exe"));
      signExe(join(payload, "fased-desktop.exe"));
    }
    execFileSync(
      "C:\\Program Files (x86)\\Inno Setup 6\\ISCC.exe",
      ["packaging/windows.iss"],
      {
        stdio: "inherit",
        env: {
          ...process.env,
          FASED_PACKAGE_PAYLOAD: payload,
          FASED_PACKAGE_OUTPUT: output,
        },
      },
    );
    if (winSigning.enabled) {
      const unsignedExe = join(output, "fased-app-windows-setup-unsigned.exe");
      const signedExe = join(output, "fased-app-windows-setup.exe");
      const { rename } = await import("node:fs/promises");
      await rename(unsignedExe, signedExe);
      signExe(signedExe);
      signed = true;
    }
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
}
if (process.platform === "darwin") {
  const macSigning = resolveMacOSSigningConfig(process.env);
  const fixture = resolve(".evidence/macos-package-stage");
  await mkdir(fixture, { mode: 0o700 });
  try {
    const arch = process.arch === "x64" ? "amd64" : process.arch;
    execFileSync("ditto", [
      "-xk",
      join(output, `fased-app-darwin-${arch}.zip`),
      fixture,
    ]);
    const appPath = join(fixture, "Fased App.app");
    execFileSync("plutil", ["-lint", join(appPath, "Contents/Info.plist")], {
      stdio: "inherit",
    });
    if (macSigning.enabled) {
      execFileSync(
        "codesign",
        buildMacOSCodesignAppArgs(macSigning.identity, appPath),
        { stdio: "inherit" },
      );
      execFileSync(
        "codesign",
        ["--verify", "--deep", "--strict", "--verbose=2", appPath],
        { stdio: "inherit" },
      );
      signed = true;
    }
    const dmgName = macSigning.enabled
      ? "fased-app-macos.dmg"
      : "fased-app-macos-unsigned.dmg";
    const dmgPath = join(output, dmgName);
    await createMacDiskImage([
      "create",
      "-volname",
      macSigning.enabled ? "Fased App" : "Fased App Engineering",
      "-srcfolder",
      fixture,
      "-ov",
      "-format",
      "UDZO",
      dmgPath,
    ]);
    if (macSigning.enabled) {
      execFileSync(
        "codesign",
        buildMacOSCodesignDmgArgs(macSigning.identity, dmgPath),
        { stdio: "inherit" },
      );
      execFileSync(
        "codesign",
        ["--verify", "--strict", "--verbose=2", dmgPath],
        { stdio: "inherit" },
      );
    }
    if (macSigning.enabled && macSigning.notarizationEnabled) {
      execFileSync(
        "xcrun",
        buildMacOSNotarySubmitArgs({
          dmgPath,
          profile: macSigning.notaryProfile,
          keychain: macSigning.notaryKeychain,
        }),
        { stdio: "inherit" },
      );
      execFileSync("xcrun", ["stapler", "staple", dmgPath], {
        stdio: "inherit",
      });
      execFileSync("xcrun", ["stapler", "validate", dmgPath], {
        stdio: "inherit",
      });
      notarized = true;
    }
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
}
let linuxPackages = null;
if (process.platform === "linux") {
  const arch = linuxArch(process.arch);
  const bundleName = `fased-app-linux-${arch}`;
  const archive = join(output, `${bundleName}.tar.gz`);
  const fixture = resolve(".evidence/linux-package-stage");
  await rm(fixture, { recursive: true, force: true });
  await mkdir(fixture, { mode: 0o700 });
  try {
    execFileSync("tar", ["-xzf", archive, "-C", fixture], {
      stdio: "inherit",
    });
    const manifest = JSON.parse(await readFile("package.json", "utf8"));
    linuxPackages = await buildLinuxDesktopPackages({
      payloadDir: join(fixture, bundleName),
      outputDir: output,
      version: process.env.FASED_RELEASE_VERSION || manifest.version || "0.0.0",
      arch,
    });
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
}
const { readdir } = await import("node:fs/promises");
const files = {};
for (const name of await readdir(output)) {
  if (!/\.(zip|gz|exe|dmg|deb|AppImage)$/.test(name)) continue;
  const b = await readFile(join(output, name));
  files[name] = {
    bytes: b.length,
    sha256: createHash("sha256").update(b).digest("hex"),
  };
}
await writeFile(
  ".evidence/native-package-result.json",
  JSON.stringify(
    {
      source,
      platform: process.platform,
      files,
      ...(linuxPackages ? { linuxPackages } : {}),
      signed,
      notarized,
      unsignedEngineering: !signed,
      publicRelease: false,
    },
    null,
    2,
  ) + "\n",
);
