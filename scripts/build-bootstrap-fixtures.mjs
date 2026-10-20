// Ordinary-user compiler for reconstructible predecessor and bad-health fixtures.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, mkdtemp, cp, writeFile, rm, readFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import path from "node:path";
assert(process.getuid() > 0);
const root = path.resolve(".evidence/p4-bootstrap");
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
    await cp("dist", path.join(dir, "dist"), { recursive: true });
    return dir;
  };
  assert.equal(
    execFileSync(
      "git",
      [
        "diff",
        prior,
        "--",
        "package.json",
        "package-lock.json",
        "web",
        "ui.go",
        "vite.config.ts",
      ],
      { encoding: "utf8" },
    ),
    "",
    "predecessor embedded UI inputs changed",
  );
  const old = await archive(prior, "predecessor");
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
