import { defineConfig } from "@playwright/test";
import prototype from "./playwright.config";
export default defineConfig({
  ...prototype,
  testMatch: ["core.spec.ts", "research-fixture.spec.ts"],
  grep: /one consented research fixture|saved AI request draft shares/,
  outputDir: ".evidence/f3-research-loop/coupled-browser",
  reporter: [
    ["list"],
    [
      "json",
      { outputFile: ".evidence/f3-research-loop/coupled-browser-results.json" },
    ],
  ],
  fullyParallel: false,
  workers: 1,
  webServer: undefined,
});
