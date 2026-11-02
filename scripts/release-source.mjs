// One protected source package -> one immutable tag -> one original CI artifact.
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  readFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  readdirSync,
} from "node:fs";
import path from "node:path";
import { persistLocalReleaseFile } from "./local-release-signing.mjs";
import {
  openPublisherJournal,
  readPublisherFile,
  ensurePublisherDirectory,
} from "./publisher-journal.mjs";
export function acceptedReleasePR(pr, head) {
  if (
    pr.baseRefName !== "main" ||
    pr.headRefOid !== head ||
    !["OPEN", "MERGED"].includes(pr.state)
  )
    throw Error("Exact current-source PR to main required");
  return pr;
}
export async function prepareReleaseCandidate(
  version,
  number,
  {
    run = execFileSync,
    progress = (s) => process.stdout.write(`Release: ${s}\n`),
    workspace = process.cwd(),
    delay = (ms) => new Promise((r) => setTimeout(r, ms)),
  } = {},
) {
  if (
    !/^[0-9]+\.[0-9]+\.[0-9]+$/.test(version) ||
    (number && !/^[1-9][0-9]*$/.test(String(number)))
  )
    throw Error("Invalid version/PR");
  const call = (p, a, o = {}) =>
    run(p, a, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      maxBuffer: 2 * 1024 * 1024,
      ...o,
    });
  call("gh", ["auth", "status"]);
  for (const args of [
    ["remote", "get-url", "--all", "origin"],
    ["remote", "get-url", "--push", "--all", "origin"],
  ]) {
    const urls = call("git", args).trim().split("\n");
    if (
      !urls.length ||
      urls.some(
        (url) =>
          ![
            /^git@(github\.com|github-fased):fased-ai\/fased-app\.git$/,
            /^https:\/\/github\.com\/fased-ai\/fased-app(?:\.git)?$/,
          ].some((rule) => rule.test(url)),
      )
    )
      throw Error("Fixed Fased origin required before release");
  }
  const repository = JSON.parse(
    call("gh", ["api", "/repos/fased-ai/fased-app"]),
  );
  if (
    String(repository.id) !== "1426211857" ||
    String(repository.owner?.id) !== "279582504" ||
    repository.full_name !== "fased-ai/fased-app"
  )
    throw Error("Fased repository identity changed");
  // This settings endpoint needs admin-read, unavailable to GITHUB_TOKEN.
  // The owner checks it here before creating a tag; the workflow and clients
  // independently require immutable:true on the final published object.
  if (
    JSON.parse(
      call("gh", ["api", "/repos/fased-ai/fased-app/immutable-releases"]),
    ).enabled !== true
  )
    throw Error("Enable immutable releases before tagging");
  if (call("git", ["status", "--porcelain"]).trim())
    throw Error("Commit the reviewed package before release");
  const head = call("git", ["rev-parse", "HEAD"]).trim(),
    tree = call("git", ["rev-parse", "HEAD^{tree}"]).trim();
  const view = () =>
    JSON.parse(
      call("gh", [
        "pr",
        "view",
        "--repo",
        "fased-ai/fased-app",
        ...(number ? [String(number)] : []),
        "--json",
        "number,state,baseRefName,headRefOid,mergeCommit",
      ]),
    );
  let pr = acceptedReleasePR(view(), head);
  progress("required CI");
  // GitHub registers the required aggregate only after its dependency jobs.
  // Watch current checks first, then require the protected aggregate itself.
  const checkArgs = [
    "pr",
    "checks",
    "--repo",
    "fased-ai/fased-app",
    String(pr.number),
    "--watch",
    "--fail-fast",
    "--interval",
    "15",
  ];
  call("gh", checkArgs, { stdio: "inherit" });
  call("gh", [...checkArgs, "--required"], { stdio: "inherit" });

  if (pr.state === "OPEN") {
    progress("merge passing package");
    call("gh", [
      "pr",
      "merge",
      "--repo",
      "fased-ai/fased-app",
      String(pr.number),
      "--squash",
      "--match-head-commit",
      head,
    ]);
    pr = acceptedReleasePR(view(), head);
  }
  const source = pr.mergeCommit?.oid;
  if (pr.state !== "MERGED" || !/^[a-f0-9]{40}$/.test(source ?? ""))
    throw Error("Merged source not confirmed");
  call("git", ["fetch", "origin", "main"]);
  if (call("git", ["rev-parse", `${source}^{tree}`]).trim() !== tree)
    throw Error("Merged tree differs from passing package");
  const tag = `v${version}`,
    remote = call("git", [
      "ls-remote",
      "--tags",
      "origin",
      `refs/tags/${tag}`,
      `refs/tags/${tag}^{}`,
    ]);
  if (!remote.trim()) {
    if (!call("git", ["tag", "--list", tag]).trim())
      call("git", ["tag", "-a", tag, source, "-m", `Fased ${version}`]);
    if (
      call("git", ["cat-file", "-t", `refs/tags/${tag}`]).trim() !== "tag" ||
      call("git", ["rev-parse", `${tag}^{commit}`]).trim() !== source
    )
      throw Error("Local tag identity differs");
    progress("tag-bound candidate build");
    call("git", ["push", "origin", `refs/tags/${tag}`]);
  } else if (
    !remote
      .split("\n")
      .some((line) => line === `${source}\trefs/tags/${tag}^{}`)
  )
    throw Error("Existing tag differs; never move it");
  let runs;
  for (let attempt = 0; attempt < 12; attempt++) {
    runs = JSON.parse(
      call("gh", [
        "run",
        "list",
        "--repo",
        "fased-ai/fased-app",
        "--workflow",
        "release.yml",
        "--branch",
        tag,
        "--event",
        "push",
        "--json",
        "databaseId,headSha,status,conclusion",
      ]),
    );
    if (runs.length) break;
    await delay(5000);
  }
  if (runs?.length !== 1 || runs[0].headSha !== source)
    throw Error("One exact tag-bound build required; do not rerun");
  const id = String(runs[0].databaseId);
  if (runs[0].status !== "completed") {
    try {
      call(
        "gh",
        [
          "run",
          "watch",
          id,
          "--repo",
          "fased-ai/fased-app",
          "--exit-status",
          "--interval",
          "10",
        ],
        {
          stdio: "inherit",
        },
      );
    } catch {}
  } // Read canonical outcome; failed publication must remain recoverable.
  const outcome = JSON.parse(
    call("gh", [
      "run",
      "view",
      id,
      "--repo",
      "fased-ai/fased-app",
      "--json",
      "conclusion,attempt,jobs",
    ]),
  );
  if (
    !["success", "failure", "cancelled", "timed_out"].includes(
      outcome.conclusion,
    ) ||
    outcome.attempt !== 1
  )
    throw Error("Original candidate build did not pass");
  if (outcome.conclusion !== "success") {
    const producer = outcome.jobs?.find((j) => j.name === "ubuntu-amd64");
    if (
      !producer ||
      !producer.steps?.some(
        (s) =>
          s.name ===
            "Verify original provenance offline and stage public signing inputs" &&
          s.conclusion === "success",
      )
    )
      throw Error(
        "Original build did not complete verification; never rerun it",
      );
  }
  const entries = JSON.parse(
    call("gh", [
      "api",
      `/repos/fased-ai/fased-app/actions/runs/${id}/artifacts`,
    ]),
  ).artifacts.filter(
    (a) => a.name === "ubuntu-amd64-release-candidate" && !a.expired,
  );
  const root = path.resolve(workspace, `.evidence/ubuntu-${version}-candidate`);
  mkdirSync(root, { recursive: true, mode: 0o700 });
  ensurePublisherDirectory(root);
  if (entries.length === 0)
    return recoverPublicCandidate({
      version,
      source,
      runId: id,
      root,
      workspace,
      call,
    });
  if (
    entries.length !== 1 ||
    !/^sha256:[a-f0-9]{64}$/.test(entries[0].digest) ||
    !/^[1-9][0-9]*$/.test(String(entries[0].id))
  )
    throw Error("Authenticated candidate digest unavailable");
  const zip = path.join(root, "original-artifact.zip");
  let raw;
  if (existsSync(zip)) {
    try {
      raw = readPublisherFile(zip, 128 * 1024 * 1024);
    } catch {}
  }
  raw ??= call(
    "gh",
    ["api", `/repos/fased-ai/fased-app/actions/artifacts/${entries[0].id}/zip`],
    { encoding: "buffer", maxBuffer: 128 * 1024 * 1024 },
  );

  if (
    `sha256:${createHash("sha256").update(raw).digest("hex")}` !==
    entries[0].digest
  )
    throw Error("Original ZIP digest differs");
  const journalDir = path.join(root, "artifact-operation");
  const journal = openPublisherJournal(journalDir, entries[0].digest.slice(7), {
    resume: existsSync(path.join(journalDir, "operation.json")),
  });
  let scratch;
  try {
    persistLocalReleaseFile(journal, zip, raw, 128 * 1024 * 1024);
    const directory = path.join(root, "artifacts");
    mkdirSync(directory, { recursive: true, mode: 0o700 });
    scratch = mkdtempSync(path.join(root, "extract-"));
    call("python3", [
      "-c",
      `
import zipfile,pathlib,sys,stat
names=set('fased-app-linux-amd64.tar.gz fased-app-linux-amd64.json fased-install-linux-amd64 fased-app-linux-amd64.provenance.sigstore.json verified-provenance.json expected-provenance.json provenance-policy.json first-install-seed.json install.sh build-inputs.json artifact-inventory.json signing-inputs.json fased-release.json'.split())
out=pathlib.Path(sys.argv[2]);seen=set();total=0
with zipfile.ZipFile(sys.argv[1]) as z:
 for f in z.infolist():
  p=pathlib.PurePosixPath(f.filename)
  if f.is_dir():continue
  name=p.name
  assert name in names and name not in seen and '..' not in p.parts and not p.is_absolute()
  assert stat.S_IFMT(f.external_attr>>16) in (0,stat.S_IFREG)
  total+=f.file_size;assert 0<f.file_size<=128*1024*1024 and total<=256*1024*1024
  seen.add(name);data=z.read(f);target=out/name
  if target.exists():assert target.read_bytes()==data
  else:
   with target.open('xb') as w:w.write(data)
   target.chmod(0o600)
 assert seen==names
`,
      zip,
      scratch,
    ]);
    for (const name of readdirSync(scratch))
      persistLocalReleaseFile(
        journal,
        path.join(directory, name),
        readFileSync(path.join(scratch, name)),
        128 * 1024 * 1024,
      );
    const packet = JSON.parse(
      readFileSync(path.join(directory, "signing-inputs.json"), "utf8"),
    );
    if (
      packet.version !== version ||
      packet.sourceCommit !== source ||
      String(packet.runId) !== id ||
      packet.runAttempt !== "1"
    )
      throw Error("Candidate differs from actual original build");
    return directory;
  } finally {
    if (scratch) rmSync(scratch, { recursive: true });
    journal.close();
  }
}

