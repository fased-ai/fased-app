import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { once } from "node:events";
import {
  readFile,
  writeFile,
  mkdtemp,
  readdir,
  rm,
  stat,
  symlink,
  unlink,
} from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  validateInstalledTradePlan,
  verifyInstalledTradeReceiver,
  installedNativeTradeJourney,
  verifyPreservedOriginal,
  remoteInstalledCommand,
  remoteHeldStatus,
  remoteBrowserControl,
  allowedOwnerLink,
  createInstalledTradeReceiptWriter,
  withInstalledTradeLock,
} from "./fixture-installed-trade-journey.mjs";
const sha = (x) => createHash("sha256").update(x).digest("hex");
const h = "a".repeat(64),
  id = "a".repeat(32),
  accountId = "b".repeat(32),
  instance = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI";
const plan = () => ({
  version: 1,
  source: "c".repeat(40),
  profile: "local",
  ownerUid: 1800,
  clientUid: 1800,
  instance,
  data: "/home/disposable/.config/fased-app",
  manifestSha256: h,
  enrollmentSha256: h,
  coreSha256: h,
  signerSha256: h,
  native: { path: "/home/disposable/aligned/fased-desktop", sha256: h },
  origin: "",
  remoteUser: "",
  accountId,
  accountName: "Trade",
  privateFile: "/home/disposable/private.json",
  receipt: "/home/disposable/.evidence/installed-native-onchain-result.json",
  mainFrame: "Fased App",
  preserve: {
    ownerUid: 1400,
    manifestSha256: h,
    before: "/home/disposable/.evidence/before.json",
    beforeSha256: h,
    after: "/home/disposable/.evidence/after.json",
  },
  packageReceipt: "/home/disposable/.evidence/build.json",
  packageReceiptSha256: h,
});
const ref = {
  version: 3,
  signerId: id,
  walletId: "d".repeat(32),
  epoch: "e".repeat(32),
  network: "devnet",
  publicKey: "f".repeat(64),
};
function receiver() {
  return {
    uid: 1800,
    manifestSha256: h,
    enrollmentSha256: h,
    preservedManifestSha256: h,
    coreSha256: h,
    signerSha256: h,
    peer: {
      ready: true,
      ownerUid: 1800,
      instanceId: instance,
      signerId: id,
      network: "devnet",
    },
    enrollment: { instanceId: instance },
    manifest: {
      root: "/var/lib/fased-app-custody-1800",
      owner: 1800,
      version: 3,
      location: "local",
      enrollment: true,
      nativeOwnerAccess: true,
      mode: "ordinary-custody:v1",
      profile: "owner-authorized:v1",
      provenance: "controlled-disposable:v1",
      coreHash: h,
      signerHash: h,
      network: "devnet",
      id,
      app: { uid: 1801 },
      signer: { uid: 1802 },
    },
    accounts: {
      setup: { instanceId: instance, source: "local" },
      fixture: {
        status: {
          custody: {
            origin: "generated",
            profile: "owner-authorized:v1",
            provenance: "controlled-disposable:v1",
          },
          fixtureOnly: true,
          admissionHeld: true,
          recoveryRequired: true,
          swapReady: true,
          walletSigner: false,
          financialExecution: false,
          updateHeld: false,
          locked: true,
          account: ref,
        },
      },
      record: {
        accounts: [
          {
            id: accountId,
            name: "Trade",
            version: 4,
            mode: "managed",
            network: "devnet",
            rpc: {
              version: 2,
              endpoint: "https://devnet.helius-rpc.com",
              network: "devnet",
            },
            signer: ref,
          },
        ],
      },
    },
  };
}
function fixture() {
  const p = plan(),
    r = receiver(),
    events = [],
    saved = [];
  let draft = null,
    submitted = false,
    locked = false;
  const q = {
    version: "ordinary-controlled-swap-v1",
    fixtureOnly: true,
    digest: "1".repeat(64),
    account: ref,
    quote: {
      id: "2".repeat(32),
      instanceId: instance,
      accountId,
      profileId: "trading-analyst",
      network: "devnet",
      owner: "publicOwner",
      amount: "12500000",
      output: "100000000",
      minimumOutput: "99500000",
      inputMint: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
      outputMint: "So11111111111111111111111111111111111111112",
      slippageBps: 50,
      maxFeeLamports: "5000",
      rentLamports: "0",
      createdAt: Math.floor(Date.now() / 1000),
      expiresAt: Math.floor(Date.now() / 1000) + 45,
    },
  };
  const intent = {
    version: 2,
    instanceId: instance,
    action: "approve-swap",
    network: "devnet",
    operationId: q.quote.id,
    proposal: Buffer.from(JSON.stringify(q)).toString("base64url"),
  };
  const state = () => ({
    revision: 1,
    providerCallsEnabled: false,
    financialExecution: false,
    onchain: {
      available: true,
      tasks: submitted
        ? [{ id: "onchain-task:" + q.quote.id, swap: { proposal: q } }]
        : [],
      runs: submitted
        ? [
            {
              taskId: "onchain-task:" + q.quote.id,
              id: "onchain-task:" + q.quote.id + ":1",
              swap: {
                state: "confirmed",
                signature: "one-original",
                signed: "signed-original",
              },
            },
          ]
        : [],
    },
  });
  const io = {
    kind: "source-mocked-not-installed",
    preflight: async () => r,
    read: async () => state(),
    runtime: async () => ({ app: "100", signer: "101", boot: "same" }),
    draft: async () => draft,
    unlock: async () => {
      events.push("private-input");
      return "disposable fixture password";
    },
    native: async (step) => {
      events.push(step.action + ":" + step.name);
      if (step.name === "Approve" && step.frame === "Fased App")
        draft = { intent, origin: p.origin, dispatched: false };
      if (step.name === "Submit") submitted = true;
    },
    lock: async () => {
      locked = true;
      events.push("lock");
    },
    preserved: async () => {
      events.push("preserved");
      return h;
    },
    inspect: async () => ({
      readOnly: true,
      status: { locked, recoveryRequired: true },
      swap: {
        state: "confirmed",
        proposal: q,
        signature: "one-original",
        signed: "signed-original",
      },
    }),
    change: async () => {
      throw Error("no CLI approval/submission shortcut allowed");
    },
  };
  return {
    p,
    r,
    io,
    q,
    intent,
    events,
    saved,
    save: async (value) => saved.push(structuredClone(value)),
    setSubmitted: () => {
      submitted = true;
      locked = true;
    },
  };
}
function remoteFixture() {
  const f = fixture();
  Object.assign(f.p, {
    profile: "remote",
    clientUid: 1000,
    origin: "https://fixture.tail123.ts.net",
    remoteUser: "fixture-owner",
  });
  Object.assign(f.r.manifest, {
    location: "remote",
    publicOrigin: f.p.origin,
    nativeOwnerAccess: false,
  });
  f.r.accounts.setup.source = "remote";
  const read = f.io.read;
  f.io.read = async () => {
    const value = await read();
    delete value.financialExecution;
    delete value.providerCallsEnabled;
    return value;
  };
  f.io.heldStatus = async () => ({
    instanceId: f.p.instance,
    profileId: "trading-analyst",
    revision: 1,
    financialExecution: false,
    providerCallsEnabled: false,
  });
  return f;
}

test("whole-journey lock rejects a concurrent runner before private input or Submit", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "fased-runner-lock-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const first = remoteFixture(),
    second = remoteFixture();
  let enter, proceed;
  const entered = new Promise((resolve) => (enter = resolve));
  const gate = new Promise((resolve) => (proceed = resolve));
  const running = withInstalledTradeLock(dir, async () => {
    enter();
    await gate;
    return installedNativeTradeJourney(
      first.p,
      first.io,
      undefined,
      first.save,
    );
  });
  await entered;
  try {
    await assert.rejects(
      withInstalledTradeLock(dir, () =>
        installedNativeTradeJourney(
          second.p,
          second.io,
          undefined,
          second.save,
        ),
      ),
      /another.*running/i,
    );
    assert.deepEqual(second.events, []);
    assert.deepEqual(second.saved, []);
  } finally {
    proceed();
  }
  assert.equal((await running).result, "NEEDS_RESTART");
  assert.equal(first.events.filter((x) => x === "click:Submit").length, 1);
  assert.equal(
    await withInstalledTradeLock(dir, async () => "reacquired"),
    "reacquired",
  );
});

