// Maintainer-only local/Ubuntu rehearsal. No installed instance, root process,
// production credential, public endpoint, publisher or updater is contacted.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
assert.ok(process.getuid?.() > 0, "ordinary Linux/macOS user required");
const root = path.resolve(".evidence/p4-production-readiness");
await mkdir(root, { recursive: true, mode: 0o700 });
const scratch = await mkdtemp(path.join(root, "runner-"));
const sha = (b) => createHash("sha256").update(b).digest("hex");
const save = (name, value) =>
  writeFile(path.join(root, name), JSON.stringify(value, null, 2) + "\n");
const run = (command, args, extra = {}) =>
  spawnSync(command, args, {
    encoding: "utf8",
    timeout: 180000,
    maxBuffer: 24 << 20,
    ...extra,
  });
const receipt = {
  result: "FAIL",
  fixtureOnly: true,
  evidenceClass:
    "ordinary-user controlled HTTPS, simulated DNS answers/pinned dial and disposable publisher ledger; real fixture TUF/provenance verification",
  realWallets: false,
  automaticUpdates: false,
  publicActivation: false,
  productionCredentials: false,
};
try {
  const files = run("git", [
    "ls-files",
    "-z",
    "--cached",
    "--others",
    "--exclude-standard",
  ]);
  assert.equal(files.status, 0);
  const sources = {};
  for (const file of files.stdout.split("\0").filter(Boolean).sort())
    sources[file] = sha(await readFile(file));
  await save("source-inputs.json", sources);
  receipt.sourceInputsSHA256 = sha(JSON.stringify(sources, null, 2) + "\n");
  const packages = [
    "./internal/releaseops",
    "./cmd/fased-release-fixture",
    "./internal/update",
  ];
  const pattern =
    "^(TestRelease(Transport.*|Ledger.*|ReadbackSignedJourney|Readiness.*)|TestTrustRehearsalRootAndOnlineRotation)$";
  const result = run("go", [
    "test",
    "-race",
    "-json",
    "-count=1",
    "-timeout",
    "2m",
    ...packages,
    "-run",
    pattern,
  ]);
  await writeFile(path.join(root, "tests.jsonl"), result.stdout ?? "");
  await writeFile(path.join(root, "stderr.log"), result.stderr ?? "");
  assert.equal(
    result.status,
    0,
    result.stderr || "focused rehearsal failed; see tests.jsonl",
  );
  const events = result.stdout
    .trim()
    .split("\n")
    .map((x) => JSON.parse(x));
  assert.ok(
    !events.some((x) => x.Action === "skip" || x.Action === "fail"),
    "no missing/skipped acceptance",
  );
  const passed = events.filter((x) => x.Action === "pass" && x.Test);
  const expected = [
    "TestReleaseTransportExactReadback",
    "TestReleaseTransportRefusals",
    "TestReleaseTransportDNSAndRebinding",
    "TestReleaseTransportBindingLimits",
    "TestReleaseLedgerReservationAndRetry",
    "TestReleaseLedgerFaults",
    "TestReleaseLedgerConcurrencyAndBounds",
    "TestReleaseLedgerReadbackAcknowledgementCuts",
    "TestReleaseLedgerRejectsAuthorityAndPaths",
    "TestReleaseReadbackSignedJourney",
    "TestReleaseReadinessStaticBoundary",
    "TestReleaseReadinessCannotActivate",
    "TestTrustRehearsalRootAndOnlineRotation",
  ].sort();
  assert.deepEqual(
    passed
      .filter((x) => !x.Test.includes("/"))
      .map((x) => x.Test)
      .sort(),
    expected,
  );
  receipt.passed = passed.map((x) => x.Test);
  const binary = path.join(scratch, "fased-release-fixture");
  const build = run("go", [
    "build",
    "-trimpath",
    "-o",
    binary,
    "./cmd/fased-release-fixture",
  ]);
  assert.equal(build.status, 0, build.stderr);
  receipt.binarySHA256 = sha(await readFile(binary));
  const report = run(binary, ["readiness", "--disposable-fixture"]);
  assert.equal(report.status, 0, report.stderr);
  const status = JSON.parse(report.stdout);
  assert.equal(status.result, "BLOCKED");
  assert.equal(status.blockers.length, 8);
  for (const key of [
    "productionAdmission",
    "publicActivation",
    "realWallets",
    "automaticUpdates",
  ])
    assert.equal(status[key], false);
  await save("readiness.json", status);
  for (const args of [
    ["activate", "--disposable-fixture"],
    ["readiness", "--production"],
    ["readiness", "--disposable-fixture", "--approve"],
    ["publish", "--disposable-fixture"],
  ]) {
    const rejected = run(binary, args);
    assert.equal(rejected.status, 1);
    assert.equal(rejected.stdout, "");
  }
  // The fixture tool is deliberately absent from the ordinary package producer.
  const producer = await readFile("scripts/package-native.mjs", "utf8");
  assert.ok(!producer.includes("fased-release-fixture"));
  for (const [file, digest] of Object.entries(sources))
    assert.equal(
      sha(await readFile(file)),
      digest,
      `source changed during frozen verification: ${file}`,
    );
  receipt.result = "PASS";
  receipt.productionReadiness =
    "BLOCKED: eight independent production binding/acceptance groups; fixtures cannot fill them";
} finally {
  await rm(scratch, { recursive: true });
  receipt.scratchRemoved = true;
  await save("result.json", receipt);
}
console.log(
  JSON.stringify({
    result: receipt.result,
    checks: receipt.passed.length,
    productionReadiness: "BLOCKED",
    evidence: root,
  }),
);
