import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  root: "web",
  server: { host: "127.0.0.1", port: 5173, strictPort: true },
  build: { outDir: "../dist", emptyOutDir: true },
  test: {
    include: ["src/**/*.test.ts"],
    reporters: ["default", "json"],
    outputFile: "../.evidence/unit-results.json",
  },
});
