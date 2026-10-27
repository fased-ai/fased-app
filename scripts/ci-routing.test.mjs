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
