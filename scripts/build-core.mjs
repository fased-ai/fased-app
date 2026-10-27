import { execFileSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
const args = process.argv.slice(2);
if (args.length > 1 || (args.length === 1 && args[0] !== "--installer"))
  throw new Error("Use build:core or build:installer");
const installer = args[0] === "--installer";
const target = installer ? "fased-install" : "fased-app";
await mkdir(".evidence/bin", { recursive: true });
execFileSync(
  "go",
  [
    "build",
    "-trimpath",
    "-o",
    `.evidence/bin/${target}${process.platform === "win32" ? ".exe" : ""}`,
    `./cmd/${target}`,
  ],
  {
    stdio: "inherit",
    env: installer ? { ...process.env, CGO_ENABLED: "0" } : process.env,
  },
);