test("whole-journey OS lock is released when its holder dies", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "fased-runner-lock-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const module = new URL(
    "./fixture-installed-trade-journey.mjs",
    import.meta.url,
  ).href;
  const script = `import { withInstalledTradeLock } from ${JSON.stringify(module)}; await withInstalledTradeLock(process.argv[1], async () => { console.log("READY"); await new Promise(() => setInterval(() => global.gc(), 10)); });`;
  const child = spawn(
    process.execPath,
    ["--expose-gc", "--input-type=module", "-e", script, dir],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  t.after(() => {
    if (child.exitCode === null && child.signalCode === null)
      child.kill("SIGKILL");
  });
  const [ready] = await Promise.race([
    once(child.stdout, "data"),
    once(child, "exit").then(([code]) => {
      throw new Error(`lock holder exited before readiness: ${code}`);
    }),
    new Promise((_, reject) => {
      const timer = setTimeout(
        () => reject(new Error("lock holder did not become ready")),
        5000,
      );
      timer.unref();
    }),
  ]);
  assert.match(String(ready), /READY/);
  // Force collection while the holder is alive; an orphaned async chain must
  // not cause Node's FileHandle finalizer to drop the OS lock.
  await new Promise((resolve) => setTimeout(resolve, 100));
  assert.equal(child.exitCode, null, "lock holder exited before contention");
  const directory = await stat(dir);
  const snapshot = async (pid) => {
    const descriptors = [];
    let entries;
    try {
      entries = await readdir(`/proc/${pid}/fd`);
    } catch (error) {
      if (error.code === "ENOENT") return { pid, exited: true };
      throw error;
    }
    for (const fd of entries) {
      try {
        const file = await stat(`/proc/${pid}/fd/${fd}`);
        if (file.dev === directory.dev && file.ino === directory.ino) {
          const info = await readFile(`/proc/${pid}/fdinfo/${fd}`, "utf8");
          descriptors.push({ fd, locks: info.match(/^lock:.*$/gm) ?? [] });
        }
      } catch (error) {
        if (error.code !== "ENOENT") throw error;
      }
    }
    return { pid, descriptors };
  };
  t.diagnostic(`lock holder: ${JSON.stringify(await snapshot(child.pid))}`);
  await assert.rejects(
    withInstalledTradeLock(dir, async () => {
      t.diagnostic(
        `unexpected overlap: ${JSON.stringify({ holder: await snapshot(child.pid), contender: await snapshot(process.pid) })}`,
      );
    }),
    /another.*running/i,
  );
  const exited = once(child, "exit");
  child.kill("SIGKILL");
  await exited;
  assert.equal(
    await withInstalledTradeLock(dir, async () => "recovered"),
    "recovered",
  );
});

test("real receipt writer resumes past interrupted staging and preserves the exact original", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "fased-receipt-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const f = remoteFixture();
  f.p.receipt = join(dir, "receipt.json");
  const previous = {
    version: 1,
    planHash: sha(JSON.stringify(f.p)),
    source: f.p.source,
    profile: "remote",
    instance: f.p.instance,
    manifestSha256: f.p.manifestSha256,
    enrollmentSha256: f.p.enrollmentSha256,
    class: f.io.kind,
    result: "OPEN",
    startedAt: Date.now() - 1000,
    before: { app: "100", signer: "101", boot: "same" },
  };
  const original = JSON.stringify(previous, null, 2) + "\n";
  const digest = sha(JSON.stringify(previous));
  const sidecar = f.p.receipt + "." + digest + ".pre-review-failure.json";
  await writeFile(f.p.receipt, original, { mode: 0o600 });
  await writeFile(f.p.receipt + ".next", "interrupted original staging", {
    mode: 0o600,
  });
  await writeFile(sidecar, original, { mode: 0o600 });
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    previous,
    createInstalledTradeReceiptWriter(f.p, digest),
    { resumePreReview: digest },
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.equal(
    JSON.parse(await readFile(f.p.receipt, "utf8")).result,
    "NEEDS_RESTART",
  );
  assert.equal(await readFile(sidecar, "utf8"), original);
  assert.equal(
    await readFile(f.p.receipt + ".next", "utf8"),
    "interrupted original staging",
  );
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
});

test("real receipt writer refuses replaced or symlinked ancestry", async (t) => {
  const dir = await mkdtemp(join(tmpdir(), "fased-receipt-"));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const f = remoteFixture();
  f.p.receipt = join(dir, "receipt.json");
  const previous = { result: "OPEN", instance: f.p.instance };
  const original = JSON.stringify(previous, null, 2) + "\n";
  const digest = sha(JSON.stringify(previous));
  const sidecar = f.p.receipt + "." + digest + ".pre-review-failure.json";
  await writeFile(f.p.receipt, original, { mode: 0o600 });
  await writeFile(sidecar, "not the original", { mode: 0o600 });
  await assert.rejects(
    createInstalledTradeReceiptWriter(f.p, digest)({ result: "OPEN" }),
  );
  assert.equal(await readFile(f.p.receipt, "utf8"), original);
  await unlink(sidecar);
  await symlink(f.p.receipt, sidecar);
  await assert.rejects(
    createInstalledTradeReceiptWriter(f.p, digest)({ result: "OPEN" }),
  );
  assert.equal(await readFile(f.p.receipt, "utf8"), original);
});

test("Remote journey uses scoped CLI, browser status and native owner before one submission", async () => {
  const f = remoteFixture();
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    undefined,
    f.save,
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
  assert.ok(
    f.events.indexOf("set:OS account") <
      f.events.indexOf("click:Connect wallet"),
  );

  assert.ok(
    f.events.indexOf("present:" + f.q.digest) <
      f.events.indexOf("private-input"),
  );
  f.io.runtime = async () => ({ app: "200", signer: "201", boot: "same" });
  f.events.length = 0;
  const after = await installedNativeTradeJourney(f.p, f.io, result, f.save);
  assert.equal(after.result, "PASS");
  assert.ok(
    !f.events.some((x) => /Quote|Approve|Submit|private-input/.test(x)),
  );
});

test("Remote browser permits only a bound native owner intent", () => {
  const f = remoteFixture();
  const q = new URLSearchParams({
    action: "approve-swap",
    instance: f.p.instance,
    network: "devnet",
    origin: f.p.origin,
    proposal: "abc_DEF-123",
  });
  const link = "fased://wallet-owner?" + q.toString();
  assert.equal(allowedOwnerLink(f.p, link), true);
  for (const [key, value] of [
    ["instance", "foreign"],
    ["origin", "https://other.tail123.ts.net"],
    ["network", "foreign"],
    ["proposal", "bad.secret"],
    ["action", "setup"],
  ]) {
    const bad = new URLSearchParams(q);
    bad.set(key, value);
    assert.equal(
      allowedOwnerLink(f.p, "fased://wallet-owner?" + bad.toString()),
      false,
    );
  }
  const duplicate = new URLSearchParams(q);
  duplicate.append("instance", f.p.instance);
  assert.equal(
    allowedOwnerLink(f.p, "fased://wallet-owner?" + duplicate.toString()),
    false,
  );
});

