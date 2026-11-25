import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import {
  CLI_TARGETS,
  LINUX_PLATFORM_FILES,
  buildBundleManifest,
} from "./build-dual-track-cli.mjs";
import {
  renderDualTrackCliEntry,
  renderEntry,
} from "./render-install-entry.mjs";
import {
  createLoopbackFixtureTLS,
  startLoopbackArchiveServer,
} from "./smoke-dual-track-installers.mjs";

export const DEFAULT_SOAK_DISTROS = [
  "debian:12",
  "debian:13",
  "fedora:41",
  "fedora:42",
  "fedora:43",
  "ubuntu:24.04",
];

const sha256Hex = (buf) => createHash("sha256").update(buf).digest("hex");

function normalizeArch(raw = process.arch) {
  if (raw === "x64" || raw === "amd64") return "amd64";
  if (raw === "arm64" || raw === "aarch64") return "arm64";
  throw new Error(`Unsupported soak architecture: ${raw}`);
}

export function parseSoakArgs(argv = process.argv.slice(2)) {
  const opts = {
    runtime: existsSync("/usr/bin/podman") ? "podman" : "docker",
    arch: normalizeArch(process.arch),
    distros: [...DEFAULT_SOAK_DISTROS],
    output: "",
    timeoutMs: 180000,
  };
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    if (arg === "--runtime" && argv[i + 1]) {
      opts.runtime = argv[++i];
      if (!["podman", "docker"].includes(opts.runtime)) {
        throw new Error(`Unsupported container runtime: ${opts.runtime}`);
      }
    } else if (arg === "--arch" && argv[i + 1]) {
      opts.arch = normalizeArch(argv[++i]);
    } else if (arg === "--distros" && argv[i + 1]) {
      opts.distros = argv[++i]
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      if (opts.distros.length === 0) {
        throw new Error("--distros must include at least one image");
      }
    } else if (arg === "--output" && argv[i + 1]) {
      opts.output = path.resolve(argv[++i]);
    } else if (arg === "--timeout-ms" && argv[i + 1]) {
      opts.timeoutMs = Number.parseInt(argv[++i], 10);
      if (!Number.isFinite(opts.timeoutMs) || opts.timeoutMs < 10000) {
        throw new Error("Invalid --timeout-ms");
      }
    } else {
      throw new Error(`Unknown or incomplete soak option: ${arg}`);
    }
  }
  return opts;
}

/** Extracts install.sh OS metadata + platform + remote gate clauses for safe non-root distro verification. */
export function buildInstallGateProbeScript(renderedInstallSh, osReleaseText) {
  const lines = renderedInstallSh.split(/\r?\n/);
  const parseStart = lines.findIndex((l) =>
    l.includes("[[ -r /etc/os-release ]]"),
  );
  const devCheckStart = lines.findIndex(
    (l, idx) =>
      idx > parseStart && l.startsWith("if [[ $development_only == 0 ]]; then"),
  );
  const remoteStart = lines.findIndex(
    (l, idx) =>
      idx > devCheckStart && l.startsWith("if [[ $profile == remote ]]; then"),
  );
  const remoteClose = lines.findIndex(
    (l, idx) => idx > remoteStart && l.trim() === "fi",
  );
  if (
    parseStart < 0 ||
    devCheckStart <= parseStart ||
    remoteStart <= devCheckStart ||
    remoteClose <= remoteStart
  ) {
    throw new Error("Unable to locate install.sh platform gate blocks");
  }

  const sanitized = [];
  for (let i = parseStart; i < devCheckStart; i += 1) {
    const line = lines[i];
    if (
      line.trim() ===
      "[[ -r /etc/os-release ]] || die 'OS release metadata is missing.'"
    ) {
      sanitized.push("profile=remote");
      sanitized.push("OS_RELEASE_PATH=${OS_RELEASE_PATH:-/etc/os-release}");
      sanitized.push(
        '[[ -r "$OS_RELEASE_PATH" ]] || die "OS release metadata is missing."',
      );
      continue;
    }
    if (line.trim() === "done < /etc/os-release") {
      sanitized.push('done < "$OS_RELEASE_PATH"');
      continue;
    }
    sanitized.push(line);
  }
  for (let i = remoteStart; i <= remoteClose; i += 1) {
    sanitized.push(lines[i]);
  }

  return [
    "#!/bin/bash",
    "set -euo pipefail",
    "die() { printf 'Fased: %s\\n' \"$*\" >&2; exit 1; }",
    ...(osReleaseText ? [] : []),
    ...sanitized,
    'printf "INSTALL_GATE_PASS:%s:%s:%s:%s\\n" "$ID" "$VERSION_ID" "$package_family" "$target"',
    "",
  ].join("\n");
}

