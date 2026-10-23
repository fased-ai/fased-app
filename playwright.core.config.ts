import { defineConfig } from "@playwright/test";
import prototype from "./playwright.config";
export default defineConfig({
  ...prototype,
  testMatch: [
    "core.spec.ts",
    "research-ai.spec.ts",
    "research-fixture.spec.ts",
    "research-loop.spec.ts",
    "session-rehearsal.spec.ts",
    "session-automatic.spec.ts",
  ],
  outputDir: ".evidence/core-browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/core-browser-results.json" }],
  ],
  fullyParallel: false,
  workers: 1,
  webServer: undefined,
});
