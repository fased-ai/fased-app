// Non-installed fixture model only. No installed RPC, service or UID provisioning.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  open,
  readFile,
  readdir,
  rmdir,
  writeFile,
} from "node:fs/promises";
import path from "node:path";
import {
  sha,
  sources,
  unchanged,
  save,
} from "./handoff-transport-evidence.mjs";
assert.equal(
  process.env.FASED_INSTALLED_ADMISSION_MODEL,
  "1",
  "explicit model opt-in required",
);
assert.equal(
  process.platform,
  "linux",
  "ordinary-user Linux model; no platform emulation",
);
assert.ok(process.getuid() > 0, "never execute project code as root");
const root = path.resolve(".evidence/p4-installed-admission-model");
await mkdir(root, { recursive: true, mode: 0o700 });
const required = [
  "Compatibility",
  "Authority",
  "Review",
  "Recovery",
  "Bridge",
  "NoExecution",
]
  .map((x) => `TestInstalledAdmission${x}Model`)
  .sort();
const coupled = [
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
  evidenceClass:
    "ordinary-user non-installed model; disposable keys/inert artifacts/controlled HTTPS",
  installedHelperUIDAcceptance:
    "BLOCKED: IA-16 model assertions do not exercise installed helpers or real role UIDs",
  productionVersions: "UNASSIGNED/BLOCKED",
  realWallets: false,
  installedExecution: false,
  automaticUpdates: false,
  checks: {},
};
const attempt = await mkdtemp(path.join(root, "attempt-"));
const env = { ...process.env, FASED_HANDOFF_EVIDENCE: attempt };
const inputs = await sources();
await save(path.join(root, "model-source-inputs.json"), inputs);
receipt.sourceInputsSHA256 = sha(JSON.stringify(inputs));
let child;
process.once("SIGTERM", () => {
  receipt.interrupted = true;
  child?.kill("SIGTERM");
});
async function go(args, prefix) {
  const out = await open(path.join(root, prefix + ".stdout"), "w");
  const err = await open(path.join(root, prefix + ".stderr"), "w");
  let status;
  try {
    console.log("Running " + args.join(" "));
    child = spawn("go", args, { env, stdio: ["ignore", out.fd, err.fd] });
    const timer = setTimeout(() => child?.kill("SIGTERM"), 180000);
    try {
      status = await new Promise((resolve, reject) => {
        child.once("error", reject);
        child.once("close", resolve);
      });
    } finally {
      clearTimeout(timer);
      child = undefined;
    }
  } finally {
    await out.close();
    await err.close();
  }
  return {
    status,
    stdout: await readFile(path.join(root, prefix + ".stdout"), "utf8"),
    stderr: await readFile(path.join(root, prefix + ".stderr"), "utf8"),
  };
}
try {
  for (const file of [
    "internal/update/installed_admission_model.go",
    "internal/update/installed_admission_model_test.go",
    "scripts/check-update-installed-admission-model.mjs",
  ])
    await readFile(file);
  const list = await go(
    [
      "test",
      "./internal/update",
      "-list",
      "^(TestInstalledAdmission.*Model|TestFixtureHandoff.*)$",
    ],
    "discovery",
  );
  await writeFile(
    path.join(root, "model-test-list.log"),
    (list.stdout ?? "") + (list.stderr ?? ""),
  );
  assert.equal(list.status, 0, "model test discovery failed");
  assert.deepEqual(
    list.stdout
      .split("\n")
      .filter((x) => x.startsWith("TestInstalledAdmission"))
      .sort(),
    required,
    "six exact nonzero model families required",
  );
  assert.deepEqual(
    list.stdout
      .split("\n")
      .filter((x) => x.startsWith("TestFixtureHandoff"))
      .sort(),
    coupled,
    "coupled checkpoint/verifier families required",
  );
  // Each connected group has its own finite deadline; combining the new
  // model and existing crypto-heavy suite exceeded the old two-minute budget.
  const events = [];
  for (const [prefix, pattern] of [
    ["model", "^TestInstalledAdmission.*Model$"],
    ["coupled", "^TestFixtureHandoff.*$"],
  ]) {
    const run = await go(
      [
        "test",
        "-race",
        "-json",
        "-count=1",
        "-timeout",
        "2m",
        "./internal/update",
        "-run",
        pattern,
      ],
      prefix,
    );
    assert.equal(
      run.status,
      0,
      `${prefix} matrix failed: ${run.error?.message ?? run.stderr}`,
    );
    events.push(
      ...run.stdout
        .trim()
        .split("\n")
        .map((x) => JSON.parse(x)),
    );
  }
  await writeFile(
    path.join(root, "model-tests.jsonl"),
    events.map((x) => JSON.stringify(x)).join("\n") + "\n",
  );
  assert.equal(
    events.filter((x) => x.Action === "fail" || x.Action === "skip").length,
    0,
    "no failed/skipped model cases",
  );
  const passed = events
    .filter((x) => x.Action === "pass" && x.Test)
    .map((x) => x.Test);
  for (const name of [...required, ...coupled])
    assert.ok(passed.includes(name), `missing completed family ${name}`);
  const leaves = passed.filter(
    (x) => !passed.some((y) => y.startsWith(x + "/")),
  );
  const own = leaves.filter((x) => x.startsWith("TestInstalledAdmission"));
  const ids = (p) =>
    [
      ...new Set(
        own.flatMap((x) =>
          [...x.matchAll(new RegExp(p + "-(\\d{2})", "g"))].map((m) =>
            Number(m[1]),
          ),
        ),
      ),
    ].sort((a, b) => a - b);
  assert.deepEqual(
    ids("IA"),
    Array.from({ length: 16 }, (_, i) => i + 1),
  );
  assert.deepEqual(
    ids("RC"),
    Array.from({ length: 8 }, (_, i) => i + 1),
  );
  const outputs = events
    .filter((x) => x.Action === "output")
    .map((x) => x.Output)
    .join("");
  assert.match(
    outputs,
    /MODEL_CAPABILITIES: realWallets=false installedExecution=false automaticUpdates=false financialCanaries=unchanged reservedFloor=unchanged installedFloor=unchanged/,
  );
  const financial = outputs
    .split("\n")
    .filter((x) => x.includes("MODEL_FINANCIAL "))
    .map((x) => JSON.parse(x.split("MODEL_FINANCIAL ")[1]));
  assert.ok(
    financial.length >= 2,
    "missing actual before/after financial evidence",
  );
  for (const row of financial) {
    assert.equal(row.BeforeCanary, row.AfterCanary);
    assert.equal(row.BeforeInstalled, row.AfterInstalled);
    assert.equal(row.BeforeReserved, row.AfterReserved);
  }
  const fixtures = path.join(attempt, "fixtures");
  assert.deepEqual(
    await readdir(fixtures),
    [],
    "owned disposable fixtures remain",
  );
  await rmdir(fixtures);
  assert.deepEqual(await readdir(attempt), []);
  await rmdir(attempt);
  await unchanged(inputs);
  Object.assign(receipt, {
    result: "PASS",
    entrypoints: required,
    coupledEntrypoints: coupled,
    modelLeaves: own.length,
    coupledLeaves: leaves.length - own.length,
    IA: ids("IA"),
    RC: ids("RC"),
    financialComparisons: financial,
    checks: Object.fromEntries(leaves.map((x) => [x, "PASS"])),
    generatedInputsRemoved: true,
    privateKeys: "memory only",
  });
} finally {
  await save(path.join(root, "model-result.json"), receipt);
}
console.log(
  `Non-installed admission model: ${receipt.result} (${receipt.modelLeaves} model / ${receipt.coupledLeaves} coupled cases; installed UID proof BLOCKED)`,
);
