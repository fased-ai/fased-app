// Actual unsigned Inno setup/uninstall, with generated profile paths on the
// disposable Windows runner. This does not prove Windows 11 normal-user/UAC.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  cp,
  mkdir,
  readFile,
  readdir,
  writeFile,
  rm,
  stat,
} from "node:fs/promises";
import { join, resolve } from "node:path";
import { randomBytes, createHash } from "node:crypto";

async function checkContract() {
  const source = await readFile("packaging/windows.iss", "utf8");
  const files = source.match(
    /^Source: "\{#Payload\}\\\*"; DestDir: "([^"]+)"/m,
  );
  assert.ok(files, "one explicit bundle payload is required");
  assert.equal(
    files[1],
    "{app}\\bundle",
    "Inno metadata must remain outside the verified bundle inventory",
  );
  for (const executable of ["fased-app.exe", "fased-desktop.exe"])
    assert.ok(
      !source.includes(`'{app}\\${executable}'`) &&
        !source.includes(`"{app}\\${executable}"`),
      "installer, removal and launch must use the isolated verified payload",
    );
  assert.match(source, /function GetCustomSetupExitCode\(\): Integer;/);
  assert.match(source, /CoreInstallAttempted and \(not CoreInstallConfirmed\)/);
  assert.match(source, /CoreInstallConfirmed := Code = 0;/);
  assert.match(source, /CoreInstallAttempted := True;/);
  assert.match(
    source,
    /CoreInstallConfirmed := ExecAndLogOutput\([^\n]+ewWaitUntilTerminated, Code, nil\);/,
    "core install errors must reach the existing setup log without weakening failure propagation",
  );
  assert.match(
    source,
    /Result := ExecAndLogOutput\([^\n]+'uninstall'[^\n]+ewWaitUntilTerminated, Code, nil\);/,
    "core removal errors must reach the existing uninstall log",
  );
  return { result: "PASS", class: "source contract; Windows runtime unproven" };
}

async function failureSnapshot(app, data) {
  const snapshot = {};
  try {
    const manifest = JSON.parse(
      await readFile(join(app, "install.json"), "utf8"),
    );
    snapshot.manifest = Object.fromEntries(
      [
        "schema",
        "phase",
        "version",
        "root",
        "data",
        "listen",
        "location",
        "autostart",
      ]
        .filter((key) => Object.hasOwn(manifest, key))
        .map((key) => [key, manifest[key]]),
    );
  } catch (error) {
    snapshot.manifestError = error.code ?? error.name;
  }
  try {
    snapshot.endpointPresent = (
      await stat(join(data, "endpoint.json"))
    ).isFile();
  } catch (error) {
    snapshot.endpointPresent = false;
    if (error.code !== "ENOENT") snapshot.endpointError = error.code;
  }
  try {
    snapshot.task = JSON.parse(
      execFileSync(
        "powershell.exe",
        [
          "-NoProfile",
          "-NonInteractive",
          "-Command",
          `
$taskIdentity = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$taskName = 'FasedApp-' + $taskIdentity
$ownedTask = Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue
if ($null -eq $ownedTask) { @{present=$false;name=$taskName} | ConvertTo-Json -Compress; exit 0 }
$ownedInfo = Get-ScheduledTaskInfo -TaskName $taskName -ErrorAction Stop
@{present=$true;name=$taskName;state=[string]$ownedTask.State;user=$ownedTask.Principal.UserId;logon=[string]$ownedTask.Principal.LogonType;runLevel=[string]$ownedTask.Principal.RunLevel;lastRun=$ownedInfo.LastRunTime;lastResult=$ownedInfo.LastTaskResult;nextRun=$ownedInfo.NextRunTime;missedRuns=$ownedInfo.NumberOfMissedRuns} | ConvertTo-Json -Compress
`,
        ],
        { encoding: "utf8", timeout: 10000 },
      ),
    );
  } catch (error) {
    snapshot.taskError = error.code ?? error.status ?? error.name;
  }
  return snapshot;
}

