// Password-free release authority: protected GitHub workflow + immutable assets
// + offline-verified GitHub/Sigstore build provenance. No local signing secrets.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  readFileSync,
  writeFileSync,
  existsSync,
  chmodSync,
  mkdtempSync,
  rmSync,
} from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { prepareReleaseCandidate } from "./release-source.mjs";
import {
  attestedInventory,
  attestedMultiArchInventory,
  isMainBoundCandidate,
  verifyInstalledAcceptance,
  verifyLocalAcceptance,
  recordLocalAcceptance,
  recordRemoteAcceptance,
} from "./release-acceptance.mjs";
import { seedForArch, validateConfig } from "./prepare-ubuntu-release.mjs";
import {
  githubPublisher,
  runRecordedPublisher,
  verifyAnnotatedTag,
} from "./publish-ubuntu-release.mjs";
import {
  openPublisherJournal,
  readPublisherFile,
  ensurePublisherDirectory,
} from "./publisher-journal.mjs";
const sha = (b) => createHash("sha256").update(b).digest("hex");
const repo = "/repos/fased-ai/fased-app";
const names = [
  "fased-app-linux-amd64.tar.gz",
  "fased-install-linux-amd64",
  "fased-app-linux-amd64.provenance.sigstore.json",
  "install.sh",
  "fased-release.json",
  "fased-app-linux-arm64.tar.gz",
  "fased-install-linux-arm64",
  "fased-app-linux-arm64.provenance.sigstore.json",
  "fased-release-linux-arm64.json",
];

export function releaseSequence(version) {
  if (!/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version))
    throw Error("Canonical stable version required");
  let sequence = 0n;
  for (const part of version.split(".")) {
    const value = BigInt(part);
    if (value >= 1000000n) throw Error("Bounded release version required");
    sequence = sequence * 1000000n + value;
  }
  return String(sequence + 1n);
}

export function verifierEnvironment(source = process.env) {
  const env = {
    GOTOOLCHAIN: "local",
    GOFLAGS: "-mod=readonly",
    GOPROXY: "off",
    GOSUMDB: "off",
  };
  for (const key of [
    "PATH",
    "HOME",
    "USER",
    "LOGNAME",
    "LANG",
    "LC_ALL",
    "TZ",
    "TMPDIR",
    "GOCACHE",
    "GOMODCACHE",
    "GOROOT",
  ])
    if (typeof source[key] === "string") env[key] = source[key];
  return env;
}

export async function publicAsset(
  version,
  name,
  identity,
  fetchResponse = fetch,
) {
  if (!/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version) || !names.includes(name))
    throw Error("Exact public release asset required");
  let address = `https://github.com/fased-ai/fased-app/releases/download/v${version}/${name}`,
    response;
  for (let hop = 0; hop < 2; hop++) {
    response = await fetchResponse(address, {
      redirect: "manual",
      signal: AbortSignal.timeout(60000),
    });
    if (response.status === 200) break;
    const next = response.headers.get("location");
    await response.body?.cancel();
    const u = next ? new URL(next) : null;
    if (
      hop !== 0 ||
      response.status !== 302 ||
      !u ||
      u.protocol !== "https:" ||
      u.host !== "release-assets.githubusercontent.com" ||
      u.username ||
      u.password
    )
      throw Error("Public release asset redirect refused");
    address = u.href;
  }
  if (response.status !== 200 || response.headers.get("content-encoding"))
    throw Error("Complete public asset required");
  const chunks = [];
  let size = 0;
  for await (const chunk of response.body) {
    size += chunk.length;
    if (size > identity.size) throw Error("Public asset exceeds approved size");
    chunks.push(Buffer.from(chunk));
  }
  const bytes = Buffer.concat(chunks);
  if (bytes.length !== identity.size || sha(bytes) !== identity.sha256)
    throw Error("Public asset differs from approved bytes");
  return bytes;
}
export { attestedInventory } from "./release-acceptance.mjs";

export async function verifyPublisherTag(api, packet, inventory) {
  const { version, source } = inventory;
  let { tagObject } = inventory;
  if (isMainBoundCandidate(packet)) {
    const ref = await api("GET", repo + `/git/ref/tags/v${version}`);
    if (
      ref.ref !== `refs/tags/v${version}` ||
      ref.object?.type !== "tag" ||
      !/^[a-f0-9]{40}$/.test(ref.object.sha)
    )
      throw Error("Accepted source requires an existing annotated release tag");
    tagObject = ref.object.sha;
  }
  await verifyAnnotatedTag(api, version, tagObject, source);
  return tagObject;
}

