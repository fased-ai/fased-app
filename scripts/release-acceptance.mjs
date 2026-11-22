// Publication consumes protected CI artifacts or repository-owner-reviewed
// real-host receipts. A caller-supplied PASS file never authorizes publication.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

// Shared by the VM producer and authenticated publication consumer. Joining
// documents validates coverage; it never authenticates caller-written evidence.
export const stagedAcceptanceContract = JSON.parse(
  readFileSync(new URL("./staged-acceptance-contract.json", import.meta.url)),
);
for (const value of Object.values(stagedAcceptanceContract))
  if (Array.isArray(value) || (value && typeof value === "object"))
    Object.freeze(value);
Object.freeze(stagedAcceptanceContract);

export function matchesReleasedPredecessor(
  actual,
  expected = stagedAcceptanceContract.releasedPredecessor,
) {
  return (
    Boolean(actual && expected) &&
    actual.version === expected.version &&
    actual.source === expected.source &&
    actual.artifactSetSha256 === expected.artifactSetSha256
  );
}

// Match canonical Tailscale Serve origins, including isolated HTTPS listeners.
// Compare the original string so URL normalization cannot erase an input port.
export function isPrivateTailscaleOrigin(value) {
  const match =
    /^https:\/\/(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.){2}ts\.net(?::([1-9][0-9]{3,4}))?$/.exec(
      value,
    );
  if (!match || match[0] !== value) return false;
  const port = Number(match[1]);
  return match[1] === undefined || (port >= 1024 && port <= 65535);
}

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
const digest = /^[a-f0-9]{64}$/;

export function isMainBoundCandidate(packet) {
  return (
    [2, 4].includes(packet.version) &&
    packet.release?.buildRef === "refs/heads/main" &&
    packet.release.tagObject === ""
  );
}