test("Remote held-feature status must be authenticated, matching and disabled before UI or secret", async () => {
  for (const field of [
    "instanceId",
    "profileId",
    "revision",
    "financialExecution",
    "providerCallsEnabled",
  ]) {
    const f = remoteFixture();
    const held = await f.io.heldStatus();
    held[field] =
      field === "revision" ? 2 : field.endsWith("Id") ? "foreign" : true;
    f.io.heldStatus = async () => held;
    await assert.rejects(
      installedNativeTradeJourney(f.p, f.io, undefined, f.save),
    );
    assert.ok(
      !f.events.some((x) => /Quote|Approve|Submit|private-input/.test(x)),
    );
  }
});

test("interrupted Remote submission inspects the original and never submits again", async () => {
  const f = remoteFixture();
  f.setSubmitted();
  const previous = {
    version: 1,
    planHash: sha(JSON.stringify(f.p)),
    source: f.p.source,
    profile: "remote",
    instance: f.p.instance,
    manifestSha256: f.p.manifestSha256,
    enrollmentSha256: f.p.enrollmentSha256,
    class: f.io.kind,
    result: "OPEN",
    stage: "submitted",
    proposal: f.q,
    intent: f.intent,
    before: { app: "100", signer: "101", boot: "same" },
    taskId: "onchain-task:" + f.q.quote.id,
    startedAt: Date.now() - 1000,
  };
  const pending = await installedNativeTradeJourney(
    f.p,
    f.io,
    previous,
    f.save,
  );
  assert.equal(pending.result, "NEEDS_RESTART");
  assert.ok(
    !f.events.some((x) => /Quote|Approve|Submit|private-input/.test(x)),
  );
  f.io.runtime = async () => ({ app: "200", signer: "201", boot: "same" });
  const after = await installedNativeTradeJourney(f.p, f.io, pending, f.save);
  assert.equal(after.result, "PASS");
  assert.ok(
    !f.events.some((x) => /Quote|Approve|Submit|private-input/.test(x)),
  );
});

test("hard interruption before review holds its original receipt and permits only explicit empty recovery", async () => {
  const f = remoteFixture();
  const previous = {
    version: 1,
    planHash: sha(JSON.stringify(f.p)),
    source: f.p.source,
    profile: "remote",
    instance: f.p.instance,
    manifestSha256: f.p.manifestSha256,
    enrollmentSha256: f.p.enrollmentSha256,
    class: f.io.kind,
    result: "OPEN",
    startedAt: Date.now() - 1000,
    before: { app: "100", signer: "101", boot: "same" },
  };
  const held = await installedNativeTradeJourney(f.p, f.io, previous, f.save);
  assert.equal(held.result, "RECOVERY_REQUIRED");
  assert.deepEqual(f.saved, []);
  assert.deepEqual(f.events, []);
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    previous,
    f.save,
    {
      resumePreReview: sha(JSON.stringify(previous)),
    },
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.deepEqual(result.preReviewFailure.receipt, previous);
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
});

test("hard interruption immediately after resumed pre-review save can recover its exact ancestry", async () => {
  const f = remoteFixture();
  const native = f.io.native;
  f.io.native = async () => {
    throw new Error("first pre-review interruption");
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  const original = structuredClone(f.saved.at(-1));
  f.io.native = native;
  f.events.length = 0;
  let interrupted;
  await assert.rejects(
    installedNativeTradeJourney(
      f.p,
      f.io,
      original,
      async (receipt) => {
        interrupted = structuredClone(receipt);
        throw new Error("simulated process loss after receipt save");
      },
      { resumePreReview: sha(JSON.stringify(original)) },
    ),
  );
  assert.equal(interrupted.result, "OPEN");
  assert.equal(interrupted.stage, undefined);
  assert.deepEqual(interrupted.preReviewFailure.receipt, original);
  assert.deepEqual(f.events, []);
  const held = await installedNativeTradeJourney(
    f.p,
    f.io,
    interrupted,
    f.save,
  );
  assert.equal(held.result, "RECOVERY_REQUIRED");
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    interrupted,
    f.save,
    {
      resumePreReview: sha(JSON.stringify(interrupted)),
    },
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.deepEqual(result.preReviewFailure.receipt, interrupted);
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
});

test("repeated interruption rechecks a resolved reviewed ancestor before resuming", async () => {
  for (const approved of [true, false]) {
    const f = remoteFixture(),
      native = f.io.native;
    f.io.native = async (step) => {
      if (step.name === f.q.digest) throw new Error("review interrupted");
      return native(step);
    };
    await assert.rejects(
      installedNativeTradeJourney(f.p, f.io, undefined, f.save),
    );
    const original = structuredClone(f.saved.at(-1));
    assert.equal(original.stage, "reviewed");
    original.proposal.quote.id = "9".repeat(32);
    original.proposal.quote.createdAt = 0;
    original.proposal.quote.expiresAt = 1;
    original.intent.operationId = original.proposal.quote.id;
    original.intent.proposal = Buffer.from(
      JSON.stringify(original.proposal),
    ).toString("base64url");
    const interrupted = structuredClone(original);
    delete interrupted.stage;
    delete interrupted.intent;
    delete interrupted.proposal;
    delete interrupted.failure;
    interrupted.result = "OPEN";
    interrupted.preReviewFailure = {
      sha256: sha(JSON.stringify(original)),
      receipt: original,
    };
    interrupted.resolvedUnsentDraft = {
      sha256: "a".repeat(64),
      operationId: original.intent.operationId,
    };
    let launched = false;
    f.io.draft = async () =>
      launched
        ? { intent: f.intent, origin: f.p.origin, dispatched: false }
        : null;
    f.io.native = async (step) => {
      if (step.name === "Approve" && step.frame === f.p.mainFrame)
        launched = true;
      return native(step);
    };
    const inspect = f.io.inspect;
    f.io.inspect = async (intent, ...args) =>
      intent.proposal === original.intent.proposal
        ? {
            readOnly: true,
            status: { locked: true },
            swap: approved ? { state: "approved" } : null,
          }
        : inspect(intent, ...args);
    f.events.length = 0;
    const run = () =>
      installedNativeTradeJourney(f.p, f.io, interrupted, f.save, {
        resumePreReview: sha(JSON.stringify(interrupted)),
      });
    if (approved) {
      await assert.rejects(run(), /ancestor signer approval must be absent/);
      assert.deepEqual(f.events, []);
    } else {
      const result = await run();
      assert.equal(result.result, "NEEDS_RESTART");
      assert.deepEqual(result.preReviewFailure.receipt, interrupted);
      assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
    }
  }
});

test("hard interruption with a draft requires exact expired unsigned original before retry", async () => {
  const f = remoteFixture();
  const previous = {
    version: 1,
    planHash: sha(JSON.stringify(f.p)),
    source: f.p.source,
    profile: "remote",
    instance: f.p.instance,
    manifestSha256: f.p.manifestSha256,
    enrollmentSha256: f.p.enrollmentSha256,
    class: f.io.kind,
    result: "OPEN",
    startedAt: Date.now() - 1000,
    before: { app: "100", signer: "101", boot: "same" },
  };
  const oldQ = structuredClone(f.q);
  oldQ.quote.id = "9".repeat(32);
  oldQ.quote.createdAt = 0;
  oldQ.quote.expiresAt = 1;
  const old = {
    intent: {
      ...f.intent,
      operationId: oldQ.quote.id,
      proposal: Buffer.from(JSON.stringify(oldQ)).toString("base64url"),
    },
    origin: f.p.origin,
    dispatched: false,
    completed: true,
  };
  f.io.draft = async () => old;
  f.io.inspect = async () => ({
    readOnly: true,
    status: { locked: true },
    swap: null,
  });
  const held = await installedNativeTradeJourney(f.p, f.io, previous, f.save);
  assert.equal(held.result, "RECOVERY_REQUIRED");
  assert.equal(held.draftSha256, sha(JSON.stringify(old)));
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, previous, f.save, {
      resumePreReview: sha(JSON.stringify(previous)),
    }),
  );
  assert.deepEqual(f.events, []);
  const native = f.io.native;
  let launched = false;
  f.io.draft = async () =>
    launched
      ? { intent: f.intent, origin: f.p.origin, dispatched: false }
      : old;
  f.io.native = async (step) => {
    if (step.name === "Approve" && step.frame === f.p.mainFrame)
      launched = true;
    return native(step);
  };
  const inspect = f.io.inspect;
  f.io.inspect = async (intent) =>
    intent.proposal === old.intent.proposal
      ? inspect(intent)
      : {
          readOnly: true,
          status: { locked: true, recoveryRequired: true },
          swap: {
            state: "confirmed",
            proposal: f.q,
            signature: "one-original",
            signed: "signed-original",
          },
        };
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    previous,
    f.save,
    {
      resumePreReview: sha(JSON.stringify(previous)),
      resolvedDraftSha256: sha(JSON.stringify(old)),
    },
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.deepEqual(result.preReviewFailure.receipt, previous);
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
});

