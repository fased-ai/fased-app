import { strict as assert } from "node:assert";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import {
  bindingOverlay,
  compatibilityConfig,
  candidateInventory,
  expectedProvenance,
  fedoraPredecessorConfig,
  releaseContext,
  validateConfig,
  verifiedCandidate,
} from "./prepare-ubuntu-release.mjs";

test("compatibility reader is explicitly private and cannot replace the target config", () => {
  const original = { ...complete(), version: "0.1.6" };
  const snapshot = JSON.stringify(original);
  assert.equal(compatibilityConfig(original).version, "0.1.4");
  assert.equal(JSON.stringify(original), snapshot);
  assert.throws(() => compatibilityConfig({ ...original, version: "0.1.4" }));
});

const rawConfig = JSON.parse(
  readFileSync(new URL("../release/ubuntu.json", import.meta.url)),
);
const sha = (v) => createHash("sha256").update(v).digest("hex");
const copy = (v) => structuredClone(v);
const unconfigured = () => ({
  ...rawConfig,

  seedFile: null,
  seedSha256: null,
  custodyApprovalSha256: null,
  initialTrustApprovalSha256: null,
});
const complete = () => ({
  ...rawConfig,
  version: "0.1.0", // Deliberate immutable fixture, independent of the current release.
  seedFile: "release/public/ubuntu-amd64.json",
  seedSha256: "a".repeat(64),
  custodyApprovalSha256: "b".repeat(64),
  initialTrustApprovalSha256: "c".repeat(64),
});
const source = {
  commit: "d".repeat(40),
  workflowCommit: "d".repeat(40),
  tree: "e".repeat(40),
  tagObject: "f".repeat(40),
  onMain: true,
  clean: true,
};
const env = {
  GITHUB_EVENT_NAME: "push",
  GITHUB_REF: "refs/tags/v0.1.0",
  GITHUB_REPOSITORY: rawConfig.repository,
  GITHUB_REPOSITORY_ID: rawConfig.repositoryId,
  GITHUB_REPOSITORY_OWNER_ID: rawConfig.ownerId,
  GITHUB_WORKFLOW_REF: `${rawConfig.repository}/.github/workflows/release.yml@refs/tags/v0.1.0`,
  GITHUB_SHA: source.commit,
  GITHUB_RUN_ID: "12",
  GITHUB_RUN_ATTEMPT: "1",
  RUNNER_OS: "Linux",
  RUNNER_ARCH: "X64",
};

test("Fedora test predecessor is package-only and cannot select a publisher", () => {
  const c = { ...rawConfig, version: "0.1.12" };
  const staged = {
    ...env,
    GITHUB_EVENT_NAME: "workflow_dispatch",
    GITHUB_REF: "refs/heads/main",
    GITHUB_WORKFLOW_REF: `${c.repository}/${c.workflow}@refs/heads/main`,
    FASED_RELEASE_SOURCE: source.commit,
    FASED_PACKAGE_ONLY: "true",
    FASED_FEDORA_PREDECESSOR: "true",
  };
  assert.equal(fedoraPredecessorConfig(c, staged, "prepare").version, "0.1.10");
  assert.equal(c.version, "0.1.12");
  for (const patch of [
    { FASED_PACKAGE_ONLY: "false" },
    { GITHUB_REF: "refs/heads/other" },
    { GITHUB_REPOSITORY_ID: "7" },
    { FASED_RELEASE_SOURCE: "b".repeat(40) },
    { FASED_FEDORA_PREDECESSOR: "unknown" },
  ]) {
    assert.throws(() =>
      fedoraPredecessorConfig(c, { ...staged, ...patch }, "prepare"),
    );
  }
  for (const command of ["publisher", "replay", "signer-sign"]) {
    assert.throws(() => fedoraPredecessorConfig(c, staged, command));
  }
  assert.throws(
    () => verifiedCandidate(c, { version: "0.1.10" }, {}, {}, {}, {}),
    /Candidate\/public approval identity changed/,
  );
  assert.throws(() =>
    fedoraPredecessorConfig({ ...c, version: "0.1.13" }, staged, "prepare"),
  );
});

test("production trust inputs are explicit; configuration cannot redirect authority", () => {
  assert.deepEqual(validateConfig(unconfigured()), ["seedFile", "seedSha256"]);
  assert.deepEqual(validateConfig(complete()), []);
  assert.deepEqual(validateConfig(rawConfig), []);
  for (const patch of [
    { version: "0.1.0-rc.1" },
    { version: "00.1.0" },
    { repository: "other/app" },
    { repositoryId: "7" },
    { ownerId: "3" },
    { workflow: "other.yml" },
    { target: { os: "linux", arch: "arm64", ubuntu: "24.04" } },
    { seedFile: "../../secret" },
    { seedSha256: "bad" },
    { privateKey: "never" },
    { target: { ...rawConfig.target, extra: true } },
  ])
    assert.throws(() => validateConfig({ ...complete(), ...patch }));
});

test("production public inputs bind the signing identity, seed and pre-build approval", () => {
  const publicFile = (name) =>
    readFileSync(new URL(`../release/public/${name}`, import.meta.url));
  const seedBytes = readFileSync(
    new URL(`../${rawConfig.seedFile}`, import.meta.url),
  );
  const seed = JSON.parse(seedBytes);
  if (rawConfig.releaseAuthority === "github-attestation-v1") {
    assert.equal(sha(seedBytes), rawConfig.seedSha256);
    assert.equal(seed.version, 4);
    assert.equal(seed.signingProfile, "github-attestation-v1");
    assert.equal(seed.root, "");
    assert.equal(seed.provenance.repository, rawConfig.repository);
    assert.equal(seed.provenance.ownerId, rawConfig.ownerId);
    assert.equal(rawConfig.custodyApprovalSha256, null);
    assert.equal(rawConfig.initialTrustApprovalSha256, null);
    return;
  }
  const approvalBytes = publicFile(
    `ubuntu-${rawConfig.version}-initial-trust.json`,
  );
  const approval = JSON.parse(approvalBytes);
  const signing = JSON.parse(
    publicFile(`ubuntu-${rawConfig.version}-signing.json`),
  );
  assert.equal(sha(seedBytes), rawConfig.seedSha256);
  assert.equal(sha(approvalBytes), rawConfig.initialTrustApprovalSha256);
  assert.equal(approval.seedSha256, rawConfig.seedSha256);
  assert.equal(approval.seedFile, rawConfig.seedFile);
  assert.equal(approval.releaseVersion, rawConfig.version);
  assert.equal(signing.releaseVersion, rawConfig.version);
  assert.equal(
    approval.versionedInstallerUrl,
    `https://github.com/${rawConfig.repository}/releases/download/v${rawConfig.version}/install.sh`,
  );
  assert.equal(approval.ownerApprovalSha256, rawConfig.custodyApprovalSha256);
  assert.equal(signing.ownerApprovalSha256, rawConfig.custodyApprovalSha256);
  assert.equal(approval.signingIdentitySHA256, signing.identitySHA256);
  assert.equal(signing.recoveryVerified, true);
  assert.equal(seed.version, 3);
  assert.equal(seed.signingProfile, "single-owner-v1");
  assert.equal(seed.developmentOnly, false);
  assert.equal(seed.minimumSequence, "1");
  assert.equal(seed.github.metadataBranch, "release-metadata");
  for (const identity of [
    seed,
    seed.github,
    seed.provenance,
    signing,
    approval,
  ]) {
    assert.equal(identity.repository, rawConfig.repository);
  }
  for (const identity of [seed.github, seed.provenance, approval]) {
    assert.equal(identity.repositoryId, rawConfig.repositoryId);
  }
  assert.equal(seed.provenance.ownerId, rawConfig.ownerId);
  assert.equal(seed.provenance.workflow, rawConfig.workflow);
  assert.equal(
    sha(Buffer.from(seed.provenance.trustedRoot, "base64")),
    approval.provenanceRootSha256,
  );
  const root = JSON.parse(Buffer.from(seed.root, "base64"));
  assert.equal(Object.keys(root.signed.keys).length, 4);
  assert.equal(new Set(Object.values(signing.roles)).size, 4);
  for (const [role, keyid] of Object.entries(signing.roles)) {
    assert.deepEqual(root.signed.roles[role], {
      keyids: [keyid],
      threshold: 1,
    });
    assert.equal(root.signed.keys[keyid].keytype, "ed25519");
  }
  assert.equal(approval.scope, "pre-build-public-verification-inputs");
  for (const key of [
    "publishingAuthorized",
    "schedulingAuthorized",
    "activationAuthorized",
  ]) {
    assert.equal(approval[key], false);
  }
  for (const raw of [
    seedBytes,
    approvalBytes,
    publicFile(`ubuntu-${rawConfig.version}-signing.json`),
  ]) {
    assert.doesNotMatch(
      raw.toString(),
      /BEGIN (?:ENCRYPTED )?PRIVATE KEY|\/home\/|keys\.enc/,
    );
  }
});

test("candidate preparation CLI reaches verified metadata output without a publisher request", async (t) => {
  for (const variant of [
    "complete",
    "changed-tracked-seed",
    "wrong-provenance-owner",
  ]) {
    await t.test(variant, (t) => {
      const f = durableFixture(t, "stage");
      const moduleFile = resolve("scripts/prepare-ubuntu-release.mjs");
      const seedBytes = readFileSync(rawConfig.seedFile);
      const seed = JSON.parse(seedBytes);
      mkdirSync(join(f.parent, "release/public"), {
        recursive: true,
        mode: 0o700,
      });
      writeFileSync(
        join(f.parent, "release/ubuntu.json"),
        JSON.stringify(rawConfig),
        { mode: 0o600 },
      );
      writeFileSync(join(f.parent, rawConfig.seedFile), seedBytes, {
        mode: 0o600,
      });
      writeFileSync(
        join(f.parent, "package-lock.json"),
        "fixture-package-lock",
        { mode: 0o600 },
      );
      writeFileSync(join(f.parent, "go.sum"), "fixture-go-sum", {
        mode: 0o600,
      });
      const descriptor = join(f.parent, "prepare-input.json");
      const callsFile = join(f.parent, "calls.json");
      const workflowOutput = join(f.parent, "workflow-output");
      writeFileSync(
        descriptor,
        JSON.stringify({
          moduleFile,
          source,
          config: rawConfig,
          policy: seed.provenance,
          variant,
          callsFile,
        }),
        { mode: 0o600 },
      );
      // Execute the real CLI in a disposable directory; only host/Git/Go inputs are controlled.
      const code = String.raw`
import fs from "node:fs";import cp from "node:child_process";import{syncBuiltinESMExports}from"node:module";import{pathToFileURL}from"node:url";
const input=JSON.parse(fs.readFileSync(process.argv[1],"utf8"));const originalRead=fs.readFileSync;const calls=[];
fs.readFileSync=(file,...args)=>file==="/etc/os-release"?["ID=ubuntu",'VERSION_ID="24.04"',""].join(String.fromCharCode(10)):originalRead(file,...args);
cp.execFileSync=(file,args,options={})=>{calls.push([file,args]);let value;
if(file==="git"){
 const key=args.join(" ");
 if(key==="rev-parse HEAD")value=input.source.commit;
 else if(key==="rev-parse HEAD^{tree}")value=input.source.tree;
 else if(key==="rev-parse refs/tags/v"+input.config.version+"^{tag}")value=input.source.tagObject;
 else if(key==="status --porcelain")value="";
 else if(key==="merge-base --is-ancestor "+input.source.commit+" refs/remotes/origin/main")value="";
 else if(key==="show "+input.source.commit+":"+input.config.seedFile)value=input.variant==="changed-tracked-seed"?Buffer.from("changed"):originalRead(input.config.seedFile);
 else throw Error("Unexpected Git call: "+key);
}else if(file==="go"&&JSON.stringify(args)===JSON.stringify(["run","./cmd/fased-release-candidate","verify-seed","--seed-file",input.config.seedFile,"--seed-sha256",input.config.seedSha256])){value=JSON.stringify({...input.policy,...(input.variant==="wrong-provenance-owner"?{ownerId:"1"}:{})});}
else throw Error("Unexpected execution: "+file);
return options.encoding?value.toString():Buffer.from(value);
};syncBuiltinESMExports();process.argv=[process.execPath,input.moduleFile,"prepare"];await import(pathToFileURL(input.moduleFile).href);fs.writeFileSync(input.callsFile,JSON.stringify(calls));
`;
      const child = spawnSync(
        process.execPath,
        ["--input-type=module", "-e", code, descriptor],
        {
          cwd: f.parent,
          env: {
            ...process.env,
            ...env,
            GITHUB_REF: `refs/tags/v${rawConfig.version}`,
            GITHUB_WORKFLOW_REF: `${rawConfig.repository}/.github/workflows/release.yml@refs/tags/v${rawConfig.version}`,
            GITHUB_OUTPUT: workflowOutput,
          },
          encoding: "utf8",
          timeout: 10000,
        },
      );
      const output = join(f.parent, ".evidence/ubuntu-release");
      if (variant !== "complete") {
        assert.equal(child.status, 1);
        assert.match(
          child.stderr,
          variant === "changed-tracked-seed"
            ? /Approved tracked public seed changed/
            : /Production public trust repository changed/,
        );
        assert.equal(existsSync(output), false);
        assert.equal(existsSync(workflowOutput), false);
        return;
      }
      assert.equal(child.status, 0, child.stderr);
      const inputs = JSON.parse(
        readFileSync(join(output, "build-inputs.json")),
      );
      assert.equal(inputs.sourceCommit, source.commit);
      assert.equal(inputs.tagObject, source.tagObject);
      assert.equal(inputs.seedSha256, rawConfig.seedSha256);
      assert.equal(inputs.packageLockSha256, sha("fixture-package-lock"));
      assert.equal(inputs.goSumSha256, sha("fixture-go-sum"));
      assert.equal(inputs.publicRelease, false);
      assert.equal(inputs.activation, false);
      assert.deepEqual(
        readFileSync(join(output, "first-install-seed.json")),
        seedBytes,
      );
      assert.deepEqual(
        JSON.parse(readFileSync(join(output, "provenance-policy.json"))),
        seed.provenance,
      );
      const overlay = JSON.parse(readFileSync(join(output, "overlay.json")));
      assert.equal(Object.keys(overlay.Replace).length, 3);
      for (const file of Object.values(overlay.Replace))
        assert.ok(readFileSync(file, "utf8").includes(rawConfig.seedSha256));
      assert.equal(
        readFileSync(workflowOutput, "utf8").match(/^version=(.*)$/m)?.[1],
        rawConfig.version,
      );
      assert.match(readFileSync(workflowOutput, "utf8"), /^overlay=/m);
      assert.equal(
        JSON.parse(readFileSync(callsFile)).filter(([name]) => name === "go")
          .length,
        1,
      );
    });
  }
});

