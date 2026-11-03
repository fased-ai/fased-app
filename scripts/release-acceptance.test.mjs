import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  acceptanceBinding,
  installedAcceptanceChecks,
  validateInstalledAcceptance,
  verifyInstalledAcceptance,
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
  ])
    assert.throws(() =>
      validateInstalledAcceptance(
        { ...receipt(), ...change },
        packet,
        inventory,
      ),
    );
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
