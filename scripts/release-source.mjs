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
import { openPublisherJournal } from "./publisher-journal.mjs";
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
  if (pr.state === "OPEN") {
    progress("required CI");
    call(
      "gh",
      [
        "pr",
        "checks",
        "--repo",
        "fased-ai/fased-app",
        String(pr.number),
        "--required",
        "--watch",
        "--fail-fast",
        "--interval",
        "15",
      ],
      { stdio: "inherit" },
    );
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
  if (runs[0].status !== "completed")
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
  const outcome = JSON.parse(
    call("gh", [
      "run",
      "view",
      id,
      "--repo",
      "fased-ai/fased-app",
      "--json",
      "conclusion,attempt",
    ]),
  );
  if (outcome.conclusion !== "success" || outcome.attempt !== 1)
    throw Error("Original candidate build did not pass");
  const entries = JSON.parse(
    call("gh", [
      "api",
      `/repos/fased-ai/fased-app/actions/runs/${id}/artifacts`,
    ]),
  ).artifacts.filter(
    (a) => a.name === "ubuntu-amd64-release-candidate" && !a.expired,
  );
  if (entries.length !== 1 || !/^sha256:[a-f0-9]{64}$/.test(entries[0].digest))
    throw Error("Authenticated candidate digest unavailable");
  const root = path.resolve(workspace, `.evidence/ubuntu-${version}-candidate`);
  mkdirSync(root, { recursive: true, mode: 0o700 });
  const zip = path.join(root, "original-artifact.zip");
  const raw = existsSync(zip)
    ? readFileSync(zip)
    : call(
        "gh",
        [
          "api",
          `/repos/fased-ai/fased-app/actions/artifacts/${entries[0].id}/zip`,
        ],
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
names=set('fased-app-linux-amd64.tar.gz fased-app-linux-amd64.json fased-install-linux-amd64 fased-app-linux-amd64.provenance.sigstore.json verified-provenance.json expected-provenance.json provenance-policy.json first-install-seed.json install.sh build-inputs.json artifact-inventory.json signing-inputs.json'.split())
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
