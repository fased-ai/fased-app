import { createHash } from "node:crypto";
import { readdir, readFile, writeFile, mkdir } from "node:fs/promises";
import path from "node:path";
const omitted = new Set([".git", ".evidence", "node_modules", "dist"]);
async function walk(dir = ".") {
  const found = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (omitted.has(entry.name)) continue;
    const file = path.join(dir, entry.name);
    if (entry.isDirectory()) found.push(...(await walk(file)));
    else if (entry.isFile()) found.push(file);
  }
  return found.sort();
}
const sources = {};
for (const file of await walk())
  sources[file] = createHash("sha256")
    .update(await readFile(file))
    .digest("hex");
await mkdir(".evidence", { recursive: true });
await writeFile(
  ".evidence/source-inputs.json",
  JSON.stringify(
    {
      createdAt: new Date().toISOString(),
      sources,
      state:
        "Disposable browser fixtures only; no financial or user-owned runtime state.",
    },
    null,
    2,
  ) + "\n",
);
console.log(
  `Recorded ${Object.keys(sources).length} source/configuration hashes.`,
);
