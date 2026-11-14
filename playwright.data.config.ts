import { defineConfig } from "@playwright/test";
import research from "./playwright.research.config";
export default defineConfig({
  ...research,
  testMatch: "research-data.spec.ts",
  outputDir: ".evidence/f3-data-delivery/browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/f3-data-delivery/browser-results.json" }],
  ],
  webServer: [
    {
      command: "node ../wen/server/mining/research-fixture.mjs 7350",
      gracefulShutdown: { signal: "SIGTERM", timeout: 2000 },
      url: "http://127.0.0.1:7350/api/mining/v1/research",
      reuseExistingServer: false,
    },
    ...(Array.isArray(research.webServer) ? research.webServer : []).map(
      (s) => ({
        ...s,
        env: { WEN_RESEARCH_ORIGIN: "http://127.0.0.1:7350" },
      }),
    ),
  ],
});