test("only the exact clean annotated main-merged tag-push can build", () => {
  assert.equal(releaseContext(complete(), env, source).tag, "v0.1.0");
  for (const patch of [
    { GITHUB_EVENT_NAME: "workflow_dispatch" },
    { GITHUB_EVENT_NAME: "pull_request" },
    { GITHUB_REF: "refs/heads/main" },
    { GITHUB_REF: "refs/tags/v0.1.1" },
    { GITHUB_REPOSITORY_ID: "7" },
    { GITHUB_REPOSITORY_OWNER_ID: "3" },
    { GITHUB_SHA: "a".repeat(40) },
    { GITHUB_RUN_ATTEMPT: "0" },
    { RUNNER_OS: "Windows" },
    { RUNNER_ARCH: "ARM64" },
    { GITHUB_WORKFLOW_REF: "other" },
  ])
    assert.throws(() =>
      releaseContext(complete(), { ...env, ...patch }, source),
    );
  for (const patch of [
    { onMain: false },
    { clean: false },
    { clean: "false" },
    { tagObject: "" },
    { workflowCommit: "a".repeat(40) },
  ])
    assert.throws(() =>
      releaseContext(complete(), env, { ...source, ...patch }),
    );
});

test("build overlay embeds only the exact approved public seed in both binaries", () => {
  const seed = Buffer.from('{"public":"test-only"}');
  const bindings = bindingOverlay(seed, sha(seed), "/fixture");
  assert.deepEqual(
    bindings.map((b) => b.target),
    [
      "/fixture/cmd/fased-app/publisher_seed_generated.go",
      "/fixture/cmd/fased-install/publisher_seed_generated.go",
      "/fixture/internal/bootstrap/publisher_seed_generated.go",
    ],
  );
  for (const b of bindings.slice(0, 2)) {
    assert.ok(b.source.includes(seed.toString("base64")));
    assert.ok(b.source.includes(sha(seed)));
    assert.match(b.source, /func init\(\)/);
    assert.doesNotMatch(b.source, /os\.Getenv|https:|privateKey/);
  }
  assert.equal(bindings[2].output, "bootstrap.go");
  assert.match(bindings[2].source, /^package bootstrap\n/);
  assert.match(bindings[2].source, /productionGitHubActivationEnabled = true/);
  assert.doesNotMatch(bindings[2].source, /os\.Getenv|https:|privateKey/);
  assert.throws(() => bindingOverlay(seed, "a".repeat(64), "/fixture"));
});

const archive = {
  file: "fased-app-linux-amd64.tar.gz",
  size: 12,
  sha256: "a".repeat(64),
};
const installer = {
  file: "fased-install-linux-amd64",
  size: 13,
  sha256: "b".repeat(64),
};
const input = {
  ...releaseContext(complete(), env, source),
  seedSha256: complete().seedSha256,
  custodyApprovalSha256: complete().custodyApprovalSha256,
  initialTrustApprovalSha256: complete().initialTrustApprovalSha256,
};
const report = {
  publicRelease: false,
  manifest: {
    schema: 2,
    unsignedEngineering: false,
    desktop: false,
    platform: "linux",
    arch: "amd64",
    version: input.version,
    source: input.sourceCommit,
  },
  identity: { bytes: archive.size, sha256: archive.sha256 },
  manifestIdentity: { sha256: "c".repeat(64) },
};

test("actual package identities and provenance expectations cannot drift apart", () => {
  assert.equal(
    candidateInventory(input, archive, installer, report).archive.sha256,
    archive.sha256,
  );
  for (const identity of [
    { bytes: archive.size + 1, sha256: archive.sha256 },
    { size: archive.size, sha256: archive.sha256 },
  ]) {
    assert.throws(
      () =>
        candidateInventory(input, archive, installer, { ...report, identity }),
      /Producer\/archive version or identity changed/,
    );
  }
  const expected = expectedProvenance(input, archive);
  assert.equal(expected.archiveSha256, archive.sha256);
  assert.equal(expected.sourceCommit, input.sourceCommit);
  assert.equal(expected.runId, "12");
  for (const patch of [
    { version: "0.1.1" },
    { source: "b".repeat(40) },
    { schema: 1 },
    { unsignedEngineering: true },
    { desktop: true },
    { arch: "arm64" },
    { platform: "windows" },
  ])
    assert.throws(() =>
      candidateInventory(input, archive, installer, {
        ...report,
        manifest: { ...report.manifest, ...patch },
      }),
    );
  assert.throws(() =>
    candidateInventory(
      input,
      { ...archive, sha256: "d".repeat(64) },
      installer,
      report,
    ),
  );
  assert.throws(() =>
    candidateInventory(
      input,
      archive,
      { ...installer, file: archive.file },
      report,
    ),
  );
});

test("staging requires actual verifier timestamp enums, exact policy and tag identity", () => {
  const policy = {
    version: 1,
    repository: rawConfig.repository,
    repositoryId: rawConfig.repositoryId,
    ownerId: rawConfig.ownerId,
    workflow: rawConfig.workflow,
    trustedRoot: "public-fixture",
  };
  const proof = { sha256: "e".repeat(64) };
  const objects = candidateInventory(input, archive, installer, report);
  const decision = {
    BundleSHA256: proof.sha256,
    PolicySHA256: sha(JSON.stringify(policy)),
    Identity: `https://github.com/${rawConfig.repository}/${rawConfig.workflow}@refs/tags/v0.1.0`,
    TimestampSource: "Tlog",
    VerifiedAt: "2026-11-01T00:00:00Z",
  };
  for (const TimestampSource of ["Tlog", "TimestampAuthority"])
    assert.doesNotThrow(() =>
      verifiedCandidate(
        complete(),
        input,
        objects,
        { ...decision, TimestampSource },
        proof,
        policy,
      ),
    );
  for (const patch of [
    { BundleSHA256: "f".repeat(64) },
    { PolicySHA256: "f".repeat(64) },
    { Identity: decision.Identity.replace("v0.1.0", "v0.1.1") },
    { TimestampSource: "transparency-log" },
    { VerifiedAt: "invalid" },
  ])
    assert.throws(() =>
      verifiedCandidate(
        complete(),
        input,
        objects,
        { ...decision, ...patch },
        proof,
        policy,
      ),
    );
  assert.throws(() =>
    verifiedCandidate(rawConfig, input, objects, decision, proof, policy),
  );
  assert.throws(() =>
    verifiedCandidate(
      complete(),
      { ...input, seedSha256: "d".repeat(64) },
      objects,
      decision,
      proof,
      policy,
    ),
  );
  const altered = copy(policy);
  altered.trustedRoot = "substituted";
  assert.throws(() =>
    verifiedCandidate(complete(), input, objects, decision, proof, altered),
  );
});

test("an explicit package-only run cannot start VM acceptance", () => {
  const release = readFileSync(
    new URL("../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );
  assert.match(
    release,
    /package_only:\n(?:.*\n)*?\s+default: false\n\s+type: boolean/,
  );
  const acceptance = release.slice(
    release.indexOf("  installed-acceptance:\n"),
  );
  const condition = acceptance
    .split("\n")
    .find((line) => line.trimStart().startsWith("if:"));
  assert.match(condition ?? "", /!inputs\.package_only/);
  const inspection = readFileSync(
    new URL("../.github/workflows/inspection-acceptance.yml", import.meta.url),
    "utf8",
  );
  const vm = inspection.slice(inspection.indexOf("  inspection-vm:\n"));
  const vmCondition = vm
    .split("\n")
    .find((line) => line.trimStart().startsWith("if:"));
  assert.match(vmCondition ?? "", /github\.event_name == 'workflow_dispatch'/);
});

test("staged release has one pinned unpublished Ubuntu build", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );
  assert.match(workflow, /workflow_dispatch:/);
  assert.doesNotMatch(
    workflow,
    /push:\n|pull_request|secrets\.|gh release (create|upload|edit|delete)|git push|activate/,
  );
  assert.match(workflow, /installed-acceptance:\n/);
  assert.match(workflow, /--staged-package-only/);
  assert.match(workflow, /--released-local-only/);
  assert.match(workflow, /gh release download v0\.1\.6/);
  assert.doesNotMatch(
    workflow,
    /compatibility-prepare|ubuntu-release-compatibility/,
  );
  assert.doesNotMatch(
    workflow,
    /sudo .*?(node|python3|fased-install|fased-app)/,
  );
  assert.doesNotMatch(workflow, /contents: write/);
  assert.doesNotMatch(workflow, /node scripts\/release-ubuntu\.mjs publish/);
  assert.match(
    workflow,
    /chmod 600 .*fased-app-linux-amd64\.tar\.gz.*fased-install-linux-amd64.*provenance\.sigstore\.json.*install\.sh.*fased-release\.json.*first-install-seed\.json/,
  );
  assert.doesNotMatch(
    workflow,
    /signer-sign|npm run release -- credentials|passphrase|keys\.enc/,
  );
  assert.equal((workflow.match(/runs-on:/g) ?? []).length, 3);
  assert.match(workflow, /candidate-inputs:\n/);
  assert.match(workflow, /ubuntu-amd64:\n    needs: candidate-inputs/);
  assert.match(
    workflow,
    /installed-acceptance:\n    needs: \[candidate-inputs, ubuntu-amd64\]/,
  );
  assert.match(workflow, /runs-on: ubuntu-24\.04/);
  for (const line of workflow.split("\n").filter((s) => s.includes("uses:")))
    assert.match(line, /uses: actions\/[a-z-]+@[a-f0-9]{40}(?: |$)/);
  assert.ok(workflow.indexOf(".mjs prepare") < workflow.indexOf("npm ci"));
  assert.match(workflow, /GOFLAGS: -mod=readonly -overlay=/);
  assert.equal((workflow.match(/npm run build/g) ?? []).length, 1);
  assert.match(
    workflow,
    /subject-path: \|\n\s+\.evidence\/ubuntu-release\/fased-app-linux-amd64.tar.gz\n\s+\.evidence\/ubuntu-release\/fased-install-linux-amd64/,
  );
  assert.match(workflow, /verify-provenance --policy-file/);
  assert.match(workflow, /create-storage-record: false/);
  assert.ok(
    workflow.indexOf("verify-provenance --policy-file") <
      workflow.indexOf(".mjs finish"),
  );
  assert.match(
    workflow,
    /if: \$\{\{ always\(\) && steps\.prepare\.outcome == 'success' \}\}\n\s+uses: actions\/upload-artifact@/,
  );
  const checks = readFileSync(
    new URL("../.github/workflows/checks.yml", import.meta.url),
    "utf8",
  );
  assert.match(
    checks,
    /node --test --test-isolation=none scripts\/prepare-ubuntu-release.test.mjs/,
  );
  assert.match(
    checks,
    /go list [^\n]+\.\/cmd\/fased-release-candidate > \.evidence\/core-test-packages.txt/,
  );
});

test("Fedora predecessor workflow cannot enter VM acceptance or candidate replay", () => {
  const workflow = readFileSync(
    new URL("../.github/workflows/release.yml", import.meta.url),
    "utf8",
  );
  assert.match(
    workflow,
    /fedora_predecessor:\n(?:.*\n)*?\s+default: false\n\s+type: boolean/,
  );
  assert.match(
    workflow,
    /FASED_FEDORA_PREDECESSOR.*== true[\s\S]*?FASED_PACKAGE_ONLY.*!= true[\s\S]*?FASED_REPLAY_RUN_ID/,
  );
  assert.match(workflow, /fedora-test-predecessor.*release-candidate/);
  const installed = workflow.slice(
    workflow.indexOf("  installed-acceptance:\n"),
  );
  assert.match(
    installed.split("\n").find((line) => line.trimStart().startsWith("if:")) ??
      "",
    /!inputs\.package_only/,
  );
});

