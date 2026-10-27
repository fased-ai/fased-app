// Called only inside the existing disposable managed Ubuntu identity fixture.
import assert from "node:assert/strict";
import { fixtureDevelopmentEnvelope } from "./fixture-development-envelope.mjs";
import { spawn, execFileSync } from "node:child_process";
import { randomBytes, generateKeyPairSync } from "node:crypto";
import { readFile, writeFile, mkdir, readdir, rm } from "node:fs/promises";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { sha, save } from "./handoff-transport-evidence.mjs";
export const fixtureInspectionDigest = (domain, obj) =>
  sha("fased-update-snapshot:v1:" + domain + "\0" + JSON.stringify(obj));
export const fixtureInspectionPTY = String.raw`import os,pty,select,sys,time,re,json,signal,hashlib
binary,op,ident,tx,inputs,artifacts,choice,boundary=sys.argv[1:]
if op=='provision':args=[binary,'custody','fixture-inspection','provision','--transaction',tx,'--input-dir',inputs,'--artifact-dir',artifacts]
else:args=[binary,'custody','fixture-inspection',op,'--bridge-id',ident]
args+=['--disposable-fixture','--prepare-host']
pid,fd=pty.fork()
if pid==0:os.execv(binary,args)
seen=b'';sent=False;interrupted=False;deadline=time.time()+85;done=None
try:
 while time.time()<deadline:
  ready,_,_=select.select([fd],[],[],.05)
  if ready:
   try:b=os.read(fd,65536)
   except OSError:break
   if not b:break
   seen+=b
   prompt=re.search(rb'Type ((?:BRIDGE FIXTURE|INSPECT FIXTURE) [a-f0-9]{64}):',seen)
   if prompt and not sent:
    os.write(fd,(prompt.group(1) if choice=='approve' else b'CANCEL')+b'\n');sent=True
   if boundary and ('Fixture acceptance stopped: ').encode() in seen:
    stopped=re.search(rb"Fixture acceptance stopped: ([0-9]+) "+boundary.encode()+rb"\r?\n",seen)
    if stopped:
     actual=int(stopped.group(1));until=time.monotonic()+1
     while time.monotonic()<until:
      status=open("/proc/%d/status"%actual).read()
      if "State:\tT" in status:break
      time.sleep(.005)
     assert "State:\tT" in status,status
     assert hashlib.sha256(open("/proc/%d/exe"%actual,"rb").read()).digest()==hashlib.sha256(open(binary,"rb").read()).digest()
     assert os.stat("/proc/%d"%actual).st_uid==os.getuid()
     os.kill(actual,signal.SIGKILL)
     try:os.killpg(pid,signal.SIGKILL)
     except ProcessLookupError:pass
     interrupted=True;break
  p,status=os.waitpid(pid,os.WNOHANG)
  if p:
   done=os.waitstatus_to_exitcode(status)
   # Exit notification can arrive before the final PTY bytes are consumed.
   drain_until=time.monotonic()+1
   while time.monotonic()<drain_until:
    ready,_,_=select.select([fd],[],[],.05)
    if not ready:break
    try:last=os.read(fd,65536)
    except OSError:break
    if not last:break
    seen+=last
   break
finally:
 if done is None:
  if time.time()>=deadline:
   try:os.killpg(pid,signal.SIGKILL)
   except ProcessLookupError:pass
  try:_,status=os.waitpid(pid,0);done=os.waitstatus_to_exitcode(status)
  except ChildProcessError:done=-1
 os.close(fd)
print(json.dumps({'exitCode':done,'interrupted':interrupted,'output':seen.decode(errors='replace')}))`;
export async function fixtureInspectionJourney({
  root,
  env,
  check,
  ownerStatus,
  restartApp,
  restartSigner,
  stopAt = "",
}) {
  assert.ok(
    process.env.GITHUB_ACTIONS === "true" ||
      process.env.FASED_DISPOSABLE_VM === "1",
  );
  assert.ok(process.getuid() > 0);
  const evidence = path.resolve(
      env.FASED_INSPECTION_EVIDENCE_ROOT ?? ".evidence/p4-installed-inspection",
    ),
    work = path.join(evidence, "public-fixture"),
    core = path.resolve(".evidence/bin/fased-app"),
    signer = path.resolve(".evidence/bin/fased-signer");
  await mkdir(work, { recursive: true, mode: 0o700 });
  const run = (bin, args, input) =>
    execFileSync(bin, args, {
      env,
      encoding: "utf8",
      input,
      timeout: 90000,
      maxBuffer: 1 << 20,
    });
  const osTool = (tool, args) => run("/usr/bin/sudo", ["-n", tool, ...args]);
  const report = {
    result: "FAIL",
    fixtureOnly: true,
    realWallets: false,
    automaticUpdates: false,
    checks: {},
    installedVMReboot:
      "BLOCKED: hosted Ubuntu service manager is not a reboot-capable VM",
  };
  const pass = (name, v, details) => {
    if (!v && details)
      console.error(JSON.stringify({ fixtureCheck: name, details }));
    check("inspection:" + name, v);
    report.checks[name] = true;
  };
  const snapshot = () =>
    JSON.parse(
      run(core, [
        "custody",
        "updates",
        "inventory",
        "--disposable-fixture",
        "--prepare-host",
      ]),
    );
  const base = JSON.parse(await readFile(root + "/install.json", "utf8")),
    id = randomBytes(16).toString("hex"),
    before = snapshot(),
    sequence = Number(before.signer.sequenceFloor) + 1;
  report.before = before;
  const target = {
    ...base,
    inspectionBridge: id,
    coreHash: sha(await readFile(core)),
    signerHash: sha(await readFile(signer)),
  };
  const units = {};
  for (const role of ["app", "signer"]) {
    units[role] = await readFile(root + "/fased-" + role + ".service", "utf8");
  }
  const variant = (candidate) =>
    ["app", "signer"].map((role) => {
      let s = units[role];
      const image = candidate
        ? `${root}/fixture-inspection-v1/capsules/${id}/candidate/fased-${role}`
        : `${root}/fased-${role}`;
      s = s.replace(
        /^ExecStart=.*$/m,
        `ExecStart=${image} fixture-reader-guard --owner ${base.owner} --disposable-fixture`,
      );
      if (role === "signer")
        s = s.replace(
          `ReadWritePaths=${root}/signer-state ${root}/signer-ipc`,
          `ReadWritePaths=${root}/signer-state ${root}/signer-ipc ${root}/fixture-inspection-v1/signer/state ${root}/fixture-inspection-v1/signer/anchor`,
        );
      return s;
    });
  const digest = fixtureInspectionDigest;
  const catalog = {
    version: 1,
    profile: "fased-app-installed-inspection-fixture:v1",
    fixtureOnly: true,
    provision: id,
    installation: base.id,
    configSha256: sha(await readFile(root + "/identity.json")),
    rootSha256: sha(root),
    manifestSha256: sha(await readFile(root + "/install.json")),
    beforeImages: [base.coreHash, base.signerHash],
    candidateImages: [target.coreHash, target.signerHash],
    beforeUnits: digest("fixture-unit-pair:v1", [units.app, units.signer]),
    guardedUnits: digest("fixture-unit-pair:v1", variant(true)),
    finalUnits: digest("fixture-unit-pair:v1", variant(false)),
    uids: [base.owner, base.app.uid, base.signer.uid],
    gids: [process.getgid(), base.app.gid, base.signer.gid],
    reader: {
      profile: "fased-app-installed-inspection-fixture:v1",
      capability: "fixture-installed-inspection-v1",
      descriptor: 1,
      store: 1,
      source: 1,
      predecessor: 4,
    },
    roles: ["", ""],
  };
  await save(path.join(work, "export-config.json"), {
    catalog,
    core: before.core,
    signer: before.signer,
  });
  const exporter = spawn(
    env.FASED_INSPECTION_EXPORTER ?? "go",
    env.FASED_INSPECTION_EXPORTER
      ? [
          "-test.run=^TestInstalledInspectionFixtureExport$",
          "-test.timeout=110s",
        ]
      : [
          "test",
          "-count=1",
          "-run",
          "^TestInstalledInspectionFixtureExport$",
          "-timeout",
          "110s",
          "./internal/update",
        ],
    {
      env: { ...env, FASED_INSTALLED_INSPECTION_EXPORT: work },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let exportLog = "";
  exporter.on("error", (error) => {
    exportLog += error.message;
  });
  exporter.stdout.on("data", (b) => {
    exportLog += b;
  });
  exporter.stderr.on("data", (b) => {
    exportLog += b;
  });
  const exportDone = new Promise((resolve) => exporter.once("close", resolve));
  const tty = fixtureInspectionPTY;
  const txpath = path.join(work, "transaction.json"),
    candidate = path.join(work, "candidate"),
    inputDir = path.join(work, "inputs");
  const terminal = (op, choice = "approve", boundary = "") =>
    JSON.parse(
      run("python3", [
        "-c",
        tty,
        core,
        op,
        id,
        txpath,
        inputDir,
        candidate,
        choice,
        boundary,
      ]),
    );
  try {
    for (let n = 0; n < 150; n++) {
      try {
        await readFile(path.join(work, "export-ready"));
        break;
      } catch {
        if (exporter.exitCode !== null)
          throw Error("fixture exporter failed: " + exportLog);
        if (n === 149) throw Error("fixture exporter did not become ready");
        await delay(100);
      }
    }
    const inputs = {};
    for (const n of await readdir(inputDir)) {
      if (n === "bundle") {
        for (const f of await readdir(path.join(inputDir, n)))
          inputs[n + "/" + f] = sha(await readFile(path.join(inputDir, n, f)));
      } else inputs[n] = sha(await readFile(path.join(inputDir, n)));
    }
    const { publicKey, privateKey } = generateKeyPairSync("ed25519"),
      pub = publicKey.export({ type: "spki", format: "der" }).subarray(-32),
      keyId = sha(pub),
      now = Math.floor(Date.now() / 1000);
    const trust = {
      version: 1,
      purpose: "fased-app-update-development:v1",
      keys: [
        {
          id: keyId,
          publicKey: pub.toString("base64"),
          notBefore: now - 60,
          notAfter: now + 3600,
        },
      ],
    };
    const components = [];
    for (const role of ["app", "signer"]) {
      const file = "fased-" + role,
        bytes = await readFile(root + "/" + file);
      const contract = JSON.parse(
        run(
          root + "/" + file,
          role === "app"
            ? ["updates", "compatibility"]
            : ["fixture-compatibility"],
        ),
      );
      components.push({
        version: "0.0.0-disposable",
        artifact: { file, size: bytes.length, sha256: sha(bytes) },
        contract: role === "app" ? contract.contract : contract,
      });
    }
    const lifetime = Number(env.FASED_FIXTURE_MAINTENANCE_TTL ?? 600);
    assert.ok(
      Number.isSafeInteger(lifetime) && lifetime >= 30 && lifetime <= 600,
    );
    const maintenance = {
      version: 1,
      product: "fased-app",
      channel: "development",
      sequence: String(sequence),
      issuedAt: now - 1,
      expiresAt: now + lifetime,
      target: { os: "linux", arch: "amd64" },
      components,
    };
    const envelope = fixtureDevelopmentEnvelope(maintenance, keyId, privateKey);
    const tx = {
      tlsCa: (await readFile(path.join(work, "fixture-ca.pem"))).toString(
        "base64",
      ),
      version: 1,
      profile: "fased-app-fixture-reader-bridge:v1",
      id,
      base,
      target,
      catalogSha256: inputs["catalog.json"],
      inputs,
      maintenance: Buffer.from(envelope).toString("base64"),
      trust: Buffer.from(JSON.stringify(trust)).toString("base64"),
      reviewedAt: now,
      core: before.core,
      signer: before.signer,
    };
    await save(txpath, tx);
    await save(path.join(evidence, "public-capsule.json"), tx);
    await mkdir(candidate, { mode: 0o700 });
    for (const role of ["app", "signer"])
      await writeFile(
        path.join(candidate, "fased-" + role),
        await readFile(role === "app" ? core : signer),
        { mode: 0o700 },
      );
    const cancelled = terminal("provision", "cancel");
    await save(path.join(evidence, "review-cancellation.json"), cancelled);
    pass(
      "protectedCancellationLeavesNoProvision",
      cancelled.exitCode !== 0 &&
        cancelled.output.includes("BRIDGE FIXTURE ") &&
        cancelled.output.includes("bridge review cancelled") &&
        (await readFile(root + "/install.json", "utf8").then(
          (s) => !s.includes("inspectionBridge"),
        )),
      cancelled,
    );
    if (stopAt) env.FASED_TEST_BRIDGE_STOP = stopAt;
    const result = terminal("provision", "approve", stopAt);
    delete env.FASED_TEST_BRIDGE_STOP;
    await save(path.join(evidence, "bridge-terminal.json"), result);
    if (stopAt) {
      pass(
        "actualControllerStoppedAtExactCut",
        result.interrupted && result.exitCode !== 0,
      );
      report.cut = stopAt;
      report.bridgeId = id;
      report.result = "CUT";
      return report;
    }
    pass("guardedBridgeCompletes", result.exitCode === 0);
    const after = snapshot();
    report.after = after;
    pass(
      "nonzeroHistoryAndFloorsPreserved",
      before.signer.installedSequence !== "0" &&
        before.signer.installedSequence === after.signer.installedSequence &&
        Number(after.signer.sequenceFloor) === sequence &&
        after.signer.authoritySha256 === before.signer.authoritySha256 &&
        after.signer.spent === before.signer.spent &&
        after.signer.held === before.signer.held &&
        after.signer.unknown === before.signer.unknown &&
        after.signer.activeIntentId === "" &&
        ownerStatus().locked,
    );
    const inspect = terminal("inspect");
    await save(path.join(evidence, "inspection-terminal.json"), inspect);
    pass(
      "realOwnerReviewAndIndependentOfflineSigner",
      inspect.exitCode === 0 &&
        inspect.output.includes('"financialExecution":false') &&
        inspect.output.includes('"updaterEnabled":false') &&
        inspect.output.includes('"exchange":'),
    );
    pass(
      "inspectionCancellationDeniesCompletion",
      ((cancel) =>
        cancel.exitCode !== 0 &&
        cancel.output.includes("INSPECT FIXTURE ") &&
        cancel.output.includes("inspection cancelled"))(
        terminal("inspect", "cancel"),
      ),
    );
    const history = JSON.parse(
      run(core, [
        "custody",
        "fixture-inspection",
        "status",
        "--disposable-fixture",
        "--prepare-host",
      ]),
    );
    pass(
      "freshStatusIsHistoricalWithoutAuthority",
      history.historical &&
        history.fixtureOnly &&
        history.exchange === "" &&
        !history.installAllowed &&
        !history.updaterEnabled,
    );
    pass("completedBridgeRetryHistorical", terminal("bridge").exitCode === 0);
    pass("postFenceRollbackRefused", terminal("rollback").exitCode !== 0);
    const unit = osTool("/usr/bin/systemctl", [
      "show",
      `fased-custody-${base.owner}-signer.service`,
      "--property=RestrictAddressFamilies,NoNewPrivileges,ProtectSystem,ReadWritePaths",
    ]);
    pass(
      "actualUnixOnlySignerSandbox",
      unit.includes("AF_UNIX") &&
        !unit.includes("AF_INET") &&
        unit.includes("NoNewPrivileges=yes") &&
        unit.includes("ProtectSystem=strict") &&
        unit.includes("/fixture-inspection-v1/signer/anchor"),
    );
    const as = (identity, bin, args) =>
      run("/usr/bin/sudo", [
        "-n",
        "/usr/bin/setpriv",
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
        bin,
        ...args,
      ]);
    let denied = false;
    try {
      as(base.app, core, [
        "owner-fixture-inspection",
        "--owner",
        String(base.owner),
        "--disposable-fixture",
        "--prepare-host",
      ]);
    } catch {
      denied = true;
    }
    pass("appDeniedProtectedOwnerHelper", denied);
    denied = false;
    try {
      as(base.app, "/usr/bin/cat", [
        root + "/fixture-inspection-v1/signer/state/head.json",
      ]);
    } catch {
      denied = true;
    }
    pass("appDeniedSignerPrivateCheckpoint", denied);
    restartApp();
    restartSigner();
    await delay(400);
    pass(
      "managerRestartKeepsNewGuardedReaders",
      terminal("bridge").exitCode === 0 && terminal("inspect").exitCode === 0,
    );
    report.result = "PASS";
  } finally {
    await writeFile(path.join(work, "export-done"), "done");
    const code = await exportDone;
    await writeFile(path.join(evidence, "export.log"), exportLog);
    if (code !== 0 && report.result === "PASS") report.result = "FAIL";
    await save(path.join(evidence, "installed-result.json"), report);
    await rm(work, { recursive: true, force: true });
  }
  assert.equal(report.result, "PASS");
  return report;
}
