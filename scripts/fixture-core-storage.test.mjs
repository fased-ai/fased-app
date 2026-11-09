import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  runReaderStage,
  projectReadOnlySignerJournal,
  projectReadOnlyUpdateCommand,
} from "./fixture-core-storage.mjs";

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

test("historical app-only projection never acquires ordinary maintenance authority", async (t) => {
  const { mkdtemp, mkdir, writeFile, readFile, rm } =
    await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const path = await import("node:path");
  const { projectPasswordOnlyContract } =
    await import("./fixture-core-storage.mjs");
  const dir = await mkdtemp(path.join(tmpdir(), "fased-contract-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  await mkdir(path.join(dir, "internal/core"), { recursive: true });
  const source = readFileSync(
    new URL("../internal/core/update.go", import.meta.url),
    "utf8",
  );
  await writeFile(path.join(dir, "internal/core/update.go"), source);
  await projectPasswordOnlyContract(dir);
  const projected = await readFile(
    path.join(dir, "internal/core/update.go"),
    "utf8",
  );
  assert.ok(projected.includes("func OrdinaryCustodyReadContract()"));
  assert.ok(!projected.includes("func OrdinaryUpdateContract()"));
  assert.ok(!projected.includes("update.OrdinaryMaintenanceFeature"));
  assert.ok(projected.includes("return schema == StateSchema"));
});

test("historical signer journal excludes new maintenance decoder dependencies", () => {
  const source = readFileSync(
    new URL("../internal/signer/journal.go", import.meta.url),
    "utf8",
  );
  const projected = projectReadOnlySignerJournal(source);
  assert.ok(!projected.includes("OrdinaryHandoff"));
  assert.ok(!projected.includes('json:"ordinaryHandoff'));
  for (const retained of [
    'json:"admission,omitempty"',
    'json:"installation,omitempty"',
    'json:"update,omitempty"',
    "func recordHash(",
  ])
    assert.ok(projected.includes(retained), retained);
  assert.throws(
    () => projectReadOnlySignerJournal(projected),
    /exact ordinary handoff field/,
  );
  assert.throws(
    () => projectReadOnlySignerJournal(source + source),
    /exact ordinary handoff field/,
  );
});

test("projected guarded CLI refuses ordinary maintenance instead of advertising an absent core method", async (t) => {
  const { mkdtemp, mkdir, writeFile, readFile, rm } =
    await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const path = await import("node:path");
  const { projectPasswordOnlyContract } =
    await import("./fixture-core-storage.mjs");
  const dir = await mkdtemp(path.join(tmpdir(), "fased-guarded-command-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  await mkdir(path.join(dir, "internal/core"), { recursive: true });
  await mkdir(path.join(dir, "cmd/fased-app"), { recursive: true });
  const core = readFileSync(
    new URL("../internal/core/update.go", import.meta.url),
    "utf8",
  );
  const command = readFileSync(
    new URL("../cmd/fased-app/updates.go", import.meta.url),
    "utf8",
  );
  await writeFile(path.join(dir, "internal/core/update.go"), core);
  await writeFile(path.join(dir, "cmd/fased-app/updates.go"), command);
  await projectPasswordOnlyContract(dir);
  const projected = await readFile(
    path.join(dir, "cmd/fased-app/updates.go"),
    "utf8",
  );
  assert.equal(projected, projectReadOnlyUpdateCommand(command));
  assert.ok(!projected.includes("OrdinaryUpdateContract"));
  assert.ok(!projected.includes("ordinary-custody-compatibility"));
  assert.ok(projected.includes("core.UpdateContract()"));
  assert.ok(projected.includes("core.ManagedUpdateContract()"));
  assert.throws(
    () => projectReadOnlyUpdateCommand(projected),
    /exact ordinary CLI/,
  );
  assert.throws(
    () => projectReadOnlyUpdateCommand(command + command),
    /exact ordinary CLI/,
  );
  for (const partial of [
    command.replace("core.OrdinaryUpdateContract()", "core.UpdateContract()"),
    command.replace(' || args[0] == "ordinary-custody-compatibility"', ""),
  ]) {
    await writeFile(path.join(dir, "internal/core/update.go"), core);
    await writeFile(path.join(dir, "cmd/fased-app/updates.go"), partial);
    await assert.rejects(
      projectPasswordOnlyContract(dir),
      /partial ordinary CLI/,
    );
  }
});

test("composed readonly Onchain adapter refuses ordinary config without backporting authority", async () => {
  const { projectReadOnlyOnchainAdapter } =
    await import("./fixture-core-storage.mjs");
  const source = readFileSync(
    new URL("../internal/core/onchain.go", import.meta.url),
    "utf8",
  );
  const projected = projectReadOnlyOnchainAdapter(source);
  assert.ok(!projected.includes("ordinaryFixture"));
  assert.doesNotMatch(projected, /\bc\.Profile\b/);
  assert.doesNotMatch(projected, /\bc\.Provenance\b/);
  assert.ok(
    projected.includes("if c.Version != 2 || c.Network != s.custodyNetwork"),
  );
  assert.throws(
    () => projectReadOnlyOnchainAdapter(projected),
    /exact ordinary fixture declaration/,
  );
});
