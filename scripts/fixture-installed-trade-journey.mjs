// Opt-in extension of the managed-custody producer. No installer, build, OS
// privilege grant, token import, signer shortcut or replacement runtime lives here.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile, lstat, writeFile, rename } from "node:fs/promises";
import { homedir } from "node:os";
import { join, dirname } from "node:path";

const hash = (x) => createHash("sha256").update(x).digest("hex");
const hex = /^[a-f0-9]{64}$/;
const safePath = /^\/[A-Za-z0-9_./-]+$/;
const instanceID = /^[A-Za-z0-9_-]{43}$/;
const profile = "trading-analyst";
const allowed = [
  "version",
  "source",
  "profile",
  "ownerUid",
  "clientUid",
  "instance",
  "data",
  "manifestSha256",
  "enrollmentSha256",
  "coreSha256",
  "signerSha256",
  "native",
  "origin",
  "remoteUser",
  "accountId",
  "accountName",
  "privateFile",
  "receipt",
  "mainFrame",
  "preserve",
  "packageReceipt",
  "packageReceiptSha256",
];

export function validateInstalledTradePlan(p) {
  assert.ok(
    p && Object.keys(p).every((k) => allowed.includes(k)),
    "unknown/private plan field",
  );
  assert.equal(p.version, 1);
  assert.match(p.source, /^[a-f0-9]{40}$/);
  assert.equal(
    p.profile,
    "local",
    "Remote Onchain CLI owner route is not admitted; Local first",
  );
  for (const n of [p.ownerUid, p.clientUid])
    assert.ok(Number.isSafeInteger(n) && n > 0);
  if (p.profile === "local") assert.equal(p.ownerUid, p.clientUid);
  assert.notEqual(
    p.ownerUid,
    1750,
    "retained config-only owner cannot become a positive enrollment",
  );
  assert.match(p.instance, instanceID);
  assert.match(p.packageReceiptSha256, hex);
  for (const n of [
    "manifestSha256",
    "enrollmentSha256",
    "coreSha256",
    "signerSha256",
  ])
    assert.match(p[n], hex);
  for (const n of ["data", "privateFile", "receipt", "packageReceipt"])
    assert.match(p[n], safePath);
  for (const path of [
    p.data,
    p.privateFile,
    p.receipt,
    p.packageReceipt,
    p.native.path,
  ])
    assert.ok(
      !path.split("/").some((s) => s === ".." || s === "."),
      "canonical paths required",
    );
  assert.deepEqual(Object.keys(p.native).sort(), ["path", "sha256"]);
  assert.match(p.native.path, safePath);
  assert.match(p.native.sha256, hex);
  assert.equal(p.native.path.split("/").at(-1), "fased-desktop");
  assert.equal(p.mainFrame, "Fased App", "freeze the actual main frame");
  assert.match(p.accountId, /^[a-f0-9]{32}$/);
  assert.ok(
    typeof p.accountName === "string" &&
      p.accountName.length > 0 &&
      p.accountName.length <= 80,
  );
  assert.deepEqual(Object.keys(p.preserve).sort(), [
    "after",
    "before",
    "beforeSha256",
    "manifestSha256",
    "ownerUid",
  ]);
  assert.match(p.preserve.before, safePath);
  assert.match(p.preserve.after, safePath);
  assert.match(p.preserve.beforeSha256, hex);
  assert.ok(
    Number.isSafeInteger(p.preserve.ownerUid) && p.preserve.ownerUid > 0,
  );
  assert.notEqual(
    p.preserve.ownerUid,
    p.ownerUid,
    "original owner is never the positive destination",
  );
  assert.match(p.preserve.manifestSha256, hex);
  assert.equal(p.origin, "");
  assert.equal(p.remoteUser, "");

  return {
    root: `/var/lib/fased-app-custody-${p.ownerUid}`,
    preservedRoot: `/var/lib/fased-app-custody-${p.preserve.ownerUid}`,
  };
}

