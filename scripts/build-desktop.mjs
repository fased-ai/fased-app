import { execFileSync } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { resolve } from "node:path";

await mkdir(".evidence/bin", { recursive: true });
const windows = process.platform === "win32";
const output = resolve(`.evidence/bin/fased-desktop${windows ? ".exe" : ""}`);
const tags =
  process.platform === "linux" ? "production,webkit2_41" : "production";
const environment = { ...process.env, CGO_ENABLED: windows ? "0" : "1" };
// A direct Go build must declare the framework normally supplied by native
// packaging. Wails' macOS frontend references UTType on current Apple SDKs.
if (process.platform === "darwin")
  environment.CGO_LDFLAGS = [
    process.env.CGO_LDFLAGS,
    "-framework UniformTypeIdentifiers",
  ]
    .filter(Boolean)
    .join(" ");
await writeFile(
  ".evidence/desktop-build.json",
  JSON.stringify(
    {
      platform: process.platform,
      arch: process.arch,
      status: "building",
      unsignedEngineeringBuild: true,
    },
    null,
    2,
  ) + "\n",
);
execFileSync(
  "go",
  ["-C", "desktop", "build", "-trimpath", "-tags", tags, "-o", output, "."],
  {
    stdio: "inherit",
    env: environment,
  },
);
const data = await readFile(output);
await writeFile(
  ".evidence/desktop-build.json",
  JSON.stringify(
    {
      platform: process.platform,
      arch: process.arch,
      bytes: data.length,
      sha256: createHash("sha256").update(data).digest("hex"),
      unsignedEngineeringBuild: true,
    },
    null,
    2,
  ) + "\n",
);