function buildLinuxSoakArtifacts(repoRoot, workDir, arch) {
  const hostMount = path.join(workDir, "mount");
  const cliDir = path.join(workDir, "cli");
  const stageName = `fased-app-linux-${arch}`;
  const stageDir = path.join(workDir, stageName);
  mkdirSync(hostMount, { recursive: true, mode: 0o755 });
  mkdirSync(cliDir, { recursive: true, mode: 0o755 });
  mkdirSync(stageDir, { recursive: true, mode: 0o755 });

  const pkg = JSON.parse(
    readFileSync(path.join(repoRoot, "package.json"), "utf8"),
  );
  const releaseCfg = JSON.parse(
    readFileSync(path.join(repoRoot, "release/ubuntu.json"), "utf8"),
  );
  const version = String(releaseCfg.version || pkg.version || "0.2.1");
  const desktopVersion = `${version}-desktop`;
  const source = execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: repoRoot,
    encoding: "utf8",
  }).trim();

  if (!existsSync(path.join(repoRoot, "dist", "index.html"))) {
    execFileSync("npm", ["run", "build"], {
      cwd: repoRoot,
      stdio: "inherit",
      timeout: 180000,
    });
  }

  const buildEnv = {
    ...process.env,
    CGO_ENABLED: "0",
    GOOS: "linux",
    GOARCH: arch,
  };
  const ldflags = `-s -w -X github.com/fased-ai/fased-app/internal/core.Version=${version} -X github.com/fased-ai/fased-app/internal/core.BuildSource=${source}`;

  execFileSync(
    "go",
    [
      "build",
      "-trimpath",
      "-ldflags",
      ldflags,
      "-o",
      path.join(stageDir, "fased-app"),
      "./cmd/fased-app",
    ],
    { cwd: repoRoot, env: buildEnv, stdio: "inherit", timeout: 180000 },
  );
  execFileSync(
    "go",
    [
      "build",
      "-trimpath",
      "-ldflags",
      ldflags,
      "-o",
      path.join(stageDir, "fased-signer"),
      "./cmd/fased-signer",
    ],
    { cwd: repoRoot, env: buildEnv, stdio: "inherit", timeout: 180000 },
  );
  execFileSync(
    "go",
    [
      "build",
      "-trimpath",
      "-o",
      path.join(workDir, "fased-install"),
      "./cmd/fased-install",
    ],
    { cwd: repoRoot, env: buildEnv, stdio: "inherit", timeout: 180000 },
  );
  execFileSync(
    "go",
    [
      "test",
      "-c",
      "-o",
      path.join(hostMount, "hostplatform.test"),
      "./internal/hostplatform",
    ],
    { cwd: repoRoot, env: buildEnv, stdio: "inherit", timeout: 180000 },
  );
  chmodSync(path.join(hostMount, "hostplatform.test"), 0o755);
  chmodSync(path.join(stageDir, "fased-app"), 0o755);
  chmodSync(path.join(stageDir, "fased-signer"), 0o755);

  const files = {
    "fased-app": inspectQuick(path.join(stageDir, "fased-app")),
    "fased-signer": inspectQuick(path.join(stageDir, "fased-signer")),
  };
  for (const [dstName, srcRel] of Object.entries(LINUX_PLATFORM_FILES)) {
    const dstPath = path.join(stageDir, dstName);
    copyFileSync(path.join(repoRoot, srcRel), dstPath);
    chmodSync(dstPath, 0o644);
    files[dstName] = inspectQuick(dstPath);
  }
  for (const legal of ["LICENSE", "NOTICE"]) {
    const dstPath = path.join(stageDir, legal);
    copyFileSync(path.join(repoRoot, legal), dstPath);
    chmodSync(dstPath, 0o644);
    files[legal] = inspectQuick(dstPath);
  }
  writeFileSync(
    path.join(stageDir, "bundle.json"),
    JSON.stringify(
      buildBundleManifest({
        platform: "linux",
        arch,
        version: desktopVersion,
        source,
        files,
      }),
      null,
      2,
    ) + "\n",
    { mode: 0o644 },
  );

  const archiveName = `${stageName}.tar.gz`;
  const archivePath = path.join(cliDir, archiveName);
  execFileSync("tar", ["-C", workDir, "-czf", archivePath, stageName], {
    stdio: "inherit",
    timeout: 60000,
  });
  const archiveBytes = readFileSync(archivePath);
  const archiveSha256 = sha256Hex(archiveBytes);

  const tls = createLoopbackFixtureTLS(workDir);
  return {
    version,
    desktopVersion,
    source,
    arch,
    hostMount,
    archiveName,
    archiveBytes,
    archiveSha256,
    tls,
    installerBytes: readFileSync(path.join(workDir, "fased-install")),
    seedBytes: readFileSync(
      path.join(
        repoRoot,
        arch === "arm64"
          ? "release/public/linux-arm64-attested-seed.json"
          : "release/public/ubuntu-attested-seed.json",
      ),
    ),
    cliTargetCount: CLI_TARGETS.length,
  };
}

