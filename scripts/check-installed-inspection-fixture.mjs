// Runs unprivileged supporting checks locally; installed proof is Ubuntu CI only.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdir, open, readFile } from "node:fs/promises";
import path from "node:path";
import {
  sources,
  save,
  sha,
  unchanged,
} from "./handoff-transport-evidence.mjs";
assert.equal(process.env.FASED_INSTALLED_INSPECTION_FIXTURE, "1");
assert.equal(process.platform, "linux");
assert.ok(process.getuid() > 0);
const root = path.resolve(".evidence/p4-installed-inspection");
await mkdir(root, { recursive: true, mode: 0o700 });
const inputs = await sources();
await save(path.join(root, "source-inputs.json"), inputs);
const installed = process.env.FASED_INSPECTION_ACCEPTANCE === "1";
if (installed) {
  assert.equal(process.env.GITHUB_ACTIONS, "true");
  assert.match(
    await readFile("/etc/os-release", "utf8"),
    /VERSION_ID="24\.04"/,
  );
}
const receipt = {
  result: "FAIL",
  fixtureOnly: true,
  realWallets: false,
  automaticUpdates: false,
  sourceInputsSHA256: sha(JSON.stringify(inputs)),
  mode: installed
    ? "actual disposable Ubuntu installed adapter and guarded bridge"
    : "local supporting protocol/storage only; no owner installation",
  vmReboot: "BLOCKED: separate boot-capable disposable target required",
  checks: {},
};
let child;
process.once("SIGTERM", () => child?.kill("SIGTERM"));
async function run(bin, args, name, timeout) {
  const out = await open(path.join(root, name + ".stdout"), "w"),
    err = await open(path.join(root, name + ".stderr"), "w");
  try {
    console.log("Running " + name);
    child = spawn(bin, args, {
      env: process.env,
      stdio: ["ignore", out.fd, err.fd],
    });
    const timer = setTimeout(() => child?.kill("SIGTERM"), timeout);
    let status;
    try {
      status = await new Promise((resolve, reject) => {
        child.once("error", reject);
        child.once("close", resolve);
      });
    } finally {
      clearTimeout(timer);
      child = undefined;
    }
    assert.equal(status, 0, name + " failed; inspect persistent logs");
  } finally {
    await out.close();
    await err.close();
  }
}
try {
  await run(
    "go",
    [
      "test",
      "-race",
      "-json",
      "-count=1",
      "-timeout",
      "2m",
      "./internal/update",
      "./internal/core",
      "./internal/custodyinstall",
      "./internal/signer",
      "./cmd/fased-app",
      "./cmd/fased-signer",
      "-run",
      "^(TestInstalledInspection(FrameRefusal|GuardBeforeState|ReviewDeadline)|TestInspection(GuardedUnitsBeforeFence|BridgeFiniteLifecycleContracts|ColdMetadataNoMigration)|TestColdInspection.*|TestBootstrap.*)$",
    ],
    "focused",
    180000,
  );
  const events = (await readFile(path.join(root, "focused.stdout"), "utf8"))
    .trim()
    .split("\n")
    .map((x) => JSON.parse(x));
  assert.ok(!events.some((x) => x.Action === "fail" || x.Action === "skip"));
  const passed = events
    .filter((x) => x.Action === "pass" && x.Test)
    .map((x) => x.Test);
  for (const f of [
    "TestInstalledInspectionFrameRefusal",
    "TestInstalledInspectionGuardBeforeState",
    "TestInspectionColdMetadataNoMigration",
    "TestInspectionGuardedUnitsBeforeFence",
  ])
    assert.ok(passed.includes(f), "missing required test " + f);
  assert.ok(passed.length > 10);
  receipt.checks.focused = { result: "PASS", passedTests: passed };
  if (installed) {
    await run(
      "node",
      ["scripts/check-managed-custody.mjs"],
      "installed",
      210000,
    );
    const proof = JSON.parse(
      await readFile(path.join(root, "installed-result.json"), "utf8"),
    );
    assert.equal(proof.result, "PASS");
    assert.ok(Object.keys(proof.checks).length >= 10);
    receipt.checks.installed = proof;
  }
  await unchanged(inputs);
  receipt.result = installed
    ? "PASS_INSTALLED_ENGINEERING_VM_OPEN"
    : "PASS_SUPPORTING_INSTALLED_CI_PENDING";
} finally {
  await save(path.join(root, "result.json"), receipt);
}
console.log(receipt.result);
