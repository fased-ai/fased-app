import test from "node:test";
import assert from "node:assert/strict";
import { renderEntry, renderWindowsEntry } from "./render-install-entry.mjs";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const template = readFileSync(
  new URL("../install.sh", import.meta.url),
  "utf8",
);
const windowsTemplate = readFileSync(
  new URL("../install.ps1", import.meta.url),
  "utf8",
);
function assertShellSyntax(script) {
  const dir = mkdtempSync(join(tmpdir(), "fased-entry-syntax-"));
  try {
    const entry = join(dir, "install.sh");
    writeFileSync(entry, script);
    const parsed = spawnSync("bash", ["-n", entry]);
    assert.equal(parsed.status, 0, parsed.stderr.toString());
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}
test("entry refuses missing trust and uncontrolled download roots", () => {
  assert.throws(() => renderEntry(template, {}));
  assert.throws(() =>
    renderEntry(template, {
      version: "1.0.0",
      installer: new Uint8Array([1]),
      seed: Buffer.from("{}"),
      url: "http://attacker.invalid",
    }),
  );
});

const seed = Buffer.from(
  JSON.stringify({
    version: 3,
    repository: "fased-ai/fased-app",
    target: { os: "linux", arch: "amd64" },
    developmentOnly: false,
    channel: "stable",
    provenance: {},
  }),
);
const config = {
  version: "1.0.0",
  installer: Buffer.from("disposable verifier bytes"),
  seed,
};
test("public entry pins exact GitHub verifier and public trust; no runtime bypass", () => {
  const script = renderEntry(template, config);
  assert.match(
    script,
    /https:\/\/github.com\/fased-ai\/fased-app\/releases\/download\/v1.0.0\/fased-install-linux-amd64/,
  );
  assert.ok(!script.includes("@INSTALLER_SHA256@"));
  assertShellSyntax(script);
  for (const flag of [
    "--url",
    "--insecure",
    "--skip-verify",
    "--development",
  ]) {
    const r = spawnSync("bash", ["-c", script, "entry", flag]);
    assert.equal(r.status, 1);
    assert.match(r.stderr.toString(), /Unknown option/);
  }
  assert.throws(() =>
    renderEntry(template, { ...config, ca: Buffer.from("untrusted CA") }),
  );
});
test("unstamped source cannot modify a host", () => {
  const r = spawnSync("bash", ["-c", template, "entry", "--remote"]);
  assert.equal(r.status, 1);
  assert.match(r.stderr.toString(), /No trusted installer release/);
});

test("Local installation prepares the update authorization prerequisite", () => {
  const ownerStart = template.indexOf(
    "  missing=()\n  packages=(ca-certificates curl)",
  );
  const rootStart = template.indexOf(
    "\nmissing=()\npackages=(ca-certificates curl sudo util-linux)",
  );
  assert.ok(ownerStart >= 0 && rootStart >= 0);
  for (const [start, endMarker] of [
    [ownerStart, "  for package in"],
    [rootStart, "\nfor package in"],
  ]) {
    const end = template.indexOf(endMarker, start);
    assert.ok(end > start);
    const selection = template.slice(start, end);
    for (const [family, action, expected] of [
      ["apt", "install", "polkitd"],
      ["dnf", "install", "polkit"],
      ["apt", "status", ""],
    ]) {
      const result = spawnSync("bash", [
        "-c",
        `
        set -eu
        profile=local package_family=$1 action=$2
        ${selection}
        printf '%s\\n' "\${packages[@]}"
      `,
        "entry",
        family,
        action,
      ]);
      assert.equal(result.status, 0, result.stderr.toString());
      const packages = result.stdout.toString().trim().split("\n");
      assert.equal(packages.includes("polkitd"), expected === "polkitd");
      assert.equal(packages.includes("polkit"), expected === "polkit");
      assert.ok(!packages.some((name) => /gtk|webkit/i.test(name)));
    }
  }
});

test("Local entry detects apt/dnf families before setup and refuses ambiguous hosts", () => {
  const dir = mkdtempSync(join(tmpdir(), "fased-os-entry-"));
  try {
    const osRelease = join(dir, "os-release");
    for (const tool of ["apt-get", "dpkg-query", "dnf", "rpm"]) {
      writeFileSync(join(dir, tool), "#!/bin/sh\nexit 0\n", { mode: 0o700 });
    }
    const detached = `import os,subprocess,sys
r=subprocess.run(['bash','-c',sys.argv[1],'entry','--local'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,preexec_fn=os.setsid,timeout=4)
sys.stdout.buffer.write(r.stdout);sys.stderr.buffer.write(r.stderr);sys.exit(r.returncode)
`;
    for (const [release, cpu, expected] of [
      [
        "ID=fedora\nVERSION_ID=45\n",
        "x86_64",
        /Run this command in an interactive terminal/,
      ],
      [
        "ID=fedora\nVERSION_ID=45\n",
        "aarch64",
        /No reviewed package for this OS and CPU/,
      ],
      [
        "ID=fedora\nVERSION_ID=46\n",
        "x86_64",
        /Run this command in an interactive terminal/,
      ],
      [
        "ID=debian\nVERSION_ID=12\n",
        "x86_64",
        /Run this command in an interactive terminal/,
      ],
      [
        'ID=linuxmint\nID_LIKE="ubuntu debian"\nVERSION_ID=22\n',
        "x86_64",
        /Run this command in an interactive terminal/,
      ],
      [
        'ID=rocky\nID_LIKE="rhel fedora"\nVERSION_ID=9.5\n',
        "x86_64",
        /Run this command in an interactive terminal/,
      ],
      [
        'ID=custom\nID_LIKE="debian fedora"\nVERSION_ID=1\n',
        "x86_64",
        /Conflicting Linux package families/,
      ],
      [
        'ID=custom\nID_LIKE="debian;id"\nVERSION_ID=1\n',
        "x86_64",
        /Invalid OS family metadata/,
      ],
      [
        "ID=unknown\nVERSION_ID=1\n",
        "x86_64",
        /No supported Linux package manager family/,
      ],
      [
        "ID=debian\nID_LIKE=debian\nID_LIKE=debian\nVERSION_ID=12\n",
        "x86_64",
        /Invalid OS family metadata/,
      ],
    ]) {
      writeFileSync(osRelease, release);
      let script = renderEntry(template, config)
        .replaceAll("/etc/os-release", osRelease)
        .replace("case $(/usr/bin/uname -m)", `case ${cpu}`);
      for (const tool of ["apt-get", "dpkg-query", "dnf", "rpm"]) {
        script = script.replaceAll(`/usr/bin/${tool}`, join(dir, tool));
      }
      const result = spawnSync("python3", ["-c", detached, script], {
        timeout: 6000,
      });
      assert.ifError(result.error);
      assert.equal(result.status, 1, result.stderr.toString());
      assert.match(result.stderr.toString(), expected);
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

function entryTerminalCode() {
  const terminal = template
    .split("else die 'Invalid installer binding.'; fi\n")[1]
    .split("if [[ -z $profile")[0];
  const die = template.match(/^die\(\).*$/m)[0];
  return `set -euo pipefail\n${die}\n${terminal}`;
}

test("entry reports a detached terminal before any installation work", () => {
  const result = spawnSync("python3", [
    "-c",
    `import os,subprocess,sys
r=subprocess.run(['bash','-c',sys.argv[1]],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,preexec_fn=os.setsid)
sys.stdout.buffer.write(r.stdout);sys.stderr.buffer.write(r.stderr);sys.exit(r.returncode)
`,
    entryTerminalCode(),
  ]);
  assert.equal(result.status, 1, result.stderr.toString());
  assert.match(
    result.stderr.toString(),
    /Run this command in an interactive terminal/,
  );
  assert.doesNotMatch(result.stderr.toString(), /\/dev\/tty:/);
});

test("literal process-substitution entry keeps the terminal through sanitized owner handoff", () => {
  const handoff = template
    .split("\n")
    .find((line) =>
      line.startsWith('/usr/sbin/runuser -u "$operator" -- /usr/bin/env -i '),
    )
    .split(" -- ")[1];
  const code =
    entryTerminalCode() +
    `
operator=fixture owner_home=/tmp action=update entry_version=1.0.0 installer_url='' installer_sha256='' seed_base64='' seed_sha256='' fixture_ca_base64='' development_only=1 listen=127.0.0.1:7331 profile=remote runtime='' ssh_user=root recovery_key='' protected_installer=/tmp/disposable-protected-installer
owner_install() {
  [[ $(id -u) != 0 && -t 0 ]]
  printf 'Owner sign-in: '
  IFS= read -r answer
  [[ $answer == synthetic-owner ]]
  printf 'ENTRY_PTY_PASS:%s\\n' "$profile"
}
body="$(declare -f die owner_install); owner_install"
${handoff}
`;
  const result = spawnSync(
    "python3",
    [
      "-c",
      `import errno,os,pty,select,signal,sys,time
pid,fd=pty.fork()
if pid==0:os.execlp('bash','bash','-c','bash <(printf "%s" "$1")','entry',sys.argv[1])
output=b'';deadline=time.monotonic()+10;status=None
try:
 os.write(fd,b'synthetic-owner\\n')
 while time.monotonic()<deadline:
  if select.select([fd],[],[],0.1)[0]:
   try:
    chunk=os.read(fd,65536)
    if not chunk:break
    output+=chunk
   except OSError as e:
    if e.errno==errno.EIO:break
    raise
  done,child_status=os.waitpid(pid,os.WNOHANG)
  if done:
   status=child_status
   break
 else:raise TimeoutError('entry terminal handoff timed out')
finally:
 os.close(fd)
 if status is None:
  done,status=os.waitpid(pid,os.WNOHANG)
  if not done:os.kill(pid,signal.SIGKILL);_,status=os.waitpid(pid,0)
sys.stdout.buffer.write(output);sys.exit(os.waitstatus_to_exitcode(status))
`,
      code,
    ],
    { timeout: 15000 },
  );
  assert.equal(
    result.status,
    0,
    result.stderr.toString() + result.stdout.toString(),
  );
  assert.match(result.stdout.toString(), /ENTRY_PTY_PASS:remote/);
});

test("remote management leaves an older installation's command path untouched", () => {
  const commandGuard = template
    .split(
      "# The root-owned command contains only fixed OS identity/drop operations.",
    )[1]
    .match(/^if \[\[.*\]\]; then$/m)[0];
  for (const action of [
    "open",
    "start",
    "stop",
    "status",
    "recover",
    "setup",
  ]) {
    const result = spawnSync(
      "bash",
      ["-c", `${commandGuard}\nprintf 'INSTALL_COMMAND_REQUESTED'\nfi`],
      { env: { ...process.env, profile: "remote", action } },
    );
    assert.equal(result.status, 0, result.stderr.toString());
    assert.equal(result.stdout.toString(), "", action);
  }
  for (const action of ["install", "update"]) {
    const result = spawnSync(
      "bash",
      ["-c", `${commandGuard}\nprintf 'INSTALL_COMMAND_REQUESTED'\nfi`],
      { env: { ...process.env, profile: "remote", action } },
    );
    assert.equal(result.status, 0, result.stderr.toString());
    assert.equal(result.stdout.toString(), "INSTALL_COMMAND_REQUESTED", action);
  }
});
test("controlled fixture has explicit isolated trust", () => {
  const fixtureSeed = Buffer.from(
    JSON.stringify({
      version: 1,
      repository: "fased-ai/fased-app",
      target: { os: "linux", arch: "amd64" },
      developmentOnly: true,
    }),
  );
  const fixture = {
    ...config,
    seed: fixtureSeed,
    fixture: true,
    url: "https://127.0.0.1:24443/fased-install",
    ca: Buffer.from("disposable public CA"),
  };
  const script = renderEntry(template, fixture);
  assertShellSyntax(script);
  assert.throws(() =>
    renderEntry(template, {
      ...fixture,
      url: "https://outside.example/fased-install",
    }),
  );
  assert.throws(() => renderEntry(template, { ...fixture, fixture: false }));
});

test("root receipts reject malformed phases and preserve managed lineage", () => {
  const declaration = template.match(/^operator_record\(\).*$/m)[0];
  for (const record of [
    "pending:" + "a".repeat(32),
    "ready:1001:1001:external",
    "ready:1002:1002:" + "b".repeat(32),
  ])
    assert.equal(
      spawnSync("bash", [
        "-c",
        declaration + '; operator_record "$1"',
        "record",
        record,
      ]).status,
      0,
    );
  for (const record of [
    "unknown:1001:1001",
    "ready:0:0:external",
    "ready:1001:1001",
    "pending:bad",
    "ready:1001:1001:external:extra",
    "ready:1001:1001:external\nforeign",
  ])
    assert.equal(
      spawnSync("bash", [
        "-c",
        declaration + '; operator_record "$1"',
        "record",
        record,
      ]).status,
      1,
    );
});

// The owner inherits root's working directory after runuser. Its first operations
// must enter the owner's home before cleanup/find tries to restore that directory.
test("ordinary phase leaves the administrator working directory", () => {
  const home = mkdtempSync(join(tmpdir(), "fased-entry-home-"));
  try {
    const prefix = template
      .split("owner_install() {\n")[1]
      .split("  umask 077")[0];
    const r = spawnSync("bash", ["-c", prefix + "pwd"], {
      cwd: "/",
      env: { ...process.env, HOME: home },
    });
    assert.equal(r.status, 0, r.stderr.toString());
    assert.equal(r.stdout.toString().trim(), home);
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

test("one release selects authenticated amd64 and arm64 tuples with update/local menu", () => {
  const armSeed = Buffer.from(
    JSON.stringify({
      ...JSON.parse(seed.toString()),
      target: { os: "linux", arch: "arm64" },
    }),
  );
  const script = renderEntry(template, {
    version: "1.0.0",
    targets: [
      config,
      { ...config, seed: armSeed, installer: Buffer.from("arm verifier") },
    ],
  });
  for (const value of [
    "linux-amd64)",
    "linux-arm64)",
    "ubuntu|debian) next_family=apt",
    "fedora|rhel|centos|rocky|almalinux) next_family=dnf",
    "ubuntu:24.04)",
    "ubuntu:26.04)",
    "ubuntu:26.10)",
    "--local)",
    "--update|--setup|--wallets)",
    "  Update",
  ]) {
    assert.ok(script.includes(value), value);
  }
  assert.ok(
    script.includes(
      'fedora:45:linux-arm64) die "No reviewed package for this OS and CPU."',
    ),
  );
  assertShellSyntax(script);
  assert.throws(() =>
    renderEntry(template, { version: "1.0.0", targets: [config, config] }),
  );
  assert.throws(() =>
    renderEntry(template, {
      version: "1.0.0",
      targets: [config, { ...config, version: "2.0.0", seed: armSeed }],
    }),
  );
});

test("single-owner entry preserves Local/Remote/Update and rejects profile invention", () => {
  const configured = {
    ...config,
    seed: Buffer.from(
      JSON.stringify({
        ...JSON.parse(seed),
        signingProfile: "single-owner-v1",
      }),
    ),
  };
  const script = renderEntry(template, configured);
  assert.match(script, /--local/);
  assert.match(script, /--remote/);
  assert.match(script, /--update/);
  assert.match(script, /--install --profile/);
  assert.doesNotMatch(script, /skip-verify/);
  assert.throws(() =>
    renderEntry(template, {
      ...configured,
      seed: Buffer.from(
        JSON.stringify({ ...JSON.parse(seed), signingProfile: "arbitrary" }),
      ),
    }),
  );
});

test("the one-command SSH wizard defaults to Remote without a second install command", () => {
  const decision = template.slice(
    template.indexOf("if [[ -z $profile"),
    template.indexOf("if [[ $profile == remote ]]; then"),
  );
  const body =
    "set -eu\nprofile=''\naction=install\nexec 3<>/dev/tty\ndie(){ printf '%s\\n' \\\"$*\\\"; exit 1; }\n" +
    decision +
    '\nprintf "SELECTED:%s\\n" "$profile"\n';
  const result = spawnSync(
    "python3",
    [
      "-c",
      `
import os,pty,select,signal,sys,time
pid,fd=pty.fork()
if pid==0:
 os.execvpe('bash',['bash','-c',sys.argv[1]],dict(os.environ,SSH_CONNECTION='192.0.2.1 20000 192.0.2.2 22'))
os.write(fd,b'\\n');out=b'';deadline=time.monotonic()+5;status=None
while time.monotonic()<deadline:
 if select.select([fd],[],[],.1)[0]:
  try:out+=os.read(fd,65536)
  except OSError:break
 done,value=os.waitpid(pid,os.WNOHANG)
 if done:status=value;break
if status is None:
 done,status=os.waitpid(pid,os.WNOHANG)
 if not done:os.kill(pid,signal.SIGKILL);_,status=os.waitpid(pid,0)
os.close(fd);sys.stdout.buffer.write(out);sys.exit(os.waitstatus_to_exitcode(status))
`,
      body,
    ],
    { timeout: 10000 },
  );
  assert.equal(result.status, 0, result.stderr?.toString());
  assert.match(result.stdout.toString(), /SELECTED:remote/);
});

test("bootstrap uses one protected helper without general sudo grants", () => {
  const script = renderEntry(template, config);
  assert.doesNotMatch(script, /NOTAFTER=|NOPASSWD:|for command in/);
  assert.match(script, /maintenance-register --owner/);
  assert.match(script, /maintenance-bootstrap --owner/);
  assert.match(script, /owner_maintenance_setup "\$installer"/);
  assert.match(
    script,
    /Hash the protected copy before any project executable receives root/,
  );
  assertShellSyntax(script);
});

test("resuming a held update stops at its result without starting another operation", () => {
  const start = template.indexOf(
    '  current=$(/usr/bin/sudo -- "$protected" maintenance-register',
  );
  const end = template.indexOf("  FASED_PROTECTED_INSTALLER=$protected", start);
  assert.ok(
    start >= 0 && end > start,
    "ordinary owner maintenance entry not found",
  );
  const entry = template
    .slice(start, end)
    .replaceAll(
      '/usr/bin/sudo -- "$protected" maintenance-register',
      "fake_register",
    )
    .replaceAll(
      '/usr/bin/sudo -- "$current" maintenance-bootstrap',
      "fake_bootstrap",
    );
  const rootStart = template.indexOf(
    'maintenance_image=$("$protected_installer" maintenance-register',
  );
  const rootEnd = template.indexOf("# Keep the bootstrap lock", rootStart);
  assert.ok(
    rootStart >= 0 && rootEnd > rootStart,
    "Remote bootstrap entry not found",
  );
  const rootEntry = template
    .slice(rootStart, rootEnd)
    .replace(/\nfi\s*$/, "\n")
    .replaceAll('"$protected_installer" maintenance-register', "fake_register")
    .replaceAll('"$maintenance_image" maintenance-bootstrap', "fake_bootstrap");
  const h1 =
    "/var/lib/fased-maintenance/images/" + "a".repeat(64) + "/fased-install";
  const h2 =
    "/var/lib/fased-maintenance/images/" + "b".repeat(64) + "/fased-install";
  const run = (selectedEntry, profile, action, fail) =>
    spawnSync(
      "bash",
      [
        "-c",
        `
    set -euo pipefail
    action=$1 protected=$2 protected_installer=$2 old=$3 recovered=0 fail=$4 profile=$5 owner_uid=1800
    die() { printf '%s\\n' "$*" >&2; exit 1; }
    fake_register() { if [[ $* == *--resume ]]; then printf '%s\\n' "$protected"; else printf '%s\\n' "$old"; fi; }
    fake_bootstrap() { [[ $fail == 0 ]]; }
    ${selectedEntry}
    printf 'CONTINUE_TO_INSTALLER\\n'
  `,
        "entry",
        action,
        h2,
        h1,
        fail,
        profile,
      ],
      { encoding: "utf8" },
    );
  for (const [selectedEntry, profile] of [
    [entry, "local"],
    [rootEntry, "remote"],
  ]) {
    const resumed = run(selectedEntry, profile, "update", "0");
    assert.notEqual(resumed.status, 0, resumed.stderr);
    assert.match(
      resumed.stderr,
      /app recovery and future updates are not yet verified/,
    );
    assert.doesNotMatch(resumed.stdout, /CONTINUE_TO_INSTALLER/);
    const failed = run(selectedEntry, profile, "update", "1");
    assert.notEqual(failed.status, 0);
    assert.doesNotMatch(
      failed.stderr,
      /app recovery and future updates are not yet verified/,
    );
    const fresh = run(selectedEntry, profile, "install", "0");
    assert.equal(fresh.status, 0, fresh.stderr);
    assert.match(fresh.stdout, /CONTINUE_TO_INSTALLER/);
  }
});

test("protected helper publication is durable and never copies into the final image", () => {
  const block = template.slice(
    template.indexOf('protected_installer="$helper_root/'),
  );
  assert.match(
    block,
    /install -m 0555 -o root -g root "\$stage\/fased-install" "\$stage\/protected-installer"/,
  );
  assert.match(
    block,
    /sync -f "\$stage\/protected-installer"[\s\S]*mv -T --no-clobber/,
  );
  assert.doesNotMatch(
    block,
    /^\s*\/usr\/bin\/install\s[^\n]*"\$protected_installer"/m,
  );
  assert.match(
    block,
    /Protected maintenance checksum differs[\s\S]*maintenance-register/,
  );
});

test("Wallets console consent keeps verified acquisition, owner drop and temporary cleanup", () => {
  const script = renderEntry(template, config);
  assertShellSyntax(script);
  for (const text of [
    "--wallets)",
    "--wallet-instance",
    "--wallet-network",
    '--entry-action "$action"',
    '--wallet-instance "$wallet_instance"',
    '--wallet-network "$wallet_network"',
    'action="$action" wallet_instance="${wallet_instance:-}" wallet_network="${wallet_network:-}"',
    "trap bootstrap_cleanup EXIT",
    "maintenance-register --owner",
    '/usr/sbin/runuser -u "$operator"',
  ])
    assert.ok(script.includes(text), text);
  assert.ok(
    script.indexOf("Wallets setup requires Remote") <
      script.indexOf("Preparing Linux for Fased"),
  );
  assert.match(
    script,
    /\[\[ \$action == install \|\| -n \$entry \]\] \|\| die 'No installed operator\.'/,
  );
  assert.ok(!script.includes("PermitRootLogin yes"));
  assert.ok(!script.includes("NOPASSWD: ALL"));
  assert.ok(!script.includes("--wallet-key"));
});

test("candidate entry verifies the pinned installer before protected setup", () => {
  const script = renderEntry(template, config);
  const verify = script.indexOf(
    '"$installer" --verify-candidate "$candidate_dir"',
  );
  const setup = script.indexOf('owner_maintenance_setup "$installer"', verify);
  assert.ok(verify > 0 && setup > verify);
  assert.ok(
    script.includes(
      '--install-candidate "$candidate_dir" --candidate-packet-sha256 "$candidate_packet"',
    ),
  );
  const first = script.indexOf(
    "if [[ -n $candidate_dir || -n $candidate_packet ]]",
  );
  const last = script.indexOf("\nfi\n\nowner_maintenance_setup()", first);
  assert.ok(first > 0 && last > first);
  const check = script.slice(first, last + "\nfi".length);
  const run = (directory, digest, profile, action) =>
    spawnSync("bash", [
      "-c",
      `
    candidate_dir=$1 candidate_packet=$2 profile=$3 action=$4 operator='' development_only=0
    die() { exit 1; }
    ${check}
  `,
      "candidate",
      directory,
      digest,
      profile,
      action,
    ]);
  const digest = "a".repeat(64);
  assert.equal(run("/tmp/candidate", digest, "local", "install").status, 0);
  assert.equal(run("/tmp/candidate", digest, "remote", "install").status, 0);
  for (const args of [
    ["relative", digest, "local", "install"],
    ["/tmp/candidate", digest, "connect", "install"],
    ["/tmp/candidate", digest, "local", "update"],
    ["/tmp/candidate", "bad", "local", "install"],
  ]) {
    assert.notEqual(run(...args).status, 0);
  }
});

test("unsupported native candidates stop before protected setup", () => {
  const script = renderEntry(template, config);
  const anchor = script.indexOf(
    "Installer checksum differs; nothing was executed.",
  );
  const start = script.indexOf("\n  if [[ -n $candidate_dir ]]; then", anchor);
  const end = script.indexOf(
    '\n  "$installer" "${verifier_args[@]}" <&3',
    start,
  );
  assert.ok(anchor > 0 && start > anchor && end > start);
  const entry = script.slice(start, end);
  const run = (report, status = "0") =>
    spawnSync("bash", [
      "-c",
      `
      set -u
      candidate_dir=/candidate candidate_packet='' package_instance='' installer=verifier report=$1 status=$2
      verifier() {
        if [[ $1 == --package-controller-ready ]]; then return 1; fi
        printf '%s\\n' "$report"; return "$status"
      }
      owner_maintenance_setup() { printf 'SETUP\\n'; }
      die() { printf 'REFUSED: %s\\n' "$*"; exit 23; }
      ${entry}
      `,
      "candidate",
      report,
      status,
    ]);
  const native = run('{"version":4,"linuxPackageDigestsVerified":true}');
  assert.equal(native.status, 23, native.stderr.toString());
  assert.doesNotMatch(native.stdout.toString(), /SETUP/);
  const bundle = run('{"version":4,"linuxBundleVerified":true}');
  assert.equal(bundle.status, 23, bundle.stderr.toString());
  assert.doesNotMatch(bundle.stdout.toString(), /SETUP/);
  const invalid = run("", "1");
  assert.equal(invalid.status, 23);
  assert.doesNotMatch(invalid.stdout.toString(), /SETUP/);
  const legacy = run('{"version":4,"provenanceVerified":true}');
  assert.equal(legacy.status, 0, legacy.stderr.toString());
  assert.match(legacy.stdout.toString(), /SETUP/);
});

test("controlled candidate uses only its compiled Local instance", () => {
  const script = renderEntry(template, config);
  const anchor = script.indexOf(
    "Installer checksum differs; nothing was executed.",
  );
  const start = script.indexOf("\n  if [[ -n $candidate_dir ]]; then", anchor);
  const end = script.indexOf(
    '\n  "$installer" "${verifier_args[@]}" <&3',
    start,
  );
  assert.ok(anchor > 0 && start > anchor && end > start);
  const entry = script
    .slice(start, end)
    .replace(
      '"/var/lib/fased-app-package/$(/usr/bin/id -u)/$profile"',
      '"$fixture_parent"',
    );
  const dir = mkdtempSync(join(tmpdir(), "fased-candidate-entry-"));
  try {
    const run = (
      instance,
      report = '{"version":4,"linuxPackageDigestsVerified":true}',
    ) =>
      spawnSync("bash", [
        "-c",
        `
      set -u
      candidate_dir=/candidate candidate_packet=$1 package_instance='' installer=verifier fixture_parent=$2 fixture=$3 report=$4 profile=local
      verifier_args=(--install-candidate /candidate --candidate-packet-sha256 "$candidate_packet" --profile local)
      verifier() {
        case $1 in
          --verify-candidate) printf '%s\\n' "$report" ;;
          --package-controller-ready) return 0 ;;
          --fixture-package-instance) printf '%s\\n' "$fixture" ;;
          *) return 1 ;;
        esac
      }
      owner_maintenance_setup() { printf 'SETUP %s\\n' "$package_instance"; }
      die() { printf 'REFUSED: %s\\n' "$*"; exit 23; }
      ${entry}
      printf 'ARGS %s\\n' "\${verifier_args[@]}"
      `,
        "candidate",
        "a".repeat(64),
        dir,
        instance,
        report,
      ]);
    const pinned = "b".repeat(32);
    const selected = run(pinned);
    assert.equal(selected.status, 0, selected.stderr.toString());
    assert.match(selected.stdout.toString(), new RegExp(`SETUP ${pinned}`));
    assert.match(selected.stdout.toString(), new RegExp(`ARGS ${pinned}`));
    const bundle = run(pinned, '{"version":4,"linuxBundleVerified":true}');
    assert.equal(bundle.status, 0, bundle.stderr.toString());
    assert.match(bundle.stdout.toString(), new RegExp(`SETUP ${pinned}`));
    const ordinary = run("");
    const ordinaryID = createHash("sha256")
      .update(`${process.getuid()}\0${"a".repeat(64)}\0local`)
      .digest("hex")
      .slice(0, 32);
    assert.equal(ordinary.status, 0, ordinary.stderr.toString());
    assert.match(ordinary.stdout.toString(), new RegExp(`SETUP ${ordinaryID}`));
    assert.match(ordinary.stdout.toString(), new RegExp(`ARGS ${ordinaryID}`));
    const invalid = run("not-an-instance");
    assert.equal(invalid.status, 23);
    assert.doesNotMatch(invalid.stdout.toString(), /SETUP/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("public bundle reaches only its verified Local or Remote package handoff", () => {
  const script = renderEntry(template, config);
  const anchor = script.indexOf(
    "Installer checksum differs; nothing was executed.",
  );
  const start = script.indexOf("\n  if [[ -n $candidate_dir ]]; then", anchor);
  const end = script.indexOf(
    '\n  "$installer" "${verifier_args[@]}" <&3',
    start,
  );
  assert.ok(anchor > 0 && start > anchor && end > start);
  const entry = script
    .slice(start, end)
    .replace(
      '"/var/lib/fased-app-package/$(/usr/bin/id -u)/$profile"',
      '"$fixture_parent"',
    );
  const dir = mkdtempSync(join(tmpdir(), "fased-public-entry-"));
  try {
    const packet = "a".repeat(64);
    const installer = "b".repeat(64);
    const run = (inspection, ready = 0, targetProfile = "local") =>
      spawnSync("bash", [
        "-c",
        `
      set -u
      candidate_dir='' candidate_packet='' package_instance='' profile=$5 action=install development_only=0 listen=127.0.0.1:7331
      installer=verifier entry_version=1.0.0 installer_sha256=$1 fixture_parent=$2 inspection=$3 ready=$4
      verifier_args=()
      verifier() {
        if [[ $1 == --inspect-public-package ]]; then printf '%s\\n' "$inspection"; return 0; fi
        if [[ $1 == --package-controller-ready ]]; then return "$ready"; fi
        return 1
      }
      owner_maintenance_setup() { printf 'SETUP %s\\n' "$package_instance"; }
      die() { printf 'REFUSED: %s\\n' "$*"; exit 23; }
      ${entry}
      printf 'ARGS %s\\n' "\${verifier_args[@]}"
      `,
        "public",
        installer,
        dir,
        inspection,
        String(ready),
        targetProfile,
      ]);
    const accepted = run(`native ${packet} 1.0.0 ${installer}`);
    assert.equal(accepted.status, 0, accepted.stderr.toString());
    assert.match(accepted.stdout.toString(), /SETUP [a-f0-9]{32}/);
    assert.match(accepted.stdout.toString(), /ARGS --install-public-package/);
    const remote = run(`native ${packet} 1.0.0 ${installer}`, 0, "remote");
    const remoteID = createHash("sha256")
      .update(`${process.getuid()}\0${packet}\0remote`)
      .digest("hex")
      .slice(0, 32);
    assert.equal(remote.status, 0, remote.stderr.toString());
    assert.match(remote.stdout.toString(), new RegExp(`SETUP ${remoteID}`));
    assert.match(remote.stdout.toString(), /ARGS remote/);
    for (const result of [
      run(`native ${packet} 1.0.0 ${installer}`, 1),
      run(`native ${packet} 1.0.1 ${installer}`),
      run(`native ${packet} 1.0.0 ${"c".repeat(64)}`),
    ]) {
      assert.equal(result.status, 23, result.stderr.toString());
      assert.doesNotMatch(result.stdout.toString(), /SETUP/);
    }
    writeFileSync(join(dir, "another-instance"), "preserve");
    const existing = run(`native ${packet} 1.0.0 ${installer}`);
    assert.equal(existing.status, 23, existing.stderr.toString());
    assert.doesNotMatch(existing.stdout.toString(), /SETUP/);
    assert.equal(
      readFileSync(join(dir, "another-instance"), "utf8"),
      "preserve",
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("Server entry supports multi-distro hosts without polkit/nftables and validates flags", () => {
  const armSeed = Buffer.from(
    JSON.stringify({
      ...JSON.parse(seed.toString()),
      target: { os: "linux", arch: "arm64" },
    }),
  );
  const rendered = renderEntry(template, {
    version: "1.0.0",
    targets: [
      config,
      { ...config, seed: armSeed, installer: Buffer.from("arm verifier") },
    ],
  });
  assertShellSyntax(rendered);

  // Package selection for --server skips polkitd/polkit/nftables while preserving Local.
  const ownerStart = template.indexOf(
    "  missing=()\n  packages=(ca-certificates curl)",
  );
  const end = template.indexOf("  for package in", ownerStart);
  const selection = template.slice(ownerStart, end);
  const pkgCheck = spawnSync("bash", [
    "-c",
    `
    set -eu
    profile=server package_family=apt action=install
    ${selection}
    printf '%s\\n' "\${packages[@]}"
  `,
  ]);
  assert.equal(pkgCheck.status, 0, pkgCheck.stderr.toString());
  const pkgs = pkgCheck.stdout.toString().trim().split("\n");
  assert.deepEqual(pkgs, ["ca-certificates", "curl"]);

  // Multi-distro server acceptance (Debian, Rocky, Fedora, Ubuntu; both amd64 & arm64).
  const dir = mkdtempSync(join(tmpdir(), "fased-server-entry-"));
  try {
    const osRelease = join(dir, "os-release");
    for (const tool of ["apt-get", "dpkg-query", "dnf", "rpm"]) {
      writeFileSync(join(dir, tool), "#!/bin/sh\nexit 0\n", { mode: 0o700 });
    }
    const detached = `import os,subprocess,sys
r=subprocess.run(['bash','-c',sys.argv[1],'entry','--server','--transport','cloudflare','--domain','fased.example.com'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,preexec_fn=os.setsid,timeout=4)
sys.stdout.buffer.write(r.stdout);sys.stderr.buffer.write(r.stderr);sys.exit(r.returncode)
`;
    for (const [release, cpu] of [
      ["ID=debian\nVERSION_ID=12\n", "aarch64"],
      ['ID=rocky\nID_LIKE="rhel fedora"\nVERSION_ID=9.5\n', "aarch64"],
      ["ID=fedora\nVERSION_ID=45\n", "x86_64"],
      ["ID=ubuntu\nVERSION_ID=24.04\n", "aarch64"],
    ]) {
      writeFileSync(osRelease, release);
      let script = rendered
        .replaceAll("/etc/os-release", osRelease)
        .replace("case $(/usr/bin/uname -m)", `case ${cpu}`);
      for (const tool of ["apt-get", "dpkg-query", "dnf", "rpm"]) {
        script = script.replaceAll(`/usr/bin/${tool}`, join(dir, tool));
      }
      const result = spawnSync("python3", ["-c", detached, script], {
        timeout: 6000,
      });
      assert.equal(result.status, 1, result.stderr.toString());
      assert.match(
        result.stderr.toString(),
        /Run this command in an interactive terminal/,
      );
    }

    // Bad flag combinations fail early with explicit diagnostics.
    for (const [args, wantErr] of [
      [["--server", "--transport", "bogus"], /Server transport must be/],
      [
        ["--transport", "cloudflare", "--domain", "fased.example.com"],
        /--transport and --domain require --server/,
      ],
      [
        ["--server", "--transport", "ssh", "--domain", "fased.example.com"],
        /SSH tunnel transport does not take --domain/,
      ],
      [
        ["--server", "--transport", "cloudflare", "--domain", "http://bad.com"],
        /Invalid server HTTPS domain/,
      ],
      [["--server", "--purge"], /--purge requires --uninstall/],
    ]) {
      const r = spawnSync("bash", ["-c", rendered, "entry", ...args]);
      assert.equal(r.status, 1);
      assert.match(r.stderr.toString(), wantErr);
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("install.sh avoids Bash 4 case-conversion syntax for macOS Bash 3.2 compatibility", () => {
  assert.doesNotMatch(template, /\$\{[^}]+(?:,,|\^\^)\}/);
  const rendered = renderEntry(template, config);
  assert.doesNotMatch(rendered, /\$\{[^}]+(?:,,|\^\^)\}/);
});

test("macOS Darwin entry pins archives, rejects --remote, and fails closed without targets", () => {
  const macArmBytes = Buffer.from("fake macos arm64 zip archive");
  const macAmdBytes = Buffer.from("fake macos amd64 zip archive");
  const macArmSha = createHash("sha256").update(macArmBytes).digest("hex");
  const macAmdSha = createHash("sha256").update(macAmdBytes).digest("hex");

  const withMac = renderEntry(template, {
    ...config,
    darwinTargets: [
      { arch: "arm64", archive: macArmBytes },
      { arch: "amd64", archive: macAmdBytes },
    ],
  });
  assertShellSyntax(withMac);
  assert.match(
    withMac,
    /https:\/\/github\.com\/fased-ai\/fased-app\/releases\/download\/v1\.0\.0\/fased-app-darwin-arm64\.zip/,
  );
  assert.match(
    withMac,
    /https:\/\/github\.com\/fased-ai\/fased-app\/releases\/download\/v1\.0\.0\/fased-app-darwin-amd64\.zip/,
  );
  assert.ok(withMac.includes(macArmSha));
  assert.ok(withMac.includes(macAmdSha));

  assert.throws(() =>
    renderEntry(template, {
      ...config,
      darwinTargets: [
        { arch: "arm64", archive: macArmBytes, url: "http://bad.invalid" },
      ],
    }),
  );
  assert.throws(() =>
    renderEntry(template, {
      ...config,
      darwinTargets: [
        { arch: "arm64", archive: macArmBytes },
        { arch: "arm64", archive: macArmBytes },
      ],
    }),
  );
  assert.throws(() => renderEntry(template, { ...config, darwinTargets: [] }));

  // Mock uname -s -> Darwin and uname -m -> arm64 inside a sandboxed run.
  const asDarwin = (raw) =>
    raw
      .replaceAll("$(/usr/bin/uname -s)", "Darwin")
      .replaceAll("$(/usr/bin/uname -m)", "arm64")
      .replaceAll("$(/usr/bin/id -u)", "501");

  const withoutMac = asDarwin(renderEntry(template, config));
  const darwinScript = asDarwin(withMac);

  const remoteErr = spawnSync("bash", [
    "-c",
    darwinScript,
    "entry",
    "--remote",
  ]);
  assert.equal(remoteErr.status, 1);
  assert.match(
    remoteErr.stderr.toString(),
    /Managed Remote requires a supported Linux host; use --server on macOS/,
  );

  const missingMacErr = spawnSync("bash", [
    "-c",
    withoutMac,
    "entry",
    "--local",
  ]);
  assert.equal(missingMacErr.status, 1);
  assert.match(
    missingMacErr.stderr.toString(),
    /No authenticated macOS package for darwin-arm64/,
  );

  for (const [args, wantErr] of [
    [["--server", "--transport", "bogus"], /Server transport must be/],
    [
      ["--server", "--transport", "ssh", "--domain", "mac.example.com"],
      /SSH tunnel transport does not take --domain/,
    ],
    [
      ["--local", "--uninstall", "--purge"],
      /--purge is only supported with --server --uninstall/,
    ],
  ]) {
    const bad = spawnSync("bash", ["-c", darwinScript, "entry", ...args]);
    assert.equal(bad.status, 1);
    assert.match(bad.stderr.toString(), wantErr);
  }

  const detachedLocal = spawnSync(
    "python3",
    [
      "-c",
      `import os,subprocess,sys
r=subprocess.run(['bash','-c',sys.argv[1],'entry','--local'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,preexec_fn=os.setsid,timeout=4)
sys.stdout.buffer.write(r.stdout);sys.stderr.buffer.write(r.stderr);sys.exit(r.returncode)
`,
      darwinScript,
    ],
    { timeout: 6000 },
  );
  assert.equal(detachedLocal.status, 1);
  assert.match(
    detachedLocal.stderr.toString(),
    /Run this command in an interactive terminal/,
  );
});

test("renderWindowsEntry pins release URLs, digests, and validates targets", () => {
  const winAmdBytes = Buffer.from("fake windows amd64 zip archive");
  const winArmBytes = Buffer.from("fake windows arm64 zip archive");
  const winAmdSha = createHash("sha256").update(winAmdBytes).digest("hex");
  const winArmSha = createHash("sha256").update(winArmBytes).digest("hex");

  const psScript = renderWindowsEntry(windowsTemplate, {
    version: "1.0.0",
    targets: [
      { arch: "amd64", archive: winAmdBytes },
      { arch: "arm64", archive: winArmBytes },
    ],
  });
  assert.doesNotMatch(psScript, /@[A-Z0-9_]+@/);
  assert.match(
    psScript,
    /https:\/\/github\.com\/fased-ai\/fased-app\/releases\/download\/v1\.0\.0\/fased-app-windows-amd64\.zip/,
  );
  assert.match(
    psScript,
    /https:\/\/github\.com\/fased-ai\/fased-app\/releases\/download\/v1\.0\.0\/fased-app-windows-arm64\.zip/,
  );
  assert.ok(psScript.includes(winAmdSha));
  assert.ok(psScript.includes(winArmSha));
  assert.match(psScript, /\$EntryVersion = '1\.0\.0'/);
  assert.match(psScript, /\$DevelopmentOnly = '0'/);

  // Fixture profile accepts only loopback HTTPS.
  const psFixture = renderWindowsEntry(windowsTemplate, {
    version: "1.0.0",
    fixture: true,
    targets: [
      {
        arch: "amd64",
        archive: winAmdBytes,
        url: "https://127.0.0.1:24443/fased-win.zip",
      },
    ],
  });
  assert.match(psFixture, /\$DevelopmentOnly = '1'/);

  assert.throws(() =>
    renderWindowsEntry(windowsTemplate, {
      version: "1.0.0",
      targets: [
        {
          arch: "amd64",
          archive: winAmdBytes,
          url: "http://evil.invalid/w.zip",
        },
      ],
    }),
  );
  assert.throws(() =>
    renderWindowsEntry(windowsTemplate, {
      version: "1.0.0",
      targets: [
        { arch: "amd64", archive: winAmdBytes },
        { arch: "amd64", archive: winAmdBytes },
      ],
    }),
  );
  assert.throws(() =>
    renderWindowsEntry(windowsTemplate, {
      version: "bad",
      targets: [{ arch: "amd64", archive: winAmdBytes }],
    }),
  );
});

test("install.ps1 enforces fail-closed TLS, SHA-256 pre-extraction verification, and balanced syntax", () => {
  const hashIdx = windowsTemplate.indexOf(
    "Get-FileHash -LiteralPath $ZipPath -Algorithm SHA256",
  );
  const expandIdx = windowsTemplate.indexOf(
    "Expand-Archive -LiteralPath $ZipPath",
  );
  assert.ok(
    hashIdx > 0 && expandIdx > hashIdx,
    "SHA-256 verification must precede Expand-Archive",
  );

  for (const required of [
    "Set-StrictMode -Version Latest",
    "SecurityProtocolType]::Tls12",
    "^127\\.0\\.0\\.1:([0-9]{4,5})$",
    "$ListenPort -lt 1024 -or $ListenPort -gt 65535",
    "[switch]$Local",
    "[switch]$Server",
    "[switch]$Connect",
    "[switch]$Uninstall",
    "[switch]$Purge",
    "$env:LOCALAPPDATA",
    "fased.cmd",
  ]) {
    assert.ok(
      windowsTemplate.includes(required),
      `Missing required contract: ${required}`,
    );
  }
  assert.doesNotMatch(
    windowsTemplate,
    /SkipCertificateCheck|ServerCertificateValidationCallback/,
  );

  // Scan install.ps1 for balanced braces, parens, and quotes (skipping # line comments & strings).
  let mode = "code"; // 'code' | 'single' | 'double' | 'comment'
  const stack = [];
  for (let i = 0; i < windowsTemplate.length; i++) {
    const ch = windowsTemplate[i];
    const next = windowsTemplate[i + 1];
    if (mode === "comment") {
      if (ch === "\n") mode = "code";
      continue;
    }
    if (mode === "single") {
      if (ch === "'") {
        if (next === "'") i++;
        else mode = "code";
      }
      continue;
    }
    if (mode === "double") {
      if (ch === "`" && next !== undefined) {
        i++;
      } else if (ch === '"') {
        if (next === '"') i++;
        else mode = "code";
      }
      continue;
    }
    if (ch === "#") {
      mode = "comment";
    } else if (ch === "'") {
      mode = "single";
    } else if (ch === '"') {
      mode = "double";
    } else if (ch === "{" || ch === "(") {
      stack.push(ch);
    } else if (ch === "}") {
      assert.equal(stack.pop(), "{", `Unmatched } near index ${i}`);
    } else if (ch === ")") {
      assert.equal(stack.pop(), "(", `Unmatched ) near index ${i}`);
    }
  }
  assert.equal(
    mode,
    "code",
    `Unterminated string literal (${mode}) in install.ps1`,
  );
  assert.deepEqual(stack, [], "Unclosed delimiters in install.ps1");
});
