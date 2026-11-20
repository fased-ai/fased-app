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
    ["browser", "core"],
    ["signer", "signer"],
    ["handoff", "handoff"],
    ["handoff-uid", "handoff_uid"],
    ["inspection-installed", "inspection"],
  ])
    needs[job] = { result: plan[flag] ? "success" : "skipped" };
  needs["core-scenarios"] = {
    result: plan.scope === "full" ? "success" : "skipped",
  };
  return needs;
}
test("core scenarios are required only for full scope and cannot be omitted or skipped", () => {
  const full = result(classify([]));
  assert.doesNotThrow(() => verifyGate(full));
  for (const state of [undefined, "skipped", "failure", "cancelled"]) {
    const needs = structuredClone(full);
    if (state === undefined) delete needs["core-scenarios"];
    else needs["core-scenarios"].result = state;
    assert.throws(
      () => verifyGate(needs),
      /required result missing: core-scenarios/,
    );
  }
  for (const files of [
    ["docs/chat.md"],
    ["web/src/pages/Chat.tsx"],
    ["internal/core/rpc_check.go"],
  ]) {
    const needs = result(classify(files));
    assert.equal(needs["core-scenarios"].result, "skipped");
    assert.doesNotThrow(() => verifyGate(needs));
    needs["core-scenarios"].result = "success";
    assert.throws(
      () => verifyGate(needs),
      /required result missing: core-scenarios/,
    );
  }
});
test("selected signer checks cannot escape the full-scope parallel admission job", () => {
  for (const files of [
    [],
    ["internal/core/store.go"],
    ["web/src/core.tsx"],
    ["internal/core/rpc_public.go"],
    ["docs/chat.md"],
  ]) {
    const plan = classify(files);
    if (plan.signer) assert.equal(plan.scope, "full");
    else assert.notEqual(plan.scope, "full");
  }
  for (const files of [
    ["web/src/core.tsx"],
    ["internal/core/rpc_public.go"],
    ["docs/chat.md"],
  ]) {
    const needs = result(classify(files));
    needs.classify.outputs.signer = "true";
    needs.signer.result = "success";
    assert.throws(() => verifyGate(needs), /signer checks require full scope/);
  }
});
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

