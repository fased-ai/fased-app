// Called only inside managed disposable Ubuntu acceptance, as its non-root
// owner. Existing callbacks run fixed trusted OS tools for a service restart.
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

export async function fixtureUpdateIntentJourney({
  engine,
  root,
  cli,
  ownerStatus,
  ownerUnlock,
  walletPassword,
  restartSigner,
  restartApp,
  backups,
  check,
  fails,
  env,
}) {
  assert(process.getuid() > 0);
  const evidence = path.resolve(".evidence/p4-install");
  await mkdir(evidence, { recursive: true });
  const checks = {};
  const pass = (name, value) => {
    check("updates:" + name, value);
    checks[name] = true;
  };
  const report = {
    result: "FAIL",
    class:
      "actual managed Ubuntu owner/app/signer and PTY; no installation update",
    fixtureOnly: true,
    financialExecution: false,
    checks,
  };
  const sha = (b) => createHash("sha256").update(b).digest("hex");
  const mp = path.join(backups, "development-update-manifest.json"),
    tp = path.join(backups, "development-update-trust.json");
  let id = randomBytes(16).toString("hex");
  const inventory = () =>
    JSON.parse(cli(["updates", "inventory", "--prepare-host"]));
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const pub = publicKey.export({ type: "spki", format: "der" }).subarray(-32),
    keyId = sha(pub),
    now = Math.floor(Date.now() / 1000);
  const candidates = path.join(backups, "controller-candidates");
  await mkdir(candidates, { mode: 0o700 });
  for (const file of ["fased-app", "fased-signer"]) {
    await writeFile(
      path.join(candidates, file),
      Buffer.concat([
        await readFile(root + "/" + file),
        Buffer.from("\nFased disposable candidate marker\n"),
      ]),
      { mode: 0o700 },
    );
  }
  const contracts = [
    JSON.parse(
      execFileSync(root + "/fased-app", ["updates", "compatibility"], {
        encoding: "utf8",
        env,
      }),
    ).contract,
    JSON.parse(
      execFileSync(root + "/fased-signer", ["fixture-compatibility"], {
        encoding: "utf8",
        env,
      }),
    ),
  ];
  const manifest = {
    version: 1,
    product: "fased-app",
    channel: "development",
    sequence: "1",
    issuedAt: now - 1,
    expiresAt: now + 600,
    target: { os: "linux", arch: "amd64" },
    components: [],
  };
  for (const c of contracts) {
    const file = "fased-" + (c.component === "core" ? "app" : "signer"),
      b = await readFile(path.join(candidates, file));
    manifest.components.push({
      version: "0.0.0-development.1",
      artifact: { file, size: b.length, sha256: sha(b) },
      contract: c,
    });
  }
  const saveManifest = async (seq) => {
    manifest.sequence = String(seq);
    const payload = JSON.stringify(manifest);
    await writeFile(
      mp,
      JSON.stringify({
        version: 1,
        keyId,
        payload: manifest,
        signature: sign(
          null,
          Buffer.concat([
            Buffer.from("fased-app-update-development:v1\0"),
            Buffer.from(payload),
          ]),
          privateKey,
        ).toString("base64"),
      }),
      { mode: 0o600 },
    );
  };
  const tty = String.raw`
import os,pty,select,sys,time,re,signal,json,subprocess
assert os.geteuid()!=0
binary,operation,choice,ident,manifest,trust,artifacts=sys.argv[1:]
args=[binary,'custody','updates',operation,'--disposable-fixture','--prepare-host','--intent-id',ident]
if operation=='prepare':args+=['--manifest',manifest,'--trust-file',trust,'--artifact-dir',artifacts]
if operation=='install':args+=['--artifact-dir',artifacts]
pid,fd=pty.fork()
if pid==0:os.execv(binary,args)
seen=b'';sent=False;done=0;interrupted=False;concurrentDenied=False;deadline=time.time()+(60 if operation in ('install','rollback') else 30)
try:
 while time.time()<deadline:
  ready,_,_=select.select([fd],[],[],.05)
  if ready:
   try:chunk=os.read(fd,16384)
   except OSError:break
   if not chunk:break
   seen+=chunk
  if not sent:
   patterns={'prepare':rb'Type (PREPARE [0-9a-f]{64})','abort':rb'Type (ABORT [0-9a-f]{32} [0-9a-f]{64})','install':rb'Type (INSTALL [0-9a-f]{64})','rollback':rb'Type (ROLLBACK [0-9a-f]{64})'}
   pattern=patterns[operation]
   match=re.search(pattern,seen)
   if match:
    if operation=='install':
     competitor=subprocess.run([binary,'custody','updates','install-status','--disposable-fixture','--prepare-host','--intent-id',ident],capture_output=True,text=True,timeout=15)
     concurrentDenied=competitor.returncode!=0 and 'already owns' in competitor.stderr
     assert concurrentDenied,'competing owner did not fail closed: '+competitor.stderr
    os.write(fd,(match[1] if choice in ('approve','interrupt','denied') else b'CANCEL')+b'\n');sent=True
  if sent and choice=='interrupt' and not interrupted and (b'Installation boundary: core-replaced' if operation=='install' else b'Installation boundary: rollback-core-replaced') in seen:
   os.killpg(pid,signal.SIGKILL);interrupted=True
  done,status=os.waitpid(pid,os.WNOHANG)
  if done:break
 else:raise Exception('owner update terminal timeout')
 if not done:done,status=os.waitpid(pid,0)
 code=os.waitstatus_to_exitcode(status)
 assert sent,'no protected terminal review: '+seen.decode(errors='replace')
 assert (code==0)==(choice=='approve'),'unexpected exit: '+seen.decode(errors='replace')
 if operation=='prepare':assert b'No installation or resume is authorized' in seen and b'"sha256"' in seen and b'"spent"' in seen and b'"held"' in seen,'incomplete review'
 print(json.dumps({'terminal':True,'operation':operation,'choice':choice,'exitCode':code,'completeReview':True,'interrupted':interrupted,'concurrentDenied':concurrentDenied}))
finally:
 os.close(fd)
 if not done:os.killpg(pid,signal.SIGKILL);os.waitpid(pid,0)
`;
  const terminal = (operation, choice) =>
    JSON.parse(
      execFileSync(
        "/usr/bin/python3",
        ["-c", tty, engine, operation, choice, id, mp, tp, candidates],
        { env, encoding: "utf8", timeout: 70000 },
      ),
    );
  try {
    await writeFile(
      tp,
      JSON.stringify({
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
      }),
      { mode: 0o600 },
    );
    await saveManifest(1);
    const before = inventory();
    report.initial = before;
    const baselineCoreHash = sha(await readFile(root + "/fased-app"));
    pass(
      "serviceInventoryHasActualCoreAndSigner",
      before.core.state.version === 3 &&
        before.signer.sequenceFloor === "0" &&
        before.core.probeId === before.signer.probeId &&
        before.installAllowed === false &&
        before.financialExecution === false,
    );
    pass("actualOwnerTTYCancel", terminal("prepare", "cancel").exitCode !== 0);
    pass(
      "cancelDoesNotReserveSequence",
      inventory().signer.sequenceFloor === "0",
    );
    pass(
      "actualOwnerTTYPrepare",
      terminal("prepare", "approve").exitCode === 0,
    );
    let current = inventory();
    pass(
      "signerOwnsActiveIntentAndFloor",
      current.signer.activeIntentId === id &&
        current.signer.sequenceFloor === "1" &&
        ownerStatus().locked &&
        ownerStatus().updateHeld,
    );
    pass(
      "preparePreservesAuthoritySpentAndHeld",
      current.signer.authoritySha256 === before.signer.authoritySha256 &&
        current.signer.spent === before.signer.spent &&
        current.signer.held === before.signer.held &&
        current.signer.pending === before.signer.pending &&
        current.signer.recoveryHold === before.signer.recoveryHold,
    );
    pass(
      "unlockDeniedWhileIntentActive",
      fails(() => ownerUnlock(walletPassword)),
    );
    const stage = () =>
      JSON.parse(
        cli([
          "updates",
          "stage",
          "--prepare-host",
          "--intent-id",
          id,
          "--artifact-dir",
          candidates,
        ]),
      );
    const staged = stage();
    pass(
      "actualOwnerStagesCompleteArtifactBytes",
      staged.verified &&
        staged.state === "staged" &&
        staged.files.every((f) => f.bytes === f.artifact.size),
    );
    pass(
      "stagingCannotInstallResumeOrExecute",
      !staged.installAllowed &&
        !staged.resumeAllowed &&
        !staged.financialExecution &&
        ownerStatus().locked &&
        ownerStatus().updateHeld,
    );
    report.staged = staged;
    await saveManifest(2);
    pass(
      "competingOwnerIntentDenied",
      fails(
        () =>
          cli([
            "updates",
            "prepare",
            "--prepare-host",
            "--intent-id",
            randomBytes(16).toString("hex"),
            "--manifest",
            mp,
            "--trust-file",
            tp,
            "--artifact-dir",
            root,
          ]),
        /exclusive slot/,
      ),
    );
    const boot = current.signer.bootId;
    restartSigner();
    for (let n = 0; n < 20; n++) {
      try {
        current = inventory();
        break;
      } catch (error) {
        if (n === 19) throw error;
        await delay(100);
      }
    }
    pass(
      "actualSignerRestartRetainsIntent",
      current.signer.bootId !== boot &&
        current.signer.activeIntentId === id &&
        current.signer.sequenceFloor === "1" &&
        ownerStatus().locked,
    );
    const recovered = stage();
    pass(
      "stagedReceiptAndBytesSurviveSignerRestart",
      recovered.verified &&
        JSON.stringify(recovered.files) === JSON.stringify(staged.files) &&
        recovered.sequence === "1",
    );
    const preflight = () =>
      JSON.parse(
        cli(["updates", "preflight", "--prepare-host", "--intent-id", id]),
      );
    const ready = preflight();
    pass(
      "actualOwnerPreflightPausesCore",
      ready.verified &&
        ready.pause.state === "paused" &&
        ready.pause.request.intentId === id &&
        ready.signer.sequenceFloor === "1",
    );
    pass(
      "preflightCannotInstallResumeOrUnlock",
      !ready.installAllowed &&
        !ready.resumeAllowed &&
        !ready.financialExecution &&
        ownerStatus().locked &&
        ownerStatus().updateHeld,
    );
    const backup = () =>
      JSON.parse(
        cli(["updates", "backup", "--prepare-host", "--intent-id", id]),
      );
    const beforeBackup = inventory().signer;
    const index = backup();
    report.recoveryIndex = index;
    pass(
      "actualOwnerPublishesBoundMetadataBackup",
      index.version === 1 &&
        index.phase === "metadata-backed-up" &&
        index.binding.pause.pauseId === ready.pause.pauseId &&
        index.binding.signer.activeIntentId === id &&
        index.binding.signer.sequenceFloor === "1" &&
        JSON.stringify(index.binding.files) === JSON.stringify(staged.files) &&
        index.snapshot.file === "metadata.sqlite" &&
        index.snapshot.size > 0 &&
        /^[0-9a-f]{64}$/.test(index.snapshot.sha256),
    );
    pass(
      "backupDoesNotInstallResumeOrUnlock",
      !index.installAllowed &&
        !index.resumeAllowed &&
        !index.financialExecution &&
        ownerStatus().locked &&
        ownerStatus().updateHeld,
    );
    pass(
      "ownerCannotReadPrivateAppSnapshotDirectly",
      fails(() =>
        execFileSync(
          "/usr/bin/test",
          ["-r", root + "/app-state/lifecycle/" + id + "/metadata.sqlite"],
          { env, stdio: "ignore" },
        ),
      ),
    );
    pass(
      "exactBackupRetryKeepsOneIndex",
      JSON.stringify(backup()) === JSON.stringify(index),
    );
    const afterBackup = inventory().signer;
    pass(
      "backupLeavesSignerJournalsAndCountersUnchanged",
      JSON.stringify(beforeBackup.states) ===
        JSON.stringify(afterBackup.states) &&
        beforeBackup.spent === afterBackup.spent &&
        beforeBackup.held === afterBackup.held &&
        beforeBackup.unknown === afterBackup.unknown &&
        beforeBackup.authoritySha256 === afterBackup.authoritySha256 &&
        afterBackup.sequenceFloor === "1",
    );
    const app = (args, input) =>
      execFileSync(
        engine,
        [
          "custody",
          "accounts",
          ...args,
          "--disposable-fixture",
          "--prepare-host",
        ],
        { encoding: "utf8", input, env, timeout: 15000 },
      );
    const record = JSON.parse(app([])).record;
    pass(
      "actualPausedAccountMutationDenied",
      fails(
        () => app(["--request-stdin"], JSON.stringify(record)),
        /core metadata paused/,
      ),
    );
    restartApp();
    let next;
    for (let n = 0; n < 20; n++) {
      try {
        next = preflight();
        break;
      } catch (e) {
        if (n === 19) throw e;
        await delay(100);
      }
    }
    pass(
      "actualCoreServiceRestartRetainsPause",
      next.pause.pauseId === ready.pause.pauseId &&
        next.pause.core.bootId !== ready.pause.core.bootId &&
        next.pause.core.state.anchor === ready.pause.core.state.anchor &&
        next.signer.authoritySha256 === ready.signer.authoritySha256,
    );
    report.preflight = next;
    pass(
      "actualCoreRestartRecoversSameBackupIndex",
      JSON.stringify(backup()) === JSON.stringify(index),
    );
    restartSigner();
    let afterSignerRestart;
    for (let n = 0; n < 20; n++) {
      try {
        afterSignerRestart = backup();
        break;
      } catch (e) {
        if (n === 19) throw e;
        await delay(100);
      }
    }
    pass(
      "actualSignerRestartRevalidatesSameBackupIndex",
      JSON.stringify(afterSignerRestart) === JSON.stringify(index) &&
        ownerStatus().locked,
    );

    pass(
      "actualInstallReviewCancel",
      terminal("install", "cancel").exitCode !== 0,
    );
    pass(
      "actualControllerProcessKilledAfterCoreReplacement",
      terminal("install", "interrupt").interrupted === true,
    );
    const installed = JSON.parse(
      cli(["updates", "install", "--prepare-host", "--intent-id", id]),
    );
    report.installation = installed;
    pass(
      "actualMixedPairColdResumeReachesHealthyHeld",
      installed.phase === "healthy-held" &&
        !installed.installedSequenceCommitted &&
        !installed.resumeAllowed &&
        !installed.financialExecution,
    );
    const currentManifest = JSON.parse(
      execFileSync(engine, ["custody", "status", "--disposable-fixture"], {
        encoding: "utf8",
        env,
      }),
    ).manifest;
    pass(
      "actualInstalledPairMatchesSignedCandidate",
      currentManifest.coreHash === manifest.components[0].artifact.sha256 &&
        currentManifest.signerHash === manifest.components[1].artifact.sha256 &&
        currentManifest.coreHash !== baselineCoreHash &&
        ownerStatus().locked,
    );
    pass(
      "activeControllerBlocksUninstall",
      fails(
        () => cli(["remove", "--prepare-host"]),
        /recoverable installation/,
      ),
    );
    pass(
      "actualOwnerCannotAbortInstalledTrial",
      terminal("abort", "denied").exitCode !== 0,
    );
    const beforeRepeat = inventory().signer;
    const repeated = JSON.parse(
      cli(["updates", "install", "--prepare-host", "--intent-id", id]),
    );
    const afterRepeat = inventory().signer;
    pass(
      "healthyTrialRetryDoesNotRewriteSignerJournal",
      repeated.phase === "healthy-held" &&
        JSON.stringify(beforeRepeat.states) ===
          JSON.stringify(afterRepeat.states),
    );
    pass(
      "actualRollbackProcessKilledAfterCoreReplacement",
      terminal("rollback", "interrupt").interrupted === true,
    );
    const rolledBack = JSON.parse(
      cli(["updates", "install", "--prepare-host", "--intent-id", id]),
    );
    report.rollback = rolledBack;
    pass(
      "durableRollbackDecisionSurvivesColdResume",
      rolledBack.phase === "rolled-back-held" && ownerStatus().locked,
    );
    const restored = JSON.parse(
      execFileSync(engine, ["custody", "status", "--disposable-fixture"], {
        encoding: "utf8",
        env,
      }),
    ).manifest;
    pass(
      "rollbackRestoresOnlyPreviousExecutablePair",
      restored.coreHash === installed.binding.previousCore &&
        restored.signerHash === installed.binding.previousSigner &&
        inventory().signer.sequenceFloor === "1",
    );
    pass(
      "actualOwnerTTYAbortCancel",
      terminal("abort", "cancel").exitCode !== 0 &&
        inventory().signer.activeIntentId === id,
    );
    pass("actualOwnerTTYAbort", terminal("abort", "approve").exitCode === 0);
    pass(
      "abortedControllerCannotReplayFromRetainedHostReceipt",
      fails(
        () => cli(["updates", "install", "--prepare-host", "--intent-id", id]),
        /hold|inactive/,
      ),
    );
    pass(
      "abortedIntentCannotAcknowledgeBackup",
      fails(() => backup(), /staged intent/),
    );
    pass(
      "abortedIntentCannotStage",
      fails(() => stage(), /stale or inactive/),
    );
    pass(
      "abortedIntentCannotPreflight",
      fails(() => preflight(), /staged intent/),
    );
    pass(
      "actualAbortReleasesOnlyMatchingMetadataFence",
      JSON.parse(app(["--request-stdin"], JSON.stringify(record))).record
        .revision ===
        record.revision + 1 && ownerStatus().locked,
    );
    current = inventory();
    report.final = current;
    pass(
      "abortDoesNotRewindFloorOrUnlock",
      current.signer.activeIntentId === "" &&
        current.signer.sequenceFloor === "1" &&
        ownerStatus().locked &&
        !ownerStatus().updateHeld,
    );
    pass(
      "abortPreservesFinancialHistory",
      current.signer.authoritySha256 === before.signer.authoritySha256 &&
        current.signer.spent === before.signer.spent &&
        current.signer.held === before.signer.held &&
        current.signer.pending === before.signer.pending &&
        current.signer.unknown === before.signer.unknown &&
        current.signer.states.find((s) => s.name === "vault").anchor ===
          before.signer.states.find((s) => s.name === "vault").anchor &&
        current.signer.states.find((s) => s.name === "mock-receipts").anchor ===
          before.signer.states.find((s) => s.name === "mock-receipts").anchor,
    );
    await saveManifest(1);
    pass(
      "reservedSequenceReplayDenied",
      fails(
        () =>
          cli([
            "updates",
            "prepare",
            "--prepare-host",
            "--intent-id",
            randomBytes(16).toString("hex"),
            "--manifest",
            mp,
            "--trust-file",
            tp,
            "--artifact-dir",
            root,
          ]),
        /sequence floor/,
      ),
    );
    // A separately compiled, signed disposable candidate reports the same
    // reader contract but intentionally cannot serve. This drives real systemd
    // health failure and executable-only rollback without a product fault flag.
    id = randomBytes(16).toString("hex");
    await writeFile(
      path.join(candidates, "fased-app"),
      await readFile(path.resolve(".evidence/bin/fased-unhealthy-core")),
      { mode: 0o700 },
    );
    for (const c of manifest.components) {
      const bytes = await readFile(path.join(candidates, c.artifact.file));
      c.artifact.size = bytes.length;
      c.artifact.sha256 = sha(bytes);
    }
    await saveManifest(2);
    pass(
      "unhealthyCandidateGetsSeparateOwnerReview",
      terminal("prepare", "approve").exitCode === 0,
    );
    stage();
    pass(
      "actualUnhealthyTrialCompletesControlledRollback",
      terminal("install", "approve").exitCode === 0,
    );
    const unhealthyState = JSON.parse(
      cli(["updates", "install-status", "--prepare-host", "--intent-id", id]),
    );
    report.unhealthy = unhealthyState;
    const oldAgain = JSON.parse(
      execFileSync(engine, ["custody", "status", "--disposable-fixture"], {
        encoding: "utf8",
        env,
      }),
    ).manifest;
    pass(
      "failedHealthRestoresPreviousPairAndKeepsFloor",
      unhealthyState.installation.phase === "rolled-back-held" &&
        oldAgain.coreHash === restored.coreHash &&
        oldAgain.signerHash === restored.signerHash &&
        inventory().signer.sequenceFloor === "2" &&
        ownerStatus().locked &&
        ownerStatus().updateHeld,
    );
    pass(
      "failedHealthRollbackRequiresProtectedAbort",
      terminal("abort", "approve").exitCode === 0 &&
        inventory().signer.sequenceFloor === "2",
    );
    const end = inventory().signer;
    pass(
      "bothTrialsPreserveFinancialAuthorityAndUnknowns",
      end.authoritySha256 === before.signer.authoritySha256 &&
        end.spent === before.signer.spent &&
        end.held === before.signer.held &&
        end.pending === before.signer.pending &&
        end.unknown === before.signer.unknown &&
        end.recoveryHold === before.signer.recoveryHold,
    );
    report.finalAfterInstall = end;
    report.result = "PASS";
  } catch (error) {
    report.error = error.message;
    throw error;
  } finally {
    await rm(mp, { force: true });
    await rm(tp, { force: true });
    await rm(candidates, { recursive: true, force: true });
    report.cleanup =
      "public test inputs removed; private manifest key stayed in memory; managed parent removes generated services/accounts/state";
    await writeFile(
      path.join(evidence, "managed-install-controller-result.json"),
      JSON.stringify(report, null, 2) + "\n",
    );
  }
}
