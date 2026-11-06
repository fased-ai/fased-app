import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { githubPublisher } from "./publish-ubuntu-release.mjs";
import {
  acceptanceBinding,
  installedAcceptanceChecks,
  validateInstalledAcceptance,
  verifyInstalledAcceptance,
  recordRemoteAcceptance,
  stagedAcceptanceContract,
  attestedInventory,
  verifyCandidateReplay,
} from "./release-acceptance.mjs";
const bundleManifest = JSON.stringify({
  files: { "fased-app": { sha256: "1".repeat(64), bytes: 1 } },
});
const packet = {
  version: 2,
  productVersion: "0.0.777",
  bundleSha256: createHash("sha256").update(bundleManifest).digest("hex"),
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
function installedAgent(target = packet, localSha = "a".repeat(64)) {
  return {
    ...acceptanceBinding(target, inventory),
    localReceiptSha256: localSha,
    mode: "installed-provider",
    profile: "local",
    result: "PASS",
    fixtureOnly: true,
    sandboxed: true,
    shippingArtifactUnmodified: true,
    testCodeOverlay: false,
    realProviderCalls: false,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    engineSha256: "1".repeat(64),
    bundleSha256: target.bundleSha256,
    bundleManifest,
    instanceIdentitySha256: "2".repeat(64),
    evidenceSha256: "3".repeat(64),
    checks: Object.fromEntries(
      stagedAcceptanceContract.installedAgent.map((key) => [key, "PASS"]),
    ),
  };
}
const receipt = () => ({
  ...acceptanceBinding(packet, inventory),
  localReceiptSha256: "a".repeat(64),
  installedAgent: installedAgent(),
  uiOperationId: "1".repeat(32),
  cliOperationId: "2".repeat(32),
  automaticOperationId: "3".repeat(32),
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
  const foreignRemote = receipt();
  foreignRemote.installedAgent.profile = "remote";
  raw = zipped(foreignRemote);
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /Structured Remote instance evidence/,
  );
  raw = zipped(receipt());
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
import { mkdtempSync, rmSync, readFileSync, existsSync } from "node:fs";
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
        "protectedOwnerDevice",
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
        ownerAuthMode: "tailscale-device-v1",
      },
      upgradeApplied: true,
      uiOutcome: "applied",
      cliOutcome: "applied",
      automaticOutcome: "applied",
      automaticPolicyRestored: "manual",
      customerActivation: false,
      uiOperationId: "1".repeat(32),
      cliOperationId: "2".repeat(32),
      automaticOperationId: "3".repeat(32),
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
  // An actual public predecessor retains its existing password account.
  // That is separate from fresh setup and password-free routine Update.
  const preserved = structuredClone(upgrade);
  preserved.ownerAuthMode = "tailscale-device-v1";
  preserved.ownerEnrollment = "existing-owner-ui-v1";
  preserved.predecessor = {
    ...local.releasedPredecessor,
    packetVersion: 2,
    ownerAuthMode: "password-v1",
  };
  preserved.checks.authenticationPreservation = "PASS";
  const preservedComplete = composeInstalledAcceptance(
    local,
    fresh,
    preserved,
    target,
    inventory,
    localSha,
  );
  assert.doesNotThrow(() =>
    validateInstalledAcceptance(preservedComplete, target, inventory),
  );
  for (const mutate of [
    (r) => {
      r.remoteFresh.ownerAuthMode = "existing-password-account-preserved";
    },
    (r) => {
      r.remoteUpgrade.ownerAuthMode = "existing-password-account-preserved";
    },
    (r) => {
      r.remoteUpgrade.predecessor.source = "0".repeat(40);
    },
    (r) => {
      r.remoteUpgrade.predecessor.ownerAuthMode = "tailscale-device-v1";
    },
    (r) => {
      delete r.remoteUpgrade.checks.authenticationPreservation;
    },
    (r) => {
      r.remoteUpgrade.checks.privateAdminRecovery = "SUPPORTING";
    },
    (r) => {
      r.remoteUpgrade.automaticOutcome = "NOT_RUN";
    },
  ]) {
    const bad = structuredClone(preservedComplete);
    mutate(bad);
    assert.throws(() => validateInstalledAcceptance(bad, target, inventory));
  }
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
    (r) => {
      delete r.remoteUpgrade.checks.protectedOwnerDevice;
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

test("owner-reviewed Remote proof joins exact Local CI and rejects substitution, edits and wrong actors", async (t) => {
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
        "protectedOwnerDevice",
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
        ownerAuthMode: "tailscale-device-v1",
      },
      upgradeApplied: true,
      uiOutcome: "applied",
      cliOutcome: "applied",
      automaticOutcome: "applied",
      automaticPolicyRestored: "manual",
      customerActivation: false,
      uiOperationId: "1".repeat(32),
      cliOperationId: "2".repeat(32),
      automaticOperationId: "3".repeat(32),
    };
  let actor = { login: "fcode-ai", id: 155309118 },
    comments = [],
    posted = 0;
  const fixtureAPI = async (method, url, data) => {
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
  t.mock.method(globalThis, "fetch", async (url, init) => {
    const address = new URL(url);
    assert.equal(address.origin, "https://api.github.com");
    const value = await fixtureAPI(
      init.method,
      address.pathname + address.search,
      init.body ? JSON.parse(init.body) : undefined,
    );
    return new Response(JSON.stringify(value), {
      headers: { "Content-Type": "application/json" },
    });
  });
  const api = githubPublisher("disposable-publisher-fixture-token");
  await assert.rejects(api("POST", "/user"), /Fixed repository/);
  await assert.rejects(
    api(
      "GET",
      `/repos/fased-ai/fased-app/commits/${target.source}/comments?per_page=101`,
    ),
    /Fixed repository/,
  );
  const options = {
    downloadArtifact: () => raw,
    installedAgent: installedAgent(target, localSha),
  };
  options.installedAgent.profile = "remote";
  options.installedAgent.instanceIdentitySha256 = createHash("sha256")
    .update(upgrade.instanceId)
    .digest("hex");
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
  await assert.rejects(
    recordRemoteAcceptance(
      target,
      inventory,
      api,
      fresh,
      { ...upgrade, cliOperationId: upgrade.uiOperationId },
      options,
    ),
    /Distinct canonical/,
  );
  assert.equal(posted, 0, "duplicate operations must refuse before posting");
  await assert.rejects(
    recordRemoteAcceptance(target, inventory, api, fresh, upgrade, {
      downloadArtifact: options.downloadArtifact,
    }),
    /installed-provider journey/,
  );
  assert.equal(posted, 0, "missing installed agent must refuse before posting");
  options.installedAgent.diagnostics = "secret-never-publish";
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
        '"installed-provider"',
        '"standalone-provider-fixture"',
      );
    },
    (c) => {
      c.body = c.body.replace(
        '"instanceIdentitySha256":"' +
          options.installedAgent.instanceIdentitySha256,
        '"instanceIdentitySha256":"' + "e".repeat(64),
      );
    },
    (c) => {
      c.body = c.body.replace('"version":2,', '"version":1,');
    },
    (c) => {
      c.body = c.body.replace(
        '"cliOperationId":"' + "2".repeat(32),
        '"cliOperationId":"' + "1".repeat(32),
      );
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
  comments = [];
  posted = 0;
  const preserved = structuredClone(upgrade);
  preserved.ownerAuthMode = "tailscale-device-v1";
  preserved.ownerEnrollment = "existing-owner-ui-v1";
  preserved.predecessor = {
    ...local.releasedPredecessor,
    packetVersion: 2,
    ownerAuthMode: "password-v1",
  };
  const legacyRecorded = await recordRemoteAcceptance(
    target,
    inventory,
    api,
    fresh,
    preserved,
    options,
  );
  const legacyVerified = await verifyInstalledAcceptance(
    target,
    inventory,
    api,
    options,
  );
  assert.equal(
    legacyVerified.remoteReviewSha256,
    legacyRecorded.remoteReviewSha256,
  );
  assert.ok(comments[0].body.includes('"ownerAuthMode":"password-v1"'));
  assert.equal(posted, 1);
});

