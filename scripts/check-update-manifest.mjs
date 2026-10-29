// Disposable, ordinary-user CLI acceptance. The ephemeral Ed25519 private key
// stays in memory. No service, wallet, installed state or release is modified.
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import {
  mkdir,
  mkdtemp,
  readFile,
  writeFile,
  rm,
  stat,
  readdir,
} from "node:fs/promises";
import { spawnSync } from "node:child_process";
import path from "node:path";
import assert from "node:assert/strict";

if (process.getuid?.() === 0) throw new Error("never run project code as root");
const root = path.resolve(
  process.env.FASED_UPDATE_EVIDENCE ?? ".evidence/p4-updates",
);
await mkdir(root, { recursive: true });
const fixtures = path.join(root, "cli-fixtures");
await mkdir(fixtures, { recursive: true });
const dir = await mkdtemp(path.join(fixtures, "inspection-"));
const core = path.resolve(
  process.env.FASED_UPDATE_CORE ?? ".evidence/bin/fased-app",
);
const signer = path.resolve(
  process.env.FASED_UPDATE_SIGNER ?? ".evidence/bin/fased-signer",
);
const sha = (b) => createHash("sha256").update(b).digest("hex");
const checks = {};
const receipt = {
  result: "FAIL",
  class: "ordinary-user compiled CLI with disposable signed development inputs",
  fixtureOnly: true,
  financialExecution: false,
  checks,
  binaries: {},
};
const logs = [];
const call = (binary, args) => {
  const r = spawnSync(binary, args, { encoding: "utf8", timeout: 15000 });
  if (r.error) throw r.error;
  return r;
};
const pass = (name, condition) => {
  assert(condition, name);
  checks[name] = true;
  logs.push(`PASS ${name}`);
};

