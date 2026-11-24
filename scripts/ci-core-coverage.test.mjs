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
  const buildArtifacts = workflow.slice(
    workflow.indexOf("  build-artifacts:\n"),
    workflow.indexOf("  core:\n"),
  );
  const core = workflow.slice(
    workflow.indexOf("  core:\n"),
    workflow.indexOf("  core-scenarios:\n"),
  );
  const scenarios = workflow.slice(
    workflow.indexOf("  core-scenarios:\n"),
    workflow.indexOf("  core-packages:\n"),
  );
  const corePackages = workflow.slice(
    workflow.indexOf("  core-packages:\n"),
    workflow.indexOf("  browser:\n"),
  );
  const handoff = workflow.slice(
    workflow.indexOf("  handoff:\n"),
    workflow.indexOf("  handoff-uid:\n"),
  );
  const retainedNative = await readFile(
    new URL("../.github/workflows/desktop-compatibility.yml", import.meta.url),
    "utf8",
  );
  assert.doesNotMatch(core, /GOOS=(?:windows|darwin)/);
  for (const [goos, arch] of [
    ["windows", "amd64"],
    ["darwin", "arm64"],
  ])
    assert.ok(
      retainedNative.includes(
        `GOOS=${goos} GOARCH=${arch} CGO_ENABLED=0 go build`,
      ),
    );
  assert.match(
    corePackages,
    /go list \.\/internal\/\.\.\. \.\/cmd\/fased-app \.\/cmd\/fased-signer \.\/cmd\/fased-install \.\/cmd\/fased-package/,
  );
  assert.match(corePackages, /grep -vE '\/internal\/\(core\|update\)\$'/);
  assert.match(corePackages, /go test -race "\$\{fased_other_packages\[@\]\}"/);
  assert.match(
    corePackages,
    /needs: \[classify, build-artifacts\]\n\s+if: needs\.classify\.outputs\.scope == 'full'/,
  );
  const skip = core.match(/go test -race \.\/internal\/core -skip '([^']+)'/);
  const run = scenarios.match(
    /go test -race \.\/internal\/core -run '([^']+)'/,
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
      "go test -race ./internal/core -run '^Test(SwapRecovery|VenueCredential|.*WEN)'",
    ).length - 1,
    1,
    "the complementary partition must execute exactly once",
  );
  assert.doesNotMatch(
    core,
    /go test -race \.\/internal\/core -run '\^Test\(SwapRecovery/,
  );
  assert.doesNotMatch(
    workflow,
    /go test -race -p 1\b/,
    "bounded Go race suites must run parallel package tests",
  );
  assert.match(
    scenarios,
    /needs: \[classify, build-artifacts\]\n\s+if: needs\.classify\.outputs\.scope == 'full'/,
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
    "npm run build:core",
    "name: ci-built-binaries",
  ])
    assert.ok(
      buildArtifacts.includes(prerequisite),
      `retain shared artifact prerequisite: ${prerequisite}`,
    );
  const aggregate = workflow.slice(workflow.indexOf("  prototype:\n"));
  assert.match(aggregate, /\n\s+build-artifacts,/);
  assert.match(aggregate, /\n\s+core-packages,/);
  assert.match(aggregate, /\n\s+core-scenarios,/);
  assert.match(
    handoff,
    /name: Update unit race coverage\n\s+if: needs\.classify\.outputs\.scope == 'full'[\s\S]*?\n\s+run: go test -race \.\/internal\/update/,
  );
  assert.match(core, /timeout-minutes: 20/);
  for (const [job, minutes] of Object.entries({
    documentation: 3,
    "build-artifacts": 8,
    core: 20,
    "core-packages": 10,
    "core-scenarios": 10,
    browser: 15,
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
      "build-artifacts",
      "core",
      "core-packages",
      "core-scenarios",
      "browser",
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
  const buildArtifacts = workflow.slice(
    workflow.indexOf("  build-artifacts:\n"),
    workflow.indexOf("  core:\n"),
  );
  const scenarios = workflow.slice(
    workflow.indexOf("  core-scenarios:\n"),
    workflow.indexOf("  core-packages:\n"),
  );
  const signer = workflow.slice(
    workflow.indexOf("  signer:\n"),
    workflow.indexOf("  handoff:\n"),
  );
  const download = scenarios.indexOf(
    "      - uses: actions/download-artifact@v4\n",
  );
  assert.ok(
    buildArtifacts.indexOf("      - run: npm run build:core\n") >
      buildArtifacts.indexOf("      - run: npm run build\n"),
    "current UI assets precede ordinary CLI binaries",
  );
  assert.ok(
    buildArtifacts.includes("name: ci-built-binaries") &&
      scenarios.includes("name: ci-built-binaries") &&
      download >= 0,
    "core-scenarios downloads the shared UI and binary artifacts",
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
    assert.ok(start > download, `compiled binaries before check: ${name}`);
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

test("browser coverage runs once for every selected core path", async () => {
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const browser = workflow.split("\n  browser:\n")[1].split("\n  signer:\n")[0];
  assert.equal(workflow.match(/run: npm run test:ui(?:\n|$)/g)?.length, 1);
  assert.match(browser, /matrix:\n\s+shard: \[1, 2\]/);
  assert.match(browser, /if: matrix\.shard == 1\n\s+run: npm run test:ui/);
  assert.match(browser, /needs.classify.outputs.core == 'true'/);
  assert.ok(
    browser.indexOf("npx playwright install") <
      browser.indexOf("- name: Browser UI journey"),
  );
  for (const paths of [
    ["internal/core/onchain.go"],
    ["web/src/core.tsx"],
    ["internal/core/rpc_check.go"],
  ])
    assert.equal(classify(paths).core, true);
  assert.match(browser, /timeout-minutes: 15/);
  assert.ok(browser.includes("            .evidence/browser/\n"));
  const inspection = workflow
    .split("\n  inspection-installed:\n")[1]
    .split("\n  prototype:\n")[0];
  assert.match(inspection, /leg: \[fixture, network, enrollment\]/);
  assert.match(
    inspection,
    /if: matrix\.leg == 'fixture' \|\| matrix\.leg == 'network'\n\s+run: go test -c -o \.evidence\/bin\/wallet-core\.test \.\/internal\/core/,
  );
  for (const leg of ["fixture", "network", "enrollment"]) {
    assert.match(inspection, new RegExp(`if: matrix\\.leg == '${leg}'`));
  }
});

test("static vet, fuzz seed regression, and release assembler tests stay wired in CI", async () => {
  const checks = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const desktop = await readFile(
    new URL("../.github/workflows/desktop-compatibility.yml", import.meta.url),
    "utf8",
  );
  const audit = await readFile(
    new URL("../.github/workflows/security-audit.yml", import.meta.url),
    "utf8",
  );
  const dependabot = await readFile(
    new URL("../.github/dependabot.yml", import.meta.url),
    "utf8",
  );
  assert.match(checks, /scripts\/assemble-dual-track-release\.test\.mjs/);
  assert.match(checks, /- run: go vet \.\/\.\.\./);
  assert.match(
    checks,
    /go test -run='\^Fuzz' \.\/internal\/core \.\/internal\/decision \.\/internal\/marketdata \.\/internal\/mcpclient \.\/internal\/signer \.\/internal\/update \.\/internal\/wenpm1 \.\/internal\/worldintel \.\/internal\/automation \.\/internal\/telegram/,
  );
  assert.match(desktop, /- run: go -C desktop vet \.\/\.\.\./);
  assert.match(audit, /govulncheck/);
  assert.match(audit, /FuzzDecodeFrames/);
  assert.match(dependabot, /package-ecosystem: "gomod"/);
  assert.match(dependabot, /directory: "\/desktop"/);
  assert.match(dependabot, /package-ecosystem: "npm"/);
});
