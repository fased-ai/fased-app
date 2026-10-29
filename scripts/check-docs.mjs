import { execFileSync } from "node:child_process";
import { lstatSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const foundationFiles = [
  ".gitignore",
  "AGENTS.md",
  "CONTRIBUTING.md",
  "LICENSE",
  "NOTICE",
];
const toolingFiles = new Set([
  ".github/workflows/checks.yml",
  "scripts/check-docs.mjs",
  "scripts/check-docs.test.mjs",
]);

function supported(name) {
  return (
    foundationFiles.includes(name) ||
    toolingFiles.has(name) ||
    /^[^/]+\.md$/.test(name) ||
    /^docs\/.+\.mdx?$/.test(name) ||
    name === "docs/docs.json"
  );
}

function headings(text) {
  const slugs = new Set();
  for (const match of text.matchAll(/^#{1,6}\s+(.+?)\s*#*$/gm)) {
    const base = match[1]
      .toLowerCase()
      .replace(/[^\p{L}\p{N}_\-\s]/gu, "")
      .replace(/\s/g, "-");
    let slug = base;
    let suffix = 0;
    while (slugs.has(slug)) slug = `${base}-${++suffix}`;
    slugs.add(slug);
  }
  return slugs;
}

function pageTitle(text) {
  const front = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/.exec(text);
  const title = front?.[1].match(/^title:\s*(.+?)\s*$/m)?.[1];
  return title && title.replace(/^["']|["']$/g, "").trim();
}

// Main currently contains the licensing/documentation foundation only. Do not
// let this focused check certify application code without its own runtime CI.
export function validateFoundation(files) {
  return validate(files, false);
}

// Documentation-only evidence. Runtime acceptance belongs to the required
// application lanes, never this validator. Foundation mode stays strict.
export function validateDocumentation(files) {
  return validate(files, true);
}

function validate(files, documentationOnly) {
  const errors = [];
  const mintlify = files.has("docs/docs.json");
  function checkLink(name, target, sitePage = false) {
    if (/^(https?:|mailto:)/i.test(target)) {
      try {
        new URL(target);
      } catch {
        errors.push(`${name}: invalid URL ${target}`);
      }
      return;
    }
    if (
      /^[a-z][a-z\d+.-]*:/i.test(target) ||
      /^\/home\//.test(target) ||
      target.startsWith("//")
    ) {
      errors.push(`${name}: private or unsupported link ${target}`);
      return;
    }
    let decoded;
    try {
      decoded = decodeURIComponent(target);
    } catch {
      errors.push(`${name}: invalid URL encoding ${target}`);
      return;
    }
    const hash = decoded.indexOf("#");
    const fragment = hash < 0 ? "" : decoded.slice(hash + 1);
    const pathname = (hash < 0 ? decoded : decoded.slice(0, hash)).split(
      "?",
    )[0];
    let resolved = pathname
      ? path.posix.normalize(
          pathname.startsWith("/")
            ? (sitePage ? "docs/" : "") + pathname.slice(1)
            : path.posix.join(path.posix.dirname(name), pathname),
        )
      : name;
    if (
      resolved === ".." ||
      resolved.startsWith("../") ||
      (sitePage && !resolved.startsWith("docs/"))
    ) {
      errors.push(`${name}: link escapes repository ${target}`);
      return;
    }
    if (sitePage && !files.has(resolved) && !path.posix.extname(resolved)) {
      const candidates = [
        `${resolved}.md`,
        `${resolved}.mdx`,
        `${resolved}/index.md`,
        `${resolved}/index.mdx`,
      ].filter((p) => files.has(p));
      if (candidates.length > 1) {
        errors.push(`${name}: ambiguous target ${target}`);
        return;
      }
      if (candidates.length === 1) resolved = candidates[0];
    }
    if (!files.has(resolved)) errors.push(`${name}: missing target ${target}`);
    else if (
      fragment &&
      /\.mdx?$/.test(resolved) &&
      !headings(files.get(resolved)).has(fragment)
    )
      errors.push(`${name}: missing heading ${target}`);
  }
  if (mintlify) {
    try {
      const config = JSON.parse(files.get("docs/docs.json"));
      const walk = (value) => {
        if (!value || typeof value !== "object") return;
        for (const [key, child] of Object.entries(value)) {
          if (key === "pages" && Array.isArray(child)) {
            for (const page of child) {
              if (typeof page === "string")
                checkLink("docs/docs.json", page, true);
              else walk(page);
            }
          } else if (
            ["href", "destination", "favicon"].includes(key) &&
            typeof child === "string"
          )
            checkLink("docs/docs.json", child, true);
          else if (key === "logo") {
            for (const logo of typeof child === "string"
              ? [child]
              : Object.values(child ?? {}))
              if (typeof logo === "string")
                checkLink("docs/docs.json", logo, true);
          } else walk(child);
        }
      };
      walk(config);
    } catch {
      errors.push("docs/docs.json: invalid JSON");
    }
  }
  for (const name of [...foundationFiles, "docs/README.md"]) {
    if (!files.has(name)) errors.push(`missing foundation file ${name}`);
  }
  for (const [name, text] of files) {
    if (!supported(name)) {
      if (!documentationOnly)
        errors.push(
          `unsupported tracked path ${name}: add its owning checks before admitting application source`,
        );
      continue;
    }
    if (!/\.mdx?$/.test(name)) continue;
    const sitePage = mintlify && name.startsWith("docs/") && !!pageTitle(text);
    if (!/^#\s+\S/m.test(text) && !sitePage)
      errors.push(`${name}: missing top-level heading`);
    if (!text.endsWith("\n")) errors.push(`${name}: missing final newline`);
    if (/^(?:<{7}|={7}|>{7})(?:\s|$)/m.test(text))
      errors.push(`${name}: merge conflict marker`);
    for (const [index, line] of text.split("\n").entries()) {
      if (/[\t ]+$/.test(line))
        errors.push(`${name}:${index + 1}: trailing whitespace`);
    }
    for (const match of text.matchAll(
      /!?\[[^\]\n]*\]\(([^\s)]+)(?:\s+"[^"]*")?\)/g,
    )) {
      checkLink(name, match[1], sitePage);
    }
    for (const match of text.matchAll(/\b(?:href|src)=["']([^"']+)["']/g))
      checkLink(name, match[1], sitePage);
  }
  return errors;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const args = process.argv.slice(2);
  if (args.length && (args.length !== 1 || args[0] !== "--documentation"))
    throw new Error("Expected --documentation or no arguments");
  const documentationOnly = args[0] === "--documentation";
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const names = execFileSync("git", ["ls-files", "--cached", "-z"], {
    cwd: root,
    encoding: "utf8",
  })
    .split("\0")
    .filter(Boolean);
  const files = new Map();
  for (const name of names) {
    if (!supported(name)) {
      files.set(name, "");
      continue;
    }
    const absolute = path.join(root, name);
    if (!lstatSync(absolute).isFile())
      throw new Error(`Expected an ordinary tracked file: ${name}`);
    files.set(name, readFileSync(absolute, "utf8"));
  }
  const errors = documentationOnly
    ? validateDocumentation(files)
    : validateFoundation(files);
  if (errors.length) {
    console.error(errors.join("\n"));
    process.exitCode = 1;
  } else {
    console.log(
      documentationOnly
        ? "PASS: root/docs documentation links, headings and formatting; runtime acceptance is separate."
        : `PASS: ${files.size} tracked foundation files; documentation links, headings, formatting and scope.`,
    );
  }
}
