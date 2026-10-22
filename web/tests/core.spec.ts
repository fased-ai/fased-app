import { expect, test } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import { once } from "node:events";
import { createHash, randomBytes } from "node:crypto";

const exec = promisify(execFile);
const binary = resolve(
  process.env.FASED_CORE_BINARY ?? ".evidence/bin/fased-app",
);
const fixturePassword = "generated-browser-fixture-password";
let dir: string;
let child: ChildProcess | undefined;
let url: string;
let output = "";

async function start(aiFixture = false) {
  output = "";
  child = spawn(binary, [
    "serve",
    ...(aiFixture ? ["--ai-fixture"] : []),
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    dir,
  ]);
  child.stdout!.on("data", (data) => (output += String(data)));
  child.stderr!.on("data", (data) => (output += String(data)));
  await expect.poll(() => output.includes('"event":"listening"')).toBe(true);
  url = JSON.parse(output.trim().split("\n")[0]).url;
}
async function stop() {
  if (!child || child.exitCode !== null) return;
  const done = once(child, "exit");
  child.kill("SIGTERM");
  await done;
  child = undefined;
}
async function privateCLI(args: string[], input?: unknown) {
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
  const [code] = await once(child, "exit");
  if (code !== 0) throw new Error(error);
  return JSON.parse(output);
}
test.beforeEach(async () => {
  const root = resolve(".evidence/core-browser-fixtures");
  await mkdir(root, { recursive: true, mode: 0o700 });
  dir = await mkdtemp(join(root, "f-"));
  await start();
});
test.afterEach(async () => {
  try {
    await stop();
  } finally {
    if (dir) await rm(dir, { recursive: true, force: true });
  }
});

test("update preferences and review-only proposals share revisions, survive restart and obey the metadata pause", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#settings");
  const panel = page.getByRole("region", {
    name: "Update preferences & proposals",
  });
  await expect(
    panel.getByLabel("Show notices for pending proposals"),
  ).toBeChecked();
  await panel.getByLabel("Pin a release identity for review").check();
  await panel.getByLabel("Pinned sequence", { exact: true }).fill("7");
  await panel
    .getByLabel("Pinned manifest SHA-256", { exact: true })
    .fill("a".repeat(64));
  await panel.getByLabel("Set a preferred maintenance window").check();
  await panel.getByLabel("IANA timezone").fill("America/Chicago");
  await panel.getByLabel("Window start").fill("23:00");
  await panel.getByLabel("Window end").fill("01:00");
  await panel
    .getByRole("button", { name: "Save update preferences", exact: true })
    .click();
  await expect(panel.getByRole("status")).toContainText(
    "Saved app metadata only",
  );
  const initial = await privateCLI(["updates", "preferences"]);
  expect(initial.policy.window.timeZone).toBe("America/Chicago");
  expect(initial.policy.pin.sequence).toBe("7");
  await panel
    .getByLabel("Release label", { exact: true })
    .fill("Disposable review");
  await panel.getByLabel("Claimed sequence", { exact: true }).fill("7");
  await panel
    .getByLabel("Claimed manifest SHA-256", { exact: true })
    .fill("a".repeat(64));
  await panel
    .getByRole("button", { name: "Add review-only proposal", exact: true })
    .click();
  const card = panel.getByRole("article", {
    name: "Update proposal Disposable review",
  });
  await expect(card.getByText("UNVERIFIED", { exact: true })).toBeVisible();
  await expect(
    panel.getByRole("heading", { name: "Update notices (1)", exact: true }),
  ).toBeVisible();
  const proposed = await privateCLI(["updates", "preferences"]);
  const duplicate = await privateCLI(
    ["updates", "preferences", "--request-stdin"],
    {
      revision: proposed.revision,
      action: "propose",
      candidate: proposed.proposals[0].candidate,
    },
  );
  expect(duplicate.revision).toBe(proposed.revision);
  expect(duplicate.notifications).toEqual(proposed.notifications);
  await privateCLI(["updates", "preferences", "--request-stdin"], {
    revision: proposed.revision,
    action: "policy",
    policy: { ...proposed.policy, notify: false },
  });
  await card
    .getByRole("button", { name: "Mark reviewed", exact: true })
    .click();
  await expect(panel.getByRole("alert")).toContainText(
    "settings changed in another client",
  );
  await expect(card).toHaveCount(0);
  await panel
    .getByRole("button", { name: "Refresh update preferences", exact: true })
    .click();
  await expect(
    panel.getByRole("heading", { name: "Update notices (0)", exact: true }),
  ).toBeVisible();
  await card
    .getByRole("button", { name: "Mark reviewed", exact: true })
    .click();
  await expect(card).toContainText("reviewed");
  const reviewed = await privateCLI(["updates", "preferences"]);
  expect(reviewed.proposals[0].verified).toBe(false);
  expect(reviewed.installAllowed).toBe(false);
  expect(reviewed.resumeAllowed).toBe(false);
  expect(reviewed.automaticUpdates).toBe(false);
  expect(reviewed.financialExecution).toBe(false);
  await page.screenshot({
    path: `.evidence/p4-policy/preferences-${test.info().project.name}.png`,
    fullPage: true,
  });
  const probe = randomBytes(16).toString("hex"),
    snapshot = await privateCLI(["update-inventory", "--probe-id", probe]);
  const pause = {
    version: 1,
    intentId: randomBytes(16).toString("hex"),
    sequence: "1",
    manifestSha256: createHash("sha256")
      .update("disposable policy pause")
      .digest("hex"),
    expected: snapshot,
  };
  await privateCLI(["update-pause", "--request-stdin"], {
    action: "pause",
    probeId: probe,
    request: pause,
  });
  await panel.getByLabel("Show notices for pending proposals").check();
  await panel
    .getByRole("button", { name: "Save update preferences", exact: true })
    .click();
  await expect(panel.getByRole("alert")).toContainText(
    "core metadata paused for update",
  );
  expect((await privateCLI(["updates", "preferences"])).revision).toBe(
    reviewed.revision,
  );
  await stop();
  await start();
  await page.goto(url + "/#settings");
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(panel.getByLabel("IANA timezone")).toHaveValue(
    "America/Chicago",
  );
  await expect(card).toContainText("reviewed");
  expect((await privateCLI(["updates", "preferences"])).policy).toEqual(
    reviewed.policy,
  );
  await privateCLI(["update-pause", "--request-stdin"], {
    action: "release",
    probeId: probe,
    aborted: {
      id: pause.intentId,
      sequence: "1",
      manifestSha256: pause.manifestSha256,
      core: snapshot,
      state: "aborted",
    },
  });
});

