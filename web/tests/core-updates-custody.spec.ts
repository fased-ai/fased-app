import { openOwnerSetup } from "../../scripts/fixture-owner-setup.mjs";
import { expect, test } from "@playwright/test";
import {
  refreshFixtureAccounts,
  revealFixtureAccountEntry,
  revealFixtureApproval,
  verifiedFixtureApproval,
} from "../../scripts/fixture-account-journey.mjs";
import { spawn, type ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { once } from "node:events";
import { createHash, randomBytes } from "node:crypto";
import {
  installCoreFixture,
  captureScreenshot,
  choosePasswordSignIn,
  revealControl,
  openUpdateSettings,
  exec,
  binary,
  fixturePassword,
  ownedFixtureOrigins,
  dir,
  url,
  output,
  start,
  stop,
  privateCLI,
} from "./helpers/core-fixture.ts";

installCoreFixture();

test("Updates keeps manual and automatic preferences without activating releases", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#settings");
  const panel = page.getByRole("region", { name: "Updates", exact: true });
  await expect(panel.getByLabel("Update mode")).toHaveValue("manual");
  await expect(
    panel.getByRole("button", { name: "Check", exact: true }),
  ).toBeDisabled();
  await expect(panel).toContainText("Release channel unavailable");
  await expect(page.getByLabel("Pinned sequence", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByLabel("Manifest SHA-256", { exact: true }),
  ).toHaveCount(0);
  await panel.getByLabel("Update mode").selectOption("automatic");
  await expect
    .poll(
      async () => (await privateCLI(["updates", "preferences"])).policy.mode,
    )
    .toBe("automatic");
  await panel.getByText("Schedule and version", { exact: true }).click();
  await panel.getByLabel("Maintenance window", { exact: true }).check();
  await expect
    .poll(
      async () =>
        (await privateCLI(["updates", "preferences"])).policy.window?.start,
    )
    .toBe("02:00");
  const v = await privateCLI(["updates", "status"]);
  expect(v.controllerAvailable).toBe(false);
  expect(v.activationEnabled).toBe(false);
  expect(v.operation.phase).toBe("idle");
  await stop();
  await start();
  await page.goto(url + "/#settings");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(panel.getByLabel("Update mode")).toHaveValue("automatic");
  await expect(panel).toContainText("Automatic installation is not active.");
  expect(
    (await privateCLI(["updates", "preferences"])).policy.window.start,
  ).toBe("02:00");
});

test("Updates checks read-only desktop release manifest when controller is unavailable", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  await page.route("**/api/v1/updates/desktop-release", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        installedVersion: "0.1.36",
        latestVersion: "0.1.37",
        releaseTag: "v0.1.37-desktop",
        releaseUrl:
          "https://github.com/fased-ai/fased-app/releases/tag/v0.1.37-desktop",
        updateAvailable: true,
        signed: false,
        platform: "linux",
        arch: "amd64",
        assets: [
          {
            name: "fased-app-0.1.37-linux-amd64.AppImage",
            kind: "desktop",
            os: "linux",
            arch: "amd64",
            url: "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/fased-app-0.1.37-linux-amd64.AppImage",
            sha256: "b".repeat(64),
            bytes: 55_900_000,
          },
        ],
        checkedAt: 1795400000,
      }),
    });
  });

  await page.goto(url + "/#settings");
  const panel = page.getByRole("region", { name: "Updates", exact: true });
  await expect(panel).toContainText("Release channel unavailable");
  await panel
    .getByRole("button", { name: "Check desktop release", exact: true })
    .click();

  await expect(panel).toContainText("v0.1.37");
  await expect(panel).toContainText("Update available");
  await expect(panel).toContainText("Unsigned");
  await expect(
    panel.getByRole("link", {
      name: "fased-app-0.1.37-linux-amd64.AppImage",
      exact: true,
    }),
  ).toHaveAttribute(
    "href",
    "https://github.com/fased-ai/fased-app/releases/download/v0.1.37-desktop/fased-app-0.1.37-linux-amd64.AppImage",
  );
  await expect(panel).toContainText("bbbbbbbbbbbb");
  await expect(
    panel.getByRole("link", { name: "Release notes", exact: true }),
  ).toHaveAttribute(
    "href",
    "https://github.com/fased-ai/fased-app/releases/tag/v0.1.37-desktop",
  );
});

test("update status shares UI and private CLI observations across pause, release and restart", async ({
  page,
}) => {
  expect((await fetch(url + "/api/v1/updates/status")).status).toBe(401);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#settings");
  await openUpdateSettings(page);
  const panel = page.getByRole("region", { name: "Updates & recovery" });
  await expect(
    panel.getByRole("heading", { name: "Signer not configured" }),
  ).toBeVisible();
  await expect(panel.getByText("Unknown", { exact: true })).toHaveCount(3);
  const cli = async (args: string[], input?: unknown) => {
    if (input === undefined)
      return JSON.parse(
        (await exec(binary, [...args, "--data-dir", dir])).stdout,
      );
    const child = spawn(binary, [...args, "--data-dir", dir]);
    let output = "",
      error = "";
    child.stdout!.on("data", (b) => (output += b));
    child.stderr!.on("data", (b) => (error += b));
    child.stdin!.end(JSON.stringify(input));
    expect((await once(child, "exit"))[0], error).toBe(0);
    return JSON.parse(output);
  };
  const before = await cli(["update-status"]);
  expect(before.core.phase).toBe("idle");
  expect(before.automaticUpdates).toBe(false);
  const probe = randomBytes(16).toString("hex");
  const snapshot = await cli(["update-inventory", "--probe-id", probe]);
  const request = {
    version: 1,
    intentId: randomBytes(16).toString("hex"),
    sequence: "1",
    manifestSha256: createHash("sha256")
      .update("disposable browser fence; no signer authority")
      .digest("hex"),
    expected: snapshot,
  };
  await cli(["update-pause", "--request-stdin"], {
    action: "pause",
    probeId: probe,
    request,
  });
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(
    panel.getByRole("heading", { name: "Metadata paused", exact: true }),
  ).toBeVisible();
  await expect(
    panel.getByText(request.intentId, { exact: true }),
  ).toBeVisible();
  const held = await cli(["update-status"]);
  expect(held.core).toEqual({ phase: "paused", intentId: request.intentId });
  expect(held.signer).toEqual(before.signer);
  expect(held.guidance.some((g: { code: string }) => g.code === "paused")).toBe(
    true,
  );
  await captureScreenshot(page, {
    path: `.evidence/p4-status/paused-${test.info().project.name}.png`,
    fullPage: true,
  });
  const token = await page.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  expect(
    (
      await page.request.post(`${url}/api/v1/updates/pause`, {
        headers: { Authorization: "Bearer " + token, Origin: url },
        data: {},
      })
    ).status(),
  ).toBe(403);
  expect(
    (
      await page.request.post(`${url}/api/v1/updates/status`, {
        headers: { Authorization: "Bearer " + token },
        data: {},
      })
    ).status(),
  ).toBe(405);
  await stop();
  await start();
  await page.goto(url + "/#settings");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await openUpdateSettings(page);
  await expect(
    panel.getByRole("heading", { name: "Metadata paused", exact: true }),
  ).toBeVisible();
  await cli(["update-pause", "--request-stdin"], {
    action: "release",
    probeId: probe,
    aborted: {
      id: request.intentId,
      sequence: "1",
      manifestSha256: request.manifestSha256,
      core: snapshot,
      state: "aborted",
    },
  });
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(
    panel.getByRole("heading", { name: "Signer not configured", exact: true }),
  ).toBeVisible();
  await expect(panel.getByText("released", { exact: true })).toBeVisible();
  expect((await cli(["update-status"])).core.phase).toBe("released");
});

