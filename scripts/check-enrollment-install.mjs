// Actual disposable Ubuntu enrollment only. Never run on an owner's machine.
// Root runs fixed OS utilities; every project/Python command runs as an ordinary UID.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { watch } from "node:fs";
import { mkdir, readFile, writeFile, lstat, rm } from "node:fs/promises";
import { homedir } from "node:os";
import { resolve, join } from "node:path";
import net from "node:net";
import {
  enrollmentAPI,
  seedEnrollmentSource,
  snapshotCode,
} from "./fixture-enrollment-state.mjs";
import {
  readEnrollmentObservation,
  removeEnrollmentUnitLink,
} from "./fixture-enrollment-observer.mjs";

assert.equal(
  process.env.FASED_ENROLLMENT_ACCEPTANCE,
  "1",
  "explicit FASED_ENROLLMENT_ACCEPTANCE required",
);
assert.equal(process.env.GITHUB_ACTIONS, "true");
assert.equal(process.env.RUNNER_ENVIRONMENT, "github-hosted");
assert.equal(process.platform, "linux");
assert.ok(process.getuid() > 0 && process.getuid() === process.geteuid());
assert.match(await readFile("/etc/os-release", "utf8"), /VERSION_ID="24\.04"/);
assert.equal(resolve(process.env.GITHUB_WORKSPACE), process.cwd());
const { chromium } = await import("@playwright/test");

const uid = process.getuid(),
  gid = process.getgid();
const evidence = resolve(".evidence/b05-enrollment-installed");
await mkdir(evidence, { mode: 0o700 });
const compiledEngine = resolve(".evidence/bin/fased-app");
let engine = compiledEngine;
const signer = resolve(".evidence/bin/fased-signer");
const config = process.env.XDG_CONFIG_HOME || join(homedir(), ".config");
const custodyRoot = `/var/lib/fased-app-custody-${uid}`;
const remoteRoot = `/var/lib/fased-app-vps-${uid}`;
const unitNames = [
  `fased-custody-${uid}-app.service`,
  `fased-custody-${uid}-signer.service`,
];
const userNames = [
  `fased-fx-${uid}-app`,
  `fased-fx-${uid}-signer`,
  `fased-vps-${uid}`,
];
const journalNames = [
  "fased-app-enrollment",
  "fased-app-custody-installer",
  "fased-app-vps-installer",
];
const env = {
  ...process.env,
  XDG_RUNTIME_DIR: `/run/user/${uid}`,
  DBUS_SESSION_BUS_ADDRESS: `unix:path=/run/user/${uid}/bus`,
};
const secrets = new Set();
const token = randomBytes(8).toString("hex");
const password = `Disposable enrollment owner ${token}`;
const vaultPassword = `Disposable enrollment vault ${token}`;
const key = `fased-test-enrollment-${token}`;
for (const s of [password, vaultPassword, key]) secrets.add(s);
const scrub = (value) =>
  [...secrets].reduce(
    (s, secret) => s.replaceAll(secret, "[fixture secret]"),
    String(value),
  );
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
const report = {
  result: "FAIL",
  class:
    "actual disposable Ubuntu Local/Remote same-instance enrollment; not owner/VPS/native/live acceptance",
  fixtureOnly: true,
  mining: false,
  liveFeatures: false,
  checks: {},
  profiles: {},
  inputs: {},
};
for (const p of [engine, signer]) report.inputs[p] = sha(await readFile(p));
let log = "",
  child = null,
  browser = null,
  owns = false,
  changedLinger = false,
  startedManager = false;
const exists = (p) =>
  lstat(p).then(
    () => true,
    (e) => (e.code === "ENOENT" ? false : Promise.reject(e)),
  );
const execute = (file, args, input, extra = {}) =>
  execFileSync(file, args, {
    env,
    input,
    encoding: "utf8",
    timeout: 60000,
    maxBuffer: 4 << 20,
    ...extra,
  });
const osTool = (file, args, input) =>
  execute("/usr/bin/sudo", ["-n", "--", file, ...args], input);