// Single-owner publisher uses explicit injected fixture transport; no live API.
import {
  publicationInput,
  publishFinalized,
  stageCandidate,
  releaseCandidate,
  githubPublisher,
} from "./publish-ubuntu-release.mjs";
const psha = (b) => createHash("sha256").update(b).digest("hex");
function publisherFixture() {
  const descriptor = {
    version: 3,
    purpose: "fased-app-first-install-production:v1",
    developmentOnly: false,
    repository: "fased-ai/fased-app",
    channel: "stable",
    target: { os: "linux", arch: "amd64" },
    sequence: "7",
    productVersion: "0.1.0",
    source: "a".repeat(40),
    release: { tagObject: "9".repeat(40) },
    github: {
      repositoryId: "1426211857",
      releaseId: "11",
      assetId: "17",
      name: "fased-app-linux-amd64.tar.gz",
      size: 7,
      sha256: psha(Buffer.from("archive")),
    },
  };
  const encoded = (v) => Buffer.from(JSON.stringify(v)).toString("base64");
  const meta = (version = 1) => ({
    signed: { version, expires: new Date(Date.now() + 86400000).toISOString() },
  });
  const db = Buffer.from(JSON.stringify(descriptor));
  const publication = {
    version: 1,
    signingProfile: "single-owner-v1",
    repository: descriptor.repository,
    repositoryId: "1426211857",
    ownerId: "279582504",
    metadataBranch: "release-metadata",
    seedSha256: "b".repeat(64),
    planSha256: "c".repeat(64),
    descriptor,
    files: {
      "1.root.json": encoded(meta()),
      "1.targets.json": encoded(meta()),
      "1.snapshot.json": encoded(meta()),
      "timestamp.json": encoded(meta()),
      [`targets/production-bootstrap/${psha(db)}.descriptor.json`]:
        db.toString("base64"),
      [`targets/provenance/${"d".repeat(64)}.sigstore.json`]: encoded({
        fixture: true,
      }),
    },
  };
  const raw = Buffer.from(JSON.stringify(publication));
  const approval = { sha256: psha(raw), previousCommit: "1".repeat(40) };
  const calls = [],
    records = [];
  let head = approval.previousCommit;
  const candidate = {
    repository: publication.repository,
    repositoryId: publication.repositoryId,
    ownerId: publication.ownerId,
    version: "0.1.0",
    source: descriptor.source,
    tagObject: descriptor.release.tagObject,
    assets: [
      "fased-app-linux-amd64.tar.gz",
      "fased-install-linux-amd64",
      "fased-app-linux-amd64.provenance.sigstore.json",
      "install.sh",
    ].map((name, i) => ({
      name,
      size: i === 0 ? 7 : 1,
      sha256: i === 0 ? descriptor.github.sha256 : psha(Buffer.from("x")),
    })),
  };
  const assets = candidate.assets.map((a, i) => ({
    ...a,
    id: 17 + i,
    state: "uploaded",
    digest: `sha256:${a.sha256}`,
  }));
  let createdTree = [];
  const api = async (method, path, body) => {
    calls.push({ method, path, body });
    if (method === "GET" && path.endsWith("/immutable-releases"))
      return { enabled: true };
    if (path.endsWith("/fased-app"))
      return {
        id: 1426211857,
        full_name: publication.repository,
        owner: { id: 279582504 },
      };
    if (path.endsWith("/releases/11"))
      return {
        id: 11,
        draft: false,
        prerelease: false,
        immutable: true,
        tag_name: "v0.1.0",
        assets,
      };
    if (path.includes("/git/ref/tags/"))
      return {
        ref: "refs/tags/v0.1.0",
        object: { type: "tag", sha: candidate.tagObject },
      };
    if (path.includes("/git/tags/"))
      return {
        sha: candidate.tagObject,
        tag: "v0.1.0",
        object: { type: "commit", sha: candidate.source },
      };
    if (path.includes("/git/ref/"))
      return {
        ref: "refs/heads/release-metadata",
        object: { type: "commit", sha: head },
      };
    if (method === "GET" && path.includes("/git/commits/")) {
      if (path.endsWith(approval.previousCommit))
        return { sha: approval.previousCommit, tree: { sha: "2".repeat(40) } };
      return {
        sha: "4".repeat(40),
        tree: { sha: "3".repeat(40) },
        parents: [{ sha: approval.previousCommit }],
      };
    }
    if (method === "GET" && path.includes("/git/trees/"))
      return {
        truncated: false,
        tree: path.includes("2".repeat(40)) ? [] : createdTree,
      };
    if (method === "POST" && path.endsWith("/git/blobs")) {
      const bytes = Buffer.from(body.content, "base64");
      return {
        sha: createHash("sha1")
          .update(`blob ${bytes.length}\0`)
          .update(bytes)
          .digest("hex"),
      };
    }
    if (method === "POST" && path.endsWith("/git/trees")) {
      createdTree = body.tree;
      return { sha: "3".repeat(40) };
    }
    if (method === "POST" && path.endsWith("/git/commits"))
      return { sha: "4".repeat(40) };
    if (method === "PATCH") {
      assert.equal(body.force, false);
      head = body.sha;
      return { object: { sha: head } };
    }
    throw Error("unexpected fixture request");
  };
  return {
    publication,
    raw,
    approval,
    calls,
    records,
    api,
    candidate,
    assets,
    record: async (x) => records.push(x),
  };
}
test("single owner publishes finalized metadata with non-force ref and readback", async () => {
  const f = publisherFixture();
  const r = await publishFinalized(f.raw, f.approval, f.api, f.record);
  assert.equal(r.activation, false);
  assert.equal(f.calls.filter((x) => x.method === "PATCH").length, 1);
  assert.equal(f.records.at(-1).phase, "metadata-published");
});
test("publisher refuses changed input, identity, expiry, draft, history and concurrent head", async () => {
  for (const variant of [
    "pin",
    "identity",
    "expired",
    "draft",
    "history",
    "race",
    "record",
  ]) {
    const f = publisherFixture();
    let raw = f.raw;
    let approval = f.approval;
    if (variant === "pin") raw = Buffer.concat([raw, Buffer.from(" ")]);
    if (variant === "expired") {
      f.publication.files["timestamp.json"] = Buffer.from(
        JSON.stringify({ signed: { expires: "2000-01-01T00:00:00Z" } }),
      ).toString("base64");
      raw = Buffer.from(JSON.stringify(f.publication));
      approval = { ...approval, sha256: psha(raw) };
    }
    let refs = 0;
    const api = async (m, p, b) => {
      const r = await f.api(m, p, b);
      if (variant === "identity" && p.endsWith("/fased-app")) r.id = 9;
      if (variant === "draft" && p.includes("/releases/")) r.draft = true;
      if (variant === "history" && p.includes("/git/trees/"))
        r.tree = [
          {
            path: "1.root.json",
            sha: "9".repeat(40),
            type: "blob",
            mode: "100644",
          },
        ];
      if (variant === "race" && p.includes("/git/ref/heads/") && ++refs === 2)
        r.object.sha = "9".repeat(40);
      return r;
    };
    await assert.rejects(
      publishFinalized(
        raw,
        approval,
        api,
        variant === "record"
          ? async () => {
              throw Error("disk full");
            }
          : f.record,
      ),
      variant,
    );
    assert.equal(
      f.calls.filter((x) => x.method === "PATCH").length,
      0,
      variant,
    );
  }
  assert.throws(() => githubPublisher(""));
});
test("candidate stages exact bytes as draft only; failed upload is not replayed", async () => {
  for (const failure of [false, true]) {
    const f = publisherFixture(),
      c = Buffer.from(JSON.stringify(f.candidate));
    let uploads = 0;
    const api = async (m, p, b) => {
      if (m === "GET") return f.api(m, p, b);
      if (m === "POST") return { id: 11, draft: true, tag_name: "v0.1.0" };
      if (m === "UPLOAD") {
        uploads++;
        if (failure) throw Error("connection lost");
        return f.assets.find((x) => p.endsWith(`name=${x.name}`));
      }
      throw Error("unexpected mutation");
    };
    const op = stageCandidate(
      c,
      psha(c),
      async (name) => Buffer.from(name.endsWith(".tar.gz") ? "archive" : "x"),
      api,
      f.record,
    );
    if (failure) {
      await assert.rejects(op);
      assert.equal(uploads, 1);
      assert.equal(f.records.at(-1).phase, "asset-upload-attempted");
    } else {
      const staged = await op;
      assert.equal(staged.published, false);
      assert.equal(staged.assets.length, 4);
    }
  }
});
test("only exact finalized complete draft may become immutable release", async () => {
  const f = publisherFixture(),
    raw = Buffer.from(JSON.stringify(f.candidate));
  let patches = 0;
  const api = async (m, p, b) => {
    if (m === "PATCH") {
      patches++;
      assert.equal(b.tag_name, "v0.1.0");
      assert.equal(b.target_commitish, f.candidate.source);
      return { id: 11, draft: false, immutable: true };
    }
    if (p.endsWith("/releases/11"))
      return {
        id: 11,
        draft: true,
        prerelease: false,
        tag_name: "v0.1.0",
        assets: f.assets,
      };
    return f.api(m, p, b);
  };
  await releaseCandidate(raw, psha(raw), f.raw, f.approval, api, f.record);
  assert.equal(patches, 1);
  f.assets[1].digest = "sha256:" + "0".repeat(64);
  await assert.rejects(
    releaseCandidate(raw, psha(raw), f.raw, f.approval, api, f.record),
  );
  assert.equal(patches, 1);
});
test("publisher snapshots approvals and refuses wrong blobs/commits/tag before authority", async () => {
  for (const variant of ["approval-mutation", "blob", "commit", "tag"]) {
    const f = publisherFixture();
    const api = async (m, p, b) => {
      if (variant === "approval-mutation" && p.endsWith("/fased-app"))
        f.approval.previousCommit = "8".repeat(40);
      const r = await f.api(m, p, b);
      if (variant === "approval-mutation" && p.includes("/git/ref/heads/"))
        r.object.sha = "8".repeat(40);
      if (variant === "blob" && m === "POST" && p.endsWith("/git/blobs"))
        r.sha = "0".repeat(40);
      if (
        variant === "commit" &&
        m === "GET" &&
        p.endsWith("/git/commits/" + "4".repeat(40))
      )
        r.tree.sha = "0".repeat(40);
      if (variant === "tag" && p.includes("/git/ref/tags/"))
        r.object.sha = "0".repeat(40);
      return r;
    };
    await assert.rejects(publishFinalized(f.raw, f.approval, api, f.record));
    assert.equal(
      f.calls.filter((c) => c.method === "PATCH").length,
      0,
      variant,
    );
  }
});
test("stage refuses missing or moved canonical tag before creating a draft", async () => {
  const f = publisherFixture(),
    raw = Buffer.from(JSON.stringify(f.candidate));
  const api = async (m, p, b) => {
    const r = await f.api(m, p, b);
    if (p.includes("/git/tags/")) r.object.sha = "8".repeat(40);
    return r;
  };
  await assert.rejects(
    stageCandidate(
      raw,
      psha(raw),
      async (n) => Buffer.from(n.endsWith(".tar.gz") ? "archive" : "x"),
      api,
      f.record,
    ),
  );
  assert.equal(
    f.calls.some((c) => c.method !== "GET"),
    false,
  );
});
test("publisher rejects prior timestamp bytes that do not match tree blob identity", async () => {
  const f = publisherFixture();
  const truth = Buffer.from(JSON.stringify({ signed: { version: 99 } }));
  const id = createHash("sha1")
    .update(`blob ${truth.length}\0`)
    .update(truth)
    .digest("hex");
  const api = async (m, p, b) => {
    if (m === "GET" && p.endsWith(`/git/blobs/${id}`))
      return {
        sha: id,
        encoding: "base64",
        content: Buffer.from(
          JSON.stringify({ signed: { version: 0 } }),
        ).toString("base64"),
      };
    const r = await f.api(m, p, b);
    if (m === "GET" && p.includes("/git/trees/"))
      r.tree = [
        { path: "timestamp.json", type: "blob", mode: "100644", sha: id },
      ];
    return r;
  };
  await assert.rejects(
    publishFinalized(f.raw, f.approval, api, f.record),
    /Prior timestamp blob identity changed/,
  );
  assert.equal(
    f.calls.some((c) => c.method === "PATCH"),
    false,
  );
});

import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { resolve, join } from "node:path";
import { spawnSync, execFileSync } from "node:child_process";
import { runRecordedPublisher } from "./publish-ubuntu-release.mjs";
import {
  inspectPublisherJournal,
  openPublisherJournal,
} from "./publisher-journal.mjs";
import { runMaintainerPublisher } from "./prepare-ubuntu-release.mjs";

