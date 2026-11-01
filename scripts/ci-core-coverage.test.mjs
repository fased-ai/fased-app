import { strict as assert } from "node:assert";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { classify, verifyGate } from "./ci-routing.mjs";

test("Ubuntu acceptance executes Remote maintenance and the actual older reader", async () => {
  const workflow = await readFile(
    new URL("../.github/workflows/inspection-acceptance.yml", import.meta.url),
    "utf8",
  );
  assert.match(
    workflow,
    /python3 scripts\/check-inspection-vm\.py --maintenance-only/,
  );
  assert.match(
    workflow,
    /node scripts\/build-inspection-predecessor\.mjs --application-reader/,
  );
  assert.match(
    workflow,
    /FASED_READER_PREDECESSOR:\s+\$\{\{ github\.workspace \}\}\/\.evidence\/b01-connected-install\/reader-predecessor-schema33/,
  );
  assert.match(workflow, /TestApplicationReaderCompiledPredecessorIPC/);
  assert.match(workflow, /\.evidence\/b01-connected-install\/vm\/\*\.json/);

  const compose = workflow.indexOf(
    "      - name: Compose finalizer-produced disposable Remote update",
  );
  const boot = workflow.indexOf(
    "      - name: Actual interruption, kernel reboot and protected recovery",
  );
  assert.ok(compose >= 0 && compose < boot, "compose before long VM acceptance");
  const composition = workflow.slice(
    compose,
    workflow.indexOf("      - ", compose + 10),
  );
  assert.match(
    composition,
    /run: \|\n\s+umask 077\n\s+mkdir -p \.evidence\/b01-finalized-update\/vm\/inputs/,
    "finalizer inputs must start in an owner-private directory",
  );
});

test("full race groups retain every package and require delegated update coverage", async () => {
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const core = workflow.slice(
    workflow.indexOf("  core:\n"),
    workflow.indexOf("  desktop:\n"),
  );
  const handoff = workflow.slice(
    workflow.indexOf("  handoff:\n"),
    workflow.indexOf("  handoff-uid:\n"),
  );
  assert.match(
    core,
    /go list \.\/internal\/\.\.\. \.\/cmd\/fased-app \.\/cmd\/fased-signer \.\/cmd\/fased-install \.\/cmd\/fased-package/,
  );
  assert.match(core, /grep -vE '\/internal\/\(core\|update\)\$'/);
  assert.match(core, /go test -race -p 1 "\$\{fased_other_packages\[@\]\}"/);
  const skip = core.match(
    /go test -race -p 1 \.\/internal\/core -skip '([^']+)'/,
  );
  const run = core.match(
    /go test -race -p 1 \.\/internal\/core -run '([^']+)'/,
  );
  assert.ok(
    skip && run,
    "both complementary groups must run with race detection",
  );
  assert.equal(
    skip[1],
    run[1],
    "skip/run expressions must cover disjoint exhaustive groups",
  );
  assert.match(
    handoff,
    /name: Update unit race coverage\n\s+if: needs.classify.outputs.scope == 'full'\n\s+run: go test -race -p 1 \.\/internal\/update/,
  );
  assert.match(core, /timeout-minutes: 20/);
  for (const path of [
    "cmd/fased-app/host.go",
    "internal/core/onchain.go",
    ".github/workflows/checks.yml",
  ]) {
    const plan = classify([path]);
    assert.equal(plan.scope, "full");
    const needs = {
      documentation: { result: "success" },
      classify: {
        result: "success",
        outputs: Object.fromEntries(
          Object.entries(plan).map(([k, v]) => [k, String(v)]),
        ),
      },
    };
    for (const name of [
      "core",
      "desktop",
      "signer",
      "handoff",
      "handoff-uid",
      "inspection-installed",
    ])
      needs[name] = { result: "success" };
    assert.doesNotThrow(() => verifyGate(needs));
    needs.handoff.result = "skipped";
    assert.throws(() => verifyGate(needs), /required result missing: handoff/);
  }
});
