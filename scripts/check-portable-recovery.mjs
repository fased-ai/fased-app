// Ordinary-user local/Ubuntu checks; generated test keys only. No owner install.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
assert.equal(process.platform, "linux");
assert.ok(process.getuid() > 0);
const root = path.resolve(".evidence/p4-portable-recovery");
await mkdir(root, { recursive: true, mode: 0o700 });
const sha = (b) => createHash("sha256").update(b).digest("hex");
const save = (n, v) =>
  writeFile(path.join(root, n), JSON.stringify(v, null, 2) + "\n");
const run = (args) =>
  spawnSync("go", args, {
    encoding: "utf8",
    timeout: 180000,
    maxBuffer: 24 << 20,
  });
const report = {
  result: "FAIL",
  fixtureOnly: true,
  class:
    "ordinary-user cold encrypted archive/quarantine, actual existing vault/history replay; not authoritative live recovery",
  walletSigner: false,
  financialExecution: false,
  automaticUpdates: false,
  publicActivation: false,
};
try {
  const git = spawnSync(
    "git",
    ["ls-files", "-z", "--cached", "--others", "--exclude-standard"],
    { encoding: "utf8" },
  );
  assert.equal(git.status, 0);
  const sources = {};
  for (const f of git.stdout.split("\0").filter(Boolean).sort())
    sources[f] = sha(await readFile(f));
  await save("source-inputs.json", sources);
  report.sourceInputsSHA256 = sha(JSON.stringify(sources, null, 2) + "\n");
  const pattern =
    "^(TestPortable.*|TestVaultBackupRecoveryAndRestart|TestFixtureRecoveryRetainsLifetimeBudgetAndRevokesGrants|TestColdInspectionNoCreateRepairOrAppend|TestUpdateKeyOnlyRecoveryCannotResetFloorAuthority|TestOwnerBackupFilesExclusiveAndPrivate)$";
  const r = run([
    "test",
    "-race",
    "-json",
    "-count=1",
    "-timeout",
    "2m",
    "./internal/signer",
    "./cmd/fased-signer",
    "-run",
    pattern,
  ]);
  await writeFile(path.join(root, "tests.jsonl"), r.stdout ?? "");
  await writeFile(path.join(root, "stderr.log"), r.stderr ?? "");
  assert.equal(
    r.status,
    0,
    r.stderr || "portable checks failed; see tests.jsonl",
  );
  const events = r.stdout
    .trim()
    .split("\n")
    .map((x) => JSON.parse(x));
  assert.ok(!events.some((x) => x.Action === "skip" || x.Action === "fail"));
  report.passed = events
    .filter((x) => x.Action === "pass" && x.Test)
    .map((x) => x.Test);
  const required = [
    "TestPortableSnapshotColdRoundTrip",
    "TestPortableSnapshotInvalidInputs",
    "TestPortableSnapshotColdSourceRefusals",
    "TestPortableSnapshotQuarantineCuts",
    "TestPortableSnapshotQuarantineConflicts",
    "TestPortableSnapshotConcurrentDestinations",
    "TestPortableCommandRejectsImplicitAndActivationInputs",
    "TestVaultBackupRecoveryAndRestart",
    "TestFixtureRecoveryRetainsLifetimeBudgetAndRevokesGrants",
    "TestColdInspectionNoCreateRepairOrAppend",
    "TestUpdateKeyOnlyRecoveryCannotResetFloorAuthority",
    "TestOwnerBackupFilesExclusiveAndPrivate",
  ].sort();
  assert.deepEqual(
    report.passed.filter((x) => !x.includes("/")).sort(),
    required,
  );
  for (const [f, h] of Object.entries(sources))
    assert.equal(sha(await readFile(f)), h, `source changed: ${f}`);
  report.result = "PASS";
  report.portableAuthority =
    "BLOCKED: only historical encrypted evidence is recoverable; latest state, source retirement and venue reconciliation cannot be inferred";
} finally {
  await save("local-result.json", report);
}
console.log(
  JSON.stringify({
    result: report.result,
    passed: report.passed.length,
    evidence: root,
  }),
);