const cli = (args, input) => execute(engine, args, input);
const failed = (fn) => {
  try {
    fn();
    return false;
  } catch {
    return true;
  }
};
const check = (name, value) => {
  assert.equal(value, true, name);
  report.checks[name] = true;
};
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const as = (identity, file, args, input) =>
  osTool(
    "/usr/bin/setpriv",
    [
      "--reuid",
      String(identity.uid),
      "--regid",
      String(identity.gid),
      "--clear-groups",
      "--no-new-privs",
      "--inh-caps=-all",
      "--ambient-caps=-all",
      "--bounding-set=-all",
      "--",
      file,
      ...args,
    ],
    input,
  );
const normal = (identity, file, args, input) =>
  identity.uid === uid
    ? execute(file, args, input)
    : as(identity, file, args, input);
const freeListen = async () => {
  const s = net.createServer();
  await new Promise((r) => s.listen(0, "127.0.0.1", r));
  const port = s.address().port;
  await new Promise((r) => s.close(r));
  return `127.0.0.1:${port}`;
};
async function health(url, expected) {
  const until = Date.now() + 15000;
  while (Date.now() < until) {
    try {
      const r = await fetch(url + "/api/v1/health", {
        signal: AbortSignal.timeout(1000),
      });
      const h = await r.json();
      if (r.ok && h.status === "ok" && (!expected || h.instanceId === expected))
        return h;
    } catch {}
    await wait(100);
  }
  throw new Error("exact private instance did not become ready");
}
const api = (url, session, path, method = "GET", body) =>
  enrollmentAPI(url, session, path, method, body, scrub);
function snapshot(identity, data, hold = false) {
  return JSON.parse(
    normal(identity, "/usr/bin/python3", [
      "-c",
      snapshotCode,
      data,
      hold ? "hold" : "read",
    ]),
  );
}

async function failureInventory() {
  const detail = {};
  const statCode = String.raw`
import errno,json,os,stat,sys
assert os.geteuid()!=0
root=sys.argv[1];out=[]
def visit(path,name,depth):
    if len(out)>=256:return
    try:s=os.lstat(path)
    except OSError as e:
        out.append({'name':name,'errno':e.errno});return
    kind=('dir' if stat.S_ISDIR(s.st_mode) else 'file' if stat.S_ISREG(s.st_mode) else 'link' if stat.S_ISLNK(s.st_mode) else 'socket' if stat.S_ISSOCK(s.st_mode) else 'other')
    out.append({'name':name,'type':kind,'mode':format(stat.S_IMODE(s.st_mode),'04o'),'uid':s.st_uid,'gid':s.st_gid,'nlink':s.st_nlink,'size':s.st_size})
    if kind!='dir' or depth>=4:return
    try:
        with os.scandir(path) as entries:names=sorted(e.name for e in entries)
    except OSError as e:
        out.append({'name':name,'errno':e.errno});return
    for item in names[:256]:visit(os.path.join(path,item),item if name=='.' else name+'/'+item,depth+1)
visit(root,'.',0)
print(json.dumps(out,separators=(',',':')))
`;
  const inventory = (identity, path) =>
    JSON.parse(normal(identity, "/usr/bin/python3", ["-c", statCode, path]));
  if (source?.identity) {
    try {
      detail.source = inventory(source.identity, source.data);
    } catch {
      detail.source = { unavailable: true };
    }
  }
  try {
    const bytes = await readFile(
      join(config, "fased-app-enrollment", "state.json"),
    );
    assert.ok(bytes.length <= 16384);
    const journal = JSON.parse(bytes);
    assert.match(journal.id, /^[a-f0-9]{32}$/);
    assert.match(journal.phase, /^[a-z-]{1,64}$/);
    detail.journal = { id: journal.id, phase: journal.phase };
    const target = journal.target;
    assert.equal(target.owner, uid);
    assert.equal(target.root, custodyRoot);
    assert.match(target.id, /^[a-f0-9]{32}$/);
    for (const identity of [target.app, target.signer])
      assert.ok(
        Number.isSafeInteger(identity.uid) &&
          identity.uid > 0 &&
          identity.uid !== uid &&
          Number.isSafeInteger(identity.gid) &&
          identity.gid > 0,
      );
    detail.target = {
      id: target.id,
      owner: target.owner,
      appUid: target.app.uid,
      appGid: target.app.gid,
      signerUid: target.signer.uid,
      signerGid: target.signer.gid,
    };
    detail.stage = inventory(
      target.app,
      `${custodyRoot}/enrollment-${journal.id}.stage`,
    );
  } catch {
    detail.journalUnavailable = true;
  }
  return detail;
}

