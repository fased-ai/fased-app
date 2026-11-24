import test from "node:test";
import assert from "node:assert/strict";
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

test("resolveMacOSSigningConfig returns disabled defaults when empty", () => {
  const cfg = resolveMacOSSigningConfig({});
  assert.deepEqual(cfg, {
    enabled: false,
    identity: "",
    hasCertificateBundle: false,
    p12Base64: "",
    certPassword: "",
    notarizationEnabled: false,
    notaryProfile: "",
    notaryKeychain: "",
    hasAppleNotaryCredentials: false,
  });
});

test("resolveMacOSSigningConfig supports full cert + Apple ID notarization and explicit profile", () => {
  const cfg = resolveMacOSSigningConfig({
    APPLE_SIGNING_IDENTITY: "Developer ID Application: Fased Inc (TEAM123)",
    APPLE_CERT_P12_BASE64: "AAAA",
    APPLE_CERT_PASSWORD: "secret",
    APPLE_ID: "release@fased.invalid",
    APPLE_TEAM_ID: "TEAM123",
    APPLE_APP_PASSWORD: "app-pw",
    APPLE_NOTARY_KEYCHAIN: "/tmp/signing.keychain-db",
  });
  assert.equal(cfg.enabled, true);
  assert.equal(cfg.hasCertificateBundle, true);
  assert.equal(cfg.notarizationEnabled, true);
  assert.equal(cfg.notaryProfile, "fased-notary");
  assert.equal(cfg.notaryKeychain, "/tmp/signing.keychain-db");

  assert.deepEqual(
    buildMacOSNotarySubmitArgs({
      dmgPath: "fased.dmg",
      profile: cfg.notaryProfile,
      keychain: cfg.notaryKeychain,
    }),
    [
      "notarytool",
      "submit",
      "fased.dmg",
      "--keychain-profile",
      "fased-notary",
      "--keychain",
      "/tmp/signing.keychain-db",
      "--wait",
    ],
  );
  assert.deepEqual(buildMacOSCodesignAppArgs(cfg.identity, "Fased App.app"), [
    "--deep",
    "--force",
    "--options",
    "runtime",
    "--timestamp",
    "--sign",
    cfg.identity,
    "Fased App.app",
  ]);
  assert.deepEqual(buildMacOSCodesignDmgArgs(cfg.identity, "fased.dmg"), [
    "--force",
    "--timestamp",
    "--sign",
    cfg.identity,
    "fased.dmg",
  ]);
});

test("resolveMacOSSigningConfig fails closed on partial secrets", () => {
  assert.throws(() =>
    resolveMacOSSigningConfig({ APPLE_CERT_P12_BASE64: "AAAA" }),
  );
  assert.throws(() =>
    resolveMacOSSigningConfig({
      APPLE_CERT_P12_BASE64: "AAAA",
      APPLE_CERT_PASSWORD: "pw",
    }),
  );
  assert.throws(() =>
    resolveMacOSSigningConfig({
      APPLE_SIGNING_IDENTITY: "Developer ID Application: Test",
      APPLE_ID: "user@example.com",
      APPLE_TEAM_ID: "TEAM1",
    }),
  );
  assert.throws(() =>
    resolveMacOSSigningConfig({ APPLE_NOTARY_KEYCHAIN_PROFILE: "prof" }),
  );
});

