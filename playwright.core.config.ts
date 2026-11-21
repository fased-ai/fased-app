import { defineConfig } from "@playwright/test";
import prototype from "./playwright.config";
export default defineConfig({
  ...prototype,
  testMatch: [
    "core-auth-owner.spec.ts",
    "core-providers-signin.spec.ts",
    "core-chat-drafts.spec.ts",
    "core-tasks-schedules.spec.ts",
    "core-memory-evidence.spec.ts",
    "core-news-playbooks.spec.ts",
    "core-updates-custody.spec.ts",
    "core-shell-dashboard-i18n.spec.ts",
    "research-ai.spec.ts",
    "research-fixture.spec.ts",
    "research-loop.spec.ts",
    "session-rehearsal.spec.ts",
    "session-automatic.spec.ts",
    "strategies.spec.ts",
    "onchain.spec.ts",
    "wallet-read.spec.ts",
    "wen-mining.spec.ts",
  ],
  outputDir: ".evidence/core-browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/core-browser-results.json" }],
  ],
  fullyParallel: true,
  // Worker-local fixtures use unique temporary directories and loopback ports.
  workers: 4,
  webServer: undefined,
});
