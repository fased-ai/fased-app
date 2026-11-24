export type DesktopReleaseAssetView = {
  name: string;
  kind: "cli" | "desktop" | "installer" | "checksums";
  os: string;
  arch: string;
  url: string;
  sha256: string;
  bytes: number;
};

export type DesktopReleaseCheckView = {
  installedVersion: string;
  latestVersion: string;
  releaseTag: string;
  releaseUrl: string;
  updateAvailable: boolean;
  signed: boolean;
  platform: string;
  arch: string;
  assets: DesktopReleaseAssetView[];
  checkedAt: number;
};

const tagPrefix = "https://github.com/fased-ai/fased-app/releases/tag/";
const downloadPrefix =
  "https://github.com/fased-ai/fased-app/releases/download/";

export function readDesktopReleaseCheck(
  value: unknown,
): DesktopReleaseCheckView {
  const v = value as DesktopReleaseCheckView | null;
  if (
    !v ||
    typeof v.installedVersion !== "string" ||
    typeof v.latestVersion !== "string" ||
    !/^[0-9]+\.[0-9]+\.[0-9]+$/.test(v.latestVersion) ||
    v.releaseTag !== `v${v.latestVersion}-desktop` ||
    typeof v.releaseUrl !== "string" ||
    !v.releaseUrl.startsWith(tagPrefix) ||
    typeof v.updateAvailable !== "boolean" ||
    typeof v.signed !== "boolean" ||
    typeof v.platform !== "string" ||
    typeof v.arch !== "string" ||
    !Array.isArray(v.assets) ||
    !Number.isSafeInteger(v.checkedAt) ||
    v.checkedAt < 0
  ) {
    throw new Error("Could not check desktop release feed.");
  }
  const downloadTagPrefix = `${downloadPrefix}${v.releaseTag}/`;
  for (const a of v.assets) {
    if (
      !a ||
      typeof a.name !== "string" ||
      a.name.length === 0 ||
      !["cli", "desktop", "installer", "checksums"].includes(a.kind) ||
      typeof a.os !== "string" ||
      typeof a.arch !== "string" ||
      typeof a.url !== "string" ||
      !a.url.startsWith(downloadTagPrefix) ||
      typeof a.sha256 !== "string" ||
      !/^[a-f0-9]{64}$/.test(a.sha256) ||
      !Number.isSafeInteger(a.bytes) ||
      a.bytes <= 0
    ) {
      throw new Error("Could not check desktop release feed.");
    }
  }
  return v;
}

export function formatByteSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  if (bytes >= 1024 * 1024) {
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }
  if (bytes >= 1024) {
    return `${Math.round(bytes / 1024)} KB`;
  }
  return `${bytes} B`;
}
