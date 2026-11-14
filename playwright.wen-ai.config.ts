import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
const root = process.env.FASED_WEN_AI_ROOT ?? ".evidence/f3-manual-ai";
export default defineConfig({
  ...core,
  testMatch: "wen-manual-ai.spec.ts",
  outputDir: `${root}/browser`,
  reporter: [
    ["list"],
    ["json", { outputFile: `${root}/browser-results.json` }],
  ],
});