export function verifyInstalledTradeReceiver(p, observed) {
  const { root } = validateInstalledTradePlan(p),
    m = observed.manifest;
  assert.equal(observed.uid, p.ownerUid, "wrong OS operator");
  assert.equal(observed.manifestSha256, p.manifestSha256);
  assert.equal(observed.enrollmentSha256, p.enrollmentSha256);
  assert.equal(observed.preservedManifestSha256, p.preserve.manifestSha256);
  assert.equal(m.root, root);
  assert.equal(m.owner, p.ownerUid);
  assert.equal(m.version, 3);
  assert.equal(m.location, p.profile);
  assert.equal(m.publicOrigin ?? "", p.origin);
  assert.equal(m.enrollment, true);
  assert.equal(m.nativeOwnerAccess === true, p.profile === "local");
  assert.equal(m.mode, "ordinary-custody:v1");
  assert.equal(m.profile, "owner-authorized:v1");
  assert.equal(m.provenance, "controlled-disposable:v1");
  assert.equal(m.coreHash, p.coreSha256);
  assert.equal(m.signerHash, p.signerSha256);
  assert.equal(observed.coreSha256, p.coreSha256);
  assert.equal(observed.signerSha256, p.signerSha256);
  const peer = observed.peer;
  assert.equal(peer.ready, true);
  assert.equal(peer.ownerUid, p.ownerUid);
  assert.equal(peer.instanceId, p.instance);
  assert.equal(peer.signerId, m.id);
  assert.equal(peer.network, m.network);
  const commit = observed.enrollment;
  assert.equal(commit.instanceId, p.instance);
  const accounts = observed.accounts,
    st = accounts.fixture.status;
  assert.equal(accounts.setup.instanceId, p.instance);
  assert.equal(accounts.setup.source, p.profile);
  assert.equal(
    st.custody.origin,
    "generated",
    "imported/restored keys cannot be relabeled",
  );
  assert.equal(st.custody.profile, "owner-authorized:v1");
  assert.equal(st.custody.provenance, "controlled-disposable:v1");
  for (const k of [
    "fixtureOnly",
    "admissionHeld",
    "recoveryRequired",
    "swapReady",
  ])
    assert.equal(st[k], true, `missing protected ${k}`);
  assert.equal(st.walletSigner, false);
  assert.equal(st.financialExecution, false);
  assert.equal(st.updateHeld, false);
  assert.equal(st.locked, true, "preflight starts locked");
  const account = accounts.record.accounts.find((a) => a.id === p.accountId);
  assert.ok(account);
  assert.equal(account.version, 4);
  assert.equal(account.mode, "managed");
  assert.equal(account.name, p.accountName);
  assert.deepEqual(account.signer, st.account);
  assert.equal(account.network, m.network);
  assert.ok(
    account.rpc?.version === 2 &&
      account.rpc.network === account.network &&
      (account.rpc.endpoint ===
        `https://${account.network === "devnet" ? "devnet" : "mainnet"}.helius-rpc.com` ||
        account.rpc.endpoint ===
          `https://solana-${account.network === "devnet" ? "devnet" : "mainnet"}.g.alchemy.com`),
    "configure a same-network RPC before native trade acceptance",
  );
  assert.ok(["devnet", "mainnet-beta"].includes(m.network));
  assert.equal(st.account.signerId, m.id);
  assert.notEqual(m.app.uid, m.signer.uid);
  assert.ok(
    [m.app.uid, m.signer.uid].every(
      (n) => Number.isSafeInteger(n) && n > 0 && n !== p.ownerUid,
    ),
  );
  return account;
}

export function verifyPreservedOriginal(before, after, ownerUid, startedAt) {
  assert.equal(before.version, 1);
  assert.equal(after.version, 1);
  assert.equal(before.ownerUid, ownerUid);
  assert.equal(after.ownerUid, ownerUid);
  for (const key of ["authSha256", "accountsSha256", "rpcSha256"]) {
    assert.match(before[key], hex);
    assert.equal(after[key], before[key]);
  }
  for (const key of [
    "install.json",
    "identity.json",
    "enrollment-ready.json",
    "signer-state/vault.json",
    "signer-state/operations.log",
  ]) {
    assert.match(before.files[key], hex);
    assert.equal(after.files[key], before.files[key]);
  }
  assert.deepEqual(after.identity, before.identity);
  assert.deepEqual(after.holds, before.holds);
  assert.ok(
    before.identity &&
      before.holds &&
      Number.isFinite(Date.parse(before.capturedAt)),
  );
  assert.ok(
    Date.parse(after.capturedAt) >= startedAt &&
      Date.parse(after.capturedAt) > Date.parse(before.capturedAt),
    "fresh parent OS preservation projection required",
  );
}
async function privateOwnedFile(path, uid) {
  const st = await lstat(path);
  assert.ok(
    st.isFile() &&
      !st.isSymbolicLink() &&
      st.uid === uid &&
      (st.mode & 0o777) === 0o600,
    "owned0600 evidence required",
  );
  return readFile(path);
}