// Recover a completed immutable publication if Actions retention itself failed.
// All bytes still undergo the trusted Go proof and installer preflight in the
// caller; this is recovery of one original build, never a rebuild or new tag.
export function recoverPublicCandidate({
  version,
  source,
  runId,
  root,
  workspace,
  call,
}) {
  const release = JSON.parse(
    call("gh", ["api", `/repos/fased-ai/fased-app/releases/tags/v${version}`]),
  );
  const names = [
    "fased-app-linux-amd64.tar.gz",
    "fased-install-linux-amd64",
    "fased-app-linux-amd64.provenance.sigstore.json",
    "install.sh",
    "fased-release.json",
  ];
  if (
    release.tag_name !== `v${version}` ||
    release.draft !== false ||
    release.prerelease !== false ||
    release.immutable !== true ||
    release.assets?.length !== names.length
  )
    throw Error("Original complete immutable release required for recovery");
  const directory = path.join(root, "artifacts");
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  ensurePublisherDirectory(directory);
  const journalDir = path.join(root, "public-recovery");
  const binding = createHash("sha256")
    .update(
      JSON.stringify({ source, runId, version, releaseId: String(release.id) }),
    )
    .digest("hex");
  const journal = openPublisherJournal(journalDir, binding, {
    resume: existsSync(path.join(journalDir, "operation.json")),
  });
  try {
    for (const name of names) {
      const assets = release.assets.filter((a) => a.name === name);
      if (assets.length !== 1)
        throw Error("Original release inventory changed");
      const asset = assets[0];
      if (
        !/^[1-9][0-9]*$/.test(String(asset.id)) ||
        asset.state !== "uploaded" ||
        !Number.isSafeInteger(asset.size) ||
        asset.size <= 0 ||
        asset.size > 128 * 1024 * 1024 ||
        !/^sha256:[a-f0-9]{64}$/.test(asset.digest)
      )
        throw Error("Original asset identity invalid");
      const raw = call(
        "gh",
        [
          "api",
          "-H",
          "Accept: application/octet-stream",
          `/repos/fased-ai/fased-app/releases/assets/${asset.id}`,
        ],
        { encoding: "buffer", maxBuffer: 128 * 1024 * 1024 },
      );
      if (
        raw.length !== asset.size ||
        `sha256:${createHash("sha256").update(raw).digest("hex")}` !==
          asset.digest
      )
        throw Error("Original asset bytes differ");
      persistLocalReleaseFile(
        journal,
        path.join(directory, name),
        raw,
        128 * 1024 * 1024,
      );
    }
    const packet = JSON.parse(
      readPublisherFile(path.join(directory, "fased-release.json")),
    );
    if (
      packet.productVersion !== version ||
      packet.source !== source ||
      String(packet.release?.runId) !== runId ||
      packet.release.runAttempt !== "1"
    )
      throw Error("Original published source/run differs");
    const c = JSON.parse(
      readFileSync(path.join(workspace, "release/ubuntu.json"), "utf8"),
    );
    const seed = readFileSync(path.join(workspace, c.seedFile));
    if (createHash("sha256").update(seed).digest("hex") !== c.seedSha256)
      throw Error("Source public trust changed");
    persistLocalReleaseFile(
      journal,
      path.join(directory, "first-install-seed.json"),
      seed,
    );
    journal.state.complete = true;
    journal.save();
    return directory;
  } finally {
    journal.close();
  }
}