export function attestedInventory(c, packet, raw) {
  const names = [
    "fased-app-linux-amd64.tar.gz",
    "fased-install-linux-amd64",
    "fased-app-linux-amd64.provenance.sigstore.json",
    "install.sh",
    "fased-release.json",
  ];
  if (
    (packet.version === 1
      ? packet.release?.buildRef !== undefined
      : !isMainBoundCandidate(packet)) ||
    c.releaseAuthority !== "github-attestation-v1" ||
    packet.productVersion !== c.version ||
    !/^[a-f0-9]{40}$/.test(packet.source)
  )
    throw Error("Attested release authority/version required");
  if (
    packet.version === 4 &&
    (!digest.test(packet.bundleSha256 ?? "") ||
      !digest.test(packet.release.installerSha256 ?? "") ||
      packet.release.installerSha256 !== packet.installer?.sha256 ||
      packet.deb !== undefined ||
      packet.rpm !== undefined)
  )
    throw Error("Exact format-4 core and signer bundle identity required");
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
        a?.file !== names[i] ||
        !Number.isSafeInteger(a.size) ||
        a.size <= 0 ||
        a.size > 128 * 1024 * 1024 ||
        !digest.test(a.sha256),
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
    packet.version === 2 &&
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
    !matchesReleasedPredecessor(released)
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

function isManualOnlyFormat4(value, packetVersion) {
  return (
    packetVersion === 4 &&
    value?.automaticOutcome === "held-manual-only" &&
    value.customerActivation === false &&
    value.automaticUpdates === false &&
    value.automaticPolicyRestored === "manual"
  );
}

function validateUpdateOperations(value, manualOnly = false) {
  if (manualOnly) {
    const ids = [value.uiOperationId, value.cliOperationId];
    if (
      value.automaticOperationId !== undefined ||
      ids.some((id) => !/^[a-f0-9]{32}$/.test(id ?? "")) ||
      new Set(ids).size !== 2
    )
      throw Error(
        "Distinct canonical UI and CLI operations required for manual-only release",
      );
    return;
  }
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

function validRemoteFormat4(value, targetPacketSha256) {
  const previous = value?.predecessor;
  const fixture = value?.automaticPredecessor;
  const auth = value?.deviceAuthentication;
  const manualOnly = isManualOnlyFormat4(value, 4);
  if (
    !/^[a-f0-9]{40}$/.test(value?.source ?? "") ||
    !digest.test(targetPacketSha256 ?? "") ||
    !digest.test(value?.localReceiptSha256 ?? "") ||
    previous?.packetVersion !== 4 ||
    previous.ownerAuthMode !== "tailscale-device-v1" ||
    !olderVersion(previous.version, value.productVersion) ||
    !/^[a-f0-9]{40}$/.test(previous.source ?? "") ||
    previous.source === value.source ||
    !digest.test(previous.artifactSetSha256 ?? "") ||
    previous.artifactSetSha256 === value.artifactSetSha256 ||
    !digest.test(previous.packetSha256 ?? "") ||
    previous.packetSha256 === targetPacketSha256 ||
    !digest.test(auth?.before ?? "") ||
    auth.ui !== auth.before ||
    auth.cli !== auth.before
  )
    return false;
  if (manualOnly) {
    return fixture === undefined && auth.automatic === undefined;
  }
  return (
    fixture?.packetVersion === 4 &&
    fixture.version === previous.version &&
    /^[a-f0-9]{40}$/.test(fixture.source ?? "") &&
    ![previous.source, value.source].includes(fixture.source) &&
    digest.test(fixture.artifactSetSha256 ?? "") &&
    ![previous.artifactSetSha256, value.artifactSetSha256].includes(
      fixture.artifactSetSha256,
    ) &&
    digest.test(fixture.packetSha256 ?? "") &&
    ![previous.packetSha256, targetPacketSha256].includes(
      fixture.packetSha256,
    ) &&
    auth.automatic === auth.before
  );
}

export function completeRemoteUpgrade(
  manual,
  security,
  automatic,
  originalPredecessor,
  format4Inputs,
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
  const format4 = format4Inputs !== undefined;
  const manualOnly = format4 && isManualOnlyFormat4(manual, 4);
  const split = format4 && manual?.cliInstanceId !== undefined;
  const automaticInstance = split ? automatic?.instanceId : manual?.instanceId;
  const lanes = split
    ? manualOnly
      ? [
          [security?.ui, manual.instanceId, manual.uiOperationId],
          [security?.cli, manual.cliInstanceId, manual.cliOperationId],
        ]
      : [
          [security?.ui, manual.instanceId, manual.uiOperationId],
          [security?.cli, manual.cliInstanceId, manual.cliOperationId],
          [
            security?.automatic,
            automaticInstance,
            browser?.automaticOperation?.id,
          ],
        ]
    : [[security, manual?.instanceId]];
  const receiverIds = lanes.map(([, instanceId]) => instanceId);
  const matches = (value, instanceId) =>
    value?.result === "PASS" &&
    value.instanceId === instanceId &&
    keys.every((key) => value[key] === manual[key]) &&
    value.fixtureOnly === true &&
    ["mining", "liveFeatures", "automaticUpdates"].every(
      (key) => value[key] === false,
    );
  const format4Proof = format4 && {
    ...manual,
    ...(manualOnly ? {} : { automaticPredecessor: automatic?.predecessor }),
    deviceAuthentication: {
      before: manual?.deviceAuthentication?.before,
      ui: manual?.deviceAuthentication?.ui,
      cli: manual?.deviceAuthentication?.cli,
      ...(manualOnly ? {} : { automatic: browser?.ownerAuthenticationSha256 }),
    },
  };
  const samePredecessor = (left, right) =>
    left &&
    right &&
    [
      "version",
      "source",
      "artifactSetSha256",
      "packetVersion",
      "ownerAuthMode",
      "packetSha256",
    ].every((key) => left[key] === right[key]);
  if (
    manual?.result !== "PASS" ||
    manual.scenario !== "manual-remote-replacement" ||
    manual.ownerAuthMode !==
      (format4
        ? "tailscale-device-v1"
        : "existing-password-account-preserved") ||
    manual.fixtureOnly !== true ||
    ["mining", "liveFeatures", "automaticUpdates"].some(
      (key) => manual[key] !== false,
    ) ||
    manual.upgradeApplied !== true ||
    manual.updatePasswordPrompt !== false ||
    manual.uiOutcome !== "applied" ||
    manual.cliOutcome !== "applied" ||
    (format4
      ? originalPredecessor !== undefined ||
        !format4Inputs ||
        !format4Inputs.binding ||
        Object.entries(format4Inputs.binding).some(
          ([key, expected]) => manual[key] !== expected,
        ) ||
        format4Inputs.localReceiptSha256 !== manual.localReceiptSha256 ||
        format4Inputs.manifestSha256 !== manual.manifestSha256 ||
        !samePredecessor(manual.predecessor, format4Inputs.predecessor) ||
        !validRemoteFormat4(format4Proof, manual.manifestSha256)
      : originalPredecessor?.version !== 2 ||
        originalPredecessor.source !== manual.predecessor?.source ||
        originalPredecessor.productVersion !== manual.predecessor?.version ||
        (manual.predecessor.packetVersion !== undefined &&
          manual.predecessor.packetVersion !== originalPredecessor.version) ||
        manual.checks?.actualPublicPredecessor !== "PASS") ||
    Object.values(mapped).some((key) => manual.checks?.[key] !== "PASS") ||
    (split &&
      (new Set(receiverIds).size !== (manualOnly ? 2 : 3) ||
        (manualOnly && security?.automatic !== undefined) ||
        receiverIds.some(
          (id) => typeof id !== "string" || !/^[A-Za-z0-9_-]{43}$/.test(id),
        ))) ||
    lanes.some(
      ([proof, instanceId, operationId]) =>
        !matches(proof, instanceId) ||
        (split &&
          (proof.operationId !== operationId ||
            proof.manifestSha256 !== manual.manifestSha256 ||
            proof.ownerAuthenticationSha256 !==
              format4Proof.deviceAuthentication.before)) ||
        stagedAcceptanceContract.remoteCommon.some(
          (key) => proof.checks?.[key] !== "PASS",
        ) ||
        proof.checks?.alreadyCurrent !== "PASS" ||
        proof.checks?.authenticationPreservation !== "PASS" ||
        proof.ownerAuthMode !== "tailscale-device-v1" ||
        proof.ownerEnrollment !==
          (format4 ? undefined : "existing-owner-ui-v1") ||
        proof.transport !== "tailscale-https" ||
        proof.normalTLS !== true ||
        proof.sshControlSessionRequired !== false,
    ) ||
    (manualOnly
      ? automatic !== undefined && automatic !== null
      : !matches(automatic, automaticInstance) ||
        automatic.sshControlSessionRequired !== false ||
        automatic.ownedSSHSessionDuringApply !== false ||
        browser?.result !== "PASS" ||
        browser.instanceId !== automaticInstance ||
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
        browser.automaticOperation.receipt.id !==
          browser.automaticOperation.id ||
        browser.automaticOperation.receipt.manifestSha256 !==
          manual.manifestSha256 ||
        [manual.uiOperationId, manual.cliOperationId].includes(
          browser.automaticOperation.id,
        ) ||
        after?.instanceId !== automaticInstance ||
        after.installedVersion !== manual.productVersion ||
        after.operation?.id !== browser.automaticOperation.id ||
        after.operation.automatic !== true ||
        after.operation.phase !== "completed" ||
        after.operation.receipt?.id !== browser.automaticOperation.id ||
        after.operation.receipt.phase !== "completed" ||
        after.operation.receipt.manifestSha256 !== manual.manifestSha256 ||
        after.activationEnabled !== false ||
        (after.policy?.mode ?? "manual") !== "manual")
  )
    throw Error(
      manualOnly
        ? "Matching actual manual and security upgrade proofs required"
        : "Matching actual manual, security and detached Automatic upgrade proofs required",
    );
  const operations = manualOnly
    ? {
        uiOperationId: manual.uiOperationId,
        cliOperationId: manual.cliOperationId,
      }
    : {
        uiOperationId: manual.uiOperationId,
        cliOperationId: manual.cliOperationId,
        automaticOperationId: browser.automaticOperation.id,
      };
  validateUpdateOperations(operations, manualOnly);
  const [[primary]] = lanes;
  return {
    schemaVersion: 1,
    ...Object.fromEntries(keys.map((key) => [key, manual[key]])),
    instanceId: manual.instanceId,
    result: "PASS",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    scenario: "upgrade",
    predecessor: {
      ...manual.predecessor,
      packetVersion: format4 ? 4 : originalPredecessor.version,
      ownerAuthMode: format4 ? "tailscale-device-v1" : "password-v1",
    },
    ...(format4
      ? {
          ...(manualOnly
            ? {}
            : { automaticPredecessor: format4Proof.automaticPredecessor }),
          deviceAuthentication: format4Proof.deviceAuthentication,
        }
      : {}),
    ...(split
      ? {
          receiverIdentities: {
            ui: sha(receiverIds[0]),
            cli: sha(receiverIds[1]),
            ...(manualOnly ? {} : { automatic: sha(receiverIds[2]) }),
          },
        }
      : {}),
    transport: primary.transport,
    normalTLS: true,
    ownerAuthMode: primary.ownerAuthMode,
    ownerEnrollment: primary.ownerEnrollment,
    sshControlSessionRequired: false,
    passwordPrompt: false,
    upgradeApplied: true,
    uiOutcome: manual.uiOutcome,
    cliOutcome: manual.cliOutcome,
    automaticOutcome: manualOnly ? "held-manual-only" : "applied",
    automaticPolicyRestored: "manual",
    customerActivation: false,
    ...operations,
    checks: {
      ...primary.checks,
      ...Object.fromEntries(
        Object.entries(mapped).map(([key, source]) => [
          key,
          manual.checks[source],
        ]),
      ),
      controlledAutomatic: manualOnly ? "HELD" : "PASS",
    },
  };
}

function validateAcceptanceLeg(
  value,
  binding,
  checks,
  { manualOnly = false } = {},
) {
  if (
    !value ||
    Object.entries(binding).some(([k, v]) => value[k] !== v) ||
    value.result !== "PASS" ||
    value.fixtureOnly !== true ||
    value.mining !== false ||
    value.liveFeatures !== false ||
    value.automaticUpdates !== false ||
    checks.some((name) =>
      manualOnly && name === "controlledAutomatic"
        ? !["HELD", "PASS"].includes(value.checks?.[name])
        : value.checks?.[name] !== "PASS",
    )
  )
    throw Error("Complete matching installed package leg required");
}

function validateAppliedAcceptance(value, packetVersion) {
  const automaticOk =
    value.automaticOutcome === "applied" ||
    isManualOnlyFormat4(value, packetVersion);
  if (
    value.upgradeApplied !== true ||
    value.uiOutcome !== "applied" ||
    value.cliOutcome !== "applied" ||
    !automaticOk ||
    value.automaticPolicyRestored !== "manual" ||
    value.customerActivation !== false
  )
    throw Error(
      "Actual UI/CLI replacement and restored Manual policy required",
    );
}

function olderVersion(before, after) {
  const parts = (version) =>
    /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(version ?? "")
      ? version.split(".").map(BigInt)
      : null;
  const left = parts(before);
  const right = parts(after);
  const first = left && right ? left.findIndex((v, i) => v !== right[i]) : -1;
  return first >= 0 && left[first] < right[first];
}

function format4LocalEvidence(local, packet, inventory) {
  const target = inventory.assets.find(
    ({ name }) => name === "fased-release.json",
  )?.sha256;
  const migration = local.legacyMigration;
  const migrationChecks = [
    "interruptionRecovery",
    "workspacePreservation",
    "authenticationPreservation",
    "alreadyCurrent",
  ];
  const manualOnly = isManualOnlyFormat4(local, 4);
  if (
    !migration ||
    migration.route !== "guarded-format4-adoption" ||
    !digest.test(target ?? "") ||
    migration.targetPacketSha256 !== target ||
    !digest.test(migration.bridgeSha256 ?? "") ||
    migration.bridgeSha256 === packet.installer?.sha256 ||
    !digest.test(migration.proofSha256 ?? "") ||
    !digest.test(migration.evidenceSha256 ?? "") ||
    !/^[a-f0-9]{32}$/.test(migration.operationId ?? "") ||
    [
      local.uiOperationId,
      local.cliOperationId,
      ...(manualOnly ? [] : [local.automaticOperationId]),
    ].includes(migration.operationId) ||
    migrationChecks.some((key) => migration.checks?.[key] !== "PASS")
  )
    throw Error("Exact guarded Local migration and recovery evidence required");

  const predecessor = local.compatiblePredecessor;
  if (
    predecessor?.packetVersion !== 4 ||
    !olderVersion(predecessor.version, packet.productVersion) ||
    !/^[a-f0-9]{40}$/.test(predecessor.source ?? "") ||
    [packet.source, local.releasedPredecessor.source].includes(
      predecessor.source,
    ) ||
    !digest.test(predecessor.packetSha256 ?? "") ||
    predecessor.packetSha256 === target ||
    !digest.test(predecessor.artifactSetSha256 ?? "") ||
    [
      local.artifactSetSha256,
      local.releasedPredecessor.artifactSetSha256,
    ].includes(predecessor.artifactSetSha256)
  )
    throw Error("Distinct actual older format-4 Local predecessor required");
  validateUpdateOperations(local, manualOnly);
  if (
    !digest.test(local.lifecycleEvidenceSha256 ?? "") ||
    local.lifecycleEvidenceSha256 === migration.evidenceSha256
  )
    throw Error("Separate Local lifecycle evidence digest required");

  const {
    route,
    targetPacketSha256,
    bridgeSha256,
    proofSha256,
    operationId,
    evidenceSha256,
  } = migration;
  const { version, source, packetVersion, packetSha256, artifactSetSha256 } =
    predecessor;
  return {
    legacyMigration: {
      route,
      targetPacketSha256,
      bridgeSha256,
      proofSha256,
      operationId,
      evidenceSha256,
      checks: Object.fromEntries(
        migrationChecks.map((key) => [key, migration.checks[key]]),
      ),
    },
    compatiblePredecessor: {
      version,
      source,
      packetVersion,
      packetSha256,
      artifactSetSha256,
    },
    uiOperationId: local.uiOperationId,
    cliOperationId: local.cliOperationId,
    ...(manualOnly ? {} : { automaticOperationId: local.automaticOperationId }),
    lifecycleEvidenceSha256: local.lifecycleEvidenceSha256,
  };
}

function validateLocalAcceptance(local, packet, inventory) {
  const binding = acceptanceBinding(packet, inventory);
  const manualOnly = isManualOnlyFormat4(local, packet.version);
  validateAcceptanceLeg(local, binding, stagedAcceptanceContract.local, {
    manualOnly,
  });
  validateAppliedAcceptance(local, packet.version);
  if (
    local.localOnly !== true ||
    local.remoteTested !== false ||
    !matchesReleasedPredecessor(local.releasedPredecessor)
  )
    throw Error("Actual approved released Local predecessor required");
  return packet.version === 4
    ? format4LocalEvidence(local, packet, inventory)
    : {};
}

function validateJoinedAcceptance(receipt, packet, inventory) {
  const binding = acceptanceBinding(packet, inventory);
  validateLocalAcceptance(receipt.local, packet, inventory);
  const localDigest = receipt.localReceiptSha256;
  if (!/^[a-f0-9]{64}$/.test(localDigest ?? ""))
    throw Error("Retained raw Local document digest required");
  const upgradeManualOnly = isManualOnlyFormat4(
    receipt.remoteUpgrade,
    packet.version,
  );
  for (const [value, scenario, specific] of [
    [receipt.remoteFresh, "fresh", stagedAcceptanceContract.remoteFresh],
    [receipt.remoteUpgrade, "upgrade", stagedAcceptanceContract.remoteUpgrade],
  ]) {
    validateAcceptanceLeg(
      value,
      binding,
      [...stagedAcceptanceContract.remoteCommon, ...specific],
      { manualOnly: scenario === "upgrade" && upgradeManualOnly },
    );
    const deviceOwner =
      value.ownerAuthMode === "tailscale-device-v1" &&
      value.checks.protectedOwnerDevice === "PASS";
    // The actual supported public predecessor has an existing password owner.
    // Its normal UI can add device ownership without replacing that account.
    const preservedEnrollment =
      scenario === "upgrade" &&
      value.ownerEnrollment === "existing-owner-ui-v1" &&
      value.predecessor?.ownerAuthMode === "password-v1" &&
      matchesReleasedPredecessor(value.predecessor);
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
  validateAppliedAcceptance(upgrade, packet.version);
  validateUpdateOperations(upgrade, upgradeManualOnly);
  const previous = upgrade.predecessor;
  if (upgrade.receiverIdentities !== undefined) {
    const ids = upgrade.receiverIdentities;
    const fresh = receipt.remoteFresh;
    const expectedKeys = upgradeManualOnly
      ? ["ui", "cli"]
      : ["ui", "cli", "automatic"];
    const upgradeReceivers = expectedKeys.map((key) => ids?.[key]);
    if (
      previous?.packetVersion !== 4 ||
      !ids ||
      Object.keys(ids).length !== expectedKeys.length ||
      !upgradeReceivers.every(
        (id) => typeof id === "string" && digest.test(id),
      ) ||
      ids.ui !== (upgrade.instanceIdentitySha256 ?? sha(upgrade.instanceId)) ||
      new Set([
        ...upgradeReceivers,
        fresh.instanceIdentitySha256 ?? sha(fresh.instanceId),
      ]).size !==
        expectedKeys.length + 1 ||
      fresh.nodeIdentitySha256 !== upgrade.nodeIdentitySha256
    )
      throw Error(
        "Distinct Remote receivers on the same enrolled node required",
      );
  }
  if (
    !olderVersion(previous?.version, binding.productVersion) ||
    (previous.packetVersion === 4
      ? packet.version !== 4 ||
        !validRemoteFormat4(
          upgrade,
          inventory.assets.find(({ name }) => name === "fased-release.json")
            ?.sha256,
        )
      : previous.packetVersion !== 2 ||
        packet.source ===
          stagedAcceptanceContract.remoteFormat4.targetSource) ||
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

async function originalCandidateArtifact(packet, api) {
  const { runId, runAttempt } = packet.release;
  const listed = await api("GET", `${repo}/actions/runs/${runId}/artifacts`);
  const artifacts = listed.artifacts?.filter(
    (a) =>
      a.name === `ubuntu-amd64-release-candidate-${runAttempt}` && !a.expired,
  );
  if (
    artifacts?.length !== 1 ||
    !number.test(String(artifacts[0].id)) ||
    !/^sha256:[a-f0-9]{64}$/.test(artifacts[0].digest ?? "") ||
    String(artifacts[0].workflow_run?.id) !== runId ||
    artifacts[0].workflow_run?.head_sha !== packet.source
  )
    throw Error("Exact original candidate artifact required for replay");
  return artifacts[0];
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
    const original = {
      source: run.head_sha,
      release: { runId, runAttempt: attempt },
    };
    await verifiedProducer(original, config, api, { run });
    const artifact = await originalCandidateArtifact(original, api);
    const zip = await downloadArtifact(String(artifact.id));
    if (
      !Buffer.isBuffer(zip) ||
      zip.length === 0 ||
      zip.length > 128 * 1024 * 1024 ||
      `sha256:${sha(zip)}` !== artifact.digest
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
    !isMainBoundCandidate(packet) ||
    packet.release?.runId !== runId ||
    packet.release?.runAttempt !== attempt
  )
    throw Error("Replay cannot rebind candidate source, run or attempt");
  const inventory = attestedInventory(config, packet, raw);
  await verifiedProducer(packet, inventory, api);
  const artifact = await originalCandidateArtifact(packet, api);
  for (const asset of inventory.assets) {
    const bytes = read(`${directory}/${asset.name}`);
    if (bytes.length !== asset.size || sha(bytes) !== asset.sha256)
      throw Error("Original replay candidate asset differs: " + asset.name);
  }
  return {
    ...acceptanceBinding(packet, inventory),
    candidateArtifactId: String(artifact.id),
    candidateArtifactDigest: artifact.digest,
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
    !isMainBoundCandidate(packet) ||
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
const localReviewPrefix = "FASED_LOCAL_ACCEPTANCE_V1\n";
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
  if (value.scenario === "upgrade") {
    const predecessorFields = [
      "version",
      "source",
      "artifactSetSha256",
      "packetVersion",
      "ownerAuthMode",
    ];
    if (value.predecessor.packetVersion === 4)
      predecessorFields.push("packetSha256");
    projected.predecessor = Object.fromEntries(
      predecessorFields.map((k) => [k, value.predecessor[k]]),
    );
    if (value.predecessor.packetVersion === 4) {
      const manualOnly = isManualOnlyFormat4(value, 4);
      if (!manualOnly)
        projected.automaticPredecessor = Object.fromEntries(
          [
            "version",
            "source",
            "artifactSetSha256",
            "packetVersion",
            "packetSha256",
          ].map((k) => [k, value.automaticPredecessor[k]]),
        );
      const authKeys = manualOnly
        ? ["before", "ui", "cli"]
        : ["before", "ui", "cli", "automatic"];
      projected.deviceAuthentication = Object.fromEntries(
        authKeys.map((k) => [k, value.deviceAuthentication[k]]),
      );
      if (value.receiverIdentities) {
        const receiverKeys = manualOnly
          ? ["ui", "cli"]
          : ["ui", "cli", "automatic"];
        projected.receiverIdentities = Object.fromEntries(
          receiverKeys.map((k) => [k, value.receiverIdentities[k]]),
        );
      }
    }
  }
  return projected;
}
function ownerReviewPayload(comment, packet, prefix) {
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
    !comment.body.startsWith(prefix)
  )
    throw Error("Unmodified package-bound fcode-ai review required");
  const text = comment.body.slice(prefix.length),
    review = JSON.parse(text);
  if (JSON.stringify(review) !== text)
    throw Error("Canonical owner-reviewed acceptance required");
  return review;
}

function publicLocalReceipt(receipt, packet, inventory) {
  const additional = validateLocalAcceptance(receipt, packet, inventory);
  const { version, source, artifactSetSha256 } = receipt.releasedPredecessor;
  return {
    ...acceptanceBinding(packet, inventory),
    result: "PASS",
    fixtureOnly: true,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    localOnly: true,
    remoteTested: false,
    releasedPredecessor: { version, source, artifactSetSha256 },
    upgradeApplied: true,
    uiOutcome: "applied",
    cliOutcome: "applied",
    automaticOutcome: isManualOnlyFormat4(receipt, packet.version)
      ? "held-manual-only"
      : "applied",
    automaticPolicyRestored: "manual",
    customerActivation: false,
    checks: Object.fromEntries(
      stagedAcceptanceContract.local.map((key) => [key, receipt.checks[key]]),
    ),
    ...additional,
  };
}

function localReviewBody(packet, inventory, receipt, artifact) {
  return (
    localReviewPrefix +
    JSON.stringify({
      version: packet.version === 4 ? 2 : 1,
      candidateArtifactId: String(artifact.id),
      candidateArtifactDigest: artifact.digest,
      receipt: publicLocalReceipt(receipt, packet, inventory),
    })
  );
}

function reviewedLocal(comment, packet, inventory, artifact) {
  const { receipt } = ownerReviewPayload(comment, packet, localReviewPrefix);
  if (comment.body !== localReviewBody(packet, inventory, receipt, artifact))
    throw Error(
      "Local review differs from the authenticated candidate or receipt",
    );
  return {
    receipt,
    sha256: sha(JSON.stringify(receipt)),
    localReviewCommentId: String(comment.id),
    localReviewSha256: sha(comment.body),
  };
}

function localProofReference(proof) {
  return {
    localReceiptSha256: proof.sha256,
    ...(proof.localReviewCommentId
      ? {
          localReviewCommentId: proof.localReviewCommentId,
          localReviewSha256: proof.localReviewSha256,
        }
      : { localCIArtifactDigest: proof.artifactDigest }),
  };
}

function reviewedRemote(comment, packet, inventory, proof) {
  const review = ownerReviewPayload(comment, packet, reviewPrefix);
  const local = localProofReference(proof);
  if (
    review.version !== (proof.localReviewCommentId ? 3 : 2) ||
    JSON.stringify(review.binding) !==
      JSON.stringify(acceptanceBinding(packet, inventory)) ||
    [
      "localReceiptSha256",
      "localCIArtifactDigest",
      "localReviewCommentId",
      "localReviewSha256",
    ].some((key) => review[key] !== local[key])
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
  // The Remote record references separately authenticated Local proof.
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
async function acceptanceReviews(packet, api, prefix = reviewPrefix) {
  const comments = await api(
    "GET",
    `${repo}/commits/${packet.source}/comments?per_page=100`,
  );
  if (!Array.isArray(comments) || comments.length >= 100)
    throw Error("Bounded complete acceptance review listing required");
  return comments.filter((c) => c.body?.startsWith(prefix));
}

async function realHostCandidateArtifact(packet, inventory, api) {
  if (
    packet.version !== 4 ||
    !isMainBoundCandidate(packet) ||
    !number.test(packet.release.runId) ||
    !number.test(packet.release.runAttempt)
  )
    throw Error("Exact protected format-4 candidate required for Local review");
  await verifiedProducer(packet, inventory, api);
  return originalCandidateArtifact(packet, api);
}

async function readAuthenticatedInstalledAcceptance(
  packet,
  inventory,
  api,
  options,
) {
  if (packet.version === 4 && isMainBoundCandidate(packet)) {
    const comments = await acceptanceReviews(packet, api, localReviewPrefix);
    if (comments.length > 1)
      throw Error("One reviewed Local acceptance is required");
    if (comments.length === 1) {
      const artifact = await realHostCandidateArtifact(packet, inventory, api);
      return reviewedLocal(comments[0], packet, inventory, artifact);
    }
  }
  return readCIInstalledAcceptance(packet, inventory, api, options);
}

export async function verifyLocalAcceptance(packet, inventory, api, options) {
  const proof = await readAuthenticatedInstalledAcceptance(
    packet,
    inventory,
    api,
    options,
  );
  const binding = acceptanceBinding(packet, inventory);
  validateLocalAcceptance(proof.receipt, packet, inventory);
  return { ...binding, ...localProofReference(proof) };
}

// An owner may record a complete, reviewed real-host receipt. Partial or
// cross-packet evidence is refused before any remote write.
export async function recordLocalAcceptance(packet, inventory, api, receipt) {
  const local = publicLocalReceipt(receipt, packet, inventory);
  const artifact = await realHostCandidateArtifact(packet, inventory, api);
  const actor = await api("GET", "/user");
  if (String(actor.id) !== reviewOwner.id || actor.login !== reviewOwner.login)
    throw Error("Only the approved GitHub owner may record Local acceptance");
  const body = localReviewBody(packet, inventory, local, artifact);
  const existing = await acceptanceReviews(packet, api, localReviewPrefix);
  if (existing.length) {
    if (existing.length !== 1 || existing[0].body !== body)
      throw Error(
        "Conflicting Local review exists; preserve original evidence",
      );
    return localProofReference(
      reviewedLocal(existing[0], packet, inventory, artifact),
    );
  }
  const posted = await api(
    "POST",
    `${repo}/commits/${packet.source}/comments`,
    { body },
  );
  if (!number.test(String(posted.id)))
    throw Error("Local review outcome uncertain; reconcile before retry");
  const retained = await api("GET", `${repo}/comments/${posted.id}`);
  if (retained.body !== body)
    throw Error("Local review readback differs; reconcile before retry");
  return localProofReference(
    reviewedLocal(retained, packet, inventory, artifact),
  );
}
export async function verifyInstalledAcceptance(
  packet,
  inventory,
  api,
  options = {},
) {
  const proof = await readAuthenticatedInstalledAcceptance(
    packet,
    inventory,
    api,
    options,
  );
  let review = {};
  if (proof.receipt.localOnly === true) {
    const comments = await acceptanceReviews(packet, api);
    if (comments.length !== 1)
      throw Error("One reviewed Remote acceptance is required");
    review = reviewedRemote(comments[0], packet, inventory, proof);
  } else {
    validateInstalledAcceptance(proof.receipt, packet, inventory);
    validateUpdateOperations(proof.receipt.remoteUpgrade ?? proof.receipt);
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
    ...(proof.localReviewCommentId ? localProofReference(proof) : {}),
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
  const proof = await readAuthenticatedInstalledAcceptance(
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
      version: proof.localReviewCommentId ? 3 : 2,
      binding: acceptanceBinding(packet, inventory),
      ...localProofReference(proof),
      remoteFresh: publicRemoteLeg(remoteFresh),
      remoteUpgrade: publicRemoteLeg(remoteUpgrade),
      installedAgent: publicInstalledAgent(options.installedAgent),
    });
  if (body.length > 60000) throw Error("Bounded Remote evidence required");
  const existing = await acceptanceReviews(packet, api);
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
