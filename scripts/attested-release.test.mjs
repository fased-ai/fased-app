import { releaseSequence } from "./release-ubuntu.mjs";
import { test } from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  mkdirSync,
  mkdtempSync,
  writeFileSync,
  readFileSync,
  rmSync,
  chmodSync,
} from "node:fs";
import path from "node:path";
import { publishAttested, verifierEnvironment } from "./release-ubuntu.mjs";
import {
  recoverPublicCandidate,
  prepareReleaseCandidate,
} from "./release-source.mjs";
const sha = (b) => createHash("sha256").update(b).digest("hex");
function fixture(t) {
  const root = path.resolve(".evidence/attested-release-tests");
  mkdirSync(root, { recursive: true, mode: 0o700 });
  const parent = mkdtempSync(path.join(root, "case-"));
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const directory = path.join(parent, "candidate");
  mkdirSync(directory, { mode: 0o700 });
  const c = JSON.parse(readFileSync("release/ubuntu.json"));
  const files = Object.fromEntries(
    [
      "fased-app-linux-amd64.tar.gz",
      "fased-install-linux-amd64",
      "fased-app-linux-amd64.provenance.sigstore.json",
      "install.sh",
    ].map((name) => [name, Buffer.from("inert disposable " + name)]),
  );
  const identity = (name) => ({
    file: name,
    size: files[name].length,
    sha256: sha(files[name]),
  });
  const packet = {
    version: 1,
    productVersion: c.version,
    source: "a".repeat(40),
    bundleSha256: "b".repeat(64),
    archive: identity("fased-app-linux-amd64.tar.gz"),
    installer: identity("fased-install-linux-amd64"),
    entry: identity("install.sh"),
    release: {
      installerSha256: sha(files["fased-install-linux-amd64"]),
      sourceTree: "c".repeat(40),
      tagObject: "d".repeat(40),
      packageLockSha256: "e".repeat(64),
      goSumSha256: "f".repeat(64),
      workflowCommit: "a".repeat(40),
      runId: "123",
      runAttempt: "1",
      provenance: identity("fased-app-linux-amd64.provenance.sigstore.json"),
    },
  };
  files["fased-release.json"] = Buffer.from(JSON.stringify(packet) + "\n");
  for (const [name, raw] of Object.entries(files))
    writeFileSync(path.join(directory, name), raw, { mode: 0o600 });
  writeFileSync(
    path.join(directory, "first-install-seed.json"),
    readFileSync(c.seedFile),
    { mode: 0o600 },
  );
  let release,
    mutations = 0,
    candidateRuns = 0,
    proofRejected = false;
  const api = async (method, url, body) => {
    if (method !== "GET") mutations++;
    if (url.endsWith("/immutable-releases"))
      throw Error("GITHUB_TOKEN must not need admin access");
    if (url === "/repos/fased-ai/fased-app")
      return {
        id: 1426211857,
        owner: { id: 279582504 },
        full_name: c.repository,
      };
    if (url.includes("/git/ref/tags/"))
      return {
        ref: `refs/tags/v${c.version}`,
        object: { type: "tag", sha: packet.release.tagObject },
      };
    if (url.includes("/git/tags/"))
      return {
        sha: packet.release.tagObject,
        tag: `v${c.version}`,
        object: { type: "commit", sha: packet.source },
      };
    if (url.includes("/releases/tags/")) {
      if (!release)
        throw Object.assign(Error("not yet created"), { status: 404 });
      return structuredClone(release);
    }
    if (method === "POST" && url.endsWith("/releases")) {
      assert.equal(release, undefined);
      release = {
        id: 11,
        tag_name: body.tag_name,
        draft: true,
        prerelease: false,
        immutable: false,
        assets: [],
      };
      return structuredClone(release);
    }
    if (method === "UPLOAD") {
      const name = url.split("?name=")[1];
      const a = {
        id: 20 + release.assets.length,
        name,
        size: body.length,
        state: "uploaded",
        digest: `sha256:${sha(body)}`,
      };
      release.assets.push(a);
      return structuredClone(a);
    }
    if (url.endsWith("/releases/11")) {
      if (method === "PATCH") {
        assert.equal(body.tag_name, `v${c.version}`);
        assert.equal(body.target_commitish, packet.source);
        release.draft = false;
        release.immutable = true;
      }
      return structuredClone(release);
    }
    throw Error("unexpected controlled API request");
  };
  const execute = (program, args, options) => {
    assert.ok(
      options.env,
      "verifier process must not inherit the publisher environment",
    );
    for (const key of [
      "GH_TOKEN",
      "GITHUB_TOKEN",
      "OPENAI_API_KEY",
      "DBUS_SESSION_BUS_ADDRESS",
    ])
      assert.equal(options.env?.[key], undefined);
    if (program === "go") {
      const expected = JSON.parse(
        readFileSync(args[args.indexOf("--expected-file") + 1]),
      );
      assert.equal(expected.installerSha256, packet.installer.sha256);
      return JSON.stringify({
        BundleSHA256: proofRejected
          ? "0".repeat(64)
          : packet.release.provenance.sha256,
      });
    }
    candidateRuns++;
    return JSON.stringify({
      version: 4,
      metadataVerified: !args.includes("--verify-candidate"),
      archiveVerified: true,
      bundleVerified: true,
      provenanceVerified: true,
      installerInvoked: false,
      publicActivation: false,
      seedSha256: c.seedSha256,
      archiveSha256: packet.archive.sha256,
      sequence: releaseSequence(c.version),
    });
  };
  const fetchResponse = async (url) =>
    new Response(files[new URL(url).pathname.split("/").at(-1)], {
      status: 200,
    });
  return {
    parent,
    directory,
    c,
    packet,
    files,
    api,
    execute,
    fetchResponse,
    get release() {
      return release;
    },
    get mutations() {
      return mutations;
    },
    get candidateRuns() {
      return candidateRuns;
    },
    rejectProof() {
      proofRejected = true;
    },
  };
}

