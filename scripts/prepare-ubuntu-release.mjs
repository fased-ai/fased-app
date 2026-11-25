// Explicit maintainer tooling. Key setup is owner-gated; no implicit publish or activation.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  mkdirSync,
  readFileSync,
  writeFileSync,
  lstatSync,
  readdirSync,
  existsSync,
  mkdtempSync,
  rmSync,
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

import {
  runMetadataRefresh,
  refreshVerifier,
  onlineMetadataSigner,
} from "./refresh-ubuntu-metadata.mjs";

import {
  setupLocalSigning,
  recoverLocalSigning,
  localSigningSession,
  privateSigningPassphrase,
} from "./local-release-signing.mjs";
import { openPublisherJournal } from "./publisher-journal.mjs";
import {
  isPrivateTailscaleOrigin,
  verifyCandidateReplay,
} from "./release-acceptance.mjs";

export let evidence = ".evidence/ubuntu-release";
// A private, independently attested current-reader fixture. This version is
// never promoted: publication still requires release/ubuntu.json's target.
export function compatibilityConfig(c) {
  const [major, minor, patch] = c.version.split(".").map(Number);
  if (
    validateConfig(c).length ||
    (major === 0 && (minor === 0 || (minor === 1 && patch <= 4)))
  )
    throw Error("Reviewed target above the compatibility fixture required");
  return { ...c, version: "0.1.4" };
}

function testPredecessorWorkflow(c, env, command) {
  return (
    c &&
    env.FASED_PACKAGE_ONLY === "true" &&
    env.GITHUB_EVENT_NAME === "workflow_dispatch" &&
    env.GITHUB_REF === "refs/heads/main" &&
    env.GITHUB_REPOSITORY === c.repository &&
    env.GITHUB_REPOSITORY_ID === c.repositoryId &&
    env.GITHUB_REPOSITORY_OWNER_ID === c.ownerId &&
    env.GITHUB_WORKFLOW_REF ===
      `${c.repository}/${c.workflow}@refs/heads/main` &&
    commit.test(env.GITHUB_SHA ?? "") &&
    env.FASED_RELEASE_SOURCE === env.GITHUB_SHA &&
    number.test(env.GITHUB_RUN_ID ?? "") &&
    number.test(env.GITHUB_RUN_ATTEMPT ?? "") &&
    env.RUNNER_OS === "Linux" &&
    env.RUNNER_ARCH === "X64" &&
    ["prepare", "inventory", "finish"].includes(command)
  );
}

// The owner's Fedora has no released predecessor. This source-identical,
// non-publishable baseline is available only to the protected package-only
// workflow and is never accepted as the configured 0.1.12 release.
export function fedoraPredecessorConfig(c, env, command) {
  if (
    env.FASED_FEDORA_PREDECESSOR === undefined ||
    env.FASED_FEDORA_PREDECESSOR === "false"
  )
    return c;
  if (
    env.FASED_FEDORA_PREDECESSOR !== "true" ||
    !testPredecessorWorkflow(c, env, command) ||
    c.version !== "0.1.12" ||
    validateConfig(c).length
  )
    throw Error(
      "Fedora predecessor requires the reviewed package-only workflow",
    );
  return { ...c, version: "0.1.10" };
}

