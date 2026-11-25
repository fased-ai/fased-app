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
const psString = (value) => "'" + String(value).replaceAll("'", "''") + "'";
const validVersion = (version) =>
  /^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$/.test(version ?? "");

function darwinTargetValues(config) {
  const { version, fixture = false, targets, ca } = config;
  if (!Array.isArray(targets) || targets.length === 0) {
    throw Error("macOS targets must be a non-empty array when provided");
  }
  const seen = new Set();
  const resolved = targets.map((t) => {
    const arch = t?.arch;
    if (!["arm64", "amd64"].includes(arch) || seen.has(arch)) {
      throw Error("Unique supported macOS targets (arm64/amd64) required");
    }
    seen.add(arch);
    if (!t.archive?.length) {
      throw Error("Exact macOS archive bytes required");
    }
    const expectedUrl = `https://github.com/fased-ai/fased-app/releases/download/v${version}/fased-app-darwin-${arch}.zip`;
    if (fixture) {
      if (
        !/^https:\/\/127\.0\.0\.1:[0-9]+\//.test(t.url ?? "") ||
        !(t.ca?.length || ca?.length)
      ) {
        throw Error("Wrong controlled macOS fixture profile");
      }
    } else if (t.url !== undefined || t.ca !== undefined) {
      throw Error("Public macOS entry cannot override URL or CA");
    }
    return {
      arch,
      url: fixture ? t.url : expectedUrl,
      sha256: sha(t.archive),
    };
  });
  return resolved;
}

function windowsTargetValues(config) {
  const { version, fixture = false } = config;
  if (!validVersion(version)) {
    throw Error("Valid release version required");
  }
  const rawTargets = config.targets ?? (config.archive ? [config] : []);
  if (!Array.isArray(rawTargets) || rawTargets.length === 0) {
    throw Error("Unique supported Windows targets (amd64/arm64) required");
  }
  const seen = new Set();
  return rawTargets.map((t) => {
    const arch = t?.arch;
    if (!["amd64", "arm64"].includes(arch) || seen.has(arch)) {
      throw Error("Unique supported Windows targets (amd64/arm64) required");
    }
    seen.add(arch);
    if (!t.archive?.length) {
      throw Error("Exact Windows archive bytes required");
    }
    const expectedUrl = `https://github.com/fased-ai/fased-app/releases/download/v${version}/fased-app-windows-${arch}.zip`;
    if (fixture) {
      if (!/^https:\/\/127\.0\.0\.1:[0-9]+\//.test(t.url ?? "")) {
        throw Error("Wrong controlled Windows fixture URL");
      }
    } else if (
      t.url !== undefined ||
      config.url !== undefined ||
      config.ca !== undefined ||
      t.ca !== undefined
    ) {
      throw Error("Public Windows entry cannot override URL or CA");
    }
    return {
      arch,
      url: fixture ? t.url : expectedUrl,
      sha256: sha(t.archive),
    };
  });
}