test("installed agent gate rejects standalone, patched, unbound and incomplete evidence", async () => {
  const { validateInstalledAgent } = await import("./release-acceptance.mjs");
  const localSha = "a".repeat(64);
  const proof = installedAgent(packet, localSha);
  assert.doesNotThrow(() =>
    validateInstalledAgent(proof, packet, inventory, localSha),
  );
  assert.throws(() =>
    validateInstalledAgent(undefined, packet, inventory, localSha),
  );
  for (const patch of [
    { mode: "standalone-provider-fixture" },
    { mode: "installed-deterministic" },
    { source: "e".repeat(40) },
    { localReceiptSha256: "b".repeat(64) },
    { engineSha256: "e".repeat(64) },
    { bundleManifest: bundleManifest + "\n" },
    { sandboxed: false },
    { shippingArtifactUnmodified: false },
    { testCodeOverlay: true },
    { realProviderCalls: true },
    { liveFeatures: true },
    { checks: { ...proof.checks, chatSendStop: "HELD" } },
    { checks: { ...proof.checks, restartCredentialPolicy: "OPEN" } },
  ]) {
    assert.throws(() =>
      validateInstalledAgent(
        { ...proof, ...patch },
        packet,
        inventory,
        localSha,
      ),
    );
  }
});

test("accept-remote CLI requires the installed-agent receipt before authentication", async () => {
  const { main } = await import("./release-ubuntu.mjs");
  await assert.rejects(
    main(["accept-remote", "candidate", "fresh", "upgrade"]),
    /installed-agent-receipt/,
  );
});