test("hard-interruption recovery refuses foreign, active, approved or duplicate work", async () => {
  for (const failure of [
    "receipt",
    "draft-digest",
    "foreign-quote",
    "unfinished",
    "dispatched",
    "unexpired",
    "approved",
    "existing-task",
    "unlocked",
  ]) {
    const f = remoteFixture();
    const previous = {
      version: 1,
      planHash: sha(JSON.stringify(f.p)),
      source: f.p.source,
      profile: "remote",
      instance: f.p.instance,
      manifestSha256: f.p.manifestSha256,
      enrollmentSha256: f.p.enrollmentSha256,
      class: f.io.kind,
      result: "OPEN",
      startedAt: Date.now() - 1000,
      before: { app: "100", signer: "101", boot: "same" },
    };
    const q = structuredClone(f.q);
    q.quote.id = "9".repeat(32);
    q.quote.createdAt = 0;
    q.quote.expiresAt = 1;
    if (failure === "foreign-quote") q.quote.instanceId = "foreign";
    if (failure === "unexpired") {
      q.quote.createdAt = Math.floor(Date.now() / 1000);
      q.quote.expiresAt = q.quote.createdAt + 30;
    }
    const old = {
      intent: {
        ...f.intent,
        operationId: q.quote.id,
        proposal: Buffer.from(JSON.stringify(q)).toString("base64url"),
      },
      origin: f.p.origin,
      dispatched: failure === "dispatched",
      completed: failure !== "unfinished",
    };
    f.io.draft = async () => old;
    f.io.inspect = async () => ({
      readOnly: true,
      status: { locked: true },
      swap: failure === "approved" ? { state: "approved" } : null,
    });
    if (failure === "receipt") previous.instance = "foreign";
    if (failure === "unlocked") f.r.accounts.fixture.status.locked = false;
    if (failure === "existing-task") {
      const read = f.io.read;
      f.io.read = async () => {
        const value = await read();
        value.onchain.tasks.push({ id: "existing" });
        return value;
      };
    }
    await assert.rejects(
      installedNativeTradeJourney(f.p, f.io, previous, f.save, {
        resumePreReview: sha(JSON.stringify(previous)),
        resolvedDraftSha256:
          failure === "draft-digest"
            ? "f".repeat(64)
            : sha(JSON.stringify(old)),
      }),
      failure,
    );
    assert.deepEqual(f.events, [], failure);
    assert.deepEqual(f.saved, [], failure);
  }
});

test("interrupted original approval or uncertain submission is retained when no result exists", async () => {
  for (const stage of ["reviewed", "approved", "submitted"]) {
    const f = remoteFixture();
    f.io.inspect = async () => ({
      readOnly: true,
      status: { locked: true, recoveryRequired: true },
      swap: { state: "approved", proposal: f.q },
    });
    const previous = {
      version: 1,
      planHash: sha(JSON.stringify(f.p)),
      source: f.p.source,
      profile: "remote",
      instance: f.p.instance,
      manifestSha256: f.p.manifestSha256,
      enrollmentSha256: f.p.enrollmentSha256,
      class: f.io.kind,
      result: "OPEN",
      stage,
      proposal: f.q,
      intent: f.intent,
      before: { app: "100", signer: "101", boot: "same" },
      ...(stage === "submitted"
        ? { taskId: "onchain-task:" + f.q.quote.id }
        : {}),
    };
    const original = structuredClone(previous);
    const result = await installedNativeTradeJourney(
      f.p,
      f.io,
      previous,
      f.save,
    );
    assert.equal(result.result, "RECOVERY_REQUIRED");
    assert.deepEqual(previous, original);
    assert.deepEqual(f.saved, []);
    assert.ok(
      !f.events.some((x) => /Quote|Approve|Submit|private-input/.test(x)),
    );
  }
});

test("interrupted matching signer is locked before inspection; foreign receipt cannot lock it", async () => {
  for (const foreign of [false, true]) {
    const f = remoteFixture();
    f.r.accounts.fixture.status.locked = false;
    let locked = false;
    f.io.lock = async () => {
      locked = true;
      f.events.push("lock");
    };
    f.io.inspect = async () => ({
      readOnly: true,
      status: { locked, recoveryRequired: true },
      swap: { state: "approved", proposal: f.q },
    });
    const previous = {
      version: 1,
      planHash: foreign ? "f".repeat(64) : sha(JSON.stringify(f.p)),
      source: f.p.source,
      profile: "remote",
      instance: f.p.instance,
      manifestSha256: f.p.manifestSha256,
      enrollmentSha256: f.p.enrollmentSha256,
      class: f.io.kind,
      result: "OPEN",
      stage: "approved",
      proposal: f.q,
      intent: f.intent,
      before: { app: "100", signer: "101", boot: "same" },
    };
    if (foreign) {
      await assert.rejects(
        installedNativeTradeJourney(f.p, f.io, previous, f.save),
      );
      assert.equal(locked, false);
    } else {
      const result = await installedNativeTradeJourney(
        f.p,
        f.io,
        previous,
        f.save,
      );
      assert.equal(result.result, "RECOVERY_REQUIRED");
      assert.equal(locked, true);
      assert.deepEqual(f.saved, []);
    }
    assert.ok(
      !f.events.some((x) => /Quote|Approve|Submit|private-input/.test(x)),
    );
  }
});
test("frozen plan and genuine receiver reject originals, drift and authority before timers", () => {
  const p = plan();
  validateInstalledTradePlan(p);
  verifyInstalledTradeReceiver(p, receiver());
  for (const change of [
    (x) => (x.ownerUid = 1400),
    (x) => (x.origin = "https://public.example"),
    (x) => (x.data = "/home/../original"),
    (x) => (x.unlock = "secret"),
    (x) => (x.privateFile = "relative"),
    (x) => (x.native.sha256 = "missing"),
  ]) {
    const bad = structuredClone(p);
    change(bad);
    assert.throws(() => validateInstalledTradePlan(bad));
  }
  for (const change of [
    (x) => (x.accounts.fixture.status.custody.origin = "imported"),
    (x) => (x.accounts.fixture.status.swapReady = false),
    (x) => (x.accounts.fixture.status.financialExecution = true),
    (x) => (x.manifest.owner = 1400),
    (x) => (x.peer.ready = false),
    (x) => (x.peer.instanceId = "foreign"),
    (x) => (x.coreSha256 = "f".repeat(64)),
    (x) =>
      (x.accounts.record.accounts[0].signer = { ...ref, epoch: "foreign" }),
  ]) {
    const bad = receiver();
    change(bad);
    assert.throws(() => verifyInstalledTradeReceiver(p, bad));
  }
  assert.throws(() =>
    validateInstalledTradePlan({ ...plan(), profile: "remote" }),
  );
});

