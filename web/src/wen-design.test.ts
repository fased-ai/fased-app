import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

it("uses the pinned canonical WEN presentation artifact and approved semantic tokens", () => {
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
  expect(host).not.toMatch(/--wt-[\w-]+\s*:/);
  expect(host).toContain("prefers-reduced-motion");
});
