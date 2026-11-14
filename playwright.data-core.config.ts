import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
export default defineConfig({
  ...core,
  testMatch: "research-data-core.spec.ts",
  outputDir: ".evidence/f3-data-delivery/core-browser",
  reporter: [
    ["list"],
    [
      "json",
      { outputFile: ".evidence/f3-data-delivery/core-browser-results.json" },
    ],
  ],
  webServer: {
    command: "node ../wen/server/mining/research-fixture.mjs 7352",
    gracefulShutdown: { signal: "SIGTERM", timeout: 2000 },
    url: "http://127.0.0.1:7352/api/mining/v1/research",
    reuseExistingServer: false,
  },
});