async function checkWindowsSetup() {
  assert.equal(process.env.GITHUB_ACTIONS, "true");
  assert.equal(process.platform, "win32");
  const root = resolve(".evidence/windows-setup");
  await mkdir(root);
  const payload = join(root, "payload"),
    home = join(root, "profile"),
    roaming = join(root, "roaming"),
    data = join(roaming, "fased-app"),
    app = join(home, "AppData", "Local", "Fased App", "app");
  await mkdir(home);
  await mkdir(roaming);
  const env = { ...process.env, USERPROFILE: home, APPDATA: roaming };
  const setup = resolve(
    ".evidence/packages/fased-app-windows-setup-unsigned.exe",
  );
  const uninstaller = join(payload, "unins000.exe");
  const bundle = join(payload, "bundle");
  const binary = join(bundle, "fased-app.exe");
  const cli = (args, input) =>
    execFileSync(binary, args, {
      env,
      input,
      encoding: "utf8",
      timeout: 40000,
    });
  const report = {
    result: "FAIL",
    class:
      "Windows Server actual unsigned setup, generated profile paths; normal-user Windows 11 remains unproven",
    checks: {},
  };
  let installed = false;
  try {
    execFileSync(
      setup,
      [
        "/VERYSILENT",
        "/SUPPRESSMSGBOXES",
        "/NORESTART",
        `/DIR=${payload}`,
        `/LOG=${join(root, "install.log")}`,
      ],
      { env, timeout: 120000 },
    );
    installed = true;
    const bundleManifest = JSON.parse(
      await readFile(join(bundle, "bundle.json"), "utf8"),
    );
    assert.deepEqual(
      (await readdir(bundle)).sort(),
      ["bundle.json", ...Object.keys(bundleManifest.files)].sort(),
    );
    assert.ok((await stat(uninstaller)).isFile());
    report.checks.bundleIsolatedFromInnoMetadata = "PASS";
    const state = JSON.parse(cli(["service", "status"]));
    assert.equal(state.active, true);
    const instance = JSON.parse(cli(["status"]));
    const pw = "Fixture-" + randomBytes(24).toString("hex");
    const code = JSON.parse(
      await readFile(join(data, "claim-code.json"), "utf8"),
    ).code;
    // Claim through the same loopback API the native/browser client uses.
    const url = state.url;
    const response = await fetch(url + "/api/v1/auth/claim", {
      method: "POST",
      headers: { Origin: url, "Content-Type": "application/json" },
      body: JSON.stringify({ code, password: pw }),
    });
    assert.equal(response.status, 200);
    cli(["login", "--password-stdin"], pw + "\n");
    cli(["setup", "--host", "local"]);
    const expected = JSON.parse(cli(["setup"]));
    await writeFile(join(data, "keep-owner-note"), "preserve instance data");
    cli(["service", "stop"]);
    const manifestBytes = await readFile(join(app, "install.json"));
    const manifest = JSON.parse(manifestBytes.toString("utf8"));
    const name = Object.keys(manifest.files).find((n) => n.endsWith(".xml"));
    assert.ok(name);
    const managedBinary = join(app, name);
    const original = await readFile(managedBinary);
    try {
      await writeFile(
        managedBinary,
        Buffer.concat([original, Buffer.from("changed fixture inventory")]),
      );
      assert.throws(() =>
        execFileSync(
          uninstaller,
          ["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
          { env, timeout: 30000 },
        ),
      );
      assert.ok((await stat(uninstaller)).isFile());
      assert.ok((await stat(join(app, "install.json"))).isFile());
      report.checks.foreignInventoryAbortsUninstall = "PASS";
    } finally {
      await writeFile(managedBinary, original);
    }
    const before = {};
    for (const name of ["core.sqlite", "keep-owner-note"]) {
      before[name] = createHash("sha256")
        .update(await readFile(join(data, name)))
        .digest("hex");
    }
    // Compile one corrupt copy with no uninstaller/shortcut registration. The
    // production post-install callback must report the child's actual refusal,
    // while the stopped, claimed fixture instance remains byte-for-byte intact.
    const invalid = join(root, "invalid");
    const invalidSource = join(invalid, "source");
    const invalidOutput = join(invalid, "output");
    const invalidPayload = join(invalid, "payload");
    await mkdir(invalid);
    await cp(bundle, invalidSource, { recursive: true });
    await mkdir(invalidOutput);
    await writeFile(
      join(invalidSource, "NOTICE"),
      Buffer.concat([
        await readFile(join(invalidSource, "NOTICE")),
        Buffer.from("\ncorrupt disposable inventory fixture"),
      ]),
    );
    const inno = (await readFile("packaging/windows.iss", "utf8"))
      .replace(
        /^AppId=org\.fased\.app$/m,
        "AppId=org.fased.app.fixture-failure",
      )
      .replace(/^UninstallDisplayName=.*$/m, "Uninstallable=no")
      .replace(/\[Icons\][\s\S]*?\[Code\]/, "[Code]");
    assert.match(inno, /^AppId=org\.fased\.app\.fixture-failure$/m);
    assert.match(inno, /^Uninstallable=no$/m);
    assert.doesNotMatch(inno, /^\[(Icons|Run)\]$/m);
    const invalidScript = join(invalid, "fixture.iss");
    await writeFile(invalidScript, inno);
    execFileSync(
      "C:\\Program Files (x86)\\Inno Setup 6\\ISCC.exe",
      [invalidScript],
      {
        env: {
          ...env,
          FASED_PACKAGE_PAYLOAD: invalidSource,
          FASED_PACKAGE_OUTPUT: invalidOutput,
        },
        stdio: "pipe",
        timeout: 120000,
      },
    );
    let invalidExit;
    try {
      execFileSync(
        join(invalidOutput, "fased-app-windows-setup-unsigned.exe"),
        [
          "/VERYSILENT",
          "/SUPPRESSMSGBOXES",
          "/NORESTART",
          `/DIR=${invalidPayload}`,
          `/LOG=${join(invalid, "install.log")}`,
        ],
        { env, timeout: 120000 },
      );
    } catch (error) {
      invalidExit = error.status;
    }
    assert.ok(
      Number.isInteger(invalidExit) && invalidExit !== 0,
      "silent setup must report core-install refusal",
    );
    const invalidLogBytes = await readFile(join(invalid, "install.log"));
    const invalidLog = invalidLogBytes.toString(
      invalidLogBytes[0] === 0xff && invalidLogBytes[1] === 0xfe
        ? "utf16le"
        : "utf8",
    );
    assert.match(
      invalidLog,
      /Fased core installation was not confirmed \(exit code 1\)\./,
    );
    assert.deepEqual(await readFile(join(app, "install.json")), manifestBytes);
    for (const [name, hash] of Object.entries(before))
      assert.equal(
        createHash("sha256")
          .update(await readFile(join(data, name)))
          .digest("hex"),
        hash,
      );
    report.checks.failedSetupPreservesStoppedState = "PASS";
    report.checks.failedChildInstallReturnsFailure = "PASS";
    execFileSync(
      uninstaller,
      ["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
      { env, timeout: 120000 },
    );
    installed = false;
    for (const [name, hash] of Object.entries(before))
      assert.equal(
        createHash("sha256")
          .update(await readFile(join(data, name)))
          .digest("hex"),
        hash,
      );
    await assert.rejects(stat(join(app, "install.json")), { code: "ENOENT" });
    report.result = "PASS";
    report.instanceId = instance.instanceId;
    Object.assign(report.checks, {
      unsignedSetupStartsCore: "PASS",
      sameCLISetup: expected.host,
      uninstallPreservesStoppedState: "PASS",
      managedInstallationRemoved: "PASS",
    });
  } catch (e) {
    report.error = String(e);
    report.failure = await failureSnapshot(app, data);
    throw e;
  } finally {
    if (installed) {
      try {
        execFileSync(
          uninstaller,
          ["/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART"],
          { env, timeout: 120000 },
        );
      } catch (e) {
        report.cleanupError = String(e);
      }
    }
    for (const [name, source] of [
      ["install", join(root, "install.log")],
      ["refusal", join(root, "invalid", "install.log")],
    ])
      try {
        await writeFile(
          `.evidence/windows-setup-${name}.log`,
          await readFile(source),
        );
      } catch (error) {
        if (error.code !== "ENOENT") report.logError = String(error);
      }
    await writeFile(
      ".evidence/windows-setup-result.json",
      JSON.stringify(report, null, 2) + "\n",
    );
    if (!report.cleanupError) await rm(root, { recursive: true });
  }
}

if (process.argv.length === 3 && process.argv[2] === "--contract")
  console.log(JSON.stringify(await checkContract()));
else {
  assert.equal(process.argv.length, 2, "no runtime overrides are accepted");
  await checkContract();
  await checkWindowsSetup();
}
