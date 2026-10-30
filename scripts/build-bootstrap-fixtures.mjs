// Ordinary-user compiler for reconstructible predecessor and bad-health fixtures.
import assert from "node:assert/strict";
import { currentStorageFixture } from "./fixture-core-storage.mjs";
import { execFileSync } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  cp,
  writeFile,
  rm,
  readFile,
  symlink,
  readdir,
} from "node:fs/promises";
import { createHash } from "node:crypto";
import path from "node:path";
assert(process.getuid() > 0);
const root = path.resolve(
  process.env.FASED_BOOTSTRAP_EVIDENCE ?? ".evidence/p4-bootstrap",
);
await mkdir(root, { recursive: true });
const scratch = await mkdtemp(path.join(root, "build-fixture-"));
const prior = "d2f1fb22149a3c5190eb97d1e2ba58a9a3a0eb2a";
const report = {
  result: "FAIL",
  fixtureOnly: true,
  predecessor: prior,
  inputs: {},
  outputs: {},
};
try {
  try {
    execFileSync("git", ["cat-file", "-e", prior + "^{commit}"]);
  } catch {
    execFileSync("git", ["fetch", "--depth=1", "origin", prior]);
  }
  const archive = async (ref, name) => {
    const dir = path.join(scratch, name);
    await mkdir(dir);
    const bytes = execFileSync("git", ["archive", ref], {
      maxBuffer: 64 << 20,
    });
    execFileSync("tar", ["-xf", "-", "-C", dir], { input: bytes });
    return dir;
  };
  // These two commands do not build the predecessor UI or change its runtime
  // dependencies. Every other package field and the lockfile must still match.
  const packageInputs = (value) => {
    const inputs = structuredClone(value);
    delete inputs.scripts["build:installer"];
    delete inputs.scripts["test:core"];
    return inputs;
  };
  assert.deepEqual(
    packageInputs(JSON.parse(await readFile("package.json", "utf8"))),
    packageInputs(
      JSON.parse(
        execFileSync("git", ["show", prior + ":package.json"], {
          encoding: "utf8",
        }),
      ),
    ),
    "predecessor package inputs changed outside installer-build/core-test commands",
  );
  assert.equal(
    execFileSync("git", ["diff", prior, "--", "package-lock.json"], {
      encoding: "utf8",
    }),
    "",
    "predecessor dependencies changed; install a separately pinned dependency tree before building its UI",
  );
  const old = await archive(prior, "predecessor");
  // UI changes must not silently put today's frontend in an older executable.
  // Reuse only identical pinned dependencies, then compile the genuine old UI.
  await symlink(
    path.resolve("node_modules"),
    path.join(old, "node_modules"),
    "dir",
  );
  execFileSync("npm", ["run", "build"], { cwd: old, stdio: "pipe" });
  const oldUI = {};
  const hashUI = async (dir) => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name);
      if (entry.isDirectory()) await hashUI(file);
      else
        oldUI[path.relative(old, file)] = createHash("sha256")
          .update(await readFile(file))
          .digest("hex");
    }
  };
  await hashUI(path.join(old, "dist"));
  report.predecessorUI = {
    mode: "built-from-pinned-source",
    dependencyLockSha256: createHash("sha256")
      .update(await readFile(path.join(old, "package-lock.json")))
      .digest("hex"),
    outputs: oldUI,
  };
  // Preserve the historical entrypoint/protocol and UI, but use the current
  // storage shape. An untouched schema-3 binary is an incompatible reader.
  report.predecessorComposition = await currentStorageFixture(old);
  for (const command of ["fased-app", "fased-signer"]) {
    const file = path.join(root, command + "-lifecycle3");
    execFileSync(
      "go",
      [
        "build",
        "-mod=readonly",
        "-buildvcs=false",
        "-trimpath",
        "-o",
        file,
        "./cmd/" + command,
      ],
      { cwd: old, stdio: "pipe" },
    );
    report.outputs[file] = createHash("sha256")
      .update(await readFile(file))
      .digest("hex");
  }
  // The bad candidate has the real bootstrap helper but intentionally cannot
  // serve. This is a separately compiled fixture, never a product fault flag.
  const bad = path.join(scratch, "bad-health");
  await mkdir(bad);
  const files = execFileSync("git", [
    "ls-files",
    "--cached",
    "--others",
    "--exclude-standard",
    "-z",
    "--",
    ".",
    ":(exclude)node_modules",
  ])
    .toString()
    .split("\0")
    .filter(Boolean);
  for (const file of files) {
    const target = path.join(bad, file);
    await mkdir(path.dirname(target), { recursive: true });
    await cp(file, target);
    report.inputs[file] = createHash("sha256")
      .update(await readFile(file))
      .digest("hex");
  }
  await cp("dist", path.join(bad, "dist"), { recursive: true });
  await writeFile(
    path.join(bad, "cmd/fased-app/zz_disposable_bad_health.go"),
    'package main\nimport "os"\nfunc init(){if len(os.Args)>1 && os.Args[1]=="serve" {os.Exit(42)}}\n',
  );
  const output = path.join(root, "fased-app-bad-health");
  execFileSync(
    "go",
    [
      "build",
      "-mod=readonly",
      "-buildvcs=false",
      "-trimpath",
      "-o",
      output,
      "./cmd/fased-app",
    ],
    { cwd: bad, stdio: "pipe" },
  );
  report.outputs[output] = createHash("sha256")
    .update(await readFile(output))
    .digest("hex");
  // Compilation alone cannot establish a same-format bootstrap. Compare the
  // actual reader contracts before any privileged fixture is installed.
  const commandContract = (binary, component) => {
    const value = JSON.parse(
      execFileSync(
        binary,
        component === "core"
          ? ["updates", "compatibility"]
          : ["fixture-compatibility"],
        { encoding: "utf8" },
      ),
    );
    return component === "core" ? value.contract : value;
  };
  report.readerContracts = {};
  for (const [component, predecessor, current] of [
    ["core", path.join(root, "fased-app-lifecycle3"), output],
    [
      "signer",
      path.join(root, "fased-signer-lifecycle3"),
      path.resolve(".evidence/bin/fased-signer"),
    ],
  ]) {
    const before = commandContract(predecessor, component);
    const after = commandContract(current, component);
    const normalized = structuredClone(after);
    if (component === "core") normalized.lifecycle = before.lifecycle;
    for (const state of normalized.state) {
      if (state.name === "operations")
        state.features = state.features.filter(
          (feature) => feature !== "install-completion-v1",
        );
    }
    assert.deepEqual(
      normalized,
      before,
      component + " composed bootstrap reader changed state/protocol/authority",
    );
    report.readerContracts[component] = { before, after };
  }
  report.predecessorTree = execFileSync(
    "git",
    ["rev-parse", prior + "^{tree}"],
    { encoding: "utf8" },
  ).trim();
  report.currentTree = execFileSync("git", ["rev-parse", "HEAD^{tree}"], {
    encoding: "utf8",
  }).trim();
  report.result = "PASS";
} finally {
  await rm(scratch, { recursive: true, force: true });
  report.cleanup = "exact generated source fixture removed; no keys/history";
  await writeFile(
    path.join(root, "fixture-builds.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
}