test("update recovery view rejects unsupported or lost status without retaining a healthy observation (UI mock only)", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  let fail = false,
    unsupported = false;
  const observation = {
    version: 1,
    observedAt: new Date().toISOString(),
    readOnly: true,
    state: "recovery-required",
    core: { phase: "paused" },
    signer: {
      state: "available",
      locked: true,
      updateHeld: true,
      recoveryRequired: true,
    },
    guidance: [
      {
        code: "recovery-required",
        message: "Preserve keys, original records, counters and holds.",
        command:
          "fased-app custody updates inventory --disposable-fixture --prepare-host",
      },
    ],
    walletSigner: false,
    financialExecution: false,
    automaticUpdates: false,
  };
  await page.route("**/api/v1/updates/status", async (route) => {
    if (fail) return route.abort("failed");
    await route.fulfill({
      json: { ...observation, automaticUpdates: unsupported },
    });
  });
  await page.goto(url + "/#settings");
  await openUpdateSettings(page);
  const panel = page.getByRole("region", { name: "Updates & recovery" });
  await expect(
    panel.getByRole("heading", {
      name: "Recovery review required",
      exact: true,
    }),
  ).toBeVisible();
  await expect(panel.getByLabel("Owner inventory command")).toContainText(
    "custody updates inventory",
  );
  await expect(panel.getByText("Locked", { exact: true })).toBeVisible();
  await expect(panel.getByRole("button")).toHaveCount(1);
  await captureScreenshot(page, {
    path: `.evidence/p4-status/recovery-mock-${test.info().project.name}.png`,
    fullPage: true,
  });
  unsupported = true;
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(panel.getByRole("alert")).toContainText(
    "Update status unavailable",
  );
  await expect(
    panel.getByText("Recovery review required", { exact: true }),
  ).toHaveCount(0);
  fail = true;
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(panel.getByRole("alert")).toBeVisible();
  fail = false;
  unsupported = false;
  await panel.getByRole("button", { name: "Refresh update status" }).click();
  await expect(
    panel.getByRole("heading", {
      name: "Recovery review required",
      exact: true,
    }),
  ).toBeVisible();
});

