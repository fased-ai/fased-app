import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
export default defineConfig({
  ...core,
  testMatch: "session-automatic.spec.ts",
  outputDir: ".evidence/f4-automatic/browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/f4-automatic/browser-results.json" }],
  ],
});
