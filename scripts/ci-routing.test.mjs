import { strict as assert } from "node:assert";
import { test } from "node:test";
import { classify, lanes, verifyGate } from "./ci-routing.mjs";

test("docs, RPC, UI and coupled changes select bounded coverage", () => {
  assert.equal(classify(["docs/signer.md"]).core, false);
  const rpc = classify([
    "internal/core/rpc_public.go",
    "internal/core/rpc_check_test.go",
  ]);
  assert.equal(rpc.scope, "rpc");
  assert.equal(rpc.core, true);
  assert.equal(rpc.desktop, false);
  assert.equal(rpc.vm, false);
  assert.equal(classify(["web/src/pages/Wallets.tsx"]).scope, "ui");
  assert.equal(
    classify([
      "web/src/pages/Wallets.tsx",
      "internal/core/rpc_public.go",
      "web/tests/core.spec.ts",
    ]).scope,
    "full",
  );
  assert.equal(
    classify(["internal/core/rpc_public.go", "web/src/pages/Wallets.tsx"])
      .scope,
    "full",
  );
});
test("unknown, schema, custody, dependency and workflow paths fail conservative", () => {
  for (const paths of [
    [],
    ["internal/core/store.go"],
    ["internal/core/accounts.go"],
    ["internal/signer/store.go"],
    ["scripts/ci-routing.mjs"],
    [".github/workflows/checks.yml"],
    ["go.sum"],
    ["moved/new.go"],
    ["strange\npath"],
  ]) {
    const p = classify(paths);
    assert.equal(p.scope, "full");
    for (const lane of lanes) assert.equal(p[lane], true);
  }
});
function result(plan) {
  const needs = {
    documentation: { result: "success" },
    classify: {
      result: "success",
      outputs: Object.fromEntries(
        Object.entries(plan).map(([k, v]) => [k, String(v)]),
      ),
    },
  };
  for (const [job, flag] of [
    ["core", "core"],
    ["desktop", "desktop"],
    ["signer", "signer"],
    ["handoff", "handoff"],
    ["handoff-uid", "handoff_uid"],
    ["inspection-installed", "inspection"],
  ])
    needs[job] = { result: plan[flag] ? "success" : "skipped" };
  return needs;
}
test("aggregate accepts intentional skips only and rejects absent/failing gates", () => {
  for (const p of [
    classify(["docs/signer.md"]),
    classify(["internal/core/rpc_check.go"]),
    classify([]),
  ]) {
    const good = result(p);
    verifyGate(good);
    for (const job of Object.keys(good))
      for (const status of ["failure", "cancelled"]) {
        const n = structuredClone(good);
        n[job].result = status;
        assert.throws(() => verifyGate(n));
      }
    const n = structuredClone(good);
    delete n.core;
    assert.throws(() => verifyGate(n));
  }
  const n = result(classify([]));
  n.core.result = "skipped";
  assert.throws(() => verifyGate(n));
  n.classify.outputs.core = "";
  assert.throws(() => verifyGate(n));
});