// Commands cannot be supplied by a browser or plan. Local uses the existing
// enrolled owner route; this producer grants no OS or Remote authority.
export function installedTradeAdapters(p) {
  const { root, preservedRoot } = validateInstalledTradePlan(p);
  const exec = (file, args, input) =>
    execFileSync(file, args, {
      input,
      encoding: "utf8",
      timeout: 15000,
      maxBuffer: 2 * 1024 * 1024,
      stdio: ["pipe", "pipe", "pipe"],
    });
  const installed = (file, args, input) => exec(file, args, input);

  const cli = (args, body) =>
    JSON.parse(
      installed(
        root + "/fased-app",
        args,
        body === undefined ? undefined : JSON.stringify(body),
      ),
    );
  const read = (path) => installed("/usr/bin/cat", [path]);
  let originalBefore;
  return {
    kind: "installed-native",
    async preflight() {
      assert.equal(process.getuid(), p.clientUid);
      assert.ok(
        process.env.DISPLAY,
        "existing native graphical session required",
      );
      const nativeStat = await lstat(p.native.path);
      assert.ok(
        nativeStat.isFile() &&
          !nativeStat.isSymbolicLink() &&
          (nativeStat.mode & 0o022) === 0,
      );
      assert.equal(hash(await readFile(p.native.path)), p.native.sha256);
      const packageRaw = await readFile(p.packageReceipt);
      assert.equal(hash(packageRaw), p.packageReceiptSha256);
      const built = JSON.parse(packageRaw);
      assert.equal(built.source, p.source);
      for (const [name, want] of [
        ["fased-app", p.coreSha256],
        ["fased-signer", p.signerSha256],
        ["fased-desktop", p.native.sha256],
      ])
        assert.equal(built.components[name].sha256, want);
      const originalRaw = await privateOwnedFile(
        p.preserve.before,
        p.clientUid,
      );
      assert.equal(hash(originalRaw), p.preserve.beforeSha256);
      originalBefore = JSON.parse(originalRaw);
      assert.equal(originalBefore.ownerUid, p.preserve.ownerUid);
      assert.equal(
        originalBefore.files["install.json"],
        p.preserve.manifestSha256,
      );
      const raw = read(root + "/install.json"),
        enrollment = read(root + "/enrollment-ready.json"),
        preserved = read(preservedRoot + "/install.json");
      assert.equal(hash(raw), p.manifestSha256);
      assert.equal(hash(enrollment), p.enrollmentSha256);
      assert.equal(hash(preserved), p.preserve.manifestSha256);
      const fileHash = (path) =>
        installed("/usr/bin/sha256sum", [path]).split(/\s/)[0];
      const coreHash = fileHash(root + "/fased-app"),
        signerHash = fileHash(root + "/fased-signer");
      assert.equal(coreHash, p.coreSha256);
      assert.equal(signerHash, p.signerSha256);
      assert.equal(Number(installed("/usr/bin/id", ["-u"])), p.ownerUid);
      const status = cli(["custody", "status", "--disposable-fixture"]);
      assert.equal(status.active, true);
      return {
        uid: Number(installed("/usr/bin/id", ["-u"])),
        manifest: JSON.parse(raw),
        manifestSha256: hash(raw),
        enrollment: JSON.parse(enrollment),
        enrollmentSha256: hash(enrollment),
        peer: cli([
          "custody",
          "window",
          "--expected-instance",
          p.instance,
          "--network",
          status.manifest.network,
          "--disposable-fixture",
          "--inspect",
        ]),
        preservedManifestSha256: hash(preserved),
        coreSha256: coreHash,
        signerSha256: signerHash,
        accounts: cli(["accounts", "--data-dir", p.data]),
      };
    },
    read: () => cli(["agents", "--data-dir", p.data, "--profile", profile]),
    change: (body) =>
      cli(["agents", "--data-dir", p.data, "--request-stdin"], body),
    async draft() {
      const path = join(
        homedir(),
        ".local/state/fased/owner-backups",
        p.instance + ".trade-operation.json",
      );
      try {
        const s = await lstat(path);
        assert.ok(
          s.isFile() &&
            !s.isSymbolicLink() &&
            s.uid === p.clientUid &&
            (s.mode & 0o777) === 0o600,
        );
        return JSON.parse(await readFile(path, "utf8"));
      } catch (e) {
        if (e.code === "ENOENT") return null;
        throw e;
      }
    },
    async unlock() {
      try {
        const s = await lstat(p.privateFile);
        assert.ok(
          s.isFile() &&
            !s.isSymbolicLink() &&
            s.uid === p.clientUid &&
            (s.mode & 0o777) === 0o600,
        );
        const bytes = await readFile(p.privateFile);
        try {
          const v = JSON.parse(bytes);
          assert.ok(
            typeof v.unlock === "string" && /^[ -~]{16,128}$/.test(v.unlock),
          );
          return v.unlock;
        } finally {
          bytes.fill(0);
        }
      } catch {
        throw new Error("private fixture input unavailable or invalid");
      }
    },
    inspect: (intent, signerId) =>
      cli(
        [
          "custody",
          "window",
          "--expected-instance",
          p.instance,
          "--network",
          intent.network,
          "--disposable-fixture",
        ],
        {
          intent,
          signerId,
          phase: "inspect",
          unlock: null,
          backupPassword: null,
        },
      ),
    native: (step) => nativeControl(step),
    async runtime() {
      return {
        app: installed("/usr/bin/systemctl", [
          "show",
          "--property=MainPID",
          "--value",
          `fased-custody-${p.ownerUid}-app.service`,
        ]).trim(),
        signer: installed("/usr/bin/systemctl", [
          "show",
          "--property=MainPID",
          "--value",
          `fased-custody-${p.ownerUid}-signer.service`,
        ]).trim(),
        boot: read("/proc/sys/kernel/random/boot_id").trim(),
      };
    },
    lock: () =>
      JSON.parse(
        installed(root + "/fased-signer", [
          "owner",
          "--config",
          root + "/identity.json",
          "--disposable-fixture",
          "--action",
          "lock",
        ]),
      ),
    async preserved(final, startedAt) {
      assert.equal(
        hash(read(preservedRoot + "/install.json")),
        p.preserve.manifestSha256,
      );
      if (final) {
        const afterRaw = await privateOwnedFile(p.preserve.after, p.clientUid);
        const after = JSON.parse(afterRaw);
        verifyPreservedOriginal(
          originalBefore,
          after,
          p.preserve.ownerUid,
          startedAt,
        );
        return hash(afterRaw);
      }
    },
  };
}

