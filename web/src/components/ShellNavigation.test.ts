import { describe, expect, it } from "vitest";
import { visibleShellPaths } from "./ShellNavigation";

describe("ShellNavigation WEN terminal isolation", () => {
  it("keeps terminals/satcoin hidden even when Stage 4 walletSigner and financialExecution are enabled", () => {
    const visible = visibleShellPaths(
      "ready",
      { walletSigner: true, financialExecution: true },
      false,
    );
    const paths = visible.map((p) => p.path);
    expect(paths).toContain("terminals/jupiter");
    expect(paths).toContain("terminals/hyperliquid");
    expect(paths).not.toContain("terminals/satcoin");
  });

  it("hides all live crypto and WEN terminals when Stage 4 flags are off", () => {
    const visible = visibleShellPaths(
      "ready",
      { walletSigner: false, financialExecution: false },
      false,
    );
    const paths = visible.map((p) => p.path);
    expect(paths).not.toContain("terminals/jupiter");
    expect(paths).not.toContain("terminals/hyperliquid");
    expect(paths).not.toContain("terminals/satcoin");
    expect(paths).toContain("trade");
    expect(paths).toContain("intel");
    expect(paths).toContain("lab");
  });
});
