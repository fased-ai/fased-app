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
  upgradeRoute: "bootstrap",
  predecessorPacketVersion: 1,
  candidatePacketVersion: 2,
  upgradeApplied: true,
  uiOutcome: "alreadyCurrent",
  cliOutcome: "alreadyCurrent",
  ordinaryUpdateApply: "DEFERRED_PROFILE_TRANSITION",
  checks: Object.fromEntries(installedAcceptanceChecks.map((k) => [k, "PASS"])),
});
function zipped(r) {
  return execFileSync(
    "python3",
    [
      "-c",
      "import io,sys,zipfile; b=io.BytesIO(); z=zipfile.ZipFile(b,'w'); z.writestr('installed-acceptance.json',sys.stdin.buffer.read()); z.close(); sys.stdout.buffer.write(b.getvalue())",
    ],
    { input: JSON.stringify(r) },
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
    { artifactSetSha256: "0".repeat(64) },
    { uiOutcome: "applied" },
    { ordinaryUpdateApply: "PASS" },
  ])
    assert.throws(() =>
      validateInstalledAcceptance(
        { ...receipt(), ...change },
        packet,
        inventory,
      ),
    );
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