async function browserProof(url, profile, instance) {
  const ctx = await browser.newContext({
    viewport: { width: 1360, height: 900 },
    locale: "en-US",
  });
  const external = [];
  try {
    await ctx.route("**/*", (route) => {
      if (new URL(route.request().url()).origin !== url) {
        external.push(route.request().url());
        return route.abort();
      }
      return route.continue();
    });
    const page = await ctx.newPage();
    await page.goto(url + "/#wallets/accounts");
    await page.getByLabel("Owner password", { exact: true }).fill(password);
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
    await page.getByRole("heading", { name: "Wallets", exact: true }).waitFor();
    const h = await page.evaluate(async () =>
      (await fetch("/api/v1/health")).json(),
    );
    check(
      `${profile}:browserOriginalIdentity`,
      h.instanceId === instance &&
        h.connection.location === profile &&
        h.connection.ownerUid === uid,
    );
    await page.screenshot({ path: join(evidence, `${profile}-wallets.png`) });
    check(`${profile}:browserNoExternalRequests`, external.length === 0);
  } finally {
    await ctx.close();
  }
}

async function controlledInterruption(args, instance) {
  const dir = join(config, "fased-app-enrollment");
  await mkdir(dir, { mode: 0o700 });
  let paused = false,
    busy = false,
    journalDurable = false,
    observed,
    interruptionError,
    current;
  const preFence = new Set([
    "prepared",
    "suspending",
    "suspended",
    "importing",
    "staged",
  ]);
  // Observe before spawn, then confirm a stopped process and durable phase.
  const watcher = watch(dir, async (_event, name) => {
    if (
      !current ||
      name?.toString() !== "state.json" ||
      paused ||
      busy ||
      interruptionError
    )
      return;
    busy = true;
    try {
      const j = await readEnrollmentObservation(join(dir, "state.json"), {
        durable: journalDurable,
      });
      if (j === undefined) return;
      journalDurable = true;
      if (
        preFence.has(j.phase) &&
        j.target.id &&
        j.source.expectedInstanceId === instance
      ) {
        current.kill("SIGSTOP");
        const until = Date.now() + 2000;
        while (Date.now() < until) {
          const status = await readFile(`/proc/${current.pid}/status`, "utf8");
          if (/^State:\s+T/m.test(status)) break;
          await wait(5);
        }
        const status = await readFile(`/proc/${current.pid}/status`, "utf8");
        assert.match(status, /^State:\s+T/m, "coordinator did not stop");
        const stable = await readEnrollmentObservation(
          join(dir, "state.json"),
          { durable: true },
        );
        assert.ok(
          preFence.has(stable.phase) &&
            stable.target.id &&
            stable.source.expectedInstanceId === instance,
          "interruption raced past source fencing",
        );
        observed = stable;
        paused = true;
      }
    } catch (e) {
      interruptionError = e;
    } finally {
      busy = false;
    }
  });
  child = spawn(engine, args, { env, stdio: ["ignore", "pipe", "pipe"] });
  current = child;
  current.stdout.on("data", (b) => {
    log += scrub(b);
  });
  current.stderr.on("data", (b) => {
    log += scrub(b);
  });
  const done = new Promise((r, reject) => {
    current.once("exit", (code, signal) => r({ code, signal }));
    current.once("error", reject);
  });
  try {
    const until = Date.now() + 30000;
    while (
      !paused &&
      !interruptionError &&
      current.exitCode === null &&
      current.signalCode === null &&
      Date.now() < until
    )
      await wait(10);
    if (interruptionError) throw interruptionError;
    assert.ok(
      paused,
      "coordinator did not expose its durable prepared checkpoint",
    );
    const stage = `${custodyRoot}/enrollment-${observed.id}.stage`;
    osTool("/usr/bin/install", [
      "-d",
      "-m",
      "0700",
      "-o",
      String(observed.target.app.uid),
      "-g",
      String(observed.target.app.gid),
      "--",
      stage,
    ]);
    const marker = "disposable enrollment foreign-stage fixture";
    const markerFile = join(evidence, `foreign-${observed.id}.fixture`);
    await writeFile(markerFile, marker, { mode: 0o600, flag: "wx" });
    try {
      osTool("/usr/bin/install", [
        "-m",
        "0600",
        "-o",
        String(observed.target.app.uid),
        "-g",
        String(observed.target.app.gid),
        "--",
        markerFile,
        stage + "/foreign",
      ]);
    } finally {
      await rm(markerFile);
    }
    current.kill("SIGKILL");
    const exit = await done;
    child = null;
    check("local:actualCoordinatorInterrupted", exit.signal === "SIGKILL");
    let refusal = "";
    try {
      cli(args);
    } catch (e) {
      refusal = String(e.stderr || e.message);
    }
    log += scrub(refusal) + "\n";
    check(
      "local:foreignStageRefused",
      /enrollment import stage: invalid or conflicting enrollment image; preserve original state/.test(
        refusal,
      ),
    );
    check(
      "local:foreignStagePreserved",
      as(observed.target.app, "/usr/bin/cat", [stage + "/foreign"]) === marker,
    );
    check(
      "local:noEmptyTargetEngine",
      failed(() =>
        execute("/usr/bin/systemctl", ["is-active", "--quiet", unitNames[0]]),
      ),
    );
    as(observed.target.app, "/usr/bin/rm", ["--", stage + "/foreign"]);
  } finally {
    watcher.close();
    if (current.exitCode === null && current.signalCode === null) {
      current.kill("SIGCONT");
      current.kill("SIGKILL");
      await done;
    }
    child = null;
  }
}