test("Remote plan and installed command pin owner, host, instance and fixed operations", () => {
  const f = remoteFixture();
  const { root } = validateInstalledTradePlan(f.p);
  const isolated = structuredClone(f.p);
  isolated.origin = "https://fixture.tail123.ts.net:8443";
  validateInstalledTradePlan(isolated);
  assert.ok(
    remoteInstalledCommand(isolated, "/usr/bin/cat", [
      root + "/install.json",
    ]).args.includes("fixture.tail123.ts.net"),
  );
  for (const [file, command] of [
    [
      root + "/fased-app",
      ["custody", "accounts", "--disposable-fixture", "--prepare-host"],
    ],
    [
      root + "/fased-app",
      [
        "custody",
        "agents",
        "--disposable-fixture",
        "--prepare-host",
        "--profile",
        "trading-analyst",
      ],
    ],
    [
      root + "/fased-app",
      [
        "custody",
        "window",
        "--expected-instance",
        f.p.instance,
        "--network",
        "devnet",
        "--disposable-fixture",
        "--inspect",
      ],
    ],
    [
      root + "/fased-signer",
      [
        "owner",
        "--config",
        root + "/identity.json",
        "--disposable-fixture",
        "--action",
        "lock",
      ],
    ],
    [
      "/usr/bin/systemctl",
      [
        "show",
        "--property=MainPID",
        "--value",
        "fased-custody-1800-app.service",
      ],
    ],
    ["/usr/bin/cat", [root + "/install.json"]],
    ["/usr/bin/sha256sum", [root + "/fased-app"]],
  ])
    assert.equal(
      remoteInstalledCommand(f.p, file, command).file,
      "/usr/bin/ssh",
    );
  verifyInstalledTradeReceiver(f.p, f.r);
  for (const change of [
    (p) => (p.origin = "http://fixture.tail123.ts.net"),
    (p) => (p.origin = "https://fixture.tail123.ts.net:22"),
    (p) => (p.origin = "https://fixture.tail123.ts.net/path"),
    (p) => (p.origin = "https://fixture.tail123.ts.net.evil.example"),
    (p) => (p.remoteUser = "root"),
    (p) => (p.remoteUser = "owner;touch"),
  ]) {
    const bad = structuredClone(f.p);
    change(bad);
    assert.throws(() => validateInstalledTradePlan(bad));
  }
  const args = [
    "custody",
    "agents",
    "--disposable-fixture",
    "--prepare-host",
    "--request-stdin",
  ];
  const cmd = remoteInstalledCommand(f.p, root + "/fased-app", args);
  assert.equal(cmd.file, "/usr/bin/ssh");
  for (const flag of [
    "-oBatchMode=yes",
    "-oStrictHostKeyChecking=yes",
    "-oPasswordAuthentication=no",
    "-oProxyCommand=none",
    "-oClearAllForwardings=yes",
  ])
    assert.ok(cmd.args.includes(flag));
  assert.deepEqual(
    cmd.args.slice(-args.length - 4),
    [
      "-l",
      f.p.remoteUser,
      "--",
      "fixture.tail123.ts.net",
      root + "/fased-app",
      ...args,
    ].slice(1),
  );
  assert.throws(() =>
    remoteInstalledCommand(f.p, root + "/fased-app", [
      "agents",
      "--request-stdin",
    ]),
  );
  assert.throws(() =>
    remoteInstalledCommand(f.p, "/usr/bin/sh", ["-c", "true"]),
  );
  assert.throws(() =>
    remoteInstalledCommand(f.p, "/usr/bin/cat", ["/etc/shadow"]),
  );
});

test("Remote held status is read from the authenticated matching private browser", async () => {
  const f = remoteFixture();
  const routes = {
    health: {
      status: "ok",
      instanceId: f.p.instance,
      connection: { location: "remote", transport: "private-https" },
      capabilities: {
        aiConnections: false,
        financialExecution: false,
        walletSigner: false,
      },
    },
    "auth/state": {
      authenticated: true,
      remoteAccess: true,
      remoteEnrolled: true,
    },
    "agents?profile=trading-analyst": {
      profileId: "trading-analyst",
      revision: 1,
      financialExecution: false,
      providerCallsEnabled: false,
    },
  };
  const page = {
    evaluate: async (_fn, { route }) => structuredClone(routes[route]),
  };
  assert.deepEqual(await remoteHeldStatus(f.p, page), {
    instanceId: f.p.instance,
    profileId: "trading-analyst",
    revision: 1,
    financialExecution: false,
    providerCallsEnabled: false,
  });
  for (const change of [
    (r) => (r.health.instanceId = "foreign"),
    (r) => (r.health.connection.transport = "loopback"),
    (r) => (r.health.capabilities.walletSigner = true),
    (r) => (r["auth/state"].authenticated = false),
    (r) => (r["auth/state"].remoteEnrolled = false),
  ]) {
    const bad = structuredClone(routes);
    change(bad);
    const other = {
      evaluate: async (_fn, { route }) => structuredClone(bad[route]),
    };
    await assert.rejects(remoteHeldStatus(f.p, other));
  }
});