test("Deferred Mining preserves existing links and disabled authority across core restart", async ({
  page,
  isMobile,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const before = await (await page.request.get(`${url}/api/v1/health`)).json();
  const external: string[] = [];
  const mutations: string[] = [];
  page.on("request", (request) => {
    if (!ownedFixtureOrigins.has(new URL(request.url()).origin))
      external.push(request.url());
    if (
      !["GET", "HEAD"].includes(request.method()) &&
      !request.url().includes("/api/v1/stream/ticket") &&
      !request.url().includes("/api/v1/mining/preview")
    )
      mutations.push(request.url());
  });
  for (const route of ["asset", "loss", "satcoin"]) {
    await page.goto(`${url}/#terminals/${route}`);
    await expect(page).toHaveURL(/#mine$/);
    await expect(
      page.getByRole("region", { name: "Mine", exact: true }),
    ).toBeVisible();
    await expect(
      page.locator(
        ".terminal-workspace,.demo-chart,.session-rehearsal,.session-automatic",
      ),
    ).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Execution unavailable" }),
    ).toHaveCount(0);
    await expect(page.locator("main")).not.toContainText(
      /Asset Mining|Loss Mining|district|arena/i,
    );
    if (isMobile)
      expect(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= innerWidth,
        ),
      ).toBe(true);
  }
  expect(external).toEqual([]);
  expect(mutations).toEqual([]);
  await stop();
  await start();
  await page.goto(`${url}/#terminals/loss`);
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page).toHaveURL(/#mine$/);
  await expect(
    page.getByRole("region", { name: "Mine", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator(
      ".terminal-workspace,.demo-chart,.session-rehearsal,.session-automatic",
    ),
  ).toHaveCount(0);
  const after = await (await page.request.get(`${url}/api/v1/health`)).json();
  expect(after.instanceId).toBe(before.instanceId);
  expect(after.capabilities.walletSigner).toBe(false);
  expect(after.capabilities.financialExecution).toBe(false);
});

test("managed account entry supports current and predecessor layouts", async ({
  page,
}) => {
  page.setDefaultTimeout(1500);
  await page.setContent(
    `<details><summary>Advanced</summary><label>Name<input></label></details><button onclick="this.dataset.refreshed='yes'">Refresh</button>`,
  );
  await revealFixtureAccountEntry(page);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  await expect(page.locator("details")).toHaveAttribute("open", "");
  await refreshFixtureAccounts(page);
  await expect(
    page.getByRole("button", { name: "Refresh", exact: true }),
  ).toHaveAttribute("data-refreshed", "yes");
  await page.setContent(
    `<label>Name<input></label><details><summary>Advanced</summary><p>Other controls</p></details><button onclick="this.dataset.refreshed='yes'">Refresh accounts and approvals</button>`,
  );
  await revealFixtureAccountEntry(page);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  await expect(page.locator("details")).not.toHaveAttribute("open", "");
  await refreshFixtureAccounts(page);
  await expect(
    page.getByRole("button", {
      name: "Refresh accounts and approvals",
      exact: true,
    }),
  ).toHaveAttribute("data-refreshed", "yes");
  await page.setContent("<main></main>");
  await page.evaluate(() => {
    window.setTimeout(() => {
      document.querySelector("main")!.innerHTML = "<label>Name<input></label>";
    }, 300);
  });
  await revealFixtureAccountEntry(page);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
});

test("managed fixture approval inspection supports current and predecessor details", async ({
  page,
}) => {
  const proposalId = "7".repeat(32);
  await page.setContent(
    '<article aria-label="Account Wallet"><details class="account-details"><summary>Details</summary><div class="account-permission"><span class="badge">approved</span></div></details></article>',
  );
  await revealFixtureApproval(page, "Wallet", proposalId, 0);
  await expect(page.locator(".account-details")).toHaveAttribute("open", "");
  await expect(page.locator(".account-permission")).toBeVisible();
  await page.setContent(
    `<article aria-label="Account Wallet"></article><details><summary>History</summary><article aria-label="Proposal ${proposalId}"><span class="badge">approved</span></article></details>`,
  );
  await revealFixtureApproval(page, "Wallet", proposalId, 0);
  await expect(page.getByText("approved", { exact: true })).toBeVisible();
});

test("approval inspection reads exact genuine lifecycle3 proposal article without invented layout", async ({
  page,
}) => {
  page.setDefaultTimeout(1500);
  const target = "7".repeat(32),
    other = "8".repeat(32);
  // Genuine pinned d2f1fb22149a3c5190eb97d1e2ba58a9a3a0eb2a
  // AccountManagement.tsx505-516: separate Proposal article, no Details/Advanced.
  await page.setContent(
    `<section class="panel account-panel"><article class="account-entry" aria-label="Account Wallet"><h3>Wallet</h3></article></section><section class="panel account-panel"><div class="panel-heading"><h2>Permission proposals</h2></div><article class="account-entry" aria-label="Proposal ${other}"><div class="panel-heading"><h3>Disposable mandate</h3><span class="badge">approved</span></div></article><article class="account-entry" aria-label="Proposal ${target}"><div class="panel-heading"><h3>Disposable mandate</h3><span class="badge">not-approved</span></div></article></section>`,
  );
  await expect(
    page.locator(".account-permission,.account-advanced"),
  ).toHaveCount(0);
  const inspection = revealFixtureApproval(page, "Wallet", target, 0);
  await expect(
    page.getByRole("article", { name: `Proposal ${target}`, exact: true }),
  ).toContainText("not-approved");
  await page
    .getByRole("article", { name: `Proposal ${target}`, exact: true })
    .locator(".badge")
    .evaluate((el) => {
      el.textContent = "approved";
    });
  await inspection;
  await expect(
    page
      .getByRole("article", { name: `Proposal ${target}`, exact: true })
      .getByText("approved", { exact: true }),
  ).toBeVisible();
});

test("public approval validation binds exact proposal account digest and receipt row", async () => {
  const identity = {
    version: 1,
    signerId: "1".repeat(32),
    walletId: "2".repeat(32),
    epoch: "3".repeat(32),
    network: "disposable-fixture:v1",
    publicKey: "4".repeat(64),
  };
  const managed = {
    version: 1,
    id: "6".repeat(32),
    name: "Wallet",
    mode: "fixture-managed",
    network: identity.network,
    publicKey: identity.publicKey,
    signer: identity,
  };
  const policy = {
    account: identity,
    asset: "fixture-credit:v1",
    budget: "150",
    maxOperation: "10",
    destinations: ["fixture:treasury"],
    expiresAt: 2000000000,
  };
  const proposal = {
    version: 1,
    id: "7".repeat(32),
    policy,
    grant: {
      ...policy,
      id: "7".repeat(32),
      botId: "8".repeat(32),
      budget: "20",
      revoked: false,
    },
  };
  const older = structuredClone(proposal);
  older.id = "9".repeat(32);
  older.grant.id = older.id;
  const receipt = (item: typeof proposal) => ({
    id: item.id,
    digest: "a".repeat(64),
    receipt: {
      proposalId: item.id,
      account: identity,
      approvedAt: 1,
      state: "approved",
      digest: "a".repeat(64),
    },
  });
  const view = {
    record: {
      accounts: [managed],
      proposals: [
        { accountId: managed.id, proposal: older },
        { accountId: managed.id, proposal },
      ],
    },
    proposals: [receipt(older), receipt(proposal)],
  };
  expect(verifiedFixtureApproval(view, managed, proposal)).toEqual({
    accountId: managed.id,
    proposalId: proposal.id,
    digest: "a".repeat(64),
    row: 1,
  });
  for (const state of [
    "not-approved",
    "unverified",
    "mismatched",
    "stale",
    "revoked",
  ]) {
    const bad = structuredClone(view);
    bad.proposals[1].receipt.state = state;
    expect(() => verifiedFixtureApproval(bad, managed, proposal)).toThrow(
      "target receipt is not approved",
    );
  }
  const foreign = structuredClone(view);
  foreign.record.proposals[1].accountId = "f".repeat(32);
  expect(() => verifiedFixtureApproval(foreign, managed, proposal)).toThrow(
    "not bound to the managed account",
  );
  const changed = structuredClone(view);
  changed.proposals[1].receipt.account = { ...identity, epoch: "e".repeat(32) };
  expect(() => verifiedFixtureApproval(changed, managed, proposal)).toThrow(
    "receipt signer identity changed",
  );
  const digest = structuredClone(view);
  digest.proposals[1].receipt.digest = "b".repeat(64);
  expect(() => verifiedFixtureApproval(digest, managed, proposal)).toThrow(
    "receipt digest mismatch",
  );
  const wrong = structuredClone(view);
  wrong.proposals[1].receipt.proposalId = older.id;
  expect(() => verifiedFixtureApproval(wrong, managed, proposal)).toThrow(
    "receipt proposal identity changed",
  );
});

test("installed ordinary approval inspection waits for refreshed receipt without test editors", async ({
  page,
}) => {
  page.setDefaultTimeout(1500);
  const before = await privateCLI(["accounts"]);
  const account = {
    version: 1,
    id: "6".repeat(32),
    name: "Wallet",
    mode: "fixture-managed",
    network: "disposable-fixture:v1",
    publicKey: "4".repeat(64),
    signer: {
      version: 1,
      signerId: "1".repeat(32),
      walletId: "2".repeat(32),
      epoch: "3".repeat(32),
      network: "disposable-fixture:v1",
      publicKey: "4".repeat(64),
    },
  };
  const proposal = {
    version: 1,
    id: "7".repeat(32),
    policy: {
      account: account.signer,
      asset: "fixture-credit:v1",
      budget: "150",
      maxOperation: "10",
      destinations: ["fixture:treasury"],
      expiresAt: Math.floor(Date.now() / 1000) + 900,
    },
    grant: {
      id: "7".repeat(32),
      account: account.signer,
      asset: "fixture-credit:v1",
      budget: "20",
      maxOperation: "10",
      destinations: ["fixture:treasury"],
      expiresAt: Math.floor(Date.now() / 1000) + 900,
      botId: "8".repeat(32),
      revoked: false,
    },
  };
  let refreshing = false;
  let release!: () => void;
  let entered!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  const readStarted = new Promise<void>((resolve) => {
    entered = resolve;
  });
  await page.route("**/api/v1/accounts", async (route) => {
    expect(route.request().method()).toBe("GET");
    if (refreshing) {
      entered();
      await pending;
    }
    await route.fulfill({
      json: {
        version: 1,
        record: {
          version: 1,
          revision: refreshing ? 2 : 1,
          accounts: [account],
          proposals: refreshing ? [{ accountId: account.id, proposal }] : [],
        },
        fixture: { available: false },
        accountStates: { [account.id]: "unverified" },
        proposals: refreshing
          ? [
              {
                id: proposal.id,
                digest: "a".repeat(64),
                receipt: {
                  proposalId: proposal.id,
                  account: account.signer,
                  approvedAt: 1,
                  digest: "a".repeat(64),
                  state: "approved",
                },
              },
            ]
          : [],
      },
    });
  });
  const link = (
    await exec(binary, ["setup-link", "--data-dir", dir])
  ).stdout.trim();
  await page.goto(link);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  expect((await privateCLI(["agents"])).aiFixtureAvailable).toBeFalsy();
  await page.goto(url + "/#wallets/overview");
  const wallet = page.getByRole("article", {
    name: "Account Wallet",
    exact: true,
  });
  await expect(wallet).toBeVisible();
  await expect(page.locator(".account-advanced")).toHaveCount(0);
  await expect(page.getByLabel("Name", { exact: true })).toHaveCount(0);
  refreshing = true;
  const publicRead = page.waitForResponse(
    (response) =>
      response.request().method() === "GET" &&
      new URL(response.url()).pathname === "/api/v1/accounts",
  );
  await refreshFixtureAccounts(page);
  await readStarted;
  const inspection = revealFixtureApproval(page, "Wallet", proposal.id, 0);
  // Keep the refreshed read pending until the helper observes the current
  // no-receipt DOM. This is the installed race, not a delayed editor fixture.
  await expect(wallet.locator(".account-permission")).toHaveCount(0);
  release();
  expect(
    verifiedFixtureApproval(await (await publicRead).json(), account, proposal)
      .row,
  ).toBe(0);
  await inspection;
  await expect(wallet.locator(".account-details")).toHaveAttribute("open", "");
  await expect(
    wallet
      .locator(".account-permission")
      .getByText("approved", { exact: true }),
  ).toBeVisible();
  expect((await privateCLI(["accounts"])).record).toEqual(before.record);
});

test("public account records share UI/CLI state without wallet authority", async ({
  page,
}) => {
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#wallets/accounts");
  await expect(page.getByRole("heading", { name: "Wallets" })).toBeVisible();
  await revealFixtureAccountEntry(page);
  await revealControl(page, page.getByLabel("Name", { exact: true }), "");
  await page.getByLabel("Name", { exact: true }).fill("Disposable watch");
  await revealControl(
    page,
    page.getByLabel("Disposable public key (64 lowercase hex)"),
    "",
  );
  await page
    .getByLabel("Disposable public key (64 lowercase hex)")
    .fill("4".repeat(64));
  await page
    .getByRole("button", { name: "Save public account", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Disposable watch", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Record disposable signer", exact: true }),
  ).toBeDisabled();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin.end(fixturePassword + "\n");
  await once(login, "exit");
  expect(login.exitCode).toBe(0);
  let state = JSON.parse(
    (await exec(binary, ["accounts", "--data-dir", dir])).stdout,
  );
  await refreshFixtureAccounts(page);
  expect(state.record.accounts[0].name).toBe("Disposable watch");
  expect(state.fixture.available).toBe(false);
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  const rpcBounds = await page.getByRole("dialog").boundingBox();
  const inputBounds = await page
    .getByLabel("RPC URL", { exact: true })
    .boundingBox();
  expect(inputBounds!.x - rpcBounds!.x).toBeGreaterThanOrEqual(16);
  await page.getByRole("button", { name: "Check", exact: true }).click();
  await expect(
    page.getByText("Verified (test)", { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel("RPC URL", { exact: true })
    .fill("https://127.0.0.1/private");
  await page.getByRole("button", { name: "Check", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "only the disposable RPC",
  );
  await expect(page.getByText("Verified (test)", { exact: true })).toHaveCount(
    0,
  );
  await page.getByRole("button", { name: "Close dialog" }).click();
  const rpc = await privateCLI(["accounts", "--check-rpc-stdin"], {
    endpoint: "https://rpc.fased.invalid/solana",
    network: "disposable-fixture:v1",
    accountId: state.record.accounts[0].id,
    accountRevision: state.record.revision,
  });
  expect(rpc.account.id).toBe(state.record.accounts[0].id);
  expect(rpc.accountRevision).toBe(state.record.revision);
  expect(rpc.accountState).toBe("public-record-only");
  expect(rpc.fixtureOnly).toBe(true);
  expect(rpc.liveRPC).toBe(false);
  expect(rpc.genesisHash).toBe("11111111111111111111111111111112");
  expect((await privateCLI(["accounts"])).record).toEqual(state.record);
  await expect(
    privateCLI(["accounts", "--check-rpc-stdin"], {
      endpoint: "https://rpc.fased.invalid/solana",
      network: "disposable-fixture:v1",
      accountId: state.record.accounts[0].id,
      accountRevision: state.record.revision + 1,
    }),
  ).rejects.toThrow("wallet changed");
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove", exact: true }),
  ).toBeVisible();
  state = await privateCLI(["accounts"]);
  expect(state.record.accounts[0].rpc.endpoint).toBe(
    "https://rpc.fased.invalid/solana",
  );
  const savedCheck = await privateCLI(["accounts", "--check-rpc-stdin"], {
    accountId: state.record.accounts[0].id,
    accountRevision: state.record.revision,
  });
  expect(savedCheck.account.rpc).toEqual(state.record.accounts[0].rpc);
  await page.reload();
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  await expect(page.getByLabel("RPC URL", { exact: true })).toHaveValue(
    state.record.accounts[0].rpc.endpoint,
  );
  await expect(page.getByText("Verified (test)", { exact: true })).toHaveCount(
    0,
  );
  await page.getByText("API key", { exact: true }).first().click();
  await page
    .getByLabel("RPC API key", { exact: true })
    .fill("fased-rpc-test-disposable-browser-123456");
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page.getByRole("button", { name: "Save key", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove key", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("RPC API key", { exact: true })).toHaveValue("");
  await captureScreenshot(page, {
    path: `.evidence/b05-wallet-setup/rpc-layout-${test.info().project.name}.png`,
  });
  const credential = await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "status",
    ref: "",
  });
  expect(credential.saved).toBe(true);
  expect(credential.locked).toBe(true);
  expect(credential.liveRPC).toBe(false);
  expect(JSON.stringify(credential)).not.toContain("disposable-browser");
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page.getByRole("button", { name: "Verify key", exact: true }).click();
  await expect(page.getByText("Key verified", { exact: true })).toBeVisible();
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page
    .getByRole("button", { name: "Check connection", exact: true })
    .click();
  await expect(
    page.getByText("Connected (demo)", { exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("Vault password", { exact: true })).toHaveValue(
    "",
  );
  const connection = await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "check",
    ref: credential.ref,
    passphrase: "Disposable browser vault password 123",
  });
  expect(connection.check.fixtureOnly).toBe(true);
  expect(connection.check.liveRPC).toBe(false);
  expect(connection.locked).toBe(true);
  expect(JSON.stringify(connection)).not.toContain("disposable-browser");

  await page.getByText("Recovery", { exact: true }).click();
  const downloadBackup = page.getByRole("button", {
    name: "Download backup",
    exact: true,
  });
  let backupDownloads = 0;
  page.on("download", () => backupDownloads++);
  await expect(downloadBackup).toBeVisible({ timeout: 3000 });
  await expect(
    page.getByText("Requires this instance and wallet settings.", {
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("wrong disposable vault password");
  await downloadBackup.click();
  await expect(page.getByRole("alert")).toContainText(
    "Credential operation failed",
  );
  expect(backupDownloads).toBe(0);
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  const downloadPromise = page.waitForEvent("download");
  await downloadBackup.click();
  const download = await downloadPromise;
  expect(backupDownloads).toBe(1);
  expect(download.suggestedFilename()).toBe("fased-rpc-backup.json");
  const backupPath = join(dir, "rpc-backup.json");
  await download.saveAs(backupPath);
  const browserBackup = JSON.parse(await readFile(backupPath, "utf8"));
  expect(browserBackup.accountId).toBe(state.record.accounts[0].id);
  expect(JSON.stringify(browserBackup)).not.toContain("disposable-browser");
  expect(JSON.stringify(browserBackup)).not.toContain("vault password");

  const cliBackupPath = join(dir, "rpc-cli-backup.json");
  const backupStatus = await privateCLI(
    ["accounts", "--rpc-credential-stdin", "--rpc-backup-out", cliBackupPath],
    {
      accountId: state.record.accounts[0].id,
      revision: state.record.revision,
      action: "backup",
      ref: credential.ref,
      passphrase: "Disposable browser vault password 123",
    },
  );
  expect(backupStatus.backup).toBeUndefined();
  expect(JSON.parse(await readFile(cliBackupPath, "utf8"))).toEqual(
    browserBackup,
  );
  await page
    .getByLabel("Backup file", { exact: true })
    .setInputFiles(backupPath);
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page
    .getByRole("button", { name: "Verify backup", exact: true })
    .click();
  await expect(
    page.getByText("Backup verified", { exact: true }),
  ).toBeVisible();
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await expect(
    page.getByRole("button", { name: "Restore", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Remove key", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove key", exact: true }),
  ).toHaveCount(0);
  await page
    .getByLabel("Vault password", { exact: true })
    .fill("Disposable browser vault password 123");
  await page.getByRole("button", { name: "Restore", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove key", exact: true }),
  ).toBeVisible();
  const recovered = await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "status",
    ref: "",
  });
  expect(recovered.ref).not.toBe(credential.ref);
  expect(recovered.locked).toBe(true);
  await privateCLI(["accounts", "--rpc-credential-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    action: "remove",
    ref: recovered.ref,
    passphrase: "Disposable browser vault password 123",
  });
  await page.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Remove", exact: true }),
  ).toHaveCount(0);
  state = await privateCLI(["accounts"]);
  expect(state.record.accounts[0].rpc).toBeUndefined();
  state = await privateCLI(["accounts", "--rpc-stdin"], {
    accountId: state.record.accounts[0].id,
    revision: state.record.revision,
    endpoint: "https://rpc.fased.invalid/solana",
    network: "disposable-fixture:v1",
  });
  await expect(
    privateCLI(["accounts", "--rpc-stdin"], {
      accountId: state.record.accounts[0].id,
      revision: state.record.revision,
      endpoint: "https://rpc.fased.invalid/solana",
      network: "disposable-fixture:v1",
      apiKey: "disposable-refused",
    }),
  ).rejects.toThrow("no credentials accepted");
  await page.reload();
  const stale = structuredClone(state.record);
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  await revealControl(page, page.getByLabel("New account name"), "");
  await page.getByLabel("New account name").fill("Shared watch");
  await page
    .getByRole("button", { name: "Save account name", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Shared watch", exact: true }),
  ).toBeVisible();
  const rejected = spawn(binary, [
    "accounts",
    "--data-dir",
    dir,
    "--request-stdin",
  ]);
  rejected.stdin.end(JSON.stringify(stale));
  await once(rejected, "exit");
  expect(rejected.exitCode).not.toBe(0);
  state = JSON.parse(
    (await exec(binary, ["accounts", "--data-dir", dir])).stdout,
  );
  state.record.accounts.push({
    version: 1,
    id: "b".repeat(32),
    name: "External manual fixture",
    mode: "external",
    network: "disposable-fixture:v1",
    publicKey: "5".repeat(64),
  });
  const change = spawn(binary, [
    "accounts",
    "--data-dir",
    dir,
    "--request-stdin",
  ]);
  change.stdin.end(JSON.stringify(state.record));
  await once(change, "exit");
  expect(change.exitCode).toBe(0);
  await refreshFixtureAccounts(page);
  await expect(
    page.getByRole("heading", { name: "External manual fixture", exact: true }),
  ).toBeVisible();
  const heldCapabilities = await (
    await page.request.get(url + "/api/v1/health")
  ).json();
  expect(heldCapabilities.capabilities.walletSigner).toBe(false);
  expect(heldCapabilities.capabilities.financialExecution).toBe(false);
  await captureScreenshot(page, {
    path: `.evidence/p3-accounts/public-accounts-${test.info().project.name}.png`,
    fullPage: true,
  });
  await page.evaluate(() => sessionStorage.removeItem("fased.core.session.v1"));
  await stop();
  await start();
  await page.goto(url + "/#wallets/accounts");
  await revealControl(
    page,
    page.getByLabel("Owner password", { exact: true }),
    "",
  );
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Shared watch", exact: true }),
  ).toBeVisible();
});

test("proposal form uses explicit fixture labels (UI mock only)", async ({
  page,
}) => {
  await stop();
  await start(true);
  const ref = {
    version: 1,
    signerId: "1".repeat(32),
    walletId: "2".repeat(32),
    epoch: "3".repeat(32),
    network: "disposable-fixture:v1",
    publicKey: "4".repeat(64),
  };
  let record = {
    version: 1,
    revision: 1,
    accounts: [
      {
        version: 1,
        id: "d".repeat(32),
        name: "Mock disposable reference",
        mode: "fixture-managed",
        network: ref.network,
        publicKey: ref.publicKey,
        signer: ref,
      },
    ],
    proposals: [] as {
      accountId: string;
      proposal: {
        id: string;
        policy: { budget: string };
        grant: { budget: string; account: typeof ref };
      };
    }[],
  };
  await page.route("**/api/v1/accounts", async (route) => {
    if (route.request().method() === "PUT") {
      record = route.request().postDataJSON();
      record.revision++;
    }
    await route.fulfill({
      json: {
        record,
        fixture: {
          available: true,
          status: {
            account: ref,
            locked: true,
            recoveryRequired: false,
            walletSigner: false,
            financialExecution: false,
          },
        },
        accountStates: { [record.accounts[0].id]: "current-fixture" },
        proposals: record.proposals.map((d) => ({
          id: d.proposal.id,
          digest: "c".repeat(64),
          receipt: { digest: "", state: "not-approved" },
        })),
      },
    });
  });
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await revealControl(page, page.getByLabel("Create owner password"), "");
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#wallets/accounts");
  await revealFixtureAccountEntry(page);
  await expect(page.getByLabel("Test account", { exact: true })).toBeVisible({
    timeout: 1000,
  });
  await revealControl(
    page,
    page.getByLabel("Test account", { exact: true }),
    "",
  );
  await page
    .getByLabel("Test account", { exact: true })
    .selectOption("d".repeat(32));
  await page
    .getByRole("button", { name: "Create permission proposal", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Download public proposal", exact: true }),
  ).toBeVisible();
  expect(record.proposals[0].proposal.policy.budget).toBe("100");
  expect(record.proposals[0].proposal.grant.budget).toBe("50");
  expect(record.proposals[0].proposal.grant.account).toEqual(ref);
  await expect(
    page
      .locator(".account-advanced")
      .getByText("not-approved", { exact: true }),
  ).toBeVisible();
});

for (const method of ["create", "import"] as const) {
  test(`Wallets network ${method} joins protected owner public reference RPC and restart`, async ({
    page,
    isMobile,
  }) => {
    test.skip(
      !process.env.FASED_WALLET_BROWSER_BINARY ||
        !process.env.FASED_WALLET_OWNER_BINARY,
      "explicit compiled test-only wallet drivers required",
    );
    test.setTimeout(90000);
    page.setDefaultTimeout(10000);
    await stop();
    await mkdir(".evidence/b05-wallet-setup", { recursive: true });
    const fixtureBase = resolve(".evidence/b5-fixtures");
    await mkdir(fixtureBase, { recursive: true, mode: 0o700 });
    const root = await mkdtemp(join(fixtureBase, "f-"));
    await writeFile(
      join(root, "disposable.json"),
      "unfunded-reconstructible-wallet-fixture\n",
      { mode: 0o600 },
    );
    const dataDir = join(root, "core");
    const coreDriver = resolve(process.env.FASED_WALLET_BROWSER_BINARY!);
    const ownerDriver = resolve(process.env.FASED_WALLET_OWNER_BINARY!);
    let fixture: ChildProcess | undefined;
    let fixtureLog = "";
    let endpoint = "";
    const forbidden: string[] = [];
    const bodies: string[] = [];
    page.on("request", (request) => {
      if (new URL(request.url()).hostname !== "127.0.0.1")
        forbidden.push(request.url());
      if (request.method() === "PUT" || request.method() === "POST")
        bodies.push(request.postData() ?? "");
    });
    const startFixture = async () => {
      await rm(join(root, "ready.json"), { force: true });
      fixture = spawn(
        coreDriver,
        ["-test.run=^TestWalletBrowserFixtureDriver$", "-test.timeout=3m"],
        {
          cwd: resolve("internal/core"),
          env: {
            ...process.env,
            FASED_WALLET_BROWSER_FIXTURE: "1",
            FASED_WALLET_FIXTURE_ROOT: root,
          },
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      fixture.stdout!.on("data", (b) => {
        fixtureLog += b.toString();
      });
      fixture.stderr!.on("data", (b) => {
        fixtureLog += b.toString();
      });
      await expect
        .poll(async () => {
          if (fixture!.exitCode !== null) throw new Error(fixtureLog);
          try {
            endpoint = JSON.parse(
              await readFile(join(root, "ready.json"), "utf8"),
            ).url;
            return endpoint;
          } catch {
            return "";
          }
        })
        .not.toBe("");
    };
    const stopFixture = async () => {
      if (!fixture || fixture.exitCode !== null) return;
      const exiting = once(fixture, "exit");
      fixture.kill("SIGTERM");
      const result = await Promise.race([
        exiting,
        new Promise<never>((_, reject) =>
          setTimeout(
            () => reject(new Error("wallet fixture failed to stop")),
            8000,
          ),
        ),
      ]);
      expect(result[0]).toBe(0);
      fixture = undefined;
    };
    const cli = async (args: string[], input?: unknown) => {
      const child = spawn(binary, [...args, "--data-dir", dataDir], {
        stdio: ["pipe", "pipe", "pipe"],
      });
      let output = "",
        error = "";
      child.stdout!.on("data", (b) => {
        output += b.toString();
      });
      child.stderr!.on("data", (b) => {
        error += b.toString();
      });
      child.stdin!.end(input === undefined ? "" : JSON.stringify(input));
      const result = await once(child, "exit");
      if (result[0] !== 0) throw new Error(error);
      return JSON.parse(output);
    };
    const owner = async (action: string, ownerDir = "owner") =>
      exec(ownerDriver, ["-test.run=^TestWalletOwnerFixtureDriver$"], {
        cwd: resolve("internal/signer"),
        env: {
          ...process.env,
          FASED_WALLET_OWNER_FIXTURE: "1",
          FASED_WALLET_FIXTURE_ROOT: root,
          FASED_WALLET_OWNER_ACTION: action,
          FASED_WALLET_OWNER_DIR: ownerDir,
        },
      });
    try {
      await startFixture();
      await page.goto(endpoint + "/#wallets/accounts");
      const claim = JSON.parse(
        await readFile(join(dataDir, "claim-code.json"), "utf8"),
      );
      await openOwnerSetup(page, claim.code);
      await page.getByLabel("Create owner password").fill(fixturePassword);
      await page
        .getByRole("button", { name: "Claim workspace", exact: true })
        .click();
      await expect(
        page.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();
      const login = spawn(binary, [
        "login",
        "--data-dir",
        dataDir,
        "--password-stdin",
      ]);
      login.stdin!.end(fixturePassword + "\n");
      expect((await once(login, "exit"))[0]).toBe(0);
      await page.goto(endpoint + "/#wallets/accounts");
      await expect(
        page.getByRole("heading", { name: "Wallets", exact: true }),
      ).toBeVisible();
      await page
        .getByRole("button", { name: "Add wallet", exact: true })
        .click();
      let dialog = page.getByRole("dialog", {
        name: "Add wallet",
        exact: true,
      });
      await dialog.getByLabel("Wallet name").fill("Watch");
      await dialog
        .getByLabel("Network", { exact: true })
        .selectOption("devnet");
      await dialog.getByLabel("Public address").fill("0".repeat(32));
      await dialog.getByRole("button", { name: "Save", exact: true }).click();
      await expect(dialog.getByRole("alert")).toContainText(
        "valid Solana public address",
      );
      expect((await cli(["accounts"])).record.accounts).toHaveLength(0);
      await dialog.getByLabel("Public address").fill("1".repeat(32));
      await dialog.getByRole("button", { name: "Save", exact: true }).click();
      await expect(dialog).toHaveCount(0);
      const original = (await cli(["accounts"])).record.accounts[0];
      expect(original.version).toBe(2);
      expect(original.mode).toBe("watch");
      await page
        .getByRole("button", { name: "Add wallet", exact: true })
        .click();
      dialog = page.getByRole("dialog", { name: "Add wallet", exact: true });
      const methods = dialog.getByRole("group", {
        name: "Wallet type",
        exact: true,
      });
      await expect(
        methods.getByRole("button", { name: "Watch", exact: true }),
      ).toBeEnabled();
      await expect(
        methods.getByRole("button", { name: "Create", exact: true }),
      ).toBeDisabled();
      await expect(
        methods.getByRole("button", { name: "Import", exact: true }),
      ).toBeDisabled();
      await expect(dialog).toContainText("Protected setup unavailable");
      await expect(dialog.locator(".wallet-terminal")).toHaveCount(0);
      await expect(dialog).not.toContainText("fased-app custody");
      expect(await dialog.locator('input[type="password"]').count()).toBe(0);
      await expect(
        dialog.getByRole("button", { name: "Attach", exact: true }),
      ).toHaveCount(0);
      await dialog.getByLabel("Wallet name").fill(`Vault ${method}`);
      expect((await cli(["accounts"])).record.accounts).toHaveLength(1);
      await dialog
        .getByRole("button", { name: "Close dialog", exact: true })
        .click();
      // Keys stay in the existing protected owner CLI fixture, never an AI
      // constructor or browser form. Only its public status returns to UI.
      const ownerResult = await owner(method);
      const ownerOutput = ownerResult.stdout + ownerResult.stderr;
      expect(ownerOutput).not.toContain("disposable-wallet-owner-unlock");
      expect(ownerOutput).not.toContain("disposable-wallet-owner-backup");
      expect(ownerOutput).not.toContain("42".repeat(32));
      await refreshFixtureAccounts(page);
      await page
        .getByRole("button", { name: "Add wallet", exact: true })
        .click();
      dialog = page.getByRole("dialog", { name: "Add wallet", exact: true });
      await dialog
        .getByRole("group", { name: "Wallet type", exact: true })
        .getByRole("button", { name: "Attach", exact: true })
        .click();
      await expect(dialog.getByLabel("Wallet name")).toHaveValue(
        `Vault ${method}`,
      );
      await expect(
        dialog.getByLabel("Network", { exact: true }),
      ).toBeDisabled();
      await expect(dialog).not.toContainText("fased-app custody");
      expect(await dialog.locator('input[type="password"]').count()).toBe(0);
      await expect(dialog).toContainText("Recovery required");
      const attach = dialog.locator("form > button.primary");
      await expect(attach).toHaveText("Attach");
      await expect(attach).toBeEnabled();
      await attach.click();
      await expect(dialog).toHaveCount(0);
      let state = await cli(["accounts"]);
      expect(state.record.accounts[0]).toEqual(original);
      expect(state.record.accounts).toHaveLength(2);
      const managed = state.record.accounts.find((a: any) => a.version === 3);
      expect(managed.signer.version).toBe(2);
      expect(managed.network).toBe("devnet");
      expect(state.fixture.status.locked).toBe(true);
      expect(state.fixture.status.recoveryRequired).toBe(true);
      const row = page.getByRole("article", {
        name: `Account Vault ${method}`,
        exact: true,
      });
      await row.getByRole("button", { name: "RPC", exact: true }).click();
      const rpc = page.getByRole("dialog", {
        name: `RPC · Vault ${method}`,
        exact: true,
      });
      await expect(rpc.getByLabel("RPC URL", { exact: true })).toHaveValue("");
      await rpc
        .getByLabel("RPC URL", { exact: true })
        .fill("https://devnet.helius-rpc.com");
      await rpc.getByRole("button", { name: "Check", exact: true }).click();
      await expect(
        rpc.getByText("Verified (test)", { exact: true }),
      ).toBeVisible();
      await rpc.getByRole("button", { name: "Save", exact: true }).click();
      await expect(
        rpc.getByRole("button", { name: "Remove", exact: true }),
      ).toBeVisible();
      state = await cli(["accounts"]);
      const saved = state.record.accounts.find((a: any) => a.id === managed.id);
      expect(saved.rpc.network).toBe("devnet");
      const check = await cli(["accounts", "--check-rpc-stdin"], {
        accountId: managed.id,
        accountRevision: state.record.revision,
      });
      expect(check.fixtureOnly).toBe(true);
      expect(check.liveRPC).toBe(false);
      await rpc.getByRole("button", { name: "API key", exact: true }).click();
      const rpcKey = "disposable-provider-input-not-issued-123456";
      await rpc.getByLabel("RPC API key", { exact: true }).fill(rpcKey);
      await rpc
        .getByLabel("Vault password", { exact: true })
        .fill("Disposable wallet RPC password 123");
      await rpc.getByRole("button", { name: "Save key", exact: true }).click();
      await expect(
        rpc.getByRole("button", { name: "Remove key", exact: true }),
      ).toBeVisible();
      expect(
        await readFile(
          join(dataDir, `rpc-credential-${managed.id}.json`),
          "utf8",
        ),
      ).not.toContain(rpcKey);
      await captureScreenshot(page, {
        path: `.evidence/b05-wallet-setup/${method}-${isMobile ? "mobile" : "desktop"}.png`,
      });
      const beforeRestart = state.record;
      await stopFixture();
      await startFixture();
      await page.goto(endpoint + "/#wallets/accounts");
      await choosePasswordSignIn(page);
      await page
        .getByLabel("Owner password", { exact: true })
        .fill(fixturePassword);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
      await expect(
        page.getByRole("button", { name: "Sign out", exact: true }),
      ).toBeVisible();
      await page.goto(endpoint + "/#wallets/accounts");
      await expect(
        page.getByRole("article", {
          name: `Account Vault ${method}`,
          exact: true,
        }),
      ).toBeVisible();
      expect((await cli(["accounts"])).record).toEqual(beforeRestart);
      const credential = await cli(["accounts", "--rpc-credential-stdin"], {
        accountId: managed.id,
        revision: state.record.revision,
        action: "status",
        ref: "",
      });
      expect(credential.saved).toBe(true);
      expect(credential.locked).toBe(true);
      expect(credential.liveRPC).toBe(false);
      await owner("recover", "recovered");
      const recovered = await cli(["accounts"]);
      expect(recovered.accountStates[managed.id]).toBe("stale");
      expect(recovered.fixture.status.recoveryRequired).toBe(true);
      expect(recovered.fixture.status.account.publicKey).toBe(
        managed.publicKey,
      );
      expect(recovered.fixture.status.account.epoch).not.toBe(
        managed.signer.epoch,
      );
      const stale = await cli(["accounts", "--rpc-credential-stdin"], {
        accountId: managed.id,
        revision: state.record.revision,
        action: "status",
        ref: "",
      });
      expect(stale.stale).toBe(true);
      expect(stale.locked).toBe(true);
      await refreshFixtureAccounts(page);
      await expect(
        page
          .getByRole("article", {
            name: `Account Vault ${method}`,
            exact: true,
          })
          .getByText("Reconnect", { exact: true }),
      ).toBeVisible();
      await page
        .getByRole("button", { name: "Add wallet", exact: true })
        .click();
      const recoveryDialog = page.getByRole("dialog", {
        name: "Add wallet",
        exact: true,
      });
      await recoveryDialog
        .getByRole("group", { name: "Wallet type", exact: true })
        .getByRole("button", { name: "Attach", exact: true })
        .click();
      await expect(recoveryDialog.locator(".wallet-terminal")).toHaveCount(0);
      await expect(recoveryDialog).not.toContainText("fased-app custody");
      await recoveryDialog.getByLabel("Wallet name").fill("Recovered wallet");
      const reattach = recoveryDialog.locator("form > button.primary");
      await expect(reattach).toHaveText("Attach");
      await reattach.click();
      await expect(recoveryDialog).toHaveCount(0);
      const rebound = await cli(["accounts"]);
      expect(rebound.record.accounts).toHaveLength(3);
      expect(
        rebound.record.accounts.find((a: any) => a.id === managed.id),
      ).toEqual(saved);
      expect(rebound.fixture.status.recoveryRequired).toBe(true);
      expect(forbidden).toEqual([]);
      expect(bodies.join("\n")).not.toContain("disposable-wallet-owner-unlock");
      expect(bodies.join("\n")).not.toContain("disposable-wallet-owner-backup");
      expect(bodies.join("\n")).not.toContain("42".repeat(32));
    } finally {
      await writeFile(
        `.evidence/b05-wallet-setup/${method}-${isMobile ? "mobile" : "desktop"}-fixture.log`,
        fixtureLog,
      );
      await stopFixture();
      await rm(root, { recursive: true, force: true });
    }
  });
}

test("live read-only Solana RPC presets and OS-protected RPC keys work without password prompts", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await openOwnerSetup(page, claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();

  const pubAddress = "11111111111111111111111111111111";
  const txSig = "4".repeat(88);
  let revision = 0;
  let savedAcct: any;
  let credRef = "";
  let credCheck: { fixtureOnly: boolean; liveRPC: boolean } | undefined;
  const postedCredActions: { action: string; passphrase?: string }[] = [];

  const accountsPayload = () => ({
    setup: {
      version: 1,
      instanceId: "inst-live-rpc-check",
      source: "local",
      network: "devnet",
      state: "not-installed",
      fixtureOnly: false,
      networkWatchEnabled: true,
      ownerSupported: true,
    },
    addresses: savedAcct ? { [savedAcct.id]: pubAddress } : {},
    record: {
      version: 1,
      revision,
      accounts: savedAcct ? [savedAcct] : [],
      proposals: [],
    },
    fixture: { available: false },
    accountStates: savedAcct ? { [savedAcct.id]: "public-record-only" } : {},
    proposals: [],
  });
  const credPayload = () => ({
    canVerify: true,
    canCheck: true,
    ref: credRef,
    saved: credRef !== "",
    stale: false,
    locked: true,
    osVaultAvailable: true,
    osProtected: credRef !== "",
    testOnly: false,
    liveRPC: true,
    ...(credCheck ? { check: credCheck } : {}),
  });

  await page.route("**/api/v1/accounts", async (route) => {
    if (route.request().method() === "GET")
      await route.fulfill({ json: accountsPayload() });
    else await route.continue();
  });
  await page.route("**/api/v1/accounts/setup", async (route) => {
    const body = route.request().postDataJSON();
    revision++;
    savedAcct = {
      version: 2,
      id: "a".repeat(32),
      name: body.name,
      mode: "watch",
      network: body.network,
      publicKey: "00".repeat(32),
    };
    await route.fulfill({ json: accountsPayload() });
  });
  await page.route("**/api/v1/accounts/rpc-check", async (route) => {
    const body = route.request().postDataJSON();
    await route.fulfill({
      json: {
        account: savedAcct,
        accountRevision: revision,
        accountState: "public-record-only",
        endpoint: body.endpoint,
        network: body.network,
        genesisHash: "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG",
        checkedAt: new Date().toISOString(),
        fixtureOnly: false,
        liveRPC: true,
      },
    });
  });
  await page.route("**/api/v1/accounts/rpc", async (route) => {
    const body = route.request().postDataJSON();
    revision++;
    savedAcct = {
      ...savedAcct,
      rpc: body.remove
        ? undefined
        : { version: 2, endpoint: body.endpoint, network: body.network },
    };
    await route.fulfill({ json: accountsPayload() });
  });
  await page.route("**/api/v1/accounts/rpc-credential", async (route) => {
    const body = route.request().postDataJSON();
    postedCredActions.push({
      action: body.action,
      passphrase: body.passphrase,
    });
    credCheck = undefined;
    if (body.action === "save") {
      expect(body.passphrase ?? "").toBe("");
      credRef = "os-rpc-ref-1";
    } else if (body.action === "check") {
      expect(body.passphrase ?? "").toBe("");
      credCheck = { fixtureOnly: false, liveRPC: true };
    } else if (body.action === "remove") {
      expect(body.passphrase ?? "").toBe("");
      credRef = "";
    }
    await route.fulfill({ json: credPayload() });
  });
  await page.route("**/api/v1/accounts/read", async (route) => {
    const body = route.request().postDataJSON();
    expect(body.passphrase ?? "").toBe("");
    const now = Math.floor(Date.now() / 1000);
    await route.fulfill({
      json: {
        version: 1,
        accountId: savedAcct.id,
        revision,
        binding: "b".repeat(64),
        credentialRef: credRef,
        address: pubAddress,
        network: "devnet",
        endpoint: savedAcct.rpc.endpoint,
        commitment: "finalized",
        slot: "100",
        lamports: "123456789",
        tokens: [],
        activity: [
          { signature: txSig, slot: "99", blockTime: null, failed: false },
        ],
        activityLimit: 20,
        fetchedAt: now,
        expiresAt: now + 60,
        fixtureOnly: false,
        liveRPC: true,
        ...(body.selection
          ? {
              transaction: {
                ...body.selection,
                feeLamports: "5000",
                payer: pubAddress,
                nativeBefore: "200005000",
                nativeAfter: "123456789",
                nativeChange: "-76548211",
                tokens: [],
                tokenChangesComplete: true,
              },
            }
          : {}),
      },
    });
  });

  await page.goto(url + "/#wallets/accounts");
  await page.getByRole("button", { name: "Add wallet", exact: true }).click();
  const addDialog = page.getByRole("dialog", {
    name: "Add wallet",
    exact: true,
  });
  await addDialog.getByLabel("Wallet name").fill("Solana Watch");
  await addDialog.getByLabel("Network", { exact: true }).selectOption("devnet");
  await addDialog.getByLabel("Public address").fill(pubAddress);
  await addDialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(addDialog).toHaveCount(0);

  await page.getByRole("button", { name: "RPC", exact: true }).click();
  let rpcDialog = page.getByRole("dialog", {
    name: "RPC · Solana Watch",
    exact: true,
  });
  await rpcDialog.getByRole("button", { name: "Solana", exact: true }).click();
  await expect(rpcDialog.getByLabel("RPC URL", { exact: true })).toHaveValue(
    "https://api.devnet.solana.com",
  );
  await expect(rpcDialog.getByText("API key", { exact: true })).toHaveCount(0);
  await rpcDialog.getByRole("button", { name: "Check", exact: true }).click();
  await expect(rpcDialog.getByText("Verified", { exact: true })).toBeVisible();
  await rpcDialog.getByRole("button", { name: "Save", exact: true }).click();
  await expect(
    rpcDialog.getByRole("button", { name: "Remove", exact: true }),
  ).toBeVisible();
  await rpcDialog.getByRole("button", { name: "Close dialog" }).click();

  // Keyless balance + activity + transaction detail with zero Vault password prompts.
  const balanceRegion = page.getByRole("region", {
    name: "Balance",
    exact: true,
  });
  await balanceRegion
    .getByRole("button", { name: "Refresh", exact: true })
    .click();
  await expect(page.getByText("0.123456789 SOL")).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Unlock RPC" })).toHaveCount(0);
  await page.getByText("Activity", { exact: true }).click();
  await page.getByRole("button", { name: /^Transaction / }).click();
  const txDialog = page.getByRole("dialog", {
    name: "Transaction",
    exact: true,
  });
  await expect(txDialog.getByText("-0.076548211")).toBeVisible();
  await expect(txDialog.getByLabel("Vault password")).toHaveCount(0);
  await txDialog.getByRole("button", { name: "Close", exact: true }).click();

  // Switch to Helius with OS-protected passwordless credential save, live Connected check, Refresh & Remove key.
  await page.getByRole("button", { name: "RPC", exact: true }).click();
  rpcDialog = page.getByRole("dialog", {
    name: "RPC · Solana Watch",
    exact: true,
  });
  await rpcDialog.getByRole("button", { name: "Helius", exact: true }).click();
  await expect(rpcDialog.getByLabel("RPC URL", { exact: true })).toHaveValue(
    "https://devnet.helius-rpc.com",
  );
  await rpcDialog.getByRole("button", { name: "Save", exact: true }).click();
  await rpcDialog.getByRole("button", { name: "API key", exact: true }).click();
  await expect(rpcDialog.getByLabel("Vault password")).toHaveCount(0);
  await rpcDialog
    .getByLabel("RPC API key", { exact: true })
    .fill("disposable-os-rpc-input-not-issued-12345");
  await rpcDialog
    .getByRole("button", { name: "Save key", exact: true })
    .click();
  await expect(
    rpcDialog.getByRole("button", { name: "Remove key", exact: true }),
  ).toBeVisible();
  await rpcDialog
    .getByRole("button", { name: "Check connection", exact: true })
    .click();
  await expect(rpcDialog.getByText("Connected", { exact: true })).toBeVisible();
  await rpcDialog.getByRole("button", { name: "Close dialog" }).click();

  await balanceRegion
    .getByRole("button", { name: "Refresh", exact: true })
    .click();
  await expect(page.getByText("0.123456789 SOL")).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Unlock RPC" })).toHaveCount(0);

  await page.getByRole("button", { name: "RPC", exact: true }).click();
  rpcDialog = page.getByRole("dialog", {
    name: "RPC · Solana Watch",
    exact: true,
  });
  await rpcDialog.getByRole("button", { name: "API key", exact: true }).click();
  await rpcDialog
    .getByRole("button", { name: "Remove key", exact: true })
    .click();
  await expect(
    rpcDialog.getByRole("button", { name: "Remove key", exact: true }),
  ).toHaveCount(0);
  expect(
    postedCredActions.some((a) => a.action === "save") &&
      postedCredActions.some((a) => a.action === "remove"),
  ).toBe(true);
});