async function cleanProfile(source) {
  const failures = [];
  const fixtureIds = new Set();
  const attempt = async (f) => {
    try {
      await f();
    } catch (e) {
      failures.push(scrub(e.message));
    }
  };
  for (const name of [...unitNames, `fased-vps-${uid}.service`])
    await attempt(async () => {
      const path = "/etc/systemd/system/" + name;
      if (await exists(path)) {
        osTool("/usr/bin/systemctl", ["disable", "--now", "--", name]);
        osTool("/usr/bin/rm", ["--", path]);
      }
    });
  await attempt(async () => {
    const userUnit = join(config, "systemd/user/fased-app.service");
    if (await exists(userUnit)) {
      execute("/usr/bin/systemctl", [
        "--user",
        "disable",
        "--now",
        "fased-app.service",
      ]);
      await removeEnrollmentUnitLink(userUnit);
      execute("/usr/bin/systemctl", ["--user", "daemon-reload"]);
    }
  });
  for (const [folder, base, prefix] of [
    ["fased-app-custody-installer", "/var/lib", `.fased-custody-${uid}-`],
    ["fased-app-vps-installer", "/var/lib", `.fased-app-vps-${uid}-`],
  ])
    await attempt(async () => {
      const intentPath = join(config, folder, "intent.json");
      if (await exists(intentPath)) {
        const j = JSON.parse(await readFile(intentPath, "utf8"));
        assert.match(j.id, /^[a-f0-9]{32}$/);
        fixtureIds.add(j.id);
        const stage = join(base, prefix + j.id + ".pending");
        if (await exists(stage)) osTool("/usr/bin/rm", ["-r", "--", stage]);
      }
    });
  for (const path of [custodyRoot, remoteRoot])
    await attempt(async () => {
      if (await exists(path)) osTool("/usr/bin/rm", ["-r", "--", path]);
    });
  for (const name of userNames.toReversed())
    await attempt(async () => {
      let line;
      try {
        line = execute("/usr/bin/getent", ["passwd", name]);
      } catch {
        return;
      }
      assert.ok(
        [...fixtureIds].some((id) => line.includes(id)),
        "refuse to delete an unbound account",
      );
      osTool("/usr/sbin/userdel", [name]);
    });
  for (const name of journalNames)
    await attempt(async () => {
      if (await exists(join(config, name)))
        await rm(join(config, name), { recursive: true });
    });
  if (source?.mode === "local")
    await attempt(async () => {
      if (await exists(source.data))
        osTool("/usr/bin/rm", ["-r", "--", source.data]);
      await rm(source.base, { recursive: true, force: true });
    });
  await attempt(async () => {
    osTool("/usr/bin/systemctl", ["daemon-reload"]);
  });
  if (failures.length)
    throw new Error("enrollment fixture cleanup: " + failures.join("; "));
}

