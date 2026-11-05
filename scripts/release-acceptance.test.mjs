import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  acceptanceBinding,
  installedAcceptanceChecks,
  validateInstalledAcceptance,
  verifyInstalledAcceptance,
  recordRemoteAcceptance,
  stagedAcceptanceContract,
} from "./release-acceptance.mjs";
const packet = {
  version: 2,
  productVersion: "0.0.777",
  source: "a".repeat(40),
  release: {
    sourceTree: "b".repeat(40),
    buildRef: "refs/heads/main",
    runId: "123",
    runAttempt: "2",
  },
};
const inventory = {
  repositoryId: "1426211857",
  ownerId: "279582504",
  assets: [{ name: "install.sh", size: 1, sha256: "c".repeat(64) }],
};
const receipt = () => ({
  ...acceptanceBinding(packet, inventory),
  result: "PASS",
  fixtureOnly: true,
  mining: false,
  liveFeatures: false,
  automaticUpdates: false,
  automaticOutcome: "applied",
  automaticPolicyRestored: "manual",
  customerActivation: false,
  upgradeRoute: "installed",
  predecessorPacketVersion: 2,
  candidatePacketVersion: 2,
  upgradeApplied: true,
  uiOutcome: "applied",
  cliOutcome: "applied",
  ordinaryUpdateApply: "PASS",
  localBeforeRemote: true,
  remoteTransport: "tailscale-https",
  sshControlSessionRequired: false,
  passwordPrompt: false,
  releasedPredecessor: {
    version: "0.1.6",
    source: "738bd835b8c86c7cf7b3abfb2945240635f51fe7",
    artifactSetSha256:
      "aea00a0d9e1b7ed05a1346428cdf8cc277cbe40f93cfcd86d40fc9b45c559460",
  },
  compatibilityPredecessor: {
    fixtureOnly: true,
    published: false,
    packetVersion: 2,
    productVersion: "0.1.4",
    source: packet.source,
    sourceTree: packet.release.sourceTree,
    runId: packet.release.runId,
    runAttempt: packet.release.runAttempt,
    artifactSetSha256: "d".repeat(64),
  },
  legacyBootstrap: {
    result: "PASS",
    productVersion: "0.1.5",
    predecessorPacketVersion: 1,
    candidatePacketVersion: 2,
    upgradeApplied: true,
    predecessorArtifactSetSha256: "e".repeat(64),
  },
  checks: Object.fromEntries(installedAcceptanceChecks.map((k) => [k, "PASS"])),
});
function zipped(r) {
  return execFileSync(
    "python3",
    [
      "-c",
      "import io,sys,zipfile; b=io.BytesIO(); z=zipfile.ZipFile(b,'w'); z.writestr('installed-acceptance.json',sys.stdin.buffer.read()); z.close(); sys.stdout.buffer.write(b.getvalue())",
    ],
    { input: JSON.stringify(r), timeout: 5000 },
  );
}
test("incomplete, changed and activated journeys cannot authorize publication", () => {
  assert.doesNotThrow(() =>
    validateInstalledAcceptance(receipt(), packet, inventory),
  );
  for (const key of installedAcceptanceChecks) {
    const r = receipt();
    r.checks[key] = "SUPPORTING";
    assert.throws(() => validateInstalledAcceptance(r, packet, inventory));
  }
  for (const change of [
    { runAttempt: "1" },
    { source: "d".repeat(40) },
    { automaticUpdates: true },
    { automaticOutcome: "alreadyCurrent" },
    { automaticPolicyRestored: "automatic" },
    { customerActivation: true },
    { artifactSetSha256: "0".repeat(64) },
    { uiOutcome: "alreadyCurrent" },
    { cliOutcome: "alreadyCurrent" },
    { ordinaryUpdateApply: "DEFERRED_PROFILE_TRANSITION" },
    { result: "SUPPORTING" },
    { localBeforeRemote: false },
    { remoteTransport: "ssh-forward" },
    { sshControlSessionRequired: true },
    { passwordPrompt: true },
    { releasedPredecessor: { version: "0.1.6", source: packet.source } },
  ])
    assert.throws(() =>
      validateInstalledAcceptance(
        { ...receipt(), ...change },
        packet,
        inventory,
      ),
    );
});
test("historical SSH and rebuilt-reader receipts cannot authorize publication", () => {
  const old = receipt();
  for (const field of [
    "releasedPredecessor",
    "localBeforeRemote",
    "remoteTransport",
    "sshControlSessionRequired",
    "passwordPrompt",
  ])
    delete old[field];
  assert.throws(() => validateInstalledAcceptance(old, packet, inventory));
  for (const field of ["version", "source", "artifactSetSha256"]) {
    const value = receipt();
    value.releasedPredecessor[field] = "changed";
    assert.throws(() => validateInstalledAcceptance(value, packet, inventory));
  }
});
test("a legacy bootstrap bridge cannot stand in for ordinary installed application", () => {
  assert.throws(() =>
    validateInstalledAcceptance(
      {
        ...receipt(),
        upgradeRoute: "bootstrap",
        predecessorPacketVersion: 1,
        uiOutcome: "alreadyCurrent",
        cliOutcome: "alreadyCurrent",
        ordinaryUpdateApply: "DEFERRED_PROFILE_TRANSITION",
      },
      packet,
      inventory,
    ),
  );
  for (const field of ["compatibilityPredecessor", "legacyBootstrap"]) {
    const value = receipt();
    delete value[field];
    assert.throws(() => validateInstalledAcceptance(value, packet, inventory));
  }
  for (const change of [
    { published: true },
    { fixtureOnly: false },
    { productVersion: "0.1.5" },
    { source: "f".repeat(40) },
    { sourceTree: "f".repeat(40) },
    { runAttempt: "1" },
    { runId: "124" },
    { artifactSetSha256: "invalid" },
    {
      artifactSetSha256: acceptanceBinding(packet, inventory).artifactSetSha256,
    },
  ]) {
    const value = receipt();
    Object.assign(value.compatibilityPredecessor, change);
    assert.throws(() => validateInstalledAcceptance(value, packet, inventory));
  }
  const value = receipt();
  value.legacyBootstrap.upgradeApplied = false;
  assert.throws(() => validateInstalledAcceptance(value, packet, inventory));
});
test("complete admission includes terminal, private access, authentication, reboot and controlled automatic proof", () => {
  for (const gate of [
    "rootEntry",
    "privateAccess",
    "logoutSignIn",
    "rebootReconnect",
    "controlledAutomatic",
    "legacyBootstrap",
  ])
    assert.ok(installedAcceptanceChecks.includes(gate), gate);
});
test("acceptance comes from exact authenticated attempt artifact, not local JSON", async () => {
  let raw = zipped(receipt()),
    changed = false;
  const api = async (method, url) => {
    assert.equal(method, "GET");
    if (url.endsWith("/jobs"))
      return {
        jobs: [
          {
            name: "installed-acceptance",
            status: "completed",
            conclusion: "success",
          },
        ],
      };
    if (url.endsWith("/artifacts"))
      return {
        artifacts: [
          {
            id: 9,
            name: "ubuntu-amd64-installed-acceptance-2",
            expired: false,
            digest: "sha256:" + createHash("sha256").update(raw).digest("hex"),
            workflow_run: { id: 123, head_sha: packet.source },
          },
        ],
      };
    return {
      id: 123,
      run_attempt: changed ? 1 : 2,
      head_sha: packet.source,
      head_branch: "main",
      event: "workflow_dispatch",
      path: ".github/workflows/release.yml",
      status: "completed",
      conclusion: "success",
      repository: { id: 1426211857, owner: { id: 279582504 } },
    };
  };
  const options = { downloadArtifact: () => raw };
  assert.equal(
    (await verifyInstalledAcceptance(packet, inventory, api, options))
      .runAttempt,
    "2",
  );
  changed = true;
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /exact protected/,
  );
  changed = false;
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, {
      downloadArtifact: () => Buffer.from("edited"),
    }),
    /digest differs/,
  );
  raw = zipped({
    ...receipt(),
    checks: { ...receipt().checks, entry: "FAIL" },
  });
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /Complete matching/,
  );
});

