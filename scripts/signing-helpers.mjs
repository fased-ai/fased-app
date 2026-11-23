import fs from "node:fs";
import path from "node:path";

const DEFAULT_WINDOWS_TIMESTAMP_URL = "http://timestamp.digicert.com";
const DEFAULT_WINDOWS_KITS_BIN =
  "C:\\Program Files (x86)\\Windows Kits\\10\\bin";

function compareDottedVersionsDesc(a, b) {
  const pa = a.split(".").map((n) => Number.parseInt(n, 10) || 0);
  const pb = b.split(".").map((n) => Number.parseInt(n, 10) || 0);
  const maxLen = Math.max(pa.length, pb.length);
  for (let i = 0; i < maxLen; i++) {
    const va = pa[i] ?? 0;
    const vb = pb[i] ?? 0;
    if (va !== vb) return vb - va;
  }
  return b.localeCompare(a);
}

/** Resolves and validates macOS codesign + notarization settings fail-closed on partial secrets. */
export function resolveMacOSSigningConfig(env = process.env) {
  const identity = String(env.APPLE_SIGNING_IDENTITY || "").trim();
  const p12Base64 = String(env.APPLE_CERT_P12_BASE64 || "").trim();
  const certPassword = String(env.APPLE_CERT_PASSWORD || "");
  const profile = String(env.APPLE_NOTARY_KEYCHAIN_PROFILE || "").trim();
  const keychain = String(env.APPLE_NOTARY_KEYCHAIN || "").trim();
  const appleId = String(env.APPLE_ID || "").trim();
  const teamId = String(env.APPLE_TEAM_ID || "").trim();
  const appPassword = String(env.APPLE_APP_PASSWORD || "");

  if (Boolean(p12Base64) !== Boolean(certPassword)) {
    throw new Error(
      "Partial Apple certificate configuration: set both APPLE_CERT_P12_BASE64 and APPLE_CERT_PASSWORD.",
    );
  }
  if (p12Base64 && !identity) {
    throw new Error(
      "APPLE_CERT_P12_BASE64 and APPLE_CERT_PASSWORD require APPLE_SIGNING_IDENTITY.",
    );
  }

  const notaryTupleCount = [appleId, teamId, appPassword].filter(
    Boolean,
  ).length;
  if (notaryTupleCount > 0 && notaryTupleCount < 3) {
    throw new Error(
      "Partial Apple notarization credentials: set APPLE_ID, APPLE_TEAM_ID, and APPLE_APP_PASSWORD together.",
    );
  }

  const hasAllAppleNotary = notaryTupleCount === 3;
  const notarizationEnabled = Boolean(profile || hasAllAppleNotary);
  if (notarizationEnabled && !identity) {
    throw new Error(
      "Apple notarization configuration requires APPLE_SIGNING_IDENTITY.",
    );
  }

  return {
    enabled: Boolean(identity),
    identity,
    hasCertificateBundle: Boolean(p12Base64 && certPassword),
    p12Base64,
    certPassword,
    notarizationEnabled,
    notaryProfile: profile || (hasAllAppleNotary ? "fased-notary" : ""),
    notaryKeychain: keychain,
    hasAppleNotaryCredentials: hasAllAppleNotary,
  };
}

/** Resolves and validates Windows Authenticode PFX signing settings fail-closed on partial secrets. */
export function resolveWindowsSigningConfig(env = process.env) {
  const pfxPath = String(env.WINDOWS_SIGN_CERT_PFX_PATH || "").trim();
  const pfxBase64 = String(env.WINDOWS_SIGN_CERT_PFX_BASE64 || "").trim();
  const password = String(env.WINDOWS_SIGN_CERT_PASSWORD || "");
  const timestampUrl = String(
    env.WINDOWS_SIGN_TIMESTAMP_URL || DEFAULT_WINDOWS_TIMESTAMP_URL,
  ).trim();

  if (!/^https?:\/\/\S+$/.test(timestampUrl)) {
    throw new Error(
      `Invalid WINDOWS_SIGN_TIMESTAMP_URL: must be an http:// or https:// URL without whitespace.`,
    );
  }

  const hasPfxSource = Boolean(pfxPath || pfxBase64);
  if (hasPfxSource !== Boolean(password)) {
    throw new Error(
      "Partial Windows Authenticode configuration: set both WINDOWS_SIGN_CERT_PFX_PATH (or WINDOWS_SIGN_CERT_PFX_BASE64) and WINDOWS_SIGN_CERT_PASSWORD.",
    );
  }

  return {
    enabled: Boolean(hasPfxSource && password),
    pfxPath,
    pfxBase64,
    password,
    timestampUrl,
  };
}

/** Locates newest Windows SDK x64 signtool.exe or falls back to signtool.exe in PATH. */
export function findWindowsSigntool({
  env = process.env,
  existsSync = fs.existsSync,
  readdirSync = fs.readdirSync,
  kitsBinDir = DEFAULT_WINDOWS_KITS_BIN,
} = {}) {
  const override = String(env.WINDOWS_SIGNTOOL_PATH || "").trim();
  if (override) {
    return override;
  }
  try {
    if (existsSync(kitsBinDir)) {
      const versions = readdirSync(kitsBinDir)
        .filter((name) => /^[0-9]+(?:\.[0-9]+)*$/.test(name))
        .sort(compareDottedVersionsDesc);
      for (const ver of versions) {
        const candidate = path.win32.join(
          kitsBinDir,
          ver,
          "x64",
          "signtool.exe",
        );
        if (existsSync(candidate)) {
          return candidate;
        }
      }
    }
  } catch {
    // Fall through to PATH lookup.
  }
  return "signtool.exe";
}

export function buildMacOSCodesignAppArgs(identity, appPath) {
  return [
    "--deep",
    "--force",
    "--options",
    "runtime",
    "--timestamp",
    "--sign",
    identity,
    appPath,
  ];
}

export function buildMacOSCodesignDmgArgs(identity, dmgPath) {
  return ["--force", "--timestamp", "--sign", identity, dmgPath];
}

export function buildMacOSNotarySubmitArgs({
  dmgPath,
  profile,
  keychain = "",
}) {
  return [
    "notarytool",
    "submit",
    dmgPath,
    "--keychain-profile",
    profile,
    ...(keychain ? ["--keychain", keychain] : []),
    "--wait",
  ];
}

export function buildWindowsSigntoolSignArgs({
  pfxPath,
  password,
  timestampUrl = DEFAULT_WINDOWS_TIMESTAMP_URL,
  targetExe,
}) {
  return [
    "sign",
    "/fd",
    "SHA256",
    "/tr",
    timestampUrl,
    "/td",
    "SHA256",
    "/f",
    pfxPath,
    "/p",
    password,
    targetExe,
  ];
}

export function buildWindowsSigntoolVerifyArgs(targetExe) {
  return ["verify", "/pa", "/v", targetExe];
}