test("Remote browser adapter uses real control methods and refuses foreign pages", async () => {
  const f = remoteFixture(),
    actions = [];
  let selected = "";
  const field = {
    count: async () => 1,
    click: async () => actions.push("click"),
    fill: async (v) => {
      selected = v;
    },
    inputValue: async () => selected,
    selectOption: async (v) => {
      selected = v;
    },
    getByRole: () => ({ count: async () => 1 }),
    isVisible: async () => true,
  };
  const page = {
    url: () => f.p.origin + "/?instance=" + f.p.instance,
    getByRole: (role) =>
      role === "navigation" ? { getByRole: () => field } : field,
    getByLabel: () => field,
    getByText: () => field,
    evaluate: async (_fn, { agent }) =>
      agent ? "trading-analyst" : f.p.accountId,
  };
  await remoteBrowserControl(f.p, page, {
    frame: f.p.mainFrame,
    action: "click",
    role: "link",
    name: "Fased",
  });
  await remoteBrowserControl(f.p, page, {
    frame: f.p.mainFrame,
    action: "click",
    role: "link",
    name: "Chat",
  });
  await remoteBrowserControl(f.p, page, {
    frame: f.p.mainFrame,
    action: "select-agent",
    name: "Agent",
    value: "Trading Analyst",
  });
  await remoteBrowserControl(f.p, page, {
    frame: f.p.mainFrame,
    action: "select",
    name: "Wallet",
    value: f.p.accountName,
  });
  await remoteBrowserControl(f.p, page, {
    frame: f.p.mainFrame,
    action: "set",
    name: "Amount",
    value: "12.5",
  });
  assert.deepEqual(actions, ["click", "click"]);
  await assert.rejects(
    remoteBrowserControl(
      f.p,
      { ...page, url: () => "https://foreign.example/" },
      { frame: f.p.mainFrame, action: "click", role: "link", name: "Fased" },
    ),
  );
});
test("real-control producer uses UI submission and installed CLI read; PASS requires restart", async () => {
  const f = fixture();
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    undefined,
    f.save,
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.equal(result.class, "source-mocked-not-installed");
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
  assert.ok(
    f.events.indexOf("present:" + f.q.digest) <
      f.events.indexOf("private-input"),
  );
  assert.equal(f.saved.at(-1).signature, "one-original");
  assert.ok(!JSON.stringify(f.saved).includes("disposable fixture password"));
  f.setSubmitted();
  f.io.runtime = async () => ({ app: "200", signer: "201", boot: "same" });
  f.events.length = 0;
  const after = await installedNativeTradeJourney(f.p, f.io, result, f.save);
  assert.equal(after.result, "PASS");
  assert.equal(after.recovered, true);
  assert.ok(
    !f.events.some((x) => /Quote|Approve|Submit|private-input/.test(x)),
  );
});
test("wrong receiver refuses without UI, secret read or receipt mutation", async () => {
  const f = fixture();
  f.r.accounts.fixture.status.custody.origin = "imported";
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  assert.deepEqual(f.events, []);
  assert.deepEqual(f.saved, []);
});
test("missing or mismatched RPC refuses before native actions or private inputs", async () => {
  for (const rpc of [
    undefined,
    null,
    { version: 2, network: "mainnet-beta" },
    { version: 2, network: "devnet", endpoint: "https://foreign.invalid" },
  ]) {
    const f = fixture();
    f.r.accounts.record.accounts[0].rpc = rpc;
    await assert.rejects(
      installedNativeTradeJourney(f.p, f.io, undefined, f.save),
      /configure a same-network RPC before native trade acceptance/,
    );
    assert.deepEqual(f.events, []);
    assert.deepEqual(f.saved, []);
  }
});
test("explicit digest-bound pre-review recovery retains failure and refuses uncertain work", async () => {
  const f = fixture();
  const native = f.io.native;
  f.io.native = async () => {
    throw Error("pre-review control absent");
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  const previous = structuredClone(f.saved.at(-1));
  f.io.native = native;
  f.events.length = 0;
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, previous, f.save),
  );
  const digest = sha(JSON.stringify(previous));
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    previous,
    f.save,
    { resumePreReview: digest },
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.deepEqual(result.preReviewFailure.receipt, previous);
  assert.equal(result.preReviewFailure.sha256, digest);
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
  for (const field of [
    "intent",
    "proposal",
    "stage",
    "taskId",
    "runId",
    "signature",
    "signed",
    "preReviewFailure",
  ]) {
    const bad = structuredClone(previous);
    bad[field] = "uncertain";
    const g = fixture();
    await assert.rejects(
      installedNativeTradeJourney(g.p, g.io, bad, g.save, {
        resumePreReview: sha(JSON.stringify(bad)),
      }),
    );
    assert.deepEqual(g.events, []);
    assert.deepEqual(g.saved, []);
  }
  const g = fixture();
  g.io.draft = async () => ({ dispatched: true });
  await assert.rejects(
    installedNativeTradeJourney(g.p, g.io, previous, g.save, {
      resumePreReview: digest,
    }),
  );
  assert.deepEqual(g.events, []);
  assert.deepEqual(g.saved, []);
});
test("resolved original mismatched review needs exact proof and no signer approval", async () => {
  const f = fixture(),
    native = f.io.native;
  f.io.native = async () => {
    throw Error("control");
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  const previous = structuredClone(f.saved.at(-1));
  previous.preReviewFailure = {
    receipt: structuredClone(previous),
    sha256: sha(JSON.stringify(previous)),
  };
  const q = structuredClone(f.q);
  q.quote.profileId = "wen-operator";
  q.quote.id = "9".repeat(32);
  q.quote.expiresAt = 1;
  const old = {
    intent: {
      ...f.intent,
      operationId: q.quote.id,
      proposal: Buffer.from(JSON.stringify(q)).toString("base64url"),
    },
    origin: "",
    dispatched: false,
    completed: true,
  };
  const options = {
    resumePreReview: sha(JSON.stringify(previous)),
    resolvedDraftSha256: sha(JSON.stringify(old)),
  };
  f.events.length = 0;
  f.io.native = native;
  f.io.draft = async () => old;
  const inspect = f.io.inspect;
  f.io.inspect = async (i, ...args) =>
    i.proposal === old.intent.proposal
      ? { readOnly: true, status: { locked: true }, swap: null }
      : inspect(i, ...args);
  const originalNative = f.io.native;
  f.io.native = async (step) => {
    if (step.name === "Approve" && step.frame === "Fased App") {
      let reads = 0;
      f.io.draft = async () =>
        ++reads < 3 ? old : { intent: f.intent, origin: "", dispatched: false };
    }
    return originalNative(step);
  };
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    previous,
    f.save,
    options,
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.equal(result.resolvedUnsentDraft.sha256, options.resolvedDraftSha256);
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
  for (const change of [
    (x) => (x.dispatched = true),
    (x) => (x.completed = false),
    (x) => (x.intent = { ...x.intent, network: "foreign" }),
  ]) {
    const bad = structuredClone(old);
    change(bad);
    const g = fixture();
    g.io.draft = async () => bad;
    g.io.inspect = async () => ({
      readOnly: true,
      status: { locked: true },
      swap: { state: "approved" },
    });
    await assert.rejects(
      installedNativeTradeJourney(g.p, g.io, previous, g.save, {
        ...options,
        resolvedDraftSha256: sha(JSON.stringify(bad)),
      }),
    );
    assert.deepEqual(g.events, []);
    assert.deepEqual(g.saved, []);
  }
});
test("resolved recovery refuses uncertain ancestor or excessive history", async () => {
  const f = fixture();
  const native = f.io.native;
  f.io.native = async () => {
    throw Error("pre-review");
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  const prior = structuredClone(f.saved.at(-1));
  prior.preReviewFailure = { receipt: structuredClone(prior) };
  prior.preReviewFailure.receipt.intent = { operationId: "uncertain" };
  prior.preReviewFailure.sha256 = sha(
    JSON.stringify(prior.preReviewFailure.receipt),
  );
  f.events.length = 0;
  f.saved.length = 0;
  f.io.native = native;
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, prior, f.save, {
      resumePreReview: sha(JSON.stringify(prior)),
      resolvedDraftSha256: "a".repeat(64),
    }),
    /uncertain/,
  );
  assert.deepEqual(f.events, []);
  assert.deepEqual(f.saved, []);
});
test("resolved recovery rejects altered ancestor digest or result", async () => {
  const f = fixture();
  f.io.native = async () => {
    throw Error("pre-review");
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  const original = structuredClone(f.saved.at(-1));
  for (const corrupt of ["digest", "result"]) {
    const previous = structuredClone(original);
    previous.preReviewFailure = {
      sha256: sha(JSON.stringify(original)),
      receipt: structuredClone(original),
    };
    if (corrupt === "digest") previous.preReviewFailure.sha256 = "f".repeat(64);
    else {
      previous.preReviewFailure.receipt.result = "OPEN";
      previous.preReviewFailure.sha256 = sha(
        JSON.stringify(previous.preReviewFailure.receipt),
      );
    }
    const g = fixture();
    await assert.rejects(
      installedNativeTradeJourney(g.p, g.io, previous, g.save, {
        resumePreReview: sha(JSON.stringify(previous)),
        resolvedDraftSha256: "a".repeat(64),
      }),
    );
    assert.deepEqual(g.events, []);
    assert.deepEqual(g.saved, []);
  }
});
test("expired reviewed refusal only renews after exact unsigned completed draft inspection", async () => {
  const f = fixture(),
    native = f.io.native;
  f.io.native = async (step) => {
    if (step.name === f.q.digest) throw Error("facts unavailable");
    return native(step);
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  const previous = structuredClone(f.saved.at(-1));
  assert.equal(previous.stage, "reviewed");
  const prior = structuredClone(previous);
  delete prior.stage;
  delete prior.intent;
  delete prior.proposal;
  prior.failure = "stopped at pre-review; inspect original before resuming";
  previous.preReviewFailure = {
    sha256: sha(JSON.stringify(prior)),
    receipt: prior,
  };
  const q = structuredClone(previous.proposal);
  q.quote.id = "8".repeat(32);
  q.quote.createdAt = 0;
  q.quote.expiresAt = 1;
  previous.proposal = q;
  previous.intent = {
    ...previous.intent,
    operationId: q.quote.id,
    proposal: Buffer.from(JSON.stringify(q)).toString("base64url"),
  };
  const old = {
    intent: previous.intent,
    origin: "",
    completed: true,
    dispatched: false,
  };
  f.events.length = 0;
  f.io.draft = async () => old;
  const inspect = f.io.inspect;
  f.io.inspect = async (i, ...a) =>
    i.operationId === old.intent.operationId
      ? { readOnly: true, status: { locked: true }, swap: null }
      : inspect(i, ...a);
  f.io.native = async (step) => {
    if (step.name === "Approve" && step.frame === "Fased App")
      f.io.draft = async () => ({
        intent: f.intent,
        origin: "",
        dispatched: false,
      });
    return native(step);
  };
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    previous,
    f.save,
    {
      resumePreReview: sha(JSON.stringify(previous)),
      resolvedDraftSha256: sha(JSON.stringify(old)),
    },
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.equal(f.events.filter((x) => x === "click:Submit").length, 1);
  assert.deepEqual(result.preReviewFailure.receipt, previous);
});
test("native public review text is observed without reading editable fields", async () => {
  const { spawnSync } = await import("node:child_process");
  const source = await readFile(
    new URL("fixture-installed-trade-journey.mjs", import.meta.url),
    "utf8",
  );
  const code = source.slice(
    source.indexOf("def observed(n):"),
    source.indexOf("def match():"),
  );
  const r = spawnSync(
    "/usr/bin/python3",
    [
      "-c",
      code +
        `
class N:
 name=''
 def __init__(self,role):self.role=role
 def getRoleName(self):return self.role
 def queryText(self):
  if self.role=='password text':raise RuntimeError('private field read')
  return self
 def getText(self,a,b):return 'exact digest'
assert observed(N('description value'))=='exact digest'
assert observed(N('table cell'))=='exact digest'
assert observed(N('password text'))==''
assert observed(N('entry'))==''
`,
    ],
    { encoding: "utf8" },
  );
  assert.equal(r.status, 0, r.stderr);
});
test("owner connection precedes visible facts and secret input", async () => {
  const f = fixture();
  let connected = false;
  const native = f.io.native;
  f.io.native = async (step) => {
    if (step.frame === "Fased Wallet" && step.name === "Connect wallet")
      connected = true;
    if (
      step.frame === "Fased Wallet" &&
      (step.action === "fact" || step.name === f.q.digest)
    )
      assert.ok(connected, "owner facts are hidden before connection");
    return native(step);
  };
  assert.equal(
    (await installedNativeTradeJourney(f.p, f.io, undefined, f.save)).result,
    "NEEDS_RESTART",
  );
  assert.ok(
    f.events.indexOf("click:Connect wallet") <
      f.events.indexOf("private-input"),
  );
});
test("native agent commitment precedes Onchain and Quote", async () => {
  // Recovery may reopen on Onchain; the Agent control exists on Dashboard.

  const f = fixture();
  const r = await installedNativeTradeJourney(f.p, f.io, undefined, f.save);
  assert.equal(r.result, "NEEDS_RESTART");
  assert.ok(f.events.indexOf("click:Fased") >= 0);
  assert.ok(
    f.events.indexOf("click:Fased") < f.events.indexOf("select-agent:Agent"),
  );
  assert.ok(
    f.events.indexOf("select-agent:Agent") < f.events.indexOf("click:Onchain"),
  );
});
test("existing ambiguous owner draft cannot be replaced or approved", async () => {
  const f = fixture();
  f.io.draft = async () => ({ dispatched: true });
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
    /existing owner draft/,
  );
  assert.deepEqual(f.events, []);
});
test("restart cannot be a label: same processes and foreign original are refused", async () => {
  const f = fixture();
  f.setSubmitted();
  const prior = {
    version: 1,
    planHash: sha(JSON.stringify(f.p)),
    source: f.p.source,
    profile: "local",
    class: f.io.kind,
    result: "NEEDS_RESTART",
    proposal: f.q,
    intent: f.intent,
    before: { app: "100", signer: "101", boot: "same" },
    signature: "one-original",
    signed: "signed-original",
    taskId: "onchain-task:" + f.q.quote.id,
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, prior, f.save),
    /actual app and signer restart/,
  );
  f.io.runtime = async () => ({ app: "200", signer: "201", boot: "same" });
  f.io.inspect = async () => ({
    readOnly: true,
    status: { locked: true, recoveryRequired: true },
    swap: { proposal: f.q, signature: "different", signed: "different" },
  });
  await assert.rejects(installedNativeTradeJourney(f.p, f.io, prior, f.save));
  assert.deepEqual(f.saved, []);
});
test("lost UI dispatch resumes only its original through CLI reconciliation, no second submit", async () => {
  const f = fixture();
  const real = f.io.native;
  f.io.native = async (s) => {
    await real(s);
    if (s.name === "Submit") throw Error("lost UI reply");
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  const prior = f.saved.at(-1);
  assert.equal(prior.stage, "submitted");
  assert.equal(prior.result, "FAIL");
  f.io.runtime = async () => ({ app: "200", signer: "201", boot: "same" });
  const normal = f.io.read;
  let uncertain = true;
  f.io.read = async () => {
    const v = await normal();
    if (uncertain) v.onchain.runs[0].swap.state = "unknown";
    return v;
  };
  f.io.change = async (c) => {
    assert.equal(c.action, "swap-reconcile");
    uncertain = false;
    return normal();
  };
  f.events.length = 0;
  const result = await installedNativeTradeJourney(f.p, f.io, prior, f.save);
  assert.equal(result.result, "PASS");
  assert.equal(result.runId, "onchain-task:" + f.q.quote.id + ":1");
  assert.equal(result.signature, "one-original");
  assert.equal(result.signed, "signed-original");
  assert.ok(!f.events.includes("click:Submit"));
  assert.ok(!f.events.includes("private-input"));
});
test("native adapter has OS image/owner/intent checks and no page/signer injection", async () => {
  const source = await readFile(
    new URL("./fixture-installed-trade-journey.mjs", import.meta.url),
    "utf8",
  );
  const options = await readFile(
    new URL("../desktop/internal/window/options.go", import.meta.url),
    "utf8",
  );
  assert.ok(
    options.includes('Title:             "' + plan().mainFrame + '"') ||
      new RegExp('Title:\\s*"' + plan().mainFrame + '"').test(options),
  );
  assert.match(source, /get_process_id/);
  assert.match(source, /unverified native owner frame/);
  assert.match(source, /args\[pos\+1\]/);
  for (const forbidden of [
    "window.go",
    "WindowExecJS",
    "ChangeAgents(",
    '"swap-submit"',
    '"swap-approve"',
    '"approve-swap", "--config"',
  ])
    assert.ok(!source.includes(forbidden));
});

test("whole original preservation cannot be replaced by labels", () => {
  const before = {
    version: 1,
    ownerUid: 1400,
    capturedAt: "2026-11-09T00:00:00Z",
    identity: { id: "preserved" },
    holds: { recovery: true, pending: 0, unknown: 0, floor: 2 },
    authSha256: h,
    accountsSha256: h,
    rpcSha256: h,
    files: Object.fromEntries(
      [
        "install.json",
        "identity.json",
        "enrollment-ready.json",
        "signer-state/vault.json",
        "signer-state/operations.log",
      ].map((k) => [k, h]),
    ),
  };
  const after = {
    ...structuredClone(before),
    capturedAt: "2026-11-09T00:02:00Z",
  };
  const at = Date.parse("2026-11-09T00:01:00Z");
  verifyPreservedOriginal(before, after, 1400, at);
  for (const change of [
    (x) => (x.rpcSha256 = "f".repeat(64)),
    (x) => (x.holds.floor = 0),
    (x) => (x.files["signer-state/vault.json"] = "f".repeat(64)),
    (x) => (x.capturedAt = before.capturedAt),
  ]) {
    const bad = structuredClone(after);
    change(bad);
    assert.throws(() => verifyPreservedOriginal(before, bad, 1400, at));
  }
});

test("post-reconcile duplicate runs or replaced task cannot earn recovery PASS", async () => {
  for (const kind of ["duplicate", "replacement"]) {
    const f = fixture();
    f.setSubmitted();
    const normal = f.io.read;
    f.io.runtime = async () => ({ app: "200", signer: "201", boot: "same" });
    f.io.read = async () => {
      const v = await normal();
      v.onchain.runs[0].swap.state = "unknown";
      return v;
    };
    const prior = {
      version: 1,
      planHash: sha(JSON.stringify(f.p)),
      source: f.p.source,
      profile: "local",
      class: f.io.kind,
      result: "NEEDS_RESTART",
      proposal: f.q,
      intent: f.intent,
      before: { app: "100", signer: "101", boot: "same" },
      signature: "one-original",
      signed: "signed-original",
      taskId: "onchain-task:" + f.q.quote.id,
    };
    f.io.change = async () => {
      const v = await normal();
      if (kind === "duplicate")
        v.onchain.runs.push(structuredClone(v.onchain.runs[0]));
      else {
        v.onchain.tasks[0].id = "replacement";
        v.onchain.runs[0].taskId = "replacement";
      }
      return v;
    };
    await assert.rejects(installedNativeTradeJourney(f.p, f.io, prior, f.save));
    assert.deepEqual(f.saved, []);
  }
});

test("a displayed review fact mismatch stops before private input and submit", async () => {
  const f = fixture();
  const native = f.io.native;
  f.io.native = async (s) => {
    if (s.action === "fact" && s.name === "Minimum")
      throw Error("wrong displayed minimum");
    return native(s);
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
  );
  assert.ok(!f.events.includes("private-input"));
  assert.ok(!f.events.includes("click:Submit"));
  assert.equal(f.saved.at(-1).result, "FAIL");
});

test(
  "installed entrypoint does not load CI-only browser dependencies",
  { skip: process.platform !== "linux" || process.getuid?.() === 0 },
  async () => {
    const { mkdtemp, copyFile, rm } = await import("node:fs/promises");
    const { tmpdir } = await import("node:os");
    const { join } = await import("node:path");
    const { spawnSync } = await import("node:child_process");
    const dir = await mkdtemp(join(tmpdir(), "fased-native-entry-"));
    try {
      for (const file of [
        "check-managed-custody.mjs",
        "fixture-installed-trade-journey.mjs",
      ])
        await copyFile(new URL(file, import.meta.url), join(dir, file));
      const missing = join(dir, "missing-private-plan.json");
      const result = spawnSync(
        process.execPath,
        [join(dir, "check-managed-custody.mjs")],
        {
          env: {
            ...process.env,
            FASED_DISPOSABLE_VM_ACCEPTANCE: "1",
            FASED_INSTALLED_NATIVE_TRADE_PLAN: missing,
          },
          encoding: "utf8",
          timeout: 10000,
        },
      );
      assert.equal(result.error, undefined);
      assert.notEqual(result.status, 0);
      assert.match(result.stderr, /ENOENT/);
      assert.ok(result.stderr.includes(missing));
      assert.doesNotMatch(result.stderr, /ERR_MODULE_NOT_FOUND|playwright/);
    } finally {
      await rm(dir, { recursive: true, force: true });
    }
  },
);

test("native traversal refuses cyclic and oversized accessibility graphs", async () => {
  const { spawnSync } = await import("node:child_process");
  const source = await readFile(
    new URL("fixture-installed-trade-journey.mjs", import.meta.url),
    "utf8",
  );
  const body = source.slice(
    source.indexOf("def walk(n,"),
    source.indexOf("def match():"),
  );
  const checks = `
class Node:
 def __init__(self): self.children=[]
 def __iter__(self):
  global visits
  visits+=1
  if visits>20:raise RuntimeError('cycle not bounded')
  return iter(self.children)
a=Node();b=Node();a.children=[a,b];b.children=[a]
visits=0
assert len(list(walk(a)))==2 and visits==2
root=Node();root.children=[Node() for _ in range(2100)]
# Permit this finite wide graph; only the production walk may reject its budget.
Node.__iter__=lambda self:iter(self.children)
try:list(walk(root))
except RuntimeError as e:assert str(e)=='native tree budget exceeded'
else:raise AssertionError('missing node budget')
`;
  const result = spawnSync("/usr/bin/python3", ["-c", body + checks], {
    encoding: "utf8",
    timeout: 5000,
  });
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr);
});

