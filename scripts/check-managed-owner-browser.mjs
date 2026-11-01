import { openOwnerSetup } from "./fixture-owner-setup.mjs";
// Disposable Ubuntu installation + real SSH forwarding only. No owner browser,
// user credentials, API provider or external endpoint is used by this check.
import assert from "node:assert/strict";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { resolve, join } from "node:path";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { chromium, expect } from "@playwright/test";

assert.equal(process.env.FASED_DISPOSABLE_VM_ACCEPTANCE, "1");
assert.ok(process.getuid() > 0);
const root = resolve(".evidence/b01-owner-browser");
const config = JSON.parse(
  await readFile(join(root, "browser-config.json"), "utf8"),
);
assert.equal(config.fixtureOnly, true);
assert.equal(config.url, "http://127.0.0.1:17447");
assert.equal(config.core, "/var/lib/fased-app-vps-1000/fased-app");
assert.equal(config.ssh.at(-1), "ubuntu@127.0.0.1");
assert.equal(
  config.commandPrefix,
  "XDG_CONFIG_HOME=/home/ubuntu/fased/.evidence/p4-fault-reboot/guest/owner-config ",
);
const execute = promisify(execFile);
const password = "disposable-owner-browser-password-one";
const replacement = "disposable-owner-browser-password-two";
let claimCode = "",
  staleToken = "";
const scrub = (value) =>
  [password, replacement, claimCode, staleToken]
    .filter(Boolean)
    .reduce(
      (s, secret) => s.replaceAll(secret, "[disposable secret]"),
      String(value),
    );
async function cli(action) {
  assert.ok(["claim-code", "stop", "start", "status"].includes(action));
  const result = await execute(
    "ssh",
    [...config.ssh, `${config.commandPrefix}${config.core} vps ${action}`],
    { timeout: 30000, maxBuffer: 1024 * 1024 },
  );
  return result.stdout.trim();
}
async function recover() {
  const args = [
    ...config.ssh.slice(0, -1),
    "-tt",
    config.ssh.at(-1),
    `${config.commandPrefix}${config.core} vps recover`,
  ];
  const child = spawn("ssh", args, { stdio: ["pipe", "pipe", "pipe"] });
  let output = "",
    entered = false,
    confirmed = false;
  const collect = (bytes) => {
    output += bytes.toString();
    if (!entered && output.includes("New owner password: ")) {
      entered = true;
      child.stdin.write(replacement + "\n");
    }
    if (!confirmed && output.includes("Confirm new password: ")) {
      confirmed = true;
      child.stdin.write(replacement + "\n");
    }
  };
  child.stdout.on("data", collect);
  child.stderr.on("data", collect);
  const timer = setTimeout(() => child.kill("SIGKILL"), 30000);
  try {
    const code = await new Promise((done, fail) => {
      child.once("error", fail);
      child.once("exit", done);
    });
    await writeFile(join(root, "recovery-terminal.log"), scrub(output));
    assert.equal(
      code,
      0,
      "protected terminal recovery failed: " + scrub(output),
    );
    assert.ok(
      entered && confirmed && output.includes("all app sessions revoked"),
    );
  } finally {
    clearTimeout(timer);
    child.stdin.destroy();
    if (child.exitCode === null) child.kill("SIGKILL");
  }
}

const browser = await chromium.launch({ headless: true });
const contexts = [],
  external = [],
  errors = [];
