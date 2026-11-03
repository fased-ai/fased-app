// Actual packaged UI through the disposable guest's SSH-forwarded listener.
import assert from "node:assert/strict";
import { readFile, writeFile, chmod } from "node:fs/promises";
import { existsSync } from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";
import { pathToFileURL } from "node:url";

const password = "disposable-staged-owner-password";
const sessionKey = "fased.core.session.v1";
const retainedKeys = [
  "profiles",
  "sessions",
  "messages",
  "memories",
  "tasks",
  "runs",
];
export function updateMode(state) {
  assert.ok(
    state?.policy &&
      typeof state.policy === "object" &&
      !Array.isArray(state.policy),
  );
  const mode = state.policy.mode;
  assert.ok([undefined, "manual", "automatic"].includes(mode));
  // The installed API omits the default Manual mode, as does its UI parser.
  return mode ?? "manual";
}
export function retainedWorkspace(workspace) {
  const retained = {};
  for (const key of retainedKeys) {
    assert.ok(Array.isArray(workspace[key]), `retained ${key} array required`);
    retained[key] = workspace[key];
  }
  assert.ok(retained.profiles.length > 0);
  return createHash("sha256").update(JSON.stringify(retained)).digest("hex");
}

export function completedApplication(state, version, automatic) {
  const operation = state?.operation,
    release = operation?.release,
    receipt = operation?.receipt;
  return (
    state?.installedVersion === version &&
    operation?.phase === "completed" &&
    !!operation.id &&
    (operation.automatic === true) === automatic &&
    release?.version === version &&
    receipt?.phase === "completed" &&
    receipt.id === operation.id &&
    receipt.manifestSha256 === release.manifestSha256 &&
    receipt.engineSha256 === release.engineSha256 &&
    receipt.sequence === release.sequence &&
    /^[a-f0-9]{64}$/.test(receipt.manifestSha256 ?? "") &&
    /^[a-f0-9]{64}$/.test(receipt.engineSha256 ?? "")
  );
}

// Seed only the disposable, explicitly claimed instance through its ordinary API.
// Keep the reminder paused so a preservation comparison cannot race its scheduler.
export async function seedWorkspace(request) {
  let view = await request("agents");
  retainedWorkspace(view);
  const profileId = view.profileId;
  assert.ok(view.profiles.some((profile) => profile.id === profileId));
  const change = async (command) => {
    view = await request("agents", "POST", {
      profileId,
      revision: view.revision,
      ...command,
    });
    return view;
  };
  const sessions = new Set(view.sessions.map((value) => value.id));
  await change({
    action: "session-create",
    name: "Staged preservation conversation",
  });
  const session = view.sessions.find((value) => !sessions.has(value.id));
  assert.ok(session);
  await change({
    action: "message-add",
    id: session.id,
    text: "Preserve this harmless staged acceptance message.",
  });
  assert.ok(
    view.messages.some(
      (value) =>
        value.sessionId === session.id &&
        value.role === "user" &&
        value.text === "Preserve this harmless staged acceptance message.",
    ),
  );
  const tasks = new Set(view.tasks.map((value) => value.id));
  await change({
    action: "task-create",
    name: "Staged preservation reminder",
    text: "Review this disposable preservation note.",
    intervalSeconds: 604800,
    maxRuns: 1,
  });
  const task = view.tasks.find((value) => !tasks.has(value.id));
  assert.ok(task);
  await change({ action: "task-pause", id: task.id });
  assert.equal(
    view.tasks.find((value) => value.id === task.id)?.state,
    "paused",
  );
  const memories = new Set(view.memories.map((value) => value.id));
  await change({
    action: "memory-put",
    text: "Staged acceptance preserves this harmless owner note.",
    source: "disposable-staged-package-acceptance",
    expiresAt: 0,
  });
  const memory = view.memories.find((value) => !memories.has(value.id));
  assert.ok(memory);
  return {
    profileId,
    sessionId: session.id,
    taskId: task.id,
    memoryId: memory.id,
  };
}