test("native wallet selection uses the supported child menu, opens only when needed and refuses foreign options", async () => {
  const { spawnSync } = await import("node:child_process");
  const source = await readFile(
    new URL("fixture-installed-trade-journey.mjs", import.meta.url),
    "utf8",
  );
  const walk = source.slice(
    source.indexOf("def walk(n,"),
    source.indexOf("def match():"),
  );
  const select = source
    .slice(
      source.indexOf("elif p['action']=='select':"),
      source.indexOf("elif p['action']=='fact':"),
    )
    .replace(/^elif /, "if ");
  const harness = `
import time
class Action:
 nActions=1
 def __init__(self,callback):self.callback=callback
 def doAction(self,i):self.callback();return True
class Item:
 def __init__(self,name,menu):self.name=name;self.menu=menu
 def __iter__(self):return iter([])
 def querySelection(self):raise NotImplementedError
 def queryAction(self):return Action(lambda:setattr(self.menu,'committed',self.name))
class Menu:
 def __init__(self):self.items=[Item('Select wallet',self)];self.index=0;self.committed=None
 @property
 def childCount(self):return len(self.items)
 def __iter__(self):return iter(self.items)
 def __getitem__(self,i):return self.items[i]
 def querySelection(self):return self
 def selectChild(self,i):self.index=i;return True
 def getSelectedChild(self,i):return self.items[self.index]
class Combo:
 def __init__(self):self.menu=Menu();self.opens=0
 def __iter__(self):return iter([self.menu])
 def querySelection(self):raise NotImplementedError
 def queryAction(self):return Action(self.open)
 def open(self):self.opens+=1;self.menu.items.append(Item('Local approval',self.menu))
n=Combo();p={'action':'select','value':'Local approval'}
`;
  const script =
    walk +
    harness +
    select +
    `
assert n.opens==1 and n.menu.committed=='Local approval'
`;
  const result = spawnSync("/usr/bin/python3", ["-c", script], {
    encoding: "utf8",
    timeout: 5000,
  });
  assert.equal(result.error, undefined);
  assert.equal(result.status, 0, result.stderr);
  const wrong = spawnSync(
    "/usr/bin/python3",
    [
      "-c",
      walk +
        harness.replace(
          "'value':'Local approval'",
          "'value':'Foreign wallet'",
        ) +
        select,
    ],
    { encoding: "utf8", timeout: 5000 },
  );
  assert.equal(wrong.error, undefined);
  assert.notEqual(wrong.status, 0);
  assert.match(wrong.stderr, /native selection unavailable/);
});

