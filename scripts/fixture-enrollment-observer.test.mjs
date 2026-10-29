import assert from "node:assert/strict";
import test from "node:test";
import {
  mkdtemp,
  mkdir,
  readFile,
  rename,
  rm,
  symlink,
  unlink,
  writeFile,
} from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import {
  readEnrollmentObservation,
  removeEnrollmentUnitLink,
} from "./fixture-enrollment-observer.mjs";

async function fixture(t) {
  const dir = await mkdtemp(join(tmpdir(), "fased-enrollment-observer-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  return dir;
}

test("journal observation tolerates only initial missing/empty publication, then reads atomic data", async (t) => {
  const dir = await fixture(t),
    path = join(dir, "state.json");
  assert.equal(await readEnrollmentObservation(path), undefined);
  await writeFile(path, "", { mode: 0o600 });
  assert.equal(await readEnrollmentObservation(path), undefined);
  const expected = { phase: "prepared", target: { id: "fixture" } };
  await writeFile(join(dir, "pending"), JSON.stringify(expected), {
    mode: 0o600,
  });
  await rename(join(dir, "pending"), path);
  assert.deepEqual(await readEnrollmentObservation(path), expected);
  await writeFile(path, "");
  await assert.rejects(
    readEnrollmentObservation(path, { durable: true }),
    SyntaxError,
  );
  await unlink(path);
  await assert.rejects(readEnrollmentObservation(path, { durable: true }), {
    code: "ENOENT",
  });
});

test("non-publication filesystem errors remain fatal before durable observation", async (t) => {
  const dir = await fixture(t);
  await assert.rejects(readEnrollmentObservation(dir), { code: "EISDIR" });
});

test("unit disable may already unlink exactly the declared fixture symlink", async (t) => {
  const dir = await fixture(t),
    unit = join(dir, "fixture.service"),
    link = join(dir, "enabled.service");
  await writeFile(unit, "preserved fixture unit", { mode: 0o600 });
  await symlink(unit, link);
  await unlink(link); // Actual filesystem result of systemctl disable.
  await removeEnrollmentUnitLink(link);
  await symlink(unit, link);
  await removeEnrollmentUnitLink(link);
  assert.equal(await readFile(unit, "utf8"), "preserved fixture unit");
});

test("cleanup preserves unexpected directories and propagates other errors", async (t) => {
  const dir = await fixture(t),
    unexpected = join(dir, "foreign");
  await mkdir(unexpected);
  await writeFile(join(unexpected, "preserve"), "foreign fixture bytes");
  await assert.rejects(
    removeEnrollmentUnitLink(unexpected),
    (e) => e.code !== "ENOENT",
  );
  assert.equal(
    await readFile(join(unexpected, "preserve"), "utf8"),
    "foreign fixture bytes",
  );
});
