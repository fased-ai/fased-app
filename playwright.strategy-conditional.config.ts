import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
export default defineConfig({
  ...core,
  testMatch: "strategy-conditional.spec.ts",
  outputDir: ".evidence/f3-conditional-proposals/browser",
  reporter: [
    ["list"],
    [
      "json",
      { outputFile: ".evidence/f3-conditional-proposals/browser-results.json" },
    ],
  ],
});