test("Remote upgrade completion refuses missing or mismatched actual legs", async () => {
  const { completeRemoteUpgrade } = await import("./release-acceptance.mjs");
  const bind = {
    ...acceptanceBinding(packet, inventory),
    localReceiptSha256: "a".repeat(64),
    instanceId: "I".repeat(43),
    nodeIdentitySha256: "b".repeat(64),
  };
  const manual = {
    ...bind,
    result: "PASS",
    scenario: "manual-remote-replacement",
    ownerAuthMode: "existing-password-account-preserved",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    upgradeApplied: true,
    updatePasswordPrompt: false,
    predecessor: {
      version: "0.1.6",
      source: "738bd835b8c86c7cf7b3abfb2945240635f51fe7",
    },
    manifestSha256: "c".repeat(64),
    uiOperationId: "a".repeat(32),
    cliOperationId: "b".repeat(32),
    uiOutcome: "applied",
    cliOutcome: "applied",
    checks: Object.fromEntries(
      [
        "actualPublicPredecessor",
        "authenticationPreservation",
        "uiApply",
        "cliApply",
        "serviceRestart",
        "distinctEntryOperations",
      ].map((k) => [k, "PASS"]),
    ),
  };
  const security = {
    ...bind,
    result: "PASS",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    transport: "tailscale-https",
    normalTLS: true,
    ownerAuthMode: "tailscale-device-v1",
    ownerEnrollment: "existing-owner-ui-v1",
    sshControlSessionRequired: false,
    checks: {
      ...Object.fromEntries(
        stagedAcceptanceContract.remoteCommon.map((k) => [k, "PASS"]),
      ),
      alreadyCurrent: "PASS",
      authenticationPreservation: "PASS",
    },
  };
  const automatic = {
    ...security,
    ownedSSHSessionDuringApply: false,
    browser: {
      result: "PASS",
      instanceId: bind.instanceId,
      version: packet.productVersion,
      fromVersion: "0.1.6",
      automaticOutcome: "applied",
      policyRestored: "manual",
      customerActivation: false,
      browserClosedDuringApply: true,
      preservation: true,
      appliedManifestSha256: manual.manifestSha256,
      automaticOperation: {
        id: "c".repeat(32),
        automatic: true,
        receipt: {
          id: "c".repeat(32),
          phase: "completed",
          manifestSha256: manual.manifestSha256,
        },
      },
    },
    postUpdateState: {
      instanceId: bind.instanceId,
      installedVersion: packet.productVersion,
      activationEnabled: false,
      policy: { mode: "manual" },
      operation: {
        id: "c".repeat(32),
        automatic: true,
        phase: "completed",
        receipt: {
          id: "c".repeat(32),
          phase: "completed",
          manifestSha256: manual.manifestSha256,
        },
      },
    },
  };
  const originalPredecessor = {
    version: 2,
    productVersion: "0.1.6",
    source: manual.predecessor.source,
  };
  assert.equal(
    completeRemoteUpgrade(manual, security, automatic, originalPredecessor)
      .checks.controlledAutomatic,
    "PASS",
  );
  assert.throws(() =>
    completeRemoteUpgrade(manual, security, automatic, {
      ...originalPredecessor,
      version: 1,
    }),
  );
  assert.throws(() =>
    completeRemoteUpgrade(manual, security, automatic, {
      ...originalPredecessor,
      source: "e".repeat(40),
    }),
  );
  for (const mutate of [
    (v) => {
      v.security.checks.publicIPv6Closure = "SUPPORTING";
    },
    (v) => {
      v.security.checks.protectedOwnerDevice = "FAIL";
    },
    (v) => {
      v.security.ownerAuthMode = "existing-password-account-preserved";
    },
    (v) => {
      v.automatic.nodeIdentitySha256 = "d".repeat(64);
    },
    (v) => {
      v.automatic.browser.automaticOperation.id = manual.cliOperationId;
    },
    (v) => {
      v.automatic.browser.browserClosedDuringApply = false;
    },
    (v) => {
      v.automatic.postUpdateState.activationEnabled = true;
    },
    (v) => {
      v.automatic.browser.automaticOperation.receipt.id = "d".repeat(32);
    },
    (v) => {
      v.automatic.postUpdateState.operation.id = "d".repeat(32);
    },
    (v) => {
      v.automatic.postUpdateState.operation.automatic = false;
    },
    (v) => {
      v.automatic.postUpdateState.operation.receipt.manifestSha256 = "e".repeat(
        64,
      );
    },
    (v) => {
      v.manual.checks.authenticationPreservation = "FAIL";
    },
    (v) => {
      v.manual.cliOperationId = v.manual.uiOperationId;
    },
    (v) => {
      delete v.manual.uiOperationId;
    },
    (v) => {
      v.manual.cliOperationId = "not-a-canonical-operation";
    },
  ]) {
    const v = structuredClone({ manual, security, automatic });
    mutate(v);
    assert.throws(() =>
      completeRemoteUpgrade(
        v.manual,
        v.security,
        v.automatic,
        originalPredecessor,
      ),
    );
  }
});

