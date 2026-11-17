// Publication consumes acceptance from the protected workflow's authenticated
// artifact, never from a caller-supplied local PASS document.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

// Shared by the VM producer and authenticated publication consumer. Joining
// documents validates coverage; it never authenticates caller-written evidence.
export const stagedAcceptanceContract = JSON.parse(
  readFileSync(new URL("./staged-acceptance-contract.json", import.meta.url)),
);
for (const value of Object.values(stagedAcceptanceContract))
  if (Array.isArray(value)) Object.freeze(value);
Object.freeze(stagedAcceptanceContract);

const repo = "/repos/fased-ai/fased-app";
export const installedAcceptanceChecks = [
  "entry",
  "local",
  "remote",
  "ownerAuth",
  "uiUpdate",
  "cliUpdate",
  "restart",
  "alreadyCurrent",
  "preservation",
  "recovery",
  "legacyBootstrap",
  "rootEntry",
  "privateAccess",
  "logoutSignIn",
  "rebootReconnect",
  "controlledAutomatic",
  "localBeforeRemote",
  "mandatoryTailscale",
  "protectedOwnerDevice",
  "privateAdminRecovery",
  "independentConsoleRecovery",
  "publicIPv4Closure",
  "publicIPv6Closure",
  "privateHTTPSWithoutSSH",
  "passwordFreeUIAndCLI",
  "ordinaryRemoteUpdate",
];
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
const number = /^[1-9][0-9]*$/;
export function attestedInventory(c, packet, raw) {
  const names = [
    "fased-app-linux-amd64.tar.gz",
    "fased-install-linux-amd64",
    "fased-app-linux-amd64.provenance.sigstore.json",
    "install.sh",
    "fased-release.json",
  ];
  if (
    (packet.version === 2
      ? packet.release?.buildRef !== "refs/heads/main" ||
        packet.release?.tagObject !== ""
      : packet.release?.buildRef !== undefined) ||
    c.releaseAuthority !== "github-attestation-v1" ||
    ![1, 2].includes(packet.version) ||
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
export function acceptanceBinding(packet, inventory) {
  return {
    schemaVersion: 1,
    source: packet.source,
    sourceTree: packet.release.sourceTree,
    productVersion: packet.productVersion,
    runId: packet.release.runId,
    runAttempt: packet.release.runAttempt,
    platform: "ubuntu-24.04-amd64",
    artifactSetSha256: sha(JSON.stringify(inventory.assets)),
  };
}
export function validateInstalledAcceptance(receipt, packet, inventory) {
  if (receipt?.schemaVersion === 2)
    return validateJoinedAcceptance(receipt, packet, inventory);
  const binding = acceptanceBinding(packet, inventory);
  const installed =
    receipt?.upgradeRoute === "installed" &&
    receipt.predecessorPacketVersion === 2 &&
    receipt.candidatePacketVersion === 2 &&
    receipt.upgradeApplied === true &&
    receipt.uiOutcome === "applied" &&
    receipt.cliOutcome === "applied" &&
    receipt.ordinaryUpdateApply === "PASS";
  if (!installed)
    throw Error("Ordinary installed UI and CLI application evidence required");
  // A same-source compatibility reader and SSH-forwarded private listener are
  // useful historical proofs, but neither is the accepted customer journey.
  // Bind its actual released predecessor independently of candidate source.
  const released = receipt.releasedPredecessor;
  if (
    receipt.result !== "PASS" ||
    receipt.localBeforeRemote !== true ||
    receipt.remoteTransport !== "tailscale-https" ||
    receipt.sshControlSessionRequired !== false ||
    receipt.passwordPrompt !== false ||
    released?.version !== "0.1.6" ||
    released.source !== "738bd835b8c86c7cf7b3abfb2945240635f51fe7" ||
    released.artifactSetSha256 !==
      "aea00a0d9e1b7ed05a1346428cdf8cc277cbe40f93cfcd86d40fc9b45c559460"
  )
    throw Error(
      "Actual released Local-first and password-free Tailscale journey required",
    );
  const predecessor = receipt.compatibilityPredecessor;
  const legacy = receipt.legacyBootstrap;
  if (
    predecessor?.fixtureOnly !== true ||
    predecessor.published !== false ||
    predecessor.packetVersion !== 2 ||
    predecessor.productVersion !== "0.1.4" ||
    ["source", "sourceTree", "runId", "runAttempt"].some(
      (key) => predecessor[key] !== binding[key],
    ) ||
    !/^[a-f0-9]{64}$/.test(predecessor.artifactSetSha256 ?? "") ||
    predecessor.artifactSetSha256 === binding.artifactSetSha256 ||
    legacy?.result !== "PASS" ||
    legacy.productVersion !== "0.1.5" ||
    legacy.predecessorPacketVersion !== 1 ||
    legacy.candidatePacketVersion !== 2 ||
    legacy.upgradeApplied !== true ||
    !/^[a-f0-9]{64}$/.test(legacy.predecessorArtifactSetSha256 ?? "") ||
    legacy.predecessorArtifactSetSha256 === predecessor.artifactSetSha256 ||
    legacy.predecessorArtifactSetSha256 === binding.artifactSetSha256
  )
    throw Error(
      "Separate exact compatibility predecessor and public legacy bridge required",
    );
  if (
    !receipt ||
    Object.entries(binding).some(([k, v]) => receipt[k] !== v) ||
    receipt.fixtureOnly !== true ||
    receipt.mining !== false ||
    receipt.liveFeatures !== false ||
    receipt.automaticUpdates !== false ||
    receipt.automaticOutcome !== "applied" ||
    receipt.automaticPolicyRestored !== "manual" ||
    receipt.customerActivation !== false ||
    !receipt.checks ||
    Object.keys(receipt.checks).length !== installedAcceptanceChecks.length ||
    installedAcceptanceChecks.some((k) => receipt.checks[k] !== "PASS")
  ) {
    throw Error("Complete matching installed package acceptance required");
  }
  return binding;
}

function validateRemoteOperations(value) {
  const ids = [
    value.uiOperationId,
    value.cliOperationId,
    value.automaticOperationId,
  ];
  if (
    ids.some((id) => !/^[a-f0-9]{32}$/.test(id ?? "")) ||
    new Set(ids).size !== 3
  )
    throw Error("Distinct canonical UI, CLI and Automatic operations required");
}

export function completeRemoteUpgrade(
  manual,
  security,
  automatic,
  originalPredecessor,
) {
  const keys = [
    "source",
    "sourceTree",
    "productVersion",
    "runId",
    "runAttempt",
    "platform",
    "artifactSetSha256",
    "localReceiptSha256",
    "instanceId",
    "nodeIdentitySha256",
  ];
  const mapped = {
    authenticationPreservation: "authenticationPreservation",
    uiUpdate: "uiApply",
    cliUpdate: "cliApply",
    restart: "serviceRestart",
    ordinaryRemoteUpdate: "distinctEntryOperations",
  };
  const browser = automatic?.browser;
  const after = automatic?.postUpdateState;
  if (
    manual?.result !== "PASS" ||
    manual.scenario !== "manual-remote-replacement" ||
    manual.ownerAuthMode !== "existing-password-account-preserved" ||
    manual.fixtureOnly !== true ||
    ["mining", "liveFeatures", "automaticUpdates"].some(
      (key) => manual[key] !== false,
    ) ||
    manual.upgradeApplied !== true ||
    manual.updatePasswordPrompt !== false ||
    manual.uiOutcome !== "applied" ||
    manual.cliOutcome !== "applied" ||
    originalPredecessor?.version !== 2 ||
    originalPredecessor.source !== manual.predecessor?.source ||
    originalPredecessor.productVersion !== manual.predecessor?.version ||
    (manual.predecessor.packetVersion !== undefined &&
      manual.predecessor.packetVersion !== originalPredecessor.version) ||
    manual.checks?.actualPublicPredecessor !== "PASS" ||
    Object.values(mapped).some((key) => manual.checks?.[key] !== "PASS") ||
    [security, automatic].some(
      (v) =>
        !v ||
        v.result !== "PASS" ||
        keys.some((key) => v[key] !== manual[key]) ||
        v.fixtureOnly !== true ||
        ["mining", "liveFeatures", "automaticUpdates"].some(
          (key) => v[key] !== false,
        ),
    ) ||
    stagedAcceptanceContract.remoteCommon.some(
      (key) => security.checks?.[key] !== "PASS",
    ) ||
    security.checks?.alreadyCurrent !== "PASS" ||
    security.checks?.authenticationPreservation !== "PASS" ||
    security.ownerAuthMode !== "tailscale-device-v1" ||
    security.ownerEnrollment !== "existing-owner-ui-v1" ||
    security.transport !== "tailscale-https" ||
    security.normalTLS !== true ||
    security.sshControlSessionRequired !== false ||
    automatic.sshControlSessionRequired !== false ||
    automatic.ownedSSHSessionDuringApply !== false ||
    browser?.result !== "PASS" ||
    browser.instanceId !== manual.instanceId ||
    browser.version !== manual.productVersion ||
    browser.fromVersion !== manual.predecessor?.version ||
    browser.automaticOutcome !== "applied" ||
    browser.policyRestored !== "manual" ||
    browser.customerActivation !== false ||
    browser.browserClosedDuringApply !== true ||
    browser.preservation !== true ||
    browser.appliedManifestSha256 !== manual.manifestSha256 ||
    !/^[a-f0-9]{32}$/.test(browser.automaticOperation?.id ?? "") ||
    browser.automaticOperation.automatic !== true ||
    browser.automaticOperation.receipt?.phase !== "completed" ||
    browser.automaticOperation.receipt.id !== browser.automaticOperation.id ||
    browser.automaticOperation.receipt.manifestSha256 !==
      manual.manifestSha256 ||
    [manual.uiOperationId, manual.cliOperationId].includes(
      browser.automaticOperation.id,
    ) ||
    after?.instanceId !== manual.instanceId ||
    after.installedVersion !== manual.productVersion ||
    after.operation?.id !== browser.automaticOperation.id ||
    after.operation.automatic !== true ||
    after.operation.phase !== "completed" ||
    after.operation.receipt?.id !== browser.automaticOperation.id ||
    after.operation.receipt.phase !== "completed" ||
    after.operation.receipt.manifestSha256 !== manual.manifestSha256 ||
    after.activationEnabled !== false ||
    (after.policy?.mode ?? "manual") !== "manual"
  )
    throw Error(
      "Matching actual manual, security and detached Automatic upgrade proofs required",
    );
  const operations = {
    uiOperationId: manual.uiOperationId,
    cliOperationId: manual.cliOperationId,
    automaticOperationId: browser.automaticOperation.id,
  };
  validateRemoteOperations(operations);
  return {
    schemaVersion: 1,
    ...Object.fromEntries(keys.map((key) => [key, manual[key]])),
    result: "PASS",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    scenario: "upgrade",
    predecessor: {
      ...manual.predecessor,
      packetVersion: originalPredecessor.version,
      ownerAuthMode: "password-v1",
    },
    transport: security.transport,
    normalTLS: true,
    ownerAuthMode: security.ownerAuthMode,
    ownerEnrollment: security.ownerEnrollment,
    sshControlSessionRequired: false,
    passwordPrompt: false,
    upgradeApplied: true,
    uiOutcome: manual.uiOutcome,
    cliOutcome: manual.cliOutcome,
    automaticOutcome: "applied",
    automaticPolicyRestored: "manual",
    customerActivation: false,
    ...operations,
    checks: {
      ...security.checks,
      ...Object.fromEntries(
        Object.entries(mapped).map(([key, source]) => [
          key,
          manual.checks[source],
        ]),
      ),
      controlledAutomatic: "PASS",
    },
  };
}

function validateJoinedAcceptance(receipt, packet, inventory) {
  const binding = acceptanceBinding(packet, inventory);
  const leg = (value, names) => {
    if (
      !value ||
      Object.entries(binding).some(([k, v]) => value[k] !== v) ||
      value.result !== "PASS" ||
      value.fixtureOnly !== true ||
      value.mining !== false ||
      value.liveFeatures !== false ||
      value.automaticUpdates !== false ||
      names.some((name) => value.checks?.[name] !== "PASS")
    )
      throw Error("Complete matching installed package leg required");
  };
  const applied = (value) => {
    if (
      value.upgradeApplied !== true ||
      value.uiOutcome !== "applied" ||
      value.cliOutcome !== "applied" ||
      value.automaticOutcome !== "applied" ||
      value.automaticPolicyRestored !== "manual" ||
      value.customerActivation !== false
    )
      throw Error(
        "Actual UI/CLI replacement and restored Manual policy required",
      );
  };
  leg(receipt.local, stagedAcceptanceContract.local);
  applied(receipt.local);
  if (
    receipt.local.localOnly !== true ||
    receipt.local.remoteTested !== false ||
    receipt.local.releasedPredecessor?.version !== "0.1.6" ||
    receipt.local.releasedPredecessor.source !==
      "738bd835b8c86c7cf7b3abfb2945240635f51fe7" ||
    receipt.local.releasedPredecessor.artifactSetSha256 !==
      "aea00a0d9e1b7ed05a1346428cdf8cc277cbe40f93cfcd86d40fc9b45c559460"
  )
    throw Error("Actual approved released Local predecessor required");
  const localDigest = receipt.localReceiptSha256;
  if (!/^[a-f0-9]{64}$/.test(localDigest ?? ""))
    throw Error("Retained raw Local document digest required");
  for (const [value, scenario, specific] of [
    [receipt.remoteFresh, "fresh", stagedAcceptanceContract.remoteFresh],
    [receipt.remoteUpgrade, "upgrade", stagedAcceptanceContract.remoteUpgrade],
  ]) {
    leg(value, [...stagedAcceptanceContract.remoteCommon, ...specific]);
    const deviceOwner =
      value.ownerAuthMode === "tailscale-device-v1" &&
      value.checks.protectedOwnerDevice === "PASS";
    // The actual supported public predecessor has an existing password owner.
    // Its normal UI can add device ownership without replacing that account.
    const preservedEnrollment =
      scenario === "upgrade" &&
      value.ownerEnrollment === "existing-owner-ui-v1" &&
      value.predecessor?.ownerAuthMode === "password-v1" &&
      value.predecessor.version === "0.1.6" &&
      value.predecessor.source === "738bd835b8c86c7cf7b3abfb2945240635f51fe7" &&
      value.predecessor.artifactSetSha256 ===
        "aea00a0d9e1b7ed05a1346428cdf8cc277cbe40f93cfcd86d40fc9b45c559460";
    if (
      value.scenario !== scenario ||
      value.localReceiptSha256 !== localDigest ||
      value.transport !== "tailscale-https" ||
      value.normalTLS !== true ||
      !deviceOwner ||
      (scenario === "upgrade" &&
        !preservedEnrollment &&
        !(
          value.predecessor?.ownerAuthMode === "tailscale-device-v1" &&
          value.ownerEnrollment === undefined
        )) ||
      value.sshControlSessionRequired !== false ||
      value.passwordPrompt !== false ||
      !(value.instanceId !== undefined &&
      value.instanceIdentitySha256 === undefined
        ? /^[A-Za-z0-9_-]{43}$/.test(value.instanceId)
        : value.instanceId === undefined &&
          /^[a-f0-9]{64}$/.test(value.instanceIdentitySha256 ?? "")) ||
      !/^[a-f0-9]{64}$/.test(value.nodeIdentitySha256 ?? "")
    )
      throw Error(
        "Persistent Local-first private Remote owner and password-free Update required",
      );
  }
  const upgrade = receipt.remoteUpgrade;
  applied(upgrade);
  validateRemoteOperations(upgrade);
  const previous = upgrade.predecessor;
  const parts = (v) =>
    /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(v ?? "")
      ? v.split(".").map(BigInt)
      : null;
  const before = parts(previous?.version),
    after = parts(binding.productVersion);
  const first =
    before && after ? before.findIndex((v, i) => v !== after[i]) : -1;
  if (
    first < 0 ||
    before[first] >= after[first] ||
    previous.packetVersion !== 2 ||
    !/^[a-f0-9]{40}$/.test(previous.source ?? "") ||
    previous.source === binding.source ||
    !/^[a-f0-9]{64}$/.test(previous.artifactSetSha256 ?? "") ||
    previous.artifactSetSha256 === binding.artifactSetSha256
  )
    throw Error("Distinct actual older Remote predecessor required");
  if (
    receipt.result !== "PASS" ||
    receipt.contractVersion !== stagedAcceptanceContract.version ||
    receipt.fixtureOnly !== true ||
    receipt.mining !== false ||
    receipt.liveFeatures !== false ||
    receipt.automaticUpdates !== false ||
    Object.entries(binding).some(
      ([k, v]) => k !== "schemaVersion" && receipt[k] !== v,
    )
  )
    throw Error("Complete matching installed package acceptance required");
  return binding;
}

export function composeInstalledAcceptance(
  local,
  remoteFresh,
  remoteUpgrade,
  packet,
  inventory,
  localReceiptSha256,
) {
  const receipt = {
    ...acceptanceBinding(packet, inventory),
    schemaVersion: 2,
    contractVersion: stagedAcceptanceContract.version,
    result: "PASS",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    localReceiptSha256,
    local,
    remoteFresh,
    remoteUpgrade,
  };
  validateInstalledAcceptance(receipt, packet, inventory);
  return receipt;
}
function readReceiptZIP(raw, execute) {
  if (!Buffer.isBuffer(raw) || raw.length === 0 || raw.length > 2 * 1024 * 1024)
    throw Error("Bounded authenticated acceptance artifact required");
  const output = execute(
    "python3",
    [
      "-c",
      `
import hashlib,io,json,stat,sys,zipfile
with zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read())) as z:
 entries=z.infolist()
 if len(entries)!=1: raise ValueError('one acceptance document required')
 f=entries[0]
 if f.filename!='installed-acceptance.json' or f.is_dir() or stat.S_IFMT(f.external_attr>>16) not in (0,stat.S_IFREG) or not 0<f.file_size<=1048576: raise ValueError('invalid acceptance entry')
 raw=z.read(f)
 def unique(pairs):
  value={}
  for k,v in pairs:
   if k in value: raise ValueError('duplicate acceptance field')
   value[k]=v
  return value
 value=json.loads(raw,object_pairs_hook=unique)
 print(json.dumps({'receipt':value,'sha256':hashlib.sha256(raw).hexdigest()},separators=(',',':')))
`,
    ],
    {
      input: raw,
      encoding: "utf8",
      maxBuffer: 2 * 1024 * 1024,
      stdio: ["pipe", "pipe", "pipe"],
      timeout: 10000,
    },
  );
  return JSON.parse(output);
}

function protectedCandidateRun(run, packet, inventory) {
  return (
    String(run.id) === packet.release.runId &&
    String(run.run_attempt) === packet.release.runAttempt &&
    run.head_sha === packet.source &&
    run.head_branch === "main" &&
    run.event === "workflow_dispatch" &&
    run.path === ".github/workflows/release.yml" &&
    run.status === "completed" &&
    ["success", "failure"].includes(run.conclusion) &&
    String(run.repository?.id) === inventory.repositoryId &&
    String(run.repository?.owner?.id) === inventory.ownerId
  );
}

async function verifiedProducer(packet, inventory, api, observed = {}) {
  const run =
    observed.run ??
    (await api(
      "GET",
      `${repo}/actions/runs/${packet.release.runId}/attempts/${packet.release.runAttempt}`,
    ));
  if (!protectedCandidateRun(run, packet, inventory))
    throw Error("Exact protected candidate producer required");
  const jobs =
    observed.jobs ??
    (await api(
      "GET",
      `${repo}/actions/runs/${packet.release.runId}/attempts/${packet.release.runAttempt}/jobs`,
    ));
  const producer = jobs.jobs?.filter((j) => j.name === "ubuntu-amd64");
  if (
    producer?.length !== 1 ||
    producer[0].status !== "completed" ||
    producer[0].conclusion !== "success" ||
    !producer[0].steps?.some(
      (s) =>
        s.name ===
          "Verify original provenance offline and stage public signing inputs" &&
        s.conclusion === "success",
    )
  )
    throw Error(
      "Original attested build must have succeeded; no rebuild is permitted",
    );
  return run;
}

export async function verifyCandidateReplay(
  config,
  directory,
  {
    runId,
    attempt,
    api,
    read = readFileSync,
    downloadArtifact,
    execute = execFileSync,
  },
) {
  if (!number.test(runId ?? "") || !number.test(attempt ?? ""))
    throw Error("Exact original candidate run and attempt required");
  if (downloadArtifact) {
    const originalAPI = api,
      cache = new Map();
    api = async (method, url) => {
      if (method !== "GET") throw Error("Replay acquisition is read-only");
      if (!cache.has(url)) cache.set(url, await originalAPI(method, url));
      return cache.get(url);
    };
    const run = await api(
      "GET",
      `${repo}/actions/runs/${runId}/attempts/${attempt}`,
    );
    if (!/^[a-f0-9]{40}$/.test(run.head_sha ?? ""))
      throw Error("Original candidate source required");
    await verifiedProducer(
      { source: run.head_sha, release: { runId, runAttempt: attempt } },
      config,
      api,
    );
    const listed = await api("GET", `${repo}/actions/runs/${runId}/artifacts`);
    const artifacts = listed.artifacts?.filter(
      (a) =>
        a.name === `ubuntu-amd64-release-candidate-${attempt}` && !a.expired,
    );
    if (
      artifacts?.length !== 1 ||
      !number.test(String(artifacts[0].id)) ||
      !/^sha256:[a-f0-9]{64}$/.test(artifacts[0].digest ?? "") ||
      String(artifacts[0].workflow_run?.id) !== runId ||
      artifacts[0].workflow_run?.head_sha !== run.head_sha
    )
      throw Error("Exact original candidate artifact required for replay");
    const zip = await downloadArtifact(String(artifacts[0].id));
    if (
      !Buffer.isBuffer(zip) ||
      zip.length === 0 ||
      zip.length > 128 * 1024 * 1024 ||
      `sha256:${sha(zip)}` !== artifacts[0].digest
    )
      throw Error("Original replay ZIP digest differs");
    execute(
      "python3",
      [
        "-c",
        `
import io,pathlib,stat,sys,zipfile
names=set('fased-app-linux-amd64.tar.gz fased-app-linux-amd64.json fased-install-linux-amd64 fased-app-linux-amd64.provenance.sigstore.json verified-provenance.json expected-provenance.json provenance-policy.json first-install-seed.json install.sh build-inputs.json artifact-inventory.json signing-inputs.json fased-release.json'.split())
with zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read())) as z:
 entries=z.infolist();seen=set();total=0
 if len(entries)!=len(names):raise ValueError('exact original public candidate entries required')
 for f in entries:
  if f.filename not in names or f.filename in seen or f.is_dir() or stat.S_IFMT(f.external_attr>>16) not in (0,stat.S_IFREG):raise ValueError('invalid original candidate entry')
  seen.add(f.filename);total+=f.file_size
  if not 0<f.file_size<=128*1024*1024 or total>256*1024*1024:raise ValueError('bounded candidate required')
 out=pathlib.Path(sys.argv[1]);out.mkdir(mode=0o700)
 for f in entries:
  target=out/f.filename
  with target.open('xb') as stream:stream.write(z.read(f))
  target.chmod(0o600)
`,
        directory,
      ],
      {
        input: zip,
        encoding: "utf8",
        stdio: ["pipe", "pipe", "pipe"],
        timeout: 30000,
        maxBuffer: 1024 * 1024,
      },
    );
  }
  const raw = read(`${directory}/fased-release.json`),
    packet = JSON.parse(raw);
  if (
    packet.version !== 2 ||
    packet.release?.runId !== runId ||
    packet.release?.runAttempt !== attempt
  )
    throw Error("Replay cannot rebind candidate source, run or attempt");
  const inventory = attestedInventory(config, packet, raw);
  await verifiedProducer(packet, inventory, api);
  const listed = await api("GET", `${repo}/actions/runs/${runId}/artifacts`);
  const artifacts = listed.artifacts?.filter(
    (a) => a.name === `ubuntu-amd64-release-candidate-${attempt}` && !a.expired,
  );
  if (
    artifacts?.length !== 1 ||
    !number.test(String(artifacts[0].id)) ||
    !/^sha256:[a-f0-9]{64}$/.test(artifacts[0].digest ?? "") ||
    String(artifacts[0].workflow_run?.id) !== runId ||
    artifacts[0].workflow_run?.head_sha !== packet.source
  )
    throw Error("Exact original candidate artifact required for replay");
  for (const asset of inventory.assets) {
    const bytes = read(`${directory}/${asset.name}`);
    if (bytes.length !== asset.size || sha(bytes) !== asset.sha256)
      throw Error("Original replay candidate asset differs: " + asset.name);
  }
  return {
    ...acceptanceBinding(packet, inventory),
    candidateArtifactId: String(artifacts[0].id),
    candidateArtifactDigest: artifacts[0].digest,
    buildPerformed: false,
  };
}

async function readCIInstalledAcceptance(
  packet,
  inventory,
  api,
  {
    downloadArtifact = (id) =>
      execFileSync("gh", ["api", `${repo}/actions/artifacts/${id}/zip`], {
        encoding: "buffer",
        maxBuffer: 2 * 1024 * 1024,
        timeout: 60000,
        stdio: ["ignore", "pipe", "pipe"],
      }),
    execute = execFileSync,
  } = {},
) {
  const id = packet.release.runId,
    attempt = packet.release.runAttempt;
  if (
    packet.version !== 2 ||
    packet.release.buildRef !== "refs/heads/main" ||
    !number.test(id) ||
    !number.test(attempt)
  )
    throw Error("Staged main-bound candidate acceptance required");
  const run = await api(
    "GET",
    `${repo}/actions/runs/${id}/attempts/${attempt}`,
  );
  if (!protectedCandidateRun(run, packet, inventory))
    throw Error("Successful exact protected candidate workflow required");
  const originalJobs = await api(
    "GET",
    `${repo}/actions/runs/${id}/attempts/${attempt}/jobs`,
  );
  const originalAcceptance = originalJobs.jobs?.filter(
    (j) => j.name === "installed-acceptance",
  );
  // A package-only run can succeed while its acceptance job is skipped. It
  // still needs an independently authenticated acceptance of the same bytes.
  const replay =
    run.conclusion === "failure" ||
    (originalAcceptance?.length === 1 &&
      originalAcceptance[0].status === "completed" &&
      originalAcceptance[0].conclusion === "skipped");
  let acceptanceRun = run;
  if (replay) {
    await verifiedProducer(packet, inventory, api, {
      run,
      jobs: originalJobs,
    });
    const name = `ubuntu-amd64-installed-acceptance-replay-${id}-${attempt}`;
    const listed = await api(
      "GET",
      `${repo}/actions/artifacts?name=${name}&per_page=100`,
    );
    const matching = listed.artifacts?.filter(
      (a) => a.name === name && !a.expired,
    );
    if (
      !Array.isArray(listed.artifacts) ||
      listed.artifacts.length >= 100 ||
      !matching?.length
    )
      throw Error("One retained same-artifact acceptance replay required");
    const accepted = [];
    for (const artifact of matching) {
      if (
        !number.test(String(artifact.workflow_run?.id)) ||
        String(artifact.workflow_run.id) === id
      )
        throw Error("Independent protected acceptance run required");
      const candidate = await api(
        "GET",
        `${repo}/actions/runs/${artifact.workflow_run.id}`,
      );
      if (
        candidate.status === "completed" &&
        ["failure", "cancelled", "timed_out"].includes(candidate.conclusion)
      )
        continue;
      if (
        String(candidate.id) !== String(artifact.workflow_run.id) ||
        candidate.run_attempt !== 1 ||
        candidate.head_sha !== artifact.workflow_run.head_sha ||
        !/^[a-f0-9]{40}$/.test(candidate.head_sha ?? "") ||
        candidate.head_branch !== "main" ||
        candidate.event !== "workflow_dispatch" ||
        candidate.path !== ".github/workflows/release.yml" ||
        candidate.status !== "completed" ||
        candidate.conclusion !== "success" ||
        String(candidate.repository?.id) !== inventory.repositoryId ||
        String(candidate.repository?.owner?.id) !== inventory.ownerId
      )
        throw Error("Successful protected acceptance-only replay required");
      accepted.push(candidate);
    }
    if (accepted.length !== 1)
      throw Error("One retained same-artifact acceptance replay required");
    acceptanceRun = accepted[0];
  }
  const result = replay
    ? await api(
        "GET",
        `${repo}/actions/runs/${acceptanceRun.id}/attempts/${acceptanceRun.run_attempt}/jobs`,
      )
    : originalJobs;
  const jobs = result.jobs?.filter((j) => j.name === "installed-acceptance");
  if (
    jobs?.length !== 1 ||
    jobs[0].status !== "completed" ||
    jobs[0].conclusion !== "success"
  )
    throw Error("Installed acceptance job did not complete");
  if (replay) {
    const producer = result.jobs?.filter((j) => j.name === "ubuntu-amd64");
    if (producer?.length !== 1 || producer[0].conclusion !== "skipped")
      throw Error("Acceptance replay must not perform another build");
  }
  const listed = await api(
    "GET",
    `${repo}/actions/runs/${acceptanceRun.id}/artifacts`,
  );
  const artifacts = listed.artifacts?.filter(
    (a) =>
      a.name ===
        (replay
          ? `ubuntu-amd64-installed-acceptance-replay-${id}-${attempt}`
          : `ubuntu-amd64-installed-acceptance-${attempt}`) && !a.expired,
  );
  if (
    artifacts?.length !== 1 ||
    !number.test(String(artifacts[0].id)) ||
    !/^sha256:[a-f0-9]{64}$/.test(artifacts[0].digest) ||
    String(artifacts[0].workflow_run?.id) !== String(acceptanceRun.id) ||
    artifacts[0].workflow_run?.head_sha !== acceptanceRun.head_sha
  )
    throw Error("Exact retained CI acceptance artifact required");
  const raw = await downloadArtifact(String(artifacts[0].id));
  if (`sha256:${sha(raw)}` !== artifacts[0].digest)
    throw Error("CI acceptance artifact digest differs");
  const proof = readReceiptZIP(raw, execute);
  return {
    ...proof,
    artifactId: String(artifacts[0].id),
    artifactDigest: artifacts[0].digest,
  };
}

const reviewPrefix = "FASED_REMOTE_ACCEPTANCE_V1\n";
const reviewOwner = { login: "fcode-ai", id: "155309118" };
export function validateInstalledAgent(value, packet, inventory, localSha256) {
  if (
    !value ||
    Object.entries(acceptanceBinding(packet, inventory)).some(
      ([key, expected]) => value[key] !== expected,
    ) ||
    value.localReceiptSha256 !== localSha256 ||
    !/^[a-f0-9]{64}$/.test(localSha256 ?? "") ||
    value.mode !== "installed-provider" ||
    !["local", "remote"].includes(value.profile) ||
    value.result !== "PASS" ||
    value.fixtureOnly !== true ||
    value.sandboxed !== true ||
    value.shippingArtifactUnmodified !== true ||
    value.testCodeOverlay !== false ||
    value.realProviderCalls !== false ||
    ["mining", "liveFeatures", "automaticUpdates"].some(
      (key) => value[key] !== false,
    ) ||
    !/^[a-f0-9]{64}$/.test(value.engineSha256 ?? "") ||
    !/^[a-f0-9]{64}$/.test(value.bundleSha256 ?? "") ||
    value.bundleSha256 !== packet.bundleSha256 ||
    typeof value.bundleManifest !== "string" ||
    value.bundleManifest.length > 65536 ||
    sha(value.bundleManifest) !== packet.bundleSha256 ||
    !/^[a-f0-9]{64}$/.test(value.instanceIdentitySha256 ?? "") ||
    !/^[a-f0-9]{64}$/.test(value.evidenceSha256 ?? "") ||
    stagedAcceptanceContract.installedAgent.some(
      (key) => value.checks?.[key] !== "PASS",
    )
  )
    throw Error(
      "Complete unmodified installed-provider journey bound to package and Local proof required",
    );
  const manifest = JSON.parse(value.bundleManifest);
  if (manifest.files?.["fased-app"]?.sha256 !== value.engineSha256)
    throw Error(
      "Installed agent engine differs from authenticated bundle manifest",
    );
}
function publicInstalledAgent(value) {
  return {
    ...Object.fromEntries(
      [
        "schemaVersion",
        "source",
        "sourceTree",
        "productVersion",
        "runId",
        "runAttempt",
        "platform",
        "artifactSetSha256",
        "localReceiptSha256",
        "mode",
        "profile",
        "result",
        "fixtureOnly",
        "sandboxed",
        "shippingArtifactUnmodified",
        "testCodeOverlay",
        "realProviderCalls",
        "mining",
        "liveFeatures",
        "automaticUpdates",
        "engineSha256",
        "bundleSha256",
        "bundleManifest",
        "instanceIdentitySha256",
        "evidenceSha256",
      ].map((key) => [key, value[key]]),
    ),
    checks: Object.fromEntries(
      stagedAcceptanceContract.installedAgent.map((key) => [
        key,
        value.checks[key],
      ]),
    ),
  };
}
function validateAgentInstance(agent, legs) {
  if (
    agent.profile === "remote" &&
    !legs.some(
      (leg) =>
        (leg.instanceIdentitySha256 ?? sha(leg.instanceId)) ===
        agent.instanceIdentitySha256,
    )
  )
    throw Error("Installed agent must use the accepted Remote instance");
}
function publicRemoteLeg(value) {
  // Runtime receipts can contain private links and diagnostics. Publish only
  // validated typed evidence; hash the instance identity instead of exposing it.
  const fields = [
    "schemaVersion",
    "source",
    "sourceTree",
    "productVersion",
    "runId",
    "runAttempt",
    "platform",
    "artifactSetSha256",
    "result",
    "fixtureOnly",
    "mining",
    "liveFeatures",
    "automaticUpdates",
    "scenario",
    "localReceiptSha256",
    "transport",
    "normalTLS",
    "ownerAuthMode",
    "ownerEnrollment",
    "sshControlSessionRequired",
    "passwordPrompt",
    "nodeIdentitySha256",
    ...(value.scenario === "upgrade"
      ? [
          "upgradeApplied",
          "uiOutcome",
          "cliOutcome",
          "automaticOutcome",
          "automaticPolicyRestored",
          "customerActivation",
          "uiOperationId",
          "cliOperationId",
          "automaticOperationId",
        ]
      : []),
  ];
  const projected = Object.fromEntries(
    fields.filter((k) => Object.hasOwn(value, k)).map((k) => [k, value[k]]),
  );
  projected.instanceIdentitySha256 =
    value.instanceIdentitySha256 ?? sha(value.instanceId);
  projected.checks = Object.fromEntries(
    [
      ...stagedAcceptanceContract.remoteCommon,
      ...stagedAcceptanceContract[
        value.scenario === "fresh" ? "remoteFresh" : "remoteUpgrade"
      ],
    ].map((k) => [k, value.checks[k]]),
  );
  if (value.scenario === "upgrade")
    projected.predecessor = Object.fromEntries(
      [
        "version",
        "source",
        "artifactSetSha256",
        "packetVersion",
        "ownerAuthMode",
      ].map((k) => [k, value.predecessor[k]]),
    );
  return projected;
}
function reviewedRemote(comment, packet, inventory, proof) {
  if (
    String(comment.user?.id) !== reviewOwner.id ||
    comment.user.login !== reviewOwner.login ||
    comment.commit_id !== packet.source ||
    !number.test(String(comment.id)) ||
    !comment.created_at ||
    comment.created_at !== comment.updated_at ||
    comment.path != null ||
    typeof comment.body !== "string" ||
    comment.body.length > 60000 ||
    !comment.body.startsWith(reviewPrefix)
  )
    throw Error("Unmodified package-bound fcode-ai Remote review required");
  const text = comment.body.slice(reviewPrefix.length),
    review = JSON.parse(text);
  if (
    JSON.stringify(review) !== text ||
    review.version !== 2 ||
    JSON.stringify(review.binding) !==
      JSON.stringify(acceptanceBinding(packet, inventory)) ||
    review.localReceiptSha256 !== proof.sha256 ||
    review.localCIArtifactDigest !== proof.artifactDigest
  )
    throw Error(
      "Remote review differs from authenticated Local/package evidence",
    );
  validateInstalledAgent(
    review.installedAgent,
    packet,
    inventory,
    proof.sha256,
  );
  if (
    JSON.stringify(review.installedAgent) !==
    JSON.stringify(publicInstalledAgent(review.installedAgent))
  )
    throw Error(
      "Installed agent review must contain only public acceptance fields",
    );
  if (
    [review.remoteFresh, review.remoteUpgrade].some(
      (value) =>
        !value ||
        JSON.stringify(value) !== JSON.stringify(publicRemoteLeg(value)),
    )
  )
    throw Error("Remote review must contain only public acceptance fields");
  // Local comes exclusively from authenticated CI, never from this comment.
  const receipt = composeInstalledAcceptance(
    proof.receipt,
    review.remoteFresh,
    review.remoteUpgrade,
    packet,
    inventory,
    proof.sha256,
  );
  validateAgentInstance(review.installedAgent, [
    review.remoteFresh,
    review.remoteUpgrade,
  ]);
  return {
    receipt,
    remoteReviewCommentId: String(comment.id),
    remoteReviewSha256: sha(comment.body),
    installedAgentEvidenceSha256: review.installedAgent.evidenceSha256,
  };
}
async function remoteReviews(packet, api) {
  const comments = await api(
    "GET",
    `${repo}/commits/${packet.source}/comments?per_page=100`,
  );
  if (!Array.isArray(comments) || comments.length >= 100)
    throw Error("Bounded complete Remote review listing required");
  return comments.filter((c) => c.body?.startsWith(reviewPrefix));
}
export async function verifyInstalledAcceptance(
  packet,
  inventory,
  api,
  options = {},
) {
  const proof = await readCIInstalledAcceptance(
    packet,
    inventory,
    api,
    options,
  );
  let review = {};
  if (proof.receipt.localOnly === true) {
    const comments = await remoteReviews(packet, api);
    if (comments.length !== 1)
      throw Error("One reviewed Remote acceptance is required");
    review = reviewedRemote(comments[0], packet, inventory, proof);
  } else {
    validateInstalledAcceptance(proof.receipt, packet, inventory);
    validateRemoteOperations(proof.receipt.remoteUpgrade ?? proof.receipt);
    validateInstalledAgent(
      proof.receipt.installedAgent,
      packet,
      inventory,
      proof.receipt.localReceiptSha256,
    );
    if (proof.receipt.installedAgent.profile === "remote") {
      if (proof.receipt.schemaVersion !== 2)
        throw Error(
          "Structured Remote instance evidence required for installed agent",
        );
      validateAgentInstance(proof.receipt.installedAgent, [
        proof.receipt.remoteFresh,
        proof.receipt.remoteUpgrade,
      ]);
    }
    review.installedAgentEvidenceSha256 =
      proof.receipt.installedAgent.evidenceSha256;
  }
  return {
    ...acceptanceBinding(packet, inventory),
    artifactId: proof.artifactId,
    artifactDigest: proof.artifactDigest,
    installedAgentEvidenceSha256: review.installedAgentEvidenceSha256,
    ...(review.remoteReviewCommentId
      ? {
          remoteReviewCommentId: review.remoteReviewCommentId,
          remoteReviewSha256: review.remoteReviewSha256,
          contractVersion: stagedAcceptanceContract.version,
        }
      : {}),
  };
}

// Invoked only after the real harness journey and independent owner review.
// No release/tag/metadata mutation occurs here; GitHub is the review authority.
export async function recordRemoteAcceptance(
  packet,
  inventory,
  api,
  remoteFresh,
  remoteUpgrade,
  options = {},
) {
  const proof = await readCIInstalledAcceptance(
    packet,
    inventory,
    api,
    options,
  );
  if (proof.receipt.localOnly !== true)
    throw Error("Separate authenticated Local proof required");
  composeInstalledAcceptance(
    proof.receipt,
    remoteFresh,
    remoteUpgrade,
    packet,
    inventory,
    proof.sha256,
  );
  validateInstalledAgent(
    options.installedAgent,
    packet,
    inventory,
    proof.sha256,
  );
  validateAgentInstance(options.installedAgent, [remoteFresh, remoteUpgrade]);
  const actor = await api("GET", "/user");
  if (String(actor.id) !== reviewOwner.id || actor.login !== reviewOwner.login)
    throw Error("Only the approved GitHub owner may record Remote acceptance");
  const body =
    reviewPrefix +
    JSON.stringify({
      version: 2,
      binding: acceptanceBinding(packet, inventory),
      localReceiptSha256: proof.sha256,
      localCIArtifactDigest: proof.artifactDigest,
      remoteFresh: publicRemoteLeg(remoteFresh),
      remoteUpgrade: publicRemoteLeg(remoteUpgrade),
      installedAgent: publicInstalledAgent(options.installedAgent),
    });
  if (body.length > 60000) throw Error("Bounded Remote evidence required");
  const existing = await remoteReviews(packet, api);
  if (existing.length) {
    if (existing.length !== 1 || existing[0].body !== body)
      throw Error(
        "Conflicting Remote review exists; preserve original evidence",
      );
    return reviewedRemote(existing[0], packet, inventory, proof);
  }
  const posted = await api(
    "POST",
    `${repo}/commits/${packet.source}/comments`,
    { body },
  );
  const retained = await api("GET", `${repo}/comments/${posted.id}`);
  if (retained.body !== body)
    throw Error("Remote review readback differs; reconcile before retry");
  return reviewedRemote(retained, packet, inventory, proof);
}
