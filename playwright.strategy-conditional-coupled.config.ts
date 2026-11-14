import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
export default defineConfig({
  ...core,
  testMatch: ["strategy-proposal.spec.ts", "strategy-revision.spec.ts"],
  outputDir: ".evidence/f3-conditional-proposals/coupled-browser",
  reporter: [
    ["list"],
    [
      "json",
      {
        outputFile:
          ".evidence/f3-conditional-proposals/coupled-browser-results.json",
      },
    ],
  ],
});
