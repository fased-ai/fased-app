import { execFileSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
const args = process.argv.slice(2);
const installer = args.includes("--installer");
const versionArgs = args.filter((arg) => arg.startsWith("--version="));
if (
  args.some((arg) => arg !== "--installer" && !arg.startsWith("--version=")) ||
  args.filter((arg) => arg === "--installer").length > 1 ||
  versionArgs.length > 1
)
  throw new Error(
    "Use build:core/build:installer with optional --version=x.y.z",
  );
const version = versionArgs[0]?.slice("--version=".length);
if (version && !/^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$/.test(version))
  throw new Error("Invalid release version");
const target = installer ? "fased-install" : "fased-app";
await mkdir(".evidence/bin", { recursive: true });
execFileSync(
  "go",
  [
    "build",
    "-trimpath",
    ...(version
      ? [
          "-ldflags",
          `-X github.com/fased-ai/fased-app/internal/core.Version=${version}`,
        ]
      : []),
    "-o",
    `.evidence/bin/${target}${process.platform === "win32" ? ".exe" : ""}`,
    `./cmd/${target}`,
  ],
  {
    stdio: "inherit",
    env: installer ? { ...process.env, CGO_ENABLED: "0" } : process.env,
  },
);
