// Ordinary-user model acceptance only. No installed RPC/CLI, UID, wallet or
// execution capability is created. Test private keys remain in memory.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { mkdir, readFile, readdir, writeFile, rmdir } from "node:fs/promises";
import path from "node:path";

assert.notEqual(process.getuid?.(), 0, "never run project code as root");
const root = path.resolve(
  process.env.FASED_HANDOFF_EVIDENCE ?? ".evidence/p4-handoff-model",
);
await mkdir(root, { recursive: true, mode: 0o700 });
const env = { ...process.env, FASED_HANDOFF_EVIDENCE: root };
const required = [
  "Bindings",
  "IndependentInputs",
  "ClockAndReplay",
  "TrustProgress",
  "CASAndRecovery",
  "NoExecution",
]
  .map((x) => `TestFixtureHandoff${x}`)
  .sort();
const receipt = {
  result: "FAIL",
  fixtureOnly: true,
  class:
    "ordinary-user Go model; disposable keys/inert artifacts/controlled HTTPS",
  kernelUIDProof:
    "BLOCKED: HA-03 kernel boundary and HA-16 require later distinct-UID transport harness",
  installedExecution: false,
  automaticUpdates: false,
  realWallets: false,
  checks: {},
};
function sha(b) {
  return createHash("sha256").update(b).digest("hex");
}
function go(args) {
  return spawnSync("go", args, {
    env,
    encoding: "utf8",
    timeout: 180_000,
    maxBuffer: 16 << 20,
  });
}
try {
  const tracked = spawnSync(
    "git",
    ["ls-files", "-z", "--cached", "--others", "--exclude-standard"],
    { encoding: "utf8" },
  );
  assert.equal(tracked.status, 0);
  const sources = {};
  for (const file of tracked.stdout.split("\0").filter(Boolean).sort())
    sources[file] = sha(await readFile(file));
  const inputs = JSON.stringify(sources, null, 2) + "\n";
  await writeFile(path.join(root, "handoff-source-inputs.json"), inputs);
  receipt.sourceInputsSHA256 = sha(inputs);
  const list = go([
    "test",
    "./internal/update",
    "-list",
    "^TestFixtureHandoff",
  ]);
  await writeFile(
    path.join(root, "handoff-test-list.log"),
    (list.stdout ?? "") + (list.stderr ?? ""),
  );
  assert.equal(list.status, 0, "test discovery failed");
  assert.deepEqual(
    list.stdout
      .split("\n")
      .filter((x) => x.startsWith("TestFixtureHandoff"))
      .sort(),
    required,
    "six exact entrypoints required; no vacuous test pass",
  );
  const run = go([
    "test",
    "-race",
    "-json",
    "-timeout",
    "2m",
    "./internal/update",
    "-run",
    "^TestFixtureHandoff",
  ]);
  await writeFile(path.join(root, "handoff-tests.jsonl"), run.stdout ?? "");
  await writeFile(path.join(root, "handoff-stderr.log"), run.stderr ?? "");
  assert.equal(
    run.status,
    0,
    `model tests failed: ${run.error?.message ?? run.stderr}`,
  );
  const events = run.stdout
    .trim()
    .split("\n")
    .map((x) => JSON.parse(x));
  assert.equal(events.filter((x) => x.Action === "fail").length, 0);
  const passed = events
    .filter((x) => x.Action === "pass" && x.Test)
    .map((x) => x.Test);
  for (const name of required)
    assert.ok(passed.includes(name), `missing completed suite ${name}`);
  const leaves = passed.filter(
    (x) => !passed.some((y) => y.startsWith(x + "/")),
  );
  const covered = [
    ...new Set(
      leaves.flatMap((x) =>
        [...x.matchAll(/HA-(\d+)/g)].map((x) => Number(x[1])),
      ),
    ),
  ].sort((a, b) => a - b);
  assert.deepEqual(
    covered,
    Array.from({ length: 15 }, (_, i) => i + 1),
  );
  receipt.entrypoints = required;
  receipt.coveredModelHA = covered;
  receipt.checks = Object.fromEntries(leaves.map((x) => [x, true]));
  const fixtures = path.join(root, "fixtures");
  assert.deepEqual(
    await readdir(fixtures),
    [],
    "generated fixture state retained unexpectedly",
  );
  await rmdir(fixtures);
  receipt.generatedInputsRemoved = true;
  receipt.privateKeys = "memory only";
  // Verification ran after inputs were frozen; changes invalidate the receipt.
  for (const [file, hash] of Object.entries(sources))
    assert.equal(
      sha(await readFile(file)),
      hash,
      `source changed during matrix: ${file}`,
    );
  receipt.result = "PASS";
} finally {
  await writeFile(
    path.join(root, "handoff-model-result.json"),
    JSON.stringify(receipt, null, 2) + "\n",
  );
}
console.log(
  `Fixture owner/signer model: ${receipt.result} (${Object.keys(receipt.checks).length} leaf cases; kernel UID proof deferred)`,
);