let source;
try {
  for (const path of [
    custodyRoot,
    remoteRoot,
    ...journalNames.map((n) => join(config, n)),
    join(config, "systemd/user/fased-app.service"),
    ...unitNames.map((n) => "/etc/systemd/system/" + n),
    `/etc/systemd/system/fased-vps-${uid}.service`,
  ])
    check("absent:" + path, !(await exists(path)));
  for (const name of userNames)
    check(
      "absent-user:" + name,
      failed(() => execute("/usr/bin/getent", ["passwd", name])),
    );
  const packageDir = join(evidence, "bundle");
  assert.match(process.env.GITHUB_SHA, /^[0-9a-f]{40}$/);
  const archive = execute("go", [
    "run",
    "-mod=readonly",
    "./cmd/fased-package",
    "--engine",
    compiledEngine,
    "--out",
    packageDir,
    "--source",
    process.env.GITHUB_SHA,
  ]).trim();
  const receipt = JSON.parse(
    await readFile(join(packageDir, "fased-app-linux-amd64.json"), "utf8"),
  );
  assert.equal(receipt.publicRelease, false);
  assert.equal(receipt.manifest.schema, 1);
  assert.equal(receipt.manifest.desktop, false);
  assert.equal(receipt.manifest.unsignedEngineering, true);
  assert.equal(sha(await readFile(archive)), receipt.identity.sha256);
  const unpacked = join(evidence, "unpacked");
  await mkdir(unpacked, { mode: 0o700 });
  execute("/usr/bin/tar", ["-xf", archive, "-C", unpacked]);
  const bundle = join(unpacked, "fased-app-linux-amd64");
  engine = join(bundle, "fased-app");
  assert.equal(sha(await readFile(engine)), report.inputs[compiledEngine]);
  assert.equal(
    sha(await readFile(join(bundle, "bundle.json"))),
    receipt.manifestIdentity.sha256,
  );
  const bundleArgs = [
    "--bundle",
    bundle,
    "--bundle-sha256",
    receipt.manifestIdentity.sha256,
  ];
  report.bundle = {
    sha256: receipt.identity.sha256,
    manifestSHA256: receipt.manifestIdentity.sha256,
    source: process.env.GITHUB_SHA,
    publicRelease: false,
  };
  owns = true;
  let linger = "no";
  try {
    linger = execute("/usr/bin/loginctl", [
      "show-user",
      String(uid),
      "--property=Linger",
      "--value",
    ]).trim();
  } catch {}
  if (linger !== "yes") {
    osTool("/usr/bin/loginctl", ["enable-linger", String(uid)]);
    changedLinger = true;
  }
  if (
    failed(() =>
      execute("/usr/bin/systemctl", [
        "is-active",
        "--quiet",
        `user@${uid}.service`,
      ]),
    )
  ) {
    osTool("/usr/bin/systemctl", ["start", `user@${uid}.service`]);
    startedManager = true;
  }
  browser = await chromium.launch({ headless: true });
  for (const mode of ["local", "remote"]) {
    const listen = await freeListen(),
      url = "http://" + listen;
    const base = join(evidence, mode);
    await mkdir(base, { mode: 0o700 });
    source = {
      mode,
      base,
      prefix: join(base, "app"),
      data: join(base, "data"),
    };
    const paths = ["--prefix", source.prefix, "--data-dir", source.data];
    if (mode === "local")
      cli([
        "install",
        ...paths,
        "--listen",
        listen,
        "--prepare-host",
        "--autostart",
        ...bundleArgs,
      ]);
    else {
      cli([
        "install",
        "--vps",
        "--managed-remote",
        "--access",
        "ssh",
        "--listen",
        listen,
        "--prepare-host",
        "--autostart",
        ...bundleArgs,
      ]);
      source.data = remoteRoot + "/app-state";
    }
    const original = await health(url);
    check(
      `${mode}:ordinarySourceProfile`,
      original.connection.location === mode &&
        (mode === "local"
          ? !original.connection.ownerUid
          : original.connection.ownerUid === uid),
    );
    const identity =
      mode === "local"
        ? { uid, gid }
        : JSON.parse(await readFile(remoteRoot + "/install.json", "utf8"));
    const sourceIdentity =
      mode === "local"
        ? identity
        : { uid: identity.appUid, gid: identity.appGid };
    source.identity = sourceIdentity;
    const claim =
      mode === "local"
        ? JSON.parse(await readFile(source.data + "/claim-code.json", "utf8"))
        : { code: cli(["vps", "claim-code"]).trim() };
    secrets.add(claim.code);
    const { session } = await seedEnrollmentSource({
      url,
      claimCode: claim.code,
      password,
      key,
      vaultPassword,
      rememberSecret: (s) => secrets.add(s),
      log: (s) => {
        log += s + "\n";
      },
      scrub,
    });
    const lifecycle = (action) =>
      mode === "local"
        ? cli(["service", action, ...paths, "--prepare-host"])
        : cli(["vps", action]);
    lifecycle("stop");
    const expected = snapshot(sourceIdentity, source.data, true);
    check(
      `${mode}:seededRecordsCiphertextAndHold`,
      expected.counts.memories > 0 &&
        expected.counts.tasks === 1 &&
        expected.holds.length === 1 &&
        expected.holds[0][2] > 0 &&
        Object.keys(expected.vaults).includes("ai-credentials.json"),
    );
    lifecycle("start");
    await health(url, original.instanceId);
    if (mode === "remote")
      osTool("/usr/bin/systemctl", [
        "disable",
        "--",
        `fased-vps-${uid}.service`,
      ]);
    const enroll = [
      "custody",
      "enroll",
      "--source",
      mode,
      "--expected-instance",
      original.instanceId,
      "--network",
      "devnet",
      "--signer-binary",
      signer,
      "--prepare-host",
      "--disposable-fixture",
      ...(mode === "local" ? paths : []),
    ];
    if (mode === "local")
      await controlledInterruption(enroll, original.instanceId);
    const result = JSON.parse(cli(enroll));
    check(
      `${mode}:enrolledOriginalURL`,
      result.instanceId === original.instanceId &&
        result.url === url &&
        !result.walletSigner &&
        !result.financialExecution,
    );
    const target = JSON.parse(
      await readFile(custodyRoot + "/install.json", "utf8"),
    );
    assert.equal(
      new Set([uid, sourceIdentity.uid, target.app.uid, target.signer.uid])
        .size,
      mode === "local" ? 3 : 4,
    );
    const h = await health(url, original.instanceId);
    check(
      `${mode}:runtimeProfileAndHolds`,
      h.connection.location === mode &&
        h.connection.ownerUid === uid &&
        h.capabilities.walletSigner === false &&
        h.capabilities.financialExecution === false &&
        h.capabilities.updater === false,
    );
    assert.deepEqual(
      snapshot(target.app, custodyRoot + "/app-state"),
      expected,
    );
    const publicStatus = JSON.parse(lifecycle("status"));
    check(
      `${mode}:originalCLIResolvesTarget`,
      publicStatus.active && publicStatus.instanceId === original.instanceId,
    );
    const setup = await api(url, session, "/api/v1/accounts");
    check(
      `${mode}:walletsBoundToIsolatedSigner`,
      setup.setup.instanceId === original.instanceId &&
        setup.setup.network === "devnet" &&
        setup.setup.state === "empty" &&
        setup.fixture.available,
    );
    const pids = unitNames.map((n) =>
      Number(
        execute("/usr/bin/systemctl", [
          "show",
          n,
          "--property=MainPID",
          "--value",
        ]).trim(),
      ),
    );
    for (const [i, role] of [target.app, target.signer].entries()) {
      const status = await readFile(`/proc/${pids[i]}/status`, "utf8");
      check(
        `${mode}:${i}:kernelUIDAndNoNewPrivs`,
        new RegExp(
          `^Uid:\\s+${role.uid}\\s+${role.uid}\\s+${role.uid}\\s+${role.uid}$`,
          "m",
        ).test(status) && /^NoNewPrivs:\s+1$/m.test(status),
      );
    }
    as(target.app, "/usr/bin/python3", [
      "-c",
      "import errno,os,sys\ntry: os.open(sys.argv[1],os.O_RDWR);raise AssertionError('writable installation')\nexcept OSError as e: assert e.errno==errno.EROFS\ntry: os.listdir(sys.argv[2]);raise AssertionError('signer state exposed')\nexcept PermissionError: pass",
      `/proc/${pids[0]}/root${custodyRoot}/app-readonly-probe`,
      custodyRoot + "/signer-state",
    ]);
    check(`${mode}:actualMountAndSignerIsolation`, true);
    await browserProof(url, mode, original.instanceId);
    lifecycle("stop");
    lifecycle("start");
    await health(url, original.instanceId);
    assert.deepEqual(
      snapshot(target.app, custodyRoot + "/app-state"),
      expected,
    );
    check(`${mode}:restartPreservesRecordsCiphertextHolds`, true);
    let enabled;
    try {
      enabled = execute("/usr/bin/systemctl", [
        "is-enabled",
        unitNames[0],
      ]).trim();
    } catch (e) {
      assert.equal(e.status, 1);
      enabled = String(e.stdout).trim();
    }
    check(
      `${mode}:autostartIntentPreserved`,
      enabled === (mode === "local" ? "enabled" : "disabled"),
    );
    report.profiles[mode] = {
      instanceId: original.instanceId,
      location: h.connection.location,
      ownerUid: uid,
      appUid: target.app.uid,
      signerUid: target.signer.uid,
      snapshot: expected,
    };
    await cleanProfile(source);
    source = null;
  }
  report.result = "PASS";
} catch (e) {
  report.error = scrub(e.stack || e.message);
  report.failure = await failureInventory();
  throw e;
} finally {
  const cleanup = [];
  if (child) {
    child.kill("SIGCONT");
    child.kill("SIGKILL");
  }
  if (browser) await browser.close();
  if (owns) {
    if (report.result !== "PASS")
      for (const unit of [...unitNames, `fased-vps-${uid}.service`]) {
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
            osTool("/usr/bin/journalctl", [
              "--no-pager",
              "-n",
              "35",
              "-u",
              unit,
            ]);
        } catch (e) {
          log += "\nservice diagnostic unavailable: " + scrub(e.message);
        }
      }
    try {
      await cleanProfile(source);
    } catch (e) {
      cleanup.push(scrub(e.message));
    }
    if (changedLinger)
      try {
        osTool("/usr/bin/loginctl", ["disable-linger", String(uid)]);
      } catch (e) {
        cleanup.push(scrub(e.message));
      }
    if (startedManager)
      try {
        osTool("/usr/bin/systemctl", ["stop", `user@${uid}.service`]);
      } catch (e) {
        cleanup.push(scrub(e.message));
      }
  }
  for (const folder of ["bundle", "unpacked"])
    try {
      await rm(join(evidence, folder), { recursive: true, force: true });
    } catch (e) {
      cleanup.push(scrub(e.message));
    }
  report.cleanup = cleanup.length
    ? cleanup
    : "PASS: exact declared fixture installations, accounts and state removed";
  if (cleanup.length) report.result = "FAIL";
  await writeFile(
    join(evidence, "enrollment-installed.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  await writeFile(join(evidence, "enrollment-installed.log"), scrub(log));
  if (cleanup.length) throw new Error("enrollment fixture cleanup failed");
}
console.log(
  "PASS: actual Ubuntu Local/Remote same-instance enrollment, UID isolation, restart and preserved records/credentials/holds.",
);
