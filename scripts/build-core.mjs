import { execFileSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
await mkdir(".evidence/bin", { recursive: true });
execFileSync(
  "go",
  [
    "build",
    "-trimpath",
    "-o",
    `.evidence/bin/fased-app${process.platform === "win32" ? ".exe" : ""}`,
    "./cmd/fased-app",
  ],
  { stdio: "inherit" },
);
