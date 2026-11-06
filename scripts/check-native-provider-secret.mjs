// Native credential proof in the existing disposable Ubuntu CI runner.
// Root executes OS utilities only; the compiled test always runs as its owner.
import { execFileSync } from "node:child_process";
import { randomBytes, createHash } from "node:crypto";
import {
  readFileSync,
  writeFileSync,
  copyFileSync,
  chmodSync,
  mkdirSync,
} from "node:fs";
import path from "node:path";

if (
  process.env.CI !== "true" ||
  process.getuid?.() <= 0 ||
  !/^ID=ubuntu$/m.test(readFileSync("/etc/os-release", "utf8"))
)
  throw Error("Disposable Ubuntu CI and ordinary owner required");
const id = randomBytes(8).toString("hex"),
  dir = `/var/lib/fased-native-provider-${id}`;
const uid = process.getuid(),
  gid = process.getgid();
const evidence = path.resolve(".evidence/native-provider-secret");
mkdirSync(evidence, { recursive: true, mode: 0o700 });
const binary = path.join(evidence, "providersecrets.test");
const agentBinary = path.join(evidence, "core.test");
const run = (tool, args, options = {}) =>
  execFileSync(tool, args, {
    stdio: ["pipe", "pipe", "pipe"],
    timeout: 60000,
    ...options,
  });
const osRun = (tool, args, options = {}) =>
  run("sudo", ["-n", "--", tool, ...args], options);
run("go", ["test", "-c", "-o", binary, "./internal/providersecrets"]);
run("go", ["test", "-c", "-o", agentBinary, "./internal/core"]);
let created = false;
try {
  osRun("/usr/bin/mkdir", ["-m", "0700", "--", dir]);
  created = true;
  osRun("/usr/bin/chown", [`${uid}:${gid}`, "--", dir]);
  copyFileSync(binary, path.join(dir, "providersecrets.test"));
  chmodSync(path.join(dir, "providersecrets.test"), 0o755);
  copyFileSync(agentBinary, path.join(dir, "core.test"));
  chmodSync(path.join(dir, "core.test"), 0o755);
  const secret = randomBytes(32),
    expected = createHash("sha256").update(secret).digest("hex");
  let encrypted;
  try {
    encrypted = osRun(
      "/usr/bin/systemd-creds",
      [
        "encrypt",
        "--with-key=host",
        "--name=fased-provider-task-key",
        "-",
        "-",
      ],
      { input: secret },
    );
  } finally {
    secret.fill(0);
  }
  const encryptedPath = path.join(dir, "disposable.cred");
  writeFileSync(encryptedPath, encrypted, { mode: 0o600 });
  const outputs = [];
  const cases = [
    [0, "providersecrets.test", "TestNativeSystemdRuntimeCredential"],
    [1, "providersecrets.test", "TestNativeSystemdRuntimeCredential"],
    ["agent", "core.test", "TestInstalledProviderFixtureNativeRuntime"],
  ];
  for (const [attempt, executable, test] of cases) {
    const output = osRun(
      "/usr/bin/systemd-run",
      [
        "--quiet",
        "--wait",
        "--collect",
        "--pipe",
        `--unit=fased-native-provider-${id}-${attempt}.service`,
        `--property=User=${uid}`,
        `--property=Group=${gid}`,
        "--property=NoNewPrivileges=yes",
        "--property=ProtectSystem=strict",
        "--property=ProtectHome=yes",
        "--property=PrivateTmp=yes",
        "--property=CapabilityBoundingSet=",
        "--property=AmbientCapabilities=",
        `--property=LoadCredentialEncrypted=fased-provider-task-key:${encryptedPath}`,
        `--setenv=FASED_NATIVE_EXPECTED_SHA256=${expected}`,
        path.join(dir, executable),
        `-test.run=^${test}$`,
        "-test.v",
      ],
      { encoding: "utf8" },
    );
    if (!output.includes(`--- PASS: ${test}`))
      throw Error("Native service test did not pass");
    outputs.push(output);
  }
  writeFileSync(
    path.join(evidence, "result.json"),
    JSON.stringify(
      {
        result: "PASS",
        scope:
          "actual systemd encrypted credential delivery and installed-provider source composition in an unprivileged test service; not packaged acceptance",
        runs: outputs.length,
        installedProviderJourney: "PASS",
        shippingPackageAcceptance: false,
        user: uid,
        secretPrinted: false,
        liveFeatures: false,
      },
      null,
      2,
    ) + "\n",
  );
  writeFileSync(path.join(evidence, "service-starts.log"), outputs.join("\n"));
  console.log(
    "PASS: native encrypted credential delivery and installed-provider task/restart/revoke source journey",
  );
} finally {
  for (const attempt of [0, 1, "agent"]) {
    try {
      osRun("/usr/bin/systemctl", [
        "stop",
        `fased-native-provider-${id}-${attempt}.service`,
      ]);
    } catch {}
  }
  if (created && /^\/var\/lib\/fased-native-provider-[a-f0-9]{16}$/.test(dir))
    osRun("/usr/bin/rm", [
      "--recursive",
      "--force",
      "--one-file-system",
      "--",
      dir,
    ]);
}