export function loadCandidateInventory(c, directory) {
  const read = (name) =>
    readPublisherFile(path.join(directory, name), 128 * 1024 * 1024);
  const raw = read("fased-release.json");
  const packet = JSON.parse(raw);
  const arm64Path = path.join(directory, "fased-release-linux-arm64.json");
  if (existsSync(arm64Path)) {
    const arm64Raw = read("fased-release-linux-arm64.json");
    const arm64Packet = JSON.parse(arm64Raw);
    return {
      packet,
      raw,
      arm64Packet,
      arm64Raw,
      inventory: attestedMultiArchInventory(
        c,
        packet,
        raw,
        arm64Packet,
        arm64Raw,
      ),
    };
  }
  return { packet, raw, inventory: attestedInventory(c, packet, raw) };
}

function checkRelease(release, c, inventory, partial = false) {
  const expectedCount = inventory.assets.length;
  if (
    String(release.id).match(/^[1-9][0-9]*$/) === null ||
    release.tag_name !== `v${c.version}` ||
    release.prerelease !== false ||
    !Array.isArray(release.assets) ||
    (partial
      ? release.assets.length > expectedCount
      : release.assets.length !== expectedCount) ||
    new Set(release.assets.map((a) => String(a.id))).size !==
      release.assets.length ||
    release.assets.some(
      (a) =>
        !/^[1-9][0-9]*$/.test(String(a.id)) ||
        !inventory.assets.some((expected) => expected.name === a.name),
    )
  )
    throw Error("Exact immutable release inventory required");
  for (const expected of inventory.assets) {
    const found = release.assets.filter((a) => a.name === expected.name);
    if (partial && found.length === 0) continue;
    if (
      found.length !== 1 ||
      found[0].state !== "uploaded" ||
      found[0].size !== expected.size ||
      found[0].digest !== `sha256:${expected.sha256}`
    )
      throw Error("Release asset identity changed");
  }
  return String(release.id);
}
export async function publishAttested(
  c,
  directory,
  {
    api,
    stateRoot = path.resolve(".evidence/ubuntu-release"),
    execute = execFileSync,
    fetchResponse = fetch,
    verifyAcceptance = verifyInstalledAcceptance,
    progress = () => {},
  } = {},
) {
  if (
    process.getuid?.() === 0 ||
    validateConfig(c).length ||
    c.releaseAuthority !== "github-attestation-v1"
  )
    throw Error("Configured ordinary release owner required");
  directory = path.resolve(directory);
  const read = (name) =>
    readPublisherFile(path.join(directory, name), 128 * 1024 * 1024);
  const { packet, raw, arm64Packet, inventory } = loadCandidateInventory(
    c,
    directory,
  );
  for (const asset of inventory.assets) {
    const b = read(asset.name);
    if (b.length !== asset.size || sha(b) !== asset.sha256)
      throw Error("Candidate artifact changed");
  }
  if (sha(read("first-install-seed.json")) !== c.seedSha256)
    throw Error("Candidate trust changed");
  const installer = path.join(directory, "fased-install-linux-amd64");
  chmodSync(installer, 0o500);
  progress("verify actual candidate executable");
  const trustedSeedBytes = readFileSync(path.resolve(c.seedFile));
  if (sha(trustedSeedBytes) !== c.seedSha256)
    throw Error("Trusted public seed differs from config");
  const trustedSeed = JSON.parse(trustedSeedBytes);
  if (
    trustedSeed.version !== 4 ||
    trustedSeed.signingProfile !== "github-attestation-v1" ||
    packet.release.installerSha256 !== packet.installer.sha256
  )
    throw Error("Attested installer trust required");
  const proofTargets = [{ trustedSeed, pkt: packet }];
  if (inventory.multiArch) {
    const arm64SeedCfg = seedForArch(c, "arm64");
    const arm64SeedBytes = readFileSync(path.resolve(arm64SeedCfg.seedFile));
    if (sha(arm64SeedBytes) !== arm64SeedCfg.seedSha256)
      throw Error("Trusted arm64 public seed differs from config");
    if (sha(read("first-install-seed-arm64.json")) !== arm64SeedCfg.seedSha256)
      throw Error("Candidate arm64 trust changed");
    const arm64TrustedSeed = JSON.parse(arm64SeedBytes);
    if (
      arm64TrustedSeed.version !== 4 ||
      arm64TrustedSeed.signingProfile !== "github-attestation-v1" ||
      arm64TrustedSeed.target?.arch !== "arm64" ||
      arm64Packet.release.installerSha256 !== arm64Packet.installer.sha256
    )
      throw Error("Attested arm64 installer trust required");
    proofTargets.push({ trustedSeed: arm64TrustedSeed, pkt: arm64Packet });
  }
  for (const { trustedSeed: seedDoc, pkt } of proofTargets) {
    const scratch = mkdtempSync(path.join(directory, ".trusted-preflight-"));
    try {
      const policy = path.join(scratch, "policy.json"),
        expected = path.join(scratch, "expected.json");
      writeFileSync(policy, JSON.stringify(seedDoc.provenance), {
        mode: 0o600,
        flag: "wx",
      });
      writeFileSync(
        expected,
        JSON.stringify({
          ...(pkt.release.buildRef ? { buildRef: pkt.release.buildRef } : {}),
          installerSha256: pkt.installer.sha256,
          archiveName: pkt.archive.file,
          archiveSha256: pkt.archive.sha256,
          sourceCommit: pkt.source,
          workflowCommit: pkt.release.workflowCommit,
          tag: `v${pkt.productVersion}`,
          runId: pkt.release.runId,
          runAttempt: pkt.release.runAttempt,
        }),
        { mode: 0o600, flag: "wx" },
      );
      const decision = JSON.parse(
        execute(
          "go",
          [
            "run",
            "-buildvcs=false",
            "./cmd/fased-release-candidate",
            "verify-provenance",
            "--policy-file",
            policy,
            "--expected-file",
            expected,
            "--bundle-file",
            path.join(directory, pkt.release.provenance.file),
          ],
          {
            encoding: "utf8",
            env: verifierEnvironment(),
            timeout: 180000,
            maxBuffer: 2 * 1024 * 1024,
            stdio: ["ignore", "pipe", "pipe"],
          },
        ),
      );
      if (decision.BundleSHA256 !== pkt.release.provenance.sha256)
        throw Error("Independent installer/archive provenance failed");
    } finally {
      rmSync(scratch, { recursive: true });
    }
  }
  const preflight = JSON.parse(
    execute(installer, ["--verify-candidate", directory], {
      encoding: "utf8",
      env: verifierEnvironment(),
      timeout: 180000,
      maxBuffer: 2 * 1024 * 1024,
      stdio: ["ignore", "pipe", "pipe"],
    }),
  );
  if (
    preflight.version !== 4 ||
    !preflight.archiveVerified ||
    !preflight.bundleVerified ||
    !preflight.provenanceVerified ||
    preflight.installerInvoked ||
    preflight.publicActivation ||
    preflight.seedSha256 !== c.seedSha256 ||
    preflight.archiveSha256 !== packet.archive.sha256 ||
    (packet.version === 4 &&
      (preflight.linuxBundleVerified !== true ||
        preflight.bundleSha256 !== packet.bundleSha256))
  )
    throw Error("Actual candidate preflight failed");
  const identity = await api("GET", repo);
  if (
    String(identity.id) !== c.repositoryId ||
    String(identity.owner?.id) !== c.ownerId ||
    identity.full_name !== c.repository
  )
    throw Error("Fixed release repository required");
  let prior;
  try {
    prior = await api("GET", repo + `/releases/tags/v${c.version}`);
  } catch (error) {
    if (error.status !== 404) throw error;
  }
  const publicReadback = prior?.draft === false && prior?.immutable === true;
  const acceptance = publicReadback
    ? null
    : await verifyAcceptance(packet, inventory, api);
  // Owner settings are checked before tagging; GITHUB_TOKEN has no admin-read.
  // Canonical publication below and all consumers still require immutable:true.
  inventory.tagObject = await verifyPublisherTag(api, packet, inventory);
  const root = stateRoot;
  ensurePublisherDirectory(root, true);
  const work = path.join(root, `attested-${c.version}`);
  ensurePublisherDirectory(work, true);
  const journalDir = path.join(work, "publication");
  const binding = sha(JSON.stringify({ inventory, seed: c.seedSha256 }));
  const journal = openPublisherJournal(journalDir, binding, {
    resume: existsSync(path.join(journalDir, "operation.json")),
  });
  const candidate = Buffer.from(JSON.stringify(inventory) + "\n");
  try {
    if (acceptance) {
      journal.state.installedAcceptance = acceptance;
      journal.save();
    }
    let existing = prior;
    let id;
    if (existing?.draft === false) {
      id = checkRelease(existing, c, inventory);
      if (existing.immutable !== true)
        throw Error("Public release not immutable");
    } else {
      const stageDir = path.join(work, "stage");
      if (existing && !existsSync(path.join(stageDir, "operation.json"))) {
        id = checkRelease(existing, c, inventory, true);
        if (journal.state.adopted && journal.state.adopted.releaseId !== id)
          throw Error("Recorded draft identity changed");
        for (const recorded of journal.state.adopted?.assets ?? [])
          if (
            !existing.assets.some(
              (a) => a.name === recorded.name && String(a.id) === recorded.id,
            )
          )
            throw Error("Recorded draft asset identity changed");
        journal.state.adopted = {
          releaseId: id,
          assets: existing.assets.map((a) => ({
            id: String(a.id),
            name: a.name,
          })),
        };
        journal.save();
        for (const expected of inventory.assets) {
          if (existing.assets.some((a) => a.name === expected.name)) continue;
          if (journal.state.adoptedUploadAttempt?.name === expected.name)
            throw Error(
              "Draft upload outcome unresolved; preserve its original request",
            );
          const bytes = read(expected.name);
          if (bytes.length !== expected.size || sha(bytes) !== expected.sha256)
            throw Error("Candidate changed during draft recovery");
          journal.state.adoptedUploadAttempt = {
            releaseId: id,
            name: expected.name,
            sha256: expected.sha256,
          };
          journal.save();
          const uploaded = await api(
            "UPLOAD",
            repo + `/releases/${id}/assets?name=${expected.name}`,
            bytes,
          );
          if (
            !/^[1-9][0-9]*$/.test(String(uploaded.id)) ||
            uploaded.name !== expected.name ||
            uploaded.size !== expected.size ||
            uploaded.state !== "uploaded" ||
            uploaded.digest !== `sha256:${expected.sha256}`
          )
            throw Error("Recovered draft upload outcome unresolved");
          journal.state.adopted.assets.push({
            id: String(uploaded.id),
            name: uploaded.name,
          });
          delete journal.state.adoptedUploadAttempt;
          journal.save();
        }
      } else {
        const stage = await runRecordedPublisher(
          {
            action: "stage",
            mode: existsSync(path.join(stageDir, "operation.json"))
              ? "resume"
              : "start",
            directory: stageDir,
            candidate,
            candidateSHA256: sha(candidate),
          },
          { api, readArtifact: async (name) => read(name) },
        );
        id = stage.releaseId;
      }
      const current = await api("GET", repo + `/releases/${id}`);
      checkRelease(current, c, inventory);
      if (current.draft === true) {
        if (journal.state.publishAttempted)
          throw Error(
            "Publication outcome unresolved; preserve the original release ID and inspect",
          );
        journal.state.publishAttempted = {
          releaseId: id,
          tag: `v${c.version}`,
          source: packet.source,
        };
        journal.save();
        progress("publish verified immutable release");
        await api("PATCH", repo + `/releases/${id}`, {
          draft: false,
          tag_name: `v${c.version}`,
          target_commitish: packet.source,
        });
      }
      const published = await api("GET", repo + `/releases/${id}`);
      checkRelease(published, c, inventory);
      if (published.draft !== false || published.immutable !== true)
        throw Error("Publication not confirmed immutable");
    }
    progress("verify anonymous public installer and update authority");
    const scratch = mkdtempSync(path.join(work, "readback-"));
    let report;
    try {
      for (const name of ["install.sh", "fased-release.json"])
        await publicAsset(
          c.version,
          name,
          inventory.assets.find((a) => a.name === name),
          fetchResponse,
        );
      const binary = await publicAsset(
        c.version,
        "fased-install-linux-amd64",
        inventory.assets.find((a) => a.name === "fased-install-linux-amd64"),
        fetchResponse,
      );
      if (inventory.multiArch) {
        for (const armName of [
          "fased-release-linux-arm64.json",
          "fased-app-linux-arm64.tar.gz",
          "fased-install-linux-arm64",
          "fased-app-linux-arm64.provenance.sigstore.json",
        ])
          await publicAsset(
            c.version,
            armName,
            inventory.assets.find((a) => a.name === armName),
            fetchResponse,
          );
      }
      const exe = path.join(scratch, "fased-install");
      writeFileSync(exe, binary, { mode: 0o500, flag: "wx" });
      if (packet.version === 4) {
        const inspected = execute(exe, ["--inspect-public-package"], {
          encoding: "utf8",
          env: {
            ...verifierEnvironment(),
            XDG_CACHE_HOME: path.join(scratch, "cache"),
          },
          timeout: 180000,
          maxBuffer: 2 * 1024 * 1024,
          stdio: ["ignore", "pipe", "pipe"],
        }).trim();
        if (
          inspected !==
          `native ${sha(raw)} ${c.version} ${packet.installer.sha256}`
        )
          throw Error("Public installer/readback differs from release");
      } else {
        report = JSON.parse(
          execute(
            exe,
            [
              "--seed-file",
              path.join(directory, "first-install-seed.json"),
              "--seed-sha256",
              c.seedSha256,
              "--output",
              path.join(scratch, "acquired"),
              "--install=false",
              "--upgrade=false",
            ],
            {
              encoding: "utf8",
              env: verifierEnvironment(),
              timeout: 180000,
              maxBuffer: 2 * 1024 * 1024,
              stdio: ["ignore", "pipe", "pipe"],
            },
          ),
        );
        if (
          report.version !== 4 ||
          !report.metadataVerified ||
          !report.provenanceVerified ||
          !report.bundleVerified ||
          report.installerInvoked ||
          report.publicActivation ||
          report.archiveSha256 !== packet.archive.sha256 ||
          report.sequence !== releaseSequence(c.version)
        )
          throw Error("Public installer/readback differs from release");
      }
    } finally {
      rmSync(scratch, { recursive: true });
    }
    journal.state.result = {
      version: c.version,
      releaseId: id,
      sequence: releaseSequence(c.version),
      verified: true,
      automaticUpdates: false,
      liveActivation: false,
    };
    journal.state.complete = true;
    journal.save();
    return journal.state.result;
  } finally {
    journal.close();
  }
}
export async function main(args = process.argv.slice(2)) {
  const operation = args[0] ?? "ship";
  const evidenceArgs = new Map([
    ["accept-local", 3],
    ["verify-local", 2],
    ["accept-remote", 5],
  ]);
  const evidenceOperation = evidenceArgs.has(operation);
  if (
    (!evidenceOperation && !["ship", "publish"].includes(operation)) ||
    (evidenceOperation
      ? args.length !== evidenceArgs.get(operation)
      : args.length > 2)
  )
    throw Error(
      "Use npm run release -- [ship [PR] | publish [candidate-directory] | verify-local candidate-directory | accept-local candidate-directory local-receipt | accept-remote candidate-directory fresh-receipt upgrade-receipt installed-agent-receipt]",
    );
  const c = JSON.parse(readFileSync("release/ubuntu.json", "utf8"));
  let token = process.env.GH_TOKEN;
  if (!token) {
    execFileSync("gh", ["auth", "status"], {
      stdio: ["ignore", "pipe", "pipe"],
    });
    token = execFileSync("gh", ["auth", "token"], {
      encoding: "utf8",
      env: verifierEnvironment(),
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  }
  const directory =
    operation === "ship"
      ? await prepareReleaseCandidate(c.version, args[1])
      : path.resolve(args[1] ?? ".evidence/ubuntu-release");
  if (evidenceOperation) {
    const { packet, inventory } = loadCandidateInventory(c, directory);
    const api = githubPublisher(token);
    if (operation === "verify-local")
      return verifyLocalAcceptance(packet, inventory, api);
    if (operation === "accept-local")
      return recordLocalAcceptance(
        packet,
        inventory,
        api,
        JSON.parse(readPublisherFile(args[2])),
      );
    return recordRemoteAcceptance(
      packet,
      inventory,
      api,
      JSON.parse(readPublisherFile(args[2])),
      JSON.parse(readPublisherFile(args[3])),
      { installedAgent: JSON.parse(readPublisherFile(args[4])) },
    );
  }
  return publishAttested(c, directory, {
    api: githubPublisher(token),
    progress: (s) => process.stdout.write(`Release: ${s}\n`),
  });
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  main()
    .then((r) => console.log(JSON.stringify(r)))
    .catch((err) => {
      const raw = err instanceof Error ? err.message : String(err || "");
      const redacted = raw
        .replace(/Bearer\s+[A-Za-z0-9._-]+/gi, "Bearer [REDACTED]")
        .replace(/\bgh[pousr]_[A-Za-z0-9_]+\b/g, "[REDACTED]")
        .replace(/\bgithub_pat_[A-Za-z0-9_]+\b/g, "[REDACTED]")
        .trim();
      if (redacted) console.error(`Release error: ${redacted}`);
      console.error(
        "Release held. Preserve the original artifacts and operation records; no password is needed.",
      );
      process.exitCode = 1;
    });
}
