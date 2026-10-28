// Runs only as the ordinary owner INSIDE a disposable, booted Ubuntu VM.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { writeFileSync, readFileSync } from "node:fs";
import { readFile, writeFile, mkdir, readdir, unlink } from "node:fs/promises";
import { generateKeyPairSync, randomBytes } from "node:crypto";
import { fixtureDevelopmentEnvelope } from "./fixture-development-envelope.mjs";
import {
  fixtureInspectionJourney,
  fixtureInspectionPTY,
} from "./fixture-inspection-journey.mjs";
import { sha, save } from "./handoff-transport-evidence.mjs";
assert.equal(process.env.FASED_DISPOSABLE_VM, "1");
assert.ok(process.getuid() > 0);
assert.match(await readFile("/etc/os-release", "utf8"), /VERSION_ID="24\.04"/);
assert.match(
  await readFile("/sys/class/dmi/id/product_name", "utf8"),
  /QEMU|Standard PC/,
);
const evidence = ".evidence/p4-fault-reboot/guest";
await mkdir(evidence, { recursive: true, mode: 0o700 });
const env = {
  ...process.env,
  FASED_FIXTURE_MAINTENANCE_TTL: "120",
  XDG_CONFIG_HOME: process.cwd() + "/" + evidence + "/owner-config",
  FASED_INSPECTION_EVIDENCE_ROOT: process.cwd() + "/" + evidence,
  FASED_INSPECTION_EXPORTER: process.cwd() + "/.evidence/bin/update-tests",
};
await mkdir(env.XDG_CONFIG_HOME, { recursive: true, mode: 0o700 });
const root = "/var/lib/fased-app-custody-" + process.getuid();
const old =
  process.cwd() + "/.evidence/p4-installed-inspection/predecessor-app";
const core = process.cwd() + "/.evidence/bin/fased-app";
const run = (bin, args, input) =>
  execFileSync(bin, args, {
    env,
    input,
    encoding: "utf8",
    timeout: 120000,
    maxBuffer: 1 << 20,
  });
const osTool = (bin, args, input) =>
  run("/usr/bin/sudo", ["-n", bin, ...args], input);
const cli = (args, input) =>
  run(old, ["custody", ...args, "--disposable-fixture"], input);
const check = (name, ok) => assert.equal(ok, true, name);
const status = () =>
  JSON.parse(
    run(root + "/fased-signer", [
      "owner",
      "--config",
      root + "/identity.json",
      "--disposable-fixture",
    ]),
  );
const owner = process.getuid(),
  units = ["app", "signer"].map((r) => `fased-custody-${owner}-${r}.service`);
