// Ordinary-user local peers only. Actual distinct UID acceptance is separate.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import {
  sha,
  sources,
  unchanged,
  save,
} from "./handoff-transport-evidence.mjs";
assert.equal(process.env.FASED_HANDOFF_TRANSPORT_FIXTURE, "1");
assert.equal(process.platform, "linux");
assert.ok(process.getuid() > 0);
const root = path.resolve(".evidence/p4-handoff-transport");
await mkdir(path.join(root, "bin"), { recursive: true, mode: 0o700 });
const names = [
  "Bindings",
  "Framing",
  "IndependentPeers",
  "ReplayRecovery",
  "Bounds",
  "NoExecution",
]
  .map((x) => "TestFixtureTransport" + x)
  .sort();
const receipt = {
  result: "FAIL",
  fixtureOnly: true,
  realWallets: false,
  installedExecution: false,
  automaticUpdates: false,
  kernelUIDProof: "BLOCKED: local peers share one UID",
  checks: {},
};
const inputs = await sources();
await save(path.join(root, "transport-source-inputs.json"), inputs);
receipt.sourceInputsSHA256 = sha(JSON.stringify(inputs));
function go(args, log, timeout = 180000) {
  const r = spawnSync("go", args, {
    encoding: "utf8",
    timeout,
    maxBuffer: 16 << 20,
  });
  return writeFile(
    path.join(root, log),
    (r.stdout ?? "") + (r.stderr ?? ""),
  ).then(() => {
    assert.equal(r.status, 0, `${log}: ${r.error?.message ?? r.stderr}`);
    return r;
  });
}
try {
  const gate = spawnSync(
    "node",
    [
      "--test",
      "--test-isolation=none",
      "--test-reporter=tap",
      "scripts/handoff-transport-evidence.test.mjs",
    ],
    { encoding: "utf8" },
  );
  await writeFile(
    path.join(root, "gate-tests.log"),
    (gate.stdout ?? "") + (gate.stderr ?? ""),
  );
  assert.equal(gate.status, 0, "fixture gate regression failed");
  assert.match(gate.stdout, /# tests 2/);
  assert.match(gate.stdout, /# fail 0/);
  const list = await go(
    ["test", "./internal/update", "-list", "^TestFixtureTransport"],
    "transport-test-list.log",
  );
  assert.deepEqual(
    list.stdout
      .split("\n")
      .filter((x) => x.startsWith("TestFixtureTransport"))
      .sort(),
    names,
  );
  const helper = path.join(root, "bin", "transport-tests");
  const command = path.join(root, "bin", "fased-handoff-fixture");
  await go(
    ["test", "-c", "-race", "-o", helper, "./internal/update"],
    "compile-tests.log",
  );
  await go(
    ["build", "-trimpath", "-o", command, "./cmd/fased-handoff-fixture"],
    "compile-command.log",
  );
  const run = await go(
    [
      "tool",
      "test2json",
      "-t",
      "-p",
      "github.com/fased-ai/fased-app/internal/update",
      helper,
      "-test.v",
      "-test.timeout=120s",
      "-test.run=^TestFixtureTransport",
    ],
    "transport-tests.jsonl",
  );
  const events = run.stdout
    .trim()
    .split("\n")
    .map((x) => JSON.parse(x));
  assert.equal(events.filter((x) => x.Action === "fail").length, 0);
  const passed = events
    .filter((x) => x.Action === "pass" && x.Test)
    .map((x) => x.Test);
  for (const n of names) assert.ok(passed.includes(n));
  const leaves = passed.filter(
    (x) => !passed.some((y) => y.startsWith(x + "/")),
  );
  const cases = [
    ...new Set(
      leaves.flatMap((x) => [...x.matchAll(/UT-(\d+)/g)].map((x) => +x[1])),
    ),
  ].sort((a, b) => a - b);
  assert.deepEqual(
    cases,
    Array.from({ length: 11 }, (_, i) => i + 1),
  );
  receipt.checks = Object.fromEntries(leaves.map((n) => [n, "PASS"]));
  receipt.UIDPredicates = {
    "UT-02": "SUPPORTING",
    "UT-03": "SUPPORTING",
    "UT-12": "BLOCKED",
  };
  receipt.entrypoints = names;
  for (const file of ["cmd/fased-app/main.go", "cmd/fased-signer/main.go"]) {
    const code = await readFile(file, "utf8");
    assert.ok(
      !code.includes("fased-handoff-fixture") &&
        !code.includes("LoadFixtureTransportRole"),
    );
  }
  receipt.binaries = Object.fromEntries(
    await Promise.all(
      [helper, command].map(async (f) => [
        path.relative(process.cwd(), f),
        sha(await readFile(f)),
      ]),
    ),
  );
  await unchanged(inputs);
  receipt.generatedFixtures =
    "PASS: Go test cleanup; disposable nonauthoritative temporary roots";
  receipt.result = "PASS";
} finally {
  await save(path.join(root, "transport-result.json"), receipt);
}
console.log(
  `Fixture transport local: ${receipt.result} (${Object.keys(receipt.checks).length} leaves; actual three-UID proof deferred)`,
);