// Deterministic remote model: every mutation has a unique observable effect.
// A repeated create/upload/ref mutation fails; recovery must use reads instead.
function recoveryRemote(f, existing) {
  const state = existing ?? {
    release: null,
    mutations: [],
    head: "1".repeat(40),
    blobs: {},
    trees: { ["2".repeat(40)]: [] },
    commits: {
      ["1".repeat(40)]: { sha: "1".repeat(40), tree: { sha: "2".repeat(40) } },
    },
  };
  const api = async (m, p, b) => {
    const prefix = "/repos/fased-ai/fased-app";
    if (m === "GET" && p === prefix + "/immutable-releases")
      return { enabled: true };
    if (m !== "GET") state.mutations.push({ method: m, path: p });
    if (p === prefix)
      return {
        id: 1426211857,
        owner: { id: 279582504 },
        full_name: "fased-ai/fased-app",
      };
    if (p.includes("/git/ref/tags/"))
      return {
        ref: "refs/tags/v0.1.0",
        object: { type: "tag", sha: f.candidate.tagObject },
      };
    if (p.includes("/git/tags/"))
      return {
        sha: f.candidate.tagObject,
        tag: "v0.1.0",
        object: { type: "commit", sha: f.candidate.source },
      };
    if (m === "POST" && p === prefix + "/releases") {
      if (state.release) throw Error("DUPLICATE release creation");
      state.release = {
        id: 11,
        draft: true,
        prerelease: false,
        immutable: false,
        tag_name: "v0.1.0",
        assets: [],
      };
      return structuredClone(state.release);
    }
    if (m === "UPLOAD") {
      const name = p.split("?name=")[1];
      if (state.release.assets.some((a) => a.name === name))
        throw Error("DUPLICATE asset upload");
      const a = {
        id: 17 + state.release.assets.length,
        name,
        size: b.length,
        state: "uploaded",
        digest: `sha256:${psha(b)}`,
      };
      state.release.assets.push(a);
      return structuredClone(a);
    }
    if (p === prefix + "/releases/11") {
      if (!state.release)
        throw Object.assign(Error("not found"), { status: 404 });
      if (m === "PATCH") {
        if (!state.release.draft) throw Error("DUPLICATE release publication");
        state.release.draft = false;
        state.release.immutable = true;
      }
      return structuredClone(state.release);
    }
    if (m === "POST" && p.endsWith("/git/blobs")) {
      const bytes = Buffer.from(b.content, "base64"),
        id = createHash("sha1")
          .update(`blob ${bytes.length}\0`)
          .update(bytes)
          .digest("hex");
      state.blobs[id] = { sha: id, encoding: "base64", content: b.content };
      return { sha: id };
    }
    if (m === "POST" && p.endsWith("/git/trees")) {
      if (state.trees["3".repeat(40)]) throw Error("DUPLICATE tree creation");
      state.trees["3".repeat(40)] = [
        ...new Map(
          [...(state.trees[b.base_tree] ?? []), ...b.tree].map((e) => [
            e.path,
            e,
          ]),
        ).values(),
      ];
      return { sha: "3".repeat(40) };
    }
    if (m === "POST" && p.endsWith("/git/commits")) {
      if (state.commits["4".repeat(40)])
        throw Error("DUPLICATE commit creation");
      state.commits["4".repeat(40)] = {
        sha: "4".repeat(40),
        tree: { sha: b.tree },
        parents: b.parents.map((sha) => ({ sha })),
        message: b.message,
      };
      return { sha: "4".repeat(40) };
    }
    if (p.includes("/git/refs/heads/")) {
      if (state.head !== "1".repeat(40)) throw Error("DUPLICATE ref update");
      state.head = b.sha;
      return { object: { type: "commit", sha: state.head } };
    }
    if (p.includes("/git/ref/heads/"))
      return {
        ref: "refs/heads/release-metadata",
        object: { type: "commit", sha: state.head },
      };
    for (const [kind, table] of [
      ["blobs", state.blobs],
      ["trees", state.trees],
      ["commits", state.commits],
    ]) {
      const marker = `/git/${kind}/`;
      if (m === "GET" && p.includes(marker)) {
        const id = p.split(marker)[1].split("?")[0],
          v = table[id];
        if (!v)
          throw Object.assign(Error("unknown original object"), {
            status: 404,
          });
        return structuredClone(
          kind === "trees" ? { sha: id, truncated: false, tree: v } : v,
        );
      }
    }
    throw Error("Unexpected fixture API route " + m + " " + p);
  };
  return { state, api };
}
function durableFixture(t, action) {
  const f = publisherFixture();
  const root = resolve(".evidence/publisher-recovery/test-state");
  mkdirSync(root, { recursive: true, mode: 0o700 });
  const parent = mkdtempSync(join(root, "case-"));
  chmodSync(parent, 0o700);
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  const input = {
    action,
    mode: "start",
    directory: join(parent, "operation"),
    ...(action !== "metadata"
      ? {
          candidate: Buffer.from(JSON.stringify(f.candidate)),
          candidateSHA256: psha(Buffer.from(JSON.stringify(f.candidate))),
        }
      : {}),
    ...(action !== "stage" ? { publication: f.raw, approval: f.approval } : {}),
  };
  const remote = recoveryRemote(f);
  if (action !== "stage")
    remote.state.release = {
      id: 11,
      tag_name: "v0.1.0",
      draft: action === "release",
      prerelease: false,
      immutable: action === "metadata",
      assets: structuredClone(f.assets),
    };
  return {
    ...f,
    parent,
    input,
    remote,
    readArtifact: async (name) =>
      Buffer.from(name.endsWith(".tar.gz") ? "archive" : "x"),
  };
}
test("durable publisher completes and reopens each action as a verified no-op", async (t) => {
  for (const action of ["stage", "release", "metadata"]) {
    const f = durableFixture(t, action),
      deps = { api: f.remote.api, readArtifact: f.readArtifact };
    const first = await runRecordedPublisher(f.input, deps);
    assert.equal(first.noOp, false);
    const mutations = f.remote.state.mutations.length;
    const before = readFileSync(join(f.input.directory, "operation.json"));
    const again = await runRecordedPublisher(
      { ...f.input, mode: "resume" },
      deps,
    );
    assert.equal(again.noOp, true);
    assert.equal(f.remote.state.mutations.length, mutations);
    assert.deepEqual(
      readFileSync(join(f.input.directory, "operation.json")),
      before,
    );
    assert.equal(inspectPublisherJournal(f.input.directory).complete, true);
  }
});
test("durable upload loss is inspected and resumes with original release/asset IDs", async (t) => {
  const f = durableFixture(t, "stage");
  let dropped = false;
  const api = async (m, p, b) => {
    const r = await f.remote.api(m, p, b);
    if (m === "UPLOAD" && !dropped) {
      dropped = true;
      throw Error("response lost after upload");
    }
    return r;
  };
  await assert.rejects(
    runRecordedPublisher(f.input, { api, readArtifact: f.readArtifact }),
  );
  const original = inspectPublisherJournal(f.input.directory);
  assert.equal(original.pending.at(-1).pending, true);
  const result = await runRecordedPublisher(
    { ...f.input, mode: "resume" },
    { api: f.remote.api, readArtifact: f.readArtifact },
  );
  assert.equal(result.releaseId, "11");
  assert.equal(result.assets[0].id, "17");
  assert.equal(
    f.remote.state.mutations.filter((v) => v.method === "UPLOAD").length,
    4,
  );
  assert.equal(inspectPublisherJournal(f.input.directory).id, original.id);
});
test("uncertain draft holds until exact observed ID is explicitly bound; never creates twice", async (t) => {
  const f = durableFixture(t, "stage");
  let dropped = false;
  const api = async (m, p, b) => {
    const r = await f.remote.api(m, p, b);
    if (m === "POST" && p.endsWith("/releases") && !dropped) {
      dropped = true;
      throw Error("draft response lost");
    }
    return r;
  };
  await assert.rejects(
    runRecordedPublisher(f.input, { api, readArtifact: f.readArtifact }),
  );
  const observed = inspectPublisherJournal(f.input.directory).pending[0];
  await assert.rejects(
    runRecordedPublisher(
      { ...f.input, mode: "resume" },
      { api: f.remote.api, readArtifact: f.readArtifact },
    ),
    /Uncertain draft/,
  );
  const result = await runRecordedPublisher(
    {
      ...f.input,
      mode: "resume",
      resolutions: { 0: { requestSha256: observed.requestSha256, id: "11" } },
    },
    { api: f.remote.api, readArtifact: f.readArtifact },
  );
  assert.equal(result.releaseId, "11");
  assert.equal(
    f.remote.state.mutations.filter((v) => v.path.endsWith("/releases")).length,
    1,
  );
});
test("uncertain public transitions recover by readback with no second PATCH", async (t) => {
  for (const action of ["release", "metadata"]) {
    const f = durableFixture(t, action);
    let dropped = false;
    const api = async (m, p, b) => {
      const r = await f.remote.api(m, p, b);
      if (m === "PATCH" && !dropped) {
        dropped = true;
        throw Error("PATCH response lost");
      }
      return r;
    };
    await assert.rejects(runRecordedPublisher(f.input, { api }));
    const result = await runRecordedPublisher(
      { ...f.input, mode: "resume" },
      { api: f.remote.api },
    );
    assert.equal(result.activation, false);
    assert.equal(
      f.remote.state.mutations.filter((v) => v.method === "PATCH").length,
      1,
    );
    assert.equal(inspectPublisherJournal(f.input.directory).complete, true);
  }
});
test("uncertain absent effects and changed inputs/IDs refuse without mutations", async (t) => {
  const f = durableFixture(t, "stage");
  const api = async (m, p, b) => {
    if (m === "UPLOAD") throw Error("delivery unknown");
    return f.remote.api(m, p, b);
  };
  await assert.rejects(
    runRecordedPublisher(f.input, { api, readArtifact: f.readArtifact }),
  );
  const writes = f.remote.state.mutations.length;
  await assert.rejects(
    runRecordedPublisher(
      { ...f.input, mode: "resume" },
      { api: f.remote.api, readArtifact: f.readArtifact },
    ),
    /unresolved\/changed/,
  );
  assert.equal(f.remote.state.mutations.length, writes);
  const changed = { ...f.candidate, source: "8".repeat(40) },
    raw = Buffer.from(JSON.stringify(changed));
  await assert.rejects(
    runRecordedPublisher(
      {
        ...f.input,
        mode: "resume",
        candidate: raw,
        candidateSHA256: psha(raw),
      },
      { api: f.remote.api, readArtifact: f.readArtifact },
    ),
    /authority changed/,
  );
  assert.equal(f.remote.state.mutations.length, writes);
});
test("publisher state is private, exclusive, and corruption-preserving", async (t) => {
  const f = durableFixture(t, "stage"),
    dir = join(f.parent, "locked");
  const j = openPublisherJournal(dir, "a".repeat(64));
  assert.throws(
    () => openPublisherJournal(dir, "a".repeat(64), { resume: true }),
    /already running/,
  );
  j.close();
  const bytes = readFileSync(join(dir, "operation.json"));
  writeFileSync(
    join(dir, "operation.json"),
    Buffer.concat([bytes, Buffer.from("corrupt")]),
  );
  const bad = readFileSync(join(dir, "operation.json"));
  assert.throws(() =>
    openPublisherJournal(dir, "a".repeat(64), { resume: true }),
  );
  assert.deepEqual(readFileSync(join(dir, "operation.json")), bad);
  const publicDir = join(f.parent, "public");
  mkdirSync(publicDir, { mode: 0o755 });
  assert.throws(
    () => openPublisherJournal(publicDir, "a".repeat(64)),
    /Private owned/,
  );
});
test("production maintainer dispatch refuses missing trust before credential access", async () => {
  await assert.rejects(
    runMaintainerPublisher(unconfigured(), { tokenFile: "/does-not-exist" }),
    /dispatch disabled/,
  );
  await assert.rejects(
    runMaintainerPublisher(rawConfig, { tokenFile: "/must-not-read" }),
    /Explicit reviewed publisher request required/,
  );
});
test("actual SIGKILL restart recovers durable upload and released OS lock with original IDs", async (t) => {
  const f = durableFixture(t, "stage");
  const descriptor = join(f.parent, "child-input.json"),
    remoteFile = join(f.parent, "remote.json");
  writeFileSync(
    descriptor,
    JSON.stringify({
      candidate: f.candidate,
      assets: f.assets,
      options: { ...f.input, candidate: undefined },
      candidateBase64: f.input.candidate.toString("base64"),
      remoteFile,
    }),
    { mode: 0o600 },
  );
  const script = `import {readFileSync,writeFileSync,openSync,fsyncSync,closeSync} from 'node:fs';import {createHash} from 'node:crypto';import {runRecordedPublisher} from ${JSON.stringify(new URL("./publish-ubuntu-release.mjs", import.meta.url).href)};
 const psha=b=>createHash('sha256').update(b).digest('hex');const f=JSON.parse(readFileSync(process.argv[1]));const model=(${recoveryRemote.toString()})(f);const api=async(m,p,b)=>{const r=await model.api(m,p,b);writeFileSync(f.remoteFile,JSON.stringify(model.state),{mode:0o600});const fd=openSync(f.remoteFile,'r');fsyncSync(fd);closeSync(fd);if(m==='UPLOAD')process.kill(process.pid,'SIGKILL');return r;};await runRecordedPublisher({...f.options,candidate:Buffer.from(f.candidateBase64,'base64')},{api,readArtifact:async(n)=>Buffer.from(n.endsWith('.tar.gz')?'archive':'x')});`;
  const result = spawnSync(
    process.execPath,
    ["--input-type=module", "-e", script, descriptor],
    { timeout: 30000, maxBuffer: 65536 },
  );
  assert.equal(result.signal, "SIGKILL", result.stderr.toString());
  assert.equal(existsSync(join(f.input.directory, "writer.lock")), true);
  const remote = recoveryRemote(f, JSON.parse(readFileSync(remoteFile)));
  const recovered = await runRecordedPublisher(
    { ...f.input, mode: "resume" },
    { api: remote.api, readArtifact: f.readArtifact },
  );
  assert.equal(recovered.releaseId, "11");
  assert.equal(recovered.assets[0].id, "17");
  assert.equal(
    remote.state.mutations.filter((v) => v.method === "UPLOAD").length,
    4,
  );
  assert.equal(inspectPublisherJournal(f.input.directory).complete, true);
});
test("uncertain metadata object responses resume by exact identity without recreating objects", async (t) => {
  for (const kind of ["blobs", "trees", "commits"]) {
    const f = durableFixture(t, "metadata");
    let dropped = false;
    const api = async (m, p, b) => {
      const r = await f.remote.api(m, p, b);
      if (m === "POST" && p.endsWith(`/git/${kind}`) && !dropped) {
        dropped = true;
        throw Error("object response lost");
      }
      return r;
    };
    await assert.rejects(runRecordedPublisher(f.input, { api }));
    const last = inspectPublisherJournal(f.input.directory).pending.at(-1),
      count = f.remote.state.mutations.length;
    const resume = { ...f.input, mode: "resume" };
    if (kind !== "blobs") {
      await assert.rejects(
        runRecordedPublisher(resume, { api: f.remote.api }),
        /explicitly inspect/,
      );
      assert.equal(f.remote.state.mutations.length, count);
      resume.resolutions = {
        [last.index]: {
          requestSha256: last.requestSha256,
          id: (kind === "trees" ? "3" : "4").repeat(40),
        },
      };
    }
    const result = await runRecordedPublisher(resume, { api: f.remote.api });
    assert.equal(result.metadataCommit, "4".repeat(40));
    assert.equal(
      f.remote.state.mutations.filter((v) => v.path.endsWith(`/git/${kind}`))
        .length,
      kind === "blobs" ? 6 : 1,
    );
  }
});
test("completed recovery refuses changed remote asset IDs and metadata head", async (t) => {
  for (const action of ["stage", "metadata"]) {
    const f = durableFixture(t, action),
      deps = { api: f.remote.api, readArtifact: f.readArtifact };
    await runRecordedPublisher(f.input, deps);
    const count = f.remote.state.mutations.length;
    if (action === "stage") f.remote.state.release.assets[0].id = 99;
    else f.remote.state.head = "8".repeat(40);
    await assert.rejects(
      runRecordedPublisher({ ...f.input, mode: "resume" }, deps),
    );
    assert.equal(f.remote.state.mutations.length, count);
  }
});
test("journal write failure stops before any remote mutation", async (t) => {
  const f = durableFixture(t, "stage");
  const binding = psha(
    JSON.stringify({
      action: "stage",
      candidate: psha(f.input.candidate),
      publication: null,
      approval: null,
    }),
  );
  const j = openPublisherJournal(f.input.directory, binding);
  j.close();
  chmodSync(f.input.directory, 0o500);
  try {
    await assert.rejects(
      runRecordedPublisher(
        { ...f.input, mode: "resume" },
        { api: f.remote.api, readArtifact: f.readArtifact },
      ),
    );
    assert.equal(f.remote.state.mutations.length, 0);
  } finally {
    chmodSync(f.input.directory, 0o700);
  }
});
// Keep CLI rejection fixtures independent of the operator's real release inputs.
function maintainerCLI(t, config, operation, request) {
  const f = durableFixture(t, "stage");
  mkdirSync(join(f.parent, "release"), { mode: 0o700 });
  writeFileSync(join(f.parent, "release/ubuntu.json"), JSON.stringify(config), {
    mode: 0o600,
  });
  if (request) {
    const folder = join(f.parent, ".evidence/ubuntu-release");
    mkdirSync(folder, { recursive: true, mode: 0o700 });
    writeFileSync(
      join(
        folder,
        operation === "publisher"
          ? "publisher-request.json"
          : "refresh-request.json",
      ),
      JSON.stringify(request),
      { mode: 0o600 },
    );
  }
  return spawnSync(
    process.execPath,
    [resolve("scripts/prepare-ubuntu-release.mjs"), operation],
    { cwd: f.parent, timeout: 10000 },
  );
}
test("maintainer CLI inspects locally and blocks publishing before request/credential reads", (t) => {
  const inspect = maintainerCLI(t, unconfigured(), "publisher-inspect");
  assert.equal(inspect.status, 0, inspect.stderr.toString());
  assert.deepEqual(
    JSON.parse(inspect.stdout).map((v) => v.action),
    ["stage", "release", "metadata"],
  );
  const missing = maintainerCLI(t, unconfigured(), "publisher");
  assert.equal(missing.status, 1);
  assert.match(
    missing.stderr.toString(),
    /Production trust missing; publisher dispatch disabled/,
  );
  const configured = maintainerCLI(t, rawConfig, "publisher", {
    tokenFile: "/must-not-read",
  });
  assert.equal(configured.status, 1);
  assert.match(
    configured.stderr.toString(),
    /Explicit reviewed publisher request required/,
  );
});