function targetValues(config) {
  const { version, installer, seed, fixture = false, ca } = config;
  if (!validVersion(version) || !installer?.length || !seed?.length)
    throw Error("Version, exact verifier bytes and public trust seed required");
  const trust = JSON.parse(Buffer.from(seed).toString());
  if (
    trust.repository !== "fased-ai/fased-app" ||
    trust.target?.os !== "linux" ||
    (trust.signingProfile !== undefined &&
      !(
        trust.signingProfile === "single-owner-v1" &&
        trust.version === 3 &&
        trust.developmentOnly === false
      ) &&
      !(
        trust.signingProfile === "github-attestation-v1" &&
        trust.version === 4 &&
        trust.developmentOnly === false &&
        trust.root === ""
      )) ||
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
        (trust.version !== 3 && trust.version !== 4) ||
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
  values.DARWIN_TARGET_BINDINGS =
    config.darwinTargets !== undefined
      ? 'case "$target" in\n' +
        darwinTargetValues({
          version: values.ENTRY_VERSION,
          fixture: Boolean(config.fixture),
          ca: config.ca,
          targets: config.darwinTargets,
        })
          .map(
            (t) =>
              `darwin-${t.arch})\n` +
              `  darwin_archive_url=${shell(t.url)}\n` +
              `  darwin_archive_sha256=${shell(t.sha256)}\n` +
              ";;",
          )
          .join("\n") +
        '\n*) die "No authenticated macOS package for $target." ;;\nesac'
      : 'case "$target" in\n*) die "No authenticated macOS package for $target." ;;\nesac';
  for (const p of platforms) {
    if (!/^[a-z]+$/.test(p.id) || !/^[0-9]+(?:\.[0-9]+)?$/.test(p.version))
      throw Error("Invalid platform mapping");
  }
  values.PLATFORM_BINDINGS =
    'case "$package_family" in\n' +
    "apt) [[ -x /usr/bin/apt-get && -x /usr/bin/dpkg-query ]] || die 'The apt package manager is unavailable.' ;;\n" +
    "dnf) [[ -x /usr/bin/dnf && -x /usr/bin/rpm ]] || die 'The dnf package manager is unavailable.' ;;\n" +
    "*) die 'No supported Linux package manager family was detected.' ;;\nesac";
  values.PLATFORM_TARGET_BINDINGS =
    'case "$ID:$VERSION_ID:$target" in\n' +
    platforms
      .flatMap((p) => {
        const arches = p.architectures ?? ["amd64", "arm64"];
        if (
          !Array.isArray(arches) ||
          arches.length === 0 ||
          new Set(arches).size !== arches.length ||
          arches.some((arch) => !["amd64", "arm64"].includes(arch))
        )
          throw Error("Invalid platform architecture mapping");
        return ["amd64", "arm64"]
          .filter((arch) => !arches.includes(arch))
          .map(
            (arch) =>
              `${p.id}:${p.version}:linux-${arch}) die "No reviewed package for this OS and CPU." ;;`,
          );
      })
      .join("\n") +
    "\n*) ;;\nesac";
  values.REMOTE_PLATFORMS =
    'case "$ID:$VERSION_ID" in\n' +
    platforms
      .filter((p) => p.remote)
      .map((p) => `${p.id}:${p.version}) ;;`)
      .join("\n") +
    '\n*) die "This release supports Local; Remote requires a supported Linux release (Ubuntu 24.04+, Debian 12/13, or Fedora 41-45) and update authority." ;;\nesac';
  const result = template.replace(/@([A-Z0-9_]+)@/g, (_, key) => {
    if (!(key in values)) throw Error("Unknown installer stamp");
    return values[key];
  });
  if (/@[A-Z0-9_]+@/.test(result)) throw Error("Unstamped installer");
  return result;
}

export function renderWindowsEntry(template, config) {
  const targets = windowsTargetValues(config);
  const values = {
    ENTRY_VERSION: config.version,
    DEVELOPMENT_ONLY: config.fixture ? "1" : "0",
    WINDOWS_TARGET_BINDINGS:
      "switch ($TargetArch) {\n" +
      targets
        .map(
          (t) =>
            `${psString(t.arch)} {\n` +
            `  $ArchiveUrl = ${psString(t.url)}\n` +
            `  $ArchiveSha256 = ${psString(t.sha256)}\n` +
            "}",
        )
        .join("\n") +
      '\ndefault { Fail "No authenticated Windows package for windows-$TargetArch." }\n}',
  };
  const result = template.replace(/@([A-Z0-9_]+)@/g, (_, key) => {
    if (!(key in values)) throw Error("Unknown Windows installer stamp");
    return values[key];
  });
  if (/@[A-Z0-9_]+@/.test(result)) throw Error("Unstamped Windows installer");
  return result;
}

function dualTrackCliTargetValues(config) {
  const { version, fixture = false, ca } = config;
  if (!validVersion(version)) {
    throw Error("Valid release version required");
  }
  const rawTargets = config.targets ?? [];
  if (!Array.isArray(rawTargets) || rawTargets.length === 0) {
    throw Error("Non-empty CLI targets array required");
  }
  const seen = new Set();
  return rawTargets.map((t) => {
    const os = t?.os;
    const arch = t?.arch;
    if (
      !["darwin", "linux"].includes(os) ||
      !["amd64", "arm64"].includes(arch)
    ) {
      throw Error("Supported CLI targets are darwin/linux amd64/arm64");
    }
    const key = `${os}-${arch}`;
    if (seen.has(key)) {
      throw Error("Duplicate CLI target: " + key);
    }
    seen.add(key);
    if (!t.archive?.length) {
      throw Error("Exact CLI archive bytes required");
    }
    const ext = os === "darwin" ? "zip" : "tar.gz";
    const expectedUrl = `https://github.com/fased-ai/fased-app/releases/download/v${version}/fased-app-${key}.${ext}`;
    if (fixture) {
      if (
        !/^https:\/\/127\.0\.0\.1:[0-9]+\//.test(t.url ?? "") ||
        !(t.ca?.length || ca?.length)
      ) {
        throw Error("Wrong controlled CLI fixture profile");
      }
    } else if (t.url !== undefined || t.ca !== undefined) {
      throw Error("Public CLI entry cannot override URL or CA");
    }
    return {
      key,
      ext,
      url: fixture ? t.url : expectedUrl,
      sha256: sha(t.archive),
    };
  });
}

export function renderDualTrackCliEntry(template, config) {
  const targets = dualTrackCliTargetValues(config);
  const caBytes = config.fixture
    ? (config.ca ??
      config.targets?.find((t) => t.ca?.length)?.ca ??
      Buffer.alloc(0))
    : Buffer.alloc(0);
  const values = {
    ENTRY_VERSION: config.version,
    DEVELOPMENT_ONLY: config.fixture ? "1" : "0",
    FIXTURE_CA_BASE64: config.fixture
      ? Buffer.from(caBytes).toString("base64")
      : "",
    CLI_TARGET_BINDINGS:
      'case "$target" in\n' +
      targets
        .map(
          (t) =>
            `${t.key})\n` +
            `  archive_url=${shell(t.url)}\n` +
            `  archive_sha256=${shell(t.sha256)}\n` +
            `  archive_ext=${shell(t.ext)}\n` +
            ";;",
        )
        .join("\n") +
      '\n*) die "No authenticated CLI package for $target." ;;\nesac',
  };
  const result = template.replace(/@([A-Z0-9_]+)@/g, (_, key) => {
    if (!(key in values)) throw Error("Unknown CLI installer stamp");
    return values[key];
  });
  if (/@[A-Z0-9_]+@/.test(result)) throw Error("Unstamped CLI installer");
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
  if (process.argv[3].endsWith("install-cli.sh")) {
    const loadCliTarget = async (target) => ({
      ...target,
      archive: await readFile(target.archive),
      ...(target.ca ? { ca: await readFile(target.ca) } : {}),
    });
    const loaded = {
      ...config,
      ...(config.ca ? { ca: await readFile(config.ca) } : {}),
      targets: await Promise.all((config.targets ?? []).map(loadCliTarget)),
    };
    const result = renderDualTrackCliEntry(
      await readFile(new URL("../install-cli.sh", import.meta.url), "utf8"),
      loaded,
    );
    await writeFile(process.argv[3], result, { mode: 0o500, flag: "wx" });
  } else if (process.argv[3].endsWith(".ps1")) {
    const loadWinTarget = async (target) => ({
      ...target,
      archive: await readFile(target.archive),
    });
    const loaded = config.targets
      ? {
          ...config,
          targets: await Promise.all(
            config.targets.map((target) =>
              loadWinTarget({ ...config, ...target, targets: undefined }),
            ),
          ),
        }
      : await loadWinTarget(config);
    const result = renderWindowsEntry(
      await readFile(new URL("../install.ps1", import.meta.url), "utf8"),
      loaded,
    );
    await writeFile(process.argv[3], result, { mode: 0o600, flag: "wx" });
  } else {
    const loadTarget = async (target) => ({
      ...target,
      installer: await readFile(target.installer),
      seed: await readFile(target.seed),
      ...(target.ca ? { ca: await readFile(target.ca) } : {}),
    });
    const loadDarwinTarget = async (target) => ({
      ...target,
      archive: await readFile(target.archive),
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
          ...(config.darwinTargets !== undefined
            ? {
                darwinTargets: await Promise.all(
                  config.darwinTargets.map(loadDarwinTarget),
                ),
              }
            : {}),
        }
      : {
          ...(await loadTarget(config)),
          ...(config.darwinTargets !== undefined
            ? {
                darwinTargets: await Promise.all(
                  config.darwinTargets.map(loadDarwinTarget),
                ),
              }
            : {}),
        };
    const result = renderEntry(
      await readFile(new URL("../install.sh", import.meta.url), "utf8"),
      loaded,
    );
    await writeFile(process.argv[3], result, { mode: 0o500, flag: "wx" });
  }
}
