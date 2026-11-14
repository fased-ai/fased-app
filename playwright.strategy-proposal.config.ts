import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
export default defineConfig({
  ...core,
  testMatch: "strategy-proposal.spec.ts",
  outputDir: ".evidence/f3-ai-proposal/browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/f3-ai-proposal/browser-results.json" }],
  ],
});