import fsBuiltin from "node:fs";
import { syncBuiltinESMExports } from "node:module";
test("publisher persists operation-directory parent before dispatch and sync failure holds", async (t) => {
  const original = fsBuiltin.fsyncSync;
  let target = null,
    saw = false,
    fail = false;
  fsBuiltin.fsyncSync = (fd) => {
    if (target && fsBuiltin.realpathSync(`/proc/self/fd/${fd}`) === target) {
      saw = true;
      if (fail) throw Error("fixture parent sync failed");
    }
    return original(fd);
  };
  syncBuiltinESMExports();
  try {
    const f = durableFixture(t, "stage");
    target = f.parent;
    await runRecordedPublisher(f.input, {
      api: async (m, p, b) => {
        if (m !== "GET") assert.equal(saw, true);
        return f.remote.api(m, p, b);
      },
      readArtifact: f.readArtifact,
    });
    assert.equal(saw, true);
    const held = durableFixture(t, "stage");
    target = held.parent;
    saw = false;
    fail = true;
    await assert.rejects(
      runRecordedPublisher(held.input, {
        api: held.remote.api,
        readArtifact: held.readArtifact,
      }),
      /parent sync failed/,
    );
    assert.equal(held.remote.state.mutations.length, 0);
    // Even the interrupted, now-existing directory must sync its parent again.
    fail = false;
    saw = false;
    await runRecordedPublisher(held.input, {
      api: async (m, p, b) => {
        if (m !== "GET") assert.equal(saw, true);
        return held.remote.api(m, p, b);
      },
      readArtifact: held.readArtifact,
    });
    assert.equal(saw, true);
  } finally {
    fsBuiltin.fsyncSync = original;
    syncBuiltinESMExports();
  }
});
import { publisherOperationDirectory } from "./prepare-ubuntu-release.mjs";
test("same-version metadata renewals retain distinct journals and identical input reuses its journal", () => {
  const first = publisherOperationDirectory(rawConfig, {
    action: "metadata",
    approval: { sha256: "a".repeat(64) },
  });
  const repeat = publisherOperationDirectory(rawConfig, {
    action: "metadata",
    approval: { sha256: "a".repeat(64) },
  });
  const renewed = publisherOperationDirectory(rawConfig, {
    action: "metadata",
    approval: { sha256: "b".repeat(64) },
  });
  assert.equal(first, repeat);
  assert.notEqual(first, renewed);
  assert.ok(first.endsWith("-" + "a".repeat(64)));
  assert.ok(renewed.endsWith("-" + "b".repeat(64)));
  assert.throws(() =>
    publisherOperationDirectory(rawConfig, {
      action: "metadata",
      approval: { sha256: "../reset" },
    }),
  );
});

// Refresh uses the same durable publisher fixture, with the current publication
// already present at the reviewed metadata head. No public network is involved.
import {
  runMetadataRefresh,
  onlineMetadataSigner,
} from "./refresh-ubuntu-metadata.mjs";
import { runMaintainerRefresh } from "./prepare-ubuntu-release.mjs";
import {
  generateKeyPairSync,
  createPublicKey,
  verify as verifySignature,
} from "node:crypto";
function refreshFixture(t) {
  const f = durableFixture(t, "metadata");
  const old = JSON.parse(f.raw),
    next = structuredClone(old);
  const meta = (v) =>
    Buffer.from(
      JSON.stringify({
        signed: {
          version: v,
          expires: new Date(Date.now() + 86400000).toISOString(),
        },
      }),
    ).toString("base64");
  delete next.files["1.snapshot.json"];
  next.files["2.snapshot.json"] = meta(2);
  next.files["timestamp.json"] = meta(2);
  next.planSha256 = "e".repeat(64);
  const tree = [];
  for (const [name, content] of Object.entries(old.files)) {
    const bytes = Buffer.from(content, "base64"),
      id = createHash("sha1")
        .update(`blob ${bytes.length}\0`)
        .update(bytes)
        .digest("hex");
    tree.push({ path: name, mode: "100644", type: "blob", sha: id });
    f.remote.state.blobs[id] = { sha: id, encoding: "base64", content };
  }
  f.remote.state.trees["2".repeat(40)] = tree;
  const seed = Buffer.from("disposable fixture public seed"),
    plan = {
      version: 1,
      due: true,
      renewSnapshot: true,
      offlineRenewalNeeded: false,
      createdAt: new Date().toISOString(),
    },
    signed = [];
  const input = {
    directory: join(f.parent, "refresh"),
    mode: "start",
    seed,
    seedSHA256: sha(seed),
    publication: f.raw,
    publicationSHA256: sha(f.raw),
    previousCommit: f.approval.previousCommit,
    verifierSHA256: "a".repeat(64),
  };
  const verify = async (op, i) => {
    if (op === "plan") return i.plan ?? plan;
    if (op === "finalize")
      return {
        plan: i.plan,
        publication: i.plan.due ? next : old,
        previousPublicationSha256: input.publicationSHA256,
        targetsUnchanged: true,
        noOp: !i.plan.due,
        published: false,
        activation: false,
      };
    return {
      role: op,
      seedSha256: input.seedSHA256,
      payload: "frozen-" + op,
      planSha256: "e".repeat(64),
    };
  };
  return {
    ...f,
    input,
    old,
    next,
    plan,
    signed,
    deps: {
      verify,
      signOnline: async (r) => {
        signed.push(r.payload);
        return [{ keyid: r.role, sig: "disposable-public-signature" }];
      },
      api: f.remote.api,
    },
  };
}
test("refresh freezes one plan, publishes only renewed metadata and resumes as no-op", async (t) => {
  const f = refreshFixture(t),
    out = await runMetadataRefresh(f.input, f.deps);
  assert.equal(out.metadataCommit, "4".repeat(40));
  assert.equal(out.noOp, false);
  assert.equal(out.activation, false);
  assert.deepEqual(out.publication.descriptor, f.old.descriptor);
  assert.equal(
    out.publication.files["1.targets.json"],
    f.old.files["1.targets.json"],
  );
  assert.deepEqual(f.signed, ["frozen-snapshot", "frozen-timestamp"]);
  assert.ok(f.remote.state.mutations.every((m) => m.path.includes("/git/")));
  const count = f.remote.state.mutations.length;
  const resumed = await runMetadataRefresh(
    { ...f.input, mode: "resume" },
    f.deps,
  );
  assert.equal(resumed.noOp, true);
  assert.equal(f.remote.state.mutations.length, count);
  assert.equal(f.signed.length, 2);
  const journal = readFileSync(
    join(f.input.directory, "operation.json"),
    "utf8",
  );
  assert.ok(!journal.includes("PRIVATE KEY"));
});
test("refresh interruption retains approvals and exact payloads; changed authority refuses", async (t) => {
  const f = refreshFixture(t);
  let failed = false;
  const deps = {
    ...f.deps,
    signOnline: async (r) => {
      if (r.role === "timestamp" && !failed) {
        failed = true;
        throw Error("fixture interruption");
      }
      return f.deps.signOnline(r);
    },
  };
  await assert.rejects(
    runMetadataRefresh(f.input, deps),
    /fixture interruption/,
  );
  assert.deepEqual(f.signed, ["frozen-snapshot"]);
  assert.equal(f.remote.state.mutations.length, 0);
  await assert.rejects(runMetadataRefresh(f.input, deps), /explicit resume/);
  await assert.rejects(
    runMetadataRefresh(
      { ...f.input, mode: "resume", previousCommit: "9".repeat(40) },
      deps,
    ),
    /changed/,
  );
  f.plan.createdAt = "2099-01-01"; // Restart must use the saved plan, not this clock.
  const result = await runMetadataRefresh({ ...f.input, mode: "resume" }, deps);
  assert.equal(result.noOp, false);
  assert.deepEqual(f.signed, ["frozen-snapshot", "frozen-timestamp"]);
});
test("uncertain refresh metadata head is read back without replay or re-signing", async (t) => {
  const f = refreshFixture(t);
  let interrupted = false;
  const deps = {
    ...f.deps,
    api: async (m, p, b) => {
      const r = await f.remote.api(m, p, b);
      if (m === "PATCH" && !interrupted) {
        interrupted = true;
        throw Error("lost ref response");
      }
      return r;
    },
  };
  await assert.rejects(runMetadataRefresh(f.input, deps), /lost ref response/);
  const count = f.remote.state.mutations.length;
  const out = await runMetadataRefresh({ ...f.input, mode: "resume" }, deps);
  assert.equal(out.metadataCommit, "4".repeat(40));
  assert.equal(f.remote.state.mutations.length, count);
  assert.equal(f.signed.length, 2);
});
test("not-due refresh signs/publishes nothing and later due work remains possible", async (t) => {
  const f = refreshFixture(t);
  f.plan.due = false;
  const deps = {
    ...f.deps,
    verify: async (op, i) => {
      const r = await f.deps.verify(op, i);
      if (op === "finalize")
        r.publication = Object.fromEntries(
          Object.entries(r.publication).reverse(),
        );
      return r;
    },
    api: () => {
      throw Error("unexpected transport");
    },
    signOnline: () => {
      throw Error("unexpected key access");
    },
  };
  assert.equal((await runMetadataRefresh(f.input, deps)).noOp, true);
  f.plan.due = true;
  assert.equal((await runMetadataRefresh(f.input, f.deps)).noOp, false);
});
test("wrong head, baseline bytes, offline expiry and lock contention stop refresh", async (t) => {
  for (const kind of ["head", "bytes", "expiry", "lock"]) {
    const f = refreshFixture(t);
    const deps = { ...f.deps };
    if (kind === "head") f.remote.state.head = "8".repeat(40);
    if (kind === "bytes")
      f.remote.state.trees["2".repeat(40)][0].sha = "8".repeat(40);
    if (kind === "expiry")
      deps.verify = async () => {
        throw Error("offline authorization expired");
      };
    let lock;
    if (kind === "lock") {
      const binding = sha(
        JSON.stringify({
          version: 1,
          seed: f.input.seedSHA256,
          publication: f.input.publicationSHA256,
          previousCommit: f.input.previousCommit,
          verifier: f.input.verifierSHA256,
        }),
      );
      lock = openPublisherJournal(f.input.directory, binding);
    }
    try {
      await assert.rejects(
        runMetadataRefresh(f.input, deps),
        /changed|expired|already running/,
      );
      assert.equal(f.signed.length, 0);
      assert.equal(f.remote.state.mutations.length, 0);
    } finally {
      lock?.close();
    }
  }
});
test("online signer opens only matching private online role and verifies exact payload", async (t) => {
  const f = refreshFixture(t),
    roles = {},
    keys = {},
    paths = {};
  for (const role of ["snapshot", "timestamp"]) {
    const { privateKey, publicKey } = generateKeyPairSync("ed25519"),
      id = sha(Buffer.from(role));
    roles[role] = { threshold: 1, keyids: [id] };
    keys[id] = {
      keyval: {
        public: Buffer.from(
          publicKey.export({ format: "jwk" }).x,
          "base64url",
        ).toString("hex"),
      },
    };
    paths[role] = join(f.parent, role + ".pem");
    writeFileSync(
      paths[role],
      privateKey.export({ type: "pkcs8", format: "pem" }),
      { mode: 0o600 },
    );
  }
  const seed = Buffer.from(
      JSON.stringify({
        signingProfile: "single-owner-v1",
        root: Buffer.from(JSON.stringify({ signed: { roles, keys } })).toString(
          "base64",
        ),
      }),
    ),
    pin = sha(seed),
    payload = Buffer.from(JSON.stringify({ _type: "timestamp" }));
  const request = {
    version: 1,
    role: "timestamp",
    seedSha256: pin,
    planSha256: "e".repeat(64),
    threshold: 1,
    keyIds: roles.timestamp.keyids,
    payload: payload.toString("base64"),
    payloadSha256: sha(payload),
  };
  const signer = onlineMetadataSigner(seed, pin, paths),
    a = await signer(request);
  assert.ok(
    verifySignature(
      null,
      payload,
      createPublicKey(readFileSync(paths.timestamp)),
      Buffer.from(a[0].sig, "hex"),
    ),
  );
  for (const patch of [
    { role: "targets" },
    { payloadSha256: "f".repeat(64) },
    { keyIds: roles.snapshot.keyids },
    { seedSha256: "f".repeat(64) },
  ])
    await assert.rejects(signer({ ...request, ...patch }), /required/);
  const wrong = onlineMetadataSigner(seed, pin, {
    ...paths,
    timestamp: paths.snapshot,
  });
  await assert.rejects(wrong(request), /differs/);
  chmodSync(paths.timestamp, 0o644);
  await assert.rejects(signer(request), /Private regular/);
});
test("refresh maintainer entry refuses unconfigured production trust before credentials", async (t) => {
  const request = { tokenFile: "/must-not-read", onlineKeyFiles: {} };
  await assert.rejects(
    runMaintainerRefresh(unconfigured(), request),
    /Production trust missing/,
  );
  await assert.rejects(
    runMaintainerRefresh(rawConfig, request),
    /Explicit pinned refresh request required/,
  );
  const missing = maintainerCLI(t, unconfigured(), "publisher-refresh");
  assert.equal(missing.status, 1);
  assert.match(missing.stderr.toString(), /refresh dispatch disabled/);
  const configured = maintainerCLI(t, rawConfig, "publisher-refresh", request);
  assert.equal(configured.status, 1);
  assert.match(
    configured.stderr.toString(),
    /Explicit pinned refresh request required/,
  );
});

