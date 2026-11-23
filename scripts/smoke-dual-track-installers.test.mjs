import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import https from "node:https";
import os from "node:os";
import path from "node:path";
import {
  createLoopbackFixtureTLS,
  resolveOpenSSLBinary,
  runInstallerCommand,
  startLoopbackArchiveServer,
  verifyChecksumsAndManifest,
} from "./smoke-dual-track-installers.mjs";

const sha256Hex = (buf) => createHash("sha256").update(buf).digest("hex");

test("verifyChecksumsAndManifest validates authentic files and rejects tampering", () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "fased-smoke-sums-"));
  try {
    const fileA = Buffer.from("sample-cli-archive-bytes");
    const fileB = Buffer.from("#!/bin/bash\necho ok\n");
    writeFileSync(path.join(dir, "fased-app-linux-amd64.tar.gz"), fileA);
    writeFileSync(path.join(dir, "install-cli.sh"), fileB);
    writeFileSync(
      path.join(dir, "desktop-release-manifest.json"),
      JSON.stringify({
        schemaVersion: 1,
        version: "0.1.36",
        releaseTag: "v0.1.36-desktop",
        source: "a".repeat(40),
        signed: false,
        assets: {
          "fased-app-linux-amd64.tar.gz": {
            url: "https://github.com/fased-ai/fased-app/releases/download/v0.1.36-desktop/fased-app-linux-amd64.tar.gz",
            sha256: sha256Hex(fileA),
            bytes: fileA.length,
            os: "linux",
            arch: "amd64",
            kind: "cli",
          },
          "install-cli.sh": {
            url: "https://github.com/fased-ai/fased-app/releases/download/v0.1.36-desktop/install-cli.sh",
            sha256: sha256Hex(fileB),
            bytes: fileB.length,
            os: "all",
            arch: "all",
            kind: "installer",
          },
        },
      }),
    );
    writeFileSync(
      path.join(dir, "SHA256SUMS.txt"),
      [
        `${sha256Hex(fileA)}  fased-app-linux-amd64.tar.gz`,
        `${sha256Hex(fileB)}  install-cli.sh`,
      ].join("\n") + "\n",
    );

    const res = verifyChecksumsAndManifest(dir);
    assert.equal(res.version, "0.1.36");
    assert.equal(res.verifiedSumsCount, 2);
    assert.equal(res.verifiedManifestCount, 2);

    // Corrupting the archive must fail closed.
    writeFileSync(
      path.join(dir, "fased-app-linux-amd64.tar.gz"),
      Buffer.from("corrupted"),
    );
    assert.throws(() => verifyChecksumsAndManifest(dir), /SHA-256 mismatch/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("createLoopbackFixtureTLS and startLoopbackArchiveServer serve exact and tampered payloads cleanly", async () => {
  const workDir = mkdtempSync(path.join(os.tmpdir(), "fased-smoke-tls-"));
  let server = null;
  try {
    const tls = createLoopbackFixtureTLS(workDir);
    const archiveBytes = Buffer.from("fased-archive-payload-for-tls-test");
    server = await startLoopbackArchiveServer({
      keyPem: tls.keyPem,
      certPem: tls.certPem,
      archiveName: "test-pkg.tar.gz",
      archiveBytes,
    });

    const getBytes = (reqPath) =>
      new Promise((resolve, reject) => {
        const req = https.get(
          {
            host: "127.0.0.1",
            port: server.port,
            path: reqPath,
            ca: tls.certPem,
          },
          (res) => {
            const chunks = [];
            res.on("data", (c) => chunks.push(c));
            res.on("end", () =>
              resolve({ status: res.statusCode, body: Buffer.concat(chunks) }),
            );
          },
        );
        req.on("error", reject);
      });

    const good = await getBytes("/test-pkg.tar.gz");
    assert.equal(good.status, 200);
    assert.deepEqual(good.body, archiveBytes);

    const bad = await getBytes("/tampered/test-pkg.tar.gz");
    assert.equal(bad.status, 200);
    assert.notDeepEqual(bad.body, archiveBytes);
    assert.equal(bad.body.length, archiveBytes.length);

    // Verify runInstallerCommand does not block the event loop hosting the HTTPS server.
    const scriptPath = path.join(
      workDir,
      process.platform === "win32" ? "fetch-child.ps1" : "fetch-child.sh",
    );
    const outPath = path.join(workDir, "fetched.bin");
    if (process.platform === "win32") {
      writeFileSync(
        scriptPath,
        [
          `$out = '${outPath.replaceAll("'", "''")}'`,
          `$ca = '${tls.certPath.replaceAll("'", "''")}'`,
          `curl.exe --fail --silent --show-error --cacert $ca -o $out 'https://127.0.0.1:${server.port}/test-pkg.tar.gz'`,
          `if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }`,
        ].join("\n"),
        { mode: 0o600 },
      );
    } else {
      writeFileSync(
        scriptPath,
        [
          "#!/usr/bin/env bash",
          "set -euo pipefail",
          `curl --fail --silent --show-error --cacert '${tls.certPath}' -o '${outPath}' 'https://127.0.0.1:${server.port}/test-pkg.tar.gz'`,
        ].join("\n"),
        { mode: 0o700 },
      );
    }
    const childResult = await runInstallerCommand(scriptPath, [], {
      timeoutMs: 15000,
    });
    assert.equal(
      childResult.status,
      0,
      `runInstallerCommand failed: ${childResult.stderr}`,
    );
    assert.equal(childResult.timedOut, false);
    assert.equal(sha256Hex(readFileSync(outPath)), sha256Hex(archiveBytes));
  } finally {
    if (server) await server.close();
    rmSync(workDir, { recursive: true, force: true });
  }
});

test("resolveOpenSSLBinary probes cleanly across platforms without ReferenceError", () => {
  assert.equal(resolveOpenSSLBinary("linux"), "openssl");
  try {
    const bin = resolveOpenSSLBinary("win32");
    assert.ok(typeof bin === "string" && bin.length > 0);
  } catch (err) {
    assert.match(
      String(err),
      /Unable to locate openssl executable on Windows host/,
    );
  }
});