const boot = () =>
  readFile("/proc/sys/kernel/random/boot_id", "utf8").then((s) => s.trim());
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
const tty = String.raw`import os,pty,select,sys,time,re,json,signal
binary,op,ident,manifest,trust,artifacts=sys.argv[1:]
args=[binary,'custody','updates',op,'--intent-id',ident,'--disposable-fixture','--prepare-host']
if op=='prepare':args+=['--manifest',manifest,'--trust-file',trust,'--artifact-dir',artifacts]
if op=='install':args+=['--artifact-dir',artifacts]
pid,fd=pty.fork()
if pid==0:os.execv(binary,args)
seen=b'';sent=False;done=None;deadline=time.monotonic()+110
try:
 while time.monotonic()<deadline:
  r,_,_=select.select([fd],[],[],.05)
  if r:
   try:b=os.read(fd,65536)
   except OSError:break
   if not b:break
   seen+=b
   m=re.search(rb'Type ((?:PREPARE|INSTALL|COMPLETE) [a-f0-9]{64})',seen)
   if m and not sent:os.write(fd,m.group(1)+b'\n');sent=True
  p,s=os.waitpid(pid,os.WNOHANG)
  if p:done=os.waitstatus_to_exitcode(s);break
finally:
 if done is None:
  if time.monotonic()>=deadline:os.killpg(pid,signal.SIGKILL)
  _,s=os.waitpid(pid,0);done=os.waitstatus_to_exitcode(s)
 os.close(fd)
assert done==0 and sent,seen.decode(errors='replace')
print(json.dumps({'exitCode':done,'review':True}))`;
const [mode, arg] = process.argv.slice(2);
if (mode === "managed-remote") {
  assert.equal(arg, undefined);
  run(core, ["-test.run=^TestManagedRemoteUbuntuVM$", "-test.v"]);
  const result = JSON.parse(
    await readFile(evidence + "/managed-remote.json", "utf8"),
  );
  assert.equal(result.result, "PASS");
  assert.equal(result.fixtureOnly, true);
  process.exit(0);
}
if (mode === "setup") {
  const installed = JSON.parse(
    cli([
      "install",
      "--signer-binary",
      process.cwd() + "/.evidence/p4-installed-inspection/predecessor-signer",
      "--listen",
      "127.0.0.1:18789",
      "--prepare-host",
    ]),
  );
  assert.equal(
    new Set([owner, installed.manifest.app.uid, installed.manifest.signer.uid])
      .size,
    3,
  );
  await mkdir(evidence + "/backups", { mode: 0o700 });
  const password = "Disposable VM only " + randomBytes(12).toString("hex");
  const origin = "http://127.0.0.1:18789";
  const claimCode = cli(["claim-code", "--prepare-host"]).trim();
  const claimed = await fetch(origin + "/api/v1/auth/claim", {
    method: "POST",
    headers: { Origin: origin, "Content-Type": "application/json" },
    body: JSON.stringify({ code: claimCode, password }),
  });
  assert.equal(claimed.status, 200);
  cli(["login", "--prepare-host", "--password-stdin"], password + "\n");
  const setup = JSON.parse(
    cli(
      [
        "setup",
        "--backup-file",
        process.cwd() + "/" + evidence + "/backups/disposable.backup",
        "--password-stdin",
      ],
      [password, password, password, password, ""].join("\n"),
    ),
  );
  assert.ok(
    setup.backupVerified && setup.status.locked && !setup.status.walletSigner,
  );
  const { publicKey, privateKey } = generateKeyPairSync("ed25519"),
    pub = publicKey.export({ type: "spki", format: "der" }).subarray(-32),
    keyId = sha(pub),
    now = Math.floor(Date.now() / 1000);
  const components = [];
  for (const role of ["app", "signer"]) {
    const file = "fased-" + role,
      b = await readFile(root + "/" + file),
      r = JSON.parse(
        run(
          root + "/" + file,
          role === "app"
            ? ["updates", "compatibility"]
            : ["fixture-compatibility"],
        ),
      );
    components.push({
      version: "0.0.0-disposable",
      artifact: { file, size: b.length, sha256: sha(b) },
      contract: role === "app" ? r.contract : r,
    });
  }
  const manifest = {
    version: 1,
    product: "fased-app",
    channel: "development",
    sequence: "1",
    issuedAt: now - 1,
    expiresAt: now + 3600,
    target: { os: "linux", arch: "amd64" },
    components,
  };
  const mp = process.cwd() + "/" + evidence + "/maintenance.json",
    tp = process.cwd() + "/" + evidence + "/trust.json",
    id = randomBytes(16).toString("hex");
  await writeFile(mp, fixtureDevelopmentEnvelope(manifest, keyId, privateKey), {
    mode: 0o600,
  });
  await save(tp, {
    version: 1,
    purpose: "fased-app-update-development:v1",
    keys: [
      {
        id: keyId,
        publicKey: pub.toString("base64"),
        notBefore: now - 60,
        notAfter: now + 7200,
      },
    ],
  });
  const terminal = (op) =>
    JSON.parse(run("/usr/bin/python3", ["-c", tty, old, op, id, mp, tp, root]));
  terminal("prepare");
  cli([
    "updates",
    "stage",
    "--intent-id",
    id,
    "--artifact-dir",
    root,
    "--prepare-host",
  ]);
  cli(["updates", "preflight", "--intent-id", id, "--prepare-host"]);
  cli(["updates", "backup", "--intent-id", id, "--prepare-host"]);
  terminal("install");
  terminal("complete");
  const current = inventory();
  assert.equal(current.signer.installedSequence, "1");
  assert.equal(current.signer.sequenceFloor, "1");
  assert.equal(current.signer.activeIntentId, "");
  assert.ok(status().locked);
  // Installer autostart stays opt-in. Boot tests explicitly enable these fixture units.
  osTool("/usr/bin/systemctl", ["enable", ...units]);
  await save(evidence + "/setup.json", {
    result: "PASS",
    boot: await boot(),
    inventory: current,
    manifest: installed.manifest,
    fixtureOnly: true,
    realWallets: false,
    automaticUpdates: false,
  });
} else if (mode === "configure") {
  // Provisioning is finished; only this disposable fixture disables cloud-init.
  const marker = evidence + "/cloud-init-fixture-disabled";
  await writeFile(marker, "disposable P4 fixture only\n", { mode: 0o600 });
  osTool("/usr/bin/install", [
    "-o",
    "0",
    "-g",
    "0",
    "-m",
    "0644",
    "--",
    marker,
    "/etc/cloud/cloud-init.disabled",
  ]);
  await save(evidence + "/os-fixture.json", {
    cloudInitDisabledAfterProvisioning: true,
    projectRunsAsRoot: false,
  });
} else if (mode === "cut") {
  const cutReceipt = await fixtureInspectionJourney({
    root,
    env,
    check,
    ownerStatus: status,
    restartApp: () => osTool("/usr/bin/systemctl", ["restart", units[0]]),
    restartSigner: () => osTool("/usr/bin/systemctl", ["restart", units[1]]),
    stopAt: arg,
  });
  await save(evidence + "/installed-result.json", {
    ...cutReceipt,
    boot: await boot(),
  });
} else if (mode === "advance") {
  const [phase, bridgeId] = arg.split(":");
  assert.match(bridgeId, /^[a-f0-9]{32}$/);
  env.FASED_TEST_BRIDGE_STOP = phase;
  const result = JSON.parse(
    run("/usr/bin/python3", [
      "-c",
      fixtureInspectionPTY,
      core,
      "bridge",
      bridgeId,
      "",
      "",
      "",
      "approve",
      phase,
    ]),
  );
  assert.ok(
    result.interrupted && result.exitCode !== 0,
    "actual controller did not stop at the requested cut",
  );
  await save(evidence + "/installed-result.json", {
    result: "CUT",
    fixtureOnly: true,
    bridgeId,
    cut: phase,
    terminal: result,
    boot: await boot(),
  });
} else if (mode === "old-reader") {
  let error;
  try {
    cli(["status"]);
  } catch (e) {
    error = e;
  }
  assert.ok(
    error &&
      /foreign|changed|unknown field|invalid installation/i.test(
        String(error.stderr),
      ),
    "compiled predecessor managed verification did not refuse",
  );
  await save(evidence + "/old-reader.json", {
    result: "PASS",
    compiledPredecessorRef: "6c53e34eca22486dcef856cb701f3cd72e30eaa4",
    error: String(error.stderr),
  });
} else if (mode === "tamper") {
  const financial = () =>
    Object.fromEntries(
      ["vault.json", "operations.log", "mock-receipts.log"].map((n) => {
        try {
          return [
            n,
            sha(
              execFileSync(
                "/usr/bin/sudo",
                ["-n", "/usr/bin/cat", root + "/signer-state/" + n],
                { env, encoding: null, timeout: 10000 },
              ),
            ),
          ];
        } catch (e) {
          assert.match(String(e.stderr), /No such file/);
          return [n, "ABSENT"];
        }
      }),
    );
  const before = financial(),
    base = root + "/fixture-inspection-v1",
    inputs = base + "/inputs";
  const fixed = JSON.parse(await readFile(inputs + "/catalog.json", "utf8"));
  const replace = (file, bytes, fileOwner = "0", mode = "0644") => {
    const source = process.cwd() + "/" + evidence + "/input-fault.bin";
    writeFileSync(source, bytes, { mode: 0o600 });
    osTool("/usr/bin/install", [
      "-o",
      fileOwner,
      "-g",
      fileOwner,
      "-m",
      mode,
      "--",
      source,
      file,
    ]);
    assert.deepEqual(
      readFileSync(file),
      Buffer.from(bytes),
      "fault producer bytes changed",
    );
  };
  let retained;
  if (arg === "catalog-mode")
    replace(
      inputs + "/catalog.json",
      await readFile(inputs + "/catalog.json"),
      "0",
      "0666",
    );
  else if (arg === "catalog-owner")
    replace(
      inputs + "/catalog.json",
      await readFile(inputs + "/catalog.json"),
      String(owner),
    );
  else if (arg === "role-substitution")
    replace(inputs + "/owner.json", await readFile(inputs + "/signer.json"));
  else if (arg === "capability")
    replace(
      inputs + "/catalog.json",
      JSON.stringify({
        ...fixed,
        reader: { ...fixed.reader, capability: "production" },
      }),
    );
  else if (arg === "config-pin")
    replace(
      root + "/identity.json",
      Buffer.concat([
        await readFile(root + "/identity.json"),
        Buffer.from("\n"),
      ]),
    );
  else if (arg === "capsule-pin") {
    const file = base + "/capsules/" + fixed.provision + "/transaction.json";
    replace(file, Buffer.concat([await readFile(file), Buffer.from("\n")]));
  } else if (arg === "seed-pin")
    replace(
      inputs + "/owner-seed.json",
      Buffer.concat([
        await readFile(inputs + "/owner-seed.json"),
        Buffer.from("\n"),
      ]),
    );
  else if (arg === "source-index")
    replace(
      inputs + "/bundle/index.json",
      Buffer.concat([
        await readFile(inputs + "/bundle/index.json"),
        Buffer.from("\n"),
      ]),
    );
  else if (arg === "source-subject") {
    const ev = JSON.parse(
        await readFile(inputs + "/bundle/evidence.json", "utf8"),
      ),
      unit = JSON.parse(Buffer.from(ev.unit, "base64"));
    const file = unit.subjects[0].file;
    assert.match(file, /^[a-z0-9][a-z0-9.-]{0,63}$/);
    assert.ok(!file.includes(".."));
    replace(inputs + "/bundle/" + file, "altered inert fixture subject");
  } else if (arg === "state-missing")
    await unlink(base + "/owner/state/head.json");
  else if (arg === "anchor-corrupt")
    await writeFile(base + "/owner/anchor/head.json", "{}", { mode: 0o600 });
  else if (arg === "state-rollback") {
    const dir = base + "/owner/state",
      head = JSON.parse(await readFile(dir + "/head.json", "utf8")),
      current = JSON.parse(
        await readFile(dir + "/" + head.sha256 + ".json", "utf8"),
      );
    const previous = [];
    for (const file of await readdir(dir))
      if (/^[a-f0-9]{64}\.json$/.test(file)) {
        const cp = JSON.parse(await readFile(dir + "/" + file, "utf8"));
        if (Number(cp.serial) < Number(current.serial))
          previous.push(file.slice(0, 64));
      }
    assert.ok(previous.length);
    await writeFile(
      dir + "/head.json",
      JSON.stringify({ ...head, sha256: previous[0] }),
      { mode: 0o600 },
    );
  } else if (arg === "foreign-namespace") {
    retained = base + "/owner/state/foreign.json";
    await writeFile(retained, "preserve foreign fixture", { mode: 0o600 });
  } else if (arg === "legacy-head")
    await writeFile(
      base + "/owner/state/head.json",
      JSON.stringify({
        version: 1,
        purpose: "fased-app-update-fixture:v1",
        seedSha256: "a".repeat(64),
        stateSha256: "b".repeat(64),
      }),
      { mode: 0o600 },
    );
  else throw Error("closed input fault case");
  const result = JSON.parse(
    run("/usr/bin/python3", [
      "-c",
      fixtureInspectionPTY,
      core,
      "inspect",
      "",
      "",
      "",
      "",
      "approve",
      "",
    ]),
  );
  assert.ok(
    result.exitCode !== 0 && !result.output.includes('"exchange":'),
    "invalid input reached review/completion",
  );
  assert.deepEqual(
    financial(),
    before,
    "input refusal changed financial originals",
  );
  if (retained)
    assert.equal(await readFile(retained, "utf8"), "preserve foreign fixture");
  await save(evidence + "/tamper.json", {
    result: "PASS",
    case: arg,
    before,
    after: financial(),
    preservedForeign: !!retained,
    exitCode: result.exitCode,
    output: result.output.slice(-1200),
  });
} else if (mode === "clock") {
  const proof = JSON.parse(
    run("/usr/bin/python3", [
      "scripts/fixture-inspection-clock-probe.py",
      root,
      arg,
    ]),
  );
  await save(evidence + "/clock.json", proof);
} else if (mode === "probes") {
  const terminal = (choice) =>
    JSON.parse(
      run("/usr/bin/python3", [
        "-c",
        fixtureInspectionPTY,
        core,
        "inspect",
        "",
        "",
        "",
        "",
        choice,
        "",
      ]),
    );
  const cancelled = terminal("cancel");
  assert.ok(
    cancelled.exitCode !== 0 &&
      cancelled.output.includes("inspection cancelled"),
  );
  const approved = terminal("approve");
  assert.ok(
    approved.exitCode === 0 &&
      approved.output.includes('"exchange":') &&
      approved.output.includes('"financialExecution":false'),
  );
  await save(evidence + "/protected-inspection.json", { cancelled, approved });

  await save(evidence + "/probe-inventory.json", inventory());
  const proof = JSON.parse(
    run("/usr/bin/python3", [
      "scripts/fixture-inspection-probes.py",
      root,
      evidence + "/probe-inventory.json",
    ]),
  );
  await save(evidence + "/protocol.json", proof);
} else if (mode === "recover" || mode === "rollback" || mode === "observe") {
  const cut = { bridgeId: arg };
  assert.match(cut.bridgeId, /^[a-f0-9]{32}$/);
  const before = JSON.parse(await readFile(evidence + "/setup.json", "utf8"));
  if (mode !== "observe")
    run(core, [
      "custody",
      "fixture-inspection",
      mode === "rollback" ? "rollback" : "bridge",
      "--bridge-id",
      cut.bridgeId,
      "--disposable-fixture",
      "--prepare-host",
    ]);
  const now = inventory();
  assert.equal(
    now.signer.installedSequence,
    before.inventory.signer.installedSequence,
  );
  assert.equal(
    now.signer.authoritySha256,
    before.inventory.signer.authoritySha256,
  );
  for (const n of ["spent", "held", "unknown"])
    assert.equal(now.signer[n], before.inventory.signer[n]);
  assert.ok(status().locked && !status().walletSigner);
  await save(evidence + "/" + mode + ".json", {
    result: "PASS",
    boot: await boot(),
    inventory: now,
    units: osTool("/usr/bin/systemctl", [
      "show",
      ...units,
      "--property=MainPID,ExecStart,User,ActiveState",
    ]),
    fixtureOnly: true,
    realWallets: false,
    automaticUpdates: false,
  });
} else throw Error("closed VM fixture operation");
console.log("PASS disposable VM " + mode);
