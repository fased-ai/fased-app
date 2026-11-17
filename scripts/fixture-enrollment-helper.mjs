// Build only disposable, self-signed inputs for the hosted enrollment check.
// This module never runs the helper or performs an operating-system change.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { constants } from "node:fs";
import {
  copyFile,
  lstat,
  mkdtemp,
  readdir,
  readFile,
  rm,
  writeFile,
} from "node:fs/promises";
import { join, relative, resolve } from "node:path";

const digest = (data) => createHash("sha256").update(data).digest("hex");
const versionFlag = (version) =>
  `-X github.com/fased-ai/fased-app/internal/core.Version=${version}`;

// Test both existence and a dangling symlink. A sudo or OS error is not proof
// that a protected path is absent.
export function protectedPathMissing(path, osTool) {
  for (const flag of ["-e", "-L"]) {
    try {
      osTool("/usr/bin/test", [flag, path]);
      return false;
    } catch (error) {
      if (error.status !== 1 || String(error.stderr || "").trim()) throw error;
    }
  }
  return true;
}

// A loopback-only channel for the signed disposable fixture. The protected
// helper still verifies its own pinned seed, metadata and payload hashes.
export async function startFixtureServer(work, env, onError) {
  assert.ok(process.getuid() > 0 && process.getuid() === process.geteuid());
  const dir = await lstat(work);
  assert.ok(
    dir.isDirectory() &&
      dir.uid === process.getuid() &&
      (dir.mode & 0o777) === 0o700,
  );
  for (const name of ["key.pem", "ca.pem"]) {
    const file = await lstat(join(work, name));
    assert.ok(
      file.isFile() &&
        file.uid === process.getuid() &&
        file.nlink === 1 &&
        (file.mode & 0o777) === 0o600,
    );
  }
  const program = String.raw`
const fs = require("node:fs"), https = require("node:https"), path = require("node:path");
const work = process.argv[1], root = fs.realpathSync(path.join(work, "repo-base"));
const server = https.createServer({key: fs.readFileSync(path.join(work, "key.pem")), cert: fs.readFileSync(path.join(work, "ca.pem"))}, (req, res) => {
  if (req.method !== "GET" || !/^\/[A-Za-z0-9._/-]+$/.test(req.url) || req.url.split("/").some(p => p === "." || p === "..")) { res.writeHead(404).end(); return; }
  const file = path.join(root, req.url);
  fs.realpath(file, (err, actual) => {
    if (err || !actual.startsWith(root + path.sep)) { res.writeHead(404).end(); return; }
    fs.stat(actual, (e, stat) => {
      if (e || !stat.isFile()) { res.writeHead(404).end(); return; }
      res.writeHead(200, {"Content-Length": stat.size});
      fs.createReadStream(actual).on("error", () => res.destroy()).pipe(res);
    });
  });
});
server.listen(17449, "127.0.0.1", () => console.log("ready"));
`;
  const server = spawn(process.execPath, ["-e", program, work], {
    env,
    stdio: ["ignore", "pipe", "pipe"],
  });
  server.stderr.on("data", (b) => onError(b));
  try {
    await new Promise((resolveReady, reject) => {
      const timer = setTimeout(
        () => reject(new Error("fixture channel did not start")),
        10000,
      );
      server.once("error", (err) => {
        clearTimeout(timer);
        reject(err);
      });
      server.once("exit", () => {
        clearTimeout(timer);
        reject(new Error("fixture channel exited"));
      });
      server.stdout.once("data", (b) => {
        clearTimeout(timer);
        if (String(b).trim() === "ready") resolveReady();
        else reject(new Error("unexpected fixture channel response"));
      });
    });
  } catch (err) {
    server.kill("SIGTERM");
    throw err;
  }
  return server;
}

