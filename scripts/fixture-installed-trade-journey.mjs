// Opt-in extension of the managed-custody producer. No installer, build, OS
// privilege grant, token import, signer shortcut or replacement runtime lives here.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { constants } from "node:fs";
import {
  readFile,
  lstat,
  writeFile,
  rename,
  link,
  unlink,
  open,
} from "node:fs/promises";
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
  assert.ok(["local", "remote"].includes(p.profile), "unsupported profile");
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
  if (p.profile === "local") {
    assert.equal(p.origin, "");
    assert.equal(p.remoteUser, "");
  } else {
    const address = new URL(p.origin);
    assert.equal(address.protocol, "https:");
    assert.match(address.hostname, /^[a-z0-9-]+\.[a-z0-9-]+\.ts\.net$/);
    assert.equal(p.origin, address.origin);
    assert.equal(address.username + address.password + address.port, "");
    assert.equal(address.pathname, "/");
    assert.equal(address.search + address.hash, "");
    assert.match(p.remoteUser, /^[a-z_][a-z0-9_-]{0,31}$/);
    assert.notEqual(p.remoteUser, "root");
  }

  return {
    root: `/var/lib/fased-app-custody-${p.ownerUid}`,
    preservedRoot: `/var/lib/fased-app-custody-${p.preserve.ownerUid}`,
  };
}

