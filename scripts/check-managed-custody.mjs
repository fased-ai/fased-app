// CI-only disposable installation proof. Never run this on an owner's host.
// Root executes trusted OS tools; all Fased executables run under non-root UIDs.
import assert from "node:assert/strict";
import { fixtureAccountJourney } from "./fixture-account-journey.mjs";
import { fixtureUpdateIntentJourney } from "./fixture-update-intent-journey.mjs";
import {
  fixturePolicyJourney,
  fixtureRecoveryJourney,
} from "./fixture-policy-journey.mjs";
import { execFileSync, spawn } from "node:child_process";
import { randomBytes, createHash } from "node:crypto";
import { watch } from "node:fs";
import {
  mkdir,
  readFile,
  writeFile,
  lstat,
  readdir,
  rm,
} from "node:fs/promises";
import { resolve } from "node:path";
import net from "node:net";

assert.equal(process.env.GITHUB_ACTIONS, "true");
assert.equal(process.platform, "linux");
assert.match(await readFile("/etc/os-release", "utf8"), /VERSION_ID="24\.04"/);
const uid = process.getuid();
assert.ok(uid > 0);
const evidence = resolve(".evidence/p3-managed");
await mkdir(evidence, { recursive: true, mode: 0o700 });
const home = resolve(evidence, "owner-config"),
  backups = resolve(evidence, "backups");
await mkdir(home, { mode: 0o700 });
await mkdir(backups, { mode: 0o700 });
const env = { ...process.env, XDG_CONFIG_HOME: home };
const engine = resolve(".evidence/bin/fased-app"),
  signer = resolve(".evidence/bin/fased-signer");
const root = `/var/lib/fased-app-custody-${uid}`;
const userNames = [`fased-fx-${uid}-app`, `fased-fx-${uid}-signer`];
const units = [
  `fased-custody-${uid}-app.service`,
  `fased-custody-${uid}-signer.service`,
];
const override = `/etc/systemd/system/${units[0]}.d`;
const token = randomBytes(8).toString("hex");
const foreignText = `Disposable foreign fixture ${token}`;
const appPassword = `Disposable UI password ${token}`;
const unlock = `Disposable unlock password ${token}`;
const backupPassword = `Disposable backup password ${token}`;
const replacement = `Disposable recovered password ${token}`;
const report = {
  result: "FAIL",
  class:
    "Ubuntu CI actual installer/systemd/UID/owner-helper proof; not public or owner-host acceptance",
  fixtureOnly: true,
  financialExecution: false,
  checks: {},
  inputs: {},
};
let manifest,
  intent,
  transient,
  foreignUser = false,
  ownsInfrastructure = false,
  log = "";
for (const path of [engine, signer])
  report.inputs[path] = createHash("sha256")
    .update(await readFile(path))
    .digest("hex");
const sha = (b) => createHash("sha256").update(b).digest("hex");
const osTool = (tool, args, options = {}) =>
  execFileSync("/usr/bin/sudo", ["-n", tool, ...args], {
    encoding: "utf8",
    timeout: 30000,
    ...options,
  });
const cli = (args, input) =>
  execFileSync(engine, ["custody", ...args, "--disposable-fixture"], {
    env,
    input,
    encoding: "utf8",
    timeout: 60000,
  });
const fails = (fn, pattern) => {
  try {
    fn();
    return false;
  } catch (e) {
    if (pattern) assert.match(String(e.stderr || e.message), pattern);
    return true;
  }
};
const exists = async (p) =>
  lstat(p).then(
    () => true,
    (e) => (e.code === "ENOENT" ? false : Promise.reject(e)),
  );
const check = (name, value) => {
  assert.equal(value, true, name);
  report.checks[name] = true;
};
const as = (id, tool, args, input) =>
  osTool(
    "/usr/bin/setpriv",
    [
      "--reuid",
      String(id.uid),
      "--regid",
      String(id.gid),
      "--clear-groups",
      "--no-new-privs",
      "--inh-caps=-all",
      "--ambient-caps=-all",
      "--bounding-set=-all",
      "--",
      tool,
      ...args,
    ],
    { input },
  );
const fixtureOwner = (action, body = {}) =>
  JSON.parse(
    execFileSync(
      root + "/fased-signer",
      [
        "owner",
        "--config",
        root + "/identity.json",
        "--disposable-fixture",
        "--action",
        action,
        "--request-stdin",
      ],
      { input: JSON.stringify(body), encoding: "utf8", timeout: 10000 },
    ),
  );
