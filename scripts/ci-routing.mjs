import { execFileSync } from "node:child_process";
import { appendFileSync, mkdirSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

export const lanes = [
  "core",
  "signer",
  "handoff",
  "handoff_uid",
  "inspection",
  "vm",
];

const releaseConfig = "release/ubuntu.json";
const versionLine =
  /^  "version": "((?:0|[1-9][0-9]{0,5})(?:\.(?:0|[1-9][0-9]{0,5})){2})",$/gm;

function versionOnly(base, head) {
  try {
    const change = execFileSync(
      "git",
      ["diff", "--raw", "--no-renames", base, head, "--", releaseConfig],
      { encoding: "utf8" },
    );
    if (
      !/^:100644 100644 [a-f0-9]+ [a-f0-9]+ M\trelease\/ubuntu\.json\n$/.test(
        change,
      )
    )
      return false;

    // Latin-1 preserves every byte, including bytes outside the version line.
    const read = (ref) =>
      execFileSync("git", ["show", `${ref}:${releaseConfig}`]).toString(
        "latin1",
      );
    const before = read(base);
    const after = read(head);
    const oldLines = [...before.matchAll(versionLine)];
    const newLines = [...after.matchAll(versionLine)];
    if (oldLines.length !== 1 || newLines.length !== 1) return false;
    const order = (version) =>
      version
        .split(".")
        .map((part) => part.padStart(6, "0"))
        .join("");
    return (
      order(newLines[0][1]) > order(oldLines[0][1]) &&
      before.replace(versionLine, () => newLines[0][0]) === after
    );
  } catch {
    return false;
  }
}

export function classify(paths) {
  const plan = Object.fromEntries(lanes.map((lane) => [lane, false]));
  let scope = "none";
  const full = () => ({
    ...Object.fromEntries(lanes.map((lane) => [lane, true])),
    scope: "full",
  });
  if (!paths.length) return full();
  for (const file of paths) {
    if (!file || file.includes("\n") || file.includes("\r")) return full();
    if (
      /^(README\.md|AGENTS\.md|LICENSE|NOTICE|docs\/.*\.mdx?)$/.test(file) ||
      /^(docs\/docs\.json|docs\/\.mintignore)$/.test(file) ||
      /^docs\/assets\/[\w./-]+\.(svg|png|jpe?g|webp|ico|txt)$/.test(file)
    )
      continue;
    if (
      /^internal\/core\/(rpc_check|rpc_public|rpc_authenticated)(_test)?\.go$/.test(
        file,
      )
    ) {
      plan.core = true;
      scope = scope === "ui" || scope === "full" ? "full" : "rpc";
      continue;
    }
    if (/^web\/(src|tests)\//.test(file)) {
      plan.core = true;
      // A mixed browser/backend change keeps full core regression coverage.
      scope = scope === "rpc" || scope === "full" ? "full" : "ui";
      continue;
    }
    return full();
  }
  return { ...plan, scope };
}
export function verifyGate(needs) {
  if (needs.documentation?.result !== "success")
    throw new Error("documentation must pass");
  if (needs.classify?.result !== "success")
    throw new Error("classification must pass");
  const outputs = needs.classify.outputs;
  if (
    !outputs ||
    !["none", "rpc", "ui", "full", "version"].includes(outputs.scope)
  )
    throw new Error("invalid scope");
  if (
    outputs.scope === "version" &&
    lanes.some((lane) => outputs[lane] !== "false")
  )
    throw new Error("invalid version-only selection");
  if (outputs.signer === "true" && outputs.scope !== "full")
    throw new Error("signer checks require full scope");
  if (
    needs["core-scenarios"]?.result !==
    (outputs.scope === "full" ? "success" : "skipped")
  )
    throw new Error("required result missing: core-scenarios");
  for (const [job, flag] of [
    ["core", "core"],
    ["browser", "core"],
    ["signer", "signer"],
    ["handoff", "handoff"],
    ["handoff-uid", "handoff_uid"],
    ["inspection-installed", "inspection"],
  ]) {
    const selected = outputs[flag];
    if (!["true", "false"].includes(selected))
      throw new Error("invalid selection");
    if (needs[job]?.result !== (selected === "true" ? "success" : "skipped"))
      throw new Error(`required result missing: ${job}`);
  }
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  if (process.argv[2] === "gate") {
    verifyGate(JSON.parse(process.env.CI_NEEDS));
  } else if (process.argv[2] === "plan") {
    const { BASE_SHA: base, HEAD_SHA: head, FULL_RUN: force } = process.env;
    let plan = classify([]);
    if (
      force !== "true" &&
      /^[a-f0-9]{40}$/.test(base ?? "") &&
      /^[a-f0-9]{40}$/.test(head ?? "") &&
      !/^0+$/.test(base)
    ) {
      // Full PR diff, including deletions and both sides of renames. Never use
      // only the last commit or unverified prior-run evidence to skip coverage.
      const mergeBase = execFileSync("git", ["merge-base", base, head], {
        encoding: "utf8",
      }).trim();
      const raw = execFileSync("git", [
        "diff",
        "--name-only",
        "--no-renames",
        "-z",
        mergeBase,
        head,
      ]);
      const paths = raw.toString("utf8").split("\0").filter(Boolean);
      plan = classify(paths);
      if (
        mergeBase === base &&
        paths.length === 1 &&
        paths[0] === releaseConfig &&
        versionOnly(base, head)
      ) {
        plan = {
          ...Object.fromEntries(lanes.map((lane) => [lane, false])),
          scope: "version",
        };
      }
    }
    mkdirSync(".evidence", { recursive: true });
    writeFileSync(
      ".evidence/ci-routing.json",
      JSON.stringify({ base, head, plan }, null, 2),
    );
    if (process.env.GITHUB_OUTPUT)
      for (const [key, value] of Object.entries(plan))
        appendFileSync(process.env.GITHUB_OUTPUT, `${key}=${value}\n`);
    console.log(JSON.stringify(plan));
  } else throw new Error("expected plan or gate");
}
