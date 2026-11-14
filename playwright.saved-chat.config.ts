import { defineConfig } from "@playwright/test";
import core from "./playwright.core.config";
const run = process.env.FASED_SAVED_CHAT_RUN ?? "browser";
export default defineConfig({
  ...core,
  testMatch: "saved-chat.spec.ts",
  outputDir: `.evidence/f3-saved-chat/${run}`,
  reporter: [
    ["list"],
    ["json", { outputFile: `.evidence/f3-saved-chat/${run}-results.json` }],
  ],
});
