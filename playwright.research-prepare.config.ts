import { defineConfig } from "@playwright/test";
import prototype from "./playwright.config";
export default defineConfig({
  ...prototype,
  testMatch: "research-ai.spec.ts",
  outputDir: ".evidence/f3-research-prepare/browser",
  reporter: [
    ["list"],
    [
      "json",
      { outputFile: ".evidence/f3-research-prepare/browser-results.json" },
    ],
  ],
  fullyParallel: false,
  workers: 1,
  webServer: undefined,
});
