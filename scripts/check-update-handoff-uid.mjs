// Disposable Ubuntu CI only. Privilege runs fixed OS setup/drop tools, never
// project code. Real/effective/saved IDs/groups/caps drop before each executable.
import assert from "node:assert/strict";
import { spawn, execFileSync, spawnSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import {
  mkdir,
  mkdtemp,
  readFile,
  writeFile,
  lstat,
  rm,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  sha,
  sources,
  unchanged,
  save,
} from "./handoff-transport-evidence.mjs";
assert.equal(process.env.FASED_HANDOFF_UID_CI, "1");
assert.equal(process.env.GITHUB_ACTIONS, "true");
assert.equal(process.env.RUNNER_OS, "Linux");
assert.equal(process.platform, "linux");
assert.ok(process.getuid() > 0);
assert.match(await readFile("/etc/os-release", "utf8"), /VERSION_ID="24\.04"/);
const evidence = path.resolve(".evidence/p4-handoff-uid");
await mkdir(evidence, { recursive: true, mode: 0o700 });
const local = path.resolve(".evidence/p4-handoff-transport");
const localProof = JSON.parse(
  await readFile(path.join(local, "transport-result.json"), "utf8"),
);
assert.equal(localProof.result, "PASS");
const inputs = await sources();
await save(path.join(evidence, "source-inputs.json"), inputs);
assert.equal(
  sha(JSON.stringify(inputs)),
  localProof.sourceInputsSHA256,
  "local binary inputs differ",
);
const fixtureBinary = path.resolve(local, "bin/fased-handoff-fixture"),
  helper = path.resolve(local, "bin/transport-tests");
for (const f of [fixtureBinary, helper])
  assert.equal(
    sha(await readFile(f)),
    localProof.binaries[path.relative(process.cwd(), f)],
  );
const token = randomBytes(6).toString("hex"),
  root = `/var/lib/fhf-${token}`,
  binary = `${root}/fixture`;
const report = {
  result: "FAIL",
  class:
    "disposable Ubuntu 24.04 actual kernel UID boundary; fixture-only, not installed/retail",
  fixtureOnly: true,
  realWallets: false,
  installedExecution: false,
  automaticUpdates: false,
  sourceInputsSHA256: sha(JSON.stringify(inputs)),
  binarySHA256: sha(await readFile(fixtureBinary)),
  checks: {},
  processes: [],
  cases: [],
  cleanup: "FAIL",
};
const users = [],
  processes = [],
  createdPaths = [];
let stage,
  createdRoot = false,
  exporter,
  bundle;
let log = "";
const abort = new AbortController();
for (const signal of ["SIGINT", "SIGTERM"])
  process.once(signal, () => abort.abort());
const sudo = (tool, args, extra = {}) =>
  execFileSync("/usr/bin/sudo", ["-n", tool, ...args], {
    encoding: "utf8",
    timeout: 10000,
    ...extra,
  });
const drop = (u, tool, args) => [
  "-n",
  "/usr/bin/setpriv",
  "--reuid",
  String(u.uid),
  "--regid",
  String(u.gid),
  "--clear-groups",
  "--no-new-privs",
  "--inh-caps=-all",
  "--ambient-caps=-all",
  "--bounding-set=-all",
  "--",
  tool,
  ...args,
];
const as = (u, tool, args, extra = {}) =>
  execFileSync("/usr/bin/sudo", drop(u, tool, args), {
    encoding: "utf8",
    timeout: 10000,
    ...extra,
  });
function owned(p) {
  assert.ok(p === root || p.startsWith(root + "/"), p);
  assert.match(root, /^\/var\/lib\/fhf-[a-f0-9]{12}$/);
  return p;
}
async function waitUntil(fn, ms = 8000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (abort.signal.aborted) throw Error("fixture cancelled");
    const v = await fn();
    if (v) return v;
    await new Promise((r) => setTimeout(r, 20));
  }
  throw Error("bounded fixture wait timed out");
}
async function put(file, data, user, mode = "0600") {
  owned(file);
  const bytes = Buffer.isBuffer(data)
    ? data
    : Buffer.from(typeof data === "string" ? data : JSON.stringify(data));
  const from = path.join(stage, sha(Buffer.concat([Buffer.from(file), bytes])));
  await writeFile(from, bytes, { mode: 0o600 });
  sudo("/usr/bin/install", [
    "-m",
    mode,
    "-o",
    String(user.uid),
    "-g",
    String(user.gid),
    from,
    file,
  ]);
}
function directory(p, user, mode = "0700") {
  owned(p);
  sudo("/usr/bin/install", [
    "-d",
    "-m",
    mode,
    "-o",
    String(user?.uid ?? 0),
    "-g",
    String(user?.gid ?? 0),
    p,
  ]);
  createdPaths.push({ path: p, uid: user?.uid ?? 0 });
}
function processProof(pid, u) {
  assert.ok(Number.isSafeInteger(pid) && pid > 1);
  const stat = sudo("/usr/bin/cat", [`/proc/${pid}/stat`]);
  const start = stat.slice(stat.lastIndexOf(")") + 2).split(" ")[19];
  const status = sudo("/usr/bin/cat", [`/proc/${pid}/status`]);
  assert.match(
    status,
    new RegExp(`^Uid:\\s+${u.uid}\\s+${u.uid}\\s+${u.uid}\\s+${u.uid}$`, "m"),
  );
  for (const cap of ["CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"])
    assert.match(status, new RegExp(`^${cap}:\\s+0+$`, "m"));
  assert.match(status, /^NoNewPrivs:\s+1$/m);
  assert.equal(sudo("/usr/bin/readlink", [`/proc/${pid}/exe`]).trim(), binary);
  return {
    pid,
    uid: u.uid,
    start,
    executable: binary,
    capabilities: "all empty",
    noNewPrivileges: true,
  };
}
function start(u, args, { onEvent, input } = {}) {
  const child = spawn("/usr/bin/sudo", drop(u, binary, args), {
    stdio: ["pipe", "pipe", "pipe"],
  });
  const record = { child, u, events: [], proof: null, done: null, error: null };
  processes.push(record);
  let pending = "",
    err = "",
    queue = Promise.resolve();
  child.stdout.on("data", (data) => {
    pending += data.toString();
    let n;
    while ((n = pending.indexOf("\n")) >= 0) {
      const line = pending.slice(0, n);
      pending = pending.slice(n + 1);
      queue = queue
        .then(async () => {
          const event = JSON.parse(line);
          record.events.push(event);
          if (event.pid && !record.proof) {
            record.proof = processProof(event.pid, u);
            report.processes.push(record.proof);
          }
          if (onEvent) await onEvent(event, record);
        })
        .catch((e) => {
          record.error = e;
        });
    }
  });
  child.stderr.on("data", (d) => {
    err += d.toString();
    if (err.length > 65536) record.error = Error("fixture stderr bound");
  });
  child.stdin.end(input);
  record.done = new Promise((resolve) =>
    child.once("close", async (code, signal) => {
      await queue;
      log += `${u.name}: ${err}\n`;
      resolve({ code, signal, error: record.error });
    }),
  );
  return record;
}
async function finished(p, success = true) {
  const result = await Promise.race([
    p.done,
    new Promise((_, reject) => {
      const timer = setTimeout(
        () => reject(Error("fixture process timeout")),
        12000,
      );
      timer.unref();
    }),
  ]);
  if (result.error) throw result.error;
  if (success) assert.equal(result.code, 0, log);
  else assert.notEqual(result.code, 0, "negative fixture succeeded");
  return result;
}
async function stop(p, signal = "TERM") {
  if (!p || p.child.exitCode !== null || p.child.signalCode !== null) {
    if (p) await p.done;
    return;
  }
  if (!p.proof) {
    await waitUntil(() => p.proof || p.child.exitCode !== null);
    if (!p.proof) {
      await p.done;
      return;
    }
  }
  const proof = processProof(p.proof.pid, p.u);
  assert.equal(proof.start, p.proof.start, "PID identity changed");
  sudo("/usr/bin/kill", [`-${signal}`, String(proof.pid)]);
  await p.done;
}
function flags(c, role, action, initial = false) {
  return [
    role,
    action,
    "--disposable-fixture",
    "--config",
    c.config[role],
    "--config-sha256",
    c.pins[role],
    ...(initial ? ["--initial"] : []),
  ];
}
async function newCase(name, { fake = false } = {}) {
  const base = owned(
    `${root}/c${String(report.cases.length + 1).padStart(2, "0")}`,
  );
  directory(base, null, "0755");
  const experiment = randomBytes(16).toString("hex"),
    instance = randomBytes(16).toString("hex");
  const c = {
    name,
    base,
    config: {},
    pins: {},
    socket: `${base}/socket/owner.sock`,
    initial: {},
  };
  for (const role of ["owner", "signer", "app"]) {
    directory(
      `${base}/${role}`,
      users.find((u) => u.role === role),
    );
  }
  directory(`${base}/socket`, users[2], "0711");
  if (fake) directory(`${base}/fake`, users[1], "0711");
  for (const role of ["owner", "signer"]) {
    const u = users.find((x) => x.role === role),
      dir = `${base}/${role}`;
    directory(`${dir}/store`, u);
    directory(`${dir}/artifacts`, u);
    const seed = Buffer.from(JSON.stringify(bundle.seed)),
      policy = Buffer.from(JSON.stringify(bundle.policy)),
      ca = Buffer.from(bundle.ca, "base64");
    const config = {
      fixtureOnly: true,
      purpose: bundle.seed.purpose,
      repository: bundle.seed.repository,
      channel: "development",
      version: 1,
      profile: "fased-app-owner-signer-transport-fixture:v1",
      experiment,
      role,
      ownerUid: users[0].uid,
      appUid: users[1].uid,
      signerUid: users[2].uid,
      privateDir: dir,
      socket: fake && role === "owner" ? `${base}/fake/owner.sock` : c.socket,
      seedSha256: sha(seed),
      policySha256: sha(policy),
      caSha256: sha(ca),
    };
    c.config[role] = `${dir}/config.json`;
    c.pins[role] = sha(Buffer.from(JSON.stringify(config)));
    const files = {
      "config.json": config,
      "seed.json": seed,
      "policy.json": policy,
      "ca.pem": ca,
      "evidence.json": bundle.evidence,
      "clock.json": { now: bundle.now, confident: true },
      "financial-canary": `unopened originals/counters/holds ${role}`,
      "inventory.json": {
        instanceId: instance,
        authoritySha256: sha(Buffer.from("disposable epoch")),
        canarySha256: "",
        revision: "1",
        installedFloor: bundle.seed.installedFloor,
        reservedFloor: bundle.seed.reservedFloor,
        activeIntentId: "",
      },
      "anchor.json": {
        version: 1,
        profile: config.profile,
        experiment,
        configSha256: c.pins[role],
        head: "",
      },
    };
    for (const [n, b] of Object.entries(files)) await put(`${dir}/${n}`, b, u);
    for (const [n, b] of Object.entries(bundle.artifacts))
      await put(`${dir}/artifacts/${n}`, Buffer.from(b, "base64"), u);
  }
  c.before = canaries(c);
  report.cases.push({ name, base, before: c.before });
  return c;
}
function canaries(c) {
  return Object.fromEntries(
    ["owner", "signer"].map((role) => {
      const u = users.find((x) => x.role === role);
      return [
        role,
        sha(
          Buffer.from(
            as(u, "/usr/bin/cat", [`${c.base}/${role}/financial-canary`]),
          ),
        ),
      ];
    }),
  );
}
function checkpoint(c) {
  const u = users[2],
    dir = `${c.base}/signer/store`;
  const h = JSON.parse(as(u, "/usr/bin/cat", [`${dir}/head.json`]));
  const value = JSON.parse(
    as(u, "/usr/bin/cat", [`${dir}/${h.stateSha256}.json`]),
  );
  return { head: h.stateSha256, value };
}
function tree(c, role) {
  const u = users.find((x) => x.role === role);
  return sha(
    Buffer.from(
      as(u, "/usr/bin/find", [
        `${c.base}/${role}`,
        "-type",
        "f",
        "-exec",
        "/usr/bin/sha256sum",
        "--",
        "{}",
        "+",
      ]),
    ),
  );
}
async function serve(c) {
  const p = start(users[2], flags(c, "signer", "serve", !c.initial.signer));
  await waitUntil(
    () =>
      p.events.some((e) => e.event === "ready") || p.child.exitCode !== null,
  );
  assert.ok(
    p.events.some((e) => e.event === "ready"),
    log,
  );
  c.initial.signer = true;
  c.server = p;
  return p;
}
async function owner(
  c,
  { success = true, review = true, beforeAck, wait = true } = {},
) {
  const p = start(users[0], flags(c, "owner", "inspect", !c.initial.owner), {
    onEvent: async (e) => {
      if (e.event === "review") {
        assert.equal(e.review.review.UnitID, bundle.unitSha256);
        assert.deepEqual(e.review.review.Identity, bundle.identity);
        assert.equal(e.review.review.Subjects.length, 4);
        if (beforeAck) await beforeAck(e);
        if (review)
          await put(
            `${c.base}/owner/review-ack.json`,
            { exchangeDigest: e.review.exchangeDigest, approved: true },
            users[0],
          );
        else
          await put(
            `${c.base}/owner/review-ack.json`,
            { exchangeDigest: e.review.exchangeDigest, approved: false },
            users[0],
          );
      }
    },
  });
  c.initial.owner = true;
  if (wait) await finished(p, success);
  return p;
}
function completed(p) {
  const e = p.events.find((e) => e.event === "complete");
  assert.ok(e, "missing compiled completion");
  const o = e.report.outcome;
  assert.equal(o.outcome, "inspection-complete");
  for (const field of [
    "installAllowed",
    "resumeAllowed",
    "updaterEnabled",
    "walletSigner",
    "financialExecution",
  ])
    assert.equal(o[field], false);
  assert.equal(e.report.ownerUid, users[0].uid);
  assert.equal(e.report.signerUid, users[2].uid);
  return e.report;
}
async function check(id, fn) {
  await fn();
  report.checks[id] = "PASS";
  console.log(`${id}: PASS`);
}
async function audit(c, result) {
  const p = start(users[0], [
    ...flags(c, "owner", "status"),
    "--request-id",
    result.outcome.requestId,
    "--exchange-digest",
    result.exchangeDigest,
  ]);
  await finished(p);
  const e = p.events.find((e) => e.event === "audit");
  assert.equal(e?.historical, true);
  return e.record;
}
async function cleanupCase(c) {
  if (c.server) await stop(c.server);
  assert.deepEqual(canaries(c), c.before);
  const state = checkpoint(c);
  assert.equal(
    state.value.inventory.installedFloor,
    bundle.seed.installedFloor,
  );
  assert.equal(state.value.inventory.reservedFloor, bundle.seed.reservedFloor);
  const record = report.cases.find((x) => x.base === c.base);
  record.after = canaries(c);
  record.finalHead = state.head;
  record.pending = state.value.pending;
  record.recordKinds = state.value.records.map((x) => x.kind);
}
try {
  // One privilege prerequisite check. No owner-host use or interactive password.
  sudo("/usr/bin/true", []);
  for (const p of ["/var", "/var/lib"]) {
    const st = await lstat(p);
    assert.equal(st.uid, 0);
    assert.equal(st.mode & 0o022, 0);
  }
  assert.equal(
    await lstat(root).then(
      () => true,
      (e) => (e.code === "ENOENT" ? false : Promise.reject(e)),
    ),
    false,
  );
  directory(root, null, "0755");
  createdRoot = true;
  stage = await mkdtemp(path.join(tmpdir(), "fht-stage-"));
  for (const role of ["owner", "app", "signer"]) {
    const name = `fh-${role}-${token}`;
    sudo("/usr/sbin/useradd", [
      "--system",
      "--user-group",
      "--no-create-home",
      "--shell",
      "/usr/sbin/nologin",
      name,
    ]);
    const uid = +execFileSync("/usr/bin/id", ["-u", name], {
        encoding: "utf8",
      }).trim(),
      gid = +execFileSync("/usr/bin/id", ["-g", name], {
        encoding: "utf8",
      }).trim();
    assert.ok(uid > 0);
    users.push({ name, role, uid, gid });
  }
  assert.equal(new Set(users.map((u) => u.uid)).size, 3);
  report.identities = users;
  sudo("/usr/bin/install", [
    "-m",
    "0555",
    "-o",
    "0",
    "-g",
    "0",
    fixtureBinary,
    binary,
  ]);
  assert.equal(
    sudo("/usr/bin/sha256sum", ["--", binary]).split(" ")[0],
    report.binarySHA256,
  );
  exporter = spawn(
    helper,
    ["-test.run=^TestFixtureTransportIndependentPeers$", "-test.timeout=200s"],
    {
      env: {
        ...process.env,
        FASED_TRANSPORT_TEST_CHILD: "export",
        FASED_TRANSPORT_EXPORT: stage,
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  exporter.done = new Promise((r) => exporter.once("close", (code) => r(code)));
  exporter.stdout.on("data", (d) => {
    log += d.toString();
  });
  exporter.stderr.on("data", (d) => {
    log += d.toString();
  });
  bundle = await waitUntil(async () => {
    try {
      return JSON.parse(
        await readFile(path.join(stage, "bundle.json"), "utf8"),
      );
    } catch (e) {
      if (e.code !== "ENOENT" && !(e instanceof SyntaxError)) throw e;
      return false;
    }
  });
  report.publicInputs = {
    seedSHA256: sha(Buffer.from(JSON.stringify(bundle.seed))),
    policySHA256: sha(Buffer.from(JSON.stringify(bundle.policy))),
    unitSHA256: bundle.unitSha256,
  };
  const removeSocket = async (c) => {
    const st = await lstat(c.socket);
    assert.equal(st.uid, users[2].uid);
    assert.ok(st.isSocket());
    sudo("/usr/bin/rm", ["--", owned(c.socket)]);
  };
  await check("UT-01", async () => {
    const c = await newCase("separate compiled owner/signer");
    await serve(c);
    const result = completed(await owner(c));
    c.result = result;
    assert.notEqual(users[0].uid, users[2].uid);
    const history = await audit(c, result);
    assert.equal(history.exchangeDigest, result.exchangeDigest);
    report.cases.at(-1).result = result;
    await cleanupCase(c);
  });
  await check("UT-02", async () => {
    const c = await newCase(
      "actual app UID denied before challenge/status/store",
    );
    await serve(c);
    const before = { owner: tree(c, "owner"), signer: tree(c, "signer") };
    const file = `${c.base}/app/probe.json`;
    const config = {
      version: 1,
      profile: "fased-app-owner-signer-transport-fixture:v1",
      fixtureOnly: true,
      socket: c.socket,
      signerUid: users[2].uid,
    };
    await put(file, config, users[1]);
    const probe = start(
      users[1],
      [
        "probe",
        "raw",
        "--disposable-fixture",
        "--config",
        file,
        "--config-sha256",
        sha(Buffer.from(JSON.stringify(config))),
      ],
      { input: Buffer.alloc(0) },
    );
    await finished(probe);
    assert.equal(probe.events[0]?.bytes, 0);
    assert.equal(probe.events[0]?.uid, users[1].uid);
    for (const role of ["owner", "signer"]) {
      assert.throws(() =>
        as(users[1], "/usr/bin/cat", [`${c.base}/${role}/config.json`]),
      );
      assert.throws(() =>
        as(users[1], "/usr/bin/touch", [`${c.base}/${role}/app-injected`]),
      );
      assert.equal(tree(c, role), before[role]);
    }
    completed(await owner(c));
    await cleanupCase(c);
  });
  await check("UT-03", async () => {
    const c = await newCase("actual forged app signer kernel UID", {
      fake: true,
    });
    const fake = `${c.base}/fake/owner.sock`;
    const config = {
      version: 1,
      profile: "fased-app-owner-signer-transport-fixture:v1",
      fixtureOnly: true,
      socket: fake,
      signerUid: users[2].uid,
    };
    const file = `${c.base}/app/fake.json`;
    await put(file, config, users[1]);
    const p = start(users[1], [
      "probe",
      "fake-signer",
      "--disposable-fixture",
      "--config",
      file,
      "--config-sha256",
      sha(Buffer.from(JSON.stringify(config))),
    ]);
    await waitUntil(() => p.events.some((e) => e.event === "ready"));
    // Make path ownership look legitimate; the kernel still identifies the app.
    sudo("/usr/bin/chown", [
      `${users[2].uid}:${users[2].gid}`,
      owned(`${c.base}/fake`),
      owned(fake),
    ]);
    createdPaths.find((x) => x.path === `${c.base}/fake`).uid = users[2].uid;
    await owner(c, { success: false });
    await finished(p);
    assert.equal(
      p.events.find((e) => e.event === "fake-peer-denied")?.bytes,
      0,
    );
    assert.equal(p.proof.uid, users[1].uid);
    await serve(c);
    await cleanupCase(c);
  });
  await check("UT-04", async () => {
    const c = await newCase("bounded framing under actual owner UID");
    await serve(c);
    const before = checkpoint(c).head;
    const file = `${c.base}/owner/probe.json`;
    const config = {
      version: 1,
      profile: "fased-app-owner-signer-transport-fixture:v1",
      fixtureOnly: true,
      socket: c.socket,
      signerUid: users[2].uid,
    };
    await put(file, config, users[0]);
    const raw = [
      Buffer.from([0, 0, 0, 0]),
      Buffer.from([0, 1, 0, 1]),
      Buffer.from([0, 0, 0, 1, 123]),
    ];
    for (const data of raw) {
      const p = start(
        users[0],
        [
          "probe",
          "raw",
          "--disposable-fixture",
          "--config",
          file,
          "--config-sha256",
          sha(Buffer.from(JSON.stringify(config))),
        ],
        { input: data },
      );
      await finished(p);
      assert.equal(p.events[0]?.bytes, 0);
      assert.equal(checkpoint(c).head, before);
    }
    await cleanupCase(c);
  });
  await check("UT-05", async () => {
    const c = await newCase(
      "historical response cannot complete new connection",
    );
    await serve(c);
    const result = completed(await owner(c));
    assert.equal(result.liveRetryVerified, true);
    const before = checkpoint(c).head;
    const historical = await audit(c, result);
    assert.equal(historical.outcome.outcome, "checked");
    assert.equal(checkpoint(c).head, before);
    const wrong = start(users[0], [
      ...flags(c, "owner", "status"),
      "--request-id",
      result.outcome.requestId,
      "--exchange-digest",
      "0".repeat(64),
    ]);
    await finished(wrong, false);
    assert.equal(checkpoint(c).head, before);
    assert.ok(
      Object.keys(localProof.checks).some((x) =>
        x.includes("UT-05-live-exact-retry"),
      ),
      "coupled exact transport retry case absent",
    );
    await cleanupCase(c);
  });
  await check("UT-06", async () => {
    const c = await newCase("independent signer bytes and protected review");
    await serve(c);
    await put(
      `${c.base}/signer/artifacts/signer.fixture`,
      "FASED-INERT-ARTIFACT:v1\nsubstituted",
      users[2],
    );
    await owner(c, { success: false });
    const state = checkpoint(c);
    assert.ok(state.value.trustSha256);
    assert.equal(state.value.pending, "");
    await cleanupCase(c);
    const denied = await newCase("owner explicitly refuses fixture review");
    await serve(denied);
    await owner(denied, { success: false, review: false });
    assert.equal(checkpoint(denied).value.trustSha256, "");
    await cleanupCase(denied);
  });
  await check("UT-07", async () => {
    for (const [name, value] of [
      ["equal-expiry", { now: bundle.policy.notAfter, confident: true }],
      ["uncertain-time", { now: bundle.now, confident: false }],
    ]) {
      const c = await newCase(name);
      await serve(c);
      await put(`${c.base}/owner/clock.json`, value, users[0]);
      await owner(c, { success: false });
      await cleanupCase(c);
    }
  });
  const control = async (offset) => {
    for (const f of ["gate-hit.json", "release"])
      await rm(path.join(stage, f), { force: true });
    const count = JSON.parse(
      await readFile(path.join(stage, "timestamp-count.json"), "utf8"),
    );
    await save(path.join(stage, "control.json"), {
      pauseTimestamp: count + offset,
    });
  };
  await check("UT-08", async () => {
    const c = await newCase("signer restart after result before confirmation");
    await serve(c);
    const p = await owner(c, {
      success: false,
      wait: false,
      beforeAck: () => control(2),
    });
    await waitUntil(async () =>
      lstat(path.join(stage, "gate-hit.json")).then(
        () => true,
        (e) => (e.code === "ENOENT" ? false : Promise.reject(e)),
      ),
    );
    const durable = checkpoint(c).value.records.find(
      (r) => r.kind === "checked" && r.body?.outcome?.requestId,
    );
    assert.ok(durable);
    const boot = durable.body.outcome.signerBootId;
    await stop(c.server);
    await removeSocket(c);
    await serve(c);
    assert.notEqual(checkpoint(c).value.bootId, boot);
    await writeFile(path.join(stage, "release"), "");
    await finished(p, false);
    assert.ok(!p.events.some((e) => e.event === "complete"));
    const result = {
      outcome: durable.body.outcome,
      exchangeDigest: durable.body.exchangeDigest,
    };
    assert.equal((await audit(c, result)).outcome.signerBootId, boot);
    await cleanupCase(c);
  });
  await check("UT-09", async () => {
    const c = await newCase(
      "SIGKILL while independent signer learning pending",
    );
    await serve(c);
    const p = await owner(c, {
      success: false,
      wait: false,
      beforeAck: () => control(1),
    });
    await waitUntil(async () =>
      lstat(path.join(stage, "gate-hit.json")).then(
        () => true,
        (e) => (e.code === "ENOENT" ? false : Promise.reject(e)),
      ),
    );
    const pending = checkpoint(c).value.pending;
    assert.ok(pending);
    await stop(c.server, "KILL");
    await removeSocket(c);
    await serve(c);
    assert.equal(checkpoint(c).value.pending, pending);
    await writeFile(path.join(stage, "release"), "");
    await finished(p, false);
    await owner(c, { success: false });
    assert.equal(checkpoint(c).value.pending, pending);
    await cleanupCase(c);
  });
  await check("UT-10", async () => {
    const c = await newCase("corrupt external anchor blocks without reset");
    await serve(c);
    completed(await owner(c));
    const before = checkpoint(c).head;
    await put(`${c.base}/signer/anchor.json`, {}, users[2]);
    await owner(c, { success: false });
    assert.equal(checkpoint(c).head, before);
    await cleanupCase(c);
  });
  await check("UT-11", async () => {
    for (const args of [
      ["owner", "inspect"],
      ["install", "run", "--disposable-fixture"],
      ["signer", "serve", "--disposable-fixture", "--real-wallet"],
      ["owner", "inspect", "--disposable-fixture", "--automatic-updates"],
    ]) {
      const p = start(users[1], args);
      await finished(p, false);
    }
    assert.equal(localProof.installedExecution, false);
    assert.equal(localProof.realWallets, false);
    assert.equal(localProof.automaticUpdates, false);
  });
  await unchanged(inputs);
} finally {
  const errors = [];
  for (const p of processes.toReversed()) {
    try {
      await stop(p);
    } catch (e) {
      errors.push(String(e));
    }
  }
  if (exporter) {
    try {
      await writeFile(path.join(stage, "done"), "");
      const code = await exporter.done;
      assert.equal(code, 0, log);
    } catch (e) {
      errors.push(String(e));
    }
  }
  if (createdRoot && !errors.length) {
    try {
      for (const p of createdPaths) {
        const st = await lstat(p.path);
        assert.equal(st.uid, p.uid);
        assert.ok(st.isDirectory());
        assert.ok(!st.isSymbolicLink());
      }
      assert.equal((await lstat(root)).uid, 0);
      sudo("/usr/bin/rm", ["-r", "--", owned(root)]);
      assert.equal(
        await lstat(root).then(
          () => true,
          (e) => (e.code === "ENOENT" ? false : Promise.reject(e)),
        ),
        false,
      );
    } catch (e) {
      errors.push(String(e));
    }
  }
  if (!errors.length) {
    for (const u of users.toReversed()) {
      try {
        assert.equal(
          +execFileSync("/usr/bin/id", ["-u", u.name], {
            encoding: "utf8",
          }).trim(),
          u.uid,
        );
        sudo("/usr/sbin/userdel", [u.name]);
        const r = spawnSync("/usr/bin/getent", ["passwd", u.name]);
        assert.equal(r.status, 2);
      } catch (e) {
        errors.push(String(e));
      }
    }
  }
  if (stage && !errors.length) {
    assert.ok(stage.startsWith(path.join(tmpdir(), "fht-stage-")));
    await rm(stage, { recursive: true });
  }
  report.cleanup = errors.length
    ? errors
    : "PASS: exact child identities, sockets, disposable inputs/state/keys and newly created UIDs removed; no installed/owner resources touched";
  if (!errors.length && Object.keys(report.checks).length === 11) {
    report.checks["UT-12"] = "PASS";
    report.result = "PASS";
  }
  report.retainedResources = errors.length ? { root, stage, users } : [];
  await writeFile(path.join(evidence, "uid.log"), log);
  await save(path.join(evidence, "uid-result.json"), report);
  if (errors.length)
    throw Error(`UID fixture cleanup failed: ${errors.join("; ")}`);
}
assert.equal(report.result, "PASS");
console.log(
  `Compiled owner/signer UID boundary: PASS (${Object.keys(report.checks).length} cases)`,
);
