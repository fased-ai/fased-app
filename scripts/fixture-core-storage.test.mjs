import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runReaderStage } from "./fixture-core-storage.mjs";

const reader = { schema: 36, ref: "ad910705246844c333eae8b41b742cb818a58069" };

test("reader child commands have a fixed budget and attributable stages", (t) => {
  const logs = [];
  t.mock.method(console, "log", (value) => logs.push(value));
  const archive = Buffer.from("controlled archive bytes");
  const value = runReaderStage(
    reader,
    "archive",
    "git",
    ["archive", reader.ref],
    {
      timeout: 999999,
      killSignal: "SIGTERM",
      maxBuffer: 32 << 20,
      stdio: ["ignore", "pipe", "inherit"],
    },
    (tool, args, options) => {
      assert.equal(tool, "git");
      assert.deepEqual(args, ["archive", reader.ref]);
      assert.equal(options.timeout, 120000);
      assert.equal(options.killSignal, "SIGKILL");
      assert.equal(options.maxBuffer, 32 << 20);
      assert.deepEqual(options.stdio, ["ignore", "pipe", "inherit"]);
      return archive;
    },
  );
  assert.equal(value, archive);
  assert.deepEqual(logs, [
    `reader schema36 ${reader.ref} archive: start`,
    `reader schema36 ${reader.ref} archive: done`,
  ]);
});

test("a timed-out reader fails once without a success receipt or retry", (t) => {
  const logs = [];
  t.mock.method(console, "log", (value) => logs.push(value));
  const fault = Object.assign(new Error("controlled child timeout"), {
    code: "ETIMEDOUT",
  });
  let calls = 0;
  assert.throws(
    () =>
      runReaderStage(reader, "build", "go", ["build"], {}, () => {
        calls++;
        throw fault;
      }),
    (e) =>
      e.cause === fault &&
      e.message === `reader schema36 ${reader.ref} build: failed`,
  );
  assert.equal(calls, 1);
  assert.deepEqual(logs, [`reader schema36 ${reader.ref} build: start`]);
});

test("all application-reader tool stages are bounded without extending VM time", () => {
  const source = readFileSync(
    new URL("./build-inspection-predecessor.mjs", import.meta.url),
    "utf8",
  );
  const branch = source.slice(
    source.indexOf("const [sourceSchema, source]"),
    source.indexOf("process.exit(0);"),
  );
  assert.ok(!branch.includes("execFileSync("));
  for (const stage of [
    "object",
    "fetch",
    "archive",
    "extract",
    "build",
    "cleanup",
  ])
    assert.ok(new RegExp(`run\\(\\s*"${stage}"`).test(branch), stage);
  assert.match(branch, /GOTOOLCHAIN: "local"/);
  assert.match(branch, /"\/usr\/bin\/rm"/);
  assert.match(branch, /"--one-file-system"/);
  assert.ok(
    branch.indexOf('"cleanup"') <
      branch.indexOf("`reader-predecessor${suffix}-build.json`"),
  );
  assert.match(
    branch,
    /error\.cause\?\.status !== 128 \|\| error\.cause\?\.signal/,
  );
  const workflow = readFileSync(
    new URL("../.github/workflows/inspection-acceptance.yml", import.meta.url),
    "utf8",
  );
  assert.match(workflow, /timeout-minutes: 35/);
});
