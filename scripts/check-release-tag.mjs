import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const VALID_TAG_RE =
  /^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-desktop)?$/;
const HEX40_RE = /^[a-f0-9]{40}$/;

/**
 * Validate a GitHub release tag reference & tag object against an expected commit.
 * Mirrors internal/releaseops/published.go annotated-tag enforcement cleanly pre-publish.
 */
export function validateReleaseTagRef({ tag, expectedCommit, refObj, tagObj }) {
  if (!VALID_TAG_RE.test(String(tag || ""))) {
    throw new Error(`Invalid release tag format: ${tag}`);
  }
  const normalizedCommit = String(expectedCommit || "").toLowerCase();
  if (!HEX40_RE.test(normalizedCommit)) {
    throw new Error(`Invalid expected commit SHA: ${expectedCommit}`);
  }
  if (!refObj || refObj.ref !== `refs/tags/${tag}`) {
    throw new Error(`Tag ref missing or mismatched for ${tag}`);
  }
  const refObject = refObj.object || {};
  if (
    refObject.type !== "tag" ||
    !HEX40_RE.test(String(refObject.sha || "").toLowerCase())
  ) {
    throw new Error("annotated release tag required");
  }
  if (!tagObj || tagObj.tag !== tag) {
    throw new Error(`Annotated tag object name mismatch for ${tag}`);
  }
  const target = tagObj.object || {};
  if (
    target.type !== "commit" ||
    String(target.sha || "").toLowerCase() !== normalizedCommit
  ) {
    throw new Error(
      `Annotated tag ${tag} points to ${target.sha || "unknown"}, expected ${normalizedCommit}`,
    );
  }
  return {
    ok: true,
    tag,
    tagObject: refObject.sha.toLowerCase(),
    commit: normalizedCommit,
  };
}

function defaultFetchGitHubJson(apiPath) {
  const raw = execFileSync("gh", ["api", apiPath], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  return JSON.parse(raw);
}

export function checkReleaseTag(
  { tag, commit, repo = "fased-ai/fased-app" },
  fetchJson = defaultFetchGitHubJson,
) {
  const refObj = fetchJson(`repos/${repo}/git/refs/tags/${tag}`);
  if (refObj?.object?.type !== "tag") {
    return validateReleaseTagRef({
      tag,
      expectedCommit: commit,
      refObj,
      tagObj: null,
    });
  }
  const tagObj = fetchJson(`repos/${repo}/git/tags/${refObj.object.sha}`);
  return validateReleaseTagRef({ tag, expectedCommit: commit, refObj, tagObj });
}

function parseArgs(argv) {
  const opts = { tag: "", commit: "", repo: "fased-ai/fased-app", json: false };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--tag" && argv[i + 1]) {
      opts.tag = argv[++i];
    } else if (arg === "--commit" && argv[i + 1]) {
      opts.commit = argv[++i];
    } else if (arg === "--repo" && argv[i + 1]) {
      opts.repo = argv[++i];
    } else if (arg === "--json") {
      opts.json = true;
    } else if (arg === "--help" || arg === "-h") {
      console.log(
        "Usage: node scripts/check-release-tag.mjs --tag vX.Y.Z[-desktop] --commit <40-hex> [--repo owner/name] [--json]",
      );
      process.exit(0);
    } else {
      throw new Error(`Unknown argument: ${arg}`);
    }
  }
  if (!opts.tag || !opts.commit) {
    throw new Error("Both --tag and --commit are required");
  }
  return opts;
}

if (
  process.argv[1] &&
  pathToFileURL(process.argv[1]).href === import.meta.url
) {
  try {
    const opts = parseArgs(process.argv.slice(2));
    const result = checkReleaseTag(opts);
    if (opts.json) {
      console.log(JSON.stringify(result, null, 2));
    } else {
      console.log(
        `PASS: ${result.tag} annotated tag ${result.tagObject} -> ${result.commit}`,
      );
    }
  } catch (err) {
    console.error(`ERROR: ${err.message}`);
    process.exit(1);
  }
}