const fixtureApp = (identity, action, body = {}) =>
  JSON.parse(
    as(
      identity,
      root + "/fased-signer",
      [
        "app",
        "--config",
        root + "/identity.json",
        "--disposable-fixture",
        "--action",
        action,
        "--request-stdin",
      ],
      JSON.stringify(body),
    ),
  );
const unlockFixture = (password) =>
  execFileSync(
    root + "/fased-signer",
    [
      "owner",
      "--config",
      root + "/identity.json",
      "--disposable-fixture",
      "--action",
      "unlock",
      "--password-stdin",
    ],
    { input: password + "\n", encoding: "utf8", timeout: 10000 },
  );
const secretStatus = () =>
  JSON.parse(
    execFileSync(
      root + "/fased-signer",
      ["owner", "--config", root + "/identity.json", "--disposable-fixture"],
      { encoding: "utf8", timeout: 10000 },
    ),
  );
async function readySigner() {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    try {
      return secretStatus();
    } catch (error) {
      if (
        !/dial unix.*(no such file|connection refused)/s.test(
          String(error.stderr || error.message),
        )
      )
        throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  throw new Error("signer owner RPC did not become ready after restart");
}
const installArgs = ["install", "--signer-binary", signer, "--prepare-host"];
let listen;
async function freePort() {
  const server = net.createServer();
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const p = server.address().port;
  await new Promise((r) => server.close(r));
  return `127.0.0.1:${p}`;
}
async function healthy() {
  const until = Date.now() + 15000;
  while (Date.now() < until) {
    try {
      const r = await fetch("http://" + listen + "/api/v1/health", {
        signal: AbortSignal.timeout(1000),
      });
      if (r.ok) return await r.json();
    } catch {}
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error("managed browser listener did not become ready");
}
async function ownerTerminal() {
  // Python and the PTY driver run as the ordinary CI owner. sudo inside the
  // product elevates only the fixed credential switcher, never this script.
  const source = String.raw`
import os,pty,select,sys,time
binary=sys.argv[1];password=sys.stdin.readline().rstrip('\n');pid,fd=pty.fork()
if pid==0: os.execv(binary,[binary,'custody','reset-app-password','--disposable-fixture','--prepare-host'])
seen=b'';stage=0;done=0;deadline=time.time()+20
while time.time()<deadline:
 ready,_,_=select.select([fd],[],[],.1)
 if ready:
  try: chunk=os.read(fd,4096)
  except OSError: break
  if not chunk: break
  seen+=chunk
  if stage==0 and b'New owner password:' in seen:
   time.sleep(.05);os.write(fd,password.encode()+b'\n');stage=1
  if stage==1 and b'Confirm new password:' in seen:
   time.sleep(.05);os.write(fd,password.encode()+b'\n');stage=2
 done,status=os.waitpid(pid,os.WNOHANG)
 if done:
  assert os.waitstatus_to_exitcode(status)==0,seen.decode(errors='replace');break
else:
 os.kill(pid,9);os.waitpid(pid,0);raise Exception('owner terminal timed out')
if not done: _,status=os.waitpid(pid,0)
assert os.waitstatus_to_exitcode(status)==0,seen.decode(errors='replace')
assert stage==2 and b'Owner password replaced' in seen,seen.decode(errors='replace')
assert password.encode() not in seen,'password was echoed by terminal'
print('PASS: actual owner terminal app recovery, no password echo')
`;
  const child = spawn("/usr/bin/python3", ["-c", source, engine], {
    env,
    stdio: ["pipe", "pipe", "pipe"],
  });
  child.stdin.end(appPassword + " replacement\n");
  transient = child;
  let output = "";
  child.stdout.on("data", (b) => (output += b));
  child.stderr.on("data", (b) => (output += b));
  const code = await new Promise((resolve, reject) => {
    child.once("exit", resolve);
    child.once("error", reject);
  });
  transient = null;
  assert.equal(code, 0, output);
  log += output;
}
try {
  check(
    "freshRootAndUnits",
    !(await exists(root)) &&
      !(await exists(override)) &&
      !(await exists("/etc/systemd/system/" + units[0])) &&
      !(await exists("/etc/systemd/system/" + units[1])),
  );
  for (const name of userNames)
    check(
      `noExisting:${name}`,
      fails(() =>
        execFileSync("/usr/bin/getent", ["passwd", name], { stdio: "pipe" }),
      ),
    );
  ownsInfrastructure = true;
  listen = await freePort();
  installArgs.push("--listen", listen);
  check(
    "missingConsentDenied",
    fails(() => cli(["install", "--signer-binary", signer]), /consent/),
  );
  check("noRootWithoutConsent", !(await exists(root)));
  osTool("/usr/bin/install", ["-d", "-m", "0755", root]);
  osTool("/usr/bin/tee", [root + "/foreign"], { input: foreignText });
  check(
    "foreignRootDenied",
    fails(() => cli(installArgs), /unclassified installation root/),
  );
  check(
    "foreignRootPreserved",
    (await readFile(root + "/foreign", "utf8")) === foreignText,
  );
  assert.deepEqual(await readdir(root), ["foreign"]);
  osTool("/usr/bin/rm", ["--", root + "/foreign"]);
  osTool("/usr/bin/rmdir", ["--", root]);
  osTool("/usr/sbin/useradd", [
    "--system",
    "--user-group",
    "--no-create-home",
    "--shell",
    "/usr/sbin/nologin",
    "--comment",
    foreignText,
    userNames[0],
  ]);
  foreignUser = true;
  check(
    "foreignAccountDenied",
    fails(() => cli(installArgs), /foreign account identity/),
  );
  check(
    "foreignAccountPreserved",
    execFileSync("/usr/bin/getent", ["passwd", userNames[0]], {
      encoding: "utf8",
    }).includes(foreignText),
  );
  osTool("/usr/sbin/userdel", [userNames[0]]);
  foreignUser = false;
  intent = JSON.parse(
    await readFile(home + "/fased-app-custody-installer/intent.json", "utf8"),
  );
  const stage = `/var/lib/.fased-custody-${uid}-${intent.id}.pending`;
  osTool("/usr/bin/install", ["-d", "-m", "0755", override]);
  const overrideBytes = "[Service]\nUser=0\n";
  osTool("/usr/bin/tee", [override + "/10-foreign.conf"], {
    input: overrideBytes,
  });
  // Kill the actual unprivileged coordinator while its trusted OS copy is in
  // progress. A fixed pending original may remain; retry must preserve intent.
  const pending = `.fased-app-${intent.id}.pending`;
  let observed = false;
  const child = spawn(
    engine,
    ["custody", ...installArgs, "--disposable-fixture"],
    { env, stdio: ["ignore", "pipe", "pipe"] },
  );
  transient = child;
  child.stdout.on("data", (b) => (log += b));
  child.stderr.on("data", (b) => (log += b));
  const done = new Promise((r) =>
    child.once("exit", (code, signal) => r({ code, signal })),
  );
  const watcher = watch(stage, (_event, name) => {
    if (name?.toString() === pending && !observed) {
      observed = true;
      child.kill("SIGKILL");
    }
  });
  let timer;
  let killed;
  try {
    killed = await Promise.race([
      done,
      new Promise((_, reject) => {
        timer = setTimeout(
          () => reject(new Error("installer did not reach owned copy phase")),
          30000,
        );
      }),
    ]);
  } finally {
    clearTimeout(timer);
    watcher.close();
    if (!observed) child.kill("SIGKILL");
  }
  transient = null;
  check("actualInstallerInterrupted", observed && killed.signal === "SIGKILL");
  await new Promise((r) => setTimeout(r, 250));
  check("noPublishedRootAfterCopyInterruption", !(await exists(root)));
  // Complete the copy, but refuse the pre-existing policy override before any
  // Fased process can start under its User=0 setting.
  check(
    "foreignSystemdOverrideDenied",
    fails(() => cli(installArgs), /unexpected systemd identity\/override/),
  );
  manifest = JSON.parse(await readFile(root + "/install.json", "utf8"));
  check("sameIntentAfterInterruption", manifest.id === intent.id);
  check(
    "foreignOverridePreserved",
    (await readFile(override + "/10-foreign.conf", "utf8")) === overrideBytes,
  );
  for (const name of units)
    check(
      `notStartedWithOverride:${name}`,
      execFileSync(
        "/usr/bin/systemctl",
        ["show", name, "--property=MainPID", "--value"],
        { encoding: "utf8" },
      ).trim() === "0",
    );
  osTool("/usr/bin/rm", ["--", override + "/10-foreign.conf"]);
  osTool("/usr/bin/rmdir", ["--", override]);
  osTool("/usr/bin/systemctl", ["daemon-reload"]);
  const installed = JSON.parse(cli(installArgs));
  manifest = installed.manifest;
  check(
    "installedSeparateUIDs",
    new Set([manifest.owner, manifest.app.uid, manifest.signer.uid]).size ===
      3 &&
      manifest.app.uid > 0 &&
      manifest.signer.uid > 0,
  );
  check(
    "installedDisabledWalletCapability",
    installed.fixtureOnly &&
      !installed.walletSigner &&
      !installed.financialExecution &&
      !installed.autostart,
  );
  const baseline = {};
  for (const name of [
    "fased-app",
    "fased-signer",
    "identity.json",
    "install.json",
    "ready.json",
  ]) {
    const path = root + "/" + name;
    baseline[name] = {
      hash: sha(await readFile(path)),
      mtime: (await lstat(path)).mtimeMs,
    };
  }
  const repeated = JSON.parse(cli(installArgs));
  check("sameCommandSameIdentity", repeated.manifest.id === manifest.id);
  for (const [name, b] of Object.entries(baseline)) {
    check(
      `retryUnchanged:${name}`,
      sha(await readFile(root + "/" + name)) === b.hash &&
        (await lstat(root + "/" + name)).mtimeMs === b.mtime,
    );
  }
  const health = await healthy();
  check(
    "browserCoreReady",
    health.status === "ok" &&
      !health.capabilities.walletSigner &&
      !health.capabilities.financialExecution,
  );
  const code = cli(["claim-code", "--prepare-host"]).trim();
  const claim = await fetch("http://" + listen + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: "http://" + listen, "Content-Type": "application/json" },
    body: JSON.stringify({ code, password: appPassword }),
  });
  assert.equal(claim.status, 200);
  const claimed = await claim.json();
  // Follow the actual UI contract: HTTP loopback uses origin-scoped bearer
  // sessions; private HTTPS uses Secure cookies. Never weaken that distinction.
  assert.equal(claimed.sessionMode, "bearer");
  const session = claimed.sessionToken;
  assert.equal(typeof session, "string");
  const ownerState = await fetch("http://" + listen + "/api/v1/auth/state", {
    headers: { Authorization: "Bearer " + session },
  });
  check("ownerCanClaimUI", (await ownerState.json()).authenticated === true);
  const backup = backups + "/wallet.backup";
  const setup = JSON.parse(
    cli(
      ["setup", "--backup-file", backup, "--password-stdin"],
      `${unlock}\n${unlock}\n${backupPassword}\n${backupPassword}\n`,
    ),
  );
  const initial = setup.status;
  check(
    "protectedSetupVerifiedAndLocked",
    setup.backupVerified &&
      initial.locked &&
      !initial.walletSigner &&
      !initial.financialExecution,
  );
  const probe = String.raw`
import os,socket,json,sys
c=json.loads(sys.argv[1]);r={}
try: os.open(c['stateDir']+'/vault.json',os.O_RDONLY);r['keyReadDenied']=False
except PermissionError:r['keyReadDenied']=True
s=socket.socket(socket.AF_UNIX);s.connect(c['socketDir']+'/owner.sock');r['ownerChannelDenied']=s.recv(1)==b'';s.close()
s=socket.socket(socket.AF_UNIX);s.connect(c['socketDir']+'/app.sock');f=s.makefile('rwb');h=json.loads(f.readline());f.write((json.dumps({'version':1,'signerId':c['signerId'],'challenge':h['challenge'],'request':{'action':'verify-backup'}})+'\n').encode());f.flush();v=json.loads(f.readline());r['ownerActionDenied']=bool(v.get('error'))
for action in ['fixture-policy','fixture-grant','fixture-revoke','fixture-recover-authority','fixture-approve-proposal','fixture-update-inventory','fixture-update-status','fixture-update-prepare','fixture-update-abort','fixture-update-stage-status','fixture-update-stage-write','fixture-update-stage-finish']:
 s=socket.socket(socket.AF_UNIX);s.connect(c['socketDir']+'/app.sock');f=s.makefile('rwb');h=json.loads(f.readline());f.write((json.dumps({'version':1,'signerId':c['signerId'],'challenge':h['challenge'],'request':{'action':action}})+'\n').encode());f.flush();v=json.loads(f.readline());r['raw:'+action]=v.get('error')=='owner administration denied';s.close()
for name in ['operations.log','mock-receipts.log']:
 try: fd=os.open(c['stateDir']+'/'+name,os.O_RDONLY);os.close(fd);r['read:'+name]=False
 except PermissionError: r['read:'+name]=True
print(json.dumps(r))
`;
  const cfg = JSON.parse(await readFile(root + "/identity.json", "utf8"));
  for (const [name, v] of Object.entries(
    JSON.parse(
      as(manifest.app, "/usr/bin/python3", ["-c", probe, JSON.stringify(cfg)]),
    ),
  ))
    check(name, v);
  check(
    "appCannotRunOwnerHelper",
    fails(
      () =>
        as(manifest.app, root + "/fased-signer", [
          "setup",
          "--config",
          root + "/identity.json",
          "--disposable-fixture",
          "--file",
          backup,
        ]),
      /owner administration denied/,
    ),
  );
  check(
    "ownerCannotRunManagedCore",
    fails(
      () =>
        execFileSync(
          root + "/fased-app",
          [
            "serve",
            "--data-dir",
            root + "/app-state",
            "--installation-root",
            root,
            "--require-sandbox",
            "--custody-config",
            root + "/identity.json",
          ],
          { encoding: "utf8", timeout: 10000 },
        ),
      /managed core identity/,
    ),
  );
  osTool("/usr/bin/systemctl", ["restart", units[1]]);
  const restartedSigner = await readySigner();
  check(
    "signerRestartLocked",
    restartedSigner.locked &&
      restartedSigner.account.publicKey === initial.account.publicKey,
  );
  unlockFixture(unlock);
  const policyJourney = fixturePolicyJourney({
    account: initial.account,
    check,
    fails,
    owner: fixtureOwner,
    app: (action, body) => fixtureApp(manifest.app, action, body),
  });
  execFileSync(
    root + "/fased-signer",
    [
      "owner",
      "--config",
      root + "/identity.json",
      "--disposable-fixture",
      "--action",
      "lock",
    ],
    { encoding: "utf8" },
  );
  const keyHash = sha(
    osTool("/usr/bin/cat", [root + "/signer-state/vault.json"], {
      encoding: null,
    }),
  );
  check(
    "recoveryCannotOverwriteExistingWallet",
    fails(
      () =>
        cli(
          ["recover", "--backup-file", backup, "--password-stdin"],
          "unused\n",
        ),
      /existing wallets are preserved/,
    ),
  );
  check(
    "overwriteRefusalPreservesKey",
    sha(
      osTool("/usr/bin/cat", [root + "/signer-state/vault.json"], {
        encoding: null,
      }),
    ) === keyHash,
  );
  // Explicitly simulate loss of this disposable key, after a verified encrypted
  // backup. Product uninstall never performs either of these deletions.
  osTool("/usr/bin/systemctl", ["stop", units[1]]);
  osTool("/usr/bin/rm", [
    "--",
    root + "/signer-state/vault.json",
    root + "/signer-state/vault.lock",
  ]);
  osTool("/usr/bin/systemctl", ["start", units[1]]);
  await readySigner();
  check(
    "wrongRecoveryCredentialDenied",
    fails(
      () =>
        cli(
          ["recover", "--backup-file", backup, "--password-stdin"],
          `Wrong disposable password\n${replacement}\n${replacement}\n`,
        ),
      /invalid credential/,
    ),
  );
  check("failedRecoveryRemainsEmpty", !secretStatus().account);
  const recovered = JSON.parse(
    cli(
      ["recover", "--backup-file", backup, "--password-stdin"],
      `${backupPassword}\n${replacement}\n${replacement}\n`,
    ),
  );
  check(
    "recoveredSameKeyWithHold",
    recovered.backupVerified &&
      recovered.status.locked &&
      recovered.status.recoveryRequired &&
      recovered.status.account.publicKey === initial.account.publicKey &&
      recovered.status.account.epoch !== initial.account.epoch &&
      !recovered.status.walletSigner,
  );
  const recoveredHash = sha(
    osTool("/usr/bin/cat", [root + "/signer-state/vault.json"], {
      encoding: null,
    }),
  );
  await ownerTerminal();
  await healthy();
  const revoked = await fetch("http://" + listen + "/api/v1/setup", {
    headers: { Origin: "http://" + listen, Authorization: "Bearer " + session },
  });
  check("oldAppSessionRevoked", revoked.status === 401);
  check(
    "appPasswordRecoveryPreservesSigner",
    secretStatus().recoveryRequired &&
      secretStatus().locked &&
      sha(
        osTool("/usr/bin/cat", [root + "/signer-state/vault.json"], {
          encoding: null,
        }),
      ) === recoveredHash,
  );
  const oldLogin = await fetch("http://" + listen + "/api/v1/auth/login", {
    method: "POST",
    headers: { Origin: "http://" + listen, "Content-Type": "application/json" },
    body: JSON.stringify({ password: appPassword }),
  });
  assert.equal(oldLogin.status, 401);
  const newLogin = await fetch("http://" + listen + "/api/v1/auth/login", {
    method: "POST",
    headers: { Origin: "http://" + listen, "Content-Type": "application/json" },
    body: JSON.stringify({ password: appPassword + " replacement" }),
  });
  assert.equal(newLogin.status, 200);
  check("appPasswordRecoveryUsesNewCredential", true);
  unlockFixture(replacement);
  fixtureRecoveryJourney({
    account: recovered.status.account,
    previous: policyJourney,
    owner: fixtureOwner,
    app: (action, body) => fixtureApp(manifest.app, action, body),
    check,
    fails,
  });
  execFileSync(
    root + "/fased-signer",
    [
      "owner",
      "--config",
      root + "/identity.json",
      "--disposable-fixture",
      "--action",
      "lock",
    ],
    { encoding: "utf8" },
  );
  const journalHashes = Object.fromEntries(
    ["operations.log", "mock-receipts.log"].map((name) => [
      name,
      sha(
        osTool("/usr/bin/cat", [root + "/signer-state/" + name], {
          encoding: null,
        }),
      ),
    ]),
  );
  // A changed managed unit must abort removal without stopping either service.
  const appUnit = "/etc/systemd/system/" + units[0];
  const original = await readFile(appUnit);
  osTool("/usr/bin/tee", [appUnit], {
    input: Buffer.concat([
      original,
      Buffer.from("\n# owned mutation fixture\n"),
    ]),
  });
  check(
    "alteredUnitRemovalDenied",
    fails(
      () => cli(["remove", "--prepare-host"]),
      /foreign installed unit|changed/,
    ),
  );
  osTool("/usr/bin/install", [
    "-m",
    "0644",
    "-o",
    "root",
    "-g",
    "root",
    root + "/fased-app.service",
    appUnit,
  ]);
  osTool("/usr/bin/systemctl", ["daemon-reload"]);
  for (const name of units) osTool("/usr/bin/systemctl", ["stop", name]);
  const stoppedDB = sha(
    osTool("/usr/bin/cat", [root + "/app-state/core.sqlite"], {
      encoding: null,
    }),
  );
  cli(["remove", "--prepare-host"]);
  check(
    "removePreservesDataAndIdentities",
    sha(
      osTool("/usr/bin/cat", [root + "/app-state/core.sqlite"], {
        encoding: null,
      }),
    ) === stoppedDB &&
      sha(
        osTool("/usr/bin/cat", [root + "/signer-state/vault.json"], {
          encoding: null,
        }),
      ) === recoveredHash,
  );
  cli(["remove", "--prepare-host"]);
  const reinstated = JSON.parse(cli(installArgs));
  check(
    "reinstallKeepsIdentityAndReconciledAuthority",
    reinstated.manifest.id === manifest.id &&
      !secretStatus().recoveryRequired &&
      secretStatus().account.epoch === recovered.status.account.epoch &&
      (await healthy()).instanceId === health.instanceId,
  );
  check(
    "fixture:removalAndReinstallPreserveJournals",
    Object.entries(journalHashes).every(
      ([name, hash]) =>
        sha(
          osTool("/usr/bin/cat", [root + "/signer-state/" + name], {
            encoding: null,
          }),
        ) === hash,
    ),
  );
  const preservedSummary = fixtureApp(manifest.app, "fixture-state").fixture;
  check(
    "fixture:reinstallPreservesSpentAndHeldBudget",
    preservedSummary.spent === "70" &&
      preservedSummary.held === "30" &&
      secretStatus().locked,
  );
  await fixtureAccountJourney({
    url: "http://" + listen,
    engine,
    cli,
    app: (action, body) => fixtureApp(manifest.app, action, body),
    ownerUnlock: unlockFixture,
    ownerLock: () => fixtureOwner("lock"),
    ownerStatus: secretStatus,
    check,
    fails,
    account: recovered.status.account,
    appPassword: appPassword + " replacement",
    walletPassword: replacement,
    backups,
    evidence,
    env,
  });
  await fixtureUpdateIntentJourney({
    engine,
    root,
    cli,
    ownerStatus: secretStatus,
    ownerUnlock: unlockFixture,
    walletPassword: replacement,
    restartSigner: () => osTool("/usr/bin/systemctl", ["restart", units[1]]),
    backups,
    check,
    fails,
    env,
  });
  report.identities = {
    owner: uid,
    app: manifest.app.uid,
    signer: manifest.signer.uid,
  };
  report.installationId = manifest.id;
  report.result = "PASS";
} catch (error) {
  report.error = error.message;
  throw error;
} finally {
  const cleanup = [];
  if (ownsInfrastructure && report.result !== "PASS") {
    for (const unit of units) {
      try {
        log +=
          "\n" +
          osTool("/usr/bin/systemctl", [
            "show",
            unit,
            "--property=ActiveState,MainPID,User,Group,FragmentPath,DropInPaths",
          ]);
        log +=
          "\n" +
          osTool("/usr/bin/journalctl", ["--no-pager", "-n", "35", "-u", unit]);
      } catch (error) {
        log += "\nservice diagnostics unavailable: " + error.message;
      }
    }
  }
  if (transient && transient.exitCode === null && transient.signalCode === null)
    transient.kill("SIGKILL");
  // Every destination below was proven absent before this disposable CI test.
  // Preserve the report first; no funded key or authoritative journal exists.
  if (ownsInfrastructure)
    try {
      if (await exists(override + "/10-foreign.conf")) {
        osTool("/usr/bin/rm", ["--", override + "/10-foreign.conf"]);
        osTool("/usr/bin/rmdir", ["--", override]);
      }
      for (const unit of units) {
        if (await exists("/etc/systemd/system/" + unit)) {
          osTool("/usr/bin/systemctl", ["stop", unit]);
          osTool("/usr/bin/rm", ["--", "/etc/systemd/system/" + unit]);
        }
      }
      osTool("/usr/bin/systemctl", ["daemon-reload"]);
      if (await exists(root)) {
        assert.match(root, /^\/var\/lib\/fased-app-custody-\d+$/);
        osTool("/usr/bin/rm", ["-r", "--", root]);
      }
      if (
        !intent &&
        (await exists(home + "/fased-app-custody-installer/intent.json"))
      )
        intent = JSON.parse(
          await readFile(
            home + "/fased-app-custody-installer/intent.json",
            "utf8",
          ),
        );
      if (intent) {
        const stage = `/var/lib/.fased-custody-${uid}-${intent.id}.pending`;
        if (await exists(stage)) {
          assert.match(intent.id, /^[a-f0-9]{32}$/);
          osTool("/usr/bin/rm", ["-r", "--", stage]);
        }
      }
      for (const name of userNames.toReversed()) {
        let line;
        try {
          line = execFileSync("/usr/bin/getent", ["passwd", name], {
            encoding: "utf8",
          });
        } catch {
          continue;
        }
        assert.ok(
          (intent && line.includes(`Fased disposable ${intent.id} `)) ||
            (foreignUser && line.includes(foreignText)),
        );
        osTool("/usr/sbin/userdel", [name]);
      }
    } catch (error) {
      cleanup.push(error.message);
    }
  report.cleanup = cleanup.length
    ? cleanup
    : "PASS: only generated fixture services, accounts and unfunded state removed";
  if (cleanup.length) report.result = "FAIL";
  // Fixture passwords are never retained, including failed terminal diagnostics.
  for (const p of [
    appPassword + " replacement",
    appPassword,
    unlock,
    backupPassword,
    replacement,
  ]) {
    log = log.replaceAll(p, "[redacted fixture credential]");
    if (report.error)
      report.error = report.error.replaceAll(
        p,
        "[redacted fixture credential]",
      );
  }
  await writeFile(evidence + "/managed-install.log", log);
  await writeFile(
    evidence + "/managed-install-result.json",
    JSON.stringify(report, null, 2) + "\n",
  );
  if (!cleanup.length) {
    await rm(backups, { recursive: true });
    await rm(home, { recursive: true });
  }
  if (cleanup.length) throw new Error("managed custody fixture cleanup failed");
}
console.log(
  "PASS: installer-managed identities, actual sandboxed services and protected owner recovery; wallets disabled.",
);