export async function verifyEmbeddedSigner(helper, source, expected) {
  await new Promise((resolveVerified, reject) => {
    const child = spawn(helper, ["--emit-embedded-signer"], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    const hash = createHash("sha256");
    let pending = Buffer.alloc(0),
      header,
      size = 0,
      done = false;
    const fail = (error) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      child.kill("SIGKILL");
      reject(error);
    };
    const timer = setTimeout(
      () => fail(new Error("embedded signer check timed out")),
      30000,
    );
    child.once("error", fail);
    child.stderr.resume();
    child.stdout.on("data", (chunk) => {
      try {
        if (!header) {
          pending = Buffer.concat([pending, chunk]);
          const newline = pending.indexOf(10);
          if (newline < 0) {
            if (pending.length > 4096)
              throw new Error("oversized signer header");
            return;
          }
          if (newline === 0 || newline > 4096)
            throw new Error("invalid signer header");
          header = JSON.parse(pending.subarray(0, newline).toString());
          chunk = pending.subarray(newline + 1);
          pending = Buffer.alloc(0);
        }
        size += chunk.length;
        if (size > 64 << 20) throw new Error("oversized signer payload");
        hash.update(chunk);
      } catch (error) {
        fail(error);
      }
    });
    child.once("close", (code) => {
      if (done) return;
      try {
        assert.equal(code, 0);
        assert.equal(header?.productVersion, "0.1.11");
        assert.equal(header.source, source);
        assert.equal(header.sha256, expected);
        assert.equal(header.bytes, size);
        assert.equal(hash.digest("hex"), expected);
        done = true;
        clearTimeout(timer);
        resolveVerified();
      } catch (error) {
        fail(error);
      }
    });
  });
}