try {
  for (const binary of [core, signer])
    receipt.binaries[binary] = sha(await readFile(binary));
  const c = call(core, ["updates", "compatibility"]);
  const s = call(signer, ["fixture-compatibility"]);
  pass("compiledCompatibilityCommands", c.status === 0 && s.status === 0);
  const compiled = JSON.parse(c.stdout);
  const contracts = [compiled.contract, JSON.parse(s.stdout)];
  pass(
    "compiledCapabilitiesDisabled",
    compiled.walletSigner === false &&
      compiled.financialExecution === false &&
      compiled.updaterEnabled === false,
  );
  pass(
    "schemaPauseAndApprovalFeatures",
    contracts[0].state[0].versions.min === 29 &&
      contracts[0].state[0].versions.max === 29 &&
      contracts[0].state[0].features.includes("agents-task-analysis-v1") &&
      contracts[0].state[0].features.includes("agents-playbooks-v1") &&
      contracts[0].state[0].features.includes("agents-cron-v1") &&
      contracts[0].state[0].features.includes("agents-history-v1") &&
      contracts[0].state[0].features.includes("agents-recall-v1") &&
      contracts[0].state[0].features.includes("agents-chat-permission-v1") &&
      contracts[0].state[0].features.includes("agents-calendar-v1") &&
      contracts[0].state[0].features.includes("agents-ai-accounts-v1") &&
      contracts[0].state[0].features.includes("agents-current-models-v1") &&
      contracts[0].state[0].features.includes("agents-native-providers-v1") &&
      contracts[0].state[0].features.includes("agents-signin-plan-v2") &&
      contracts[0].state[0].features.includes("agents-local-v1") &&
      contracts[0].state[0].features.includes("agents-wen-paper-v1") &&
      contracts[0].state[0].features.includes("wen-research-v1") &&
      contracts[0].state[0].features.includes("wen-research-turn-v1") &&
      contracts[0].state[0].features.includes("wen-research-continuation-v2") &&
      contracts[0].state[0].features.includes("wen-session-rehearsal-v1") &&
      contracts[0].state[0].features.includes("wen-session-fixture-auto-v1") &&
      contracts[0].state[0].features.includes("core-pause-v1") &&
      contracts[0].lifecycle.entrypoint === 4 &&
      contracts[1].state
        .find((s) => s.name === "operations")
        .features.includes("approvals-v1") &&
      contracts[1].state
        .find((s) => s.name === "operations")
        .features.includes("lifecycle-v2-v1") &&
      contracts[1].state
        .find((s) => s.name === "operations")
        .features.includes("install-completion-v1"),
  );
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const pub = publicKey.export({ type: "spki", format: "der" }).subarray(-32);
  const keyId = sha(pub);
  const now = Math.floor(Date.now() / 1000);
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
  const manifest = {
    version: 1,
    product: "fased-app",
    channel: "development",
    sequence: "2",
    issuedAt: now - 10,
    expiresAt: now + 600,
    target: compiled.target,
    components: [],
  };
  // Both binary files must be in this explicitly supplied artifact directory.
  assert.equal(path.dirname(core), path.dirname(signer));
  for (const [i, binary] of [core, signer].entries())
    manifest.components.push({
      version: "0.0.0-development.1",
      artifact: {
        file: path.basename(binary),
        size: (await stat(binary)).size,
        sha256: receipt.binaries[binary],
      },
      contract: contracts[i],
    });
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
        anchor: sha(Buffer.from(`disposable ${s.name}`)),
      })),
    ),
    pending: 3,
    unknown: 2,
    recoveryHold: true,
  };
  const signEnvelope = (m) => ({
    version: 1,
    keyId,
    payload: m,
    signature: sign(
      null,
      Buffer.concat([
        Buffer.from("fased-app-update-development:v1\0"),
        Buffer.from(JSON.stringify(m)),
      ]),
      privateKey,
    ).toString("base64"),
  });
  const mp = path.join(dir, "manifest.json");
  const tp = path.join(dir, "trust.json");
  const ip = path.join(dir, "inventory.json");
  const args = [
    "updates",
    "inspect",
    "--development",
    "--manifest",
    mp,
    "--trust-file",
    tp,
    "--inventory",
    ip,
    "--artifact-dir",
    path.dirname(core),
  ];
  const write = (file, obj) =>
    writeFile(file, JSON.stringify(obj), { mode: 0o600 });
  await write(tp, trust);
  await write(mp, signEnvelope(manifest));
  await write(ip, inventory);
  // Stand-ins for durable vault/journals/counters: the checker must not open or
  // modify these. These bytes are not a portable-history recovery proof.
  for (const s of inventory.states)
    await writeFile(path.join(dir, s.name), `disposable ${s.name}`, {
      mode: 0o600,
    });
  const before = {};
  for (const file of await readdir(dir))
    before[file] = sha(await readFile(path.join(dir, file)));
  let r = call(core, args);
  if (r.status !== 0) throw new Error(r.stderr);
  const p = JSON.parse(r.stdout);
  pass("signedCompiledPairAccepted", p.compatible && p.artifactsVerified);
  pass(
    "neverAuthorizesInstallOrResume",
    p.installAllowed === false &&
      p.resumeAllowed === false &&
      p.updaterEnabled === false &&
      p.walletSigner === false &&
      p.financialExecution === false,
  );
  pass(
    "heldUnknownOriginalsPreserved",
    p.pending === 3 &&
      p.unknown === 2 &&
      p.recoveryHold === true &&
      JSON.stringify(p.preserve) === JSON.stringify(inventory.states),
  );
  pass(
    "reconciliationRequired",
    p.requiredActions.includes(
      "pause-new-operations-and-reconcile-originals",
    ) && p.requiredActions.includes("keep-recovery-hold"),
  );
  for (const [file, hash] of Object.entries(before))
    assert.equal(sha(await readFile(path.join(dir, file))), hash);
  pass(
    "inspectionDoesNotWriteInputs",
    (await readdir(dir)).length === Object.keys(before).length,
  );
  r = call(
    core,
    args.filter((a) => a !== "--development"),
  );
  pass("explicitDevelopmentOptInRequired", r.status !== 0 && r.stdout === "");
  const reject = (name) => {
    const r = call(core, args);
    pass(name, r.status !== 0 && r.stdout === "");
  };
  for (const [name, edit] of [
    [
      "schema14CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 14, max: 14 };
      },
    ],
    [
      "wenFixtureAutomaticFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "wen-session-fixture-auto-v1",
          );
      },
    ],
    [
      "schema6CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 6, max: 6 };
      },
    ],
    [
      "schema13CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 13, max: 13 };
      },
    ],
    [
      "wenSessionRehearsalFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "wen-session-rehearsal-v1",
          );
      },
    ],
    [
      "schema12CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 12, max: 12 };
      },
    ],
    [
      "wenResearchContinuationFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "wen-research-continuation-v2",
          );
      },
    ],
    [
      "schema9CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 9, max: 9 };
      },
    ],
    [
      "wenPaperFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-wen-paper-v1",
          );
      },
    ],
    [
      "schema8CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 8, max: 8 };
      },
    ],
    [
      "agentAdmissionFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-ai-admission-v1",
          );
      },
    ],
    [
      "schema7CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 7, max: 7 };
      },
    ],
    [
      "schema16CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 16, max: 16 };
      },
    ],
    [
      "schema17CoreReaderDenied",
      (m) => {
        m.components[0].contract.state[0].versions = { min: 17, max: 17 };
      },
    ],
    [
      "cronFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-cron-v1",
          );
      },
    ],
    [
      "historyFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-history-v1",
          );
      },
    ],
    [
      "chatPermissionFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-chat-permission-v1",
          );
      },
    ],
    [
      "recallFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-recall-v1",
          );
      },
    ],
    [
      "calendarFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-calendar-v1",
          );
      },
    ],
    [
      "agentNewsFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-news-v1",
          );
      },
    ],
    [
      "agentAITaskFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-ai-tasks-v1",
          );
      },
    ],
    [
      "agentAIConnectionFeatureErasureDenied",
      (m) => {
        m.components[0].contract.state[0].features =
          m.components[0].contract.state[0].features.filter(
            (f) => f !== "agents-ai-connections-v1",
          );
      },
    ],
    [
      "expiredManifestDenied",
      (m) => {
        m.expiresAt = now - 1;
      },
    ],
    [
      "futureManifestDenied",
      (m) => {
        m.issuedAt = now + 3600;
      },
    ],
    [
      "publicChannelDenied",
      (m) => {
        m.channel = "stable";
      },
    ],
    [
      "sameProtocolOldSignerDenied",
      (m) => {
        m.components[1].contract.features.pop();
      },
    ],
    [
      "approvalJournalDowngradeDenied",
      (m) => {
        m.components[1].contract.state
          .find((s) => s.name === "operations")
          .features.pop();
      },
    ],
    [
      "peerProtocolMismatchDenied",
      (m) => {
        m.components[1].contract.protocol++;
      },
    ],
    [
      "artifactHashMismatchDenied",
      (m) => {
        m.components[0].artifact.sha256 = "0".repeat(64);
      },
    ],
    [
      "incompleteArtifactDenied",
      (m) => {
        m.components[0].artifact.size++;
      },
    ],
    [
      "missingArtifactDenied",
      (m) => {
        m.components[0].artifact.file = "missing-fixture";
      },
    ],
    [
      "artifactTraversalDenied",
      (m) => {
        m.components[0].artifact.file = "../escape";
      },
    ],
  ]) {
    const m = structuredClone(manifest);
    edit(m);
    await write(mp, signEnvelope(m));
    reject(name);
  }
  await write(mp, signEnvelope(manifest));
  for (const [name, edit] of [
    [
      "sequenceReplayDenied",
      (i) => {
        i.sequenceFloor = "2";
      },
    ],
    [
      "unknownStateFormatDenied",
      (i) => {
        i.states[2].version++;
      },
    ],
    [
      "inventoryFeatureErasureDenied",
      (i) => {
        i.states[2].features.pop();
      },
    ],
    [
      "wrongPlatformDenied",
      (i) => {
        i.target.os = i.target.os === "linux" ? "windows" : "linux";
      },
    ],
    [
      "nonFixtureInventoryDenied",
      (i) => {
        i.fixtureOnly = false;
      },
    ],
  ]) {
    const i = structuredClone(inventory);
    edit(i);
    await write(ip, i);
    reject(name);
  }
  await write(ip, inventory);
  const tampered = signEnvelope(structuredClone(manifest));
  tampered.payload.sequence = "3";
  await write(mp, tampered);
  reject("tamperedSignedPayloadDenied");
  await write(mp, signEnvelope(manifest));
  await write(tp, { ...trust, purpose: "public-release" });
  reject("wrongTrustPurposeDenied");
  await write(tp, trust);
  const duplicate = JSON.stringify(signEnvelope(manifest)).replace(
    '"version":1',
    '"version":1,"version":1',
  );
  await writeFile(mp, duplicate);
  reject("duplicateFieldDenied");
  receipt.result = "PASS";
} catch (error) {
  receipt.error = error.message;
  logs.push(`FAIL ${error.stack}`);
  process.exitCode = 1;
} finally {
  // Exact generated fixture only; no private key was ever serialized.
  await rm(dir, { recursive: true });
  receipt.cleanup =
    "PASS: exact generated public inputs/stand-ins removed; private signing key remained in memory";
  await writeFile(
    path.join(root, "manifest-cli-result.json"),
    JSON.stringify(receipt, null, 2) + "\n",
  );
  await writeFile(path.join(root, "manifest-cli.log"), logs.join("\n") + "\n");
  console.log(
    `${receipt.result}: ${Object.keys(checks).length} development manifest CLI checks; ${receipt.cleanup}`,
  );
}
