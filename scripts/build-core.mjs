import { execFileSync } from "node:child_process";
import { constants } from "node:fs";
import { copyFile, mkdir, readFile, rm } from "node:fs/promises";
import { createHash } from "node:crypto";
const args = process.argv.slice(2);
const installer = args.includes("--installer");
const one = (prefix) => {
  const values = args.filter((a) => a.startsWith(prefix));
  if (values.length > 1) throw Error("Duplicate build option");
  return values[0]?.slice(prefix.length);
};
const version = one("--version="),
  signer = one("--wallet-signer="),
  desktop = one("--wallet-desktop="),
  source = one("--source=");
if (
  args.some(
    (a) =>
      a !== "--installer" &&
      !a.startsWith("--version=") &&
      !a.startsWith("--wallet-signer=") &&
      !a.startsWith("--wallet-desktop=") &&
      !a.startsWith("--source="),
  ) ||
  args.filter((a) => a === "--installer").length > 1
)
  throw Error("Invalid core build option");
if (version && !/^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$/.test(version))
  throw Error("Invalid release version");
if (
  (signer || desktop || source) &&
  (!signer ||
    !version ||
    !/^[a-f0-9]{40}$/.test(source ?? "") ||
    (!installer && !desktop))
)
  throw Error(
    "Wallet release binding needs a version, signer, exact source and (for the core) native client",
  );
const target = installer ? "fased-install" : "fased-app";
const staged = "cmd/fased-install/wallet-signer.bin";
const stagedDesktop = "cmd/fased-install/wallet-desktop.bin";
const owned = [];
await mkdir(".evidence/bin", { recursive: true });
try {
  const flags = version
    ? [`-X github.com/fased-ai/fased-app/internal/core.Version=${version}`]
    : [];
  if (signer) {
    const bytes = await readFile(signer);
    if (!bytes.length || bytes.length > 64 * 1024 * 1024)
      throw Error("Bounded compiled wallet signer required");
    const hash = createHash("sha256").update(bytes).digest("hex");
    if (installer) {
      await copyFile(signer, staged, constants.COPYFILE_EXCL);
      owned.push(staged);
      const copied = await readFile(staged);
      if (createHash("sha256").update(copied).digest("hex") !== hash)
        throw Error("Signer changed during embedding");
      flags.push(
        `-X main.compiledWalletSource=${source}`,
        `-X main.compiledWalletSHA256=${hash}`,
      );
    } else {
      flags.push(
        `-X main.compiledNativeSource=${source}`,
        `-X main.compiledNativeSignerSHA256=${hash}`,
      );
    }
  }
  if (desktop) {
    const bytes = await readFile(desktop);
    if (!bytes.length || bytes.length > 64 * 1024 * 1024)
      throw Error("Bounded compiled native client required");
    const hash = createHash("sha256").update(bytes).digest("hex");
    if (installer) {
      await copyFile(desktop, stagedDesktop, constants.COPYFILE_EXCL);
      owned.push(stagedDesktop);
      const copied = await readFile(stagedDesktop);
      if (createHash("sha256").update(copied).digest("hex") !== hash)
        throw Error("Native client changed during embedding");
      flags.push(`-X main.compiledDesktopSHA256=${hash}`);
    } else {
      flags.push(`-X main.compiledNativeDesktopSHA256=${hash}`);
    }
  }
  execFileSync(
    "go",
    [
      "build",
      "-trimpath",
      ...(installer && signer
        ? ["-tags", desktop ? "wallet_payload,wallet_native" : "wallet_payload"]
        : []),
      ...(flags.length ? ["-ldflags", flags.join(" ")] : []),
      "-o",
      `.evidence/bin/${target}${process.platform === "win32" ? ".exe" : ""}`,
      `./cmd/${target}`,
    ],
    {
      stdio: "inherit",
      env: installer ? { ...process.env, CGO_ENABLED: "0" } : process.env,
    },
  );
} finally {
  for (const path of owned) await rm(path);
}