test("attested publication runs proof before executable, publishes once and resumes immutable readback", async (t) => {
  const f = fixture(t),
    options = {
      api: f.api,
      execute: f.execute,
      fetchResponse: f.fetchResponse,
      stateRoot: path.join(f.parent, "publisher"),
    };
  const first = await publishAttested(f.c, f.directory, options);
  assert.equal(first.verified, true);
  assert.equal(first.releaseId, "11");
  assert.equal(first.automaticUpdates, false);
  const writes = f.mutations;
  assert.equal(writes, 7);
  await publishAttested(f.c, f.directory, options);
  assert.equal(f.mutations, writes);
});
test("attested publisher refuses an independently failed installer proof before execution or API mutation", async (t) => {
  const f = fixture(t);
  f.rejectProof();
  await assert.rejects(
    publishAttested(f.c, f.directory, {
      api: f.api,
      execute: f.execute,
      fetchResponse: f.fetchResponse,
      stateRoot: path.join(f.parent, "publisher"),
    }),
    /Independent installer\/archive provenance failed/,
  );
  assert.equal(f.candidateRuns, 0);
  assert.equal(f.mutations, 0);
});
test("workflow build permissions are normalized before private publisher admission", async (t) => {
  const f = fixture(t);
  const options = {
    api: f.api,
    execute: f.execute,
    fetchResponse: f.fetchResponse,
    stateRoot: path.join(f.parent, "publisher"),
  };
  for (const name of Object.keys(f.files))
    chmodSync(
      path.join(f.directory, name),
      name === "fased-install-linux-amd64" || name === "install.sh"
        ? 0o755
        : 0o644,
    );
  await assert.rejects(
    publishAttested(f.c, f.directory, options),
    /Private regular publisher file required/,
  );
  assert.equal(f.candidateRuns, 0);
  assert.equal(f.mutations, 0);
  for (const name of [...Object.keys(f.files), "first-install-seed.json"])
    chmodSync(path.join(f.directory, name), 0o600);
  const result = await publishAttested(f.c, f.directory, options);
  assert.equal(result.verified, true);
});
test("verifier environment excludes publishing and unrelated secrets", () => {
  const env = verifierEnvironment({
    PATH: "/usr/bin",
    HOME: "/fixture",
    GH_TOKEN: "do-not-forward",
    GITHUB_TOKEN: "do-not-forward",
    OPENAI_API_KEY: "do-not-forward",
    DBUS_SESSION_BUS_ADDRESS: "do-not-forward",
  });
  assert.equal(env.PATH, "/usr/bin");
  assert.equal(env.HOME, "/fixture");
  assert.equal(env.GH_TOKEN, undefined);
  assert.equal(env.GITHUB_TOKEN, undefined);
  assert.equal(env.OPENAI_API_KEY, undefined);
  assert.equal(env.DBUS_SESSION_BUS_ADDRESS, undefined);
  assert.equal(env.GOPROXY, "off");
});
test("zero-Actions-artifact recovery reconstructs exact immutable bytes and seed or refuses mismatch", async (t) => {
  for (const variant of ["complete", "mutable", "source", "run", "digest"]) {
    const f = fixture(t);
    const root = path.join(f.parent, "recovery");
    mkdirSync(root, { mode: 0o700 });
    const assets = Object.entries(f.files).map(([name, raw], i) => ({
      id: 20 + i,
      name,
      size: raw.length,
      state: "uploaded",
      digest: `sha256:${sha(raw)}`,
    }));
    const release = {
      id: 11,
      tag_name: `v${f.c.version}`,
      draft: false,
      prerelease: false,
      immutable: variant !== "mutable",
      assets,
    };
    const call = (program, args) => {
      if (!args.includes("Accept: application/octet-stream"))
        return JSON.stringify(release);
      const id = Number(args.at(-1).split("/").at(-1));
      const raw = f.files[assets.find((a) => a.id === id).name];
      return variant === "digest" ? Buffer.from("changed") : raw;
    };
    const inputs = {
      version: f.c.version,
      source: variant === "source" ? "0".repeat(40) : f.packet.source,
      runId: variant === "run" ? "999" : "123",
      root,
      workspace: process.cwd(),
      call,
    };
    if (variant === "complete") {
      const result = recoverPublicCandidate(inputs);
      assert.deepEqual(
        readFileSync(path.join(result, "fased-release.json")),
        f.files["fased-release.json"],
      );
      assert.equal(
        sha(readFileSync(path.join(result, "first-install-seed.json"))),
        f.c.seedSha256,
      );
    } else assert.throws(() => recoverPublicCandidate(inputs));
  }
});

