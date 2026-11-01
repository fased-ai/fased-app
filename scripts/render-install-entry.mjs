// Offline publisher/test tooling. It publishes nothing and creates no credentials.
import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const sha = (value) => createHash("sha256").update(value).digest("hex");
const platforms = JSON.parse(
  readFileSync(
    new URL("../internal/hostplatform/platforms.json", import.meta.url),
    "utf8",
  ),
);
const shell = (value) => "'" + String(value).replaceAll("'", "'\\''") + "'";
function targetValues(config) {
  const { version, installer, seed, fixture = false, ca } = config;
  if (
    !/^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$/.test(version ?? "") ||
    !installer?.length ||
    !seed?.length
  )
    throw Error("Version, exact verifier bytes and public trust seed required");
  const trust = JSON.parse(Buffer.from(seed).toString());
  if (
    trust.repository !== "fased-ai/fased-app" ||
    trust.target?.os !== "linux" ||
    !["amd64", "arm64"].includes(trust.target?.arch)
  )
    throw Error("Wrong installer trust identity");
  const url = fixture
    ? config.url
    : `https://github.com/fased-ai/fased-app/releases/download/v${version}/fased-install-linux-${trust.target.arch}`;
  if (
    fixture
      ? !/^https:\/\/127\.0\.0\.1:[0-9]+\/fased-install$/.test(url ?? "") ||
        !ca?.length ||
        trust.version !== 1 ||
        !trust.developmentOnly
      : config.url !== undefined ||
        ca !== undefined ||
        trust.version !== 3 ||
        trust.developmentOnly !== false ||
        trust.channel !== "stable" ||
        !trust.provenance
  )
    throw Error("Wrong public/controlled installer profile");
  const values = {
    ENTRY_VERSION: version,
    INSTALLER_URL: url,
    INSTALLER_SHA256: sha(installer),
    SEED_BASE64: Buffer.from(seed).toString("base64"),
    SEED_SHA256: sha(seed),
    FIXTURE_CA_BASE64: fixture ? Buffer.from(ca).toString("base64") : "",
    DEVELOPMENT_ONLY: fixture ? "1" : "0",
  };
  return { values, arch: trust.target.arch };
}
export function renderEntry(template, config) {
  const entries = (config.targets ?? [config]).map((target) =>
    targetValues({ ...config, ...target, targets: undefined }),
  );
  if (
    !entries.length ||
    new Set(entries.map((e) => e.arch)).size !== entries.length
  )
    throw Error("Unique authenticated targets required");
  const values = { ...entries[0].values };
  if (
    entries.some(
      ({ values: v }) =>
        v.ENTRY_VERSION !== values.ENTRY_VERSION ||
        v.DEVELOPMENT_ONLY !== values.DEVELOPMENT_ONLY,
    )
  )
    throw Error("Targets must share one release/trust profile");
  values.TARGET_BINDINGS =
    'case "$target" in\n' +
    entries
      .map(
        ({ values: v, arch }) =>
          `linux-${arch})\n` +
          [
            "INSTALLER_URL",
            "INSTALLER_SHA256",
            "SEED_BASE64",
            "SEED_SHA256",
            "FIXTURE_CA_BASE64",
          ]
            .map((key) => `  ${key.toLowerCase()}=${shell(v[key])}`)
            .join("\n") +
          "\n;;",
      )
      .join("\n") +
    '\n*) die "No authenticated installer package for $target." ;;\nesac';
  values.PLATFORM_BINDINGS =
    'case "$ID:$VERSION_ID" in\n' +
    platforms
      .map((p) => {
        if (!/^[a-z]+$/.test(p.id) || !/^[0-9]+\.[0-9]+$/.test(p.version))
          throw Error("Invalid platform mapping");
        return `${p.id}:${p.version}) ;;`;
      })
      .join("\n") +
    '\n*) die "This OS version has no reviewed mapping. Update the installer for newly supported versions." ;;\nesac';
  values.REMOTE_PLATFORMS =
    'case "$ID:$VERSION_ID" in\n' +
    platforms
      .filter((p) => p.remote)
      .map((p) => `${p.id}:${p.version}) ;;`)
      .join("\n") +
    '\n*) die "This release supports Local; Remote requires a supported Ubuntu release and update authority." ;;\nesac';
  const result = template.replace(/@([A-Z0-9_]+)@/g, (_, key) => {
    if (!(key in values)) throw Error("Unknown installer stamp");
    return values[key];
  });
  if (/@[A-Z0-9_]+@/.test(result)) throw Error("Unstamped installer");
  return result;
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  if (process.argv.length !== 4)
    throw Error(
      "Usage: node scripts/render-install-entry.mjs CONFIG_JSON OUTPUT",
    );
  const config = JSON.parse(await readFile(process.argv[2], "utf8"));
  const loadTarget = async (target) => ({
    ...target,
    installer: await readFile(target.installer),
    seed: await readFile(target.seed),
    ...(target.ca ? { ca: await readFile(target.ca) } : {}),
  });
  const loaded = config.targets
    ? {
        ...config,
        targets: await Promise.all(
          config.targets.map((target) =>
            loadTarget({ ...config, ...target, targets: undefined }),
          ),
        ),
      }
    : await loadTarget(config);
  const result = renderEntry(
    await readFile(new URL("../install.sh", import.meta.url), "utf8"),
    loaded,
  );
  await writeFile(process.argv[3], result, { mode: 0o500, flag: "wx" });
}
