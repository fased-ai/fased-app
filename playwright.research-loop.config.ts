import { defineConfig } from "@playwright/test";
import prototype from "./playwright.config";
export default defineConfig({
  ...prototype,
  testMatch: "research-loop.spec.ts",
  outputDir: ".evidence/f3-research-loop/browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/f3-research-loop/browser-results.json" }],
  ],
  fullyParallel: false,
  workers: 1,
  webServer: undefined,
});
