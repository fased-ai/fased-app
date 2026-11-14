import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
const researchOrigin = process.env.WEN_RESEARCH_ORIGIN;
if (
  researchOrigin &&
  !/^http:\/\/127\.0\.0\.1:[1-9]\d{0,4}$/.test(researchOrigin)
)
  throw Error("Explicit local research origin required");
export default defineConfig({
  plugins: [react()],
  root: "web",
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: researchOrigin
      ? {
          "/api/mining/v1/research": {
            target: researchOrigin,
            changeOrigin: true,
            configure(proxy) {
              proxy.on("proxyReq", (req) => {
                req.removeHeader("origin");
                req.removeHeader("cookie");
                req.removeHeader("authorization");
              });
            },
          },
        }
      : undefined,
  },
  build: { outDir: "../dist", emptyOutDir: true },
  test: {
    include: ["src/**/*.test.ts"],
    reporters: ["default", "json"],
    outputFile: "../.evidence/unit-results.json",
  },
});