test("configured refresh reads the repository seed directly and never touches credentials before verifier admission", async (t) => {
  const f = refreshFixture(t),
    c = complete();
  const folder = join(f.parent, "release/public");
  mkdirSync(folder, { recursive: true, mode: 0o700 });
  const seed = Buffer.from(
    JSON.stringify({
      signingProfile: "single-owner-v1",
      root: Buffer.from(
        JSON.stringify({ signed: { roles: {}, keys: {} } }),
      ).toString("base64"),
    }),
  );
  c.seedSha256 = sha(seed);
  writeFileSync(join(f.parent, c.seedFile), seed, { mode: 0o600 });
  const publicationFile = join(f.parent, "publication.json");
  writeFileSync(publicationFile, f.raw, { mode: 0o600 });
  const verifierFile = join(f.parent, "absent-reviewed-verifier");
  const request = {
    version: 1,
    mode: "start",
    publicationFile,
    publicationSHA256: sha(f.raw),
    previousCommit: f.approval.previousCommit,
    verifierFile,
    verifierSHA256: "a".repeat(64),
    tokenFile: join(f.parent, "must-not-read-token"),
    onlineKeyFiles: {
      snapshot: join(f.parent, "must-not-read-snapshot"),
      timestamp: join(f.parent, "must-not-read-timestamp"),
    },
  };
  const script = `import {runMaintainerRefresh} from ${JSON.stringify(new URL("./prepare-ubuntu-release.mjs", import.meta.url).href)};try{await runMaintainerRefresh(${JSON.stringify(c)},${JSON.stringify(request)});throw Error("accepted missing verifier");}catch(e){if(e.code!=="ENOENT" || e.path!==${JSON.stringify(verifierFile)})throw e;}`;
  const child = spawnSync(
    process.execPath,
    ["--input-type=module", "-e", script],
    { cwd: f.parent, encoding: "utf8" },
  );
  assert.equal(child.status, 0, child.stderr);
});

test("refresh snapshots caller authority and resolutions before any verifier await", async (t) => {
  const f = refreshFixture(t),
    old = { ...f.input };
  f.input.resolutions = {};
  const deps = {
    ...f.deps,
    verify: async (op, i) => {
      const result = await f.deps.verify(op, i);
      f.input.previousCommit = "9".repeat(40);
      f.input.directory = join(f.parent, "wrong-operation");
      f.input.resolutions["0"] = { id: "wrong" };
      return result;
    },
  };
  const out = await runMetadataRefresh(f.input, deps);
  assert.equal(out.metadataCommit, "4".repeat(40));
  assert.deepEqual(f.remote.state.commits[out.metadataCommit].parents, [
    { sha: old.previousCommit },
  ]);
  assert.ok(existsSync(join(old.directory, "operation.json")));
  assert.equal(existsSync(f.input.directory), false);
});

test("refresh never recreates a missing child publication journal on resume", async (t) => {
  const f = refreshFixture(t);
  let failed = false;
  const deps = {
    ...f.deps,
    api: async (m, p, b) => {
      const r = await f.remote.api(m, p, b);
      if (m === "PATCH" && !failed) {
        failed = true;
        throw Error("lost response");
      }
      return r;
    },
  };
  await assert.rejects(runMetadataRefresh(f.input, deps), /lost response/);
  const count = f.remote.state.mutations.length;
  fsBuiltin.unlinkSync(join(f.input.directory, "metadata/operation.json"));
  await assert.rejects(
    runMetadataRefresh({ ...f.input, mode: "resume" }, deps),
    /Missing publisher state/,
  );
  assert.equal(f.remote.state.mutations.length, count);
  assert.equal(f.signed.length, 2);
});

import {
  setupLocalSigning,
  recoverLocalSigning,
  unlockLocalSigning,
} from "./local-release-signing.mjs";
function localSigningFixture(t) {
  const root = resolve(".evidence/local-release-signing/test-state");
  mkdirSync(root, { recursive: true, mode: 0o700 });
  const parent = mkdtempSync(join(root, "case-"));
  chmodSync(parent, 0o700);
  t.after(() => rmSync(parent, { recursive: true, force: true }));
  return {
    parent,
    passphrase: Buffer.from("Disposable signing fixture phrase only"),
    input: {
      directory: join(parent, "working"),
      archiveDirectory: join(parent, "recovery"),
      mode: "start",
      keyClass: "disposable",
    },
  };
}
function localSeed(setup) {
  return Buffer.from(
    JSON.stringify({
      signingProfile: "single-owner-v1",
      root: Buffer.from(JSON.stringify(setup.initialRoot)).toString("base64"),
    }),
  );
}
function localRequest(out, seed, role = "targets", plan = "a".repeat(64)) {
  const payload = Buffer.from(
    JSON.stringify({
      _type: role,
      version: 1,
      expires: new Date(Date.now() + 86400000).toISOString(),
    }),
  );
  return {
    version: 1,
    role,
    seedSha256: sha(seed),
    planSha256: plan,
    keyIds: [out.identity.roles[role].keyid],
    threshold: 1,
    payload: payload.toString("base64"),
    payloadSha256: sha(payload),
  };
}
test("local signing encrypts four distinct keys, signs exact scopes and closes", (t) => {
  const f = localSigningFixture(t),
    out = setupLocalSigning({ ...f.input, passphrase: f.passphrase });
  assert.equal(
    new Set(Object.values(out.identity.roles).map((r) => r.keyid)).size,
    4,
  );
  assert.equal(out.published, false);
  assert.equal(out.activation, false);
  const encrypted = readFileSync(out.storeFile, "utf8");
  assert.ok(!encrypted.includes("PRIVATE KEY"));
  assert.deepEqual(readFileSync(out.storeFile), readFileSync(out.archiveFile));
  const seed = localSeed(out),
    session = unlockLocalSigning({
      storeFile: out.storeFile,
      identitySHA256: out.identitySHA256,
      keyClass: "disposable",
      passphrase: f.passphrase,
      seed,
      seedSHA256: sha(seed),
      allowedRoles: ["targets", "snapshot", "timestamp"],
    });
  for (const role of ["targets", "snapshot", "timestamp"]) {
    const req = localRequest(out, seed, role),
      a = session.sign(req);
    const pub = out.identity.roles[role].key.keyval.public;
    const key = createPublicKey({
      key: {
        kty: "OKP",
        crv: "Ed25519",
        x: Buffer.from(pub, "hex").toString("base64url"),
      },
      format: "jwk",
    });
    assert.ok(
      verifySignature(
        null,
        Buffer.from(req.payload, "base64"),
        key,
        Buffer.from(a[0].sig, "hex"),
      ),
    );
  }
  assert.throws(() => session.sign(localRequest(out, seed, "root")), /scoped/);
  assert.throws(
    () => session.sign(localRequest(out, seed, "targets", "b".repeat(64))),
    /plan changed/,
  );
  session.close();
  assert.throws(() => session.sign(localRequest(out, seed)), /closed/);
  assert.ok(
    !readFileSync(join(f.input.directory, "operation.json"), "utf8").includes(
      f.passphrase.toString(),
    ),
  );
});
test("local signing restores encrypted archives with pinned identity and harmless proof", (t) => {
  const f = localSigningFixture(t),
    out = setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
    restored = join(f.parent, "restored");
  const r = recoverLocalSigning({
    archiveFile: out.archiveFile,
    archiveSHA256: out.archiveSHA256,
    identitySHA256: out.identitySHA256,
    directory: restored,
    passphrase: f.passphrase,
    keyClass: "disposable",
    mode: "start",
  });
  assert.equal(r.keysVerified, true);
  assert.deepEqual(readFileSync(r.storeFile), readFileSync(out.storeFile));
  const seed = localSeed(out),
    s = unlockLocalSigning({
      ...r,
      passphrase: f.passphrase,
      seed,
      seedSHA256: sha(seed),
      allowedRoles: ["targets"],
    });
  assert.equal(
    s.sign(localRequest(out, seed))[0].keyid,
    out.identity.roles.targets.keyid,
  );
  s.close();
});
test("local signing rejects wrong password, pins, key class and root substitution", (t) => {
  const f = localSigningFixture(t),
    out = setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
    seed = localSeed(out);
  const base = {
    storeFile: out.storeFile,
    identitySHA256: out.identitySHA256,
    keyClass: "disposable",
    passphrase: f.passphrase,
    seed,
    seedSHA256: sha(seed),
    allowedRoles: ["targets"],
  };
  for (const patch of [
    { passphrase: Buffer.from("Definitely the wrong fixture passphrase") },
    { identitySHA256: "0".repeat(64) },
    { keyClass: "production" },
    { seedSHA256: "0".repeat(64) },
    { allowedRoles: ["root"] },
  ])
    assert.throws(() => unlockLocalSigning({ ...base, ...patch }));
  const wrong = JSON.parse(seed);
  wrong.root = Buffer.from(
    JSON.stringify({
      ...out.initialRoot,
      signed: {
        ...out.initialRoot.signed,
        roles: {
          ...out.initialRoot.signed.roles,
          targets: out.initialRoot.signed.roles.snapshot,
        },
      },
    }),
  ).toString("base64");
  const b = Buffer.from(JSON.stringify(wrong));
  assert.throws(
    () => unlockLocalSigning({ ...base, seed: b, seedSHA256: sha(b) }),
    /pinned root/,
  );
  const before = readFileSync(out.storeFile);
  const e = JSON.parse(before);
  e.tag = Buffer.alloc(16).toString("base64");
  writeFileSync(out.storeFile, Buffer.from(JSON.stringify(e) + "\n"), {
    mode: 0o600,
  });
  assert.throws(() => unlockLocalSigning(base));
  writeFileSync(out.storeFile, before);
});
test("local signing interruption resumes original keys and completed setup is no-op", (t) => {
  const f = localSigningFixture(t);
  let once = true;
  assert.throws(
    () =>
      setupLocalSigning(
        { ...f.input, passphrase: f.passphrase },
        {
          afterArchive() {
            if (once) {
              once = false;
              throw Error("fixture stop after archive");
            }
          },
        },
      ),
    /fixture stop/,
  );
  assert.throws(
    () => setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
    /explicit resume/,
  );
  const out = setupLocalSigning(
    { ...f.input, mode: "resume", passphrase: f.passphrase },
    {
      generate() {
        throw Error("must retain original keys");
      },
    },
  );
  const again = setupLocalSigning(
    { ...f.input, mode: "resume", passphrase: f.passphrase },
    {
      generate() {
        throw Error("must not generate again");
      },
    },
  );
  assert.equal(again.noOp, true);
  assert.equal(out.identitySHA256, again.identitySHA256);
  assert.equal(out.archiveSHA256, again.archiveSHA256);
});
test("local signing refuses overwrite, bad permissions and archive/path substitution", (t) => {
  const f = localSigningFixture(t),
    out = setupLocalSigning({ ...f.input, passphrase: f.passphrase });
  const req = {
    archiveFile: out.archiveFile,
    archiveSHA256: out.archiveSHA256,
    identitySHA256: out.identitySHA256,
    directory: join(f.parent, "restored"),
    passphrase: f.passphrase,
    keyClass: "disposable",
    mode: "start",
  };
  for (const patch of [
    { archiveSHA256: "0".repeat(64) },
    { identitySHA256: "0".repeat(64) },
  ])
    assert.throws(() => recoverLocalSigning({ ...req, ...patch }));
  mkdirSync(req.directory, { mode: 0o700 });
  writeFileSync(join(req.directory, "keys.enc"), "existing unique work", {
    mode: 0o600,
  });
  assert.throws(() => recoverLocalSigning(req), /Existing signing file/);
  assert.equal(
    readFileSync(join(req.directory, "keys.enc"), "utf8"),
    "existing unique work",
  );
  chmodSync(out.archiveFile, 0o644);
  assert.throws(
    () => recoverLocalSigning({ ...req, directory: join(f.parent, "another") }),
    /Private regular/,
  );
  assert.throws(
    () =>
      setupLocalSigning({
        ...f.input,
        archiveDirectory: f.input.directory,
        passphrase: f.passphrase,
      }),
    /Separate local/,
  );
});

