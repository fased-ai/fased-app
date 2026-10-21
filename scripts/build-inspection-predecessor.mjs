// Build a plain lifecycle-4 reader fixture with the current storage schema.
// This is an explicit composed fixture, not an exact historical release.
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
  root = path.resolve(
    process.env.FASED_PREDECESSOR_OUTPUT ?? ".evidence/p4-installed-inspection",
  ),
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
  // Admission tests need a compatible storage baseline. The historical schema-3
  // runtime cannot read schema 4. Backport only current storage/migration/anchor
  // code; keep the historical command dispatch without inspection admission or
  // the P5 HTTP surface/scheduler. Never claim this is the untouched old binary.
  const storage = {};
  for (const file of ["agents.go", "update.go", "lifecycle.go"]) {
    const bytes = await readFile("internal/core/" + file);
    await writeFile(path.join(scratch, "internal/core", file), bytes);
    storage[file] = sha(bytes);
  }
  const storeFile = path.join(scratch, "internal/core/store.go");
  const beforeStore = await readFile(storeFile, "utf8");
  const marker =
    "\tstore := &Store{db: db, dir: dir, now: time.Now, bootID: update.NewProbe()}";
  assert.equal(beforeStore.split(marker).length, 2);
  await writeFile(
    storeFile,
    beforeStore.replace(
      marker,
      "\tif schema <= 3 { if err = migrateAgents(ctx, db); err != nil { return nil, err } }\n" +
        marker,
    ),
  );
  storage["store.go"] = sha(await readFile(storeFile));
  // UI is inert in this predecessor journey; the current bundle can be reused.
  // Its hash is recorded independently; no historical UI equivalence is claimed.
  assert.equal(
    execFileSync(
      "git",
      ["diff", ref, "--", "ui", "package.json", "package-lock.json", "ui.go"],
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
    JSON.stringify(
      {
        result: "PASS",
        baseCommit: ref,
        fixtureOnly: true,
        exactHistoricalBinary: false,
        storage,
        uiSha256: sha(await readFile("dist/index.html")),
        outputs,
      },
      null,
      2,
    ) + "\n",
  );
} finally {
  await rm(scratch, { recursive: true, force: true });
}