// Accessible real controls, scoped to one frame. No WebKit/CDP hook, injected
// page script, fake popup or signer binding is used. Secrets travel only on stdin.
const nativeProbe = String.raw`
import json,sys,time,subprocess,pyatspi,os,hashlib,sqlite3
p=json.load(sys.stdin)
def walk(n,d=0,seen=None):
 if d>28:return
 if seen is None:seen=set()
 if n in seen:return
 if len(seen)>=2048:raise RuntimeError('native tree budget exceeded')
 seen.add(n)
 yield n
 try:
  for c in n:yield from walk(c,d+1,seen)
 except RuntimeError:raise
 except Exception:pass
def observed(n):
 if n.name:return n.name
 if n.getRoleName() not in ['description term','description value','table cell','paragraph','text','static','label','heading']:return ''
 try:
  value=n.queryText().getText(0,-1)
  return value if len(value)<=1024 else ''
 except NotImplementedError:return ''
def match():
 roots=[n for n in walk(pyatspi.Registry.getDesktop(0)) if n.getRoleName()=='frame' and n.name==p['frame']]
 if len(roots)!=1:return []
 return [n for n in walk(roots[0]) if observed(n)==p['name'] and (not p.get('role') or n.getRoleName()==p['role'])]
end=time.monotonic()+p.get('wait',8)
while True:
 ns=match()
 if len(ns)==1:break
 if time.monotonic()>=end:raise RuntimeError('accessible control unavailable')
 time.sleep(.05)
n=ns[0]
pid=next(x for x in walk(pyatspi.Registry.getDesktop(0)) if x.getRoleName()=='frame' and x.name==p['frame']).get_process_id()
verified=False
for _ in range(12):
 proc='/proc/'+str(pid)
 status=open(proc+'/status').read().splitlines()
 uid=int(next(x for x in status if x.startswith('Uid:')).split()[1])
 if uid!=p['clientUid']:raise RuntimeError('foreign native process')
 exe=os.path.realpath(proc+'/exe')
 if exe==p['expectedBinary']:
  if hashlib.sha256(open(exe,'rb').read()).hexdigest()!=p['expectedHash']:raise RuntimeError('native image changed')
  args=open(proc+'/cmdline','rb').read().split(b'\0')
  if p['frame']=='Fased Wallet':
   pos=args.index(b'--wallet-owner')
   if args[pos+1].decode()!=p['publicIntent']:raise RuntimeError('native target changed')
  else:
   pos=args.index(b'--data-dir')
   if args[pos+1].decode()!=p['data']:raise RuntimeError('native data changed')
  verified=True;break
 pid=int(next(x for x in status if x.startswith('PPid:')).split()[1])
 if pid<=1:break
if not verified:raise RuntimeError('unverified native owner frame')
if p['action']=='click':
 if not n.getState().contains(pyatspi.STATE_ENABLED):raise RuntimeError('control disabled')
 if not n.queryAction().doAction(0):raise RuntimeError('control refused')
elif p['action']=='set':
 x,y,w,h=n.queryComponent().getExtents(pyatspi.DESKTOP_COORDS)
 if w<=0 or h<=0:raise RuntimeError('control not visible')
 subprocess.run(['xdotool','mousemove',str(x+w//2),str(y+h//2),'click','1','key','ctrl+a'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 subprocess.run(['xdotool','type','--clearmodifiers','--delay','10','--file','-'],input=p['value'].encode(),check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 end=time.monotonic()+3
 stable=0
 while True:
  text=n.queryText().getText(0,-1)
  ok=len(text)==len(p['value']) if n.getRoleName()=='password text' else text==p['value']
  stable=stable+1 if ok else 0
  if stable>=3:break
  if time.monotonic()>=end:raise RuntimeError('incomplete native input')
  time.sleep(.05)
 subprocess.run(['xdotool','key','Tab'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
elif p['action']=='select-agent' or p['action']=='select' and p['name']=='Wallet':
 agent=p['action']=='select-agent'
 if agent and (p['name']!='Agent' or p['value']!='Trading Analyst'):raise RuntimeError('unexpected agent target')
 if not agent and (len(p.get('accountId',''))!=32 or any(c not in '0123456789abcdef' for c in p['accountId'])):raise RuntimeError('unexpected wallet target')
 options=[]
 for menu in walk(n):
  try:menu.querySelection()
  except NotImplementedError:continue
  if menu.childCount and all(menu[i].getRoleName()=='menu item' for i in range(menu.childCount)):
   options=[menu[i].name for i in range(menu.childCount)];break
 if not options or len(options)>30 or options.count(p['value'])!=1:raise RuntimeError('ambiguous native choices')
 i=options.index(p['value']);x,y,w,h=n.queryComponent().getExtents(pyatspi.DESKTOP_COORDS)
 if w<=0 or h<=0:raise RuntimeError('control not visible')
 subprocess.run(['xdotool','mousemove',str(x+w//2),str(y+h//2),'click','1'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 subprocess.run(['xdotool','key','--clearmodifiers','Home']+['Down']*i+['Return'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 storage=os.path.expanduser('~/.local/share/fased-desktop/localstorage/wails_wails_0.localstorage')
 st=os.lstat(storage)
 if st.st_uid!=p['clientUid'] or not os.path.isfile(storage) or os.path.islink(storage):raise RuntimeError('foreign native preferences')
 end=time.monotonic()+5
 while True:
  c=sqlite3.connect('file:'+storage+'?mode=ro',uri=True)
  try:row=c.execute('SELECT value FROM ItemTable WHERE key=?',(('fased.agent-profile.v1:'+p['instance']) if agent else ('fased.swap-draft.v1:'+p['instance']+':trading-analyst'),)).fetchone()
  finally:c.close()
  value=row[0].decode('utf-16-le') if row and isinstance(row[0],bytes) else row[0] if row else None
  if not agent and value is not None:value=json.loads(value).get('wallet')
  if value==('trading-analyst' if agent else p['accountId']):break
  if time.monotonic()>=end:raise RuntimeError('native choice did not commit')
  time.sleep(.05)
elif p['action']=='select':
 found=False;opened=False;end=time.monotonic()+2
 while not found:
  for menu in walk(n):
   try:sel=menu.querySelection()
   except NotImplementedError:continue
   for i in range(menu.childCount):
    if menu[i].name!=p['value']:continue
    try:current=sel.getSelectedChild(0)
    except Exception:current=None
    if current is None or current.name!=p['value']:
     if not sel.selectChild(i):raise RuntimeError('native selection unavailable')
     current=sel.getSelectedChild(0)
     if current.name!=p['value']:raise RuntimeError('native selection unavailable')
     action=current.queryAction()
     if action.nActions<1 or not action.doAction(0):raise RuntimeError('native selection unavailable')
    found=True;break
   if found:break
  if found:break
  if not opened:
   action=n.queryAction()
   if action.nActions<1 or not action.doAction(0):raise RuntimeError('native selection unavailable')
   opened=True
  if time.monotonic()>=end:raise RuntimeError('native selection unavailable')
  time.sleep(.05)
elif p['action']=='fact':
 parent=n.parent;idx=n.getIndexInParent()+1
 if idx>=parent.childCount or not any(observed(x)==p['value'] for x in walk(parent[idx])):raise RuntimeError('review fact changed')
elif p['action']!='present':raise RuntimeError('unknown native action')
print('{"observed":true}')
`;
function nativeControl(step) {
  try {
    return JSON.parse(
      execFileSync("/usr/bin/python3", ["-c", nativeProbe], {
        input: JSON.stringify(step),
        encoding: "utf8",
        timeout: 12000,
        stdio: ["pipe", "pipe", "pipe"],
      }),
    );
  } catch {
    throw new Error("native control failed: " + step.action + " " + step.name);
  }
}
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, test, ms = 10000) {
  const end = Date.now() + ms;
  do {
    const value = await fn();
    if (test(value)) return value;
    await pause(100);
  } while (Date.now() < end);
  throw new Error("installed observation deadline exceeded");
}
function proposalFromDraft(d, p) {
  assert.equal(d.intent.version, 2);
  assert.equal(d.intent.instanceId, p.instance);
  assert.equal(d.intent.action, "approve-swap");
  assert.equal(d.origin, p.origin);
  assert.equal(
    d.dispatched,
    false,
    "existing dispatch must recover, never reapprove",
  );
  const q = JSON.parse(Buffer.from(d.intent.proposal, "base64url").toString());
  assert.equal(q.version, "ordinary-controlled-swap-v1");
  assert.equal(q.fixtureOnly, true);
  assert.match(q.digest, hex);
  assert.equal(
    q.quote.inputMint,
    "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
  );
  assert.equal(
    q.quote.outputMint,
    "So11111111111111111111111111111111111111112",
  );
  assert.equal(q.quote.amount, "12500000");
  assert.equal(q.quote.slippageBps, 50);
  assert.equal(q.quote.maxFeeLamports, "5000");
  assert.ok(BigInt(q.quote.rentLamports) <= 6000000n);
  assert.ok(q.quote.expiresAt - q.quote.createdAt <= 60);
  assert.equal(q.quote.instanceId, p.instance);
  assert.equal(q.quote.accountId, p.accountId);
  assert.equal(q.quote.profileId, profile);
  assert.equal(d.intent.operationId, q.quote.id);
  assert.ok(q.quote.expiresAt * 1000 > Date.now());
  return q;
}
function taskResult(v, id) {
  const task = v.onchain.tasks.find((t) => t.swap?.proposal.quote.id === id);
  return (
    task && { task, run: v.onchain.runs.find((r) => r.taskId === task.id) }
  );
}