test("acceptance-only replay acquires exact original bytes without a build", async (t) => {
  const config = {
    version: packet.productVersion,
    releaseAuthority: "github-attestation-v1",
    repository: "fased-ai/fased-app",
    repositoryId: inventory.repositoryId,
    ownerId: inventory.ownerId,
  };
  const files = new Map(
    [
      "fased-app-linux-amd64.tar.gz",
      "fased-install-linux-amd64",
      "fased-app-linux-amd64.provenance.sigstore.json",
      "install.sh",
    ].map((name) => [name, Buffer.from(name)]),
  );
  const identity = (name) => ({
    file: name,
    size: files.get(name).length,
    sha256: createHash("sha256").update(files.get(name)).digest("hex"),
  });
  const target = {
    ...packet,
    archive: identity("fased-app-linux-amd64.tar.gz"),
    installer: identity("fased-install-linux-amd64"),
    entry: identity("install.sh"),
    release: {
      ...packet.release,
      tagObject: "",
      provenance: identity("fased-app-linux-amd64.provenance.sigstore.json"),
    },
  };
  files.set("fased-release.json", Buffer.from(JSON.stringify(target)));
  for (const name of [
    "fased-app-linux-amd64.json",
    "verified-provenance.json",
    "expected-provenance.json",
    "provenance-policy.json",
    "first-install-seed.json",
    "build-inputs.json",
    "artifact-inventory.json",
    "signing-inputs.json",
  ])
    files.set(name, Buffer.from("{}"));
  const zip = execFileSync(
    "python3",
    [
      "-c",
      "import io,json,sys,zipfile;b=io.BytesIO();z=zipfile.ZipFile(b,'w');[z.writestr(n,v) for n,v in json.load(sys.stdin)];z.close();sys.stdout.buffer.write(b.getvalue())",
    ],
    {
      input: JSON.stringify([...files].map(([k, v]) => [k, v.toString()])),
      timeout: 5000,
    },
  );
  const run = {
    id: 123,
    run_attempt: 2,
    head_sha: packet.source,
    head_branch: "main",
    event: "workflow_dispatch",
    path: ".github/workflows/release.yml",
    status: "completed",
    conclusion: "failure",
    repository: { id: 1426211857, owner: { id: 279582504 } },
  };
  const producer = {
    name: "ubuntu-amd64",
    status: "completed",
    conclusion: "success",
    steps: [
      {
        name: "Verify original provenance offline and stage public signing inputs",
        conclusion: "success",
      },
    ],
  };
  const artifact = {
    id: 19,
    name: "ubuntu-amd64-release-candidate-2",
    expired: false,
    digest: "sha256:" + createHash("sha256").update(zip).digest("hex"),
    workflow_run: { id: 123, head_sha: packet.source },
  };
  let reads = 0;
  const api = async (method, url) => {
    assert.equal(method, "GET");
    reads++;
    if (url.endsWith("/jobs")) return { jobs: [producer] };
    if (url.endsWith("/artifacts")) return { artifacts: [artifact] };
    return run;
  };
  const common = {
    runId: "123",
    attempt: "2",
    api,
    read: (name) => files.get(path.basename(name)),
  };
  const verified = await verifyCandidateReplay(config, "/retained", common);
  assert.equal(verified.candidateArtifactId, "19");
  assert.equal(verified.buildPerformed, false);
  assert.deepEqual(
    attestedInventory(
      config,
      target,
      files.get("fased-release.json"),
    ).assets.map((a) => a.name),
    [...files.keys()].slice(0, 5),
  );
  const workspace = mkdtempSync(path.join(tmpdir(), "fased-candidate-replay-"));
  t.after(() => rmSync(workspace, { recursive: true }));
  reads = 0;
  const directory = path.join(workspace, "original");
  const acquired = await verifyCandidateReplay(config, directory, {
    runId: "123",
    attempt: "2",
    api,
    downloadArtifact: (id) => {
      assert.equal(id, "19");
      return zip;
    },
  });
  assert.equal(acquired.artifactSetSha256, verified.artifactSetSha256);
  assert.equal(
    reads,
    3,
    "producer and inventory reads are cached during acquisition",
  );
  for (const [name, bytes] of files)
    assert.deepEqual(
      (await import("node:fs")).readFileSync(path.join(directory, name)),
      bytes,
    );
  await assert.rejects(
    verifyCandidateReplay(config, directory, {
      ...common,
      downloadArtifact: () => Buffer.from("changed"),
      execute: () => assert.fail("tampered ZIP executed"),
    }),
    /ZIP digest differs/,
  );
  await assert.rejects(
    verifyCandidateReplay(config, "/retained", { ...common, attempt: "3" }),
    /cannot rebind/,
  );
  await assert.rejects(
    verifyCandidateReplay(config, "/retained", {
      ...common,
      read: (name) =>
        path.basename(name) === "install.sh"
          ? Buffer.from("changed")
          : files.get(path.basename(name)),
    }),
    /asset differs/,
  );
  const entries = [...files].map(([name, bytes]) => [
    name,
    bytes.toString(),
    0o100600,
  ]);
  const malformed = (records, oversized = false) =>
    execFileSync(
      "python3",
      [
        "-c",
        `
import io,json,struct,sys,zipfile
b=io.BytesIO()
with zipfile.ZipFile(b,'w') as z:
 for name,value,mode in json.load(sys.stdin):
  f=zipfile.ZipInfo(name);f.external_attr=mode<<16;z.writestr(f,value)
raw=bytearray(b.getvalue())
if sys.argv[1]=='large':struct.pack_into('<I',raw,raw.index(b'PK\\x01\\x02')+24,128*1024*1024+1)
sys.stdout.buffer.write(raw)
`,
        oversized ? "large" : "normal",
      ],
      { input: JSON.stringify(records), timeout: 5000 },
    );
  for (const [label, records, oversized] of [
    ["extra", [...entries, ["extra.json", "{}", 0o100600]]],
    ["missing", entries.slice(1)],
    ["duplicate", [entries[0], ...entries.slice(0, -1)]],
    ["symlink", [[entries[0][0], "target", 0o120777], ...entries.slice(1)]],
    ["traversal", [["../escape", "{}", 0o100600], ...entries.slice(1)]],
    ["oversized", entries, true],
  ]) {
    const bad = malformed(records, oversized);
    const destination = path.join(workspace, label);
    artifact.digest =
      "sha256:" + createHash("sha256").update(bad).digest("hex");
    await assert.rejects(
      verifyCandidateReplay(config, destination, {
        runId: "123",
        attempt: "2",
        api,
        downloadArtifact: () => bad,
      }),
    );
    assert.equal(
      existsSync(destination),
      false,
      "invalid ZIP refuses before writing candidate files",
    );
  }
  artifact.digest = "sha256:" + createHash("sha256").update(zip).digest("hex");
  await assert.rejects(
    verifyCandidateReplay(config, directory, {
      runId: "123",
      attempt: "2",
      api,
      downloadArtifact: () => zip,
    }),
  );
  assert.deepEqual(
    readFileSync(path.join(directory, "install.sh")),
    files.get("install.sh"),
    "existing candidate is never overwritten",
  );
  producer.conclusion = "failure";
  await assert.rejects(
    verifyCandidateReplay(config, "/retained", common),
    /attested build must have succeeded/,
  );
  producer.conclusion = "success";
  run.head_branch = "feature";
  await assert.rejects(
    verifyCandidateReplay(config, "/retained", common),
    /protected candidate producer/,
  );
});

