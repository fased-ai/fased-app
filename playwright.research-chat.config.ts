import { defineConfig } from "@playwright/test";
import data from "./playwright.data-core.config";
const evidence =
  process.env.FASED_RESEARCH_CHAT_EVIDENCE ?? ".evidence/f3-research-chat";
const run = process.env.FASED_RESEARCH_CHAT_RUN ?? "browser";
export default defineConfig({
  ...data,
  testMatch: "research-chat.spec.ts",
  outputDir: `${evidence}/${run}`,
  reporter: [
    ["list"],
    ["json", { outputFile: `${evidence}/${run}-results.json` }],
  ],
});
