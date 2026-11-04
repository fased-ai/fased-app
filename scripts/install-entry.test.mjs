import test from "node:test";
import assert from "node:assert/strict";
import { renderEntry } from "./render-install-entry.mjs";
import { spawnSync } from "node:child_process";
import { readFileSync, mkdtempSync, rmSync } from "node:fs";
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
operator=fixture owner_home=/tmp action=update entry_version=1.0.0 installer_url='' installer_sha256='' seed_base64='' seed_sha256='' fixture_ca_base64='' development_only=1 listen=127.0.0.1:7331 profile=remote runtime='' ssh_user=root recovery_key=''
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
    "ubuntu:22.04)",
    "ubuntu:24.04)",
    "ubuntu:26.04)",
    "ubuntu:26.10)",
    "--local)",
    "--update|--setup)",
    "  Update",
  ]) {
    assert.ok(script.includes(value), value);
  }
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

test("Remote bootstrap scopes and expires only the fixed conditional Serve OS write", () => {
  const block = template.match(
    /# Only Remote setup may authorize[\s\S]*?\nfi\n/,
  )[0];
  const outputs = {};
  for (const profile of ["local", "remote"]) {
    const dir = mkdtempSync(join(tmpdir(), "fased-serve-rule-"));
    try {
      const result = spawnSync("bash", ["-c", block], {
        env: {
          ...process.env,
          stage: dir,
          profile,
          operator: "ubuntu",
          expires: "20990101000000Z",
        },
      });
      assert.equal(result.status, 0, result.stderr.toString());
      if (profile === "local") {
        assert.throws(() => readFileSync(join(dir, "authorization")));
        continue;
      }
      outputs.remote = readFileSync(join(dir, "authorization"), "utf8");
      assert.match(outputs.remote, /NOTAFTER=20990101000000Z/);
      assert.equal(outputs.remote.trim().split("\n").length, 1);
      const parsed = spawnSync("/usr/bin/visudo", ["-cf", "-"], {
        input: outputs.remote,
      });
      assert.equal(parsed.status, 0, parsed.stderr.toString());
      const rule = block.match(/serve_command='([^']+)'/)[1];
      // sudoers unescapes its colon separators before POSIX ERE matching.
      const expression = rule
        .slice("/usr/bin/curl ".length)
        .replaceAll("\\:", ":");
      const args =
        "--disable --silent --show-error --fail --max-time 15 --connect-timeout 5 --max-filesize 1048576 --noproxy * --proto =http --unix-socket /var/run/tailscale/tailscaled.sock --request POST --header Content-Type: application/json --header If-Match: " +
        "a".repeat(64) +
        " --data-binary @- --write-out %{http_code} http://local-tailscaled.sock/localapi/v0/serve-config";
      const match = spawnSync("python3", [
        "-c",
        `import re,sys
pattern=re.compile(sys.argv[1]); command=sys.argv[2]
assert pattern.fullmatch(command)
for bad in [command+' --output /etc/sudoers',command.replace('/var/run/tailscale/tailscaled.sock','/run/docker.sock'),command.replace('http://local-tailscaled.sock/localapi/v0/serve-config','http://outside.invalid/'),command.replace('POST','GET')]:
 assert not pattern.fullmatch(bad)
`,
        expression,
        args,
      ]);
      assert.equal(match.status, 0, match.stderr.toString());
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }
});
