import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import {
  validateInstalledTradePlan,
  verifyInstalledTradeReceiver,
  installedNativeTradeJourney,
  verifyPreservedOriginal,
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
        draft = { intent, origin: "", dispatched: false };
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
  assert.throws(
    () => validateInstalledTradePlan({ ...plan(), profile: "remote" }),
    /Remote Onchain CLI owner route/,
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
