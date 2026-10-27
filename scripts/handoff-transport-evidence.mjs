import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
export const sha = (b) => createHash("sha256").update(b).digest("hex");
export async function sources() {
  const r = spawnSync(
    "git",
    ["ls-files", "-z", "--cached", "--others", "--exclude-standard"],
    { encoding: "utf8" },
  );
  assert.equal(r.status, 0);
  return Object.fromEntries(
    await Promise.all(
      r.stdout
        .split("\0")
        .filter(Boolean)
        .sort()
        .map(async (f) => [f, sha(await readFile(f))]),
    ),
  );
}
export async function unchanged(before) {
  assert.deepEqual(
    await sources(),
    before,
    "source inputs changed during verification",
  );
}
export const save = (file, body) =>
  writeFile(file, JSON.stringify(body, null, 2) + "\n");

// A previous fixture's marker cannot satisfy the next case's barrier.
export function currentFixtureGate(expected, observed) {
  return (
    Number.isSafeInteger(expected) &&
    expected > 0 &&
    observed?.count === expected
  );
}
