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
import { validateConfig } from "./prepare-ubuntu-release.mjs";
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
export function attestedInventory(c, packet, raw) {
  if (
    c.releaseAuthority !== "github-attestation-v1" ||
    packet.version !== 1 ||
    packet.productVersion !== c.version ||
    !/^[a-f0-9]{40}$/.test(packet.source)
  )
    throw Error("Attested release authority/version required");
  const identities = [
    packet.archive,
    packet.installer,
    packet.release.provenance,
    packet.entry,
    { file: "fased-release.json", size: raw.length, sha256: sha(raw) },
  ];
  if (
    identities.some(
      (a, i) =>
        a.file !== names[i] ||
        !Number.isSafeInteger(a.size) ||
        a.size <= 0 ||
        a.size > 128 * 1024 * 1024 ||
        !/^[a-f0-9]{64}$/.test(a.sha256),
    )
  )
    throw Error("Complete exact attested asset inventory required");
  return {
    releaseAuthority: c.releaseAuthority,
    repository: c.repository,
    repositoryId: c.repositoryId,
    ownerId: c.ownerId,
    version: c.version,
    source: packet.source,
    tagObject: packet.release.tagObject,
    assets: identities.map((a) => ({
      name: a.file,
      size: a.size,
      sha256: a.sha256,
    })),
  };
}
function checkRelease(release, c, inventory, partial = false) {
  if (
    String(release.id).match(/^[1-9][0-9]*$/) === null ||
    release.tag_name !== `v${c.version}` ||
    release.prerelease !== false ||
    !Array.isArray(release.assets) ||
    (partial ? release.assets.length > 5 : release.assets.length !== 5) ||
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
  const raw = read("fased-release.json"),
    packet = JSON.parse(raw),
    inventory = attestedInventory(c, packet, raw);
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
  const scratch = mkdtempSync(path.join(directory, ".trusted-preflight-"));
  try {
    const policy = path.join(scratch, "policy.json"),
      expected = path.join(scratch, "expected.json");
    writeFileSync(policy, JSON.stringify(trustedSeed.provenance), {
      mode: 0o600,
      flag: "wx",
    });
    writeFileSync(
      expected,
      JSON.stringify({
        installerSha256: packet.installer.sha256,
        archiveName: packet.archive.file,
        archiveSha256: packet.archive.sha256,
        sourceCommit: packet.source,
        workflowCommit: packet.release.workflowCommit,
        tag: `v${packet.productVersion}`,
        runId: packet.release.runId,
        runAttempt: packet.release.runAttempt,
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
          path.join(directory, packet.release.provenance.file),
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
    if (decision.BundleSHA256 !== packet.release.provenance.sha256)
      throw Error("Independent installer/archive provenance failed");
  } finally {
    rmSync(scratch, { recursive: true });
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
    preflight.archiveSha256 !== packet.archive.sha256
  )
    throw Error("Actual candidate preflight failed");
  const identity = await api("GET", repo);
  if (
    String(identity.id) !== c.repositoryId ||
    String(identity.owner?.id) !== c.ownerId ||
    identity.full_name !== c.repository
  )
    throw Error("Fixed release repository required");
  // Owner settings are checked before tagging; GITHUB_TOKEN has no admin-read.
  // Canonical publication below and all consumers still require immutable:true.
  await verifyAnnotatedTag(
    api,
    c.version,
    inventory.tagObject,
    inventory.source,
  );
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
    let existing;
    try {
      existing = await api("GET", repo + `/releases/tags/v${c.version}`);
    } catch (e) {
      if (e.status !== 404) throw e;
    }
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
      const exe = path.join(scratch, "fased-install");
      writeFileSync(exe, binary, { mode: 0o500, flag: "wx" });
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
  if (!["ship", "publish"].includes(operation) || args.length > 2)
    throw Error(
      "Use npm run release -- [ship [PR] | publish [candidate-directory]]",
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
    .catch(() => {
      console.error(
        "Release held. Preserve the original artifacts and operation records; no password is needed.",
      );
      process.exitCode = 1;
    });
}