test("CLI uses cumulative changes, includes renames, and dispatch selects full", async () => {
  const { mkdtempSync, mkdirSync, writeFileSync, rmSync } =
    await import("node:fs");
  const { resolve } = await import("node:path");
  const { execFileSync } = await import("node:child_process");
  mkdirSync(".evidence", { recursive: true });
  const dir = mkdtempSync(resolve(".evidence/routing-git-"));
  const script = resolve("scripts/ci-routing.mjs");
  const git = (...args) =>
    execFileSync("git", args, { cwd: dir, encoding: "utf8" }).trim();
  try {
    git("init", "-q");
    git("config", "user.name", "Fixture");
    git("config", "user.email", "fixture@example.invalid");
    writeFileSync(`${dir}/README.md`, "base\n");
    git("add", ".");
    git("commit", "-qm", "base");
    const base = git("rev-parse", "HEAD");
    mkdirSync(`${dir}/internal/core`, { recursive: true });
    writeFileSync(`${dir}/internal/core/rpc_check.go`, "fixture\n");
    git("add", ".");
    git("commit", "-qm", "rpc");
    writeFileSync(`${dir}/README.md`, "later docs\n");
    git("add", ".");
    git("commit", "-qm", "docs");
    const plan = (force = "false") =>
      JSON.parse(
        execFileSync(process.execPath, [script, "plan"], {
          cwd: dir,
          encoding: "utf8",
          env: {
            ...process.env,
            GITHUB_OUTPUT: "",
            BASE_SHA: base,
            HEAD_SHA: git("rev-parse", "HEAD"),
            FULL_RUN: force,
          },
        }),
      );
    assert.equal(plan().scope, "rpc");
    assert.equal(plan("true").vm, true);
    git("mv", "internal/core/rpc_check.go", "unknown.go");
    git("commit", "-qm", "rename");
    assert.equal(plan().vm, true);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("documentation cannot be absent or skipped in the required aggregate", () => {
  const n = result(classify([]));
  delete n.documentation;
  assert.throws(() => verifyGate(n));
  n.documentation = { result: "skipped" };
  assert.throws(() => verifyGate(n));
});

test("ordinary recovery checks leave signer time for privileged bootstrap without losing coverage", async () => {
  const { readFile } = await import("node:fs/promises");
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const core = workflow.slice(
    workflow.indexOf("  core:\n"),
    workflow.indexOf("  desktop:\n"),
  );
  const signer = workflow.slice(
    workflow.indexOf("  signer:\n"),
    workflow.indexOf("  handoff:\n"),
  );
  for (const [name, command] of [
    [
      "Portable encrypted history and quarantined recovery checks",
      "node scripts/check-portable-recovery.mjs",
    ],
    [
      "Disposable TUF trust and read-only CLI rehearsal",
      "go test -v ./internal/update -run '^TestTrustRehearsalCompiledCLI$'",
    ],
  ]) {
    assert.equal(
      workflow.split(command).length,
      2,
      `one required execution: ${name}`,
    );
    assert.ok(
      !signer.includes(command),
      `ordinary check clogs privileged job: ${name}`,
    );
    const start = core.indexOf(`      - name: ${name}\n`);
    assert.ok(
      start > core.indexOf("      - run: npm run build:core"),
      `core binary must exist: ${name}`,
    );
    const rest = core.slice(start + 1),
      end = rest.indexOf("\n      - ");
    const step = rest.slice(0, end < 0 ? rest.length : end);
    assert.ok(
      step.includes("if: needs.classify.outputs.signer == 'true'"),
      `only selected custody changes: ${name}`,
    );
  }
  assert.match(signer, /timeout-minutes: 5/);
  assert.match(
    signer,
    /Actual older-helper bootstrap and subsequent coordinated update/,
  );
  assert.match(core, /name: core-recovery-evidence/);
  for (const path of [
    ".evidence/p4-portable-recovery/local-result.json",
    ".evidence/p4-trust-rehearsal/trust-cli-result.json",
  ])
    assert.ok(core.includes(path), `retain evidence: ${path}`);
  for (const paths of [
    ["internal/vpsinstall/install_linux.go"],
    ["go.mod"],
    [".github/workflows/checks.yml"],
  ]) {
    const plan = classify(paths);
    assert.equal(plan.signer, true);
    assert.equal(plan.core, true);
  }
});

test("full UI journey uses the existing Linux job while focused paths retain core coverage", async () => {
  const { readFile } = await import("node:fs/promises");
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const core = workflow.slice(
    workflow.indexOf("  core:\n"),
    workflow.indexOf("  desktop:\n"),
  );
  const desktop = workflow.slice(
    workflow.indexOf("  desktop:\n"),
    workflow.indexOf("  signer:\n"),
  );
  assert.doesNotMatch(
    core,
    /if: needs.classify.outputs.scope == 'full'\n\s+run: npm run test:core-ui/,
  );
  assert.match(
    core,
    /if: needs.classify.outputs.scope == 'ui'\n\s+run: npm run test:core-ui\n/,
  );
  assert.match(
    core,
    /if: needs.classify.outputs.scope == 'rpc'\n\s+run: npm run test:core-ui -- --grep/,
  );
  assert.match(
    desktop,
    /if: runner.os == 'Linux' && needs.classify.outputs.scope == 'full'\n\s+run: npm run test:core-ui\n/,
  );
  assert.ok(
    !workflow.includes("--project="),
    "every selected journey retains desktop and mobile",
  );
  assert.match(desktop, /npx playwright install --only-shell chromium/);
  for (const job of [core, desktop])
    assert.ok(
      job.includes("if: failure() || cancelled()"),
      "retain traces on failure or job cancellation",
    );
  assert.match(desktop, /\.evidence\/core-browser\//);
  assert.ok(desktop.includes(".evidence/*.png"), "retain moved screenshots");
  assert.match(core, /timeout-minutes: 20/);
  assert.match(desktop, /timeout-minutes: 15/);
  const full = classify([".github/workflows/checks.yml"]);
  assert.equal(full.core, true);
  assert.equal(full.desktop, true);
  for (const paths of [
    ["web/src/pages/Agents.tsx"],
    ["internal/core/rpc_check.go"],
  ]) {
    const plan = classify(paths);
    assert.equal(plan.core, true);
    assert.equal(plan.desktop, false);
  }
});
