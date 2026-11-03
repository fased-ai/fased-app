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
  const suffix = sourceSchema === 33 ? "" : String(sourceSchema);
  const output = path.resolve(
    process.env.FASED_PREDECESSOR_OUTPUT ?? ".evidence/b01-connected-install",
  );
  const scratch = path.join(output, "reader-predecessor-source" + suffix);
  await mkdir(output, { recursive: true, mode: 0o700 });
  await mkdir(scratch, { mode: 0o700 });
  try {
    try {
      execFileSync("git", ["cat-file", "-e", source + "^{commit}"]);
    } catch {
      execFileSync("git", ["fetch", "--depth=1", "origin", source], {
        stdio: "inherit",
      });
    }
    const archive = execFileSync("git", ["archive", "--format=tar", source], {
      maxBuffer: 32 << 20,
    });
    execFileSync("tar", ["-xf", "-", "-C", scratch], { input: archive });
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
    execFileSync(
      "go",
      [
        "build",
        "-buildvcs=false",
        "-trimpath",
        "-o",
        binary,
        "./cmd/fased-reader-fixture",
      ],
      { cwd: scratch, stdio: "inherit" },
    );
    await writeFile(
      path.join(output, `reader-predecessor${suffix}-build.json`),
      JSON.stringify(
        {
          result: "PASS",
          sourceCommit: source,
          sourceSchema,
          fixtureOnly: true,
          exactHistoricalBinary: false,
          classification: `Actual archived schema${sourceSchema} core; minimal standalone fixture entry; no installed-service or release acceptance`,
          archiveSha256: sha(archive),
          entrySha256: sha(entry),
          binarySha256: sha(await readFile(binary)),
        },
        null,
        2,
      ) + "\n",
    );
  } finally {
    await rm(scratch, { recursive: true, force: true });
  }
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
  for (const file of ["cmd", "internal", "dist", "go.mod", "go.sum", "ui.go"])
    await cp(file, path.join(guardedSource, file), { recursive: true });
  const guardedContractSHA = await projectPasswordOnlyContract(guardedSource);
  await writeFile(
    path.join(guardedSource, "cmd/fased-app/zz_disposable_guarded_reader.go"),
    `package main
import "os"
func init() {
 if len(os.Args)>1 { switch os.Args[1] {
 case "fixture-reader-guard", "fixture-inspection-compatibility", "owner-fixture-inspection", "fixture-inspection-cold", "fixture-inspection-init", "fixture-inspection-empty": return
 } }
 if len(os.Args)==3 && os.Args[1]=="updates" && os.Args[2]=="compatibility" { return }
 if len(os.Args)>3 && os.Args[1]=="custody" && os.Args[2]=="fixture-inspection" && (os.Args[3]=="bridge" || os.Args[3]=="rollback") { return }
 os.Exit(92)
}
`,
  );
  const guarded = path.join(root, "guarded-app");
  execFileSync(
    "go",
    ["build", "-buildvcs=false", "-o", guarded, "./cmd/fased-app"],
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
  outputs.guardedApp = sha(await readFile(guarded));
  outputs.guardedContractSource = guardedContractSHA;
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