function inspectQuick(p) {
  const b = readFileSync(p);
  return { sha256: sha256Hex(b), bytes: b.length };
}

function runProcessAsync(cmd, args, { timeoutMs }) {
  return new Promise((resolve) => {
    let stdout = "";
    let stderr = "";
    let settled = false;
    const child = spawn(cmd, args, { stdio: ["ignore", "pipe", "pipe"] });
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      child.kill("SIGKILL");
      resolve({
        status: -1,
        stdout,
        stderr: `${stderr}\nTIMED_OUT`,
        timedOut: true,
      });
    }, timeoutMs);
    child.stdout.on("data", (d) => {
      stdout += d.toString("utf8");
    });
    child.stderr.on("data", (d) => {
      stderr += d.toString("utf8");
    });
    child.on("close", (code, signal) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve({
        status: code ?? (signal ? -1 : 0),
        stdout,
        stderr,
        timedOut: false,
      });
    });
    child.on("error", (err) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve({ status: -1, stdout, stderr: String(err), timedOut: false });
    });
  });
}

function buildUserVerifyScript(arch) {
  return [
    "#!/bin/bash",
    "set -euo pipefail",
    'APP_PORT="$1"',
    'SERVER_PORT="$2"',
    'ARCHIVE_NAME="$3"',
    'EXPECTED_SHA="$4"',
    'EXPECTED_VERSION="$5"',
    "",
    'WORK=$(mktemp -d "$HOME/soak-work.XXXXXX")',
    'DATA_DIR="$WORK/data"',
    'mkdir -p -m 0700 "$DATA_DIR" "$WORK/extract"',
    "APP_PID=''",
    "cleanup() {",
    '  if [[ -n "$APP_PID" ]]; then',
    '    kill "$APP_PID" 2>/dev/null || true',
    '    wait "$APP_PID" 2>/dev/null || true',
    "  fi",
    '  rm -rf "$WORK"',
    "}",
    "trap cleanup EXIT",
    "",
    'curl --fail --silent --show-error --cacert /soak/loopback.crt --noproxy "*" -o "$WORK/$ARCHIVE_NAME" "https://127.0.0.1:${SERVER_PORT}/${ARCHIVE_NAME}"',
    "got_sha=$(sha256sum \"$WORK/$ARCHIVE_NAME\" | awk '{print $1}')",
    '[[ "$got_sha" == "$EXPECTED_SHA" ]]',
    'tar -xzf "$WORK/$ARCHIVE_NAME" -C "$WORK/extract"',
    `BIN_DIR="$WORK/extract/fased-app-linux-${arch}"`,
    '"$BIN_DIR/fased-app" version --json',
    '"$BIN_DIR/fased-app" server init --transport ssh --listen "127.0.0.1:${APP_PORT}" --data-dir "$DATA_DIR" --json >/dev/null',
    "",
    '"$BIN_DIR/fased-app" serve --data-dir "$DATA_DIR" --listen "127.0.0.1:${APP_PORT}" >"$WORK/serve.log" 2>&1 &',
    "APP_PID=$!",
    "",
    "for _ in $(seq 1 60); do",
    '  if body=$(curl --fail --silent --noproxy "*" "http://127.0.0.1:${APP_PORT}/api/v1/health"); then',
    '    printf "SOAK_HEALTH_JSON:%s\\n" "$body"',
    '    grep -Fq "\\"version\\":\\"${EXPECTED_VERSION}\\"" <<< "$body"',
    '    grep -Fq \'"walletSigner":false\' <<< "$body"',
    '    grep -Fq \'"financialExecution":false\' <<< "$body"',
    '    grep -Fq \'"updater":false\' <<< "$body"',
    "    exit 0",
    "  fi",
    "  sleep 0.25",
    "done",
    'cat "$WORK/serve.log" >&2',
    "exit 1",
    "",
  ].join("\n");
}