import { stagedWorkflowRun } from "./release-source.mjs";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
test("lost dispatch acknowledgement reconciles without a duplicate build", async (t) => {
  const workspace = mkdtempSync(path.join(tmpdir(), "fased-stage-dispatch-"));
  t.after(() => rmSync(workspace, { recursive: true }));
  let dispatched = 0,
    visible = false;
  const options = {
    source: packet.source,
    version: packet.productVersion,
    workspace,
    progress() {},
    delay: async () => {},
    call(program, args) {
      if (args[0] === "run")
        return JSON.stringify(
          visible
            ? [{ databaseId: 123, headSha: packet.source, status: "completed" }]
            : [],
        );
      dispatched++;
      throw Error("lost dispatch acknowledgement");
    },
  };
  await assert.rejects(stagedWorkflowRun(options), /lost dispatch/);
  await assert.rejects(stagedWorkflowRun(options), /dispatch unresolved/);
  assert.equal(dispatched, 1);
  visible = true;
  assert.equal((await stagedWorkflowRun(options)).databaseId, 123);
  assert.equal(dispatched, 1);
});

test("structured actual Local and independent Remote legs join without synthetic predecessor evidence", async () => {
  const { composeInstalledAcceptance, stagedAcceptanceContract } =
    await import("./release-acceptance.mjs");
  const target = { ...packet, productVersion: "0.1.10" };
  const bind = { ...acceptanceBinding(target, inventory) };
  const base = {
    ...bind,
    result: "PASS",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
  };
  const local = {
    ...base,
    localOnly: true,
    remoteTested: false,
    upgradeApplied: true,
    uiOutcome: "applied",
    cliOutcome: "applied",
    automaticOutcome: "applied",
    automaticPolicyRestored: "manual",
    customerActivation: false,
    releasedPredecessor: {
      version: "0.1.6",
      source: "738bd835b8c86c7cf7b3abfb2945240635f51fe7",
      artifactSetSha256:
        "aea00a0d9e1b7ed05a1346428cdf8cc277cbe40f93cfcd86d40fc9b45c559460",
    },
    checks: Object.fromEntries(
      stagedAcceptanceContract.local.map((k) => [k, "PASS"]),
    ),
  };
  const localSha = createHash("sha256")
    .update(JSON.stringify(local) + "\n")
    .digest("hex");
  const remote = (kind) => ({
    ...base,
    scenario: kind,
    localReceiptSha256: localSha,
    transport: "tailscale-https",
    normalTLS: true,
    ownerAuthMode: "tailscale-device-v1",
    sshControlSessionRequired: false,
    passwordPrompt: false,
    instanceId: (kind === "fresh" ? "F" : "U").repeat(43),
    nodeIdentitySha256: (kind === "fresh" ? "e" : "f").repeat(64),
    checks: Object.fromEntries(
      [
        ...stagedAcceptanceContract.remoteCommon,
        ...stagedAcceptanceContract[
          kind === "fresh" ? "remoteFresh" : "remoteUpgrade"
        ],
      ].map((k) => [k, "PASS"]),
    ),
  });
  const fresh = remote("fresh"),
    upgrade = {
      ...remote("upgrade"),
      predecessor: {
        version: "0.1.9",
        source: "d".repeat(40),
        artifactSetSha256: "e".repeat(64),
        packetVersion: 2,
      },
      upgradeApplied: true,
      uiOutcome: "applied",
      cliOutcome: "applied",
      automaticOutcome: "applied",
      automaticPolicyRestored: "manual",
      customerActivation: false,
    };
  const complete = composeInstalledAcceptance(
    local,
    fresh,
    upgrade,
    target,
    inventory,
    localSha,
  );
  assert.equal(complete.schemaVersion, 2);
  assert.doesNotThrow(() =>
    validateInstalledAcceptance(complete, target, inventory),
  );
  for (const mutate of [
    (r) => {
      r.remoteFresh.checks.publicIPv6Closure = "SUPPORTING";
    },
    (r) => {
      r.remoteUpgrade.predecessor.version = "0.1.10";
    },
    (r) => {
      r.remoteUpgrade.predecessor.source = target.source;
    },
    (r) => {
      r.remoteUpgrade.sshControlSessionRequired = true;
    },
    (r) => {
      r.remoteFresh.normalTLS = false;
    },
    (r) => {
      r.local.checks.uiUpdate = "FAIL";
    },
    (r) => {
      r.localReceiptSha256 = "0".repeat(64);
    },
    (r) => {
      r.remoteUpgrade.artifactSetSha256 = "0".repeat(64);
    },
    (r) => {
      r.remoteUpgrade.upgradeApplied = false;
    },
  ]) {
    const bad = structuredClone(complete);
    mutate(bad);
    assert.throws(() => validateInstalledAcceptance(bad, target, inventory));
  }
  assert.throws(() =>
    composeInstalledAcceptance(local, fresh, null, target, inventory, localSha),
  );
});