test("CLI routes cumulative changes and admits only an exact forward version change", async () => {
  const { chmodSync, mkdtempSync, mkdirSync, writeFileSync, rmSync } =
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
    writeFileSync(`${dir}/.gitignore`, ".evidence/\n");
    mkdirSync(`${dir}/release`);
    const configPath = `${dir}/release/ubuntu.json`;
    const config = '{\n  "version": "0.1.16",\n  "target": "linux"\n}\n';
    writeFileSync(configPath, config);
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
    const plan = (force = "false", baseRef = base) =>
      JSON.parse(
        execFileSync(process.execPath, [script, "plan"], {
          cwd: dir,
          encoding: "utf8",
          env: {
            ...process.env,
            GITHUB_OUTPUT: "",
            BASE_SHA: baseRef,
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
    git("switch", "-q", "--detach", base);
    git("config", "core.fileMode", "true");
    writeFileSync(configPath, config.replace("0.1.16", "0.1.17"));
    git("add", ".");
    git("commit", "-qm", "version only");
    const version = git("rev-parse", "HEAD");
    const selected = plan();
    assert.equal(selected.scope, "version");
    for (const lane of lanes) assert.equal(selected[lane], false);
    verifyGate(result(selected));
    const invalid = result(selected);
    invalid.classify.outputs.vm = "true";
    assert.throws(() => verifyGate(invalid), /invalid version-only selection/);
    invalid.classify.outputs.vm = "false";
    invalid.classify.result = "failure";
    assert.throws(() => verifyGate(invalid), /classification must pass/);
    assert.equal(plan("true").scope, "full");

    for (const [name, text] of [
      ["rollback", config.replace("0.1.16", "0.1.15")],
      ["noncanonical version", config.replace("0.1.16", "0.1.017")],
      [
        "config drift",
        config.replace("0.1.16", "0.1.17").replace("linux", "other"),
      ],
      [
        "whitespace drift",
        config.replace("0.1.16", "0.1.17").replace('"target": ', '"target":  '),
      ],
    ]) {
      git("switch", "-q", "--detach", base);
      writeFileSync(configPath, text);
      git("add", ".");
      git("commit", "-qm", name);
      assert.equal(plan().scope, "full", name);
    }
    git("switch", "-q", "--detach", version);
    chmodSync(configPath, 0o755);
    git("add", "release/ubuntu.json");
    git("commit", "-qm", "mode change");
    assert.equal(plan().scope, "full");
    git("switch", "-q", "--detach", version);
    writeFileSync(`${dir}/README.md`, "mixed changes\n");
    git("add", ".");
    git("commit", "-qm", "mixed changes");
    assert.equal(plan().scope, "full");
    git("switch", "-q", "--detach", base);
    writeFileSync(`${dir}/README.md`, "advanced base\n");
    git("add", ".");
    git("commit", "-qm", "advanced base");
    const advanced = git("rev-parse", "HEAD");
    git("switch", "-q", "--detach", version);
    assert.equal(plan("false", advanced).scope, "full");
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
    workflow.indexOf("  core-scenarios:\n"),
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

test("browser and joined CLI coverage run independently of retained native clients", async () => {
  const { readFile } = await import("node:fs/promises");
  const workflow = await readFile(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  const browser = workflow.split("\n  browser:\n")[1].split("\n  signer:\n")[0];
  assert.match(browser, /if: needs.classify.outputs.core == 'true'/);
  assert.match(browser, /npm run test:ui/);
  assert.match(browser, /npm run test:core-ui -- --fully-parallel --workers=3/);
  assert.match(browser, /npm run test:core-ui -- --grep/);
  assert.doesNotMatch(
    workflow,
    /build:desktop|build-desktop|gtk|webkit|package:native|test:native-window/,
  );
  assert.doesNotMatch(browser, /--project=/);
  for (const [env, binary, pkg] of [
    ["FASED_WALLET_BROWSER_BINARY", "wallet-core.test", "core"],
    ["FASED_WALLET_OWNER_BINARY", "wallet-owner.test", "signer"],
  ]) {
    assert.ok(browser.includes(`${env}: .evidence/bin/${binary}`));
    assert.ok(
      browser.includes(
        `go test -c -o .evidence/bin/${binary} ./internal/${pkg}`,
      ),
    );
  }
  assert.match(browser, /if: failure\(\) \|\| cancelled\(\)/);
  assert.match(browser, /\.evidence\/core-browser\//);
  for (const paths of [
    [],
    ["web/src/pages/Agents.tsx"],
    ["internal/core/rpc_check.go"],
  ]) {
    const needs = result(classify(paths));
    assert.equal(needs.browser.result, "success");
    assert.doesNotThrow(() => verifyGate(needs));
    for (const state of ["skipped", "failure", "cancelled", undefined]) {
      const broken = structuredClone(needs);
      if (state === undefined) delete broken.browser;
      else broken.browser.result = state;
      assert.throws(() => verifyGate(broken), /browser/);
    }
  }
  // The trusted base router can still emit the retired desktop flag.
  const base = result(classify([]));
  base.classify.outputs.desktop = "true";
  assert.doesNotThrow(() => verifyGate(base));
});

test("Mintlify configuration and static assets are docs, executable or mixed paths stay conservative", () => {
  const docs = [
    "docs/docs.json",
    "docs/.mintignore",
    "docs/guide/chat.mdx",
    "docs/assets/logo.svg",
    "docs/assets/screenshot.png",
    "docs/assets/fased-branding-license.txt",
  ];
  assert.equal(classify(docs).scope, "none");
  for (const lane of lanes) assert.equal(classify(docs)[lane], false);
  for (const other of [
    "docs/custom.js",
    "docs/assets/script.js",
    "docs/hooks.ts",
    "scripts/check-docs.mjs",
    "scripts/ci-routing.mjs",
    "internal/core/server.go",
  ]) {
    const plan = classify([...docs, other]);
    assert.equal(plan.scope, "full");
    for (const lane of lanes) assert.equal(plan[lane], true);
  }
});

test("release-script and non-custody agent changes use bounded CI lanes and keep sensitive paths conservative", () => {
  const release = classify([
    "scripts/release-acceptance.mjs",
    "scripts/release-acceptance.test.mjs",
    "scripts/staged-acceptance-contract.json",
    "docs/maintainers/ubuntu-release.md",
  ]);
  assert.equal(release.scope, "release");
  for (const lane of lanes) assert.equal(release[lane], false);
  assert.doesNotThrow(() => verifyGate(result(release)));
  const badRelease = result(release);
  badRelease.classify.outputs.core = "true";
  assert.throws(() => verifyGate(badRelease), /invalid release-only selection/);

  const agent = classify([
    "internal/core/agent_chat.go",
    "internal/core/provider_models.json",
    "internal/core/ai_connection.go",
    "web/src/pages/Chat.tsx",
    "docs/ai-connections.md",
  ]);
  assert.equal(agent.scope, "agent");
  assert.equal(agent.core, true);
  assert.equal(agent.signer, false);
  assert.equal(agent.handoff, false);
  assert.equal(agent.handoff_uid, false);
  assert.equal(agent.inspection, false);
  assert.equal(agent.vm, false);
  assert.doesNotThrow(() => verifyGate(result(agent)));

  // Sensitive, mixed, or strategy/browser-poller paths stay conservative.
  for (const paths of [
    ["scripts/release-acceptance.mjs", "web/src/pages/Chat.tsx"],
    ["scripts/staged-package-browser.mjs"],
    ["internal/core/agent_strategies.go"],
    ["internal/core/agent_strategies_test.go"],
    ["internal/core/agent_chat.go", "internal/core/rpc_public.go"],
    ["internal/core/agent_chat.go", "internal/core/store.go"],
    ["internal/core/agent_chat.go", "internal/signer/store.go"],
  ]) {
    const plan = classify(paths);
    assert.equal(plan.scope, "full", paths.join(", "));
    for (const lane of lanes) assert.equal(plan[lane], true);
  }
});