import { localSigningSession } from "./local-release-signing.mjs";
import { runMaintainerLocalSigning } from "./prepare-ubuntu-release.mjs";
test("local signing owner entry joins setup and recovery with private prompts only", async (t) => {
  const f = localSigningFixture(t);
  let prompts = 0;
  const getPassphrase = async () => {
    prompts++;
    return Buffer.from(f.passphrase);
  };
  const out = await runMaintainerLocalSigning(
    rawConfig,
    "signer-setup",
    { version: 1, ...f.input },
    { getPassphrase },
  );
  assert.equal(prompts, 2);
  const restored = await runMaintainerLocalSigning(
    rawConfig,
    "signer-recover",
    {
      version: 1,
      keyClass: "disposable",
      mode: "start",
      directory: join(f.parent, "restored"),
      archiveFile: out.archiveFile,
      archiveSHA256: out.archiveSHA256,
      identitySHA256: out.identitySHA256,
    },
    { getPassphrase },
  );
  assert.equal(restored.keysVerified, true);
  assert.equal(prompts, 3);
  const again = await runMaintainerLocalSigning(
    rawConfig,
    "signer-recover",
    {
      version: 1,
      keyClass: "disposable",
      mode: "resume",
      directory: join(f.parent, "restored"),
      archiveFile: out.archiveFile,
      archiveSHA256: out.archiveSHA256,
      identitySHA256: out.identitySHA256,
    },
    { getPassphrase },
  );
  assert.equal(again.noOp, true);
});
test("local signing production admission and plaintext password fields refuse before prompt", async () => {
  const getPassphrase = () => {
    throw Error("must not ask for private input");
  };
  await assert.rejects(
    runMaintainerLocalSigning(
      rawConfig,
      "signer-setup",
      { version: 1, keyClass: "production", mode: "start" },
      { getPassphrase },
    ),
    /Production signing ownership approval missing/,
  );
  await assert.rejects(
    runMaintainerLocalSigning(
      rawConfig,
      "signer-setup",
      {
        version: 1,
        keyClass: "disposable",
        mode: "start",
        directory: "/not-read",
        archiveDirectory: "/not-read-2",
        passphrase: "forbidden-in-request",
      },
      { getPassphrase },
    ),
    /Pinned private/,
  );
});
test("local signing lazy unlock prompts once, revokes on close and skips not-due refresh", async (t) => {
  const f = localSigningFixture(t),
    out = setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
    seed = localSeed(out);
  let count = 0;
  const signer = localSigningSession(
    {
      storeFile: out.storeFile,
      identitySHA256: out.identitySHA256,
      keyClass: "disposable",
      seed,
      seedSHA256: sha(seed),
      allowedRoles: ["snapshot", "timestamp"],
    },
    {
      getPassphrase: async () => {
        count++;
        return Buffer.from(f.passphrase);
      },
    },
  );
  assert.equal(count, 0);
  await signer.sign(localRequest(out, seed, "snapshot"));
  await signer.sign(localRequest(out, seed, "timestamp"));
  assert.equal(count, 1);
  signer.close();
  await assert.rejects(
    signer.sign(localRequest(out, seed, "timestamp")),
    /closed/,
  );
  const inert = refreshFixture(t);
  inert.plan.due = false;
  const noUnlock = localSigningSession(
    {
      storeFile: join(f.parent, "does-not-exist"),
      identitySHA256: out.identitySHA256,
      keyClass: "disposable",
      seed,
      seedSHA256: sha(seed),
      allowedRoles: ["snapshot", "timestamp"],
    },
    {
      getPassphrase: () => {
        throw Error("not-due must not unlock");
      },
    },
  );
  try {
    assert.equal(
      (
        await runMetadataRefresh(inert.input, {
          ...inert.deps,
          signOnline: noUnlock.sign,
          api: () => {
            throw Error("not-due transport");
          },
        })
      ).noOp,
      true,
    );
  } finally {
    noUnlock.close();
  }
});

test("local signing freezes requests, serializes unlock and honors cancellation", async (t) => {
  const f = localSigningFixture(t),
    out = setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
    seed = localSeed(out);
  let release,
    count = 0;
  const scope = {
    storeFile: out.storeFile,
    identitySHA256: out.identitySHA256,
    keyClass: "disposable",
    seed,
    seedSHA256: sha(seed),
    allowedRoles: ["snapshot", "timestamp"],
  };
  const signer = localSigningSession(scope, {
    getPassphrase: () => {
      count++;
      return new Promise((resolve) => {
        release = resolve;
      });
    },
  });
  const req = localRequest(out, seed, "snapshot"),
    pending = signer.sign(req),
    other = signer.sign(localRequest(out, seed, "timestamp"));
  req.payload = "changed-after-await";
  req.planSha256 = "b".repeat(64);
  release(Buffer.from(f.passphrase));
  await Promise.all([pending, other]);
  assert.equal(count, 1);
  signer.close();
  const cancelled = localSigningSession(scope, {
    getPassphrase: () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  });
  const attempt = cancelled.sign(localRequest(out, seed, "timestamp"));
  cancelled.close();
  release(Buffer.from(f.passphrase));
  await assert.rejects(attempt, /closed/);
});

test("local signing private terminal hides passphrases and restores modes on cancel", () => {
  const module = new URL("./local-release-signing.mjs", import.meta.url).href;
  const python = String.raw`import os,pty,select,signal,subprocess,sys,termios,time,json
module=sys.argv[1]
secret=b'Disposable terminal phrase only'
results=[]
for action in ['enter','ctrl-c','sigterm','eof-before-exit']:
 pid,fd=pty.fork()
 if pid==0:
  code='import {privateSigningPassphrase} from '+json.dumps(module)+';try{const p=await privateSigningPassphrase();p.fill(0);console.log("fixture done");}catch{console.log("fixture cancelled");process.exitCode=1;}'
  if action=='eof-before-exit':
   result=subprocess.call(['node','--input-type=module','-e',code])
   for childfd in [0,1,2]:os.close(childfd)
   time.sleep(0.05)
   os._exit(result)
  os.execvp('node',['node','--input-type=module','-e',code])
 data=b'';end=time.monotonic()+10;sent=False;status=None;eof=False
 try:
  while time.monotonic()<end:
   ready,_,_=select.select([] if eof else [fd],[],[],0.1)
   if ready:
    try:
     part=os.read(fd,8192)
    except OSError:part=b''
    data+=part
    if not part:eof=True
   if not sent and b'Release passphrase:' in data:
    sent=True
    if action in ['enter','eof-before-exit']:os.write(fd,secret+b'\r')
    elif action=='ctrl-c':os.write(fd,b'\x03')
    else:os.kill(pid,signal.SIGTERM)
   done,code=os.waitpid(pid,os.WNOHANG)
   if done:status=code;break
  if status is None:
   done,code=os.waitpid(pid,os.WNOHANG)
   if done:status=code
  if status is None:raise AssertionError('private terminal child did not exit')
  if not sent or secret in data:raise AssertionError('private terminal input was exposed')
  if not termios.tcgetattr(fd)[3]&termios.ECHO:raise AssertionError('private terminal echo was not restored')
  exitcode=os.waitstatus_to_exitcode(status)
  if exitcode!=(0 if action in ['enter','eof-before-exit'] else 1):raise AssertionError('private terminal wrong outcome')
  results.append(action)
 finally:
  if status is None:
   try:os.kill(pid,signal.SIGKILL)
   except ProcessLookupError:pass
   try:os.waitpid(pid,0)
   except ChildProcessError:pass
  os.close(fd)
print(json.dumps({'privateTerminal':results}))`;
  const child = spawnSync("python3", ["-c", python, module], {
    encoding: "utf8",
    timeout: 40000,
  });
  assert.equal(child.status, 0, child.stderr);
  assert.ok(child.stdout.includes("sigterm"));
});

test("local signing recovers interrupted ciphertext link without replacing identities", (t) => {
  const f = localSigningFixture(t),
    original = fsBuiltin.linkSync;
  let fired = false;
  fsBuiltin.linkSync = (...args) => {
    const r = original(...args);
    if (!fired && args[1].endsWith(".enc")) {
      fired = true;
      throw Error("lost ciphertext link acknowledgement");
    }
    return r;
  };
  syncBuiltinESMExports();
  try {
    assert.throws(
      () => setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
      /lost ciphertext link/,
    );
  } finally {
    fsBuiltin.linkSync = original;
    syncBuiltinESMExports();
  }
  const out = setupLocalSigning(
    { ...f.input, mode: "resume", passphrase: f.passphrase },
    {
      generate() {
        throw Error("must not regenerate");
      },
    },
  );
  assert.equal(fired, true);
  assert.deepEqual(readFileSync(out.storeFile), readFileSync(out.archiveFile));
  assert.equal(
    fsBuiltin
      .readdirSync(f.input.archiveDirectory)
      .some((n) => n.startsWith(".local-signing-")),
    false,
  );
});
test("local signing recovers a classified partial ciphertext temp file", (t) => {
  const f = localSigningFixture(t),
    original = fsBuiltin.writeFileSync;
  let fired = false;
  fsBuiltin.writeFileSync = (file, data, ...rest) => {
    if (
      !fired &&
      typeof file === "number" &&
      fsBuiltin
        .readlinkSync("/proc/self/fd/" + file)
        .includes("/.local-signing-")
    ) {
      fired = true;
      original(file, Buffer.from(data).subarray(0, 10), ...rest);
      throw Error("partial ciphertext write");
    }
    return original(file, data, ...rest);
  };
  syncBuiltinESMExports();
  try {
    assert.throws(
      () => setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
      /partial ciphertext/,
    );
  } finally {
    fsBuiltin.writeFileSync = original;
    syncBuiltinESMExports();
  }
  const out = setupLocalSigning(
    { ...f.input, mode: "resume", passphrase: f.passphrase },
    {
      generate() {
        throw Error("must retain encrypted original");
      },
    },
  );
  assert.equal(fired, true);
  assert.deepEqual(readFileSync(out.storeFile), readFileSync(out.archiveFile));
});

test("local signing re-syncs complete interrupted ciphertext before linking", (t) => {
  const f = localSigningFixture(t),
    original = fsBuiltin.fsyncSync;
  let fired = false;
  fsBuiltin.fsyncSync = (fd) => {
    if (
      !fired &&
      fsBuiltin.readlinkSync("/proc/self/fd/" + fd).includes("/.local-signing-")
    ) {
      fired = true;
      throw Error("complete ciphertext before fsync");
    }
    return original(fd);
  };
  syncBuiltinESMExports();
  try {
    assert.throws(
      () => setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
      /before fsync/,
    );
  } finally {
    fsBuiltin.fsyncSync = original;
    syncBuiltinESMExports();
  }
  let fileSynced = false;
  const link = fsBuiltin.linkSync;
  fsBuiltin.fsyncSync = (fd) => {
    if (
      fsBuiltin.readlinkSync("/proc/self/fd/" + fd).includes("/.local-signing-")
    )
      fileSynced = true;
    return original(fd);
  };
  fsBuiltin.linkSync = (...args) => {
    assert.equal(
      fileSynced,
      true,
      "ciphertext inode was not synced before publication",
    );
    return link(...args);
  };
  syncBuiltinESMExports();
  try {
    const out = setupLocalSigning(
      { ...f.input, mode: "resume", passphrase: f.passphrase },
      {
        generate() {
          throw Error("must retain keys");
        },
      },
    );
    assert.deepEqual(
      readFileSync(out.storeFile),
      readFileSync(out.archiveFile),
    );
  } finally {
    fsBuiltin.fsyncSync = original;
    fsBuiltin.linkSync = link;
    syncBuiltinESMExports();
  }
});
test("local signing syncs existing recovery directory parent after interrupted mkdir", (t) => {
  const f = localSigningFixture(t),
    original = fsBuiltin.fsyncSync;
  let fired = false;
  fsBuiltin.fsyncSync = (fd) => {
    if (
      !fired &&
      fsBuiltin.readlinkSync("/proc/self/fd/" + fd) === f.parent &&
      existsSync(f.input.archiveDirectory)
    ) {
      fired = true;
      throw Error("recovery mkdir before parent sync");
    }
    return original(fd);
  };
  syncBuiltinESMExports();
  try {
    assert.throws(
      () => setupLocalSigning({ ...f.input, passphrase: f.passphrase }),
      /before parent sync/,
    );
  } finally {
    fsBuiltin.fsyncSync = original;
    syncBuiltinESMExports();
  }
  assert.ok(existsSync(f.input.archiveDirectory));
  let parents = 0;
  fsBuiltin.fsyncSync = (fd) => {
    if (fsBuiltin.readlinkSync("/proc/self/fd/" + fd) === f.parent) parents++;
    return original(fd);
  };
  syncBuiltinESMExports();
  try {
    setupLocalSigning(
      { ...f.input, mode: "resume", passphrase: f.passphrase },
      {
        generate() {
          throw Error("must retain keys");
        },
      },
    );
    assert.ok(
      parents >= 2,
      "both existing working and recovery directory names need parent sync",
    );
  } finally {
    fsBuiltin.fsyncSync = original;
    syncBuiltinESMExports();
  }
});