test("owner-reviewed Remote proof joins exact Local CI and rejects substitution, edits and wrong actors", async () => {
  const target = { ...packet, productVersion: "0.1.10" };
  const binding = acceptanceBinding(target, inventory);
  const local = {
    ...receipt(),
    ...binding,
    localOnly: true,
    remoteTested: false,
    checks: Object.fromEntries(
      stagedAcceptanceContract.local.map((k) => [k, "PASS"]),
    ),
  };
  const localText = JSON.stringify(local) + "\n";
  const localSha = createHash("sha256").update(localText).digest("hex");
  // Preserve the exact UTF-8 receipt bytes, including their newline.
  const raw = execFileSync(
    "python3",
    [
      "-c",
      "import io,sys,zipfile; b=io.BytesIO(); z=zipfile.ZipFile(b,'w'); z.writestr('installed-acceptance.json',sys.stdin.buffer.read()); z.close(); sys.stdout.buffer.write(b.getvalue())",
    ],
    { input: localText, timeout: 5000 },
  );
  const artifactDigest =
    "sha256:" + createHash("sha256").update(raw).digest("hex");
  const remote = (scenario) => ({
    ...binding,
    result: "PASS",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    scenario,
    localReceiptSha256: localSha,
    transport: "tailscale-https",
    normalTLS: true,
    ownerAuthMode: "tailscale-device-v1",
    sshControlSessionRequired: false,
    passwordPrompt: false,
    instanceId: (scenario === "fresh" ? "F" : "U").repeat(43),
    nodeIdentitySha256: "f".repeat(64),
    checks: Object.fromEntries(
      [
        ...stagedAcceptanceContract.remoteCommon,
        ...stagedAcceptanceContract[
          scenario === "fresh" ? "remoteFresh" : "remoteUpgrade"
        ],
      ].map((k) => [k, "PASS"]),
    ),
  });
  const fresh = remote("fresh"),
    upgrade = {
      ...remote("upgrade"),
      predecessor: {
        version: "0.1.9",
        source: "d".repeat(40),
        artifactSetSha256: "e".repeat(64),
        packetVersion: 2,
      },
      upgradeApplied: true,
      uiOutcome: "applied",
      cliOutcome: "applied",
      automaticOutcome: "applied",
      automaticPolicyRestored: "manual",
      customerActivation: false,
    };
  let actor = { login: "fcode-ai", id: 155309118 },
    comments = [],
    posted = 0;
  const api = async (method, url, data) => {
    if (url === "/user") return actor;
    if (url.endsWith("/jobs"))
      return {
        jobs: [
          {
            name: "installed-acceptance",
            status: "completed",
            conclusion: "success",
          },
        ],
      };
    if (url.endsWith("/artifacts"))
      return {
        artifacts: [
          {
            id: 9,
            name: "ubuntu-amd64-installed-acceptance-2",
            expired: false,
            digest: artifactDigest,
            workflow_run: { id: 123, head_sha: target.source },
          },
        ],
      };
    if (url.includes("/attempts/"))
      return {
        id: 123,
        run_attempt: 2,
        head_sha: target.source,
        head_branch: "main",
        event: "workflow_dispatch",
        path: ".github/workflows/release.yml",
        status: "completed",
        conclusion: "success",
        repository: { id: 1426211857, owner: { id: 279582504 } },
      };
    if (url.includes("/comments?")) return comments;
    if (method === "POST") {
      posted++;
      const c = {
        id: 99,
        user: actor,
        body: data.body,
        commit_id: target.source,
        created_at: "2026-11-04T12:00:00Z",
        updated_at: "2026-11-04T12:00:00Z",
        path: null,
      };
      comments = [c];
      return c;
    }
    if (url.endsWith("/comments/99")) return comments[0];
    assert.fail(`unexpected API ${method} ${url}`);
  };
  const options = { downloadArtifact: () => raw };
  await assert.rejects(
    verifyInstalledAcceptance(target, inventory, api, options),
    /One reviewed/,
  );
  actor = { login: "someone-else", id: 1 };
  await assert.rejects(
    recordRemoteAcceptance(target, inventory, api, fresh, upgrade, options),
    /approved GitHub owner/,
  );
  assert.equal(posted, 0);
  actor = { login: "fcode-ai", id: 155309118 };
  fresh.setupUrl = "https://private.example/#setup=secret-never-publish";
  fresh.uiOutcome = { privateKey: "secret-never-publish" };
  fresh.diagnostics = { privateKey: "secret-never-publish" };
  upgrade.predecessor.privatePath = "/private/owner-recovery";
  const recorded = await recordRemoteAcceptance(
    target,
    inventory,
    api,
    fresh,
    upgrade,
    options,
  );
  assert.equal(recorded.remoteReviewCommentId, "99");
  assert.ok(!comments[0].body.includes("secret-never-publish"));
  assert.ok(!comments[0].body.includes("/private/owner-recovery"));
  assert.ok(!comments[0].body.includes(fresh.instanceId));
  assert.ok(comments[0].body.includes("instanceIdentitySha256"));
  await recordRemoteAcceptance(target, inventory, api, fresh, upgrade, options);
  assert.equal(posted, 1, "reconciliation must not post a duplicate review");
  const verified = await verifyInstalledAcceptance(
    target,
    inventory,
    api,
    options,
  );
  assert.equal(verified.remoteReviewSha256, recorded.remoteReviewSha256);
  const original = structuredClone(comments[0]);
  for (const mutate of [
    (c) => {
      c.user.id = 1;
    },
    (c) => {
      c.commit_id = "e".repeat(40);
    },
    (c) => {
      c.updated_at = "2026-11-04T13:00:00Z";
    },
    (c) => {
      c.body = c.body.replace(localSha, "0".repeat(64));
    },
    (c) => {
      c.body = c.body.replace('"normalTLS":true', '"normalTLS":false');
    },
    (c) => {
      c.body = c.body.replace(
        '"localCIArtifactDigest":"sha256:',
        '"localCIArtifactDigest":"sha256:0',
      );
    },
  ]) {
    comments = [structuredClone(original)];
    mutate(comments[0]);
    await assert.rejects(
      verifyInstalledAcceptance(target, inventory, api, options),
    );
  }
  comments = [original, structuredClone(original)];
  await assert.rejects(
    verifyInstalledAcceptance(target, inventory, api, options),
    /One reviewed/,
  );
});