test("same command recovers a published release after original run retention/readback failed without rebuilding", async (t) => {
  const f = fixture(t),
    workspace = f.parent;
  mkdirSync(path.join(workspace, "release/public"), {
    recursive: true,
    mode: 0o700,
  });
  writeFileSync(
    path.join(workspace, "release/ubuntu.json"),
    JSON.stringify(f.c),
    { mode: 0o600 },
  );
  writeFileSync(
    path.join(workspace, f.c.seedFile),
    readFileSync(f.c.seedFile),
    { mode: 0o600 },
  );
  const release = {
    id: 11,
    tag_name: `v${f.c.version}`,
    draft: false,
    prerelease: false,
    immutable: true,
    assets: Object.entries(f.files).map(([name, b], i) => ({
      id: 20 + i,
      name,
      size: b.length,
      state: "uploaded",
      digest: `sha256:${sha(b)}`,
    })),
  };
  const commands = [];
  const run = (program, args, options) => {
    commands.push([program, ...args]);
    if (program === "git") {
      if (args[0] === "remote")
        return "git@github-fased:fased-ai/fased-app.git";
      if (args[0] === "status" || args[0] === "fetch") return "";
      if (args[0] === "rev-parse")
        return args[1] === "HEAD"
          ? f.packet.source
          : f.packet.release.sourceTree;
      if (args[0] === "ls-remote")
        return `${f.packet.source}\trefs/tags/v${f.c.version}^{}\n`;
      throw Error("no Git mutation is allowed during recovery");
    }
    if (args[0] === "auth") return "";
    if (args[0] === "pr" && args[1] === "checks") return "";
    if (args[0] === "pr" && args[1] === "view")
      return JSON.stringify({
        number: 77,
        state: "MERGED",
        baseRefName: "main",
        headRefOid: f.packet.source,
        mergeCommit: { oid: f.packet.source },
      });
    if (args[0] === "run" && args[1] === "list")
      return JSON.stringify([
        { databaseId: 123, headSha: f.packet.source, status: "completed" },
      ]);
    if (args[0] === "run" && args[1] === "view")
      return JSON.stringify({
        attempt: 1,
        conclusion: "failure",
        jobs: [
          {
            name: "ubuntu-amd64",
            steps: [
              {
                name: "Verify original provenance offline and stage public signing inputs",
                conclusion: "success",
              },
            ],
          },
        ],
      });
    if (args[0] === "api") {
      const route = args.at(-1);
      if (route.endsWith("/immutable-releases"))
        return JSON.stringify({ enabled: true });
      if (route === "/repos/fased-ai/fased-app")
        return JSON.stringify({
          id: 1426211857,
          owner: { id: 279582504 },
          full_name: f.c.repository,
        });
      if (route.endsWith("/artifacts"))
        return JSON.stringify({ artifacts: [] });
      if (route.includes("/releases/tags/")) return JSON.stringify(release);
      const asset = release.assets.find((a) =>
        route.endsWith(`/assets/${a.id}`),
      );
      if (asset) return f.files[asset.name];
    }
    throw Error("unexpected recovery command");
  };
  const directory = await prepareReleaseCandidate(f.c.version, "77", {
    run,
    workspace,
    progress() {},
  });
  const api = async (method, url, body) => {
    assert.equal(method, "GET", "recovery must not mutate a completed release");
    if (url.includes("/releases/")) return structuredClone(release);
    return f.api(method, url, body);
  };
  const result = await publishAttested(f.c, directory, {
    api,
    execute: f.execute,
    fetchResponse: f.fetchResponse,
    stateRoot: path.join(f.parent, "publisher"),
  });
  assert.equal(result.verified, true);
  assert.equal(result.releaseId, "11");
  assert.ok(
    !commands.some((c) => c[0] === "git" && ["push", "tag"].includes(c[1])),
  );
});