function buildContainerScript(
  arch,
  serverPort,
  appPort,
  archiveName,
  archiveSha256,
  version,
) {
  return `#!/bin/bash
set -euo pipefail

. /etc/os-release
echo "SOAK_DISTRO:\${ID}:\${VERSION_ID}"

missing_bins=()
for tool in bash curl tar gzip sha256sum base64 useradd su; do
  command -v "$tool" >/dev/null 2>&1 || missing_bins+=("$tool")
done

if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  if (( \${#missing_bins[@]} > 0 )) || [[ ! -f /etc/ssl/certs/ca-certificates.crt ]]; then
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends bash curl tar gzip coreutils ca-certificates passwd >/dev/null
  fi
  dpkg-query --version >/dev/null
elif command -v dnf >/dev/null 2>&1; then
  if (( \${#missing_bins[@]} > 0 )); then
    pkgs=()
    command -v curl >/dev/null 2>&1 || pkgs+=(curl-minimal)
    command -v tar >/dev/null 2>&1 || pkgs+=(tar)
    command -v gzip >/dev/null 2>&1 || pkgs+=(gzip)
    command -v sha256sum >/dev/null 2>&1 || pkgs+=(coreutils)
    command -v useradd >/dev/null 2>&1 || pkgs+=(shadow-utils)
    command -v su >/dev/null 2>&1 || pkgs+=(util-linux)
    if (( \${#pkgs[@]} > 0 )); then
      dnf install -y -q --setopt=install_weak_deps=False "\${pkgs[@]}" >/dev/null
    fi
  fi
  rpm -q --quiet rpm
else
  echo "No apt-get or dnf package manager found" >&2
  exit 1
fi

id fasedsoak >/dev/null 2>&1 || useradd -m -s /bin/bash fasedsoak

# 1. Real Go hostplatform verification inside live distro container.
FASED_SOAK_LIVE_HOST=1 /soak/hostplatform.test -test.v -test.run '^TestSoakLiveHostPlatform$'

# 2. Stamped install.sh OS + Remote gate checks (positive on live distro, negative on unsupported release).
gate_out=$(bash /soak/install-gate-probe.sh)
echo "$gate_out"
grep -q '^INSTALL_GATE_PASS:' <<< "$gate_out"

unsupported_os=$(mktemp)
printf 'ID=%s\\nVERSION_ID=0\\n' "$ID" > "$unsupported_os"
if OS_RELEASE_PATH="$unsupported_os" bash /soak/install-gate-probe.sh >/tmp/unsup.out 2>&1; then
  rm -f "$unsupported_os"
  echo "Expected install.sh gate to reject unsupported \${ID}:0" >&2
  exit 1
fi
rm -f "$unsupported_os"
if ! grep -q 'Remote requires a supported Linux release' /tmp/unsup.out; then
  cat /tmp/unsup.out >&2
  rm -f /tmp/unsup.out
  exit 1
fi
rm -f /tmp/unsup.out

# 3. Run unprivileged dual-track tamper check + verified HTTPS archive pull + local serve health check.
run_as_user() {
  su -s /bin/bash fasedsoak -c "$1"
}

tamper_log=$(mktemp)
if run_as_user "bash /soak/tampered-cli.sh --local --no-autostart --listen 127.0.0.1:${appPort}" >"$tamper_log" 2>&1; then
  cat "$tamper_log" >&2
  rm -f "$tamper_log"
  echo "Tampered install-cli.sh unexpectedly succeeded" >&2
  exit 1
fi
if ! grep -qi 'checksum differs' "$tamper_log"; then
  echo "Tampered install-cli.sh failed with unexpected message:" >&2
  cat "$tamper_log" >&2
  rm -f "$tamper_log"
  exit 1
fi
rm -f "$tamper_log"

if ! run_as_user "bash /soak/user-verify.sh '${appPort}' '${serverPort}' '${archiveName}' '${archiveSha256}' '${version}'"; then
  echo "user-verify.sh failed on \${ID}:\${VERSION_ID}" >&2
  exit 1
fi
`;
}

