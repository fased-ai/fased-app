import { describe, expect, it } from "vitest";
import { formatByteSize, readDesktopReleaseCheck } from "./desktop-release";

function validView() {
  return {
    installedVersion: "0.1.36",
    latestVersion: "0.1.37",
    releaseTag: "v0.1.37-desktop",
    releaseUrl:
      "https://github.com/fased-ai/fased-app/releases/tag/v0.1.37-desktop",
    updateAvailable: true,
    signed: false,
    platform: "darwin",
    arch: "arm64",
    assets: [
      {
        name: "fased-app-0.1.37-macos-arm64.dmg",
        kind: "desktop",
        os: "darwin",
        arch: "arm64",
        url: "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/fased-app-0.1.37-macos-arm64.dmg",
        sha256: "b".repeat(64),
        bytes: 42_000_000,
      },
      {
        name: "SHA256SUMS.txt",
        kind: "checksums",
        os: "all",
        arch: "all",
        url: "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/SHA256SUMS.txt",
        sha256: "c".repeat(64),
        bytes: 1200,
      },
    ],
    checkedAt: 1795400000,
  };
}

describe("readDesktopReleaseCheck", () => {
  it("accepts a valid GitHub companion release view", () => {
    const parsed = readDesktopReleaseCheck(validView());
    expect(parsed.latestVersion).toBe("0.1.37");
    expect(parsed.assets).toHaveLength(2);
    expect(formatByteSize(42 * 1024 * 1024)).toBe("42.0 MB");
  });

  it("rejects non-GitHub URLs, mismatched tags, and bad digests", () => {
    const evilUrl = validView();
    evilUrl.assets[0].url =
      "https://evil.example/releases/download/v0.1.37-desktop/fased-app.dmg";
    expect(() => readDesktopReleaseCheck(evilUrl)).toThrow();

    const evilTagUrl = validView();
    evilTagUrl.releaseUrl = "https://evil.example/tag/v0.1.37-desktop";
    expect(() => readDesktopReleaseCheck(evilTagUrl)).toThrow();

    const badHash = validView();
    badHash.assets[0].sha256 = "not-a-hash";
    expect(() => readDesktopReleaseCheck(badHash)).toThrow();

    const badTag = validView();
    badTag.releaseTag = "v0.1.37";
    expect(() => readDesktopReleaseCheck(badTag)).toThrow();
  });
});