async function context(healthOverride) {
  const ctx = await browser.newContext({
    locale: "en-US",
    colorScheme: "dark",
    viewport: { width: 1280, height: 850 },
  });
  contexts.push(ctx);
  await ctx.route("**/*", async (route) => {
    const request = route.request();
    if (new URL(request.url()).origin !== config.url) {
      external.push(request.url());
      await route.abort();
      return;
    }
    if (
      healthOverride &&
      new URL(request.url()).pathname === "/api/v1/health"
    ) {
      const health = JSON.parse(JSON.stringify(config.health));
      healthOverride(health.connection);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(health),
      });
      return;
    }
    await route.continue();
  });
  const page = await ctx.newPage();
  page.setDefaultTimeout(8000);
  page.on("pageerror", (error) => errors.push(error.message));
  return { ctx, page };
}
async function api(page, path, method = "GET", body) {
  return page.evaluate(
    async ({ path, method, body }) => {
      const token = sessionStorage.getItem("fased.core.session.v1"),
        headers = {};
      if (token) headers.Authorization = `Bearer ${token}`;
      if (method !== "GET") {
        const state = await (
          await fetch("/api/v1/auth/state", { headers })
        ).json();
        headers["Content-Type"] = "application/json";
        headers["X-CSRF-Token"] = state.csrfToken;
      }
      const response = await fetch("/api/v1/" + path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      return { status: response.status, body: await response.json() };
    },
    { path, method, body },
  );
}
async function signIn(page, value) {
  await page.getByLabel("Owner password", { exact: true }).fill(value);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
}
const result = {
  result: "FAIL",
  fixtureOnly: true,
  realAI: false,
  realWallets: false,
  publicActivation: false,
  ownerBrowserChanged: false,
  checks: {},
};
try {
  await mkdir(root, { recursive: true });
  // Controlled compatibility inputs, without changing the running installation.
  for (const [name, mutate] of [
    [
      "string",
      (v) => {
        v.ownerUid = "1000";
      },
    ],
    [
      "fraction",
      (v) => {
        v.ownerUid = 1.5;
      },
    ],
    [
      "negative",
      (v) => {
        v.ownerUid = -1;
      },
    ],
    [
      "overflow",
      (v) => {
        v.ownerUid = 4294967295;
      },
    ],
    [
      "local-positive",
      (v) => {
        v.location = "local";
        v.ownerUid = 1000;
      },
    ],
  ]) {
    const { ctx, page } = await context(mutate);
    let auth = 0;
    page.on("request", (r) => {
      if (r.url().endsWith("/api/v1/auth/state")) auth++;
    });
    await page.goto(config.url);
    await expect(
      page.getByRole("heading", {
        name: "Connection unavailable",
        exact: true,
      }),
    ).toBeVisible();
    assert.equal(auth, 0, name + " metadata reached authentication");
    await ctx.close();
  }
  result.checks.invalidMetadataRejectedBeforeAuthentication = true;
  const { page } = await context();
  await page.goto(config.url);
  await expect(
    page.getByRole("heading", { name: "Claim this workspace", exact: true }),
  ).toBeVisible();
  await expect(page.getByLabel("One-use claim code")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Claim workspace", exact: true }),
  ).toBeDisabled();
  await page.setViewportSize({ width: 390, height: 844 });
  assert.ok(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
    "setup command overflowed mobile viewport",
  );
  await page.screenshot({ path: join(root, "setup-mobile.png") });
  await page.setViewportSize({ width: 1280, height: 850 });
  claimCode = await cli("claim-code");
  assert.match(claimCode, /^[A-Za-z0-9_-]+$/);
  await openOwnerSetup(page, claimCode);
  await page.getByLabel("Create owner password").fill(password);
  await page
    .getByRole("button", { name: "Claim workspace", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  result.checks.realManagedClaim = true;
  for (const [name, mutate] of [
    [
      "missing",
      (v) => {
        delete v.ownerUid;
      },
    ],
    [
      "zero",
      (v) => {
        v.ownerUid = 0;
      },
    ],
  ]) {
    const { ctx, page: legacy } = await context(mutate);
    await legacy.goto(config.url);
    await expect(
      legacy.getByRole("heading", { name: "Sign in", exact: true }),
    ).toBeVisible();
    await legacy.getByText("Recovery", { exact: true }).click();
    await expect(
      legacy.getByText("Use this installation’s recovery commands.", {
        exact: true,
      }),
    ).toBeVisible();
    assert.equal(
      await legacy
        .locator("details")
        .filter({ has: legacy.getByText("Recovery", { exact: true }) })
        .locator("code")
        .count(),
      0,
      name + " instance suggested unbound recovery commands",
    );
    await ctx.close();
  }
  result.checks.legacyRecoveryDoesNotTargetDefaultInstance = true;
  let setup = await api(page, "setup");
  assert.equal(setup.status, 200);
  const changed = {
    ...setup.body,
    settings: {
      ...setup.body.settings,
      host: setup.body.settings.host === "local" ? "vps" : "local",
    },
  };
  setup = await api(page, "setup", "PUT", changed);
  assert.equal(setup.status, 200);
  const before = await api(page, "health");
  assert.equal(before.body.connection.ownerUid, 1000);
  assert.equal(before.body.connection.location, "remote");
  result.checks.preferenceCannotChangeManagedIdentity = true;
  const { page: peer } = await context();
  await peer.goto(config.url);
  await signIn(peer, password);
  await expect(
    peer.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  staleToken = await peer.evaluate(() =>
    sessionStorage.getItem("fased.core.session.v1"),
  );
  assert.ok(staleToken);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  await page.getByText("Recovery", { exact: true }).click();
  for (const action of ["stop", "recover", "start"])
    await expect(
      page.locator("code").filter({ hasText: `${config.core} vps ${action}` }),
    ).toBeVisible();
  await page.screenshot({ path: join(root, "recovery-guidance.png") });
  await signIn(page, password);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  result.checks.signOutAndSignIn = true;
  await cli("stop");
  await recover();
  await cli("start");
  const stale = await peer.request.get(config.url + "/api/v1/setup", {
    headers: { Authorization: "Bearer " + staleToken },
  });
  assert.equal(stale.status(), 401);
  await page.reload();
  await peer.reload();
  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  await expect(
    peer.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  await signIn(page, password);
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Sign in", exact: true }),
  ).toBeVisible();
  await signIn(page, replacement);
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  const after = await api(page, "health"),
    retained = await api(page, "setup");
  assert.equal(after.body.instanceId, before.body.instanceId);
  assert.deepEqual(retained.body, setup.body);
  for (const capability of [
    "aiConnections",
    "walletSigner",
    "financialExecution",
    "updater",
  ])
    assert.equal(after.body.capabilities[capability], false);
  assert.equal(after.body.connection.ownerUid, 1000);
  result.checks.protectedTerminalRecovery = true;
  result.checks.sessionsRevoked = true;
  result.checks.oldPasswordRejected = true;
  result.checks.identityAndSavedSettingsPreserved = true;
  result.checks.liveFeaturesDisabled = true;
  assert.deepEqual(external, []);
  assert.deepEqual(errors, []);
  result.result = "PASS";
} catch (error) {
  result.error = scrub(error.stack ?? error);
  throw error;
} finally {
  await writeFile(
    join(root, "browser-result.json"),
    JSON.stringify(result, null, 2) + "\n",
  );
  for (const ctx of contexts) await ctx.close().catch(() => {});
  await browser.close();
}