test("release sequence follows semantic version order independently of GitHub IDs", () => {
  assert.equal(releaseSequence("0.1.4"), "1000005");
  assert.ok(
    BigInt(releaseSequence("1.0.0")) >
      BigInt(releaseSequence("0.999999.999999")),
  );
  assert.ok(
    BigInt(releaseSequence("0.2.0")) > BigInt(releaseSequence("0.1.999999")),
  );
  assert.throws(() => releaseSequence("01.2.3"));
  assert.throws(() => releaseSequence("0.1000000.0"));
});

test("same publication command recovers an exact partial workflow draft without its original journal", async (t) => {
  const f = fixture(t);
  let uploads = 0;
  const options = {
    api: async (...args) => {
      if (args[0] === "UPLOAD" && ++uploads === 3)
        throw Error("disposable workflow interrupted before third upload");
      return f.api(...args);
    },
    execute: f.execute,
    fetchResponse: f.fetchResponse,
    stateRoot: path.join(f.parent, "workflow"),
  };
  await assert.rejects(
    publishAttested(f.c, f.directory, options),
    /interrupted/,
  );
  assert.equal(f.release.draft, true);
  assert.equal(f.release.assets.length, 2);
  const originals = structuredClone(f.release.assets);
  const resumed = {
    ...options,
    api: f.api,
    stateRoot: path.join(f.parent, "owner"),
  };
  const result = await publishAttested(f.c, f.directory, resumed);
  assert.equal(result.verified, true);
  assert.equal(result.releaseId, "11");
  assert.deepEqual(f.release.assets.slice(0, 2), originals);
  assert.equal(f.release.assets.length, 5);
  const mutations = f.mutations;
  await publishAttested(f.c, f.directory, resumed);
  assert.equal(f.mutations, mutations);
});

test("partial draft recovery refuses altered or unexpected uploaded assets before mutation", async (t) => {
  for (const mismatch of ["digest", "unexpected", "duplicate-id"])
    await t.test(mismatch, async (t) => {
      const f = fixture(t);
      let uploads = 0;
      const options = {
        api: async (...args) => {
          if (args[0] === "UPLOAD" && ++uploads === 3)
            throw Error("interrupted");
          return f.api(...args);
        },
        execute: f.execute,
        fetchResponse: f.fetchResponse,
        stateRoot: path.join(f.parent, "workflow"),
      };
      await assert.rejects(
        publishAttested(f.c, f.directory, options),
        /interrupted/,
      );
      if (mismatch === "digest")
        f.release.assets[0].digest = "sha256:" + "0".repeat(64);
      if (mismatch === "unexpected") f.release.assets[0].name = "unrelated";
      if (mismatch === "duplicate-id")
        f.release.assets[1].id = f.release.assets[0].id;
      const mutations = f.mutations;
      await assert.rejects(
        publishAttested(f.c, f.directory, {
          ...options,
          api: f.api,
          stateRoot: path.join(f.parent, "owner"),
        }),
        /inventory|identity/,
      );
      assert.equal(f.mutations, mutations);
    });
});
