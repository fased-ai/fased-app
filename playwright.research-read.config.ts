import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
export default defineConfig({
  ...core,
  testMatch: "research-read.spec.ts",
  outputDir: ".evidence/f3-research-read/browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/f3-research-read/browser-results.json" }],
  ],
});
