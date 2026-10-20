// Ordinary-user, controlled HTTPS only. Disposable TLS/Ed25519 keys and outputs
// are removed in finally. No installed instance or owner API is contacted.
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { createServer } from "node:https";
import {
  mkdir,
  mkdtemp,
  readFile,
  writeFile,
  rm,
  stat,
  readdir,
} from "node:fs/promises";
import path from "node:path";
import assert from "node:assert/strict";

if (process.getuid?.() === 0) throw new Error("never run project code as root");
const run = promisify(execFile);
const root = path.resolve(
  process.env.FASED_ACQUISITION_EVIDENCE ?? ".evidence/p4-acquisition",
);
const core = path.resolve(
  process.env.FASED_UPDATE_CORE ?? ".evidence/bin/fased-app",
);
const signer = path.resolve(
  process.env.FASED_UPDATE_SIGNER ?? ".evidence/bin/fased-signer",
);
await mkdir(root, { recursive: true });
const fixtureRoot = path.join(root, "https-fixtures");
await mkdir(fixtureRoot, { recursive: true });
const dir = await mkdtemp(path.join(fixtureRoot, "acquisition-"));
const sha = (b) => createHash("sha256").update(b).digest("hex");
const checks = {};
const receipt = {
  result: "FAIL",
  class:
    "ordinary-user compiled CLI with controlled HTTPS and disposable bootstrap trust",
  fixtureOnly: true,
  checks,
  binaries: {},
};
let server;
const pass = (name, condition) => {
  assert(condition, name);
  checks[name] = true;
};
const call = async (args) => {
  try {
    return {
      ...(await run(core, args, { timeout: 25000, maxBuffer: 1 << 20 })),
      code: 0,
    };
  } catch (e) {
    if (typeof e.code !== "number") throw e;
    return { code: e.code, stdout: e.stdout, stderr: e.stderr };
  }
};
try {
  const binaries = await Promise.all([readFile(core), readFile(signer)]);
  [core, signer].forEach((p, i) => {
    receipt.binaries[p] = sha(binaries[i]);
  });
  const compiled = JSON.parse(
    (await call(["updates", "compatibility"])).stdout,
  );
  const contracts = [
    compiled.contract,
    JSON.parse((await run(signer, ["fixture-compatibility"])).stdout),
  ];
  const keyPath = path.join(dir, "tls.key"),
    caPath = path.join(dir, "tls.crt");
  await run("openssl", [
    "req",
    "-x509",
    "-newkey",
    "ed25519",
    "-nodes",
    "-keyout",
    keyPath,
    "-out",
    caPath,
    "-subj",
    "/CN=localhost",
    "-addext",
    "subjectAltName=IP:127.0.0.1",
    "-days",
    "1",
  ]);
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const pub = publicKey.export({ type: "spki", format: "der" }).subarray(-32);
  const keyId = sha(pub),
    now = Math.floor(Date.now() / 1000);
  const trust = {
    version: 1,
    purpose: "fased-app-update-development:v1",
    keys: [
      {
        id: keyId,
        publicKey: pub.toString("base64"),
        notBefore: now - 60,
        notAfter: now + 3600,
      },
    ],
  };
  const trustBytes = JSON.stringify(trust);
  const manifest = {
    version: 1,
    product: "fased-app",
    channel: "development",
    sequence: "2",
    issuedAt: now - 10,
    expiresAt: now + 600,
    target: compiled.target,
    components: contracts.map((contract, i) => ({
      version: "0.0.0-development.1",
      artifact: {
        file: i === 0 ? "fased-app" : "fased-signer",
        size: binaries[i].length,
        sha256: sha(binaries[i]),
      },
      contract,
    })),
  };
  const inventory = {
    version: 1,
    fixtureOnly: true,
    target: compiled.target,
    sequenceFloor: "1",
    states: contracts.flatMap((c) =>
      c.state.map((s) => ({
        name: s.name,
        version: s.versions.min,
        features: s.features,
        anchor: sha(`disposable ${s.name}`),
      })),
    ),
    pending: 3,
    unknown: 2,
    recoveryHold: true,
  };
  const trustPath = path.join(dir, "trust.json"),
    invPath = path.join(dir, "inventory.json");
  await writeFile(trustPath, trustBytes, { mode: 0o600 });
  await writeFile(invPath, JSON.stringify(inventory), { mode: 0o600 });
  let mode = "complete",
    candidate = structuredClone(manifest);
  const requests = [];
  const envelope = () => {
    const payload = JSON.stringify(candidate);
    return JSON.stringify({
      version: 1,
      keyId,
      payload: candidate,
      signature: sign(
        null,
        Buffer.concat([
          Buffer.from("fased-app-update-development:v1\0"),
          Buffer.from(payload),
        ]),
        privateKey,
      ).toString("base64"),
    });
  };
  server = createServer(
    { key: await readFile(keyPath), cert: await readFile(caPath) },
    (req, res) => {
      requests.push(req.url);
      if (req.url === "/release/manifest.json") {
        if (mode === "manifest-redirect") {
          res.writeHead(302, {
            location: "https://untrusted.invalid/manifest.json",
          });
          res.end();
          return;
        }
        if (mode === "oversized") {
          res.writeHead(200);
          res.write(" ");
          res.end("x".repeat(65537));
          return;
        }
        let body = envelope();
        if (mode === "bad-signature")
          body = body.replace('"sequence":"2"', '"sequence":"3"');
        res.end(body);
        return;
      }
      const index =
        req.url === "/release/fased-app"
          ? 0
          : req.url === "/release/fased-signer"
            ? 1
            : -1;
      if (index < 0) {
        res.writeHead(404);
        res.end();
        return;
      }
      if (index === 1 && mode === "artifact-redirect") {
        res.writeHead(307, { location: "/other" });
        res.end();
        return;
      }
      if (index === 1 && mode === "missing") {
        res.writeHead(404);
        res.end();
        return;
      }
      if (index === 1 && mode === "short") {
        res.end(binaries[index].subarray(1));
        return;
      }
      if (index === 1 && mode === "hash") {
        const b = Buffer.from(binaries[index]);
        b[0] ^= 1;
        res.end(b);
        return;
      }
      if (index === 1 && mode === "long-chunked") {
        res.write(binaries[index]);
        res.end("excess");
        return;
      }
      res.end(binaries[index]);
    },
  );
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const origin = `https://127.0.0.1:${server.address().port}`;
  const args = (output) => [
    "updates",
    "acquire",
    "--development",
    "--origin",
    origin,
    "--manifest-url",
    `${origin}/release/manifest.json`,
    "--trust-file",
    trustPath,
    "--trust-sha256",
    sha(trustBytes),
    "--inventory",
    invPath,
    "--output",
    output,
    "--tls-ca-file",
    caPath,
  ];
  const output = path.join(dir, "complete");
  let result = await call(args(output));
  assert.equal(result.code, 0, result.stderr);
  const acquired = JSON.parse(result.stdout),
    plan = acquired.plan;
  pass(
    "signedCompiledPairFromControlledHTTPS",
    acquired.developmentOnly &&
      plan.compatible &&
      plan.artifactsVerified &&
      acquired.trustSha256 === sha(trustBytes),
  );
  pass(
    "allExecutionAuthorityDisabled",
    [
      plan.installAllowed,
      plan.resumeAllowed,
      plan.updaterEnabled,
      plan.walletSigner,
      plan.financialExecution,
    ].every((v) => v === false),
  );
  pass(
    "unknownsHoldsAndAnchorsPreserved",
    plan.pending === 3 &&
      plan.unknown === 2 &&
      plan.recoveryHold &&
      JSON.stringify(plan.preserve) === JSON.stringify(inventory.states),
  );
  pass(
    "privateNonexecutableCompleteOutput",
    (await stat(output)).mode % 512 === 0o700 &&
      (await readdir(output)).length === 6 &&
      (
        await Promise.all(
          (await readdir(output)).map((f) => stat(path.join(output, f))),
        )
      ).every((s) => s.isFile() && s.mode % 512 === 0o600),
  );
  pass(
    "receiptBindsExactEnvelope",
    sha(await readFile(path.join(output, "manifest.json"))) ===
      acquired.envelopeSha256 &&
      JSON.parse(await readFile(path.join(output, "acquisition.json"), "utf8"))
        .plan.manifestSha256 === plan.manifestSha256,
  );
  const inspect = [
    "updates",
    "inspect",
    "--development",
    "--manifest",
    path.join(output, "manifest.json"),
    "--trust-file",
    trustPath,
    "--inventory",
    invPath,
    "--artifact-dir",
    output,
  ];
  pass(
    "existingOfflineVerifierAcceptsSamePair",
    (await call(inspect)).code === 0,
  );
  pass("existingOutputNeverOverwritten", (await call(args(output))).code !== 0);
  await writeFile(path.join(output, "fased-signer"), "corrupt", {
    mode: 0o600,
  });
  pass("receiptDoesNotAuthorizeAlteredBytes", (await call(inspect)).code !== 0);
  for (const name of [
    "trust-substitution",
    "no-development",
    "untrusted-tls",
    "wrong-origin",
    "manifest-redirect",
    "artifact-redirect",
    "expired",
    "replay",
    "bad-signature",
    "missing-pair",
    "short",
    "hash",
    "missing",
    "long-chunked",
    "oversized",
  ]) {
    mode = name;
    candidate = structuredClone(manifest);
    const target = path.join(dir, name),
      argv = args(target),
      count = requests.length;
    if (name === "trust-substitution")
      argv[argv.indexOf("--trust-sha256") + 1] = "0".repeat(64);
    if (name === "no-development")
      argv.splice(argv.indexOf("--development"), 1);
    if (name === "untrusted-tls") argv.splice(argv.indexOf("--tls-ca-file"), 2);
    if (name === "wrong-origin")
      argv[argv.indexOf("--origin") + 1] = "https://untrusted.invalid";
    if (name === "expired") candidate.expiresAt = now - 1;
    if (name === "replay") candidate.sequence = "1";
    if (name === "missing-pair") candidate.components.pop();
    result = await call(argv);
    pass(
      `refuses-${name}`,
      result.code !== 0 &&
        result.stdout === "" &&
        !(await readdir(dir)).includes(name),
    );
    if (["trust-substitution", "no-development", "wrong-origin"].includes(name))
      pass(`no-network-${name}`, requests.length === count);
  }
  pass("redirectsNeverFollowed", !requests.includes("/other"));
  pass(
    "noInstanceOrSignerStateCreated",
    !(await readdir(dir)).some((f) => /sqlite|vault|journal|intent/.test(f)),
  );
  pass(
    "publicInputsUnchanged",
    sha(await readFile(trustPath)) === sha(trustBytes) &&
      sha(await readFile(invPath)) === sha(JSON.stringify(inventory)),
  );
  receipt.result = "PASS";
} catch (error) {
  receipt.error = String(error.stack ?? error);
  process.exitCode = 1;
} finally {
  if (server) {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  }
  await rm(dir, { recursive: true });
  receipt.disposableKeysAndOutputsRemoved = !(await readdir(fixtureRoot))
    .length;
  await writeFile(
    path.join(root, "acquisition-cli-result.json"),
    JSON.stringify(receipt, null, 2) + "\n",
  );
  await writeFile(
    path.join(root, "acquisition-cli.log"),
    Object.keys(checks)
      .map((c) => `PASS ${c}`)
      .join("\n") + (receipt.error ? `\n${receipt.error}\n` : "\n"),
  );
}
console.log(
  `${receipt.result}: ${Object.keys(checks).length} controlled HTTPS acquisition checks`,
);