test("publisher checks immutable boolean before publication instead of trusting HTTP success", async () => {
  const f = publisherFixture(),
    raw = Buffer.from(JSON.stringify(f.candidate));
  let mutations = 0;
  const api = async (method, route, body) => {
    if (method !== "GET") mutations++;
    if (route.endsWith("/immutable-releases")) return { enabled: false };
    if (route.endsWith("/releases/11"))
      return {
        id: 11,
        draft: true,
        prerelease: false,
        tag_name: "v0.1.0",
        assets: f.assets,
      };
    return f.api(method, route, body);
  };
  await assert.rejects(
    releaseCandidate(raw, sha(raw), f.raw, f.approval, api, f.record),
    /immutable releases/,
  );
  assert.equal(mutations, 0);
});

import { publicAsset } from "./release-ubuntu.mjs";
test("release readback uses anonymous pinned public bytes and refuses unsafe redirects", async () => {
  const raw = Buffer.from("disposable published executable fixture"),
    identity = { size: raw.length, sha256: sha(raw) };
  const urls = [];
  const fetchFixture = async (url, options) => {
    urls.push(url);
    assert.equal(options.headers, undefined);
    assert.equal(options.redirect, "manual");
    return urls.length === 1
      ? new Response(null, {
          status: 302,
          headers: {
            location:
              "https://release-assets.githubusercontent.com/github-production-release-asset/fixture?temporary=not-retained",
          },
        })
      : new Response(raw, { status: 200 });
  };
  assert.deepEqual(
    await publicAsset(
      "0.1.4",
      "fased-install-linux-amd64",
      identity,
      fetchFixture,
    ),
    raw,
  );
  assert.equal(urls.length, 2);
  await assert.rejects(
    publicAsset(
      "0.1.4",
      "fased-install-linux-amd64",
      identity,
      async () =>
        new Response(null, {
          status: 302,
          headers: { location: "https://evil.invalid/?credential=secret" },
        }),
    ),
    /redirect refused/,
  );
  await assert.rejects(
    publicAsset(
      "0.1.4",
      "fased-install-linux-amd64",
      identity,
      async () => new Response(Buffer.from("wrong"), { status: 200 }),
    ),
    /differs/,
  );
});

import { persistLocalReleaseFile } from "./local-release-signing.mjs";
test("release resumes frozen finalization input after lost link acknowledgement without replacing bytes", (t) => {
  const f = localSigningFixture(t),
    file = join(f.parent, "finalization.json"),
    raw = Buffer.from('{"fixture":"frozen expiry and source"}\n');
  const journal = { state: {}, save() {} };
  assert.throws(() => {
    persistLocalReleaseFile(journal, file, raw);
    throw Error("lost acknowledgement");
  }, /lost acknowledgement/);
  assert.deepEqual(readFileSync(file), raw);
  persistLocalReleaseFile(journal, file, raw);
  assert.throws(
    () => persistLocalReleaseFile(journal, file, Buffer.from("changed")),
    /changed|differs/,
  );
  assert.deepEqual(readFileSync(file), raw);
});

import {
  acceptedReleasePR,
  prepareReleaseCandidate,
} from "./release-source.mjs";
test("release source requires exact reviewed main PR and refuses failed CI before mutation", async () => {
  const head = "a".repeat(40),
    pr = { number: 77, baseRefName: "main", headRefOid: head, state: "OPEN" };
  assert.throws(() => acceptedReleasePR({ ...pr, baseRefName: "other" }, head));
  assert.throws(() => acceptedReleasePR(pr, "b".repeat(40)));
  const calls = [];
  const run = (program, args) => {
    calls.push([program, ...args]);
    if (
      program === "gh" &&
      args[0] === "api" &&
      args[1] === "/repos/fased-ai/fased-app/immutable-releases"
    )
      return JSON.stringify({ enabled: true });
    if (program === "git" && args[0] === "remote")
      return "git@github-fased:fased-ai/fased-app.git";
    if (
      program === "gh" &&
      args[0] === "api" &&
      args[1] === "/repos/fased-ai/fased-app"
    )
      return JSON.stringify({
        id: 1426211857,
        owner: { id: 279582504 },
        full_name: "fased-ai/fased-app",
      });
    if (program === "git" && args[0] === "status") return "";
    if (program === "git" && args[0] === "rev-parse") return head;
    if (args[0] === "pr" && args[1] === "view") return JSON.stringify(pr);
    if (args[0] === "pr" && args[1] === "checks")
      throw Error("controlled CI failure");
    return "";
  };
  await assert.rejects(
    prepareReleaseCandidate("0.1.4", "77", { run, progress() {} }),
    /CI failure/,
  );
  assert.ok(
    !calls.some(
      (c) =>
        (c[0] === "gh" && c[1] === "pr" && c[2] === "merge") ||
        (c[0] === "git" && c[1] === "push") ||
        c.includes("tag"),
    ),
  );
});

test("release source resumes staged attempt2 and verifies its original ZIP before promotion", async (t) => {
  const f = localSigningFixture(t),
    source = "a".repeat(40),
    tree = "b".repeat(40),
    id = "123";
  const version = "0.0.777",
    zip = join(f.parent, "fixture.zip");
  execFileSync("python3", [
    "-c",
    `import zipfile,json,sys
names='fased-app-linux-amd64.tar.gz fased-app-linux-amd64.json fased-install-linux-amd64 fased-app-linux-amd64.provenance.sigstore.json verified-provenance.json expected-provenance.json provenance-policy.json first-install-seed.json install.sh build-inputs.json artifact-inventory.json signing-inputs.json fased-release.json'.split()
with zipfile.ZipFile(sys.argv[1],'w') as z:
 for name in names:
  raw=json.dumps(dict(version='0.0.777',sourceCommit='a'*40,runId='123',runAttempt='2')) if name=='signing-inputs.json' else json.dumps(dict(version=2,productVersion='0.0.777',source='a'*40,bundleSha256='b'*64,archive=dict(file='fased-app-linux-amd64.tar.gz',size=1,sha256='c'*64),installer=dict(file='fased-install-linux-amd64',size=1,sha256='d'*64),entry=dict(file='install.sh',size=1,sha256='e'*64),release=dict(buildRef='refs/heads/main',tagObject='',sourceTree='b'*40,runId='123',runAttempt='2',provenance=dict(file='fased-app-linux-amd64.provenance.sigstore.json',size=1,sha256='f'*64)))) if name=='fased-release.json' else 'disposable fixture'
  z.writestr(name,raw)
`,
    zip,
  ]);
  const archive = readFileSync(zip),
    calls = [];
  const run = (program, args, options) => {
    calls.push([program, ...args]);
    if (
      program === "gh" &&
      args[0] === "api" &&
      args[1] === "/repos/fased-ai/fased-app/immutable-releases"
    )
      return JSON.stringify({ enabled: true });
    if (program === "git" && args[0] === "remote")
      return "git@github-fased:fased-ai/fased-app.git";
    if (
      program === "gh" &&
      args[0] === "api" &&
      args[1] === "/repos/fased-ai/fased-app"
    )
      return JSON.stringify({
        id: 1426211857,
        owner: { id: 279582504 },
        full_name: "fased-ai/fased-app",
      });
    if (program === "python3") return execFileSync(program, args, options);
    if (program === "git") {
      if (args[0] === "status") return "";
      if (args[0] === "rev-parse") return args[1] === "HEAD" ? source : tree;
      if (args[0] === "ls-remote")
        return `${source}\trefs/tags/v${version}^{}\n`;
      if (args[0] === "fetch") return "";
      throw Error("unexpected Git mutation");
    }
    if (args[0] === "auth") return "";
    if (args[0] === "pr")
      return JSON.stringify({
        number: 77,
        state: "MERGED",
        baseRefName: "main",
        headRefOid: source,
        mergeCommit: { oid: source },
      });
    if (args[0] === "run" && args[1] === "list")
      return JSON.stringify([
        { databaseId: 123, headSha: source, status: "completed" },
      ]);
    if (args[0] === "run" && args[1] === "view")
      return JSON.stringify({
        conclusion: "success",
        attempt: 2,
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
    if (args[0] === "api" && args[1].endsWith("/zip")) return archive;
    if (
      args[0] === "api" &&
      args[1] === "/repos/fased-ai/fased-app/immutable-releases"
    )
      return JSON.stringify({ enabled: true });
    if (args[0] === "api")
      return JSON.stringify({
        artifacts: [
          {
            id: 456,
            name: "ubuntu-amd64-release-candidate-2",
            digest: `sha256:${sha(archive)}`,
            expired: false,
          },
        ],
      });
    throw Error("unexpected release command");
  };
  const directory = await prepareReleaseCandidate(version, "77", {
    run,
    workspace: f.parent,
    config: { ...rawConfig, version },
    verifyAcceptance: async (packet) => {
      assert.equal(packet.release.runAttempt, "2");
    },
    progress() {},
  });
  assert.equal(
    JSON.parse(readFileSync(join(directory, "signing-inputs.json"))).runId,
    id,
  );
  assert.ok(
    !calls.some(
      (c) =>
        (c[0] === "gh" && c[1] === "pr" && c[2] === "merge") ||
        (c[0] === "git" && c[1] === "push"),
    ),
  );
});

test("release source refuses a different origin before GitHub or Git mutation", async () => {
  const calls = [];
  await assert.rejects(
    prepareReleaseCandidate("0.1.4", "77", {
      run(program, args) {
        calls.push([program, ...args]);
        if (
          program === "gh" &&
          args[0] === "api" &&
          args[1] === "/repos/fased-ai/fased-app/immutable-releases"
        )
          return JSON.stringify({ enabled: true });
        if (program === "git" && args[0] === "remote")
          return "git@github.com:someone/another-project.git";
        return "";
      },
      progress() {},
    }),
    /Fixed Fased origin/,
  );
  assert.deepEqual(
    calls.map((c) => c.slice(0, 2)),
    [
      ["gh", "auth"],
      ["git", "remote"],
    ],
  );
});

test("durable release inputs support larger artifacts and recover a journaled partial temporary", (t) => {
  const f = localSigningFixture(t),
    target = join(f.parent, "artifact.zip"),
    raw = Buffer.alloc(96 * 1024, 7);
  const temp = join(f.parent, ".local-signing-" + "a".repeat(32));
  writeFileSync(temp, raw.subarray(0, 128), { mode: 0o600, flag: "wx" });
  const journal = {
    state: { files: { [target]: { sha256: sha(raw), temp } } },
    save() {},
  };
  persistLocalReleaseFile(journal, target, raw, 128 * 1024 * 1024);
  assert.deepEqual(readFileSync(target), raw);
  assert.equal(existsSync(temp), false);
});

test("release waits for dependency checks before the late registered required aggregate", async () => {
  const head = "a".repeat(40),
    checks = [];
  let merged = false;
  const run = (program, args) => {
    if (program === "git") {
      if (args[0] === "remote")
        return "git@github-fased:fased-ai/fased-app.git";
      if (args[0] === "status") return "";
      if (args[0] === "rev-parse") return head;
      if (args[0] === "fetch")
        throw Error("controlled stop after verified checks");
    }
    if (args[0] === "auth") return "";
    if (
      args[0] === "api" &&
      args[1] === "/repos/fased-ai/fased-app/immutable-releases"
    )
      return JSON.stringify({ enabled: true });
    if (args[0] === "api")
      return JSON.stringify({
        id: 1426211857,
        owner: { id: 279582504 },
        full_name: "fased-ai/fased-app",
      });
    if (args[0] === "pr" && args[1] === "view")
      return JSON.stringify({
        number: 77,
        state: merged ? "MERGED" : "OPEN",
        baseRefName: "main",
        headRefOid: head,
        mergeCommit: merged ? { oid: head } : null,
      });
    if (args[0] === "pr" && args[1] === "checks") {
      checks.push(args);
      return "";
    }
    if (args[0] === "pr" && args[1] === "merge") {
      assert.equal(checks.length, 2);
      merged = true;
      return "";
    }
    throw Error("unexpected command");
  };
  await assert.rejects(
    prepareReleaseCandidate("0.1.4", "77", { run, progress() {} }),
    /controlled stop/,
  );
  assert.equal(checks.length, 2);
  assert.equal(checks[0].includes("--required"), false);
  assert.equal(checks[1].includes("--required"), true);
});

// The active no-password authority is tested separately from retained profiles.
import "./attested-release.test.mjs";

test("staged context binds protected main, exact source and actual attempt without a tag", () => {
  const stagedEnv = {
    ...env,
    GITHUB_EVENT_NAME: "workflow_dispatch",
    GITHUB_REF: "refs/heads/main",
    GITHUB_WORKFLOW_REF: `${rawConfig.repository}/.github/workflows/release.yml@refs/heads/main`,
    FASED_RELEASE_SOURCE: source.commit,
    GITHUB_RUN_ATTEMPT: "2",
  };
  const stagedSource = { ...source, tagObject: "" };
  assert.equal(
    releaseContext(complete(), stagedEnv, stagedSource).buildRef,
    "refs/heads/main",
  );
  for (const change of [
    { GITHUB_REF: "refs/heads/topic" },
    { FASED_RELEASE_SOURCE: "a".repeat(40) },
    {
      GITHUB_WORKFLOW_REF: `${rawConfig.repository}/.github/workflows/release.yml@refs/tags/v0.1.0`,
    },
  ])
    assert.throws(() =>
      releaseContext(complete(), { ...stagedEnv, ...change }, stagedSource),
    );
});
