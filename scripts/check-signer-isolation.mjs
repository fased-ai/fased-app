// Controlled CI only. Root runs trusted OS tools to create disposable accounts
// and copy fixed inputs. Every project executable runs AFTER setpriv drops UID,
// groups and capabilities. Never run this fixture on an owner's machine.
import assert from "node:assert/strict";
import { fixturePolicyJourney } from "./fixture-policy-journey.mjs";
import { spawn, execFileSync } from "node:child_process";
import { randomBytes, createHash } from "node:crypto";
import { mkdir, readFile, writeFile, lstat } from "node:fs/promises";
import { resolve } from "node:path";

assert.equal(process.env.GITHUB_ACTIONS, "true");
assert.equal(process.platform, "linux");
assert.match(await readFile("/etc/os-release", "utf8"), /VERSION_ID="24\.04"/);
assert.notEqual(process.getuid(), 0);
const evidence = resolve(".evidence/p3-signer");
await mkdir(evidence, { recursive: true, mode: 0o700 });
const token = randomBytes(6).toString("hex");
const root = `/etc/fased-signer-fixture-${token}`;
const binary = `${root}/fased-signer`;
const localBinary = resolve(".evidence/bin/fased-signer");
const users = [],
  servers = [];
const report = {
  result: "FAIL",
  class:
    "disposable Ubuntu CI, actual distinct kernel UIDs; not retail installed custody",
  fixtureOnly: true,
  financialExecution: false,
  binarySha256: createHash("sha256")
    .update(await readFile(localBinary))
    .digest("hex"),
  checks: {},
};
let createdRoot = false;
let log = "";
const sudo = (tool, args, options = {}) =>
  execFileSync("/usr/bin/sudo", ["-n", tool, ...args], {
    encoding: "utf8",
    timeout: 20000,
    ...options,
  });
