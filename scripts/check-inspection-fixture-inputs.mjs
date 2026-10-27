// Disposable non-activated inputs only; never installs, starts services or signs.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  open,
  readFile,
  readdir,
  rmdir,
} from "node:fs/promises";
import path from "node:path";
import {
  sha,
  sources,
  unchanged,
  save,
} from "./handoff-transport-evidence.mjs";
assert.equal(
  process.env.FASED_INSPECTION_FIXTURE_INPUTS,
  "1",
  "explicit fixture opt-in required",
);
assert.equal(process.platform, "linux", "Linux fixtures only");
assert.ok(process.getuid() > 0, "never run project code as root");
const root = path.resolve(".evidence/p4-inspection-fixture-inputs");
await mkdir(root, { recursive: true, mode: 0o700 });
const attempt = await mkdtemp(path.join(root, "attempt-"));
const inputs = await sources();
await save(path.join(root, "source-inputs.json"), inputs);
const receipt = {
  result: "FAIL",
  fixtureOnly: true,
  evidenceClass:
    "ordinary-user Linux file/checkpoint primitives; ownership substitution SUPPORTING; no installed UID/reboot proof",
  realWallets: false,
  installedExecution: false,
  automaticUpdates: false,
  sourceInputsSHA256: sha(JSON.stringify(inputs)),
  checks: {},
};
const families = [
  "Descriptors",
  "OfflineSource",
  "RoleInputs",
  "Publication",
  "Recovery",
  "NoExecution",
]
  .map((x) => "TestInspectionFixture" + x)
  .sort();
let child;
process.once("SIGTERM", () => {
  receipt.interrupted = true;
  child?.kill("SIGTERM");
});
async function go(args, name) {
  const out = await open(path.join(root, name + ".stdout"), "w"),
    err = await open(path.join(root, name + ".stderr"), "w");
  let status;
  try {
    console.log("Running go " + args.join(" "));
    child = spawn("go", args, {
      env: { ...process.env, FASED_INSPECTION_EVIDENCE: attempt },
      stdio: ["ignore", out.fd, err.fd],
    });
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
  const stdout = await readFile(path.join(root, name + ".stdout"), "utf8");
  assert.equal(status, 0, name + " failed; inspect persistent stdout/stderr");
  return stdout;
}
try {
  const list = await go(
    ["test", "./internal/update", "-list", "^TestInspectionFixture"],
    "discovery",
  );
  assert.deepEqual(
    list
      .split("\n")
      .filter((x) => x.startsWith("TestInspectionFixture"))
      .sort(),
    families,
    "six exact nonzero families required",
  );
  const events = JSON.parse(
    "[" +
      (
        await go(
          [
            "test",
            "-race",
            "-json",
            "-count=1",
            "-timeout",
            "2m",
            "./internal/update",
            "-run",
            "^TestInspectionFixture",
          ],
          "round-trip",
        )
      )
        .trim()
        .split("\n")
        .join(",") +
      "]",
  );
  assert.ok(
    !events.some((x) => x.Action === "fail" || x.Action === "skip"),
    "no failed/skipped tests",
  );
  const passes = events
    .filter((x) => x.Action === "pass" && x.Test)
    .map((x) => x.Test);
  for (const f of families)
    assert.ok(passes.includes(f), "missing family " + f);
  assert.ok(
    passes.filter((x) => x.includes("/")).length > 30,
    "nonzero refusal/recovery cases required",
  );
  const cases = [
    ...new Set(
      events
        .filter((x) => x.Action === "output")
        .flatMap((x) => x.Output.match(/IFP-0[1-8]/g) ?? []),
    ),
  ].sort();
  assert.deepEqual(
    cases,
    Array.from({ length: 8 }, (_, i) => "IFP-0" + (i + 1)),
    "eight actual case markers required",
  );
  const canaries = events
    .filter(
      (x) => x.Action === "output" && x.Output.includes("INSPECTION_EVIDENCE "),
    )
    .map((x) => JSON.parse(x.Output.split("INSPECTION_EVIDENCE ")[1]));
  assert.ok(
    canaries.length > 30,
    "actual financial/readback/cleanup comparisons required",
  );
  for (const c of canaries) {
    assert.equal(c.before, c.after);
    assert.equal(c.installed, 3);
    assert.equal(c.reserved, 4);
    assert.equal(c.cleanup, true);
    assert.equal(c.execution, false);
  }
  receipt.checks.roundTrip = {
    result: "PASS",
    cases,
    passedTests: passes,
    canaryComparisons: canaries.length,
  };
  const coupled = (
    await go(
      [
        "test",
        "-race",
        "-json",
        "-count=1",
        "-timeout",
        "2m",
        "./internal/update",
        "-run",
        "^Test(TrustRehearsal(StrictExpiry|Store.*)|ReleaseAdmission(CompleteUnitAndReadOnlyRestart|StrictEvidenceAndSignatures|FailurePreservesTrustAndFixtureFloors|KeepsUpstreamTrustProgress))$",
      ],
      "coupled",
    )
  )
    .trim()
    .split("\n")
    .map((x) => JSON.parse(x));
  assert.ok(!coupled.some((x) => x.Action === "fail" || x.Action === "skip"));
  const coupledPasses = coupled
    .filter((x) => x.Action === "pass" && x.Test)
    .map((x) => x.Test);
  assert.ok(coupledPasses.length > 10);
  receipt.checks.coupled = { result: "PASS", passedTests: coupledPasses };
  // No installed consumer, controller dependency, socket or executable launch.
  for (const f of [
    "internal/update/inspection_fixture.go",
    "internal/update/inspection_fixture_source.go",
    "internal/update/inspection_fixture_files_linux.go",
    "internal/update/inspection_fixture_files_unsupported.go",
  ]) {
    const text = await readFile(f, "utf8");
    assert.ok(
      !/"(?:os\/exec|net|net\/http|.*\/(?:signer|custodyinstall))"/.test(text),
      "forbidden execution/network/import in " + f,
    );
  }
  await unchanged(inputs);
  assert.deepEqual(
    await readdir(attempt),
    [],
    "owned disposable fixtures remain",
  );
  receipt.result = "PASS";
} finally {
  receipt.cleanup =
    (await readdir(attempt)).length === 0
      ? "PASS"
      : "FAIL: retain and inspect exact attempt directory";
  if (receipt.cleanup === "PASS") await rmdir(attempt);
  await save(path.join(root, "result.json"), receipt);
}
console.log(
  "PASS: six input/offline/checkpoint families, eight IFP cases and coupled regressions; execution disabled.",
);
