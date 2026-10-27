import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
export default defineConfig({
  ...core,
  testMatch: "session-rehearsal.spec.ts",
  outputDir: ".evidence/f4-rehearsal/browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/f4-rehearsal/browser-results.json" }],
  ],
});
