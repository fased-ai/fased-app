// Build the exact plain lifecycle-4 predecessor once with shared dependencies.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, cp, readFile, writeFile, rm } from "node:fs/promises";
import path from "node:path";
import { sha } from "./handoff-transport-evidence.mjs";
assert.ok(
  process.env.GITHUB_ACTIONS === "true" ||
    process.env.FASED_DISPOSABLE_VM_BUILD === "1",
);
assert.ok(process.getuid() > 0);
const ref = "6c53e34eca22486dcef856cb701f3cd72e30eaa4",
  root = path.resolve(".evidence/p4-installed-inspection"),
  scratch = path.join(root, "predecessor-source");
await mkdir(root, { recursive: true, mode: 0o700 });
await mkdir(scratch, { mode: 0o700 });
try {
  try {
    execFileSync("git", ["cat-file", "-e", ref + "^{commit}"]);
  } catch {
    execFileSync("git", ["fetch", "--depth=1", "origin", ref], {
      stdio: "inherit",
    });
  }
  const archive = execFileSync("git", ["archive", "--format=tar", ref], {
    maxBuffer: 32 << 20,
  });
  execFileSync("tar", ["-xf", "-", "-C", scratch], { input: archive });
  // UI sources are unchanged; reuse the already compiled static UI.
  assert.equal(
    execFileSync(
      "git",
      [
        "diff",
        ref,
        "--",
        "ui",
        "src",
        "package.json",
        "package-lock.json",
        "ui.go",
      ],
      { encoding: "utf8" },
    ),
    "",
  );
  await cp("dist", path.join(scratch, "dist"), { recursive: true });
  const outputs = {};
  for (const role of ["app", "signer"]) {
    const dest = path.join(root, "predecessor-" + role);
    execFileSync("go", ["build", "-o", dest, "./cmd/fased-" + role], {
      cwd: scratch,
      stdio: "inherit",
    });
    outputs[role] = sha(await readFile(dest));
  }
  await writeFile(
    path.join(root, "predecessor-build.json"),
    JSON.stringify({ result: "PASS", commit: ref, outputs }, null, 2) + "\n",
  );
} finally {
  await rm(scratch, { recursive: true, force: true });
}
