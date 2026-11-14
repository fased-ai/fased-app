// Publication consumes acceptance from the protected workflow's authenticated
// artifact, never from a caller-supplied local PASS document.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";

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
  const binding = acceptanceBinding(packet, inventory);
  const bootstrap =
    receipt?.upgradeRoute === "bootstrap" &&
    receipt.predecessorPacketVersion === 1 &&
    receipt.candidatePacketVersion === 2 &&
    receipt.upgradeApplied === true &&
    receipt.uiOutcome === "alreadyCurrent" &&
    receipt.cliOutcome === "alreadyCurrent" &&
    receipt.ordinaryUpdateApply === "DEFERRED_PROFILE_TRANSITION";
  const installed =
    receipt?.upgradeRoute === "installed" &&
    receipt.predecessorPacketVersion === 2 &&
    receipt.candidatePacketVersion === 2 &&
    receipt.upgradeApplied === true &&
    receipt.uiOutcome === "applied" &&
    receipt.cliOutcome === "applied" &&
    receipt.ordinaryUpdateApply === "PASS";
  if (!bootstrap && !installed)
    throw Error("Explicit supported update transition evidence required");
  if (
    !receipt ||
    Object.entries(binding).some(([k, v]) => receipt[k] !== v) ||
    receipt.fixtureOnly !== true ||
    receipt.mining !== false ||
    receipt.liveFeatures !== false ||
    receipt.automaticUpdates !== false ||
    !receipt.checks ||
    Object.keys(receipt.checks).length !== installedAcceptanceChecks.length ||
    installedAcceptanceChecks.some((k) => receipt.checks[k] !== "PASS")
  ) {
    throw Error("Complete matching installed package acceptance required");
  }
  return binding;
}
function readReceiptZIP(raw, execute) {
  if (!Buffer.isBuffer(raw) || raw.length === 0 || raw.length > 2 * 1024 * 1024)
    throw Error("Bounded authenticated acceptance artifact required");
  const output = execute(
    "python3",
    [
      "-c",
      `
import io,json,stat,sys,zipfile
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
 print(json.dumps(value,separators=(',',':')))
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
export async function verifyInstalledAcceptance(
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
  if (
    String(run.id) !== id ||
    String(run.run_attempt) !== attempt ||
    run.head_sha !== packet.source ||
    run.head_branch !== "main" ||
    run.event !== "workflow_dispatch" ||
    run.path !== ".github/workflows/release.yml" ||
    run.status !== "completed" ||
    run.conclusion !== "success" ||
    String(run.repository?.id) !== inventory.repositoryId ||
    String(run.repository?.owner?.id) !== inventory.ownerId
  )
    throw Error("Successful exact protected candidate workflow required");
  const result = await api(
    "GET",
    `${repo}/actions/runs/${id}/attempts/${attempt}/jobs`,
  );
  const jobs = result.jobs?.filter((j) => j.name === "installed-acceptance");
  if (
    jobs?.length !== 1 ||
    jobs[0].status !== "completed" ||
    jobs[0].conclusion !== "success"
  )
    throw Error("Installed acceptance job did not complete");
  const listed = await api("GET", `${repo}/actions/runs/${id}/artifacts`);
  const artifacts = listed.artifacts?.filter(
    (a) =>
      a.name === `ubuntu-amd64-installed-acceptance-${attempt}` && !a.expired,
  );
  if (
    artifacts?.length !== 1 ||
    !number.test(String(artifacts[0].id)) ||
    !/^sha256:[a-f0-9]{64}$/.test(artifacts[0].digest) ||
    String(artifacts[0].workflow_run?.id) !== id ||
    artifacts[0].workflow_run?.head_sha !== packet.source
  )
    throw Error("Exact retained CI acceptance artifact required");
  const raw = await downloadArtifact(String(artifacts[0].id));
  if (`sha256:${sha(raw)}` !== artifacts[0].digest)
    throw Error("CI acceptance artifact digest differs");
  const receipt = readReceiptZIP(raw, execute);
  const binding = validateInstalledAcceptance(receipt, packet, inventory);
  return {
    ...binding,
    artifactId: String(artifacts[0].id),
    artifactDigest: artifacts[0].digest,
  };
}
