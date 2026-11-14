import test from "node:test";
import assert from "node:assert/strict";
import { renderEntry } from "./render-install-entry.mjs";
import { spawnSync } from "node:child_process";
import { readFileSync, writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const template = readFileSync(
  new URL("../install.sh", import.meta.url),
  "utf8",
);
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
  assert.equal(spawnSync("bash", ["-n"], { input: script }).status, 0);
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

test("Fedora Local entry accepts only the mapped release and CPU before setup", () => {
  const dir = mkdtempSync(join(tmpdir(), "fased-os-entry-"));
  try {
    const osRelease = join(dir, "os-release");
    const detached = `import os,subprocess,sys
r=subprocess.run(['bash','-c',sys.argv[1],'entry','--local'],stdout=subprocess.PIPE,stderr=subprocess.PIPE,preexec_fn=os.setsid,timeout=4)
sys.stdout.buffer.write(r.stdout);sys.stderr.buffer.write(r.stderr);sys.exit(r.returncode)
`;
    for (const [version, cpu, expected] of [
      ["45", "x86_64", /Run this command in an interactive terminal/],
      ["45", "aarch64", /No reviewed package for this OS and CPU/],
      ["46", "x86_64", /This OS version has no reviewed mapping/],
    ]) {
      writeFileSync(osRelease, `ID=fedora\nVERSION_ID=${version}\n`);
      const script = renderEntry(template, config)
        .replaceAll("/etc/os-release", osRelease)
        .replace("case $(/usr/bin/uname -m)", `case ${cpu}`);
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
  assert.equal(spawnSync("bash", ["-n"], { input: script }).status, 0);
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
    "fedora:45)",
    "fedora:45:linux-amd64)",
    "ubuntu:22.04)",
    "ubuntu:24.04)",
    "ubuntu:26.04)",
    "ubuntu:26.10)",
    "--local)",
    "--update|--setup|--wallets)",
    "  Update",
  ]) {
    assert.ok(script.includes(value), value);
  }
  assert.ok(!script.includes("fedora:45:linux-arm64)"));
  assert.equal(spawnSync("bash", ["-n"], { input: script }).status, 0);
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
  assert.match(script, /Hash the protected copy before any project executable receives root/);
  const parsed = spawnSync("bash", ["-n"], { input: script });
  assert.equal(parsed.status, 0, parsed.stderr.toString());
});

test("resuming a held update stops at its result without starting another operation", () => {
  const start = template.indexOf('  current=$(/usr/bin/sudo -- "$protected" maintenance-register');
  const end = template.indexOf('  FASED_PROTECTED_INSTALLER=$protected', start);
  assert.ok(start >= 0 && end > start, "ordinary owner maintenance entry not found");
  const entry = template.slice(start, end)
    .replaceAll('/usr/bin/sudo -- "$protected" maintenance-register', 'fake_register')
    .replaceAll('/usr/bin/sudo -- "$current" maintenance-bootstrap', 'fake_bootstrap');
  const rootStart = template.indexOf('maintenance_image=$("$protected_installer" maintenance-register');
  const rootEnd = template.indexOf('# Keep the bootstrap lock', rootStart);
  assert.ok(rootStart >= 0 && rootEnd > rootStart, "Remote bootstrap entry not found");
  const rootEntry = template.slice(rootStart, rootEnd)
    .replaceAll('"$protected_installer" maintenance-register', 'fake_register')
    .replaceAll('"$maintenance_image" maintenance-bootstrap', 'fake_bootstrap');
  const h1 = "/var/lib/fased-maintenance/images/" + "a".repeat(64) + "/fased-install";
  const h2 = "/var/lib/fased-maintenance/images/" + "b".repeat(64) + "/fased-install";
  const run = (selectedEntry, profile, action, fail) => spawnSync("bash", ["-c", `
    set -euo pipefail
    action=$1 protected=$2 protected_installer=$2 old=$3 recovered=0 fail=$4 profile=$5 owner_uid=1800
    die() { printf '%s\\n' "$*" >&2; exit 1; }
    fake_register() { if [[ $* == *--resume ]]; then printf '%s\\n' "$protected"; else printf '%s\\n' "$old"; fi; }
    fake_bootstrap() { [[ $fail == 0 ]]; }
    ${selectedEntry}
    printf 'CONTINUE_TO_INSTALLER\\n'
  `, "entry", action, h2, h1, fail, profile], { encoding: "utf8" });
  for (const [selectedEntry, profile] of [[entry, "local"], [rootEntry, "remote"]]) {
    const resumed = run(selectedEntry, profile, "update", "0");
    assert.notEqual(resumed.status, 0, resumed.stderr);
    assert.match(resumed.stderr, /app recovery and future updates are not yet verified/);
    assert.doesNotMatch(resumed.stdout, /CONTINUE_TO_INSTALLER/);
    const failed = run(selectedEntry, profile, "update", "1");
    assert.notEqual(failed.status, 0);
    assert.doesNotMatch(failed.stderr, /app recovery and future updates are not yet verified/);
    const fresh = run(selectedEntry, profile, "install", "0");
    assert.equal(fresh.status, 0, fresh.stderr);
    assert.match(fresh.stdout, /CONTINUE_TO_INSTALLER/);
  }
});

test("protected helper publication is durable and never copies into the final image", () => {
  const block = template.slice(template.indexOf('protected_installer="$helper_root/'));
  assert.match(block, /install -m 0555 -o root -g root "\$stage\/fased-install" "\$stage\/protected-installer"/);
  assert.match(block, /sync -f "\$stage\/protected-installer"[\s\S]*mv -T --no-clobber/);
  assert.doesNotMatch(block, /^\s*\/usr\/bin\/install\s[^\n]*"\$protected_installer"/m);
  assert.match(block, /Protected maintenance checksum differs[\s\S]*maintenance-register/);
});



test("Wallets console consent keeps verified acquisition, owner drop and temporary cleanup", () => {
  const script = renderEntry(template, config);
  assert.equal(spawnSync("bash", ["-n"], { input: script }).status, 0);
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
      script.indexOf("Preparing Ubuntu for Fased"),
  );
  assert.match(
    script,
    /\[\[ \$action == install \|\| -n \$entry \]\] \|\| die 'No installed operator\.'/,
  );
  assert.ok(!script.includes("PermitRootLogin yes"));
  assert.ok(!script.includes("NOPASSWD: ALL"));
  assert.ok(!script.includes("--wallet-key"));
});

test("candidate Local entry verifies the pinned installer before protected setup", () => {
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
  for (const args of [
    ["relative", digest, "local", "install"],
    ["/tmp/candidate", digest, "remote", "install"],
    ["/tmp/candidate", digest, "local", "update"],
    ["/tmp/candidate", "bad", "local", "install"],
  ]) {
    assert.notEqual(run(...args).status, 0);
  }
});
