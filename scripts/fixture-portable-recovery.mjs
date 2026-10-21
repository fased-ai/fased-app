// Disposable Ubuntu-only, actual signer/app/owner UIDs. No owner host use.
import assert from "node:assert/strict";
import { randomBytes, createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdir, readFile, writeFile, lstat } from "node:fs/promises";
import path from "node:path";
export async function fixturePortableRecovery({
  root,
  manifest,
  osTool,
  as,
  check,
  fails,
  ownerStatus,
  stopSigner,
  restartSigner,
  fixtureOwner,
  walletPassword,
}) {
  assert.equal(process.env.GITHUB_ACTIONS, "true");
  assert.ok(process.getuid() > 0);
  const evidence = path.resolve(".evidence/p4-portable-recovery");
  await mkdir(evidence, { recursive: true, mode: 0o700 });
  const report = {
    result: "FAIL",
    fixtureOnly: true,
    class:
      "Ubuntu installed immutable offline CLI and actual role UIDs; quarantined history, not restored spending authority",
    checks: {},
    walletSigner: false,
    automaticUpdates: false,
    publicActivation: false,
  };
  const pass = (n, v) => {
    check(`portable:${n}`, v);
    report.checks[n] = true;
  };
  const transfer = root + "/portable-transfer-fixture",
    targetState = root + "/portable-empty-state-fixture",
    targetSockets = root + "/portable-sockets-fixture",
    targetConfig = root + "/portable-target-fixture.json";
  const owned = [];
  const sha = (b) => createHash("sha256").update(b).digest("hex");
  const password = "Disposable history " + randomBytes(16).toString("hex");
  const executable = root + "/fased-signer",
    sourceConfig = root + "/identity.json",
    archive = transfer + "/history.enc",
    quarantine = transfer + "/quarantine";
  const cli = (action, config = sourceConfig, extra = []) => [
    action,
    "--disposable-fixture",
    "--config",
    config,
    "--file",
    archive,
    "--password-stdin",
    ...extra,
  ];
  const run = (
    args,
    input = (args[0] === "fixture-portable-export"
      ? walletPassword + "\n"
      : "") +
      password +
      "\n",
  ) => JSON.parse(as(manifest.signer, executable, args, input));
  let stopped = false;
  try {
    for (const p of [transfer, targetState, targetSockets, targetConfig]) {
      try {
        await lstat(p);
        assert.fail("foreign fixture destination: " + p);
      } catch (e) {
        if (e.code !== "ENOENT") throw e;
      }
    }
    for (const [p, mode] of [
      [transfer, "0700"],
      [targetState, "0700"],
      [targetSockets, "0755"],
    ]) {
      osTool("/usr/bin/install", [
        "-d",
        "-o",
        String(manifest.signer.uid),
        "-g",
        String(manifest.signer.gid),
        "-m",
        mode,
        p,
      ]);
      owned.push(p);
    }
    const source = JSON.parse(await readFile(sourceConfig, "utf8"));
    const target = {
      ...source,
      signerId: randomBytes(16).toString("hex"),
      stateDir: targetState,
      socketDir: targetSockets,
    };
    const publicConfig = path.join(evidence, "target-config.json");
    await writeFile(publicConfig, JSON.stringify(target) + "\n", {
      mode: 0o600,
    });
    osTool("/usr/bin/install", [
      "-o",
      "0",
      "-g",
      "0",
      "-m",
      "0644",
      publicConfig,
      targetConfig,
    ]);
    owned.push(targetConfig);
    const before = fixtureOwner("fixture-state").fixture;
    report.before = before;
    pass(
      "ownerUidDenied",
      fails(
        () =>
          execFileSync(executable, cli("fixture-portable-export"), {
            input: password + "\n",
            encoding: "utf8",
          }),
        /configured signer UID/,
      ),
    );
    pass(
      "appUidDenied",
      fails(
        () =>
          as(
            manifest.app,
            executable,
            cli("fixture-portable-export"),
            password + "\n",
          ),
        /configured signer UID/,
      ),
    );
    pass(
      "runningSourceCaptureDenied",
      fails(() => run(cli("fixture-portable-export")), /already locked/),
    );
    stopSigner();
    stopped = true;
    const originals = {};
    for (const n of ["vault.json", "operations.log", "mock-receipts.log"])
      originals[n] = sha(
        osTool("/usr/bin/cat", [root + "/signer-state/" + n], {
          encoding: null,
        }),
      );
    report.originalHashes = originals;
    const receipt = run(cli("fixture-portable-export"));
    report.capture = receipt;
    pass(
      "coldNonzeroHistoryCaptured",
      receipt.history.spent === before.spent &&
        receipt.history.held === before.held &&
        BigInt(receipt.history.installedSequence) > 0n &&
        BigInt(receipt.history.reservedFloor) >=
          BigInt(receipt.history.installedSequence),
    );
    pass(
      "appCannotReadEncryptedArchive",
      fails(() => as(manifest.app, "/usr/bin/cat", [archive])),
    );
    const recoverArgs = cli("fixture-portable-recover", targetConfig, [
      "--directory",
      quarantine,
      "--snapshot-sha256",
      receipt.archiveSha256,
    ]);
    pass(
      "wrongPasswordDenied",
      fails(
        () => run(recoverArgs, "Wrong disposable history password\n"),
        /invalid credential/,
      ),
    );
    pass(
      "substitutedFingerprintDenied",
      fails(
        () =>
          run(
            cli("fixture-portable-recover", targetConfig, [
              "--directory",
              quarantine,
              "--snapshot-sha256",
              "0".repeat(64),
            ]),
          ),
        /fingerprint/,
      ),
    );
    const recovered = run(recoverArgs);
    report.recovered = recovered;
    assert.deepEqual(recovered, receipt);
    pass(
      "archiveOnlyRecoveryMatchesOriginals",
      recovered.history.vaultSha256 === originals["vault.json"] &&
        recovered.history.operationsSha256 === originals["operations.log"] &&
        recovered.history.mockSha256 === originals["mock-receipts.log"],
    );
    pass(
      "recoveryAlwaysHistoricalLockedHeld",
      recovered.historical &&
        recovered.readOnly &&
        recovered.locked &&
        recovered.recoveryRequired &&
        !recovered.installAllowed &&
        !recovered.resumeAllowed &&
        !recovered.walletSigner &&
        !recovered.financialExecution &&
        !recovered.automaticUpdates,
    );
    pass(
      "emptyTargetLiveStateUntouched",
      as(manifest.signer, "/usr/bin/find", [
        targetState,
        "-mindepth",
        "1",
        "-print",
      ]).trim() === "",
    );
    const names = as(manifest.signer, "/usr/bin/find", [
      quarantine,
      "-mindepth",
      "1",
      "-maxdepth",
      "1",
      "-printf",
      "%f\n",
    ])
      .trim()
      .split("\n")
      .sort();
    assert.deepEqual(
      names,
      [
        "intent.json",
        "receipt.json",
        "vault.json.snapshot",
        "operations.log.snapshot",
        "mock-receipts.log.snapshot",
      ].sort(),
    );
    pass("noLiveSignerFilenamesPublished", true);
    pass(
      "appCannotReadRecoveredHistory",
      fails(() =>
        as(manifest.app, "/usr/bin/cat", [
          quarantine + "/operations.log.snapshot",
        ]),
      ),
    );
    assert.deepEqual(run(recoverArgs), receipt);
    pass("historicalRetryIdempotent", true);
    for (const [n, h] of Object.entries(originals))
      assert.equal(
        sha(
          osTool("/usr/bin/cat", [root + "/signer-state/" + n], {
            encoding: null,
          }),
        ),
        h,
      );
    pass("sourceKeyAndJournalsUnchanged", true);
    await restartSigner();
    stopped = false;
    const status = ownerStatus();
    const after = fixtureOwner("fixture-state").fixture;
    report.after = after;
    pass(
      "sourceRestartLockedWithSameAccounting",
      status.locked &&
        !status.walletSigner &&
        after.spent === before.spent &&
        after.held === before.held &&
        after.account.epoch === before.account.epoch,
    );
    report.result = "PASS";
  } catch (e) {
    report.error = e.message;
    throw e;
  } finally {
    if (stopped) await restartSigner();
    // These exact destinations were absent and contain only this declared test's
    // reconstructible copies. Never remove original signer state or owner data.
    for (const p of owned.toReversed()) {
      assert.ok(p.startsWith(root + "/portable-"));
      osTool("/usr/bin/rm", ["-r", "--", p]);
    }
    report.disposableTransfersRemoved = true;
    await writeFile(
      path.join(evidence, "installed-result.json"),
      JSON.stringify(report, null, 2) + "\n",
    );
  }
}