export function automaticPredecessor(c, env, command) {
  const input = env.FASED_AUTOMATIC_PREDECESSOR_PINS;
  if (input === undefined || input === "") return null;
  const pins =
    /^([1-9][0-9]{3,4}):([a-f0-9]{64}):([a-f0-9]{32})(?::(\S+):([a-f0-9]{64}))?$/.exec(
      input,
    );
  if (
    pins?.[0] !== input ||
    Number(pins[1]) > 60000 ||
    (pins[4] !== undefined && !isPrivateTailscaleOrigin(pins[4])) ||
    !testPredecessorWorkflow(c, env, command) ||
    (env.FASED_FEDORA_PREDECESSOR !== undefined &&
      env.FASED_FEDORA_PREDECESSOR !== "false") ||
    c.version !== "0.1.23" ||
    c.releaseAuthority !== "github-attestation-v1" ||
    validateConfig(c).length
  )
    throw Error(
      "Automatic predecessor requires a reviewed package-only workflow and exact receiver pins",
    );
  const [, owner, machine, instance, origin, device] = pins;
  return {
    config: { ...c, version: "0.1.22" },
    owner,
    machine,
    instance,
    ...(origin && { origin, device }),
  };
}
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
  "releaseAuthority",
];
const optionalArm64Fields = ["arm64SeedFile", "arm64SeedSha256"];
const allowedFields = [...fields, ...optionalArm64Fields];
export function validateConfig(c) {
  const arm64Present = c ? optionalArm64Fields.filter((k) => k in c) : [];
  if (
    !c ||
    (arm64Present.length !== 0 && arm64Present.length !== 2) ||
    Object.keys(c).some((k) => !allowedFields.includes(k)) ||
    Object.keys(c).length !==
      fields.length -
        (c.releaseAuthority === undefined ? 1 : 0) +
        arm64Present.length ||
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
  if (
    c.releaseAuthority !== undefined &&
    c.releaseAuthority !== "github-attestation-v1"
  )
    throw Error("Unsupported release authority");
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
  if (arm64Present.length === 2) {
    if ((c.arm64SeedFile === null) !== (c.arm64SeedSha256 === null))
      throw Error("Paired arm64 seed file and digest required");
    if (c.arm64SeedSha256 !== null && !hash.test(c.arm64SeedSha256))
      throw Error("Invalid public approval digest");
    if (
      c.arm64SeedFile !== null &&
      (!/^release\/public\/[A-Za-z0-9_-]+\.json$/.test(c.arm64SeedFile) ||
        c.releaseAuthority !== "github-attestation-v1")
    )
      throw Error("Approved arm64 attested seed path required");
  }
  return (
    c.releaseAuthority === "github-attestation-v1"
      ? ["seedFile", "seedSha256"]
      : [
          "seedFile",
          "seedSha256",
          "custodyApprovalSha256",
          "initialTrustApprovalSha256",
        ]
  ).filter((k) => c[k] === null);
}
export function seedForArch(c, arch = "amd64") {
  if (arch === "amd64")
    return { seedFile: c.seedFile, seedSha256: c.seedSha256 };
  if (arch === "arm64") {
    if (
      c.releaseAuthority !== "github-attestation-v1" ||
      !c.arm64SeedFile ||
      !hash.test(c.arm64SeedSha256 ?? "")
    )
      throw Error("Approved Linux arm64 attested seed configuration required");
    return { seedFile: c.arm64SeedFile, seedSha256: c.arm64SeedSha256 };
  }
  throw Error("Unsupported Linux release architecture");
}
export function releaseContext(c, env, source, arch = "amd64") {
  if (arch !== "amd64" && arch !== "arm64")
    throw Error("Unsupported Linux release architecture");
  validateConfig(c);
  const tag = `v${c.version}`;
  const staged = env.GITHUB_EVENT_NAME === "workflow_dispatch";
  const ref = staged ? "refs/heads/main" : `refs/tags/${tag}`;
  const expectedRunnerArch = arch === "arm64" ? "ARM64" : "X64";
  if (
    (!staged && env.GITHUB_EVENT_NAME !== "push") ||
    env.GITHUB_REF !== ref ||
    env.GITHUB_REPOSITORY !== c.repository ||
    env.GITHUB_REPOSITORY_ID !== c.repositoryId ||
    env.GITHUB_REPOSITORY_OWNER_ID !== c.ownerId ||
    env.GITHUB_WORKFLOW_REF !== `${c.repository}/${c.workflow}@${ref}` ||
    env.RUNNER_OS !== "Linux" ||
    env.RUNNER_ARCH !== expectedRunnerArch ||
    !commit.test(env.GITHUB_SHA ?? "") ||
    !number.test(env.GITHUB_RUN_ID ?? "") ||
    !number.test(env.GITHUB_RUN_ATTEMPT ?? "") ||
    source.commit !== env.GITHUB_SHA ||
    source.workflowCommit !== source.commit ||
    !commit.test(source.tree) ||
    (staged
      ? source.tagObject !== "" || env.FASED_RELEASE_SOURCE !== source.commit
      : !commit.test(source.tagObject)) ||
    source.onMain !== true ||
    source.clean !== true
  )
    throw Error("Exact clean, main-merged release workflow context required");
  return {
    version: c.version,
    tag,
    ...(arch === "arm64" ? { arch } : {}),
    sourceCommit: source.commit,
    sourceTree: source.tree,
    tagObject: source.tagObject,
    workflowCommit: source.workflowCommit,
    runId: env.GITHUB_RUN_ID,
    runAttempt: env.GITHUB_RUN_ATTEMPT,
    ...(staged ? { buildRef: ref } : {}),
  };
}

function git(...args) {
  return execFileSync("git", args, { encoding: "utf8" }).trim();
}
function writeAt(dir, name, value) {
  writeFileSync(path.join(dir, name), JSON.stringify(value, null, 2) + "\n", {
    mode: 0o600,
    flag: "wx",
  });
}
function readAt(dir, name) {
  return JSON.parse(readFileSync(path.join(dir, name), "utf8"));
}
function fileAt(dir, name, max) {
  const p = path.join(dir, name),
    st = lstatSync(p);
  if (!st.isFile() || st.isSymbolicLink() || st.size <= 0 || st.size > max)
    throw Error("Exact bounded candidate file required");
  const bytes = readFileSync(p);
  return {
    bytes,
    identity: { file: name, size: bytes.length, sha256: sha(bytes) },
  };
}
function write(name, value) {
  writeAt(evidence, name, value);
}
function read(name) {
  return readAt(evidence, name);
}
function file(name, max) {
  return fileAt(evidence, name, max);
}
export function bindingOverlay(seed, pin, root, automatic) {
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
  const bindings = declarations.map(([target, type, name, output]) => ({
    target: path.resolve(root, target),
    output,
    source: `package main\nimport "encoding/base64"\nfunc init() {\n b, e := base64.StdEncoding.DecodeString(${JSON.stringify(seed.toString("base64"))})\n if e != nil { panic("invalid compiled public release seed") }\n ${name} = &${type}{Seed:b, SHA256:${JSON.stringify(pin)}}\n}\n`,
  }));
  bindings.push({
    target: path.resolve(
      root,
      "internal/bootstrap/publisher_seed_generated.go",
    ),
    output: "bootstrap.go",
    source: `package bootstrap\n// Publisher-approved seed SHA256: ${pin}\nfunc init() { productionGitHubActivationEnabled = true }\n`,
  });
  if (automatic) {
    bindings.push({
      target: path.resolve(
        root,
        "internal/hostplatform/automatic_predecessor_pins_generated.go",
      ),
      output: "automatic-predecessor.go",
      source: `//go:build linux && amd64 && fased_automatic_predecessor && !custody_acceptance_fixture && !fased_realhost_fixture

package hostplatform

func init() {
	AutomaticPredecessorOwnerUID = ${JSON.stringify(automatic.owner)}
	AutomaticPredecessorMachineSHA256 = ${JSON.stringify(automatic.machine)}
	AutomaticPredecessorInstance = ${JSON.stringify(automatic.instance)}
	AutomaticPredecessorRemoteOrigin = ${JSON.stringify(automatic.origin ?? "")}
	AutomaticPredecessorRemoteDeviceSHA256 = ${JSON.stringify(automatic.device ?? "")}
}
`,
    });
  }
  return bindings;
}
function prepare(c, automatic, arch = "amd64") {
  const missing = validateConfig(c);
  if (missing.length)
    throw Error("Production inputs missing: " + missing.join(", "));
  const { seedFile, seedSha256 } = seedForArch(c, arch);
  if (!process.env.GITHUB_OUTPUT)
    throw Error("Workflow output channel required");
  const source = {
    commit: git("rev-parse", "HEAD"),
    tree: git("rev-parse", "HEAD^{tree}"),
    workflowCommit: git("rev-parse", "HEAD"),
    tagObject:
      process.env.GITHUB_EVENT_NAME === "workflow_dispatch"
        ? ""
        : git("rev-parse", `${process.env.GITHUB_REF}^{tag}`),
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
  const context = releaseContext(c, process.env, source, arch);
  const osRelease = readFileSync("/etc/os-release", "utf8");
  if (
    !/^ID=ubuntu$/m.test(osRelease) ||
    !/^VERSION_ID="24\.04"$/m.test(osRelease)
  )
    throw Error("Ubuntu24.04 build host required");
  const seedStat = lstatSync(seedFile);
  if (!seedStat.isFile() || seedStat.isSymbolicLink())
    throw Error("Public seed must be a regular tracked file");
  const seed = readFileSync(seedFile);
  if (
    seed.length > 65536 ||
    sha(seed) !== seedSha256 ||
    !execFileSync("git", ["show", `${source.commit}:${seedFile}`]).equals(seed)
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
        seedFile,
        "--seed-sha256",
        seedSha256,
        ...(arch === "arm64" ? ["--arch", arch] : []),
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
  for (const b of bindingOverlay(seed, seedSha256, process.cwd(), automatic)) {
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
    releaseAuthority: c.releaseAuthority,
    ...(context.buildRef === "refs/heads/main" ? { linuxBundle: true } : {}),
    configSha256: sha(readFileSync("release/ubuntu.json")),
    seedSha256,
    custodyApprovalSha256: c.custodyApprovalSha256,
    initialTrustApprovalSha256: c.initialTrustApprovalSha256,
    packageLockSha256: sha(readFileSync("package-lock.json")),
    goSumSha256: sha(readFileSync("go.sum")),
    publicRelease: false,
    activation: false,
  });
  writeFileSync(
    process.env.GITHUB_OUTPUT,
    `version=${c.version}\noverlay=${path.resolve(evidence, "overlay.json")}\nautomatic_predecessor=${Boolean(automatic)}\n`,
    { flag: "a" },
  );
}
export function expectedProvenance(input, archive, installer, packages) {
  return {
    ...(input.buildRef ? { buildRef: input.buildRef } : {}),
    ...(input.releaseAuthority === "github-attestation-v1"
      ? { installerSha256: installer.sha256 }
      : {}),
    ...(input.nativePackages === true
      ? { debSha256: packages.deb.sha256, rpmSha256: packages.rpm.sha256 }
      : {}),
    archiveName: archive.file,
    archiveSha256: archive.sha256,
    sourceCommit: input.sourceCommit,
    workflowCommit: input.workflowCommit,
    tag: input.tag,
    runId: input.runId,
    runAttempt: input.runAttempt,
  };
}
export function candidateInventory(input, archive, installer, report, system) {
  const arch = input.arch ?? "amd64";
  if (arch !== "amd64" && arch !== "arm64")
    throw Error("Unsupported Linux release architecture");
  for (const [identity, name] of [
    [archive, `fased-app-linux-${arch}.tar.gz`],
    [installer, `fased-install-linux-${arch}`],
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
    report.manifest?.schema !== (input.linuxBundle === true ? 4 : 2) ||
    report.manifest.unsignedEngineering !== false ||
    report.manifest.desktop !== false ||
    report.manifest.version !== input.version ||
    report.manifest.source !== input.sourceCommit ||
    report.manifest.platform !== "linux" ||
    report.manifest.arch !== arch ||
    report.identity?.sha256 !== archive.sha256 ||
    report.identity?.bytes !== archive.size ||
    !hash.test(report.manifestIdentity?.sha256 ?? "")
  )
    throw Error("Producer/archive version or identity changed");
  if (input.linuxBundle === true) {
    const signer = report.manifest.files?.["fased-signer"];
    const core = report.manifest.files?.["fased-app"];
    if (
      input.nativePackages === true ||
      input.buildRef !== "refs/heads/main" ||
      input.releaseAuthority !== "github-attestation-v1" ||
      !hash.test(signer?.sha256 ?? "") ||
      !Number.isSafeInteger(signer?.bytes) ||
      signer.bytes <= 0 ||
      signer.bytes > 64 * 1024 * 1024 ||
      !hash.test(core?.sha256 ?? "") ||
      !Number.isSafeInteger(core?.bytes) ||
      core.bytes <= 0 ||
      core.bytes > 64 * 1024 * 1024
    )
      throw Error("The browser bundle requires one bounded core and signer");
  } else if (input.linuxBundle !== undefined && input.linuxBundle !== false) {
    throw Error("Unexpected Linux bundle input");
  }
  const result = {
    archive,
    installer,
    manifestSha256: report.manifestIdentity.sha256,
  };
  if (input.nativePackages === true) {
    if (
      input.buildRef !== "refs/heads/main" ||
      input.releaseAuthority !== "github-attestation-v1" ||
      !system
    )
      throw Error("Native packages require an unpublished main candidate");
    const { deb, rpm, receipt } = system;
    const identities = [
      [deb, `fased-app-linux-${arch}.deb`],
      [rpm, `fased-app-linux-${arch}.rpm`],
    ];
    const equalFile = (a, b) =>
      a && b && a.sha256 === b.sha256 && a.bytes === b.bytes;
    if (
      !receipt ||
      receipt.version !== 1 ||
      receipt.product !== input.version ||
      receipt.source !== input.sourceCommit ||
      receipt.architecture !== arch ||
      receipt.publicRelease !== false ||
      !receipt.inputs ||
      !receipt.packages ||
      receipt.inputs["fased-desktop"] !== undefined ||
      !equalFile(
        receipt.inputs["fased-app"],
        report.manifest.files?.["fased-app"],
      ) ||
      !equalFile(receipt.inputs["fased-install"], {
        sha256: installer.sha256,
        bytes: installer.size,
      }) ||
      !hash.test(receipt.inputs["fased-signer"]?.sha256 ?? "") ||
      !Number.isSafeInteger(receipt.inputs["fased-signer"]?.bytes) ||
      receipt.inputs["fased-signer"].bytes <= 0 ||
      receipt.inputs["fased-signer"].bytes > 64 * 1024 * 1024 ||
      Object.keys(receipt.packages).sort().join(",") !==
        identities
          .map(([, name]) => name)
          .sort()
          .join(",") ||
      identities.some(
        ([a, name]) =>
          !a ||
          a.file !== name ||
          !hash.test(a.sha256 ?? "") ||
          !Number.isSafeInteger(a.size) ||
          a.size <= 0 ||
          a.size > 128 * 1024 * 1024 ||
          !equalFile(receipt.packages[name], {
            sha256: a.sha256,
            bytes: a.size,
          }),
      )
    )
      throw Error("Native package identities differ from the reviewed build");
    result.deb = deb;
    result.rpm = rpm;
  } else if (
    system !== undefined ||
    (input.nativePackages !== undefined && input.nativePackages !== false)
  ) {
    throw Error("Unexpected native package inputs");
  }
  return result;
}
function nativePackageInputs(arch = "amd64") {
  return {
    deb: file(`fased-app-linux-${arch}.deb`, 128 * 1024 * 1024).identity,
    rpm: file(`fased-app-linux-${arch}.rpm`, 128 * 1024 * 1024).identity,
    receipt: read(`fased-linux-system-${arch}/packages.json`),
  };
}
function inventory() {
  const input = read("build-inputs.json");
  const arch = input.arch ?? "amd64";
  const archive = file(
    `fased-app-linux-${arch}.tar.gz`,
    128 * 1024 * 1024,
  ).identity;
  const installer = file(
    `fased-install-linux-${arch}`,
    128 * 1024 * 1024,
  ).identity;
  const report = read(`fased-app-linux-${arch}.json`);
  const objects = candidateInventory(
    input,
    archive,
    installer,
    report,
    input.nativePackages === true ? nativePackageInputs(arch) : undefined,
  );
  write("artifact-inventory.json", objects);
  write(
    "expected-provenance.json",
    expectedProvenance(input, archive, installer, objects),
  );
}
export function verifiedCandidate(c, input, objects, decision, proof, policy) {
  const arch = input.arch ?? "amd64";
  const { seedSha256: expectedSeedSha256 } = seedForArch(c, arch);
  if (
    validateConfig(c).length ||
    input.version !== c.version ||
    input.tag !== `v${c.version}` ||
    input.seedSha256 !== expectedSeedSha256 ||
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
      `https://github.com/${c.repository}/${c.workflow}@${input.buildRef ?? `refs/tags/${input.tag}`}` ||
    !["Tlog", "TimestampAuthority"].includes(decision.TimestampSource) ||
    !Number.isFinite(Date.parse(decision.VerifiedAt))
  )
    throw Error("Actual offline-verified provenance required");
}
export function attestedCandidatePacket(input, objects, entry, proof) {
  if (
    input.linuxBundle === true &&
    (input.nativePackages === true ||
      input.buildRef !== "refs/heads/main" ||
      input.tagObject !== "" ||
      objects.deb ||
      objects.rpm)
  )
    throw Error("Linux bundle requires one unpublished main candidate");
  if (
    input.nativePackages === true &&
    (input.buildRef !== "refs/heads/main" ||
      input.tagObject !== "" ||
      !objects.deb ||
      !objects.rpm)
  )
    throw Error("Native packages require an unpublished main candidate");
  return {
    version:
      input.linuxBundle === true
        ? 4
        : input.nativePackages === true
          ? 3
          : input.buildRef
            ? 2
            : 1,
    productVersion: input.version,
    source: input.sourceCommit,
    bundleSha256: objects.manifestSha256,
    archive: objects.archive,
    installer: objects.installer,
    entry,
    ...(input.nativePackages === true
      ? { deb: objects.deb, rpm: objects.rpm }
      : {}),
    release: {
      ...(input.buildRef ? { buildRef: input.buildRef } : {}),
      installerSha256: objects.installer.sha256,
      sourceTree: input.sourceTree,
      tagObject: input.tagObject,
      packageLockSha256: input.packageLockSha256,
      goSumSha256: input.goSumSha256,
      workflowCommit: input.workflowCommit,
      runId: input.runId,
      runAttempt: input.runAttempt,
      provenance: proof,
    },
  };
}
function finish(c) {
  const input = read("build-inputs.json"),
    arch = input.arch ?? "amd64",
    objects = read("artifact-inventory.json"),
    decision = read("verified-provenance.json");
  const { seedSha256: expectedSeedSha256 } = seedForArch(c, arch);
  const archiveName = `fased-app-linux-${arch}.tar.gz`;
  const installerName = `fased-install-linux-${arch}`;
  const reportName = `fased-app-linux-${arch}.json`;
  const provName = `fased-app-linux-${arch}.provenance.sigstore.json`;
  const packetName =
    arch === "arm64" ? "fased-release-linux-arm64.json" : "fased-release.json";
  if (
    JSON.stringify(read("expected-provenance.json")) !==
    JSON.stringify(
      expectedProvenance(input, objects.archive, objects.installer, objects),
    )
  )
    throw Error("Verified artifact/source expectations changed");
  for (const identity of [objects.archive, objects.installer])
    if (
      ![archiveName, installerName].includes(identity.file) ||
      JSON.stringify(file(identity.file, 128 * 1024 * 1024).identity) !==
        JSON.stringify(identity)
    )
      throw Error("Candidate artifact changed after verification");
  if (input.nativePackages === true || input.linuxBundle === true) {
    const current = candidateInventory(
      input,
      objects.archive,
      objects.installer,
      read(reportName),
      input.nativePackages === true ? nativePackageInputs(arch) : undefined,
    );
    if (JSON.stringify(current) !== JSON.stringify(objects))
      throw Error("Native packages changed after provenance verification");
  } else if (objects.deb !== undefined || objects.rpm !== undefined) {
    throw Error("Unexpected native package inventory");
  }
  const proof = file(provName, 64 * 1024).identity;
  verifiedCandidate(
    c,
    input,
    objects,
    decision,
    proof,
    read("provenance-policy.json"),
  );
  if (
    file("first-install-seed.json", 65536).identity.sha256 !==
    expectedSeedSha256
  )
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
    finalizer:
      c.releaseAuthority === "github-attestation-v1"
        ? {
            inputVersion: 1,
            command: "fased-install",
            verify: "--verify-candidate",
            privateKeysAccepted: false,
          }
        : {
            inputVersion: 1,
            command: "fased-release-candidate",
            request: "release-request",
            complete: "release-finalize",
            privateKeysAccepted: false,
            metadataOnly: true,
          },
    missing:
      c.releaseAuthority === "github-attestation-v1"
        ? ["final production bootstrap verification before publication"]
        : [
            "owner-authorized immutable GitHub release/asset locators and reviewed sequence/version/expiry plan",
            "exact descriptor/targets/snapshot/timestamp signing payloads and approved profile signatures",
            "final production bootstrap verification before publication",
          ],
    publicRelease: false,
    activation: false,
  });
  if (c.releaseAuthority === "github-attestation-v1")
    write(
      packetName,
      attestedCandidatePacket(
        input,
        objects,
        file("install.sh", 2 * 1024 * 1024).identity,
        proof,
      ),
    );
}
export function combineMultiArch(
  c,
  {
    amd64Dir = ".evidence/ubuntu-release-amd64",
    arm64Dir = ".evidence/ubuntu-release-arm64",
    outDir = ".evidence/linux-multiarch-release",
    verifyProvenance = ({ policy, expected, bundleFile }) => {
      const scratch = mkdtempSync(path.join(outDir, ".provenance-check-"));
      try {
        const policyFile = path.join(scratch, "policy.json");
        const expectedFile = path.join(scratch, "expected.json");
        writeFileSync(policyFile, JSON.stringify(policy), {
          mode: 0o600,
          flag: "wx",
        });
        writeFileSync(expectedFile, JSON.stringify(expected), {
          mode: 0o600,
          flag: "wx",
        });
        return JSON.parse(
          execFileSync(
            "go",
            [
              "run",
              "./cmd/fased-release-candidate",
              "verify-provenance",
              "--policy-file",
              policyFile,
              "--expected-file",
              expectedFile,
              "--bundle-file",
              bundleFile,
            ],
            { encoding: "utf8" },
          ),
        );
      } finally {
        rmSync(scratch, { recursive: true, force: true });
      }
    },
    renderEntry = ({ version, targets, output }) => {
      const scratch = mkdtempSync(path.join(outDir, ".entry-config-"));
      try {
        const configFile = path.join(scratch, "entry-config.json");
        writeFileSync(
          configFile,
          JSON.stringify({ version, targets }, null, 2) + "\n",
          { mode: 0o600, flag: "wx" },
        );
        execFileSync("node", [
          "scripts/render-install-entry.mjs",
          configFile,
          output,
        ]);
      } finally {
        rmSync(scratch, { recursive: true, force: true });
      }
    },
  } = {},
) {
  const missing = validateConfig(c);
  if (missing.length || c.releaseAuthority !== "github-attestation-v1")
    throw Error("Reviewed dual-arch attested release configuration required");
  const amd64SeedCfg = seedForArch(c, "amd64");
  const arm64SeedCfg = seedForArch(c, "arm64");
  const trackedSeeds = {
    amd64: readFileSync(amd64SeedCfg.seedFile),
    arm64: readFileSync(arm64SeedCfg.seedFile),
  };
  if (
    sha(trackedSeeds.amd64) !== amd64SeedCfg.seedSha256 ||
    sha(trackedSeeds.arm64) !== arm64SeedCfg.seedSha256 ||
    JSON.parse(trackedSeeds.amd64).target?.arch !== "amd64" ||
    JSON.parse(trackedSeeds.arm64).target?.arch !== "arm64"
  )
    throw Error("Tracked amd64/arm64 public trust seeds differ from config");

  const loadSide = (arch, dir, expectedSeedSha256) => {
    const input = readAt(dir, "build-inputs.json");
    if (
      (input.arch ?? "amd64") !== arch ||
      input.linuxBundle !== true ||
      input.nativePackages === true ||
      input.buildRef !== "refs/heads/main" ||
      input.tagObject !== "" ||
      input.releaseAuthority !== "github-attestation-v1" ||
      input.seedSha256 !== expectedSeedSha256
    )
      throw Error(`Invalid ${arch} attested main candidate inputs`);
    const archive = fileAt(
      dir,
      `fased-app-linux-${arch}.tar.gz`,
      128 * 1024 * 1024,
    );
    const installer = fileAt(
      dir,
      `fased-install-linux-${arch}`,
      128 * 1024 * 1024,
    );
    const report = readAt(dir, `fased-app-linux-${arch}.json`);
    const objects = candidateInventory(
      input,
      archive.identity,
      installer.identity,
      report,
    );
    if (
      JSON.stringify(objects) !==
      JSON.stringify(readAt(dir, "artifact-inventory.json"))
    )
      throw Error(`Candidate ${arch} artifact inventory changed`);
    const expected = expectedProvenance(
      input,
      objects.archive,
      objects.installer,
      objects,
    );
    if (
      JSON.stringify(expected) !==
      JSON.stringify(readAt(dir, "expected-provenance.json"))
    )
      throw Error(`Candidate ${arch} expected provenance changed`);
    const proof = fileAt(
      dir,
      `fased-app-linux-${arch}.provenance.sigstore.json`,
      64 * 1024,
    );
    const policy = readAt(dir, "provenance-policy.json");
    const decision = readAt(dir, "verified-provenance.json");
    const seed = fileAt(dir, "first-install-seed.json", 65536);
    if (seed.identity.sha256 !== expectedSeedSha256)
      throw Error(`Candidate ${arch} public trust seed changed`);
    verifiedCandidate(c, input, objects, decision, proof.identity, policy);
    return {
      arch,
      dir,
      input,
      objects,
      expected,
      archive,
      installer,
      proof,
      policy,
      seed,
    };
  };

  const amd = loadSide("amd64", amd64Dir, amd64SeedCfg.seedSha256);
  const arm = loadSide("arm64", arm64Dir, arm64SeedCfg.seedSha256);
  for (const key of [
    "version",
    "tag",
    "sourceCommit",
    "sourceTree",
    "tagObject",
    "workflowCommit",
    "runId",
    "runAttempt",
    "packageLockSha256",
    "goSumSha256",
    "configSha256",
    "releaseAuthority",
  ])
    if (amd.input[key] !== arm.input[key])
      throw Error(`Dual-arch release ${key} differs between amd64 and arm64`);

  rmSync(outDir, { recursive: true, force: true });
  mkdirSync(outDir, { recursive: true, mode: 0o700 });
  for (const side of [amd, arm]) {
    for (const f of [side.archive, side.installer, side.proof])
      writeFileSync(path.join(outDir, f.identity.file), f.bytes, {
        mode: 0o600,
        flag: "wx",
      });
    const seedOut =
      side.arch === "arm64"
        ? "first-install-seed-arm64.json"
        : "first-install-seed.json";
    writeFileSync(path.join(outDir, seedOut), side.seed.bytes, {
      mode: 0o600,
      flag: "wx",
    });
    const liveDecision = verifyProvenance({
      policy: side.policy,
      expected: side.expected,
      bundleFile: path.join(outDir, side.proof.identity.file),
    });
    verifiedCandidate(
      c,
      side.input,
      side.objects,
      liveDecision,
      side.proof.identity,
      side.policy,
    );
  }
  const entryOut = path.join(outDir, "install.sh");
  renderEntry({
    version: amd.input.version,
    targets: [
      {
        installer: path.join(outDir, "fased-install-linux-amd64"),
        seed: path.join(outDir, "first-install-seed.json"),
      },
      {
        installer: path.join(outDir, "fased-install-linux-arm64"),
        seed: path.join(outDir, "first-install-seed-arm64.json"),
      },
    ],
    output: entryOut,
  });
  const entryIdentity = fileAt(outDir, "install.sh", 2 * 1024 * 1024).identity;
  writeAt(
    outDir,
    "fased-release.json",
    attestedCandidatePacket(
      amd.input,
      amd.objects,
      entryIdentity,
      amd.proof.identity,
    ),
  );
  writeAt(
    outDir,
    "fased-release-linux-arm64.json",
    attestedCandidatePacket(
      arm.input,
      arm.objects,
      entryIdentity,
      arm.proof.identity,
    ),
  );
  writeAt(outDir, "signing-inputs.json", {
    version: amd.input.version,
    sourceCommit: amd.input.sourceCommit,
    runId: amd.input.runId,
    runAttempt: amd.input.runAttempt,
    multiArch: true,
    objects: amd.objects,
    arm64Objects: arm.objects,
    entry: entryIdentity,
    publicRelease: false,
    activation: false,
  });
  writeAt(outDir, "multiarch-manifest.json", {
    schemaVersion: 1,
    version: amd.input.version,
    sourceCommit: amd.input.sourceCommit,
    runId: amd.input.runId,
    runAttempt: amd.input.runAttempt,
    architectures: ["amd64", "arm64"],
    assets: [
      amd.archive.identity.file,
      amd.installer.identity.file,
      amd.proof.identity.file,
      "fased-release.json",
      arm.archive.identity.file,
      arm.installer.identity.file,
      arm.proof.identity.file,
      "fased-release-linux-arm64.json",
      "install.sh",
    ],
  });
  return {
    version: amd.input.version,
    sourceCommit: amd.input.sourceCommit,
    runId: amd.input.runId,
    runAttempt: amd.input.runAttempt,
    assets: [
      amd.archive.identity.file,
      amd.installer.identity.file,
      amd.proof.identity.file,
      "fased-release.json",
      arm.archive.identity.file,
      arm.installer.identity.file,
      arm.proof.identity.file,
      "fased-release-linux-arm64.json",
      "install.sh",
    ],
  };
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
  const refreshPrefix = `refresh-${c.version}-`;
  for (const name of (existsSync(evidence) ? readdirSync(evidence) : [])
    .filter(
      (n) =>
        n.startsWith(refreshPrefix) && hash.test(n.slice(refreshPrefix.length)),
    )
    .sort())
    rows.push({
      action: "refresh",
      baselineSha256: name.slice(refreshPrefix.length),
      ...inspectPublisherJournal(path.resolve(evidence, name)),
    });
  return rows;
}

// One explicit invocation only. No schedule, production default or activation.
export async function runMaintainerRefresh(c, request) {
  if (validateConfig(c).length)
    throw Error("Production trust missing; refresh dispatch disabled");
  const fields = [
    "version",
    "mode",
    "publicationFile",
    "publicationSHA256",
    "previousCommit",
    "verifierFile",
    "verifierSHA256",
    "onlineKeyFiles",
    "localSigner",
    "tokenFile",
    "resolutions",
  ];
  if (
    !request ||
    request.version !== 1 ||
    Object.keys(request).some((k) => !fields.includes(k)) ||
    !["start", "resume"].includes(request.mode) ||
    !hash.test(request.publicationSHA256) ||
    !commit.test(request.previousCommit) ||
    !hash.test(request.verifierSHA256) ||
    !!request.onlineKeyFiles === !!request.localSigner ||
    (request.onlineKeyFiles &&
      Object.keys(request.onlineKeyFiles).sort().join(",") !==
        "snapshot,timestamp") ||
    (request.localSigner &&
      (Object.keys(request.localSigner).sort().join(",") !==
        "identitySHA256,storeFile" ||
        !hash.test(request.localSigner.identitySHA256))) ||
    [
      request.publicationFile,
      request.verifierFile,
      request.tokenFile,
      ...(request.onlineKeyFiles
        ? Object.values(request.onlineKeyFiles)
        : [request.localSigner.storeFile]),
    ].some((v) => typeof v !== "string" || !path.isAbsolute(v))
  )
    throw Error("Explicit pinned refresh request required");
  const seedStat = lstatSync(c.seedFile);
  if (!seedStat.isFile() || seedStat.isSymbolicLink() || seedStat.size > 65536)
    throw Error("Bounded reviewed public seed file required");
  const seed = readFileSync(c.seedFile);
  if (
    sha(seed) !== c.seedSha256 ||
    JSON.parse(seed).signingProfile !== "single-owner-v1"
  )
    throw Error("Reviewed single-owner public seed required");
  const publication = readPublisherFile(request.publicationFile, 1024 * 1024);
  if (
    sha(publication) !== request.publicationSHA256 ||
    JSON.parse(publication).descriptor?.productVersion !== c.version
  )
    throw Error("Refresh/config identity differs");
  const directory = path.resolve(
    evidence,
    `refresh-${c.version}-${request.publicationSHA256}`,
  );
  let api;
  const local = request.localSigner
    ? localSigningSession({
        ...request.localSigner,
        keyClass: "production",
        seed,
        seedSHA256: c.seedSha256,
        allowedRoles: ["snapshot", "timestamp"],
      })
    : null;
  try {
    return await runMetadataRefresh(
      { ...request, directory, seed, seedSHA256: c.seedSha256, publication },
      {
        verify: refreshVerifier(
          request.verifierFile,
          request.verifierSHA256,
          path.resolve(evidence),
        ),
        signOnline: local
          ? local.sign
          : onlineMetadataSigner(seed, c.seedSha256, request.onlineKeyFiles),
        api: async (...args) => {
          api ??= githubPublisher(
            readPublisherFile(request.tokenFile, 4096).toString().trim(),
          );
          return api(...args);
        },
      },
    );
  } finally {
    local?.close();
  }
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

// One owner entry for local setup/sign/recovery. Production is refused before
// credential entry until the existing ownership approval is actually configured.
export async function runMaintainerLocalSigning(
  c,
  operation,
  request,
  {
    getPassphrase = privateSigningPassphrase,
    generate,
    afterFinalize = () => {},
  } = {},
) {
  validateConfig(c);
  if (
    !["signer-setup", "signer-sign", "signer-recover"].includes(operation) ||
    !request ||
    request.version !== 1 ||
    !["disposable", "production"].includes(request.keyClass) ||
    !["start", "resume"].includes(request.mode)
  )
    throw Error("Explicit local signing operation required");
  request = JSON.parse(JSON.stringify(request));
  if (request.keyClass === "production") {
    if (
      !c.custodyApprovalSha256 ||
      !request.ownerApprovalFile ||
      sha(readPublisherFile(request.ownerApprovalFile, 65536)) !==
        c.custodyApprovalSha256
    )
      throw Error(
        "Production signing ownership approval missing; no keys admitted",
      );
  }
  const common = ["version", "keyClass", "mode", "ownerApprovalFile"];
  const specific =
    operation === "signer-setup"
      ? ["directory", "archiveDirectory"]
      : operation === "signer-recover"
        ? ["directory", "archiveFile", "archiveSHA256", "identitySHA256"]
        : [
            "directory",
            "storeFile",
            "identitySHA256",
            "candidateFile",
            "candidateSHA256",
            "seedFile",
            "seedSHA256",
            "verifierFile",
            "verifierSHA256",
            "workDirectory",
            "outputDirectory",
          ];
  if (
    Object.keys(request).some((k) => ![...common, ...specific].includes(k)) ||
    specific
      .filter((k) => !k.endsWith("SHA256"))
      .some(
        (k) => typeof request[k] !== "string" || !path.isAbsolute(request[k]),
      ) ||
    specific
      .filter((k) => k.endsWith("SHA256"))
      .some((k) => !hash.test(request[k]))
  )
    throw Error("Pinned private local signing request required");
  if (operation !== "signer-sign") {
    const pass = await getPassphrase(
      operation === "signer-setup"
        ? "New release passphrase: "
        : "Recovery passphrase: ",
    );
    try {
      if (operation === "signer-setup") {
        const again = await getPassphrase("Confirm release passphrase: ");
        try {
          if (!pass.equals(again)) throw Error("Release passphrases differ");
        } finally {
          again.fill(0);
        }
        return setupLocalSigning(
          { ...request, passphrase: pass },
          generate ? { generate } : {},
        );
      }
      return recoverLocalSigning({ ...request, passphrase: pass });
    } finally {
      pass.fill(0);
    }
  }
  const candidate = readPublisherFile(request.candidateFile, 65536),
    seed = readPublisherFile(request.seedFile, 65536);
  if (
    sha(candidate) !== request.candidateSHA256 ||
    sha(seed) !== request.seedSHA256 ||
    sha(readPublisherFile(request.verifierFile, 128 * 1024 * 1024)) !==
      request.verifierSHA256
  )
    throw Error("Local signing input changed");
  const document = JSON.parse(candidate),
    resolveCandidate = (p) =>
      path.resolve(path.dirname(request.candidateFile), p);
  if (
    resolveCandidate(document.seedFile) !== request.seedFile ||
    document.seedSha256 !== request.seedSHA256
  )
    throw Error("Candidate and signing seed differ");
  const binding = sha(
    JSON.stringify({
      operation,
      keyClass: request.keyClass,
      candidate: request.candidateSHA256,
      seed: request.seedSHA256,
      identity: request.identitySHA256,
      verifier: request.verifierSHA256,
      work: request.workDirectory,
      output: request.outputDirectory,
    }),
  );
  const journal = openPublisherJournal(request.directory, binding, {
    resume: request.mode === "resume",
  });
  const signer = localSigningSession(
    {
      storeFile: request.storeFile,
      identitySHA256: request.identitySHA256,
      keyClass: request.keyClass,
      seed,
      seedSHA256: request.seedSHA256,
      allowedRoles: ["targets", "snapshot", "timestamp"],
    },
    { getPassphrase },
  );
  try {
    const call = (command, role) => {
      if (
        sha(readPublisherFile(request.candidateFile, 65536)) !==
          request.candidateSHA256 ||
        sha(readPublisherFile(request.seedFile, 65536)) !==
          request.seedSHA256 ||
        sha(readPublisherFile(request.verifierFile, 128 * 1024 * 1024)) !==
          request.verifierSHA256
      )
        throw Error("Signing input changed while unlocked");
      const scratch = mkdtempSync(path.join(request.directory, ".sign-input-"));
      try {
        const approvalsFile = path.join(scratch, "approvals.json");
        writeFileSync(
          approvalsFile,
          JSON.stringify(journal.state.approvals ?? {}) + "\n",
          { mode: 0o600, flag: "wx" },
        );
        const args = [
          command,
          "--candidate-file",
          request.candidateFile,
          "--approvals-file",
          approvalsFile,
          "--work-dir",
          request.workDirectory,
        ];
        if (role) args.push("--role", role);
        else args.push("--output", request.outputDirectory);
        return JSON.parse(
          execFileSync(request.verifierFile, args, {
            encoding: "utf8",
            timeout: 30000,
            maxBuffer: 2 * 1024 * 1024,
            stdio: ["ignore", "pipe", "pipe"],
          }),
        );
      } finally {
        rmSync(scratch, { recursive: true, force: true });
      }
    };
    if (journal.state.complete) {
      const report = call("release-inspect");
      const publication = readPublisherFile(
        path.join(request.outputDirectory, "publication.json"),
        1024 * 1024,
      );
      if (
        sha(publication) !== journal.state.result.publicationSHA256 ||
        JSON.stringify(report) !== JSON.stringify(journal.state.result.report)
      )
        throw Error("Completed local signing output changed");
      return { ...journal.state.result, noOp: true };
    }
    journal.state.approvals ??= {};
    for (const role of ["targets", "snapshot", "timestamp"]) {
      const r = call("release-request", role);
      if (
        r.role !== role ||
        r.seedSha256 !== request.seedSHA256 ||
        (journal.state.planSHA256 && journal.state.planSHA256 !== r.planSha256)
      )
        throw Error("Release signing authority changed");
      journal.state.planSHA256 = r.planSha256;
      journal.save();
      if (!journal.state.approvals[role]) {
        journal.state.approvals[role] = await signer.sign(r);
        journal.save();
      }
    }
    const recovering = journal.state.finalizerStarted === true;
    if (!recovering) {
      journal.state.finalizerStarted = true;
      journal.save();
    }
    const report = call(recovering ? "release-inspect" : "release-finalize");
    afterFinalize();
    const publication = readPublisherFile(
      path.join(request.outputDirectory, "publication.json"),
      1024 * 1024,
    );
    const result = {
      version: 1,
      report,
      publicationSHA256: sha(publication),
      identitySHA256: request.identitySHA256,
      published: false,
      activation: false,
    };
    journal.state.result = result;
    journal.state.complete = true;
    journal.save();
    return { ...result, noOp: false };
  } finally {
    signer.close();
    journal.close();
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  try {
    if (process.getuid?.() === 0) throw Error("Ordinary maintainer required");
    if (process.argv.length !== 3)
      throw Error("Exactly one staging command required");
    const targetArch = process.env.FASED_TARGET_ARCH ?? "amd64";
    if (targetArch !== "amd64" && targetArch !== "arm64")
      throw Error("Unsupported FASED_TARGET_ARCH");
    let c = JSON.parse(readFileSync("release/ubuntu.json", "utf8"));
    let command = process.argv[2];
    if (targetArch === "arm64") {
      if (
        command.startsWith("compatibility-") ||
        process.env.FASED_FEDORA_PREDECESSOR === "true" ||
        (process.env.FASED_AUTOMATIC_PREDECESSOR_PINS ?? "") !== "" ||
        !["prepare", "inventory", "finish"].includes(command)
      )
        throw Error("arm64 staging supports only prepare, inventory, finish");
      evidence = ".evidence/ubuntu-release-arm64";
    }
    if (command.startsWith("compatibility-")) {
      command = command.slice("compatibility-".length);
      if (!["prepare", "inventory", "finish"].includes(command))
        throw Error("Compatibility fixtures cannot publish or sign");
      evidence = ".evidence/ubuntu-release-compatibility";
      c = compatibilityConfig(c);
    }
    const automatic =
      targetArch === "amd64"
        ? automaticPredecessor(c, process.env, command)
        : null;
    if (targetArch === "amd64")
      c = automatic?.config ?? fedoraPredecessorConfig(c, process.env, command);
    const missing = validateConfig(c);
    switch (command) {
      case "replay": {
        if (
          process.env.GITHUB_REF !== "refs/heads/main" ||
          process.env.GITHUB_EVENT_NAME !== "workflow_dispatch" ||
          process.env.GITHUB_RUN_ATTEMPT !== "1"
        )
          throw Error(
            "Acceptance-only replay requires one reviewed-main dispatch",
          );
        console.log(
          JSON.stringify(
            await verifyCandidateReplay(c, evidence, {
              runId: process.env.FASED_REPLAY_RUN_ID,
              attempt: process.env.FASED_REPLAY_RUN_ATTEMPT,
              downloadArtifact: (id) =>
                execFileSync(
                  "gh",
                  [
                    "api",
                    `/repos/fased-ai/fased-app/actions/artifacts/${id}/zip`,
                  ],
                  {
                    encoding: "buffer",
                    maxBuffer: 128 * 1024 * 1024,
                    timeout: 60000,
                    stdio: ["ignore", "pipe", "pipe"],
                  },
                ),
              api: async (method, url) => {
                if (method !== "GET")
                  throw Error("Replay preparation is read-only");
                return JSON.parse(
                  execFileSync("gh", ["api", url], {
                    encoding: "utf8",
                    maxBuffer: 2 * 1024 * 1024,
                    timeout: 30000,
                  }),
                );
              },
            }),
            null,
            2,
          ),
        );
        break;
      }
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
      case "signer-setup":
      case "signer-sign":
      case "signer-recover":
        console.log(
          JSON.stringify(
            await runMaintainerLocalSigning(
              c,
              process.argv[2],
              JSON.parse(
                readPublisherFile(
                  path.resolve(evidence, "local-signing-request.json"),
                ),
              ),
            ),
            null,
            2,
          ),
        );
        break;
      case "publisher-inspect":
        console.log(JSON.stringify(inspectMaintainerPublisher(c), null, 2));
        break;
      case "publisher-refresh":
        if (missing.length)
          throw Error("Production trust missing; refresh dispatch disabled");
        console.log(
          JSON.stringify(
            await runMaintainerRefresh(
              c,
              JSON.parse(
                readPublisherFile(
                  path.resolve(evidence, "refresh-request.json"),
                ),
              ),
            ),
            null,
            2,
          ),
        );
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
        prepare(c, automatic, targetArch);
        break;
      case "inventory":
        inventory();
        break;
      case "finish":
        finish(c);
        break;
      case "combine":
        if (targetArch !== "amd64")
          throw Error("combine runs on the primary host only");
        console.log(JSON.stringify(combineMultiArch(c), null, 2));
        break;
      default:
        throw Error(
          "plan, prepare, inventory, finish, combine, publisher-inspect, publisher-refresh, signer-setup, signer-sign, signer-recover or explicit publisher request required",
        );
    }
  } catch (e) {
    console.error("ubuntu-release:", e.message);
    process.exitCode = 1;
  }
}
