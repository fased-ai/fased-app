import { strict as assert } from "node:assert";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { classify, verifyGate } from "./ci-routing.mjs";

test("Native installed-provider compilation follows the real UI build", async () => {
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const core = workflow.slice(
    workflow.indexOf("  core:\n"),
    workflow.indexOf("  core-scenarios:\n"),
  );
  const prepared = core.indexOf("- run: npm run check");
  const native = core.indexOf(
    "- name: Native protected provider credential runtime",
  );
  const pkg = JSON.parse(
    await readFile(new URL("../package.json", import.meta.url), "utf8"),
  );
  assert.match(pkg.scripts.check, /\bnpm run build\b/);
  assert.ok(
    prepared >= 0 && native > prepared,
    "generate the embedded dist before compiling core.test",
  );
  const harness = await readFile(
    new URL("./check-native-provider-secret.mjs", import.meta.url),
    "utf8",
  );
  assert.match(harness, /"\.\/internal\/core"/);
  assert.match(harness, /TestInstalledProviderFixtureNativeRuntime/);
});

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
  assert.ok(
    compose >= 0 && compose < boot,
    "compose before long VM acceptance",
  );
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

test("full race groups retain every package and require parallel scenarios and delegated update coverage", async () => {
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const core = workflow.slice(
    workflow.indexOf("  core:\n"),
    workflow.indexOf("  core-scenarios:\n"),
  );
  const scenarios = workflow.slice(
    workflow.indexOf("  core-scenarios:\n"),
    workflow.indexOf("  desktop:\n"),
  );
  const handoff = workflow.slice(
    workflow.indexOf("  handoff:\n"),
    workflow.indexOf("  handoff-uid:\n"),
  );
  const desktop = workflow.slice(
    workflow.indexOf("  desktop:\n"),
    workflow.indexOf("  signer:\n"),
  );
  for (const [name, goos, arch, runner] of [
    ["Headless Windows compilation", "windows", "amd64", "Windows"],
    ["Headless macOS compilation", "darwin", "arm64", "macOS"],
  ]) {
    const output = goos === "windows" ? "windows.exe" : "macos";
    const command = `run: GOOS=${goos} GOARCH=${arch} CGO_ENABLED=0 go build -o .evidence/bin/${output} ./cmd/fased-app`;
    assert.ok(
      core.includes(
        `- name: ${name}\n        if: needs.classify.outputs.desktop != 'true'\n        ${command}`,
      ),
      "leaf routes retain both headless compilation targets",
    );
    assert.ok(
      desktop.includes(
        `- name: ${name}\n        if: runner.os == '${runner}'\n        shell: bash\n        ${command}`,
      ),
      "selected platform jobs own the same headless compilation targets",
    );
  }
  assert.equal(classify(["web/src/core.tsx"]).desktop, false);
  assert.equal(classify(["internal/core/rpc_check.go"]).desktop, false);
  assert.match(
    core,
    /go list \.\/internal\/\.\.\. \.\/cmd\/fased-app \.\/cmd\/fased-signer \.\/cmd\/fased-install \.\/cmd\/fased-package/,
  );
  assert.match(core, /grep -vE '\/internal\/\(core\|update\)\$'/);
  assert.match(core, /go test -race -p 1 "\$\{fased_other_packages\[@\]\}"/);
  const skip = core.match(
    /go test -race -p 1 \.\/internal\/core -skip '([^']+)'/,
  );
  const run = scenarios.match(
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
  assert.equal(run[1], "^Test(SwapRecovery|VenueCredential|.*WEN)");
  assert.equal(
    workflow.split(
      "go test -race -p 1 ./internal/core -run '^Test(SwapRecovery|VenueCredential|.*WEN)'",
    ).length - 1,
    1,
    "the complementary partition must execute exactly once",
  );
  assert.doesNotMatch(
    core,
    /go test -race -p 1 \.\/internal\/core -run '\^Test\(SwapRecovery/,
  );
  assert.match(
    scenarios,
    /needs: classify\n\s+if: needs.classify.outputs.scope == 'full'/,
  );
  assert.match(scenarios, /runs-on: ubuntu-24\.04\n\s+timeout-minutes: 10/);
  for (const prerequisite of [
    "actions/checkout@v4",
    "actions/setup-node@v4",
    'node-version: "24"',
    "npm ci",
    "actions/setup-go@v5",
    'go-version: "1.26.x"',
    "npm run build",
  ])
    assert.ok(
      scenarios.includes(prerequisite),
      `retain scenario prerequisite: ${prerequisite}`,
    );
  const aggregate = workflow.slice(workflow.indexOf("  prototype:\n"));
  assert.match(aggregate, /\n\s+core-scenarios,/);
  assert.match(
    handoff,
    /name: Update unit race coverage\n\s+if: needs.classify.outputs.scope == 'full'\n\s+run: go test -race -p 1 \.\/internal\/update/,
  );
  assert.match(core, /timeout-minutes: 20/);
  for (const [job, minutes] of Object.entries({
    documentation: 3,
    core: 20,
    "core-scenarios": 10,
    desktop: 15,
    signer: 5,
    handoff: 5,
    "handoff-uid": 5,
    "inspection-installed": 10,
  })) {
    const body = workflow.match(
      new RegExp(`^  ${job}:\\n[\\s\\S]*?(?=^  [a-z-]+:|$(?![\\s\\S]))`, "m"),
    )?.[0];
    assert.ok(body, `required job exists: ${job}`);
    assert.match(
      body,
      new RegExp(`timeout-minutes: ${minutes}(?:\\n|$)`),
      `fixed job budget: ${job}`,
    );
  }
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
      "core-scenarios",
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

test("ordinary admission checks run once in parallel without consuming the managed signer budget", async () => {
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const scenarios = workflow.slice(
    workflow.indexOf("  core-scenarios:\n"),
    workflow.indexOf("  desktop:\n"),
  );
  const signer = workflow.slice(
    workflow.indexOf("  signer:\n"),
    workflow.indexOf("  handoff:\n"),
  );
  const build = scenarios.indexOf(
    "      - name: Build ordinary admission CLI binaries\n",
  );
  assert.ok(
    build > scenarios.indexOf("      - run: npm run build\n"),
    "current UI assets precede ordinary CLI binaries",
  );
  assert.match(
    scenarios.slice(build),
    /run: \|\n\s+npm run build:core\n\s+go build -trimpath -o \.evidence\/bin\/fased-signer \.\/cmd\/fased-signer/,
  );
  for (const [name, command, env] of [
    [
      "Read-only signed development manifest and compatibility journey",
      "node scripts/check-update-manifest.mjs",
      "FASED_UPDATE_EVIDENCE: .evidence/p4-complete",
    ],
    [
      "Development HTTPS acquisition and independently pinned bootstrap trust",
      "node scripts/check-update-acquisition.mjs",
      null,
    ],
    [
      "Compiled private core state inventory journey",
      "node scripts/check-update-state.mjs",
      "FASED_STATE_EVIDENCE: .evidence/p4-complete",
    ],
    [
      "Inert release-unit and fixture provenance admission",
      "go test -v ./internal/update -run '^TestReleaseAdmissionCompiledCLI$'",
      "FASED_ADMISSION_EVIDENCE: ${{ github.workspace }}/.evidence/p4-release-admission",
    ],
  ]) {
    assert.equal(
      workflow.split(command).length,
      2,
      `one required execution: ${name}`,
    );
    assert.ok(
      !signer.includes(command),
      `ordinary work must leave signer: ${name}`,
    );
    const start = scenarios.indexOf(`      - name: ${name}\n`);
    assert.ok(start > build, `compiled binaries before check: ${name}`);
    const end = scenarios.indexOf("\n      - ", start + 1);
    const step = scenarios.slice(start, end < 0 ? undefined : end);
    assert.ok(
      step.includes("if: needs.classify.outputs.signer == 'true'"),
      `same selected-signing boundary: ${name}`,
    );
    if (env) assert.ok(step.includes(env), `same evidence location: ${name}`);
  }
  assert.ok(
    scenarios.includes(
      "FASED_ADMISSION_CORE: ${{ github.workspace }}/.evidence/bin/fased-app",
    ),
  );
  assert.match(
    scenarios,
    /if: always\(\) && needs.classify.outputs.signer == 'true'[\s\S]*?name: core-scenarios-evidence/,
  );
  for (const evidence of [
    ".evidence/p4-release-admission/admission-cli-result.json",
    ".evidence/p4-release-admission/admission-cli.log",
    ".evidence/p4-acquisition/acquisition-cli-result.json",
    ".evidence/p4-acquisition/acquisition-cli.log",
    ".evidence/p4-complete/manifest-cli-result.json",
    ".evidence/p4-complete/manifest-cli.log",
    ".evidence/p4-complete/core-cli-result.json",
    ".evidence/p4-complete/core-cli.log",
  ]) {
    assert.ok(
      scenarios.includes(evidence),
      `standalone ordinary evidence retained: ${evidence}`,
    );
    assert.ok(
      signer.includes(evidence),
      `overlapping managed evidence retained: ${evidence}`,
    );
  }
  for (const name of [
    "Installer-managed identities and protected owner recovery",
    "Compile pinned predecessor and bad-health bootstrap fixtures",
    "Actual older-helper bootstrap and subsequent coordinated update",
  ])
    assert.ok(
      signer.includes(name),
      `privileged managed journey stays intact: ${name}`,
    );
  assert.match(scenarios, /timeout-minutes: 10/);
  assert.match(signer, /timeout-minutes: 5/);
});