async function runContainerSoakForDistro(opts, shared, distro) {
  const containerName = `fased-soak-${process.pid}-${distro.replace(/[^a-z0-9]/gi, "-")}`;
  const appPort = 18000 + Math.floor(Math.random() * 10000);
  const entryScriptPath = path.join(
    shared.hostMount,
    `run-${distro.replace(/[^a-z0-9]/gi, "-")}.sh`,
  );
  writeFileSync(
    entryScriptPath,
    buildContainerScript(
      shared.arch,
      shared.server.port,
      appPort,
      shared.archiveName,
      shared.archiveSha256,
      shared.version,
    ),
    { mode: 0o755 },
  );

  const dockerPlatform =
    shared.arch === "arm64" ? "linux/arm64" : "linux/amd64";
  const args = [
    "run",
    "--rm",
    "--name",
    containerName,
    "--network=host",
    "--platform",
    dockerPlatform,
    ...(opts.runtime === "podman" ? ["--security-opt", "label=disable"] : []),
    "-v",
    `${shared.hostMount}:/soak:ro`,
    `docker.io/library/${distro}`,
    "/bin/bash",
    `/soak/${path.basename(entryScriptPath)}`,
  ];

  const startedAt = Date.now();
  try {
    const res = await runProcessAsync(opts.runtime, args, {
      timeoutMs: opts.timeoutMs,
    });
    if (res.status !== 0) {
      throw new Error(
        `Multi-OS soak failed on ${distro} (${shared.arch}) (exit ${res.status}):\n${res.stdout}\n${res.stderr}`,
      );
    }
    const platformMatch = /SOAK_HOST_PLATFORM:(\{.*\})/.exec(res.stdout);
    assert.ok(
      platformMatch,
      `Missing SOAK_HOST_PLATFORM output on ${distro}:\n${res.stdout}`,
    );
    const platformJson = JSON.parse(platformMatch[1]);
    assert.equal(platformJson.arch, shared.arch);
    assert.equal(platformJson.remote, true);

    const healthMatch = /SOAK_HEALTH_JSON:(\{.*\})/.exec(res.stdout);
    assert.ok(
      healthMatch,
      `Missing SOAK_HEALTH_JSON output on ${distro}:\n${res.stdout}`,
    );
    const healthJson = JSON.parse(healthMatch[1]);
    assert.equal(healthJson.capabilities?.walletSigner, false);
    assert.equal(healthJson.capabilities?.financialExecution, false);
    assert.equal(healthJson.capabilities?.updater, false);

    return {
      distro,
      arch: shared.arch,
      runtime: opts.runtime,
      status: "PASS",
      durationMs: Date.now() - startedAt,
      platform: platformJson,
      healthVersion: healthJson.version,
      tamperRejected: true,
      installGateVerified: true,
    };
  } finally {
    try {
      execFileSync(opts.runtime, ["rm", "-f", containerName], {
        stdio: "ignore",
        timeout: 15000,
      });
    } catch {
      // Container already removed by --rm.
    }
  }
}

