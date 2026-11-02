// One owner command joins the existing verifier, signer and durable publisher.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  readFileSync,
  writeFileSync,
  existsSync,
  chmodSync,
  rmSync,
  mkdtempSync,
} from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { prepareReleaseCandidate } from "./release-source.mjs";
import { desktopCredentials } from "./release-credentials.mjs";
import {
  unlockLocalSigning,
  persistLocalReleaseFile,
} from "./local-release-signing.mjs";
import {
  runMaintainerLocalSigning,
  verifiedCandidate,
  expectedProvenance,
  validateConfig,
} from "./prepare-ubuntu-release.mjs";
import {
  githubPublisher,
  runRecordedPublisher,
} from "./publish-ubuntu-release.mjs";
import {
  openPublisherJournal,
  readPublisherFile,
  ensurePublisherDirectory,
} from "./publisher-journal.mjs";

const sha = (b) => createHash("sha256").update(b).digest("hex");
const repo = "/repos/fased-ai/fased-app";
const roles = ["targets", "snapshot", "timestamp"];
const json = (p) => JSON.parse(readPublisherFile(p));
const bytes = (v) => Buffer.from(JSON.stringify(v) + "\n");

export async function publicAsset(
  version,
  name,
  identity,
  fetchResponse = fetch,
) {
  let address = `https://github.com/fased-ai/fased-app/releases/download/v${version}/${name}`;
  if (!/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version) || path.basename(name) !== name)
    throw Error("Exact public release asset required");
  let response;
  for (let hop = 0; hop < 2; hop++) {
    response = await fetchResponse(address, {
      redirect: "manual",
      signal: AbortSignal.timeout(60000),
    });
    if (response.status === 200) break;
    const location = response.headers.get("location");
    await response.body?.cancel();
    const u = location ? new URL(location) : null;
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
  let length = 0;
  for await (const chunk of response.body) {
    length += chunk.length;
    if (length > identity.size)
      throw Error("Public asset exceeds approved size");
    chunks.push(Buffer.from(chunk));
  }
  const raw = Buffer.concat(chunks);
  if (raw.length !== identity.size || sha(raw) !== identity.sha256)
    throw Error("Public asset differs from approved bytes");
  return raw;
}

// Completed suboperations always reconcile their remote identities on resume.
// Only the existing publisher can recover an uncertain HTTP mutation.
export async function releaseSequence(
  journal,
  operations,
  progress = () => {},
) {
  for (const name of [
    "preflight",
    "stage",
    "finalize",
    "publish",
    "metadata",
    "readback",
  ]) {
    progress(name);
    const result = await operations[name](journal.state);
    journal.state[name] = result;
    journal.save();
  }
  journal.state.complete = true;
  journal.save();
  return {
    ...journal.state.readback,
    automaticUpdates: false,
    liveActivation: false,
  };
}

export function finalizationPlan(packet, stage, previous, cdnPath, now) {
  const old = previous.descriptor;
  const previousVersions = Object.fromEntries(
    roles.map((role) => {
      const values = Object.entries(previous.files).filter(([name]) =>
        role === "timestamp"
          ? name === "timestamp.json"
          : new RegExp(`^[0-9]+\\.${role}\\.json$`).test(name),
      );
      if (values.length !== 1)
        throw Error("Previous signed metadata roles required");
      return [
        role,
        JSON.parse(Buffer.from(values[0][1], "base64")).signed.version,
      ];
    }),
  );
  if (
    !/^[0-9]+$/.test(old.sequence) ||
    BigInt(old.sequence) < 1n ||
    Object.values(previousVersions).some(
      (v) => !Number.isSafeInteger(v) || v < 1,
    )
  )
    throw Error("Previous release floors invalid");
  const asset = stage.assets.find(
    (a) => a.name === packet.objects.archive.file,
  );
  if (
    !asset ||
    !/^\/github-production-release-asset\/1426211857\/[A-Za-z0-9-]+$/.test(
      cdnPath,
    )
  )
    throw Error("Pinned GitHub archive locator required");
  const expires = (hours) =>
    new Date(now.getTime() + hours * 3600000).toISOString();
  return {
    version: 1,
    descriptor: {
      version: 3,
      purpose: "fased-app-first-install-production:v1",
      developmentOnly: false,
      repository: "fased-ai/fased-app",
      channel: "stable",
      target: { os: "linux", arch: "amd64" },
      sequence: String(BigInt(old.sequence) + 1n),
      productVersion: packet.version,
      source: packet.sourceCommit,
      bundleSha256: packet.objects.manifestSha256,
      archive: packet.objects.archive,
      github: {
        repositoryId: "1426211857",
        releaseId: stage.releaseId,
        assetId: asset.id,
        name: asset.name,
        size: asset.size,
        sha256: asset.sha256,
        cdnPath,
      },
      release: {
        sourceTree: packet.sourceTree,
        tagObject: packet.tagObject,
        packageLockSha256: packet.packageLockSha256,
        goSumSha256: packet.goSumSha256,
        workflowCommit: packet.workflowCommit,
        runId: packet.runId,
        runAttempt: packet.runAttempt,
        provenance: packet.provenance,
      },
    },
    previousSequence: old.sequence,
    previousVersions,
    versions: Object.fromEntries(
      roles.map((r) => [r, previousVersions[r] + 1]),
    ),
    expires: {
      targets: expires(29 * 24),
      snapshot: expires(6 * 24),
      timestamp: expires(47),
    },
  };
}