test("installed native journey observes the asynchronous wallet option before selection", async () => {
  const f = fixture();
  let choiceObserved = false;
  const native = f.io.native;
  f.io.native = async (step) => {
    if (
      step.action === "present" &&
      step.name === f.p.accountName &&
      step.role === "menu item"
    )
      choiceObserved = true;
    if (step.action === "select" && step.name === "Wallet")
      assert.ok(
        choiceObserved,
        "wallet option must exist before selecting the remounted control",
      );
    return native(step);
  };
  const result = await installedNativeTradeJourney(
    f.p,
    f.io,
    undefined,
    f.save,
  );
  assert.equal(result.result, "NEEDS_RESTART");
  assert.ok(
    f.events.indexOf("present:" + f.p.accountName) <
      f.events.indexOf("select:Wallet"),
  );
});

test("missing native wallet option stops before quote and private input", async () => {
  const f = fixture();
  const native = f.io.native;
  f.io.native = async (step) => {
    if (step.action === "present" && step.name === f.p.accountName)
      throw Error("native wallet option unavailable");
    return native(step);
  };
  await assert.rejects(
    installedNativeTradeJourney(f.p, f.io, undefined, f.save),
    /native wallet option unavailable/,
  );
  assert.equal(f.saved.at(-1).result, "FAIL");
  assert.ok(
    !f.events.some((event) => /Quote|Approve|Submit|private-input/.test(event)),
  );
});