export async function browserRequest(page, route, method = "GET", body) {
  return page.evaluate(
    async ({ route, method, body, sessionKey }) => {
      const token = sessionStorage.getItem(sessionKey),
        headers = {};
      if (token) headers.Authorization = `Bearer ${token}`;
      if (method !== "GET") {
        const state = await (
          await fetch("/api/v1/auth/state", {
            headers,
            credentials: "same-origin",
          })
        ).json();
        assertCSRF(state);
        headers["Content-Type"] = "application/json";
        headers["X-CSRF-Token"] = state.csrfToken;
      }
      function assertCSRF(state) {
        if (!state.authenticated || !state.csrfToken)
          throw Error("authenticated staged mutation required");
      }
      const response = await fetch("/api/v1/" + route, {
        method,
        headers,
        credentials: "same-origin",
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!response.ok)
        throw Error(`staged request failed: HTTP ${response.status}`);
      return response.json();
    },
    { route, method, body, sessionKey },
  );
}

export function browserAddress(config) {
  assert.equal(config.fixtureOnly, true);
  assert.ok(
    [
      "claim",
      "check",
      "preserve",
      "ui-update",
      "automatic-update",
      "reconnect",
      "auth-cycle",
    ].includes(config.action),
  );
  const address = new URL(config.url);
  assert.equal(address.protocol, "http:");
  assert.equal(address.hostname, "127.0.0.1");
  assert.equal(address.username + address.password, "");
  assert.ok(["17481", "17482", "17483"].includes(address.port));
  if (address.port === "17483")
    assert.ok(
      ["claim", "preserve"].includes(config.action),
      "legacy browser only claims and preserves the public bridge",
    );
  assert.match(config.version, /^\d+\.\d+\.\d+$/);
  const applying = ["ui-update", "automatic-update"].includes(config.action);
  if (applying) {
    assert.match(config.fromVersion, /^\d+\.\d+\.\d+$/);
    assert.notEqual(config.fromVersion, config.version);
  }
  return address;
}

export async function runStagedBrowser(config, { chromium, expect }) {
  assert.equal(process.env.FASED_DISPOSABLE_VM_ACCEPTANCE, "1");
  assert.ok(process.getuid() > 0);
  const address = browserAddress(config);
  const applying = ["ui-update", "automatic-update"].includes(config.action);
  if (config.action === "claim")
    assert.ok(
      !existsSync(config.baselineFile),
      "preserve the existing baseline",
    );
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.FASED_TEST_BROWSER
      ? { executablePath: process.env.FASED_TEST_BROWSER }
      : {}),
  });
  const context = await browser.newContext({
    locale: "en-US",
    ...(existsSync(config.storageFile)
      ? { storageState: config.storageFile }
      : {}),
  });
  const sessionFile =
    config.sessionFile ?? config.storageFile + ".session.json";
  if (existsSync(sessionFile)) {
    const token = JSON.parse(await readFile(sessionFile, "utf8")).token;
    assert.equal(typeof token, "string");
    await context.addInitScript(
      ({ origin, key, token }) => {
        if (location.origin === origin) sessionStorage.setItem(key, token);
      },
      { origin: address.origin, key: sessionKey, token },
    );
  }
  const external = [];
  await context.route("**/*", async (route) => {
    if (new URL(route.request().url()).origin !== address.origin) {
      external.push(new URL(route.request().url()).origin);
      await route.abort();
    } else await route.continue();
  });
  const page = await context.newPage();
  const request = (route, method, body) =>
    browserRequest(page, route, method, body);
  const waitAuthenticated = async () =>
    expect
      .poll(
        async () => {
          try {
            return (await request("auth/state")).authenticated;
          } catch {
            return false;
          }
        },
        { timeout: 120000 },
      )
      .toBe(true);
  const checkHealth = async (version) => {
    const health = await request("health");
    assert.equal(health.status, "ok");
    assert.equal(health.version, version);
    for (const key of ["aiConnections", "financialExecution", "walletSigner"])
      assert.equal(health.capabilities[key], false);
    if (config.instanceId) assert.equal(health.instanceId, config.instanceId);
    return health;
  };
  let baseline,
    seed,
    uiOutcome,
    automaticOutcome,
    automaticOperation,
    appliedManifestSha256;
  try {
    await page.goto(config.url);
    if (config.action === "claim") {
      await page
        .getByLabel("Create owner password", { exact: true })
        .fill(password);
      await page
        .getByRole("button", { name: "Claim workspace", exact: true })
        .click();
    }
    await waitAuthenticated();
    const before = await checkHealth(
      applying ? config.fromVersion : config.version,
    );
    if (config.action === "claim") {
      seed = await seedWorkspace(request);
      baseline = {
        instanceId: before.instanceId,
        workspaceSha256: retainedWorkspace(await request("agents")),
        sourceVersion: before.version,
        seed,
      };
      await writeFile(config.baselineFile, JSON.stringify(baseline) + "\n", {
        mode: 0o600,
        flag: "wx",
      });
    } else {
      baseline = JSON.parse(await readFile(config.baselineFile, "utf8"));
      assert.equal(before.instanceId, baseline.instanceId);
      assert.equal(
        retainedWorkspace(await request("agents")),
        baseline.workspaceSha256,
      );
    }
    if (config.action === "auth-cycle") {
      await page.getByRole("button", { name: "Sign out", exact: true }).click();
      await expect(
        page.getByRole("heading", { name: "Sign in", exact: true }),
      ).toBeVisible();
      assert.equal((await request("auth/state")).authenticated, false);
      await page.getByLabel("Owner password", { exact: true }).fill(password);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
      await waitAuthenticated();
    }
    if (applying || config.action === "check") {
      await page.goto(address.origin + "/#settings");
      const updates = page.getByRole("region", {
        name: "Updates",
        exact: true,
      });
      if (config.action === "automatic-update") {
        const original = await request("updates/application");
        assert.equal(updateMode(original), "manual");
        assert.equal(original.activationEnabled, false);
        await updates
          .getByLabel("Update mode", { exact: true })
          .selectOption("automatic");
        await expect
          .poll(
            async () => {
              try {
                const state = await request("updates/application");
                return (
                  completedApplication(state, config.version, true) &&
                  updateMode(state) === "automatic" &&
                  state.activationEnabled === true
                );
              } catch {
                return false;
              }
            },
            { timeout: 180000 },
          )
          .toBe(true);
        const state = await request("updates/application");
        assert.ok(completedApplication(state, config.version, true));
        automaticOperation = {
          id: state.operation.id,
          automatic: true,
          receipt: state.operation.receipt,
        };
        appliedManifestSha256 = state.operation.receipt.manifestSha256;
        await page.reload();
        await waitAuthenticated();
        await updates
          .getByLabel("Update mode", { exact: true })
          .selectOption("manual");
        await expect
          .poll(async () => {
            const restored = await request("updates/application");
            return (
              updateMode(restored) === "manual" &&
              restored.activationEnabled === false
            );
          })
          .toBe(true);
        automaticOutcome = "applied";
      } else if (config.action === "ui-update") {
        await updates
          .getByRole("button", { name: "Check", exact: true })
          .click();
        await expect
          .poll(
            async () => (await request("updates/application")).operation.phase,
            { timeout: 120000 },
          )
          .toBe("available");
        const available = await request("updates/application");
        assert.equal(available.installedVersion, config.fromVersion);
        assert.equal(available.operation.release.version, config.version);
        assert.equal(updateMode(available), "manual");
        appliedManifestSha256 = available.operation.release.manifestSha256;
        assert.match(appliedManifestSha256, /^[a-f0-9]{64}$/);
        await updates
          .getByRole("button", { name: "Update", exact: true })
          .click();
        const review = updates.getByRole("region", {
          name: "Review update",
          exact: true,
        });
        const applied = page.waitForRequest(
          (r) =>
            new URL(r.url()).pathname === "/api/v1/updates/application" &&
            r.method() === "POST" &&
            r.postDataJSON()?.action === "apply",
        );
        await review
          .getByRole("button", { name: "Update now", exact: true })
          .click();
        assert.equal(
          (await applied).postDataJSON().manifestSha256,
          appliedManifestSha256,
        );
        await expect
          .poll(
            async () => {
              try {
                const health = await request("health"),
                  state = await request("updates/application");
                return (
                  health.version === config.version &&
                  completedApplication(state, config.version, false)
                );
              } catch {
                return false;
              }
            },
            { timeout: 180000 },
          )
          .toBe(true);
        await page.reload();
        await waitAuthenticated();
        uiOutcome = "applied";
      } else {
        await updates
          .getByRole("button", { name: "Check", exact: true })
          .click();
        await expect
          .poll(
            async () => (await request("updates/application")).operation.phase,
            { timeout: 120000 },
          )
          .toBe("current");
        await expect(updates.getByRole("status")).toHaveText("Up to date");
        uiOutcome = "alreadyCurrent";
      }
      const state = await request("updates/application");
      assert.equal(updateMode(state), "manual");
      assert.equal(state.activationEnabled, false);
      assert.equal(state.installedVersion, config.version);
    }
    const health = await checkHealth(config.version);
    assert.equal(health.instanceId, baseline.instanceId);
    const workspaceSha256 = retainedWorkspace(await request("agents"));
    assert.equal(workspaceSha256, baseline.workspaceSha256);
    assert.deepEqual(external, []);
    await context.storageState({ path: config.storageFile });
    await chmod(config.storageFile, 0o600);
    const token = await page.evaluate(
      (key) => sessionStorage.getItem(key) ?? "",
      sessionKey,
    );
    await writeFile(sessionFile, JSON.stringify({ token }) + "\n", {
      mode: 0o600,
    });
    await chmod(sessionFile, 0o600);
    return {
      result: "PASS",
      action: config.action,
      instanceId: health.instanceId,
      version: health.version,
      workspaceSha256,
      ownerAuth: true,
      preservation: config.action !== "claim",
      ...(seed ? { seeded: seed } : {}),
      ...(uiOutcome ? { uiOutcome } : {}),
      ...(appliedManifestSha256
        ? { appliedManifestSha256, fromVersion: config.fromVersion }
        : {}),
      ...(config.action === "ui-update" ? { ordinaryUpdateApply: "PASS" } : {}),
      ...(automaticOutcome
        ? {
            automaticOutcome,
            automaticOperation,
            policyRestored: "manual",
            customerActivation: false,
          }
        : {}),
      ...(config.action === "auth-cycle" ? { logoutSignIn: true } : {}),
      ...(config.action === "reconnect" ? { reconnected: true } : {}),
    };
  } finally {
    await context.close();
    await browser.close();
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  const config = JSON.parse(await readFile(process.argv[2], "utf8"));
  const result = await runStagedBrowser(
    config,
    await import("@playwright/test"),
  );
  console.log(JSON.stringify(result));
}