const privileged = (tool, args) => sudo(tool, args);
const as = (user, tool, args, input) =>
  sudo(
    "/usr/bin/setpriv",
    [
      "--reuid",
      String(user.uid),
      "--regid",
      String(user.gid),
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
const check = (name, value) => {
  assert.equal(value, true, name);
  report.checks[name] = true;
};
const invoke = (user, config, role, action, input, extra = []) =>
  JSON.parse(
    as(
      user,
      binary,
      [
        role,
        "--config",
        config,
        "--disposable-fixture",
        "--action",
        action,
        ...extra,
      ],
      input,
    ),
  );
const fails = (fn, expected) => {
  try {
    fn();
    return false;
  } catch (error) {
    if (expected) assert.match(String(error.stderr || error.message), expected);
    return true;
  }
};
async function start(user, config) {
  const child = spawn(
    "/usr/bin/sudo",
    [
      "-n",
      "/usr/bin/setpriv",
      "--reuid",
      String(user.uid),
      "--regid",
      String(user.gid),
      "--clear-groups",
      "--no-new-privs",
      "--inh-caps=-all",
      "--ambient-caps=-all",
      "--bounding-set=-all",
      "--",
      binary,
      "serve",
      "--config",
      config,
      "--disposable-fixture",
    ],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  const record = {
    child,
    done: new Promise((resolve) =>
      child.once("exit", (code, signal) => resolve({ code, signal })),
    ),
    pid: null,
  };
  servers.push(record);
  let output = "";
  child.stdout.on("data", (b) => {
    output += b;
    log += b;
  });
  child.stderr.on("data", (b) => {
    log += b;
  });
  const until = Date.now() + 10000;
  while (!output.includes("Disposable signer ready")) {
    if (
      child.exitCode !== null ||
      child.signalCode !== null ||
      Date.now() > until
    )
      throw new Error("signer did not become ready; see isolated log");
    await new Promise((resolve) => setTimeout(resolve, 40));
  }
  const ready = output.match(/pid=(\d+) uid=(\d+)/);
  assert.equal(Number(ready[2]), user.uid);
  record.pid = Number(ready[1]);
  const status = await readFile(`/proc/${record.pid}/status`, "utf8");
  assert.match(
    status,
    new RegExp(
      `Uid:\\s+${user.uid}\\s+${user.uid}\\s+${user.uid}\\s+${user.uid}`,
    ),
  );
  return record;
}
async function stop(record) {
  if (record.child.exitCode === null && record.child.signalCode === null) {
    if (record.pid) privileged("/usr/bin/kill", ["-TERM", String(record.pid)]);
    else record.child.kill("SIGTERM");
    let timer;
    try {
      await Promise.race([
        record.done,
        new Promise((_, reject) => {
          timer = setTimeout(
            () => reject(new Error("signer stop timed out")),
            10000,
          );
        }),
      ]);
    } finally {
      clearTimeout(timer);
    }
  }
}
try {
  report.trustedAncestors = [];
  for (const directory of ["/", "/etc"]) {
    const info = await lstat(directory);
    assert.equal(info.uid, 0);
    assert.equal(info.isDirectory(), true);
    assert.equal(info.mode & 0o022, 0);
    report.trustedAncestors.push({
      path: directory,
      uid: info.uid,
      mode: info.mode & 0o777,
    });
  }
  // Refuse adoption of a pre-existing path, even with a random fixture suffix.
  assert.equal(
    await lstat(root).then(
      () => true,
      (e) => (e.code === "ENOENT" ? false : Promise.reject(e)),
    ),
    false,
  );
  privileged("/usr/bin/install", [
    "-d",
    "-m",
    "0755",
    "-o",
    "root",
    "-g",
    "root",
    root,
  ]);
  createdRoot = true;
  for (const suffix of ["owner", "app", "signer"]) {
    const name = `ff-${suffix}-${token}`;
    privileged("/usr/sbin/useradd", [
      "--system",
      "--user-group",
      "--no-create-home",
      "--shell",
      "/usr/sbin/nologin",
      name,
    ]);
    const uid = Number(
      execFileSync("/usr/bin/id", ["-u", name], { encoding: "utf8" }).trim(),
    );
    const gid = Number(
      execFileSync("/usr/bin/id", ["-g", name], { encoding: "utf8" }).trim(),
    );
    assert.ok(uid > 0);
    users.push({ name, uid, gid });
  }
  const [owner, app, signer] = users;
  check("distinctNonRootUIDs", new Set(users.map((u) => u.uid)).size === 3);
  report.identities = Object.fromEntries(
    users.map((u) => [u.name.split("-")[1], u.uid]),
  );
  privileged("/usr/bin/install", [
    "-m",
    "0755",
    "-o",
    "root",
    "-g",
    "root",
    localBinary,
    binary,
  ]);
  for (const [name, user, mode] of [
    ["state", signer, "0700"],
    ["sockets", signer, "0755"],
    ["restored-state", signer, "0700"],
    ["restored-sockets", signer, "0755"],
    ["owner-backups", owner, "0700"],
  ]) {
    privileged("/usr/bin/install", [
      "-d",
      "-m",
      mode,
      "-o",
      user.name,
      "-g",
      user.name,
      `${root}/${name}`,
    ]);
  }
  const config = {
    version: 1,
    mode: "disposable-fixture:v1",
    signerId: randomBytes(16).toString("hex"),
    ownerUid: owner.uid,
    appUid: app.uid,
    signerUid: signer.uid,
    stateDir: `${root}/state`,
    socketDir: `${root}/sockets`,
  };
  async function publishConfig(name, value) {
    const local = `${evidence}/${name}`;
    await writeFile(local, JSON.stringify(value) + "\n", { mode: 0o600 });
    privileged("/usr/bin/install", [
      "-m",
      "0644",
      "-o",
      "root",
      "-g",
      "root",
      local,
      `${root}/${name}`,
    ]);
    return `${root}/${name}`;
  }
  const configPath = await publishConfig("identity.json", config);
  check(
    "wrongDaemonUIDDenied",
    fails(
      () =>
        as(app, binary, [
          "serve",
          "--config",
          configPath,
          "--disposable-fixture",
        ]),
      /daemon must run only as its configured unprivileged signer UID/,
    ),
  );
  const sameUID = await publishConfig("same-uid.json", {
    ...config,
    appUid: owner.uid,
  });
  check(
    "sameUIDTopologyDenied",
    fails(
      () =>
        as(signer, binary, [
          "serve",
          "--config",
          sameUID,
          "--disposable-fixture",
        ]),
      /three distinct non-root UIDs/,
    ),
  );
  let server = await start(signer, configPath);
  const unlock = "Disposable CI unlock password";
  const backupPassword = "Disposable CI backup password";
  const replacement = "Disposable CI replacement password";
  const initial = invoke(
    owner,
    configPath,
    "owner",
    "create",
    `${unlock}\n${unlock}\n`,
    ["--password-stdin"],
  );
  check(
    "createdLockedAndDisabled",
    initial.locked &&
      initial.fixtureOnly &&
      !initial.walletSigner &&
      !initial.financialExecution &&
      !initial.unattendedUnlock,
  );
  // Bypass the client role guard. The daemon must enforce actual kernel UID,
  // even when a malicious app constructs the owner protocol itself.
  const probe = String.raw`
import os,socket,json,sys
c=json.loads(sys.argv[1]); result={}
for name,p in [('operations.log',c['stateDir']+'/operations.log'),('mock-receipts.log',c['stateDir']+'/mock-receipts.log'),('vault.json',c['stateDir']+'/vault.json'),('binary',os.path.dirname(c['stateDir'])+'/fased-signer'),('configuration',os.path.dirname(c['stateDir'])+'/identity.json')]:
 try: fd=os.open(p,os.O_RDWR); os.close(fd); result[name]=False
 except PermissionError: result[name]=True
s=socket.socket(socket.AF_UNIX); s.connect(c['socketDir']+'/owner.sock'); result['ownerChannelDenied']=s.recv(1)==b''; s.close()
s=socket.socket(socket.AF_UNIX); s.connect(c['socketDir']+'/app.sock'); f=s.makefile('rwb'); hello=json.loads(f.readline())
f.write((json.dumps({'version':1,'signerId':c['signerId'],'challenge':hello['challenge'],'request':{'action':'export-fixture-seed'}})+'\n').encode()); f.flush()
r=json.loads(f.readline()); result['rawExportDenied']=bool(r.get('error')) and not r.get('document'); s.close()
for action in ['fixture-policy','fixture-grant','fixture-revoke','fixture-recover-authority','fixture-approve-proposal']:
 s=socket.socket(socket.AF_UNIX);s.connect(c['socketDir']+'/app.sock');f=s.makefile('rwb');h=json.loads(f.readline());f.write((json.dumps({'version':1,'signerId':c['signerId'],'challenge':h['challenge'],'request':{'action':action}})+'\n').encode());f.flush();v=json.loads(f.readline());result['raw:'+action]=v.get('error')=='owner administration denied';s.close()
print(json.dumps(result))
`;
  const denied = JSON.parse(
    as(app, "/usr/bin/python3", ["-c", probe, JSON.stringify(config)]),
  );
  for (const [name, value] of Object.entries(denied))
    check(`app:${name}`, value);
  const view = invoke(app, configPath, "app", "status");
  check(
    "appSeesPublicReferenceOnly",
    view.account.publicKey === initial.account.publicKey && !view.walletSigner,
  );
  check(
    "appCannotUseOwnerHelper",
    fails(() => invoke(app, configPath, "owner", "status")),
  );
  check(
    "arbitrarySigningUnavailable",
    fails(() => invoke(owner, configPath, "owner", "sign")),
  );
  invoke(owner, configPath, "owner", "unlock", `${unlock}\n`, [
    "--password-stdin",
  ]);
  const backup = `${root}/owner-backups/backup.json`;
  const seed = `${root}/owner-backups/seed.fixture`;
  invoke(
    owner,
    configPath,
    "owner",
    "backup",
    `${backupPassword}\n${backupPassword}\n`,
    ["--password-stdin", "--file", backup],
  );
  invoke(owner, configPath, "owner", "export-fixture-seed", undefined, [
    "--file",
    seed,
  ]);
  check(
    "appCannotReadOwnerBackup",
    fails(() => as(app, "/usr/bin/cat", [backup])),
  );
  check(
    "ownerCannotReadSignerFilesDirectly",
    fails(() => as(owner, "/usr/bin/cat", [`${config.stateDir}/vault.json`])),
  );
  check(
    "exportNeverPrinted",
    !log.includes(unlock) && !log.includes(backupPassword),
  );
  fixturePolicyJourney({
    account: initial.account,
    check,
    fails,
    owner: (action, body = {}) =>
      invoke(owner, configPath, "owner", action, JSON.stringify(body), [
        "--request-stdin",
      ]),
    app: (action, body = {}) =>
      invoke(app, configPath, "app", action, JSON.stringify(body), [
        "--request-stdin",
      ]),
  });
  await stop(server);
  server = await start(signer, configPath);
  const restarted = invoke(owner, configPath, "owner", "status");
  check(
    "restartLocksAndPreservesIdentity",
    restarted.locked &&
      restarted.account.walletId === initial.account.walletId &&
      restarted.account.epoch === initial.account.epoch,
  );
  check(
    "fixture:restartPreservesLifetimeSpend",
    invoke(app, configPath, "app", "fixture-state").fixture.spent === "70",
  );
  check(
    "lockedExportDenied",
    fails(() =>
      invoke(owner, configPath, "owner", "export-fixture-seed", undefined, [
        "--file",
        `${root}/owner-backups/locked.fixture`,
      ]),
    ),
  );
  await stop(server);
  const restoredConfig = {
    ...config,
    signerId: randomBytes(16).toString("hex"),
    stateDir: `${root}/restored-state`,
    socketDir: `${root}/restored-sockets`,
  };
  const restoredPath = await publishConfig(
    "restored-identity.json",
    restoredConfig,
  );
  server = await start(signer, restoredPath);
  check(
    "wrongBackupPasswordDenied",
    fails(() =>
      invoke(
        owner,
        restoredPath,
        "owner",
        "restore",
        `${unlock}\n${replacement}\n${replacement}\n`,
        ["--password-stdin", "--file", backup],
      ),
    ),
  );
  const restored = invoke(
    owner,
    restoredPath,
    "owner",
    "restore",
    `${backupPassword}\n${replacement}\n${replacement}\n`,
    ["--password-stdin", "--file", backup],
  );
  check(
    "restoreFreshAuthorityAndHold",
    restored.locked &&
      restored.recoveryRequired &&
      restored.account.publicKey === initial.account.publicKey &&
      restored.account.walletId === initial.account.walletId &&
      restored.account.epoch !== initial.account.epoch &&
      restored.account.signerId !== initial.account.signerId &&
      !restored.walletSigner,
  );
  check(
    "restoreCannotOverwrite",
    fails(() =>
      invoke(
        owner,
        restoredPath,
        "owner",
        "restore",
        `${backupPassword}\n${replacement}\n${replacement}\n`,
        ["--password-stdin", "--file", backup],
      ),
    ),
  );
  invoke(owner, restoredPath, "owner", "unlock", `${replacement}\n`, [
    "--password-stdin",
  ]);
  const recoveredSeed = `${root}/owner-backups/recovered.fixture`;
  invoke(owner, restoredPath, "owner", "export-fixture-seed", undefined, [
    "--file",
    recoveredSeed,
  ]);
  as(owner, "/usr/bin/cmp", [seed, recoveredSeed]);
  check("recoveredKeyMatches", true);
  check(
    "recoveryHoldCannotClear",
    fails(() => invoke(owner, restoredPath, "owner", "clear-recovery-hold")),
  );
  await stop(server);
  check("allProjectProcessesUnprivileged", true);
  report.result = "PASS";
} catch (error) {
  report.error = error.message;
  throw error;
} finally {
  const cleanupErrors = [];
  for (const server of servers) {
    try {
      await stop(server);
    } catch (e) {
      cleanupErrors.push(e.message);
    }
  }
  if (createdRoot && !cleanupErrors.length) {
    assert.match(root, /^\/etc\/fased-signer-fixture-[a-f0-9]{12}$/);
    try {
      privileged("/usr/bin/rm", ["-r", "--", root]);
    } catch (e) {
      cleanupErrors.push(e.message);
    }
  }
  for (const user of users.toReversed()) {
    try {
      assert.equal(
        Number(
          execFileSync("/usr/bin/id", ["-u", user.name], {
            encoding: "utf8",
          }).trim(),
        ),
        user.uid,
      );
      privileged("/usr/sbin/userdel", [user.name]);
    } catch (e) {
      cleanupErrors.push(e.message);
    }
  }
  report.cleanup = cleanupErrors.length
    ? cleanupErrors
    : "PASS: generated processes, unfunded keys, state and accounts removed";
  if (cleanupErrors.length) report.result = "FAIL";
  await writeFile(`${evidence}/isolation.log`, log);
  await writeFile(
    `${evidence}/isolation-result.json`,
    JSON.stringify(report, null, 2) + "\n",
  );
  if (cleanupErrors.length) throw new Error("signer fixture cleanup failed");
}
console.log(
  "PASS: distinct owner/app/signer UIDs, kernel denials and encrypted recovery; no live wallet capability.",
);
