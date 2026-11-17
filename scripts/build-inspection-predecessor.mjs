// Build a plain lifecycle-4 reader fixture with the current storage schema.
// This is an explicit composed fixture, not an exact historical release.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdir, cp, readFile, writeFile, rm } from "node:fs/promises";
import path from "node:path";
import { sha } from "./handoff-transport-evidence.mjs";
import {
  currentStorageFixture,
  projectPasswordOnlyContract,
  runReaderStage,
} from "./fixture-core-storage.mjs";
assert.ok(
  process.env.GITHUB_ACTIONS === "true" ||
    process.env.FASED_DISPOSABLE_VM_BUILD === "1",
);
assert.ok(process.getuid() > 0);
const args = process.argv.slice(2);
assert.ok(
  args.length === 0 ||
    (args.length === 1 &&
      [
        "--application-reader",
        "--application-reader35",
        "--application-reader36",
      ].includes(args[0])),
  "Unknown predecessor fixture mode",
);
if (
  [
    "--application-reader",
    "--application-reader35",
    "--application-reader36",
  ].includes(args[0])
) {
  const readers = {
    "--application-reader": [33, "ecc832e55afa0d62f42cacfdefa5c9cd5d73c8fb"],
    "--application-reader35": [35, "90bdf4a31bb658f4358d5e71a8c9f50dc3ebbf3a"],
    "--application-reader36": [36, "ad910705246844c333eae8b41b742cb818a58069"],
  };
  // Archived core sources with an inert entrypoint and no public updater.
  const [sourceSchema, source] = readers[args[0]];
  const run = (stage, tool, argv, options) =>
    runReaderStage(
      { schema: sourceSchema, ref: source },
      stage,
      tool,
      argv,
      options,
    );
  const suffix = sourceSchema === 33 ? "" : String(sourceSchema);
  const output = path.resolve(
    process.env.FASED_PREDECESSOR_OUTPUT ?? ".evidence/b01-connected-install",
  );
  const scratch = path.join(output, "reader-predecessor-source" + suffix);
  await mkdir(output, { recursive: true, mode: 0o700 });
  await mkdir(scratch, { mode: 0o700 });
  let report;
  try {
    try {
      run("object", "git", ["cat-file", "-e", source + "^{commit}"]);
    } catch (error) {
      // Only an absent object may use the existing acquisition path. A stalled
      // or interrupted tool fails this reader; it is never retried as a fetch.
      if (error.cause?.status !== 128 || error.cause?.signal) throw error;
      run("fetch", "git", ["fetch", "--depth=1", "origin", source]);
    }
    const archive = run("archive", "git", ["archive", "--format=tar", source], {
      maxBuffer: 32 << 20,
      stdio: ["ignore", "pipe", "inherit"],
    });
    run("extract", "tar", ["-xf", "-", "-C", scratch], {
      input: archive,
      stdio: ["pipe", "inherit", "inherit"],
    });
    assert.match(
      await readFile(path.join(scratch, "internal/core/update.go"), "utf8"),
      new RegExp(`const StateSchema = ${sourceSchema}\\b`),
    );
    const entry = await readFile(
      new URL(
        "./fixtures/application-reader-predecessor.go.txt",
        import.meta.url,
      ),
    );
    const entryPath = path.join(scratch, "cmd/fased-reader-fixture");
    await mkdir(entryPath);
    await writeFile(path.join(entryPath, "main.go"), entry);
    const binary = path.join(
      output,
      `reader-predecessor-schema${sourceSchema}`,
    );
    run(
      "build",
      "go",
      [
        "build",
        "-buildvcs=false",
        "-trimpath",
        "-o",
        binary,
        "./cmd/fased-reader-fixture",
      ],
      { cwd: scratch, env: { ...process.env, GOTOOLCHAIN: "local" } },
    );
    report = {
      result: "PASS",
      sourceCommit: source,
      sourceSchema,
      fixtureOnly: true,
      exactHistoricalBinary: false,
      classification: `Actual archived schema${sourceSchema} core; minimal standalone fixture entry; no installed-service or release acceptance`,
      archiveSha256: sha(archive),
      entrySha256: sha(entry),
      binarySha256: sha(await readFile(binary)),
    };
  } finally {
    // This exact private source directory was exclusively created above; it
    // contains only the immutable archive and injected fixture entrypoint.
    // Async recursive fs cleanup stalled after successful CI compilation.
    run("cleanup", "/usr/bin/rm", [
      "--recursive",
      "--force",
      "--one-file-system",
      "--",
      scratch,
    ]);
  }
  await writeFile(
    path.join(output, `reader-predecessor${suffix}-build.json`),
    JSON.stringify(report, null, 2) + "\n",
  );
  process.exit(0);
}
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
  const { storage } = await currentStorageFixture(scratch);
  // This composed reader reuses today's UI for the account/update journeys.
  // Adapt only public health metadata and an authenticated GET-only Updates
  // projection for this caller's current UI. The shared storage composer and
  // genuine-old-UI bootstrap fixtures retain their historical routing. No new
  // command, owner/mutation route, worker or controller authority is enabled.
  const httpPath = path.join(scratch, "internal/core/http.go"),
    http = await readFile(httpPath, "utf8"),
    healthMarker = "\t\twriteJSON(w, 200, h.store.Health())";
  assert.equal(http.split(healthMarker).length, 2);
  const projectionMarker =
    '\tswitch r.URL.Path {\n\tcase "/api/v1/updates/preferences":';
  assert.equal(http.split(projectionMarker).length, 2);
  const authentication = http.indexOf('"owner authentication required"');
  assert.ok(
    authentication >= 0 && authentication < http.indexOf(projectionMarker),
  );
  assert.ok(!http.includes('case "/api/v1/updates/application":'));
  await writeFile(
    httpPath,
    http
      .replace(
        healthMarker,
        `\t\tlocation, transport := "local", "loopback"
\t\tif h.publicHost != "" && r.Host == h.publicHost { location, transport = "remote", "private-https" }
\t\twriteJSON(w, 200, struct {
\t\t\tHealth
\t\t\tConnection map[string]string \x60json:"connection"\x60
\t\t}{h.store.Health(), map[string]string{"location": location, "transport": transport}})`,
      )
      .replace(
        projectionMarker,
        `\tswitch r.URL.Path {
\tcase "/api/v1/updates/application":
\t\tif r.Method != http.MethodGet { writeError(w,405,"method not allowed");return }
\t\tv,e:=h.store.ApplicationUpdates(r.Context())
\t\tif e!=nil { writeError(w,500,"update status unavailable");return }
\t\twriteJSON(w,200,v)
\t\treturn
\tcase "/api/v1/updates/preferences":`,
      ),
  );
  execFileSync("gofmt", ["-w", httpPath]);
  const runtimeMetadata = {
    fixtureOnly: true,
    source:
      "historical health plus current connection descriptor and authenticated read-only Updates projection; nil controller",
    httpSHA256: sha(await readFile(httpPath)),
  };
  // Installer-build, core-test and owner release commands do not produce this inert UI.
  // Keep every other package field, UI input and lockfile bound to the baseline.
  const packageInputs = (value) => {
    const inputs = structuredClone(value);
    delete inputs.scripts["build:installer"];
    delete inputs.scripts["test:core"];
    delete inputs.scripts.release;
    return inputs;
  };
  assert.deepEqual(
    packageInputs(JSON.parse(await readFile("package.json", "utf8"))),
    packageInputs(
      JSON.parse(
        execFileSync("git", ["show", ref + ":package.json"], {
          encoding: "utf8",
        }),
      ),
    ),
  );
  // Hash the reused current bundle separately; it is not historical UI proof.
  assert.equal(
    execFileSync(
      "git",
      ["diff", ref, "--", "ui", "package-lock.json", "ui.go"],
      { encoding: "utf8" },
    ),
    "",
  );
  await cp("dist", path.join(scratch, "dist"), { recursive: true });
  const outputs = {};
  for (const role of ["app", "signer"]) {
    const dest = path.join(root, "predecessor-" + role);
    execFileSync(
      "go",
      ["build", "-buildvcs=false", "-o", dest, "./cmd/fased-" + role],
      {
        cwd: scratch,
        stdio: "inherit",
      },
    );
    outputs[role] = sha(await readFile(dest));
  }
  // The inspection bridge changes its guarded reader, never the financial
  // contract. Build a current guard with the exact source36 contract, isolated
  // from ordinary serve/setup commands and excluded from every release package.
  const guardedSource = path.join(scratch, "guarded-source");
  await mkdir(guardedSource);
  for (const file of [
    "cmd",
    "internal",
    "packaging",
    "dist",
    "go.mod",
    "go.sum",
    "ui.go",
  ])
    await cp(file, path.join(guardedSource, file), { recursive: true });
  // This test binary only supplies the guarded inspection entrypoint. The
  // custody-maintenance tests exercise a newer contract deliberately removed
  // from this older-reader projection; they run in the normal core suite.
  await rm(
    path.join(
      guardedSource,
      "cmd/fased-app/application_custody_maintenance_linux_test.go",
    ),
  );
  const guardedContractSHA = await projectPasswordOnlyContract(guardedSource);
  const guardedCommandSHA = sha(
    await readFile(path.join(guardedSource, "cmd/fased-app/updates.go")),
  );
  await writeFile(
    path.join(guardedSource, "cmd/fased-app/zz_disposable_guarded_reader.go"),
    `package main
import "os"
func init() { if len(os.Args)>1 && os.Args[1]=="serve" { os.Exit(92) } }
`,
  );
  // The existing VM fault/replay harness is test-only; keep its TestMain
  // boundary hook in this immutable candidate so cuts exercise the real child.
  const guarded = path.join(root, "guarded-app");
  execFileSync(
    "go",
    ["test", "-c", "-buildvcs=false", "-o", guarded, "./cmd/fased-app"],
    {
      cwd: guardedSource,
      stdio: "inherit",
    },
  );
  const contract = (binary) =>
    JSON.parse(
      execFileSync(binary, ["updates", "compatibility"], { encoding: "utf8" }),
    ).contract;
  assert.deepEqual(
    contract(guarded),
    contract(path.join(root, "predecessor-app")),
    "guarded candidate must keep the exact financial reader contract",
  );
  const inspection = JSON.parse(
    execFileSync(guarded, ["fixture-inspection-compatibility"], {
      encoding: "utf8",
    }),
  );
  assert.equal(inspection.capability, "fixture-installed-inspection-v1");
  assert.throws(
    () => execFileSync(guarded, ["serve"], { stdio: "pipe" }),
    (error) => error.status === 92,
    "guarded fixture must refuse ordinary serve",
  );
  const unhealthy = path.join(root, "unhealthy-app");
  execFileSync(
    "go",
    [
      "build",
      "-buildvcs=false",
      "-o",
      unhealthy,
      "./internal/custodyinstall/testdata/unhealthy-core",
    ],
    {
      cwd: guardedSource,
      stdio: "inherit",
    },
  );
  assert.deepEqual(
    contract(unhealthy).state,
    contract(path.join(root, "predecessor-app")).state,
    "failed-health trial must preserve the exact source state contract",
  );
  outputs.unhealthyApp = sha(await readFile(unhealthy));
  assert.throws(
    () =>
      execFileSync(
        guarded,
        [
          "status",
          "--data-dir",
          path.join(guardedSource, "absent-owner-state"),
        ],
        { stdio: "pipe" },
      ),
    (error) => error.status !== 92,
    "readiness status must reach the production IPC verifier",
  );
  outputs.guardedApp = sha(await readFile(guarded));
  outputs.guardedContractSource = guardedContractSHA;
  outputs.guardedCommandSource = guardedCommandSHA;
  await writeFile(
    path.join(root, "predecessor-build.json"),
    JSON.stringify(
      {
        result: "PASS",
        baseCommit: ref,
        fixtureOnly: true,
        exactHistoricalBinary: false,
        storage,
        runtimeMetadata,
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
