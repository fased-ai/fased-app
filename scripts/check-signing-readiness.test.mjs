import test from "node:test";
import assert from "node:assert/strict";
import { evaluateSigningSecrets } from "./check-signing-readiness.mjs";

test("evaluateSigningSecrets reports exact missing sets when empty", () => {
  const res = evaluateSigningSecrets([]);
  assert.equal(res.macosReady, false);
  assert.equal(res.windowsReady, false);
  assert.equal(res.allSignedReady, false);
  assert.deepEqual(res.missingWindows, [
    "WINDOWS_SIGN_CERT_PFX_BASE64",
    "WINDOWS_SIGN_CERT_PASSWORD",
  ]);
  assert.equal(res.missingMacos.length, 4);
});

test("evaluateSigningSecrets accepts keychain profile or Apple ID notarization", () => {
  const keychainRes = evaluateSigningSecrets([
    "APPLE_CERT_P12_BASE64",
    "APPLE_CERT_PASSWORD",
    "APPLE_SIGNING_IDENTITY",
    "APPLE_NOTARY_KEYCHAIN_PROFILE",
  ]);
  assert.equal(keychainRes.macosReady, true);
  assert.equal(keychainRes.windowsReady, false);
  assert.equal(keychainRes.allSignedReady, false);

  const fullRes = evaluateSigningSecrets([
    "APPLE_CERT_P12_BASE64",
    "APPLE_CERT_PASSWORD",
    "APPLE_SIGNING_IDENTITY",
    "APPLE_ID",
    "APPLE_TEAM_ID",
    "APPLE_APP_PASSWORD",
    "WINDOWS_SIGN_CERT_PFX_BASE64",
    "WINDOWS_SIGN_CERT_PASSWORD",
  ]);
  assert.deepEqual(fullRes, {
    macosReady: true,
    windowsReady: true,
    allSignedReady: true,
    linuxDesktopSigned: false,
    linuxSigningNote:
      "Linux desktop/CLI uses GitHub Sigstore provenance attestation (no GPG/APT repo signing yet)",
    missingMacos: [],
    missingWindows: [],
  });
});
