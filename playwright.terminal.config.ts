import { defineConfig } from "@playwright/test";
const widths = [
  { name: "desktop", width: 1440, height: 900 },
  { name: "tablet", width: 1024, height: 768 },
  { name: "mobile", width: 390, height: 844 },
];
export default defineConfig({
  testDir: "./web/tests",
  outputDir: ".evidence/f2-terminal/browser",
  reporter: [
    ["list"],
    ["json", { outputFile: ".evidence/f2-terminal/browser-results.json" }],
  ],
  fullyParallel: true,
  workers: 2,
  timeout: 30000,
  use: {
    headless: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    launchOptions: process.env.FASED_TEST_BROWSER
      ? { executablePath: process.env.FASED_TEST_BROWSER }
      : undefined,
  },
  projects: ["wen", "fased"].flatMap((host) =>
    widths.map((size) => ({
      name: `${host}-${size.name}`,
      metadata: { host },
      testMatch:
        host === "fased"
          ? [
              "shared-terminal.spec.ts",
              "f2-terminal.spec.ts",
              "prototype.spec.ts",
              "landing-terminal.spec.ts",
            ]
          : ["shared-terminal.spec.ts", "landing-terminal.spec.ts"],
      use: {
        baseURL: `http://127.0.0.1:${host === "wen" ? 5186 : 5187}`,
        viewport: { width: size.width, height: size.height },
        isMobile: size.name === "mobile",
        hasTouch: size.name === "mobile",
      },
    })),
  ),
  webServer: [
    {
      command: "npm run dev -- --port 5186 --strictPort",
      cwd: "../wen",
      url: "http://127.0.0.1:5186",
      reuseExistingServer: false,
      timeout: 30000,
    },
    {
      command: "npm run dev -- --port 5187 --strictPort",
      url: "http://127.0.0.1:5187",
      reuseExistingServer: false,
      timeout: 30000,
    },
    {
      command:
        "python3 -m http.server 5188 --bind 127.0.0.1 --directory dist-landing",
      cwd: "../wen",
      url: "http://127.0.0.1:5188",
      reuseExistingServer: false,
      timeout: 10000,
    },
    {
      command: "python3 -m http.server 5189 --bind 127.0.0.1 --directory dist",
      cwd: "../fased_landing",
      url: "http://127.0.0.1:5189",
      reuseExistingServer: false,
      timeout: 10000,
    },
  ],
});
