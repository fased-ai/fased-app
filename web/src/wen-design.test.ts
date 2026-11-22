import { createHash } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";

function collectCssFiles(dir: string): string[] {
  const results: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "vendor") continue;
      results.push(...collectCssFiles(full));
    } else if (entry.name.endsWith(".css")) {
      results.push(full);
    }
  }
  return results;
}

it("preserves the pinned WEN artifact while using the approved reference palette", () => {
  const root = new URL("./vendor/wen-design/", import.meta.url);
  const manifest = JSON.parse(
    readFileSync(new URL("manifest.json", root), "utf8"),
  );
  const css = readFileSync(new URL("style.css", root));
  const digest = createHash("sha256").update(css).digest("hex");
  expect(manifest.schema).toBe("wen-design-artifact/1");
  expect(manifest.version).toBe("0.1.0");
  expect(manifest.files["style.css"]).toBe(digest);
  expect(manifest.sourceSHA256).toBe(digest);
  expect(manifest.source).toBe("wen/packages/strategy-terminal/src/style.css");
  const tokens = {
    bg: "#15141C",
    surface: "#1E1D29",
    raised: "#292738",
    border: "#373447",
    text: "#F3F2F7",
    muted: "#B7B2C8",
    coral: "#FF647C",
    cyan: "#27C5DA",
    violet: "#9A7AFF",
  };
  for (const [key, value] of Object.entries(tokens)) {
    expect(css.toString()).toContain(`--wt-${key}:${value}`);
  }
  const host = readFileSync(new URL("./wen-app.css", import.meta.url), "utf8");
  expect(host).not.toMatch(
    /--wt-(?:bg|surface|raised|border|text|muted|coral|cyan|violet):/,
  );
  expect(css.toString()).toContain("prefers-reduced-motion");
  expect(
    readFileSync(new URL("./styles.css", import.meta.url), "utf8"),
  ).toContain("prefers-reduced-motion");
});

it("uses WEN CSS custom properties across all non-vendor stylesheets instead of hardcoded colors", () => {
  const srcRoot = fileURLToPath(new URL(".", import.meta.url));
  const cssFiles = collectCssFiles(srcRoot);
  expect(cssFiles.length).toBeGreaterThanOrEqual(10);
  const violations: string[] = [];
  for (const file of cssFiles) {
    const stripped = readFileSync(file, "utf8")
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/:root(?:\.wt)?(?:\[data-theme="[^"]+"\])?\s*\{[^}]*\}/g, "");
    const match = /(?:#[0-9a-fA-F]{3,8}\b|\brgba?\()/i.exec(stripped);
    if (match) {
      violations.push(`${file.replace(srcRoot, "")}: ${match[0]}`);
    }
  }
  expect(violations).toEqual([]);
});