test("update status shares UI and private CLI observations across pause, release and restart", async ({
  page,
}) => {
  await expect(
    exec(binary, ["updates", "status", "--data-dir", dir]),
  ).rejects.toThrow();
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await page.goto(url + "/#settings");
  const panel = page.getByRole("region", { name: "Updates & recovery" });
  await expect(
    panel.getByRole("heading", { name: "Signer not configured" }),
  ).toBeVisible();
  await expect(panel.getByText("Unknown", { exact: true })).toHaveCount(3);
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
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
  const before = await cli(["updates", "status"]);
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
  const held = await cli(["updates", "status"]);
  expect(held.core).toEqual({ phase: "paused", intentId: request.intentId });
  expect(held.signer).toEqual(before.signer);
  expect(held.guidance.some((g: { code: string }) => g.code === "paused")).toBe(
    true,
  );
  await page.screenshot({
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
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
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
  expect((await cli(["updates", "status"])).core.phase).toBe("released");
});

test("update recovery view rejects unsupported or lost status without retaining a healthy observation (UI mock only)", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
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
  await page.screenshot({
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

test("claim, UI/CLI setup, restart and logout use one private instance", async ({
  page,
}) => {
  await page.goto(url);
  await expect(
    page.getByRole("heading", { name: "Claim this workspace" }),
  ).toBeVisible();
  await page.screenshot({
    path: `.evidence/claim-${test.info().project.name}.png`,
    fullPage: true,
  });
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  const health = await (await page.request.get(`${url}/api/v1/health`)).json();
  expect(health.capabilities.walletSigner).toBe(false);
  expect(health.capabilities.financialExecution).toBe(false);
  expect(
    await page.evaluate(() =>
      Object.keys(localStorage).some((key) => key.includes("session")),
    ),
  ).toBe(false);
  expect(output).not.toContain(claim.code);
  expect(output).not.toContain(fixturePassword);
  await page
    .getByRole("button", { name: "Set up workspace", exact: true })
    .click();
  await page.getByRole("button", { name: /Private VPS Always-on/ }).click();
  await expect(
    page.getByRole("button", { name: /Private VPS Always-on/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Close dialog" }).click();
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();

  // CLI credentials travel over the protected socket, never URL/arguments.
  await new Promise<void>((resolveLogin, reject) => {
    const login = spawn(binary, [
      "login",
      "--data-dir",
      dir,
      "--password-stdin",
    ]);
    login.stdin.end(fixturePassword + "\n");
    login.on("error", reject);
    login.on("exit", (code) =>
      code === 0
        ? resolveLogin()
        : reject(new Error("fixture CLI login failed")),
    );
  });
  const current = JSON.parse(
    (await exec(binary, ["setup", "--data-dir", dir])).stdout,
  );
  expect(current.settings.host).toBe("vps");
  await exec(binary, [
    "setup",
    "--data-dir",
    dir,
    "--host",
    "local",
    "--wallet-mode",
    "watch",
  ]);
  await page.reload();
  await page
    .getByRole("button", { name: "Set up workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: /Local computer Simpler/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Save setup preferences" }),
  ).toBeEnabled();
  // Another client edits after the review was loaded. The UI must stay open,
  // show the conflict and require a second review rather than report success.
  await exec(binary, ["setup", "--data-dir", dir, "--provider", "anthropic"]);
  await page.getByRole("button", { name: "Save setup preferences" }).click();
  await expect(page.getByRole("dialog").getByRole("alert")).toHaveText(
    /settings changed in another client/,
  );
  expect(
    JSON.parse((await exec(binary, ["setup", "--data-dir", dir])).stdout)
      .settings.complete,
  ).toBe(false);
  await page.getByRole("button", { name: "Save setup preferences" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(
    JSON.parse((await exec(binary, ["setup", "--data-dir", dir])).stdout)
      .settings.complete,
  ).toBe(true);
  const duplicate = await exec(binary, [
    "serve",
    "--listen",
    "127.0.0.1:0",
    "--data-dir",
    dir,
  ]).then(
    () => "unexpected",
    (e: Error) => e.message,
  );
  expect(duplicate).toContain("already owns");

  await stop();
  await start();
  const restarted = JSON.parse(
    (await exec(binary, ["status", "--data-dir", dir])).stdout,
  );
  expect(restarted.instanceId).toBe(health.instanceId);
  expect(
    JSON.parse((await exec(binary, ["setup", "--data-dir", dir])).stdout)
      .settings.walletMode,
  ).toBe("watch");
  await page.goto(url);
  await expect(
    page.getByRole("heading", { name: "Welcome back" }),
  ).toBeVisible();
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome back" }),
  ).toBeVisible();
  expect((await page.request.get(`${url}/api/v1/setup`)).status()).toBe(401);
});

test("production UI fails closed when the core API is unavailable", async ({
  page,
}) => {
  await page.route("**/api/v1/auth/state", (route) =>
    route.fulfill({
      status: 503,
      contentType: "text/html",
      body: "unavailable",
    }),
  );
  await page.goto(url);
  await expect(
    page.getByRole("heading", { name: "Core connection unavailable" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Set up workspace" }),
  ).toHaveCount(0);
});

test("public account records share UI/CLI state without wallet authority", async ({
  page,
}) => {
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await page.goto(url + "/#wallets/accounts");
  await expect(
    page.getByRole("heading", { name: "Public account records" }),
  ).toBeVisible();
  await page.getByLabel("Name", { exact: true }).fill("Disposable watch");
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
  expect(state.record.accounts[0].name).toBe("Disposable watch");
  expect(state.fixture.available).toBe(false);
  const stale = structuredClone(state.record);
  await page.getByRole("button", { name: "Rename", exact: true }).click();
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
  await page
    .getByRole("button", { name: "Refresh accounts and approvals" })
    .click();
  await expect(
    page.getByRole("heading", { name: "External manual fixture", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("REAL WALLETS DISABLED", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: `.evidence/p3-accounts/public-accounts-${test.info().project.name}.png`,
    fullPage: true,
  });
  await page.evaluate(() => sessionStorage.removeItem("fased.core.session.v1"));
  await stop();
  await start();
  await page.goto(url + "/#wallets/accounts");
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
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  await page.goto(url + "/#wallets/accounts");
  await expect(page.getByLabel("Fixture account", { exact: true })).toBeVisible(
    { timeout: 1000 },
  );
  await page
    .getByLabel("Fixture account", { exact: true })
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
  await expect(page.getByText("not-approved", { exact: true })).toBeVisible();
});

test("local agents share UI and CLI records, isolate profiles and survive core restart", async ({
  page,
}) => {
  test.setTimeout(60000);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/chat");
  await page.getByRole("button", { name: "New profile", exact: true }).click();
  await page
    .getByLabel("Profile name", { exact: true })
    .fill("Local acceptance researcher");
  await page
    .getByRole("button", { name: "Create profile", exact: true })
    .click();
  await expect(
    page.getByRole("heading", {
      name: "Local acceptance researcher · Conversations",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByLabel("Conversation name").fill("Saved thesis");
  await page
    .getByRole("button", { name: "Create conversation", exact: true })
    .click();
  await page
    .getByLabel("Message", { exact: true })
    .fill("Our local thesis survives restart");
  await page.getByRole("button", { name: "Save message", exact: true }).click();
  await expect(
    page.getByText("Our local thesis survives restart", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Saved locally. No AI provider was called.", {
      exact: false,
    }),
  ).toBeVisible();
  let view = await privateCLI(["agents"]);
  const p = view.profiles.find(
    (x: { name: string }) => x.name === "Local acceptance researcher",
  ).id;
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.messages).toHaveLength(2);
  const localCommand = async (command: object) => {
    const prior = await privateCLI(["agents", "--profile", p]);
    return privateCLI(["agents", "--request-stdin"], {
      revision: prior.revision,
      profileId: p,
      ...command,
    });
  };
  view = await localCommand({
    action: "memory-put",
    text: "Concise answers",
    source: "Owner preference",
  });
  const memoryID = view.memories[0].id;
  await page.getByRole("button", { name: "memory", exact: true }).click();
  await expect(
    page.getByText("Concise answers", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Correct note", exact: true }).click();
  await page.getByLabel("Memory note").fill("Detailed answers");
  await page
    .getByLabel("Evidence or source")
    .fill("Corrected owner preference");
  await page
    .getByRole("button", { name: "Save correction", exact: true })
    .click();
  await expect(
    page.getByText("Detailed answers", { exact: true }),
  ).toBeVisible();
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.memories[0].id).toBe(memoryID);
  expect(view.memories[0].history[0].text).toBe("Concise answers");
  await page.getByRole("button", { name: "settings", exact: true }).click();
  await page.getByLabel("Provider preference").selectOption("openai");
  await page.getByLabel("Model preference").fill("reference-only");
  await page.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect
    .poll(
      async () =>
        (await privateCLI(["agents", "--profile", p])).profiles.find(
          (x: { id: string }) => x.id === p,
        ).provider,
    )
    .toBe("openai");
  await page.getByRole("button", { name: "bots", exact: true }).click();
  await page.getByLabel("Paper bot name").fill("Fixed example");
  await page
    .getByRole("button", { name: "Create paper bot", exact: true })
    .click();
  await expect(page.getByText("Fixed example", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "tasks", exact: true }).click();
  await page
    .getByLabel("Task name", { exact: true })
    .fill("One paper evaluation");
  view = await privateCLI(["agents", "--profile", p]);
  await page.getByLabel("Task kind").selectOption(view.bots[0].id);
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await page
    .getByRole("button", { name: "Run now: One paper evaluation", exact: true })
    .click();
  await expect(page.getByText("paper-filled", { exact: true })).toBeVisible();
  view = await privateCLI(["agents", "--profile", p]);
  expect(view.runs).toHaveLength(1);
  expect(view.bots[0].spentCents).toBe(100);
  expect(view.financialExecution).toBe(false);
  expect(view.providerCallsEnabled).toBe(false);
  const other = await privateCLI(["agents", "--profile", "news-researcher"]);
  expect(other.messages).toHaveLength(0);
  expect(other.memories).toHaveLength(0);
  expect(other.runs).toHaveLength(0);
  expect(
    await page
      .locator(".local-agents .memory-card")
      .first()
      .locator("p")
      .evaluate((e) => e.getBoundingClientRect().width),
  ).toBeGreaterThan(150);
  await page.getByRole("button", { name: "Refresh", exact: true }).focus();
  await page.screenshot({
    path: resolve(
      ".evidence/p5-local-agents/tasks-" + test.info().project.name + ".png",
    ),
    fullPage: true,
  });
  await stop();
  await start();
  await page.goto(url + "/#agents/chat");
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page
    .locator(".agent-picker")
    .getByRole("button", { name: "Local acceptance researcher", exact: false })
    .click();
  await expect(
    page.getByText("Our local thesis survives restart", { exact: true }),
  ).toBeVisible();
  view = await privateCLI(["agents", "--profile", p, "--search", "thesis"]);
  expect(view.messages).toHaveLength(2);
  expect(view.runs).toHaveLength(1);
  expect(view.notices).toHaveLength(1);
  expect(view.memories[0].text).toBe("Detailed answers");
  await page.screenshot({
    path: resolve(
      ".evidence/p5-local-agents/chat-" + test.info().project.name + ".png",
    ),
    fullPage: true,
  });
});

test("bounded news inputs retain provenance across UI CLI and restart, hold dependent failures and isolate profiles", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/news");
  const panel = page.getByRole("region", { name: "News inputs", exact: true });
  await panel
    .getByLabel("News source name", { exact: true })
    .fill("Controlled headlines");
  await panel
    .getByLabel("Public RSS URL", { exact: true })
    .fill("https://news.fased.invalid/headlines.xml");
  await panel
    .getByLabel("Source freshness limit (seconds)", { exact: true })
    .fill("600");
  await panel
    .getByRole("button", { name: "Add news source", exact: true })
    .click();
  const source = panel.getByRole("article", {
    name: "News source Controlled headlines",
    exact: true,
  });
  await source
    .getByRole("button", { name: "Fetch headlines", exact: true })
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).newsFetches[0]?.state)
    .toBe("completed");
  await panel
    .getByRole("button", { name: "Refresh news records", exact: true })
    .click();
  await expect(source).toContainText("Controlled paper-news headline");
  let v = await privateCLI(["agents"]);
  const sourceID = v.newsSources[0].id;
  const first = v.newsFetches[0];
  expect(first.bodySha256).toMatch(/^[a-f0-9]{64}$/);
  expect(first.items).toHaveLength(1);
  expect(first.fetchedAt).toBeGreaterThanOrEqual(first.items[0].publishedAt);
  expect(
    (await privateCLI(["agents", "--profile", "trading-analyst"])).newsFetches,
  ).toHaveLength(0);
  await page.goto(url + "/#agents/tasks");
  await page
    .getByRole("combobox", { name: "News input", exact: true })
    .first()
    .selectOption(sourceID);
  await page
    .getByLabel("Task name", { exact: true })
    .fill("Sourced UI reminder");
  await page
    .getByLabel("Reminder text", { exact: true })
    .fill("Review the retained headlines");
  await page.getByLabel("Maximum evaluations", { exact: true }).fill("1");
  await page.getByRole("button", { name: "Create task", exact: true }).click();
  await page
    .getByRole("button", { name: "Run now: Sourced UI reminder", exact: true })
    .click();
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[0]?.state)
    .toBe("completed");
  v = await privateCLI(["agents"]);
  expect(v.runs[0].news.sourceId).toBe(sourceID);
  expect(v.runs[0].news.fetchId).not.toBe(first.id);
  expect(v.aiRequests).toHaveLength(0);
  await page.goto(url + "/#agents/news");
  await panel
    .getByRole("button", { name: "Refresh news records", exact: true })
    .click();
  await panel
    .getByLabel("News source name", { exact: true })
    .fill("Unavailable headlines");
  await panel
    .getByLabel("Public RSS URL", { exact: true })
    .fill("https://news.fased.invalid/unavailable.xml");
  await panel
    .getByRole("button", { name: "Add news source", exact: true })
    .click();
  await expect(
    panel.getByRole("article", {
      name: "News source Unavailable headlines",
      exact: true,
    }),
  ).toBeVisible();
  v = await privateCLI(["agents"]);
  const bad = v.newsSources.find(
    (s: { name: string }) => s.name === "Unavailable headlines",
  );
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-create",
    name: "Dependent failure",
    text: "Hold this reminder",
    intervalSeconds: 60,
    maxRuns: 1,
    newsSourceId: bad.id,
    newsMaxAgeSeconds: 300,
  });
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-run",
    id: v.tasks[1].id,
  });
  await expect
    .poll(async () => (await privateCLI(["agents"])).tasks[1]?.state)
    .toBe("held");
  v = await privateCLI(["agents"]);
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-create",
    name: "Independent success",
    text: "No news needed",
    intervalSeconds: 60,
    maxRuns: 1,
  });
  await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    action: "task-run",
    id: v.tasks[2].id,
  });
  await stop();
  await start();
  await page.goto(url);
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/news");
  await expect(source).toContainText(first.bodySha256);
  await expect(
    panel.getByRole("article", {
      name: "News source Unavailable headlines",
      exact: true,
    }),
  ).toContainText("failed");
  v = await privateCLI(["agents"]);
  expect(v.tasks.map((t: { state: string }) => t.state)).toEqual([
    "completed",
    "held",
    "completed",
  ]);
  expect(v.newsFetches[0].bodySha256).toBe(first.bodySha256);
  expect(v.providerCallsEnabled).toBe(false);
  expect(v.financialExecution).toBe(false);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth + 1,
    ),
  ).toBe(true);
  await page.screenshot({
    path: `.evidence/p5-news/${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("OpenAI test connection shares encrypted storage, bound consent and restart lock across UI and CLI", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/settings");
  const panel = page.getByRole("region", {
    name: "OpenAI connection",
    exact: true,
  });
  await expect(panel.getByRole("status")).toHaveText("No connection saved");
  const key = "fased-test-browser-disposable-key";
  const passphrase = "disposable-browser-vault-passphrase";
  await panel.getByLabel("Disposable API key").fill(key);
  await panel.getByLabel("Vault passphrase").fill(passphrase);
  await panel.getByRole("button", { name: "Save test connection" }).click();
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await expect(panel.getByLabel("Disposable API key")).toHaveValue("");
  await expect(panel.getByLabel("Vault passphrase")).toHaveValue("");
  let connection = await privateCLI(["ai-connection"]);
  expect(connection.locked).toBe(false);
  expect(connection.liveCallsEnabled).toBe(false);
  const firstRef = connection.ref;
  const ciphertext = await readFile(join(dir, "ai-credentials.json"), "utf8");
  expect(ciphertext).not.toContain(key);
  expect(ciphertext).not.toContain(passphrase);
  await page.goto(url + "/#agents/chat");
  await page.getByLabel("Conversation name").fill("Connected fixture review");
  await page.getByRole("button", { name: "Create conversation" }).click();
  const review = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await review
    .getByRole("combobox", { name: "AI connection", exact: true })
    .selectOption(firstRef);
  await review
    .getByLabel("AI request", { exact: true })
    .fill("Analyze fixed paper inputs using the test connection");
  await review.getByRole("button", { name: "Prepare disclosure" }).click();
  await review.getByText("Exact context disclosure", { exact: true }).click();
  await expect(review).toContainText(firstRef);
  await review
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await review.getByRole("button", { name: "Run development fixture" }).click();
  await expect(review.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  let agents = await privateCLI(["agents"]);
  expect(agents.aiRequests[0].connectionRef).toBe(firstRef);
  expect(agents.aiRequests[0].heldMicrousd).toBe(0);
  expect(JSON.stringify(agents)).not.toContain(key);
  expect(
    await page.evaluate(() => JSON.stringify([localStorage, sessionStorage])),
  ).not.toContain(key);
  await stop();
  await start();
  await page.goto(url);
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/settings");
  await expect(panel.getByRole("status")).toContainText("Locked");
  connection = await privateCLI(["ai-connection"]);
  expect(connection.ref).toBe(firstRef);
  expect(connection.locked).toBe(true);
  connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "unlock",
    ref: firstRef,
    passphrase,
  });
  expect(connection.locked).toBe(false);
  await page.reload();
  await expect(panel.getByRole("status")).toContainText("Unlocked");
  await panel
    .getByRole("button", { name: "Lock connection", exact: true })
    .click();
  await expect(panel.getByRole("status")).toContainText("Locked");
  expect((await privateCLI(["ai-connection"])).locked).toBe(true);
  connection = await privateCLI(["ai-connection", "--request-stdin"], {
    action: "save",
    ref: firstRef,
    key: key + "-replacement",
    passphrase,
  });
  expect(connection.ref).not.toBe(firstRef);
  await privateCLI(["ai-connection", "--request-stdin"], {
    action: "remove",
    ref: connection.ref,
    passphrase,
  });
  await page.reload();
  await expect(panel.getByRole("status")).toHaveText("No connection saved");
  agents = await privateCLI(["agents"]);
  expect(agents.aiRequests[0].state).toBe("completed");
  expect(agents.providerCallsEnabled).toBe(false);
  expect(output).not.toContain(key);
  await page.screenshot({
    path: `.evidence/p5-openai-connection/${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("optional provider fixture requires scoped disclosure and budgets, preserves review and never creates a task", async ({
  page,
}) => {
  test.setTimeout(60000);
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/chat");
  await page
    .getByLabel("Conversation name")
    .fill("Provider fixture conversation");
  await page.getByRole("button", { name: "Create conversation" }).click();
  await expect(
    page.getByRole("heading", {
      name: "Provider fixture conversation",
      exact: true,
    }),
  ).toBeVisible();
  const panel = page.getByRole("region", {
    name: "Optional AI request review",
  });
  await panel
    .getByLabel("AI request", { exact: true })
    .fill("Compare a bounded paper strategy");
  await panel.getByRole("button", { name: "Prepare disclosure" }).click();
  await expect(panel.getByText("prepared", { exact: true })).toBeVisible();
  const run = panel.getByRole("button", { name: "Run development fixture" });
  await expect(run).toBeDisabled();
  await panel.getByText("Exact context disclosure", { exact: true }).click();
  await expect(panel.locator("pre")).toContainText('"store":false');
  await panel
    .getByLabel("I reviewed this context and these fixture limits")
    .check();
  await run.click();
  await expect(panel.getByText("completed", { exact: true })).toBeVisible({
    timeout: 12000,
  });
  const card = panel.getByRole("region", { name: "Review paper journal" });
  await card.getByRole("button", { name: "Keep draft" }).click();
  await expect(card).toContainText("kept");
  const view = await privateCLI(["agents"]);
  expect(view.aiRequests).toHaveLength(1);
  expect(view.tasks).toHaveLength(0);
  expect(view.aiRequests[0].heldMicrousd).toBe(0);
  expect(view.providerCallsEnabled).toBe(false);
  const request = {
    revision: view.revision,
    action: "ai-prepare",
    profileId: view.profileId,
    id: view.sessions[0].id,
    text: "Cancelled preview",
    limits: {
      maxInputTokens: 24000,
      maxOutputTokens: 1024,
      deadlineSeconds: 3,
      budgetMicrousd: 30000,
    },
  };
  const next = await privateCLI(["agents", "--request-stdin"], request);
  await privateCLI(["agents", "--request-stdin"], {
    revision: next.revision,
    action: "ai-cancel",
    profileId: view.profileId,
    id: next.aiRequests[1].id,
  });
  await stop();
  await start();
  await page.goto(url);
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/chat");
  await expect(page.getByText("completed", { exact: true })).toBeVisible();
  await expect(page.getByText("cancelled", { exact: true })).toBeVisible();
  const saved = await privateCLI(["agents"]);
  expect(saved.aiFixtureAvailable).toBe(false);
  expect(saved.aiRequests[0].cards[0].review).toBe("kept");
  expect(saved.tasks).toHaveLength(0);
  const profileName = saved.profiles.find(
    (p: { id: string }) => p.id === saved.profileId,
  ).name;
  const sessionName = "Provider fixture conversation";
  const question = saved.aiRequests[0].prompt;
  const answer = "No model, market feed or wallet was contacted.";
  const checkSearch = async () => {
    for (const query of [question.toUpperCase(), answer.toUpperCase()]) {
      await page.getByLabel("Search conversations").fill(query);
      await expect(
        page.locator(".agent-session-list").getByRole("button", {
          name: sessionName,
          exact: true,
        }),
      ).toBeVisible();
      const hit = await privateCLI([
        "agents",
        "--profile",
        saved.profileId,
        "--search",
        query,
      ]);
      expect(hit.sessions.map((s: { id: string }) => s.id)).toEqual([
        saved.aiRequests[0].sessionId,
      ]);
      const other = await privateCLI([
        "agents",
        "--profile",
        "news-researcher",
        "--search",
        query,
      ]);
      expect(other.sessions).toHaveLength(0);
      expect(other.aiRequests).toHaveLength(0);
      expect(hit.providerCallsEnabled).toBe(false);
    }
  };
  await checkSearch();
  await page
    .locator(".local-profile-picker")
    .getByRole("button", {
      name: /News Researcher/,
    })
    .click();
  await page.getByLabel("Search conversations").fill(answer);
  await expect(page.locator(".agent-session-list button")).toHaveCount(0);
  await page
    .locator(".local-profile-picker")
    .getByRole("button", {
      name: new RegExp(profileName),
    })
    .click();
  await page.getByRole("button", { name: "settings", exact: true }).click();
  await page.getByLabel("Provider preference").selectOption("anthropic");
  await page.getByLabel("Model preference").fill("preference-only");
  await page.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect
    .poll(async () => {
      const current = await privateCLI([
        "agents",
        "--profile",
        saved.profileId,
      ]);
      return current.profiles.find(
        (p: { id: string }) => p.id === saved.profileId,
      ).provider;
    })
    .toBe("anthropic");
  await page.getByRole("button", { name: "chat", exact: true }).click();
  await checkSearch();
  await page.getByLabel("Search conversations").fill("no-such-phrase");
  await expect(page.locator(".agent-session-list button")).toHaveCount(0);
  await page.getByLabel("Search conversations").fill("");
  await mkdir(".evidence/p5-ai-search", { recursive: true });
  await page.screenshot({
    path: `.evidence/p5-ai-search/search-${test.info().project.name}.png`,
    fullPage: true,
  });
});

test("bounded AI schedules share consent and execution with CLI, survive restart and keep paper inputs fixed", async ({
  page,
}) => {
  await stop();
  await start(true);
  await page.goto(url);
  const claim = JSON.parse(
    await readFile(join(dir, "claim-code.json"), "utf8"),
  );
  await page.getByLabel("One-use claim code").fill(claim.code);
  await page.getByLabel("Create owner password").fill(fixturePassword);
  await page.getByRole("button", { name: "Claim workspace" }).click();
  await expect(
    page.getByText("PRIVATE CORE · TERMINAL PROTOTYPES", { exact: true }),
  ).toBeVisible();
  const login = spawn(binary, ["login", "--data-dir", dir, "--password-stdin"]);
  login.stdin!.end(fixturePassword + "\n");
  expect((await once(login, "exit"))[0]).toBe(0);
  await page.goto(url + "/#agents/chat");
  await page.getByLabel("Conversation name").fill("Scheduled context");
  await page.getByRole("button", { name: "Create conversation" }).click();
  await expect(
    page.getByRole("heading", { name: "Scheduled context", exact: true }),
  ).toBeVisible();
  let v = await privateCLI(["agents"]);
  v = await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    profileId: v.profileId,
    action: "bot-create",
    name: "Fixed task paper",
    quantity: 2,
    priceCents: 5,
    budgetCents: 20,
  });
  const bot = v.bots[0].id;
  await page.goto(url + "/#agents/tasks");
  const region = page.getByRole("region", {
    name: "Scheduled AI steps",
    exact: true,
  });
  await expect(
    region.getByLabel("Step after validated AI response").getByRole("option", {
      name: "Fixed paper evaluation · Fixed task paper",
    }),
  ).toHaveCount(1);
  await region.getByLabel("AI task title").fill("Two bounded AI evaluations");
  await region
    .getByLabel("AI analysis prompt")
    .fill("Compare fixed paper inputs");
  await region.getByLabel("Step after validated AI response").selectOption(bot);
  await region.getByRole("button", { name: "Prepare AI task review" }).click();
  const task = region.getByRole("article", {
    name: "AI task Two bounded AI evaluations",
  });
  await expect(task.getByText("review", { exact: true })).toBeVisible();
  const authorize = task.getByRole("button", {
    name: "Authorize fixture schedule",
  });
  await expect(authorize).toBeDisabled();
  await task.getByText("Review AI task disclosure").click();
  await expect(task.locator("pre")).toContainText('"store":false');
  await task
    .getByLabel("Allow this frozen context, schedule and limits")
    .check();
  await authorize.click();
  await expect(task.getByText("scheduled", { exact: true })).toBeVisible();
  await task.getByRole("button", { name: "Evaluate now" }).click();
  await page.goto(url + "/#overview");
  await expect
    .poll(async () => {
      v = await privateCLI(["agents"]);
      return v.tasks[0].runs;
    })
    .toBe(1);
  expect(v.bots[0].spentCents).toBe(10);
  expect(v.aiRequests).toHaveLength(1);
  const taskId = v.tasks[0].id;
  await stop();
  await start(true);
  v = await privateCLI(["agents"]);
  await privateCLI(["agents", "--request-stdin"], {
    revision: v.revision,
    profileId: v.profileId,
    action: "task-run",
    id: taskId,
  });
  await expect
    .poll(async () => {
      v = await privateCLI(["agents"]);
      return v.tasks[0].state;
    })
    .toBe("completed");
  expect(v.bots[0].spentCents).toBe(20);
  expect(v.runs).toHaveLength(2);
  expect(v.aiRequests).toHaveLength(2);
  expect(v.aiRequests[0].id).not.toBe(v.aiRequests[1].id);
  expect(v.tasks).toHaveLength(1);
  expect(v.providerCallsEnabled).toBe(false);
  await page.goto(url);
  await page
    .getByLabel("Owner password", { exact: true })
    .fill(fixturePassword);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.goto(url + "/#agents/tasks");
  await expect(task.getByText("completed", { exact: true })).toBeVisible();
  await expect(task).toContainText("2/2 evaluations");
  await expect(task).toContainText(
    "No model, market feed or wallet was contacted.",
  );
  await region.getByLabel("AI task title").fill("Cancelled schedule");
  await region.getByLabel("AI analysis prompt").fill("No call needed");
  await region.getByRole("button", { name: "Prepare AI task review" }).click();
  const cancelled = region.getByRole("article", {
    name: "AI task Cancelled schedule",
  });
  await cancelled.getByRole("button", { name: "Cancel AI schedule" }).click();
  await expect(cancelled.getByText("cancelled", { exact: true })).toBeVisible();
  await mkdir(".evidence/p5-ai-tasks", { recursive: true });
  await page.screenshot({
    path: `.evidence/p5-ai-tasks/tasks-${test.info().project.name}.png`,
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= window.innerWidth,
    ),
  ).toBe(true);
});