export function verifyInstalledTradeReceiver(
  p,
  observed,
  { allowUnlocked = false } = {},
) {
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
  if (allowUnlocked) assert.equal(typeof st.locked, "boolean");
  else assert.equal(st.locked, true, "preflight starts locked");
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

// Remote commands remain a fixed, typed list. SSH uses the same strict pinned
// host/key-only policy as the existing native owner client; no shell text,
// copied session or password is accepted from the plan or browser.
export function remoteInstalledCommand(p, file, args) {
  const { root, preservedRoot } = validateInstalledTradePlan(p);
  assert.equal(p.profile, "remote");
  const fixed = [
    [root + "/fased-app", ["custody", "status", "--disposable-fixture"]],
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
        profile,
      ],
    ],
    [
      root + "/fased-app",
      [
        "custody",
        "agents",
        "--disposable-fixture",
        "--prepare-host",
        "--request-stdin",
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
    ["/usr/bin/id", ["-u"]],
    ...[
      root + "/install.json",
      root + "/enrollment-ready.json",
      preservedRoot + "/install.json",
      "/proc/sys/kernel/random/boot_id",
    ].map((path) => ["/usr/bin/cat", [path]]),
    ...[root + "/fased-app", root + "/fased-signer"].map((path) => [
      "/usr/bin/sha256sum",
      [path],
    ]),
    ...["app", "signer"].map((role) => [
      "/usr/bin/systemctl",
      [
        "show",
        "--property=MainPID",
        "--value",
        `fased-custody-${p.ownerUid}-${role}.service`,
      ],
    ]),
  ];
  for (const network of ["devnet", "mainnet-beta"]) {
    const base = [
      "custody",
      "window",
      "--expected-instance",
      p.instance,
      "--network",
      network,
      "--disposable-fixture",
    ];
    fixed.push(
      [root + "/fased-app", base],
      [root + "/fased-app", [...base, "--inspect"]],
    );
  }
  assert.ok(
    fixed.some(
      ([candidate, values]) =>
        candidate === file && JSON.stringify(values) === JSON.stringify(args),
    ),
    "unexpected Remote owner command",
  );
  const host = new URL(p.origin).hostname;
  return {
    file: "/usr/bin/ssh",
    args: [
      "-F",
      "/dev/null",
      "-T",
      "-a",
      "-oBatchMode=yes",
      "-oStrictHostKeyChecking=yes",
      "-oUserKnownHostsFile=" + join(homedir(), ".ssh", "known_hosts"),
      "-oGlobalKnownHostsFile=/dev/null",
      "-oPasswordAuthentication=no",
      "-oKbdInteractiveAuthentication=no",
      "-oClearAllForwardings=yes",
      "-oPermitLocalCommand=no",
      "-oProxyCommand=none",
      "-oControlMaster=no",
      "-oControlPath=none",
      "-oConnectTimeout=10",
      "-oConnectionAttempts=1",
      "-l",
      p.remoteUser,
      "--",
      host,
      file,
      ...args,
    ],
  };
}

export function allowedOwnerLink(p, value) {
  const u = new URL(value);
  const q = u.searchParams;
  return (
    u.protocol === "fased:" &&
    u.hostname === "wallet-owner" &&
    !u.username &&
    !u.password &&
    !u.port &&
    !u.pathname &&
    !u.hash &&
    [...q.keys()].sort().join(",") ===
      "action,instance,network,origin,proposal" &&
    q.get("action") === "approve-swap" &&
    q.get("instance") === p.instance &&
    q.get("origin") === p.origin &&
    ["devnet", "mainnet-beta"].includes(q.get("network")) &&
    /^[A-Za-z0-9_-]{1,16384}$/.test(q.get("proposal") ?? "")
  );
}

async function openRemoteTradeBrowser(p) {
  const { chromium } = await import("@playwright/test");
  const browser = await chromium.launch({
    headless: false,
    ...(process.env.FASED_TEST_BROWSER
      ? { executablePath: process.env.FASED_TEST_BROWSER }
      : {}),
  });
  try {
    // A fresh browser obtains its own normal Tailscale/device session. No
    // storage state, CLI token or owner session is imported into this context.
    const context = await browser.newContext({ locale: "en-US" });
    const blocked = [];
    await context.route("**/*", async (route) => {
      const url = route.request().url();
      let permitted = false;
      try {
        permitted =
          new URL(url).origin === p.origin || allowedOwnerLink(p, url);
      } catch {
        /* refuse */
      }
      if (!permitted) {
        blocked.push(true);
        return route.abort();
      }
      return route.continue();
    });
    const page = await context.newPage();
    await page.goto(p.origin + "/?instance=" + encodeURIComponent(p.instance));
    await until(
      async () => {
        try {
          return await remoteBrowserRequest(page, "auth/state");
        } catch {
          return null;
        }
      },
      (v) =>
        v?.authenticated === true &&
        v.remoteAccess === true &&
        v.remoteEnrolled === true,
      30000,
    );
    assert.deepEqual(blocked, [], "unexpected external browser request");
    return {
      page,
      assertPrivate: () =>
        assert.deepEqual(blocked, [], "unexpected external browser request"),
      close: () => browser.close(),
    };
  } catch (error) {
    await browser.close();
    throw error;
  }
}

// Read held capability flags through the authenticated browser's existing
// same-origin API, not through the narrower tokenless CLI projection.
async function remoteBrowserRequest(page, route) {
  assert.ok(
    ["health", "auth/state", "agents?profile=trading-analyst"].includes(route),
  );
  return page.evaluate(
    async ({ route }) => {
      const token = sessionStorage.getItem("fased.core.session.v1");
      const headers = token ? { Authorization: `Bearer ${token}` } : {};
      const response = await fetch("/api/v1/" + route, {
        headers,
        credentials: "same-origin",
      });
      if (!response.ok)
        throw new Error(`Remote read failed: HTTP ${response.status}`);
      return response.json();
    },
    { route },
  );
}

export async function remoteHeldStatus(p, page) {
  validateInstalledTradePlan(p);
  assert.equal(p.profile, "remote");
  const health = await remoteBrowserRequest(page, "health");
  const auth = await remoteBrowserRequest(page, "auth/state");
  const view = await remoteBrowserRequest(
    page,
    "agents?profile=" + encodeURIComponent(profile),
  );
  assert.equal(health.status, "ok");
  assert.equal(health.instanceId, p.instance);
  assert.equal(health.connection?.location, "remote");
  assert.equal(health.connection?.transport, "private-https");
  for (const key of ["aiConnections", "financialExecution", "walletSigner"])
    assert.equal(health.capabilities?.[key], false);
  assert.equal(auth.authenticated, true);
  assert.equal(auth.remoteAccess, true);
  assert.equal(auth.remoteEnrolled, true);
  return {
    instanceId: health.instanceId,
    profileId: view.profileId,
    revision: view.revision,
    financialExecution: view.financialExecution,
    providerCallsEnabled: view.providerCallsEnabled,
  };
}

// Main Remote controls are in the actual browser. The wallet owner window
// continues through nativeControl and retains its binary/intent verification.
export async function remoteBrowserControl(p, page, step) {
  const current = new URL(page.url());
  assert.equal(current.origin, p.origin);
  assert.equal(current.searchParams.get("instance"), p.instance);
  assert.equal(step.frame, p.mainFrame);
  if (step.action === "click") {
    const role = step.role === "push button" ? "button" : step.role;
    assert.ok(["button", "link"].includes(role));
    let target = page;
    if (step.name === "Chat")
      target = page.getByRole("navigation", {
        name: "Main navigation",
        exact: true,
      });
    if (step.name === "Onchain")
      target = page.getByRole("navigation", {
        name: "More pages",
        exact: true,
      });
    await target.getByRole(role, { name: step.name, exact: true }).click();
  } else if (step.action === "set") {
    assert.equal(step.name, "Amount");
    const field = page.getByLabel(step.name, { exact: true });
    await field.fill(step.value);
    assert.equal(await field.inputValue(), step.value);
  } else if (step.action === "select-agent" || step.action === "select") {
    const agent = step.action === "select-agent";
    assert.equal(step.name, agent ? "Agent" : "Wallet");
    const value = agent ? profile : p.accountId;
    const field = page.getByRole("combobox", { name: step.name, exact: true });
    const option = field.getByRole("option", {
      name: step.value,
      exact: true,
      includeHidden: true,
    });
    assert.equal(await option.count(), 1, "ambiguous browser choice");
    await field.selectOption(value);
    await until(
      async () => {
        if ((await field.inputValue()) !== value) return false;
        return page.evaluate(
          ({ agent, instance }) => {
            const key = agent
              ? `fased.agent-profile.v1:${instance}`
              : `fased.swap-draft.v1:${instance}:trading-analyst`;
            const raw = localStorage.getItem(key);
            return agent ? raw : raw && JSON.parse(raw).wallet;
          },
          { agent, instance: p.instance },
        );
      },
      (selected) => selected === value,
      5000,
    );
  } else if (step.action === "present") {
    const locator =
      step.role === "menu item"
        ? page.getByRole("option", {
            name: step.name,
            exact: true,
            includeHidden: true,
          })
        : page.getByText(step.name, { exact: true });
    await until(
      async () =>
        (await locator.count()) === 1 &&
        (step.role === "menu item" || (await locator.isVisible())),
      Boolean,
    );
  } else {
    throw new Error("unsupported Remote browser control");
  }
}

// Commands cannot be supplied by a browser or plan. Both profiles use the
// existing installed owner routes and grant no additional OS authority.
export function installedTradeAdapters(p) {
  const { root, preservedRoot } = validateInstalledTradePlan(p);
  let remoteBrowser;
  const exec = (file, args, input) =>
    execFileSync(file, args, {
      input,
      encoding: "utf8",
      timeout: 15000,
      maxBuffer: 2 * 1024 * 1024,
      stdio: ["pipe", "pipe", "pipe"],
    });
  const installed = (file, args, input) => {
    if (p.profile === "local") return exec(file, args, input);
    const remote = remoteInstalledCommand(p, file, args);
    return exec(remote.file, remote.args, input);
  };

  const cli = (args, body) =>
    JSON.parse(
      installed(
        root + "/fased-app",
        args,
        body === undefined ? undefined : JSON.stringify(body),
      ),
    );
  const accountArgs =
    p.profile === "remote"
      ? ["custody", "accounts", "--disposable-fixture", "--prepare-host"]
      : ["accounts", "--data-dir", p.data];
  const agentArgs =
    p.profile === "remote"
      ? ["custody", "agents", "--disposable-fixture", "--prepare-host"]
      : ["agents", "--data-dir", p.data];
  const read = (path) => installed("/usr/bin/cat", [path]);
  let originalBefore;
  return {
    kind:
      p.profile === "remote"
        ? "installed-remote-browser-native-owner"
        : "installed-native",
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
        accounts: cli(accountArgs),
      };
    },
    read: () => cli([...agentArgs, "--profile", profile]),
    change: (body) => cli([...agentArgs, "--request-stdin"], body),
    async heldStatus() {
      assert.equal(p.profile, "remote");
      remoteBrowser ??= await openRemoteTradeBrowser(p);
      const result = await remoteHeldStatus(p, remoteBrowser.page);
      remoteBrowser.assertPrivate();
      return result;
    },
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
    async native(step) {
      if (p.profile === "remote" && step.frame === p.mainFrame) {
        await remoteBrowserControl(p, remoteBrowser.page, step);
        remoteBrowser.assertPrivate();
        return;
      }
      return nativeControl(step);
    },
    async close() {
      if (remoteBrowser) {
        try {
          remoteBrowser.assertPrivate();
        } finally {
          await remoteBrowser.close();
        }
      }
    },
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
  assert.equal(d.origin, p.origin);
  assert.equal(
    d.dispatched,
    false,
    "existing dispatch must recover, never reapprove",
  );
  return proposalFromIntent(d.intent, p, false);
}
function proposalFromIntent(intent, p, allowExpired) {
  assert.equal(intent.version, 2);
  assert.equal(intent.instanceId, p.instance);
  assert.equal(intent.action, "approve-swap");
  const q = JSON.parse(Buffer.from(intent.proposal, "base64url").toString());
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
  assert.match(q.quote.id, /^[a-f0-9]{32}$/);
  assert.equal(intent.operationId, q.quote.id);
  if (!allowExpired) assert.ok(q.quote.expiresAt * 1000 > Date.now());
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
  assert.ok(
    Object.keys(options).every((k) =>
      ["resumePreReview", "resolvedDraftSha256"].includes(k),
    ),
  );
  const resumePreReview = options.resumePreReview !== undefined;
  const boundReceipt =
    previous &&
    previous.version === 1 &&
    previous.planHash === hash(JSON.stringify(p)) &&
    previous.source === p.source &&
    previous.profile === p.profile &&
    previous.instance === p.instance &&
    previous.class === io.kind &&
    previous.manifestSha256 === p.manifestSha256 &&
    previous.enrollmentSha256 === p.enrollmentSha256;
  const openPreReview =
    boundReceipt &&
    previous.result === "OPEN" &&
    [
      "stage",
      "intent",
      "proposal",
      "taskId",
      "runId",
      "signature",
      "signed",
      "failure",
    ].every((k) => previous[k] == null) &&
    Number.isSafeInteger(previous.startedAt) &&
    previous.startedAt > 0 &&
    previous.before?.boot &&
    [previous.before.app, previous.before.signer].every((n) =>
      /^[1-9][0-9]*$/.test(n),
    );
  const interrupted =
    !resumePreReview &&
    boundReceipt &&
    ["OPEN", "FAIL", "RECOVERY_REQUIRED"].includes(previous.result) &&
    ["reviewed", "approved", "submitted"].includes(previous.stage);
  const observed = await io.preflight();
  const account = verifyInstalledTradeReceiver(p, observed, {
    allowUnlocked: !!interrupted,
  });
  if (interrupted) {
    const originalProposal = proposalFromIntent(previous.intent, p, true);
    assert.deepEqual(originalProposal, previous.proposal);
    assert.deepEqual(originalProposal.account, account.signer);
    assert.equal(originalProposal.quote.network, account.network);
    if (!observed.accounts.fixture.status.locked) {
      await io.lock();
      const locked = await io.inspect(previous.intent, account.signer.signerId);
      assert.equal(locked.readOnly, true);
      assert.equal(locked.status.locked, true);
      assert.equal(locked.status.recoveryRequired, true);
      if (locked.swap)
        assert.equal(locked.swap.proposal.digest, previous.proposal.digest);
    }
  }
  const reviewedRefusal =
    resumePreReview &&
    options.resolvedDraftSha256 !== undefined &&
    previous?.stage === "reviewed";
  if (resumePreReview) {
    assert.ok(previous && (previous.result === "FAIL" || openPreReview));
    if (!openPreReview)
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
    assert.ok(resumePreReview);
  }
  if (resumePreReview) {
    let prior = previous,
      child,
      depth = 0;
    while (prior) {
      assert.ok(++depth <= 8, "bounded unsent recovery history exceeded");
      assert.equal(prior.planHash, hash(JSON.stringify(p)));
      const reviewedAncestor = prior.stage === "reviewed";
      if (prior.result === "OPEN") {
        assert.equal(
          prior.stage,
          undefined,
          "open ancestor has uncertain work",
        );
        assert.equal(prior.failure, undefined);
        assert.equal(prior.instance, p.instance);
        assert.equal(prior.source, p.source);
        assert.equal(prior.profile, p.profile);
        assert.equal(prior.class, io.kind);
        assert.equal(prior.manifestSha256, p.manifestSha256);
        assert.equal(prior.enrollmentSha256, p.enrollmentSha256);
      } else {
        assert.equal(prior.result, "FAIL", "ancestor must remain failed");
        assert.equal(
          prior.failure,
          reviewedAncestor
            ? "stopped at reviewed; inspect original before resuming"
            : "stopped at pre-review; inspect original before resuming",
        );
      }
      if (reviewedAncestor) {
        assert.equal(prior.result, "FAIL");
        if (depth === 1) assert.ok(reviewedRefusal);
        else {
          assert.match(child?.resolvedUnsentDraft?.sha256 ?? "", hex);
          assert.equal(
            child.resolvedUnsentDraft.operationId,
            prior.intent?.operationId,
          );
          assert.deepEqual(
            proposalFromIntent(prior.intent, p, true),
            prior.proposal,
          );
          assert.deepEqual(prior.proposal.account, account.signer);
          assert.equal(prior.proposal.quote.network, account.network);
          assert.ok(prior.proposal.quote.expiresAt * 1000 <= Date.now());
          const original = await io.inspect(
            prior.intent,
            account.signer.signerId,
          );
          assert.equal(original.readOnly, true);
          assert.equal(original.status.locked, true);
          assert.equal(
            original.swap ?? null,
            null,
            "ancestor signer approval must be absent",
          );
        }
      }
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
          reviewedAncestor && ["stage", "intent", "proposal"].includes(key)
            ? prior[key] != null
            : prior[key] == null,
          "original uncertain outcome must reconcile",
        );
      child = prior;
      prior = prior.preReviewFailure?.receipt;
    }
  }
  const record = (previous ? structuredClone(previous) : undefined) ?? {
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
  const held = p.profile === "remote" ? await io.heldStatus() : current;
  if (p.profile === "remote") {
    assert.equal(held.instanceId, p.instance);
    assert.equal(held.profileId, profile);
    assert.equal(held.revision, current.revision);
  }
  assert.equal(held.financialExecution, false);
  assert.equal(held.providerCallsEnabled, false);
  const runtime = await io.runtime();
  for (const n of [runtime.app, runtime.signer])
    assert.match(n, /^[1-9][0-9]*$/);
  assert.ok(runtime.boot);
  if (openPreReview && !resumePreReview) {
    const originalDraft = await io.draft();
    return {
      ...record,
      result: "RECOVERY_REQUIRED",
      failure: "inspect the original pre-review state before resuming",
      ...(originalDraft
        ? { draftSha256: hash(JSON.stringify(originalDraft)) }
        : {}),
    };
  }
  if (previous && !resumePreReview) {
    assert.ok(
      record.result === "NEEDS_RESTART" || interrupted,
      "only an original dispatched result can recover; never start another quote",
    );
    assert.ok(record.proposal && record.intent);
    assert.deepEqual(
      proposalFromIntent(record.intent, p, true),
      record.proposal,
    );
    assert.deepEqual(record.proposal.account, account.signer);
    assert.equal(record.proposal.quote.network, account.network);
    const expectedTaskId = "onchain-task:" + record.proposal.quote.id;
    if (record.taskId) assert.equal(record.taskId, expectedTaskId);
    assert.ok(record.before && record.before.boot);
    for (const n of [record.before.app, record.before.signer])
      assert.match(n, /^[1-9][0-9]*$/);
    const restarted =
      runtime.boot !== record.before.boot ||
      (runtime.app !== record.before.app &&
        runtime.signer !== record.before.signer);
    if (record.result === "NEEDS_RESTART")
      assert.ok(restarted, "actual app and signer restart required");
    let finalView = current;
    let found = taskResult(current, record.proposal.quote.id);
    assert.ok(
      current.onchain.tasks.filter(
        (t) => t.swap?.proposal.quote.id === record.proposal.quote.id,
      ).length <= 1,
      "duplicate original task",
    );
    if (!found?.run) {
      const original = await io.inspect(record.intent, account.signer.signerId);
      assert.equal(original.readOnly, true);
      assert.equal(original.status.locked, true);
      assert.equal(original.status.recoveryRequired, true);
      if (original.swap)
        assert.equal(original.swap.proposal.digest, record.proposal.digest);
      // The original may still be approved, queued or uncertain. Preserve the
      // receipt and hold; never issue another UI Submit to fill the gap.
      return {
        ...record,
        result: "RECOVERY_REQUIRED",
        failure:
          "original result unavailable; inspect or reconcile without resubmission",
      };
    }
    assert.equal(found.task.swap.proposal.digest, record.proposal.digest);
    assert.equal(found.task.id, expectedTaskId);
    record.taskId = expectedTaskId;
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
    if (found?.run?.swap?.state !== "confirmed")
      return {
        ...record,
        result: "RECOVERY_REQUIRED",
        failure: "original result remains unresolved; do not resubmit",
      };
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
    if (!restarted) {
      record.runId = found.run.id;
      record.signature = found.run.swap.signature;
      record.signed = found.run.swap.signed;
      record.result = "NEEDS_RESTART";
      await save(record);
      return record;
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
    } else if (oldQuote.quote.profileId === profile) {
      assert.deepEqual(
        proposalFromIntent(existingDraft.intent, p, true),
        oldQuote,
      );
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
    if (p.profile === "remote") await gui("click", "Chat", "link");
    await gui("select-agent", "Agent", "combo box", "Trading Analyst");
    if (p.profile === "remote") await gui("click", "More", "push button");
    await gui("click", "Onchain", p.profile === "remote" ? "link" : undefined);
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
    if (p.profile === "remote") {
      await gui("set", "Private address", "entry", p.origin, ownerFrame);
      await gui("set", "OS account", "entry", p.remoteUser, ownerFrame);
    }
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
// Interrupted writes cannot reserve a fixed .next name forever. Never remove
// old staging files: they may be the only surviving evidence of an attempt.
async function writeReceiptAtomically(
  target,
  content,
  preserveExisting = false,
) {
  const temp = target + ".stage-" + randomBytes(16).toString("hex");
  let created = false;
  try {
    await writeFile(temp, content, { mode: 0o600, flag: "wx" });
    created = true;
    if (preserveExisting) {
      try {
        await link(temp, target);
      } catch (error) {
        if (error.code !== "EEXIST") throw error;
        const existing = await privateOwnedFile(target, process.getuid());
        assert.ok(
          existing.equals(Buffer.from(content)),
          "original receipt ancestry changed",
        );
      }
    } else await rename(temp, target);
  } finally {
    if (created) {
      try {
        await unlink(temp);
      } catch (error) {
        if (error.code !== "ENOENT") throw error;
      }
    }
  }
}

export function createInstalledTradeReceiptWriter(p, resumePreReview) {
  let preservedFailure = false;
  return async (r) => {
    if (resumePreReview && !preservedFailure) {
      const original = await privateOwnedFile(p.receipt, process.getuid());
      assert.equal(hash(JSON.stringify(JSON.parse(original))), resumePreReview);
      await writeReceiptAtomically(
        p.receipt + "." + resumePreReview + ".pre-review-failure.json",
        original,
        true,
      );
      preservedFailure = true;
    }
    r.package = {
      core: p.coreSha256,
      signer: p.signerSha256,
      native: p.native.sha256,
    };
    await writeReceiptAtomically(p.receipt, JSON.stringify(r, null, 2) + "\n");
  };
}

// The existing private evidence directory is the lock target. The kernel drops
// the lock when its last descriptor closes, including after process death.
export async function withInstalledTradeLock(directory, run) {
  const expected = await lstat(directory);
  assert.ok(
    expected.isDirectory() &&
      !expected.isSymbolicLink() &&
      expected.uid === process.getuid() &&
      (expected.mode & 0o777) === 0o700,
    "private acceptance directory required",
  );
  const fd = await open(
    directory,
    constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW,
  );
  try {
    const actual = await fd.stat();
    assert.equal(actual.dev, expected.dev);
    assert.equal(actual.ino, expected.ino);
    try {
      execFileSync("/usr/bin/flock", ["--exclusive", "--nonblock", "3"], {
        stdio: ["ignore", "ignore", "pipe", fd.fd],
        timeout: 5000,
      });
    } catch (error) {
      throw new Error(
        "another acceptance runner is running or the OS lock is unavailable",
        { cause: error },
      );
    }
    return await run();
  } finally {
    await fd.close();
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
  return withInstalledTradeLock(dirname(file), async () => {
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
    if (resolvedDraftSha256 !== undefined)
      assert.match(resolvedDraftSha256, hex);
    const save = createInstalledTradeReceiptWriter(p, resumePreReview);
    const adapters = installedTradeAdapters(p);
    let r;
    try {
      r = await installedNativeTradeJourney(
        p,
        adapters,
        previous,
        save,
        resumePreReview
          ? {
              resumePreReview,
              ...(resolvedDraftSha256 ? { resolvedDraftSha256 } : {}),
            }
          : {},
      );
    } finally {
      await adapters.close();
    }
    console.log(
      r.result +
        ": installed Onchain " +
        p.profile +
        "; no rebuild or publication",
    );
    return r.result === "PASS" ? 0 : 3;
  });
}