test("authenticated replay proof keeps original package identity and rejects replacement builds", async () => {
  const raw = zipped(receipt());
  const original = {
    id: 123,
    run_attempt: 2,
    head_sha: packet.source,
    head_branch: "main",
    event: "workflow_dispatch",
    path: ".github/workflows/release.yml",
    status: "completed",
    conclusion: "failure",
    repository: { id: 1426211857, owner: { id: 279582504 } },
  };
  const replay = {
    ...original,
    id: 456,
    run_attempt: 1,
    head_sha: "d".repeat(40),
    conclusion: "success",
  };
  const producer = {
    name: "ubuntu-amd64",
    status: "completed",
    conclusion: "success",
    steps: [
      {
        name: "Verify original provenance offline and stage public signing inputs",
        conclusion: "success",
      },
    ],
  };
  const skipped = { name: "ubuntu-amd64", conclusion: "skipped" };
  const artifact = {
    id: 29,
    name: "ubuntu-amd64-installed-acceptance-replay-123-2",
    expired: false,
    digest: "sha256:" + createHash("sha256").update(raw).digest("hex"),
    workflow_run: { id: 456, head_sha: replay.head_sha },
  };
  let duplicates = false,
    stale = false;
  const api = async (method, url) => {
    assert.equal(method, "GET");
    if (url.endsWith("/123/attempts/2/jobs"))
      return {
        jobs: [
          producer,
          {
            name: "installed-acceptance",
            status: "completed",
            conclusion: "failure",
          },
        ],
      };
    if (url.endsWith("/456/attempts/1/jobs"))
      return {
        jobs: [
          skipped,
          {
            name: "installed-acceptance",
            status: "completed",
            conclusion: "success",
          },
        ],
      };
    if (url.includes("/actions/artifacts?"))
      return {
        artifacts: duplicates
          ? [artifact, artifact]
          : stale
            ? [
                {
                  ...artifact,
                  id: 30,
                  workflow_run: { ...artifact.workflow_run, id: 888 },
                },
                artifact,
              ]
            : [artifact],
      };
    if (url.endsWith("/456/artifacts")) return { artifacts: [artifact] };
    if (url.endsWith("/456")) return replay;
    if (url.endsWith("/888"))
      return { ...replay, id: 888, conclusion: "failure" };
    if (url.endsWith("/123/attempts/2")) return original;
    assert.fail("unexpected API " + url);
  };
  const options = {
    downloadArtifact: (id) => {
      assert.equal(id, "29");
      return raw;
    },
  };
  const result = await verifyInstalledAcceptance(
    packet,
    inventory,
    api,
    options,
  );
  assert.equal(result.runId, "123");
  assert.equal(result.runAttempt, "2");
  assert.equal(result.artifactId, "29");
  stale = true;
  assert.equal(
    (await verifyInstalledAcceptance(packet, inventory, api, options))
      .artifactId,
    "29",
    "a failed replay parent does not hide successful proof",
  );
  stale = false;
  skipped.conclusion = "success";
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /must not perform another build/,
  );
  skipped.conclusion = "skipped";
  producer.conclusion = "failure";
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /attested build must have succeeded/,
  );
  producer.conclusion = "success";
  replay.head_branch = "feature";
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /protected acceptance-only/,
  );
  replay.head_branch = "main";
  replay.run_attempt = 2;
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /protected acceptance-only/,
  );
  replay.run_attempt = 1;
  duplicates = true;
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /One retained same-artifact/,
  );
  duplicates = false;
  artifact.workflow_run.head_sha = "e".repeat(40);
  await assert.rejects(
    verifyInstalledAcceptance(packet, inventory, api, options),
    /protected acceptance-only/,
  );
});