export async function installedNativeTradeJourney(
  p,
  io,
  previous,
  save,
  options = {},
) {
  validateInstalledTradePlan(p);
  const account = verifyInstalledTradeReceiver(p, await io.preflight());
  assert.ok(
    Object.keys(options).every((k) =>
      ["resumePreReview", "resolvedDraftSha256"].includes(k),
    ),
  );
  const resumePreReview = options.resumePreReview !== undefined;
  const reviewedRefusal =
    resumePreReview &&
    options.resolvedDraftSha256 !== undefined &&
    previous?.stage === "reviewed";
  if (resumePreReview) {
    assert.ok(previous && previous.result === "FAIL");
    assert.equal(
      previous.failure,
      reviewedRefusal
        ? "stopped at reviewed; inspect original before resuming"
        : "stopped at pre-review; inspect original before resuming",
    );
    assert.equal(
      options.resumePreReview,
      hash(JSON.stringify(previous)),
      "original failed receipt changed",
    );
    for (const key of [
      "stage",
      "intent",
      "proposal",
      "taskId",
      "runId",
      "signature",
      "signed",
    ])
      assert.ok(
        reviewedRefusal && ["stage", "intent", "proposal"].includes(key)
          ? previous[key] != null
          : previous[key] == null,
        "uncertain or repeated pre-review continuation refused",
      );
  }
  if (options.resolvedDraftSha256 !== undefined) {
    assert.match(options.resolvedDraftSha256, hex);
    assert.ok(resumePreReview && previous.preReviewFailure?.receipt);
    let prior = previous,
      depth = 0;
    while (prior) {
      assert.ok(++depth <= 8, "bounded unsent recovery history exceeded");
      assert.equal(prior.planHash, hash(JSON.stringify(p)));
      assert.equal(prior.result, "FAIL", "ancestor must remain failed");
      assert.equal(
        prior.failure,
        depth === 1 && reviewedRefusal
          ? "stopped at reviewed; inspect original before resuming"
          : "stopped at pre-review; inspect original before resuming",
      );
      if (prior.preReviewFailure) {
        assert.match(prior.preReviewFailure.sha256, hex);
        assert.equal(
          prior.preReviewFailure.sha256,
          hash(JSON.stringify(prior.preReviewFailure.receipt)),
          "ancestor digest changed",
        );
      }
      for (const key of [
        "stage",
        "intent",
        "proposal",
        "taskId",
        "runId",
        "signature",
        "signed",
      ])
        assert.ok(
          depth === 1 &&
            reviewedRefusal &&
            ["stage", "intent", "proposal"].includes(key)
            ? prior[key] != null
            : prior[key] == null,
          "original uncertain outcome must reconcile",
        );
      prior = prior.preReviewFailure?.receipt;
    }
  } else if (resumePreReview)
    assert.ok(
      previous.preReviewFailure == null,
      "repeated pre-review continuation refused",
    );
  const record = (resumePreReview ? structuredClone(previous) : previous) ?? {
    version: 1,
    planHash: hash(JSON.stringify(p)),
    source: p.source,
    profile: p.profile,
    instance: p.instance,
    manifestSha256: p.manifestSha256,
    enrollmentSha256: p.enrollmentSha256,
    startedAt: Date.now(),
    result: "OPEN",
    class: io.kind,
    fixtureOnly: true,
    mining: false,
    liveExecution: false,
    customerAutomatic: false,
  };
  assert.equal(record.planHash, hash(JSON.stringify(p)));
  assert.equal(record.class, io.kind);
  const current = await io.read();
  assert.equal(current.financialExecution, false);
  assert.equal(current.providerCallsEnabled, false);
  const runtime = await io.runtime();
  for (const n of [runtime.app, runtime.signer])
    assert.match(n, /^[1-9][0-9]*$/);
  assert.ok(runtime.boot);
  if (previous && !resumePreReview) {
    assert.ok(
      record.result === "NEEDS_RESTART" ||
        (record.result === "FAIL" && record.stage === "submitted"),
      "only an original dispatched result can recover; never start another quote",
    );
    assert.ok(record.proposal && record.intent);
    assert.deepEqual(record.proposal.account, account.signer);
    assert.ok(
      runtime.boot !== record.before.boot ||
        (runtime.app !== record.before.app &&
          runtime.signer !== record.before.signer),
      "actual app and signer restart required",
    );
    let finalView = current;
    let found = taskResult(current, record.proposal.quote.id);
    assert.ok(found?.run, "original result missing");
    assert.equal(found.task.swap.proposal.digest, record.proposal.digest);
    assert.equal(found.task.id, record.taskId);
    assert.equal(
      current.onchain.runs.filter((r) => r.taskId === found.task.id).length,
      1,
    );
    if (
      ["unknown", "sending", "submitted", "awaiting-receipt"].includes(
        found.run.swap.state,
      )
    ) {
      const v = await io.change({
        action: "swap-reconcile",
        profileId: profile,
        revision: current.revision,
        swap: { id: found.task.id },
      });
      finalView = v;
      found = taskResult(v, record.proposal.quote.id);
    }
    assert.equal(found.task.id, record.taskId);
    assert.equal(found.run.id, record.taskId + ":1");
    assert.equal(
      finalView.onchain.tasks.filter(
        (t) => t.swap?.proposal.quote.id === record.proposal.quote.id,
      ).length,
      1,
    );
    assert.equal(
      finalView.onchain.runs.filter((r) => r.taskId === record.taskId).length,
      1,
    );
    assert.equal(found.run.swap.state, "confirmed");
    if (record.runId) assert.equal(found.run.id, record.runId);
    const original = await io.inspect(
      record.intent,
      record.proposal.account.signerId,
    );
    assert.equal(original.readOnly, true);
    assert.equal(original.swap.state, "confirmed");
    assert.equal(original.status.locked, true);
    assert.equal(original.status.recoveryRequired, true);
    assert.equal(original.swap.proposal.digest, record.proposal.digest);
    assert.equal(found.run.swap.signature, original.swap.signature);
    assert.equal(found.run.swap.signed, original.swap.signed);
    if (record.signature) {
      assert.equal(found.run.swap.signature, record.signature);
      assert.equal(found.run.swap.signed, record.signed);
    }
    assert.equal(
      current.onchain.tasks.filter(
        (t) => t.swap?.proposal.quote.id === record.proposal.quote.id,
      ).length,
      1,
    );
    record.preservationAfterSha256 = await io.preserved(true, record.startedAt);
    assert.match(record.preservationAfterSha256, hex);
    record.after = runtime;
    record.runId = found.run.id;
    record.signature = found.run.swap.signature;
    record.signed = found.run.swap.signed;
    record.result = "PASS";
    record.recovered = true;
    await save(record);
    return record;
  }
  const existingDraft = await io.draft();
  if (options.resolvedDraftSha256 !== undefined) {
    assert.ok(existingDraft);
    assert.equal(
      hash(JSON.stringify(existingDraft)),
      options.resolvedDraftSha256,
    );
    assert.equal(existingDraft.completed, true);
    assert.equal(existingDraft.dispatched, false);
    assert.equal(existingDraft.origin, p.origin);
    const oldQuote = JSON.parse(
      Buffer.from(existingDraft.intent.proposal, "base64url").toString(),
    );
    assert.equal(existingDraft.intent.instanceId, p.instance);
    assert.equal(existingDraft.intent.network, account.network);
    assert.equal(oldQuote.quote.accountId, p.accountId);
    assert.deepEqual(oldQuote.account, account.signer);
    if (reviewedRefusal) {
      assert.deepEqual(existingDraft.intent, previous.intent);
      assert.deepEqual(oldQuote, previous.proposal);
      assert.equal(oldQuote.quote.profileId, profile);
    } else assert.notEqual(oldQuote.quote.profileId, profile);
    assert.ok(oldQuote.quote.expiresAt * 1000 <= Date.now());
    const original = await io.inspect(
      existingDraft.intent,
      account.signer.signerId,
    );
    assert.equal(original.readOnly, true);
    assert.equal(original.status.locked, true);
    assert.equal(
      original.swap ?? null,
      null,
      "original signer approval must be absent",
    );
    record.resolvedUnsentDraft = {
      sha256: options.resolvedDraftSha256,
      operationId: existingDraft.intent.operationId,
    };
  } else
    assert.equal(
      existingDraft,
      null,
      "existing owner draft requires explicit recovery; do not replace it",
    );
  assert.equal(current.onchain.available, true);
  assert.equal(
    current.onchain.tasks.length,
    0,
    "positive namespace is dedicated and empty",
  );
  assert.equal(
    current.onchain.runs.length,
    0,
    "no existing run may be replaced",
  );
  if (resumePreReview) {
    record.preReviewFailure = {
      sha256: options.resumePreReview,
      receipt: structuredClone(previous),
    };
    record.result = "OPEN";
    delete record.failure;
    if (reviewedRefusal) {
      delete record.stage;
      delete record.intent;
      delete record.proposal;
    }
  }
  record.before = runtime;
  await save(record);
  const gui = (action, name, role, value, frame = p.mainFrame) => {
    let publicIntent;
    if (frame === "Fased Wallet") {
      const q = new URLSearchParams({
        action: "approve-swap",
        instance: p.instance,
        network: record.intent.network,
        proposal: record.intent.proposal,
      });
      if (p.origin) q.set("origin", p.origin);
      q.sort();
      publicIntent = "fased://wallet-owner?" + q.toString();
    }
    return io.native({
      action,
      name,
      role,
      value,
      frame,
      clientUid: p.clientUid,
      expectedBinary: p.native.path,
      expectedHash: p.native.sha256,
      profile: p.profile,
      data: p.data,
      origin: p.origin,
      instance: p.instance,
      accountId: p.accountId,
      publicIntent,
    });
  };
  try {
    await gui("click", "Fased", "link");
    await gui("select-agent", "Agent", "combo box", "Trading Analyst");
    await gui("click", "Onchain");
    // Async choices recreate the native control; observe its option first.
    await gui("present", p.accountName, "menu item");
    await gui("select", "Wallet", "combo box", p.accountName);
    await gui("set", "Amount", "entry", "12.5");
    await gui("click", "Quote", "push button");
    await gui("click", "Approve", "push button");
    const draft = await until(
      () => io.draft(),
      (d) =>
        d !== null &&
        (!record.resolvedUnsentDraft ||
          d.intent.operationId !== record.resolvedUnsentDraft.operationId),
    );
    const proposal = proposalFromDraft(draft, p);
    assert.deepEqual(proposal.account, account.signer);
    assert.equal(proposal.quote.network, account.network);
    record.proposal = proposal;
    record.intent = draft.intent;
    record.stage = "reviewed";
    await save(record);
    const ownerFrame = "Fased Wallet";
    await gui("click", "Connect wallet", "push button", undefined, ownerFrame);
    await gui("present", proposal.digest, undefined, undefined, ownerFrame);
    await gui(
      "present",
      proposal.quote.owner,
      undefined,
      undefined,
      ownerFrame,
    );
    const unit = (n, d) => {
      const x = String(n).padStart(d + 1, "0"),
        f = x.slice(-d).replace(/0+$/, "");
      return x.slice(0, -d) + (f ? "." + f : "");
    };
    for (const [name, value] of [
      ["Pay", unit(proposal.quote.amount, 6) + " USDC"],
      ["Receive", unit(proposal.quote.output, 9) + " wSOL"],
      ["Minimum", unit(proposal.quote.minimumOutput, 9) + " wSOL"],
      ["Fee limit", unit(proposal.quote.maxFeeLamports, 9) + " SOL"],
      ["Rent", unit(proposal.quote.rentLamports, 9) + " SOL"],
      ["Expires", new Date(proposal.quote.expiresAt * 1000).toLocaleString()],
    ])
      await gui("fact", name, undefined, value, ownerFrame);
    const secret = await io.unlock();
    await gui("set", "Wallet password", "password text", secret, ownerFrame);
    await gui("click", "Approve", "push button", undefined, ownerFrame);
    await gui("present", "Approved", undefined, undefined, ownerFrame);
    await gui("click", "Done", "push button", undefined, ownerFrame);
    await gui("click", "Check approval", "push button");
    await gui("present", "Approved");
    record.stage = "approved";
    await save(record);
    // Persist possible dispatch before the real UI write. A lost UI reply is
    // recovered from the original core/signer records, never by another click.
    record.stage = "submitted";
    record.taskId = "onchain-task:" + proposal.quote.id;
    await save(record);
    await gui("click", "Submit", "push button");
    const result = await until(
      () => io.read(),
      (v) => taskResult(v, proposal.quote.id)?.run?.swap?.state === "confirmed",
    );
    const found = taskResult(result, proposal.quote.id);
    assert.equal(found.task.swap.proposal.digest, proposal.digest);
    assert.equal(found.task.id, record.taskId);
    assert.equal(
      result.onchain.runs.filter((r) => r.taskId === found.task.id).length,
      1,
    );
    assert.ok(found.run.swap.signature);
    assert.ok(found.run.swap.signed);
    assert.equal(
      result.onchain.tasks.filter(
        (t) => t.swap?.proposal.quote.id === proposal.quote.id,
      ).length,
      1,
    );
    await gui("present", "Confirmed");
    await io.lock();
    await io.preserved();
    const original = await io.inspect(record.intent, proposal.account.signerId);
    assert.equal(original.readOnly, true);
    assert.equal(original.swap.state, "confirmed");
    assert.equal(original.status.locked, true);
    assert.equal(original.swap.proposal.digest, proposal.digest);
    assert.equal(original.swap.signed, found.run.swap.signed);
    assert.equal(original.swap.signature, found.run.swap.signature);
    record.signature = found.run.swap.signature;
    record.signed = found.run.swap.signed;
    record.taskId = found.task.id;
    record.runId = found.run.id;
    record.result = "NEEDS_RESTART";
    await save(record);
    return record;
  } catch (error) {
    try {
      await io.lock();
    } catch {}
    if (record.intent) {
      try {
        await gui("click", "Close", "push button", undefined, "Fased Wallet");
      } catch {}
    }
    record.result = "FAIL";
    record.failure =
      "stopped at " +
      (record.stage ?? "pre-review") +
      "; inspect original before resuming";
    await save(record);
    throw error;
  }
}
export async function runInstalledNativeTradePlan(file) {
  assert.equal(process.env.FASED_DISPOSABLE_VM_ACCEPTANCE, "1");
  assert.ok(process.getuid() > 0);
  const s = await lstat(file);
  assert.ok(
    s.isFile() &&
      !s.isSymbolicLink() &&
      s.uid === process.getuid() &&
      (s.mode & 0o777) === 0o600,
  );
  const p = JSON.parse(await readFile(file, "utf8"));
  validateInstalledTradePlan(p);
  assert.equal(dirname(p.receipt), dirname(file));
  assert.equal(
    p.receipt.split("/").at(-1),
    "installed-native-onchain-result.json",
  );
  assert.ok(p.receipt !== p.privateFile && p.receipt !== p.packageReceipt);
  const dir = await lstat(dirname(file));
  assert.ok(
    dir.isDirectory() &&
      !dir.isSymbolicLink() &&
      dir.uid === process.getuid() &&
      (dir.mode & 0o777) === 0o700,
  );
  try {
    const r = await lstat(p.receipt);
    assert.ok(
      r.isFile() &&
        !r.isSymbolicLink() &&
        r.uid === process.getuid() &&
        (r.mode & 0o777) === 0o600,
    );
  } catch (e) {
    if (e.code !== "ENOENT") throw e;
  }

  let previous;
  try {
    previous = JSON.parse(await readFile(p.receipt, "utf8"));
  } catch (e) {
    if (e.code !== "ENOENT") throw e;
  }
  const resumePreReview =
    process.env.FASED_INSTALLED_NATIVE_TRADE_RESUME_PRE_REVIEW;
  if (resumePreReview !== undefined) {
    assert.match(resumePreReview, hex);
    assert.ok(previous);
    assert.equal(resumePreReview, hash(JSON.stringify(previous)));
  }
  const resolvedDraftSha256 =
    process.env.FASED_INSTALLED_NATIVE_TRADE_RESOLVED_DRAFT;
  if (resolvedDraftSha256 !== undefined) assert.match(resolvedDraftSha256, hex);
  let preservedFailure = false;
  const save = async (r) => {
    if (resumePreReview && !preservedFailure) {
      const original = await readFile(p.receipt);
      assert.equal(hash(JSON.stringify(JSON.parse(original))), resumePreReview);
      await writeFile(
        p.receipt + "." + resumePreReview + ".pre-review-failure.json",
        original,
        {
          mode: 0o600,
          flag: "wx",
        },
      );
      preservedFailure = true;
    }
    r.package = {
      core: p.coreSha256,
      signer: p.signerSha256,
      native: p.native.sha256,
    };
    await writeFile(p.receipt + ".next", JSON.stringify(r, null, 2) + "\n", {
      mode: 0o600,
      flag: "wx",
    });
    await rename(p.receipt + ".next", p.receipt);
  };
  const r = await installedNativeTradeJourney(
    p,
    installedTradeAdapters(p),
    previous,
    save,
    resumePreReview
      ? {
          resumePreReview,
          ...(resolvedDraftSha256 ? { resolvedDraftSha256 } : {}),
        }
      : {},
  );
  console.log(
    r.result +
      ": installed native Onchain " +
      p.profile +
      "; no rebuild or publication",
  );
  return r.result === "PASS" ? 0 : 3;
}
