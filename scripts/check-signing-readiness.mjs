import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const MACOS_CERT_SECRETS = [
  "APPLE_CERT_P12_BASE64",
  "APPLE_CERT_PASSWORD",
  "APPLE_SIGNING_IDENTITY",
];
const MACOS_NOTARY_PROFILE = "APPLE_NOTARY_KEYCHAIN_PROFILE";
const MACOS_NOTARY_APPLE_ID = [
  "APPLE_ID",
  "APPLE_TEAM_ID",
  "APPLE_APP_PASSWORD",
];
const WINDOWS_CERT_SECRETS = [
  "WINDOWS_SIGN_CERT_PFX_BASE64",
  "WINDOWS_SIGN_CERT_PASSWORD",
];

/**
 * Evaluate whether GitHub Actions secrets satisfy dual-track-package.yml signed release gates.
 */
export function evaluateSigningSecrets(secretNames) {
  const present = new Set(
    (Array.isArray(secretNames) ? secretNames : []).map((s) =>
      typeof s === "string" ? s : String(s?.name || ""),
    ),
  );

  const missingMacosBase = MACOS_CERT_SECRETS.filter(
    (name) => !present.has(name),
  );
  const hasNotaryProfile = present.has(MACOS_NOTARY_PROFILE);
  const missingAppleIDNotary = MACOS_NOTARY_APPLE_ID.filter(
    (name) => !present.has(name),
  );
  const hasMacosNotary = hasNotaryProfile || missingAppleIDNotary.length === 0;
  const missingMacos = [...missingMacosBase];
  if (!hasMacosNotary) {
    missingMacos.push(
      `${MACOS_NOTARY_PROFILE} OR (${missingAppleIDNotary.join(" + ")})`,
    );
  }

  const missingWindows = WINDOWS_CERT_SECRETS.filter(
    (name) => !present.has(name),
  );
  const macosReady = missingMacosBase.length === 0 && hasMacosNotary;
  const windowsReady = missingWindows.length === 0;

  return {
    macosReady,
    windowsReady,
    allSignedReady: macosReady && windowsReady,
    linuxDesktopSigned: false,
    linuxSigningNote:
      "Linux desktop/CLI uses GitHub Sigstore provenance attestation (no GPG/APT repo signing yet)",
    missingMacos,
    missingWindows,
  };
}

function defaultListSecretNames(repo) {
  const args = ["secret", "list", "--json", "name"];
  if (repo) args.push("--repo", repo);
  const raw = execFileSync("gh", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  const parsed = JSON.parse(raw || "[]");
  return Array.isArray(parsed)
    ? parsed.map((item) => item.name).filter(Boolean)
    : [];
}

function parseArgs(argv) {
  const opts = { repo: "", secretsJson: "", json: false, requireSigned: false };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--repo" && argv[i + 1]) {
      opts.repo = argv[++i];
    } else if (arg === "--secrets-json" && argv[i + 1]) {
      opts.secretsJson = argv[++i];
    } else if (arg === "--json") {
      opts.json = true;
    } else if (arg === "--require-signed") {
      opts.requireSigned = true;
    } else if (arg === "--help" || arg === "-h") {
      console.log(
        "Usage: node scripts/check-signing-readiness.mjs [--repo owner/name] [--secrets-json JSON] [--json] [--require-signed]",
      );
      process.exit(0);
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  return opts;
}

if (
  process.argv[1] &&
  pathToFileURL(process.argv[1]).href === import.meta.url
) {
  try {
    const opts = parseArgs(process.argv.slice(2));
    const names = opts.secretsJson
      ? JSON.parse(opts.secretsJson)
      : defaultListSecretNames(opts.repo);
    const status = evaluateSigningSecrets(names);
    if (opts.json) {
      console.log(JSON.stringify(status, null, 2));
    } else {
      console.log(
        `macOS ready: ${status.macosReady}, Windows ready: ${status.windowsReady}, allSignedReady: ${status.allSignedReady}`,
      );
      if (status.missingMacos.length) {
        console.log(`Missing macOS secrets: ${status.missingMacos.join(", ")}`);
      }
      if (status.missingWindows.length) {
        console.log(
          `Missing Windows secrets: ${status.missingWindows.join(", ")}`,
        );
      }
    }
    if (opts.requireSigned && !status.allSignedReady) {
      process.exit(1);
    }
  } catch (err) {
    console.error(`ERROR: ${err.message}`);
    process.exit(1);
  }
}