test("workflow input preflight fails partial replay modes before either job", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );
  const block = workflow
    .split("  candidate-inputs:\n")[1]
    ?.split("  ubuntu-amd64:\n")[0];
  assert.ok(block, "always-run input preflight exists");
  const script = block
    .split("        run: |\n")[1]
    .split("\n")
    .map((line) => line.slice(10))
    .join("\n");
  assert.match(workflow, /ubuntu-amd64:\n    needs: candidate-inputs/);
  assert.match(
    workflow,
    /installed-acceptance:\n    needs: \[candidate-inputs, ubuntu-amd64\]/,
  );
  const env = {
    ...process.env,
    GITHUB_REF: "refs/heads/main",
    GITHUB_SHA: "a".repeat(40),
    GITHUB_RUN_ATTEMPT: "1",
    FASED_INPUT_SOURCE: "a".repeat(40),
    FASED_REPLAY_RUN_ID: "",
    FASED_REPLAY_RUN_ATTEMPT: "",
  };
  const run = (change) =>
    execFileSync("bash", ["-c", script], {
      env: { ...env, ...change },
      stdio: ["ignore", "pipe", "pipe"],
      timeout: 5000,
    });
  assert.doesNotThrow(() => run({}));
  assert.doesNotThrow(() =>
    run({ FASED_REPLAY_RUN_ID: "123", FASED_REPLAY_RUN_ATTEMPT: "2" }),
  );
  for (const change of [
    { FASED_REPLAY_RUN_ID: "123" },
    { FASED_REPLAY_RUN_ATTEMPT: "2" },
    { FASED_REPLAY_RUN_ID: "0123", FASED_REPLAY_RUN_ATTEMPT: "2" },
    { FASED_REPLAY_RUN_ID: "123", FASED_REPLAY_RUN_ATTEMPT: "0" },
    {
      FASED_REPLAY_RUN_ID: "123",
      FASED_REPLAY_RUN_ATTEMPT: "2",
      GITHUB_RUN_ATTEMPT: "2",
    },
    { FASED_INPUT_SOURCE: "b".repeat(40) },
    { GITHUB_REF: "refs/heads/feature" },
  ])
    assert.throws(() => run(change));
});