export async function buildEnrollmentHelper({
  evidence,
  source,
  sourceRoot,
  sourceData,
}) {
  assert.ok(process.getuid() > 0 && process.getuid() === process.geteuid());
  assert.match(source, /^[a-f0-9]{40}$/);
  for (const path of [evidence, sourceRoot, sourceData])
    assert.ok(
      path === resolve(path) && !/\s/.test(path),
      "fixed absolute fixture path required",
    );
  const evidenceStat = await lstat(evidence);
  assert.ok(
    evidenceStat.isDirectory() &&
      evidenceStat.uid === process.getuid() &&
      (evidenceStat.mode & 0o777) === 0o700,
    "private fixture evidence directory required",
  );
  assert.equal(sourceRoot, join(evidence, "local", "app"));
  assert.equal(sourceData, join(evidence, "local", "data"));
  const owner = String(process.getuid());
  const machine = (await readFile("/etc/machine-id", "utf8")).trim();
  assert.match(machine, /^[a-f0-9]{32}$/);
  const machineHash = digest(machine);
  const work = await mkdtemp(join(evidence, "helper-inputs-"));
  const outputs = [
    ["fased-app-tests", "./cmd/fased-app", versionFlag("0.1.11")],
    ["fased-app-next", "./cmd/fased-app", versionFlag("0.1.12")],
    ["fased-signer", "./cmd/fased-signer", "-buildid=fased-ci-enrollment-base"],
    [
      "fased-signer-next",
      "./cmd/fased-signer",
      "-buildid=fased-ci-enrollment-target",
    ],
  ];
  const artifacts = {};
  for (const [name, pkg, flags] of outputs) {
    const path = join(work, name);
    execFileSync(
      "go",
      ["test", "-c", "-buildvcs=false", "-ldflags", flags, "-o", path, pkg],
      {
        stdio: "inherit",
        timeout: 180000,
      },
    );
    artifacts[name] = digest(await readFile(path));
  }
  const receipt = Buffer.from(
    JSON.stringify({
      version: 1,
      fixtureOnly: true,
      declaredSourceCommit: source,
      artifacts,
    }) + "\n",
  );
  await writeFile(join(work, "build-receipt.json"), receipt, {
    mode: 0o600,
    flag: "wx",
  });
  execFileSync(
    join(work, "fased-app-tests"),
    ["-test.run=^TestProduceCustodyPairFixture$", "-test.count=1"],
    {
      env: {
        ...process.env,
        FASED_CUSTODY_FIXTURE_DIR: work,
        FASED_CUSTODY_FIXTURE_SOURCE: source,
        FASED_CUSTODY_FIXTURE_BUILD_SHA256: digest(receipt),
      },
      stdio: "inherit",
      timeout: 180000,
    },
  );
  const seed = digest(await readFile(join(work, "seed.json")));
  const ca = digest(await readFile(join(work, "ca.pem")));
  const trust = JSON.parse(await readFile(join(work, "fixture.json"), "utf8"));
  assert.equal(trust.sourceSha256, artifacts["fased-app-tests"]);
  assert.equal(trust.seedSha256, seed);
  assert.equal(trust.caSha256, ca);
  assert.equal(trust.targetSignerSha256, artifacts["fased-signer-next"]);

  // Build a private copy of the installer package so an interrupted fixture
  // build cannot leave go:embed binaries in the application source tree.
  const staged = await mkdtemp(resolve(".evidence/ci-enrollment-helper-src-"));
  const stagedIdentity = await lstat(staged);
  const stagedNames = new Set();
  try {
    for (const entry of await readdir(resolve("cmd/fased-install"), {
      withFileTypes: true,
    })) {
      if (
        !entry.isFile() ||
        !entry.name.endsWith(".go") ||
        entry.name.endsWith("_test.go")
      )
        continue;
      await copyFile(
        resolve("cmd/fased-install", entry.name),
        join(staged, entry.name),
        constants.COPYFILE_EXCL,
      );
      stagedNames.add(entry.name);
    }
    for (const [name, file] of [
      ["fased-signer", "wallet-signer.bin"],
      ["fased-signer-next", "wallet-signer-fixture-successor.bin"],
    ]) {
      await copyFile(
        join(work, name),
        join(staged, file),
        constants.COPYFILE_EXCL,
      );
      stagedNames.add(file);
    }
    const ldflags = [
      versionFlag("0.1.11"),
      `-X main.compiledWalletSource=${source}`,
      `-X main.compiledWalletSHA256=${artifacts["fased-signer"]}`,
      "-X main.fixtureSuccessorVersion=0.1.12",
      `-X main.fixtureSuccessorSource=${source}`,
      `-X main.fixtureSuccessorSHA256=${artifacts["fased-signer-next"]}`,
      `-X main.fixtureSeedSHA256=${seed}`,
      `-X main.fixtureCASHA256=${ca}`,
      `-X github.com/fased-ai/fased-app/internal/hostplatform.FixtureOwnerUID=${owner}`,
      `-X github.com/fased-ai/fased-app/internal/hostplatform.FixtureMachineSHA256=${machineHash}`,
      `-X github.com/fased-ai/fased-app/internal/hostplatform.FixtureSourceRoot=${sourceRoot}`,
      `-X github.com/fased-ai/fased-app/internal/hostplatform.FixtureSourceData=${sourceData}`,
    ].join(" ");
    execFileSync(
      "go",
      [
        "build",
        "-buildvcs=false",
        "-tags",
        "custody_acceptance_fixture,wallet_payload,fased_realhost_fixture",
        "-ldflags",
        ldflags,
        "-o",
        join(work, "fased-install"),
        "./" + relative(process.cwd(), staged),
      ],
      {
        env: { ...process.env, CGO_ENABLED: "0" },
        stdio: "inherit",
        timeout: 180000,
      },
    );
  } finally {
    const stat = await lstat(staged);
    assert.ok(
      stat.isDirectory() &&
        stat.dev === stagedIdentity.dev &&
        stat.ino === stagedIdentity.ino,
      "fixture build directory changed; preserve it",
    );
    for (const entry of await readdir(staged, { withFileTypes: true }))
      assert.ok(
        entry.isFile() && stagedNames.has(entry.name),
        "foreign fixture build input; preserve it",
      );
    await rm(staged, { recursive: true });
  }
  const helper = join(work, "fased-install");
  await verifyEmbeddedSigner(helper, source, artifacts["fased-signer"]);
  return {
    work,
    helper,
    helperSHA256: digest(await readFile(helper)),
    seed,
    ca,
    artifacts,
  };
}