test("resolveWindowsSigningConfig handles unsigned, signed, invalid URL, and partial secrets", () => {
  const off = resolveWindowsSigningConfig({});
  assert.equal(off.enabled, false);
  assert.equal(off.timestampUrl, "https://timestamp.digicert.com");

  const on = resolveWindowsSigningConfig({
    WINDOWS_SIGN_CERT_PFX_PATH: "C:\\temp\\cert.pfx",
    WINDOWS_SIGN_CERT_PASSWORD: "pw",
    WINDOWS_SIGN_TIMESTAMP_URL: "https://timestamp.sectigo.com",
  });
  assert.equal(on.enabled, true);
  assert.deepEqual(
    buildWindowsSigntoolSignArgs({
      pfxPath: on.pfxPath,
      password: on.password,
      timestampUrl: on.timestampUrl,
      targetExe: "fased-app.exe",
    }),
    [
      "sign",
      "/fd",
      "SHA256",
      "/tr",
      "https://timestamp.sectigo.com",
      "/td",
      "SHA256",
      "/f",
      "C:\\temp\\cert.pfx",
      "/p",
      "pw",
      "fased-app.exe",
    ],
  );
  assert.deepEqual(buildWindowsSigntoolVerifyArgs("fased-app.exe"), [
    "verify",
    "/pa",
    "/v",
    "fased-app.exe",
  ]);

  assert.throws(() =>
    resolveWindowsSigningConfig({
      WINDOWS_SIGN_CERT_PFX_PATH: "C:\\temp\\cert.pfx",
    }),
  );
  assert.throws(() =>
    resolveWindowsSigningConfig({
      WINDOWS_SIGN_CERT_PASSWORD: "pw",
    }),
  );
  assert.throws(() =>
    resolveWindowsSigningConfig({
      WINDOWS_SIGN_TIMESTAMP_URL: "ftp://bad.invalid/ts",
    }),
  );
});

test("findWindowsSigntool picks highest Windows Kits SDK version or override", () => {
  assert.equal(
    findWindowsSigntool({ env: { WINDOWS_SIGNTOOL_PATH: "D:\\signtool.exe" } }),
    "D:\\signtool.exe",
  );

  const kitsDir = "C:\\Program Files (x86)\\Windows Kits\\10\\bin";
  const existing = new Set([
    kitsDir,
    `${kitsDir}\\10.0.19041.0\\x64\\signtool.exe`,
    `${kitsDir}\\10.0.26100.0\\x64\\signtool.exe`,
  ]);
  const found = findWindowsSigntool({
    env: {},
    kitsBinDir: kitsDir,
    existsSync: (p) => existing.has(p),
    readdirSync: () => [
      "10.0.19041.0",
      "arm64",
      "10.0.22621.0",
      "10.0.26100.0",
    ],
  });
  assert.equal(found, `${kitsDir}\\10.0.26100.0\\x64\\signtool.exe`);
});

test("FASED_REQUIRE_SIGNED enforces fail-closed macOS and Windows signing secrets", () => {
  for (const val of ["1", "true"]) {
    assert.throws(
      () => resolveMacOSSigningConfig({ FASED_REQUIRE_SIGNED: val }),
      /FASED_REQUIRE_SIGNED=1 requires macOS/,
    );
    assert.throws(
      () =>
        resolveMacOSSigningConfig({
          FASED_REQUIRE_SIGNED: val,
          APPLE_SIGNING_IDENTITY: "Developer ID Application: Test",
        }),
      /FASED_REQUIRE_SIGNED=1 requires macOS/,
    );
    assert.throws(
      () => resolveWindowsSigningConfig({ FASED_REQUIRE_SIGNED: val }),
      /FASED_REQUIRE_SIGNED=1 requires Windows/,
    );
  }

  const macOk = resolveMacOSSigningConfig({
    FASED_REQUIRE_SIGNED: "1",
    APPLE_SIGNING_IDENTITY: "Developer ID Application: Test",
    APPLE_CERT_P12_BASE64: "AAAA",
    APPLE_CERT_PASSWORD: "pw",
    APPLE_NOTARY_KEYCHAIN_PROFILE: "fased-notary",
  });
  assert.equal(macOk.enabled, true);
  assert.equal(macOk.notarizationEnabled, true);

  const winOk = resolveWindowsSigningConfig({
    FASED_REQUIRE_SIGNED: "true",
    WINDOWS_SIGN_CERT_PFX_PATH: "C:\\temp\\cert.pfx",
    WINDOWS_SIGN_CERT_PASSWORD: "pw",
  });
  assert.equal(winOk.enabled, true);
  assert.equal(winOk.timestampUrl, "https://timestamp.digicert.com");
});