export async function releaseUbuntu(
  c,
  owner,
  packetDirectory,
  {
    api,
    token,
    execute = execFileSync,
    progress = (stage) => process.stdout.write(`Release: ${stage}\n`),
    credentials,
    now = () => new Date(),
    fetchResponse = fetch,
  } = {},
) {
  c = JSON.parse(JSON.stringify(c));
  owner = JSON.parse(JSON.stringify(owner));
  if (process.getuid?.() === 0 || validateConfig(c).length)
    throw Error("Configured ordinary release owner required");
  const directory = path.resolve(packetDirectory);
  const packet = json(path.join(directory, "signing-inputs.json"));
  const readArtifact = (name, max = 128 * 1024 * 1024) => {
    if (path.basename(name) !== name)
      throw Error("Candidate file name invalid");
    return readPublisherFile(path.join(directory, name), max);
  };
  const seed = readArtifact("first-install-seed.json", 65536);
  if (
    sha(seed) !== c.seedSha256 ||
    packet.version !== c.version ||
    sha(readPublisherFile(owner.ownerApprovalFile, 65536)) !==
      c.custodyApprovalSha256 ||
    sha(readPublisherFile(owner.verifierFile, 128 * 1024 * 1024)) !==
      owner.verifierSHA256
  )
    throw Error("Owner, verifier or public trust differs from approved inputs");
  const signingConfig = {
    storeFile: owner.storeFile,
    identitySHA256: owner.identitySHA256,
    keyClass: "production",
    seed,
    seedSHA256: c.seedSha256,
    allowedRoles: roles,
  };
  credentials ??= desktopCredentials(signingConfig);
  api ??= githubPublisher(token);
  const identities = [
    packet.objects.archive,
    packet.objects.installer,
    packet.provenance,
    packet.entry,
  ];
  for (const a of identities) {
    const b = readArtifact(a.file);
    if (b.length !== a.size || sha(b) !== a.sha256)
      throw Error("Candidate artifact changed");
  }
  verifiedCandidate(
    c,
    packet,
    packet.objects,
    packet.provenanceDecision,
    packet.provenance,
    json(path.join(directory, "provenance-policy.json")),
  );
  if (
    JSON.stringify(expectedProvenance(packet, packet.objects.archive)) !==
    JSON.stringify(json(path.join(directory, "expected-provenance.json")))
  )
    throw Error("Candidate provenance expectations changed");
  const inventory = {
    repository: c.repository,
    repositoryId: c.repositoryId,
    ownerId: c.ownerId,
    version: c.version,
    source: packet.sourceCommit,
    tagObject: packet.tagObject,
    assets: identities.map((a) => ({
      name: a.file,
      size: a.size,
      sha256: a.sha256,
    })),
  };
  const candidate = bytes(inventory),
    candidateSHA256 = sha(candidate);
  const root = path.resolve(".evidence/ubuntu-release");
  ensurePublisherDirectory(root, true);
  const work = path.join(root, `release-${c.version}`);
  ensurePublisherDirectory(work, true);
  const historyDir = path.join(root, "owner-release");
  const history = openPublisherJournal(
    historyDir,
    sha(bytes({ identity: owner.identitySHA256, seed: c.seedSha256 })),
    { resume: existsSync(path.join(historyDir, "operation.json")) },
  );
  history.state.packages ??= {};
  if (!history.state.packages[c.version]) {
    history.state.packages[c.version] = history.state.last
      ? {
          ...owner,
          previousPublicationFile: history.state.last.publicationFile,
          previousMetadataCommit: history.state.last.metadataCommit,
        }
      : owner;
    history.save();
  }
  owner = history.state.packages[c.version];
  const binding = sha(bytes({ candidateSHA256, owner, config: c }));
  let outer;
  try {
    outer = openPublisherJournal(path.join(work, "sequence"), binding, {
      resume: existsSync(path.join(work, "sequence/operation.json")),
    });
  } catch (err) {
    history.close();
    throw err;
  }
  const mode = (dir) =>
    existsSync(path.join(dir, "operation.json")) ? "resume" : "start";
  const publish = (action, publication, approval) => {
    const dir = path.join(work, action);
    return runRecordedPublisher(
      {
        action,
        mode: mode(dir),
        directory: dir,
        candidate,
        candidateSHA256,
        publication,
        approval,
      },
      { api, readArtifact },
    );
  };
  const invoke = (exe, args) => {
    try {
      return JSON.parse(
        execute(exe, args, {
          encoding: "utf8",
          timeout: 180000,
          maxBuffer: 2 * 1024 * 1024,
          stdio: ["ignore", "pipe", "pipe"],
        }),
      );
    } catch {
      throw Error(
        "Release executable verification failed; no publication permitted",
      );
    }
  };
  const inspect = async (name, published = false) => {
    const scratch = mkdtempSync(path.join(work, `${name}-`));
    const output = path.join(scratch, "acquired");
    try {
      let installer = path.join(directory, packet.objects.installer.file);
      if (published) {
        await publicAsset(
          c.version,
          packet.entry.file,
          packet.entry,
          fetchResponse,
        );
        installer = path.join(scratch, packet.objects.installer.file);
        writeFileSync(
          installer,
          await publicAsset(
            c.version,
            packet.objects.installer.file,
            packet.objects.installer,
            fetchResponse,
          ),
          { flag: "wx", mode: 0o500 },
        );
      }
      chmodSync(installer, 0o500);
      const result = invoke(installer, [
        "--seed-file",
        path.join(directory, "first-install-seed.json"),
        "--seed-sha256",
        c.seedSha256,
        "--output",
        output,
        "--install=false",
        "--upgrade=false",
      ]);
      if (
        !result.metadataVerified ||
        !result.provenanceVerified ||
        !result.archiveVerified ||
        !result.bundleVerified ||
        result.installerInvoked ||
        result.publicActivation
      )
        throw Error("Actual installer acquisition proof required");
      return result;
    } finally {
      rmSync(scratch, { recursive: true });
    }
  };
  try {
    const result = await releaseSequence(
      outer,
      {
        preflight: async () => {
          if (!outer.state.finalize) {
            const pass = await credentials.getPassphrase();
            try {
              unlockLocalSigning({
                ...signingConfig,
                passphrase: pass,
              }).close();
            } finally {
              pass.fill(0);
            }
          }
          if ((await api("GET", repo + "/immutable-releases")).enabled !== true)
            throw Error("Enable immutable GitHub releases before release");
          const proof = invoke(owner.verifierFile, [
            "verify-provenance",
            "--policy-file",
            path.join(directory, "provenance-policy.json"),
            "--expected-file",
            path.join(directory, "expected-provenance.json"),
            "--bundle-file",
            path.join(directory, packet.provenance.file),
          ]);
          if (proof.BundleSHA256 !== packet.provenance.sha256)
            throw Error("Original provenance differs");
          return inspect("preflight-acquisition");
        },
        stage: () => publish("stage"),
        finalize: async (state) => {
          if (!state.finalizationRequest) {
            const previous = json(owner.previousPublicationFile);
            if (
              previous.seedSha256 !== c.seedSha256 ||
              previous.descriptor.sequence !== state.preflight.sequence
            )
              throw Error(
                "Previous publication differs from actual verified public channel",
              );
            const asset = state.stage.assets.find(
              (a) => a.name === packet.objects.archive.file,
            );
            const response = await fetchResponse(
              `https://api.github.com${repo}/releases/assets/${asset.id}`,
              {
                headers: {
                  Accept: "application/octet-stream",
                  Authorization: `Bearer ${token}`,
                  "X-GitHub-Api-Version": "2022-11-28",
                },
                redirect: "manual",
                signal: AbortSignal.timeout(30000),
              },
            );
            const location = response.headers.get("location");
            await response.body?.cancel();
            const u = location ? new URL(location) : null;
            if (
              response.status !== 302 ||
              !u ||
              u.protocol !== "https:" ||
              u.host !== "release-assets.githubusercontent.com" ||
              u.username ||
              u.password
            )
              throw Error("GitHub archive CDN identity unavailable");
            // Never persist the temporary query/token.
            const plan = finalizationPlan(
              packet,
              state.stage,
              previous,
              u.pathname,
              now(),
            );
            const input = {
              version: 1,
              seedFile: path.join(directory, "first-install-seed.json"),
              seedSha256: c.seedSha256,
              archiveFile: path.join(directory, packet.objects.archive.file),
              provenanceFile: path.join(directory, packet.provenance.file),
              plan,
            };
            const inputFile = path.join(work, "finalization-candidate.json");
            const raw = bytes(input);
            state.finalizationInput = input;
            state.finalizationRequest = {
              version: 1,
              keyClass: "production",
              ownerApprovalFile: owner.ownerApprovalFile,
              directory: path.join(work, "signing"),
              storeFile: owner.storeFile,
              identitySHA256: owner.identitySHA256,
              candidateFile: inputFile,
              candidateSHA256: sha(raw),
              seedFile: input.seedFile,
              seedSHA256: c.seedSha256,
              verifierFile: owner.verifierFile,
              verifierSHA256: owner.verifierSHA256,
              workDirectory: path.join(work, "finalizer-work"),
              outputDirectory: path.join(work, "finalized"),
            };
            outer.save();
          }
          persistLocalReleaseFile(
            outer,
            state.finalizationRequest.candidateFile,
            bytes(state.finalizationInput),
          );
          return runMaintainerLocalSigning(
            c,
            "signer-sign",
            {
              ...state.finalizationRequest,
              mode: mode(state.finalizationRequest.directory),
            },
            { getPassphrase: () => credentials.getPassphrase() },
          );
        },
        publish: (state) => {
          const publication = readPublisherFile(
            path.join(work, "finalized/publication.json"),
          );
          return publish("release", publication, {
            sha256: sha(publication),
            previousCommit: owner.previousMetadataCommit,
          });
        },
        metadata: () => {
          const publication = readPublisherFile(
            path.join(work, "finalized/publication.json"),
          );
          return publish("metadata", publication, {
            sha256: sha(publication),
            previousCommit: owner.previousMetadataCommit,
          });
        },
        readback: async (state) => {
          const result = await inspect("public-acquisition", true);
          const p = json(path.join(work, "finalized/publication.json"));
          if (
            result.sequence !== p.descriptor.sequence ||
            result.archiveSha256 !== packet.objects.archive.sha256
          )
            throw Error("Public release readback differs from signed release");
          return {
            version: c.version,
            releaseId: state.stage.releaseId,
            metadataCommit: state.metadata.metadataCommit,
            publicationFile: path.join(work, "finalized/publication.json"),
            verified: true,
          };
        },
      },
      progress,
    );
    history.state.last = result;
    history.save();
    return result;
  } finally {
    outer.close();
    history.close();
  }
}

