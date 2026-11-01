// Explicit maintainer tooling. No key generation, implicit publish or activation.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  mkdirSync,
  readFileSync,
  writeFileSync,
  lstatSync,
  readdirSync,
  existsSync,
} from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import {
  runRecordedPublisher,
  publicationInput,
  githubPublisher,
} from "./publish-ubuntu-release.mjs";
import {
  inspectPublisherJournal,
  readPublisherFile,
} from "./publisher-journal.mjs";

export const evidence = ".evidence/ubuntu-release";
const sha = (b) => createHash("sha256").update(b).digest("hex");
const hash = /^[a-f0-9]{64}$/;
const commit = /^[a-f0-9]{40}$/;
const number = /^[1-9][0-9]*$/;
const fields = [
  "schemaVersion",
  "version",
  "repository",
  "repositoryId",
  "ownerId",
  "workflow",
  "target",
  "seedFile",
  "seedSha256",
  "custodyApprovalSha256",
  "initialTrustApprovalSha256",
];
export function validateConfig(c) {
  if (
    !c ||
    Object.keys(c).some((k) => !fields.includes(k)) ||
    Object.keys(c).length !== fields.length ||
    c.schemaVersion !== 1 ||
    !/^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(c.version) ||
    c.repository !== "fased-ai/fased-app" ||
    c.repositoryId !== "1426211857" ||
    c.ownerId !== "279582504" ||
    c.workflow !== ".github/workflows/release.yml" ||
    !c.target ||
    Object.keys(c.target).sort().join(",") !== "arch,os,ubuntu" ||
    c.target.os !== "linux" ||
    c.target.arch !== "amd64" ||
    c.target.ubuntu !== "24.04"
  )
    throw Error("Reviewed Ubuntu release identity required");
  for (const key of [
    "seedSha256",
    "custodyApprovalSha256",
    "initialTrustApprovalSha256",
  ])
    if (c[key] !== null && !hash.test(c[key]))
      throw Error("Invalid public approval digest");
  if (
    c.seedFile !== null &&
    !/^release\/public\/[A-Za-z0-9_-]+\.json$/.test(c.seedFile)
  )
    throw Error("Approved repository public seed path required");
  return [
    "seedFile",
    "seedSha256",
    "custodyApprovalSha256",
    "initialTrustApprovalSha256",
  ].filter((k) => c[k] === null);
}
export function releaseContext(c, env, source) {
  validateConfig(c);
  const tag = `v${c.version}`;
  if (
    env.GITHUB_EVENT_NAME !== "push" ||
    env.GITHUB_REF !== `refs/tags/${tag}` ||
    env.GITHUB_REPOSITORY !== c.repository ||
    env.GITHUB_REPOSITORY_ID !== c.repositoryId ||
    env.GITHUB_REPOSITORY_OWNER_ID !== c.ownerId ||
    env.GITHUB_WORKFLOW_REF !==
      `${c.repository}/${c.workflow}@refs/tags/${tag}` ||
    env.RUNNER_OS !== "Linux" ||
    env.RUNNER_ARCH !== "X64" ||
    !commit.test(env.GITHUB_SHA ?? "") ||
    !number.test(env.GITHUB_RUN_ID ?? "") ||
    !number.test(env.GITHUB_RUN_ATTEMPT ?? "") ||
    source.commit !== env.GITHUB_SHA ||
    source.workflowCommit !== source.commit ||
    !commit.test(source.tree) ||
    !commit.test(source.tagObject) ||
    source.onMain !== true ||
    source.clean !== true
  )
    throw Error(
      "Exact clean, main-merged, annotated tag-push context required",
    );
  return {
    version: c.version,
    tag,
    sourceCommit: source.commit,
    sourceTree: source.tree,
    tagObject: source.tagObject,
    workflowCommit: source.workflowCommit,
    runId: env.GITHUB_RUN_ID,
    runAttempt: env.GITHUB_RUN_ATTEMPT,
  };
}
function git(...args) {
  return execFileSync("git", args, { encoding: "utf8" }).trim();
}
function write(name, value) {
  writeFileSync(
    path.join(evidence, name),
    JSON.stringify(value, null, 2) + "\n",
    { mode: 0o600, flag: "wx" },
  );
}
function read(name) {
  return JSON.parse(readFileSync(path.join(evidence, name), "utf8"));
}
function file(name, max) {
  const p = path.join(evidence, name),
    st = lstatSync(p);
  if (!st.isFile() || st.isSymbolicLink() || st.size <= 0 || st.size > max)
    throw Error("Exact bounded candidate file required");
  const bytes = readFileSync(p);
  return {
    bytes,
    identity: { file: name, size: bytes.length, sha256: sha(bytes) },
  };
}
export function bindingOverlay(seed, pin, root) {
  if (!hash.test(pin) || sha(seed) !== pin) throw Error("Seed binding changed");
  const declarations = [
    [
      "cmd/fased-app/publisher_seed_generated.go",
      "applicationChannelBinding",
      "compiledApplicationChannel",
      "core.go",
    ],
    [
      "cmd/fased-install/publisher_seed_generated.go",
      "firstInstallBinding",
      "compiledFirstInstallBinding",
      "installer.go",
    ],
  ];
  return declarations.map(([target, type, name, output]) => ({
    target: path.resolve(root, target),
    output,
    source: `package main\nimport "encoding/base64"\nfunc init() {\n b, e := base64.StdEncoding.DecodeString(${JSON.stringify(seed.toString("base64"))})\n if e != nil { panic("invalid compiled public release seed") }\n ${name} = &${type}{Seed:b, SHA256:${JSON.stringify(pin)}}\n}\n`,
  }));
}
function prepare(c) {
  const missing = validateConfig(c);
  if (missing.length)
    throw Error("Production inputs missing: " + missing.join(", "));
  if (!process.env.GITHUB_OUTPUT)
    throw Error("Workflow output channel required");
  const source = {
    commit: git("rev-parse", "HEAD"),
    tree: git("rev-parse", "HEAD^{tree}"),
    workflowCommit: git("rev-parse", "HEAD"),
    tagObject: git("rev-parse", `${process.env.GITHUB_REF}^{tag}`),
    clean: git("status", "--porcelain") === "",
    onMain: false,
  };
  execFileSync("git", [
    "merge-base",
    "--is-ancestor",
    source.commit,
    "refs/remotes/origin/main",
  ]);
  source.onMain = true;
  const context = releaseContext(c, process.env, source);
  const osRelease = readFileSync("/etc/os-release", "utf8");
  if (
    !/^ID=ubuntu$/m.test(osRelease) ||
    !/^VERSION_ID="24\.04"$/m.test(osRelease)
  )
    throw Error("Ubuntu24.04 build host required");
  const seedStat = lstatSync(c.seedFile);
  if (!seedStat.isFile() || seedStat.isSymbolicLink())
    throw Error("Public seed must be a regular tracked file");
  const seed = readFileSync(c.seedFile);
  if (
    seed.length > 65536 ||
    sha(seed) !== c.seedSha256 ||
    !execFileSync("git", ["show", `${source.commit}:${c.seedFile}`]).equals(
      seed,
    )
  )
    throw Error("Approved tracked public seed changed");
  const policy = JSON.parse(
    execFileSync(
      "go",
      [
        "run",
        "./cmd/fased-release-candidate",
        "verify-seed",
        "--seed-file",
        c.seedFile,
        "--seed-sha256",
        c.seedSha256,
      ],
      { encoding: "utf8" },
    ),
  );
  if (
    policy.repositoryId !== c.repositoryId ||
    policy.ownerId !== c.ownerId ||
    policy.workflow !== c.workflow
  )
    throw Error("Production public trust repository changed");
  mkdirSync(".evidence", { recursive: true, mode: 0o700 });
  mkdirSync(evidence, { mode: 0o700 });
  mkdirSync(path.join(evidence, "bindings"), { mode: 0o700 });
  const replacements = {};
  for (const b of bindingOverlay(seed, c.seedSha256, process.cwd())) {
    if (existsSync(b.target))
      throw Error("Preserve existing generated binding path");
    const output = path.resolve(evidence, "bindings", b.output);
    writeFileSync(output, b.source, { mode: 0o600, flag: "wx" });
    replacements[b.target] = output;
  }
  write("overlay.json", { Replace: replacements });
  write("provenance-policy.json", policy);
  writeFileSync(path.join(evidence, "first-install-seed.json"), seed, {
    mode: 0o600,
    flag: "wx",
  });
  write("build-inputs.json", {
    ...context,
    configSha256: sha(readFileSync("release/ubuntu.json")),
    seedSha256: c.seedSha256,
    custodyApprovalSha256: c.custodyApprovalSha256,
    initialTrustApprovalSha256: c.initialTrustApprovalSha256,
    packageLockSha256: sha(readFileSync("package-lock.json")),
    goSumSha256: sha(readFileSync("go.sum")),
    publicRelease: false,
    activation: false,
  });
  writeFileSync(
    process.env.GITHUB_OUTPUT,
    `version=${c.version}\noverlay=${path.resolve(evidence, "overlay.json")}\n`,
    { flag: "a" },
  );
}
export function expectedProvenance(input, archive) {
  return {
    archiveName: archive.file,
    archiveSha256: archive.sha256,
    sourceCommit: input.sourceCommit,
    workflowCommit: input.workflowCommit,
    tag: input.tag,
    runId: input.runId,
    runAttempt: input.runAttempt,
  };
}
export function candidateInventory(input, archive, installer, report) {
  for (const [identity, name] of [
    [archive, "fased-app-linux-amd64.tar.gz"],
    [installer, "fased-install-linux-amd64"],
  ])
    if (
      identity.file !== name ||
      !hash.test(identity.sha256 ?? "") ||
      !Number.isSafeInteger(identity.size) ||
      identity.size <= 0 ||
      identity.size > 128 * 1024 * 1024
    )
      throw Error("Exact candidate artifact identity required");
  if (
    report.publicRelease !== false ||
    report.manifest?.schema !== 2 ||
    report.manifest.unsignedEngineering !== false ||
    report.manifest.desktop !== false ||
    report.manifest.version !== input.version ||
    report.manifest.source !== input.sourceCommit ||
    report.manifest.platform !== "linux" ||
    report.manifest.arch !== "amd64" ||
    report.identity?.sha256 !== archive.sha256 ||
    report.identity?.size !== archive.size ||
    !hash.test(report.manifestIdentity?.sha256 ?? "")
  )
    throw Error("Producer/archive version or identity changed");
  return { archive, installer, manifestSha256: report.manifestIdentity.sha256 };
}
function inventory() {
  const input = read("build-inputs.json");
  const archive = file(
    "fased-app-linux-amd64.tar.gz",
    128 * 1024 * 1024,
  ).identity;
  const installer = file(
    "fased-install-linux-amd64",
    128 * 1024 * 1024,
  ).identity;
  const report = read("fased-app-linux-amd64.json");
  write(
    "artifact-inventory.json",
    candidateInventory(input, archive, installer, report),
  );
  write("expected-provenance.json", expectedProvenance(input, archive));
}
export function verifiedCandidate(c, input, objects, decision, proof, policy) {
  if (
    validateConfig(c).length ||
    input.version !== c.version ||
    input.tag !== `v${c.version}` ||
    input.seedSha256 !== c.seedSha256 ||
    input.custodyApprovalSha256 !== c.custodyApprovalSha256 ||
    input.initialTrustApprovalSha256 !== c.initialTrustApprovalSha256 ||
    !hash.test(objects.manifestSha256 ?? "")
  )
    throw Error("Candidate/public approval identity changed");
  // Match Go's Policy struct field order, the only accepted public policy shape.
  const canonical = {
    version: policy.version,
    repository: policy.repository,
    repositoryId: policy.repositoryId,
    ownerId: policy.ownerId,
    workflow: policy.workflow,
    trustedRoot: policy.trustedRoot,
  };
  if (
    policy.repository !== c.repository ||
    policy.repositoryId !== c.repositoryId ||
    policy.ownerId !== c.ownerId ||
    policy.workflow !== c.workflow ||
    decision.BundleSHA256 !== proof.sha256 ||
    decision.PolicySHA256 !== sha(JSON.stringify(canonical)) ||
    decision.Identity !==
      `https://github.com/${c.repository}/${c.workflow}@refs/tags/${input.tag}` ||
    !["Tlog", "TimestampAuthority"].includes(decision.TimestampSource) ||
    !Number.isFinite(Date.parse(decision.VerifiedAt))
  )
    throw Error("Actual offline-verified provenance required");
}
function finish(c) {
  const input = read("build-inputs.json"),
    objects = read("artifact-inventory.json"),
    decision = read("verified-provenance.json");
  if (
    JSON.stringify(read("expected-provenance.json")) !==
    JSON.stringify(expectedProvenance(input, objects.archive))
  )
    throw Error("Verified artifact/source expectations changed");
  for (const identity of [objects.archive, objects.installer])
    if (
      !["fased-app-linux-amd64.tar.gz", "fased-install-linux-amd64"].includes(
        identity.file,
      ) ||
      JSON.stringify(file(identity.file, 128 * 1024 * 1024).identity) !==
        JSON.stringify(identity)
    )
      throw Error("Candidate artifact changed after verification");
  const proof = file(
    "fased-app-linux-amd64.provenance.sigstore.json",
    64 * 1024,
  ).identity;
  verifiedCandidate(
    c,
    input,
    objects,
    decision,
    proof,
    read("provenance-policy.json"),
  );
  if (file("first-install-seed.json", 65536).identity.sha256 !== c.seedSha256)
    throw Error("Candidate public seed changed");
  const config = {
    version: input.version,
    installer: path.join(evidence, objects.installer.file),
    seed: path.join(evidence, "first-install-seed.json"),
  };
  write("entry-config.json", config);
  execFileSync("node", [
    "scripts/render-install-entry.mjs",
    path.join(evidence, "entry-config.json"),
    path.join(evidence, "install.sh"),
  ]);
  write("signing-inputs.json", {
    ...input,
    objects,
    provenance: proof,
    provenanceDecision: decision,
    entry: file("install.sh", 2 * 1024 * 1024).identity,
    productionSignaturesCreated: false,
    signingRequestReady: false,
    finalizer: {
      inputVersion: 1,
      command: "fased-release-candidate",
      request: "release-request",
      complete: "release-finalize",
      privateKeysAccepted: false,
      metadataOnly: true,
    },
    missing: [
      "owner-authorized immutable GitHub release/asset locators and reviewed sequence/version/expiry plan",
      "exact descriptor/targets/snapshot/timestamp signing payloads and approved profile signatures",
      "final production bootstrap verification before publication",
    ],
    publicRelease: false,
    activation: false,
  });
}
// Renewed metadata gets a new retained operation automatically. Identical
// approved bytes always reopen the same journal; no journal is reset/deleted.
export function publisherOperationDirectory(c, request) {
  validateConfig(c);
  if (!["stage", "release", "metadata"].includes(request.action))
    throw Error("Publisher action required");
  const suffix =
    request.action === "metadata" ? request.approval?.sha256 : null;
  if (request.action === "metadata" && !hash.test(suffix))
    throw Error("Approved metadata digest required");
  return path.resolve(
    evidence,
    `publisher-${c.version}-${request.action}${suffix ? "-" + suffix : ""}`,
  );
}
export function inspectMaintainerPublisher(c) {
  validateConfig(c);
  const rows = ["stage", "release"].map((action) => {
    const dir = publisherOperationDirectory(c, { action });
    return {
      action,
      ...(existsSync(dir) ? inspectPublisherJournal(dir) : { exists: false }),
    };
  });
  const prefix = `publisher-${c.version}-metadata-`;
  const dirs = existsSync(evidence)
    ? readdirSync(evidence)
        .filter(
          (name) =>
            name.startsWith(prefix) && hash.test(name.slice(prefix.length)),
        )
        .sort()
    : [];
  if (!dirs.length) rows.push({ action: "metadata", exists: false });
  for (const name of dirs)
    rows.push({
      action: "metadata",
      publicationSha256: name.slice(prefix.length),
      ...inspectPublisherJournal(path.resolve(evidence, name)),
    });
  return rows;
}

