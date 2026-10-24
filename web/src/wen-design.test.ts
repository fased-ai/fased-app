import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

it("preserves the pinned WEN artifact while applying the approved R1 workspace tokens", () => {
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
  const workspaceTokens = {
    bg: "#101016",
    surface: "#191923",
    raised: "#22222e",
    border: "#2b2b38",
    text: "#e8e7ed",
    muted: "#93929f",
    coral: "#ff682f",
    cyan: "#3ec8bc",
    violet: "#9a82ed",
  };
  for (const [key, value] of Object.entries(workspaceTokens))
    expect(host).toMatch(new RegExp(`--wt-${key}:\\s*${value}`));
  expect(css.toString()).toContain("prefers-reduced-motion");
  expect(
    readFileSync(new URL("./styles.css", import.meta.url), "utf8"),
  ).toContain("prefers-reduced-motion");
});
