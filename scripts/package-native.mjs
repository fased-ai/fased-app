import { execFileSync } from "node:child_process";
import { mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";
const output = resolve(".evidence/packages");
await mkdir(output, { recursive: true });
const windows = process.platform === "win32";
const desktop = resolve(`.evidence/bin/fased-desktop${windows ? ".exe" : ""}`);
const source = execFileSync("git", ["rev-parse", "HEAD"], {
  encoding: "utf8",
}).trim();
execFileSync(
  "go",
  [
    "run",
    "./cmd/fased-package",
    "--engine",
    resolve(`.evidence/bin/fased-app${windows ? ".exe" : ""}`),
    "--desktop",
    desktop,
    "--out",
    output,
    "--source",
    source,
  ],
  { stdio: "inherit" },
);
// Native installers are deliberately unsigned engineering outputs. No publish,
// keychain credential, signing service or Gatekeeper bypass is part of CI.
if (windows) {
  const fixture = resolve(".evidence/windows-package-stage");
  await mkdir(fixture, { mode: 0o700 });
  try {
    const archive = join(
      output,
      `fased-app-windows-${process.arch === "x64" ? "amd64" : process.arch}.zip`,
    );
    execFileSync("tar.exe", ["-xf", archive, "-C", fixture]);
    const payload = join(
      fixture,
      `fased-app-windows-${process.arch === "x64" ? "amd64" : process.arch}`,
    );
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
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
}
if (process.platform === "darwin") {
  const fixture = resolve(".evidence/macos-package-stage");
  await mkdir(fixture, { mode: 0o700 });
  try {
    const arch = process.arch === "x64" ? "amd64" : process.arch;
    execFileSync("ditto", [
      "-xk",
      join(output, `fased-app-darwin-${arch}.zip`),
      fixture,
    ]);
    execFileSync(
      "plutil",
      ["-lint", join(fixture, "Fased App.app/Contents/Info.plist")],
      { stdio: "inherit" },
    );
    execFileSync(
      "hdiutil",
      [
        "create",
        "-volname",
        "Fased App Engineering",
        "-srcfolder",
        fixture,
        "-ov",
        "-format",
        "UDZO",
        join(output, "fased-app-macos-unsigned.dmg"),
      ],
      { stdio: "inherit" },
    );
  } finally {
    await rm(fixture, { recursive: true, force: true });
  }
}
const { readdir } = await import("node:fs/promises");
const files = {};
for (const name of await readdir(output)) {
  if (!/\.(zip|gz|exe|dmg)$/.test(name)) continue;
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
      unsignedEngineering: true,
      publicRelease: false,
    },
    null,
    2,
  ) + "\n",
);