// Explicit maintainer entry only; missing production bindings refuse before
// credentials or transport are opened. Inspection is local and never dispatches.
export async function runMaintainerPublisher(c, request) {
  const missing = validateConfig(c);
  if (missing.length)
    throw Error("Production trust missing; publisher dispatch disabled");
  const keys = [
    "version",
    "action",
    "mode",
    "candidateFile",
    "candidateSHA256",
    "publicationFile",
    "approval",
    "tokenFile",
    "resolutions",
  ];
  if (
    !request ||
    request.version !== 1 ||
    Object.keys(request).some((k) => !keys.includes(k)) ||
    !["stage", "release", "metadata"].includes(request.action) ||
    !["start", "resume"].includes(request.mode) ||
    typeof request.tokenFile !== "string" ||
    !path.isAbsolute(request.tokenFile)
  )
    throw Error("Explicit reviewed publisher request required");
  const candidate =
    request.action !== "metadata"
      ? readPublisherFile(request.candidateFile)
      : undefined;
  const publication =
    request.action !== "stage"
      ? readPublisherFile(request.publicationFile)
      : undefined;
  if (candidate) {
    const v = JSON.parse(candidate);
    if (
      v.version !== c.version ||
      v.repository !== c.repository ||
      v.repositoryId !== c.repositoryId ||
      v.ownerId !== c.ownerId
    )
      throw Error("Candidate/config identity differs");
  }
  const seedStat = lstatSync(c.seedFile);
  if (!seedStat.isFile() || seedStat.isSymbolicLink() || seedStat.size > 65536)
    throw Error("Bounded reviewed public seed file required");
  const publicSeed = readFileSync(c.seedFile);
  if (
    sha(publicSeed) !== c.seedSha256 ||
    JSON.parse(publicSeed).signingProfile !== "single-owner-v1"
  )
    throw Error("Reviewed single-owner public seed required");
  if (publication) {
    const { p } = publicationInput(publication, request.approval);
    if (
      p.seedSha256 !== c.seedSha256 ||
      p.descriptor.productVersion !== c.version
    )
      throw Error("Publication/config identity differs");
  }
  const api = githubPublisher(
    readPublisherFile(request.tokenFile, 4096).toString().trim(),
  );
  return runRecordedPublisher(
    {
      action: request.action,
      mode: request.mode,
      directory: publisherOperationDirectory(c, request),
      candidate,
      candidateSHA256: request.candidateSHA256,
      publication,
      approval: request.approval,
      resolutions: request.resolutions,
    },
    { api, readArtifact: async (name) => file(name, 128 * 1024 * 1024).bytes },
  );
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  try {
    if (process.getuid?.() === 0) throw Error("Ordinary maintainer required");
    if (process.argv.length !== 3)
      throw Error("Exactly one staging command required");
    const c = JSON.parse(readFileSync("release/ubuntu.json", "utf8"));
    const missing = validateConfig(c);
    switch (process.argv[2]) {
      case "plan":
        console.log(
          JSON.stringify(
            {
              version: c.version,
              approvedBaseline: git("rev-parse", "main"),
              releaseSource: null,
              missing,
              workflow: c.workflow,
              buildPerformed: false,
              productionKeysCreated: false,
              signed: false,
              published: false,
              activation: false,
            },
            null,
            2,
          ),
        );
        break;
      case "publisher-inspect":
        console.log(JSON.stringify(inspectMaintainerPublisher(c), null, 2));
        break;
      case "publisher":
        if (missing.length)
          throw Error("Production trust missing; publisher dispatch disabled");
        console.log(
          JSON.stringify(
            await runMaintainerPublisher(
              c,
              JSON.parse(
                readPublisherFile(
                  path.resolve(evidence, "publisher-request.json"),
                ),
              ),
            ),
            null,
            2,
          ),
        );
        break;
      case "prepare":
        prepare(c);
        break;
      case "inventory":
        inventory();
        break;
      case "finish":
        finish(c);
        break;
      default:
        throw Error(
          "plan, prepare, inventory, finish, publisher-inspect or explicit publisher request required",
        );
    }
  } catch (e) {
    console.error("ubuntu-release:", e.message);
    process.exitCode = 1;
  }
}