export async function runMultiOSSoak(opts) {
  const repoRoot = process.cwd();
  const workDir = mkdtempSync(path.join(os.tmpdir(), "fased-multi-os-soak-"));
  let server = null;
  try {
    const artifacts = buildLinuxSoakArtifacts(repoRoot, workDir, opts.arch);
    server = await startLoopbackArchiveServer({
      keyPem: artifacts.tls.keyPem,
      certPem: artifacts.tls.certPem,
      archiveName: artifacts.archiveName,
      archiveBytes: artifacts.archiveBytes,
    });
    copyFileSync(
      artifacts.tls.certPath,
      path.join(artifacts.hostMount, "loopback.crt"),
    );

    const installTemplate = readFileSync(
      path.join(repoRoot, "install.sh"),
      "utf8",
    );
    const renderedInstall = renderEntry(installTemplate, {
      version: artifacts.version,
      installer: artifacts.installerBytes,
      seed: artifacts.seedBytes,
    });
    writeFileSync(
      path.join(artifacts.hostMount, "install-gate-probe.sh"),
      buildInstallGateProbeScript(renderedInstall),
      { mode: 0o755 },
    );

    const cliTemplate = readFileSync(
      path.join(repoRoot, "install-cli.sh"),
      "utf8",
    );
    const tamperedCli = renderDualTrackCliEntry(cliTemplate, {
      version: artifacts.desktopVersion,
      fixture: true,
      ca: artifacts.tls.certPem,
      targets: [
        {
          os: "linux",
          arch: opts.arch,
          archive: artifacts.archiveBytes,
          url: `https://127.0.0.1:${server.port}/tampered/${artifacts.archiveName}`,
        },
      ],
    });
    writeFileSync(
      path.join(artifacts.hostMount, "tampered-cli.sh"),
      tamperedCli,
      {
        mode: 0o755,
      },
    );
    writeFileSync(
      path.join(artifacts.hostMount, "user-verify.sh"),
      buildUserVerifyScript(opts.arch),
      {
        mode: 0o755,
      },
    );

    const results = [];
    for (const distro of opts.distros) {
      results.push(
        await runContainerSoakForDistro(opts, { ...artifacts, server }, distro),
      );
    }

    const summary = {
      result: "PASS",
      runtime: opts.runtime,
      arch: opts.arch,
      hostOS: `${process.platform}/${process.arch}`,
      version: artifacts.version,
      sourceCommit: artifacts.source,
      distroCount: results.length,
      distros: results,
      timestamp: new Date().toISOString(),
    };
    if (opts.output) {
      mkdirSync(path.dirname(opts.output), { recursive: true });
      writeFileSync(opts.output, JSON.stringify(summary, null, 2) + "\n", {
        mode: 0o600,
      });
      chmodSync(opts.output, 0o600);
    }
    return summary;
  } finally {
    if (server) {
      await server.close();
    }
    rmSync(workDir, { recursive: true, force: true });
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const opts = parseSoakArgs();
  const summary = await runMultiOSSoak(opts);
  console.log(JSON.stringify(summary, null, 2));
}
