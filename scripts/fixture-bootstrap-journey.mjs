// Called only inside the disposable managed Ubuntu three-UID fixture.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  generateKeyPairSync,
  createHash,
  randomBytes,
  sign,
} from "node:crypto";
import { readFile, writeFile, mkdir, rm } from "node:fs/promises";
import path from "node:path";
import { setTimeout as delay } from "node:timers/promises";

export async function fixtureBootstrapJourney({
  root,
  env,
  backups,
  check,
  fails,
  ownerStatus,
  restartApp,
  restartSigner,
}) {
  const evidence = path.resolve(".evidence/p4-bootstrap");
  await mkdir(evidence, { recursive: true });
  const core = path.resolve(".evidence/bin/fased-app"),
    signer = path.resolve(".evidence/bin/fased-signer"),
    bad = path.join(evidence, "fased-app-bad-health");
  const bp = path.join(backups, "bootstrap.json"),
    tp = path.join(backups, "bootstrap-trust.json"),
    mp = path.join(backups, "normal-update.json");
  const candidate = path.join(backups, "bootstrap-candidate");
  await mkdir(candidate, { mode: 0o700 });
  const checks = {},
    report = {
      result: "FAIL",
      class:
        "actual Ubuntu lifecycle-3 predecessor, three UIDs, PTY/systemd reader bootstrap",
      fixtureOnly: true,
      checks,
    };
  const pass = (name, value) => {
    check("bootstrap:" + name, value);
    checks[name] = true;
  };
  const run = (binary, args, input) =>
    execFileSync(binary, args, {
      encoding: "utf8",
      env,
      input,
      timeout: 60000,
    });
  const inventory = () =>
    JSON.parse(
      run(core, [
        "custody",
        "updates",
        "inventory",
        "--disposable-fixture",
        "--prepare-host",
      ]),
    );
  const sha = (b) => createHash("sha256").update(b).digest("hex");
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const pub = publicKey.export({ type: "spki", format: "der" }).subarray(-32),
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
  const envelope = (m) => {
    const payload = JSON.stringify(m);
    return {
      version: 1,
      keyId,
      payload: m,
      signature: sign(
        null,
        Buffer.concat([
          Buffer.from("fased-app-update-development:v1\0"),
          Buffer.from(payload),
        ]),
        privateKey,
      ).toString("base64"),
    };
  };
  const contracts = (app, key) => [
    JSON.parse(run(app, ["updates", "compatibility"])).contract,
    JSON.parse(run(key, ["fixture-compatibility"])),
  ];
  const manifest = async (seq, pair, cs) => ({
    version: 1,
    product: "fased-app",
    channel: "development",
    sequence: String(seq),
    issuedAt: now - 1,
    expiresAt: now + 600,
    target: { os: "linux", arch: "amd64" },
    components: await Promise.all(
      cs.map(async (c, n) => {
        const b = await readFile(pair[n]);
        return {
          version: "0.0.0-disposable",
          artifact: {
            file: n === 0 ? "fased-app" : "fased-signer",
            size: b.length,
            sha256: sha(b),
          },
          contract: c,
        };
      }),
    ),
  });
  const bundle = async (seq, targetCore) => {
    const a = await manifest(
      seq,
      [root + "/fased-app", root + "/fased-signer"],
      contracts(root + "/fased-app", root + "/fased-signer"),
    );
    const z = await manifest(
      seq,
      [targetCore, signer],
      contracts(targetCore, signer),
    );
    z.bootstrapFrom = sha(Buffer.from(JSON.stringify(a)));
    await writeFile(
      bp,
      JSON.stringify({
        version: 1,
        maintenance: Buffer.from(JSON.stringify(envelope(a))).toString(
          "base64",
        ),
        target: Buffer.from(JSON.stringify(envelope(z))).toString("base64"),
      }),
      { mode: 0o600 },
    );
    await writeFile(tp, JSON.stringify(trust), { mode: 0o600 });
    await writeFile(
      path.join(candidate, "fased-app"),
      await readFile(targetCore),
      { mode: 0o700 },
    );
    await writeFile(
      path.join(candidate, "fased-signer"),
      await readFile(signer),
      { mode: 0o700 },
    );
  };
  const tty = String.raw`
import os,pty,select,sys,time,re,signal,json,subprocess
assert os.getuid()>0
binary,operation,choice,ident,bundle,trust,artifacts,boundary=sys.argv[1:]
if operation=='bootstrap':args=[binary,'custody','bootstrap','--bundle',bundle,'--trust-file',trust,'--artifact-dir',artifacts]
else:
 args=[binary,'custody','updates',operation]
 if operation=='prepare':args+=['--manifest',bundle,'--trust-file',trust,'--artifact-dir',artifacts]
 if operation=='install':args+=['--artifact-dir',artifacts]
args+=['--disposable-fixture','--prepare-host','--intent-id',ident]
pid,fd=pty.fork()
if pid==0:os.execv(binary,args)
seen=b'';done=0;sent=choice=='resume';interrupted=False;deadline=time.time()+65
try:
 while time.time()<deadline:
  ready,_,_=select.select([fd],[],[],.05)
  if ready:
   try:b=os.read(fd,65536)
   except OSError:break
   if not b:break
   seen+=b
  match=re.search(rb'Type ((?:BOOTSTRAP|PREPARE|INSTALL|COMPLETE) [0-9a-f]{64})',seen)
  if not sent and match:
   competitor=subprocess.run([binary,'custody','bootstrap','--disposable-fixture','--prepare-host','--intent-id',ident,'--bundle',bundle,'--trust-file',trust,'--artifact-dir',artifacts],capture_output=True,text=True,timeout=15) if operation=='bootstrap' else None
   if competitor:assert competitor.returncode!=0 and 'already owns' in competitor.stderr,'owner lock did not exclude competitor'
   os.write(fd,(b'CANCEL' if choice=='cancel' else match[1])+b'\n');sent=True
  if sent and boundary and ('Bootstrap boundary: '+boundary).encode() in seen:
   os.killpg(pid,signal.SIGKILL);interrupted=True
  done,status=os.waitpid(pid,os.WNOHANG)
  if done:break
 else:raise Exception('bootstrap owner terminal timeout')
 if not done:done,status=os.waitpid(pid,0)
 code=os.waitstatus_to_exitcode(status)
 assert sent,'protected review absent: '+seen.decode(errors='replace')
 assert (code==0)==(choice!='cancel' and not boundary),'unexpected exit: '+seen.decode(errors='replace')
 print(json.dumps({'exitCode':code,'interrupted':interrupted,'reviewed':sent}))
finally:
 os.close(fd)
 if not done:os.killpg(pid,signal.SIGKILL);os.waitpid(pid,0)
`;
  const terminal = (binary, operation, choice, id, boundary = "", file = bp) =>
    JSON.parse(
      run("python3", [
        "-c",
        tty,
        binary,
        operation,
        choice,
        id,
        file,
        tp,
        candidate,
        boundary,
      ]),
    );
  const current = (id) =>
    JSON.parse(
      run(core, [
        "custody",
        "bootstrap",
        "--disposable-fixture",
        "--prepare-host",
        "--intent-id",
        id,
      ]),
    );
  try {
    const before = inventory().signer;
    report.before = before;
    pass(
      "genuinePredecessorLifecycle3",
      contracts(root + "/fased-app", root + "/fased-signer")[0].lifecycle
        .entrypoint === 3,
    );
    let id = randomBytes(16).toString("hex");
    await bundle(1, bad);
    pass(
      "protectedCancelDoesNotReserveFloor",
      terminal(bad, "bootstrap", "cancel", id).exitCode !== 0 &&
        inventory().signer.sequenceFloor === "0",
    );
    const damaged = JSON.parse(await readFile(bp, "utf8"));
    damaged.target = damaged.maintenance;
    const original = await readFile(bp);
    await writeFile(bp, JSON.stringify(damaged));
    pass(
      "unboundSignedTargetDeniedBeforeMaintenance",
      fails(() =>
        run(bad, [
          "custody",
          "bootstrap",
          "--disposable-fixture",
          "--prepare-host",
          "--intent-id",
          id,
          "--bundle",
          bp,
          "--trust-file",
          tp,
          "--artifact-dir",
          candidate,
        ]),
      ) && inventory().signer.sequenceFloor === "0",
    );
    await writeFile(bp, original);
    pass(
      "actualFailedHealthRollsBackAndAbortsMaintenance",
      terminal(bad, "bootstrap", "approve", id).exitCode === 0 &&
        contracts(root + "/fased-app", root + "/fased-signer")[0].lifecycle
          .entrypoint === 3 &&
        inventory().signer.sequenceFloor === "1" &&
        ownerStatus().locked &&
        !ownerStatus().updateHeld,
    );
    id = randomBytes(16).toString("hex");
    await bundle(2, core);
    pass(
      "actualDeathAfterReaderCoreReplacement",
      terminal(core, "bootstrap", "approve", id, "core-replaced").interrupted,
    );
    const installedManifest = JSON.parse(
      await readFile(root + "/install.json", "utf8"),
    );
    pass(
      "appCannotUseColdInventoryProbe",
      fails(
        () =>
          execFileSync(
            "/usr/bin/sudo",
            [
              "-n",
              "/usr/bin/setpriv",
              "--reuid",
              String(installedManifest.app.uid),
              "--regid",
              String(installedManifest.app.gid),
              "--clear-groups",
              "--no-new-privs",
              "--inh-caps=-all",
              "--ambient-caps=-all",
              "--bounding-set=-all",
              "--",
              root + "/bootstraps/" + id + "/candidate/fased-signer",
              "fixture-update-inspect",
              "--config",
              root + "/identity.json",
              "--disposable-fixture",
              "--intent-id",
              id,
            ],
            { encoding: "utf8", env },
          ),
        /configured non-root signer/,
      ),
    );
    await rm(bp);
    await rm(tp);
    pass(
      "actualColdResumeDiesAfterMatchingCoreRelease",
      terminal(core, "bootstrap", "resume", id, "released").interrupted,
    );
    const completed = current(id);
    report.completed = completed;
    pass(
      "readerHandoffCompletesWithoutInstalledRelease",
      completed.phase === "reader-upgraded" &&
        !completed.installedSequenceCommitted &&
        !completed.resumeAllowed &&
        !completed.financialExecution,
    );
    const after = inventory().signer;
    report.after = after;
    pass(
      "readerUpgradePreservesFinancialHistoryAndReservedFloor",
      after.sequenceFloor === "2" &&
        !after.installedSequence &&
        after.activeIntentId === "" &&
        after.authoritySha256 === before.authoritySha256 &&
        after.spent === before.spent &&
        after.held === before.held &&
        after.unknown === before.unknown &&
        after.recoveryHold === before.recoveryHold &&
        ownerStatus().locked,
    );
    const head = after.states.find((s) => s.name === "operations").anchor;
    pass(
      "exactCurrentRetryDoesNotWriteSignerJournal",
      current(id).alreadyCurrent &&
        inventory().signer.states.find((s) => s.name === "operations")
          .anchor === head,
    );
    restartApp();
    restartSigner();
    let restarted;
    for (let n = 0; n < 30; n++) {
      try {
        restarted = current(id);
        break;
      } catch (error) {
        if (n === 29) throw error;
        await delay(100);
      }
    }
    pass(
      "currentRetrySurvivesServiceRestarts",
      restarted.alreadyCurrent && ownerStatus().locked,
    );
    const rootIntent = JSON.parse(
        await readFile(root + "/intent.json", "utf8"),
      ),
      ownerIntent = JSON.parse(
        await readFile(
          path.join(
            env.XDG_CONFIG_HOME,
            "fased-app-custody-installer/intent.json",
          ),
          "utf8",
        ),
      );
    pass(
      "bootstrapInstallerRecordsConverged",
      JSON.stringify(rootIntent) === JSON.stringify(ownerIntent) &&
        rootIntent.coreHash === sha(await readFile(core)),
    );
    // The replacement supports an ordinary full update and sequence commit.
    const oldBootstrapId = id;
    id = randomBytes(16).toString("hex");
    for (const file of ["fased-app", "fased-signer"])
      await writeFile(
        path.join(candidate, file),
        Buffer.concat([
          await readFile(file === "fased-app" ? core : signer),
          Buffer.from("\nAfter bootstrap candidate\n"),
        ]),
        { mode: 0o700 },
      );
    const normal = await manifest(
      3,
      [path.join(candidate, "fased-app"), path.join(candidate, "fased-signer")],
      contracts(core, signer),
    );
    await writeFile(mp, JSON.stringify(envelope(normal)), { mode: 0o600 });
    await writeFile(tp, JSON.stringify(trust), { mode: 0o600 });
    for (const operation of ["prepare", "install", "complete"]) {
      if (operation === "install")
        run(core, [
          "custody",
          "updates",
          "stage",
          "--disposable-fixture",
          "--prepare-host",
          "--intent-id",
          id,
          "--artifact-dir",
          candidate,
        ]);
      pass(
        "postBootstrapProtected" + operation,
        terminal(core, operation, "approve", id, "", mp).exitCode === 0,
      );
    }
    const final = inventory().signer;
    report.final = final;
    pass(
      "ordinaryUpdateCommitsAfterReaderBridge",
      final.installedSequence === "3" &&
        final.sequenceFloor === "3" &&
        final.activeIntentId === "" &&
        ownerStatus().locked &&
        final.spent === before.spent &&
        final.held === before.held &&
        final.unknown === before.unknown,
    );
    pass(
      "supersededBootstrapCannotReplay",
      fails(() => current(oldBootstrapId)),
    );
    report.result = "PASS";
  } catch (error) {
    report.error = error.message;
    throw error;
  } finally {
    for (const file of [bp, tp, mp]) await rm(file, { force: true });
    await rm(candidate, { recursive: true, force: true });
    report.cleanup =
      "generated public documents/candidates removed; signing key stayed in memory; managed parent removes keys, accounts and services";
    await writeFile(
      path.join(evidence, "managed-bootstrap-result.json"),
      JSON.stringify(report, null, 2) + "\n",
    );
  }
}
