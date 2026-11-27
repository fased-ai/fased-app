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

it("preserves the pinned WEN Muted Pro artifact and rejects stale purple or Obsidian tokens", () => {
  const root = new URL("./vendor/wen-design/", import.meta.url);
  const manifest = JSON.parse(
    readFileSync(new URL("manifest.json", root), "utf8"),
  );
  const css = readFileSync(new URL("style.css", root));
  const digest = createHash("sha256").update(css).digest("hex");
  expect(manifest.schema).toBe("wen-design-artifact/2");
  expect(manifest.files["style.css"]).toBe(digest);
  expect(manifest.sourceSHA256).toBe(digest);
  expect(manifest.source).toBe("wen/src/features/mining/retail-theme.css");

  const tokens = {
    bg: "#0A0C10",
    surface: "#0F1218",
    raised: "#161A22",
    "raised-2": "#1D222C",
    text: "#E6E9F0",
    muted: "#868FA2",
    coral: "#E5476A",
    cyan: "#33A9B8",
  };
  const cssText = css.toString();
  for (const [key, value] of Object.entries(tokens)) {
    expect(cssText).toContain(`--wt-${key}: ${value}`);
  }
  expect(cssText).not.toMatch(/#15141C|#343144|#9A7AFF|#ff4f7c|#0d0d12/i);
  expect(cssText).toContain("prefers-reduced-motion");
  expect(
    readFileSync(new URL("./styles.css", import.meta.url), "utf8"),
  ).toContain("prefers-reduced-motion");

  for (const hostFile of ["./wen-app.css", "./shell.css"]) {
    const host = readFileSync(new URL(hostFile, import.meta.url), "utf8");
    expect(host).not.toMatch(
      /--wt-(?:bg|surface|raised|raised-2|border|text|muted|coral|cyan|violet):/,
    );
  }
});

it("keeps shared primitives single-sourced in primitives.css with zero hardcoded colors or undefined vars", () => {
  const srcRoot = fileURLToPath(new URL(".", import.meta.url));
  const cssFiles = collectCssFiles(srcRoot);
  expect(cssFiles.length).toBeGreaterThanOrEqual(10);

  const singleOwnedSelectors = [
    ".wt-wpanel",
    ".wt-kpi-box",
    ".wt-tabs-underline",
    ".wt-hairline-table",
    ".wt-btn-rose",
  ];
  const colorViolations: string[] = [];
  const collisionViolations: string[] = [];
  const undefVarViolations: string[] = [];

  for (const file of cssFiles) {
    const rel = file.replace(srcRoot, "");
    const raw = readFileSync(file, "utf8");
    const stripped = raw
      .replace(/\/\*[\s\S]*?\*\//g, "")
      .replace(/:root(?:\.wt)?(?:\[data-theme="[^"]+"\])?\s*\{[^}]*\}/g, "");
    const colorMatch = /(?:#[0-9a-fA-F]{3,8}\b|\brgba?\()/i.exec(stripped);
    if (colorMatch) {
      colorViolations.push(`${rel}: ${colorMatch[0]}`);
    }
    if (/var\(--danger\b/.test(stripped)) {
      undefVarViolations.push(`${rel}: var(--danger)`);
    }
    if (rel !== "primitives.css") {
      for (const sel of singleOwnedSelectors) {
        const escaped = sel.replace(".", "\\.");
        if (new RegExp(`(^|\\s)${escaped}\\s*\\{`, "m").test(stripped)) {
          collisionViolations.push(`${rel}: redefines ${sel}`);
        }
      }
    }
  }

  expect(colorViolations).toEqual([]);
  expect(undefVarViolations).toEqual([]);
  expect(collisionViolations).toEqual([]);
});
