import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./web/tests",
  testMatch: [
    "f3-research.spec.ts",
    "f3-library.spec.ts",
    "shared-terminal.spec.ts",
    "f2-terminal.spec.ts",
  ],
  outputDir: `.evidence/f3-research/${process.env.FASED_RESEARCH_RUN ?? "browser"}`,
  reporter: [
    ["list"],
    [
      "json",
      {
        outputFile: `.evidence/f3-research/${process.env.FASED_RESEARCH_RUN ?? "browser"}-results.json`,
      },
    ],
  ],
  workers: 2,
  timeout: 30000,
  use: {
    headless: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    launchOptions: { executablePath: process.env.FASED_TEST_BROWSER },
  },
  projects: ["wen", "fased"].flatMap((host) =>
    [1440, 390].map((width) => ({
      name: `${host}-${width}`,
      metadata: { host },
      use: {
        baseURL: `http://127.0.0.1:${host === "wen" ? 5186 : 5187}`,
        viewport: { width, height: 900 },
        isMobile: width === 390,
        hasTouch: width === 390,
      },
    })),
  ),
  webServer: [
    {
      command: "npm run dev -- --port 5186 --strictPort",
      cwd: "../wen",
      url: "http://127.0.0.1:5186",
      reuseExistingServer: false,
    },
    {
      command: "npm run dev -- --port 5187 --strictPort",
      url: "http://127.0.0.1:5187",
      reuseExistingServer: false,
    },
  ],
});
