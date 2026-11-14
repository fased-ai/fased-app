import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
const root =
  process.env.FASED_STRATEGY_REVISION_ROOT ?? ".evidence/f3-chat-revision";
const run = process.env.FASED_STRATEGY_REVISION_RUN ?? "browser";
export default defineConfig({
  ...core,
  testMatch: "strategy-revision.spec.ts",
  outputDir: `${root}/${run}`,
  reporter: [["list"], ["json", { outputFile: `${root}/${run}-results.json` }]],
});