export async function main(args = process.argv.slice(2)) {
  const operation = args[0] ?? "ship";
  if (
    !["ship", "publish", "credentials", "forget"].includes(operation) ||
    args.length > 2
  )
    throw Error(
      "Use: npm run release -- [ship [PR] | publish [candidate-directory] | credentials | forget]",
    );
  const c = JSON.parse(readFileSync("release/ubuntu.json", "utf8"));
  const owner = json(path.resolve(".evidence/ubuntu-release/owner.json"));
  const directory = path.resolve(
    args[1] ?? `.evidence/ubuntu-${c.version}-candidate/artifacts`,
  );
  if (
    validateConfig(c).length ||
    sha(readPublisherFile(owner.ownerApprovalFile, 65536)) !==
      c.custodyApprovalSha256 ||
    sha(readPublisherFile(owner.verifierFile, 128 * 1024 * 1024)) !==
      owner.verifierSHA256
  )
    throw Error("Approved signing owner and verifier required");
  const seed = readPublisherFile(path.resolve(c.seedFile), 65536);
  const signingConfig = {
    storeFile: owner.storeFile,
    identitySHA256: owner.identitySHA256,
    keyClass: "production",
    seed,
    seedSHA256: c.seedSha256,
    allowedRoles: roles,
  };
  const credentials = desktopCredentials(signingConfig);
  if (["credentials", "forget"].includes(operation))
    return operation === "credentials"
      ? credentials.remember()
      : credentials.forget();
  // Refuse before merge/tag/build when the one-time desktop setup is missing.
  if (operation === "ship") {
    const pass = credentials.getPassphrase();
    try {
      unlockLocalSigning({ ...signingConfig, passphrase: pass }).close();
    } finally {
      pass.fill(0);
    }
  }
  execFileSync("gh", ["auth", "status"], { stdio: ["ignore", "pipe", "pipe"] });
  const token = execFileSync("gh", ["auth", "token"], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
  const packetDirectory =
    operation === "ship"
      ? await prepareReleaseCandidate(c.version, args[1])
      : directory;
  return releaseUbuntu(c, owner, packetDirectory, { token });
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  main()
    .then((r) => console.log(JSON.stringify(r)))
    .catch(() => {
      console.error(
        "Release held. Preserve .evidence/ubuntu-release; resolve the recorded phase and rerun the same command.",
      );
      process.exitCode = 1;
    });
}
