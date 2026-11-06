// Actual packaged UI through the disposable guest's SSH-forwarded listener.
import assert from "node:assert/strict";
import {
  acceptanceBinding,
  validateInstalledAgent,
  stagedAcceptanceContract,
} from "./release-acceptance.mjs";
import { readFile, writeFile, chmod } from "node:fs/promises";
import { existsSync } from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";
import { pathToFileURL } from "node:url";

// The producer consumes actual runner observations, never an all-PASS template.
export function installedAgentReceipt({
  packet,
  inventory,
  localReceiptSha256,
  bundleManifest,
  observed,
  observedRaw,
}) {
  const hash = (s) => createHash("sha256").update(s).digest("hex");
  const binding = acceptanceBinding(packet, inventory);
  assert.equal(
    typeof observedRaw,
    "string",
    "raw retained observations required",
  );
  assert.deepEqual(
    JSON.parse(observedRaw),
    observed,
    "raw observations differ",
  );
  assert.ok(
    observed && Object.entries(binding).every(([k, v]) => observed[k] === v),
    "installed observation belongs to another package",
  );
  for (const key of [
    "installerOwnedProviderMode",
    "sandboxed",
    "serviceRestarted",
    "rebooted",
    "revoked",
  ])
    assert.equal(observed[key], true, `observed ${key} required`);
  for (const key of ["testCodeOverlay", "realProviderCalls"])
    assert.equal(observed[key], false);
  assert.equal(observed.result, "PASS");
  assert.ok(
    typeof observed.instanceId === "string" && observed.instanceId.length > 16,
  );
  assert.equal(
    observed.profile,
    "local",
    "this producer supports the Local installed agent only",
  );
  const restart = observed.serviceRestart;
  assert.ok(
    restart &&
      /^[1-9][0-9]*$/.test(restart.beforePID) &&
      /^[1-9][0-9]*$/.test(restart.afterPID) &&
      restart.beforePID !== restart.afterPID,
    "actual changed service PID required",
  );
  const boot = observed.kernelReboot;
  const bootID =
    /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/;
  assert.ok(
    boot &&
      bootID.test(boot.before) &&
      bootID.test(boot.after) &&
      boot.before !== boot.after,
    "actual changed kernel boot required",
  );
  const receipt = {
    ...binding,
    localReceiptSha256,
    mode: "installed-provider",
    profile: observed.profile,
    result: "PASS",
    fixtureOnly: true,
    sandboxed: true,
    shippingArtifactUnmodified: true,
    testCodeOverlay: false,
    realProviderCalls: false,
    mining: false,
    liveFeatures: false,
    automaticUpdates: false,
    engineSha256: observed.engineSha256,
    bundleSha256: packet.bundleSha256,
    bundleManifest,
    instanceIdentitySha256: hash(observed.instanceId),
    evidenceSha256: hash(observedRaw),
    checks: Object.fromEntries(
      stagedAcceptanceContract.installedAgent.map((k) => [
        k,
        observed.checks?.[k],
      ]),
    ),
  };
  validateInstalledAgent(receipt, packet, inventory, localReceiptSha256);
  return receipt;
}

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

export function applicationUpdateFailure(state) {
  const operation = state?.operation;
  if (["check-failed", "recovery", "rolled-back"].includes(operation?.phase)) {
    return new Error(
      `Update ${operation.id ?? "unknown"} ${operation.phase}: ${operation.failure || "preserve the transaction"}`,
    );
  }
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

export async function installedNavigate(page, hash) {
  assert.match(hash, /^#(?:providers|agents\/(?:settings|chat|tasks|memory))$/);
  await page.evaluate((hash) => {
    window.location.hash = hash;
  }, hash);
}

export async function installedAccount(request, ref) {
  assert.ok(
    typeof ref === "string" && ref.length > 0,
    "exact installed account required",
  );
  const view = await request("ai/connection");
  const matches = view.connections?.filter((c) => c.ref === ref) ?? [];
  assert.equal(matches.length, 1, "exact unique installed account required");
  return matches[0];
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
      "failed-check",
      "agent-prepare",
      "agent-run",
      "agent-reboot",
      "agent-finish",
    ].includes(config.action),
  );
  if (config.action.startsWith("agent-")) {
    assert.equal(config.installedProviderProfile, true);
    assert.notEqual(
      config.transport,
      "tailscale-https",
      "Local producer cannot operate on Remote owner state",
    );
  }
  const address = new URL(config.url);
  assert.equal(address.username + address.password, "");
  if (config.transport === "tailscale-https") {
    const origin = new URL(config.expectedOrigin);
    assert.equal(origin.protocol, "https:");
    assert.match(origin.hostname, /^[a-z0-9-]+\.[a-z0-9-]+\.ts\.net$/);
    assert.equal(origin.href, origin.origin + "/");
    assert.equal(origin.username + origin.password + origin.port, "");
    assert.equal(address.origin, origin.origin);
    assert.equal(address.pathname, "/");
    assert.match(config.instanceId, /^[A-Za-z0-9_-]{43}$/);
    assert.deepEqual(
      [...address.searchParams],
      [["instance", config.instanceId]],
    );
    assert.equal(
      config.sessionFile,
      undefined,
      "Remote cannot import a CLI/owner session",
    );
    if (config.action === "claim")
      assert.match(address.hash, /^#setup=[A-Za-z0-9_-]{43}$/);
    else assert.equal(address.hash, "");
  } else {
    assert.ok(
      config.transport === undefined || config.transport === "loopback",
    );
    assert.equal(address.protocol, "http:");
    assert.equal(address.hostname, "127.0.0.1");
    assert.ok(
      ["17481", "17482", "17483", "17484", "17485"].includes(address.port),
    );
    if (address.port === "17485") {
      assert.equal(
        config.installedProviderProfile,
        true,
        "dedicated installed provider profile required",
      );
      assert.ok(
        config.action === "claim" || config.action.startsWith("agent-"),
      );
    } else
      assert.ok(
        !config.action.startsWith("agent-"),
        "agent actions require their isolated installed profile",
      );
    if (address.port === "17484")
      assert.ok(
        ["claim", "check", "preserve", "reconnect"].includes(config.action),
      );
    if (address.port === "17483")
      assert.ok(
        ["claim", "preserve"].includes(config.action),
        "legacy browser only claims and preserves the public bridge",
      );
  }
  assert.match(config.version, /^\d+\.\d+\.\d+$/);
  if (config.ownerAuthMode !== undefined) {
    assert.equal(config.ownerAuthMode, "existing-password-account-preserved");
    assert.equal(config.transport, "tailscale-https");
    assert.ok(["auth-cycle", "automatic-update"].includes(config.action));
    assert.equal(config.predecessor?.version, "0.1.6");
    assert.equal(config.predecessor.ownerAuthMode, "password-v1");
    assert.equal(
      config.predecessor.source,
      "738bd835b8c86c7cf7b3abfb2945240635f51fe7",
    );
    assert.equal(
      config.predecessor.artifactSetSha256,
      "aea00a0d9e1b7ed05a1346428cdf8cc277cbe40f93cfcd86d40fc9b45c559460",
    );
  }
  if (config.legacyRemotePasswordSetup !== undefined) {
    // Only the immutable public predecessor needs an initial test password.
    // Routine Update and current Remote setup must stay password-free.
    assert.equal(config.legacyRemotePasswordSetup, true);
    assert.equal(config.transport, "tailscale-https");
    assert.ok(config.action === "claim" || config.action.startsWith("agent-"));
    assert.equal(config.version, "0.1.6");
  }
  const applying = ["ui-update", "automatic-update"].includes(config.action);
  if (applying) {
    assert.match(config.fromVersion, /^\d+\.\d+\.\d+$/);
    assert.notEqual(config.fromVersion, config.version);
  }
  return address;
}

export function privatePreflightAddress(config) {
  assert.equal(config.fixtureOnly, true);
  assert.equal(config.action, "private-preflight");
  const origin = new URL(config.expectedOrigin),
    address = new URL(config.url);
  assert.match(origin.hostname, /^[a-z0-9-]+\.[a-z0-9-]+\.ts\.net$/);
  assert.equal(origin.protocol, "https:");
  assert.equal(origin.username + origin.password + origin.port, "");
  assert.equal(origin.href, origin.origin + "/");
  assert.equal(address.origin, origin.origin);
  assert.equal(address.username + address.password, "");
  assert.equal(address.pathname, "/");
  assert.equal(address.search, "");
  assert.match(address.hash, /^#[A-Za-z0-9_-]{43}$/);
  return address;
}

export async function runPrivatePreflight(config, { chromium, expect }) {
  assert.equal(process.env.FASED_DISPOSABLE_VM_ACCEPTANCE, "1");
  assert.ok(process.getuid() > 0);
  const address = privatePreflightAddress(config);
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.FASED_TEST_BROWSER
      ? { executablePath: process.env.FASED_TEST_BROWSER }
      : {}),
  });
  try {
    // Fresh context: no owner cookie, session injection or saved credentials.
    const context = await browser.newContext({ locale: "en-US" });
    const external = [];
    await context.route("**/*", async (route) => {
      if (new URL(route.request().url()).origin !== address.origin) {
        external.push(true);
        return route.abort();
      }
      return route.continue();
    });
    const page = await context.newPage();
    const approved = page.waitForResponse(
      (response) =>
        response.url() === address.origin + "/approve" &&
        response.request().method() === "POST",
    );
    await page.goto(address.href);
    assert.equal((await approved).status(), 204);
    await expect(page.locator("#status")).toHaveText(
      "Connected. Continue in your installation terminal.",
    );
    assert.equal(page.url(), address.origin + "/");
    assert.deepEqual(external, []);
    await context.close();
    return {
      result: "PASS",
      action: config.action,
      privateBrowserApproved: true,
    };
  } finally {
    await browser.close();
  }
}

export async function restoreFixtureManualPolicy(request) {
  const before = await request("updates/application");
  if (updateMode(before) === "automatic")
    await request("updates/preferences", "PUT", {
      revision: before.policyRevision,
      action: "policy",
      policy: { ...before.policy, mode: "manual" },
    });
  const after = await request("updates/application");
  assert.equal(updateMode(after), "manual");
  assert.equal(after.activationEnabled, false);
}

export function detachedFixtureObserver(
  origin,
  { cookieRequest, token, fetchImpl = fetch },
) {
  if (!cookieRequest) assert.match(token, /^[A-Za-z0-9_-]{43}$/);
  return async (route, method = "GET", body) => {
    assert.equal(method, "GET");
    assert.equal(body, undefined);
    assert.ok(
      ["updates/application", "health", "auth/state", "agents"].includes(route),
    );
    const url = origin + "/api/v1/" + route;
    const response = cookieRequest
      ? await cookieRequest.get(url, { maxRedirects: 0, timeout: 10000 })
      : await fetchImpl(url, {
          headers: { Authorization: "Bearer " + token },
          redirect: "error",
          signal: AbortSignal.timeout(10000),
        });
    if (!(cookieRequest ? response.ok() : response.ok))
      throw Error(
        "Read-only fixture observer HTTP " +
          (cookieRequest ? response.status() : response.status),
      );
    return response.json();
  };
}

// Runs against the installed service using its ordinary authenticated API and UI.
// Private phase state is retained beside the existing browser session; it is not
// an acceptance receipt. Only the final observed result may become one.
export async function installedTaskSchedule(page, editor) {
  const schedule = editor.locator("details").filter({
    has: page.locator("summary", { hasText: /^Schedule$/ }),
  });
  await schedule.locator(":scope > summary").click();
  await schedule.getByLabel("Schedule", { exact: true }).selectOption("repeat");
  await schedule.getByLabel("Maximum evaluations", { exact: true }).fill("2");
  // The wrapped select's label includes its option text (UnitMinutesHoursDays).
  await schedule.getByLabel(/^Unit/).selectOption("60");
  await schedule.getByLabel("Every", { exact: true }).fill("20");
}

export async function stopInstalledReply(page, latest) {
  const before = await latest();
  assert.ok(before?.id, "exact reply required before Stop");
  if (before.state === "completed") return;
  try {
    await page.getByRole("button", { name: "Stop", exact: true }).click({
      timeout: 1000,
    });
  } catch (error) {
    const after = await latest();
    // Completion can remove Stop between reading state and clicking. Only
    // that same completed reply justifies accepting an unavailable control.
    if (after?.id !== before.id || after.state !== "completed") throw error;
  }
}

export async function runInstalledAgentPhase(
  config,
  { page, request, expect },
) {
  const stateFile = config.storageFile + ".agent.json";
  const phase = config.action;
  const state = existsSync(stateFile)
    ? JSON.parse(await readFile(stateFile, "utf8"))
    : { checks: {} };
  const health = await request("health");
  assert.equal(health.version, config.version);
  for (const key of ["financialExecution", "walletSigner"])
    assert.equal(health.capabilities[key], false);
  const view = () =>
    request(
      "agents?profile=" +
        encodeURIComponent(state.profileId ?? "trading-analyst"),
    );
  const change = async (command) => {
    const v = await view();
    return request("agents", "POST", {
      profileId: v.profileId,
      revision: v.revision,
      ...command,
    });
  };
  const account = () => installedAccount(request, state.ref);
  if (phase === "agent-prepare") {
    assert.equal(
      existsSync(stateFile),
      false,
      "do not duplicate an existing agent journey",
    );
    const passphrase = "disposable-installed-agent-vault";
    assert.equal(
      (await view()).sessions.some((s) => s.name === "Installed journey"),
      false,
      "reconcile a partial journey before dispatching again",
    );
    const prior =
      (await request("ai/connection")).connections?.filter(
        (c) => c.name === "Installed account",
      ) ?? [];
    assert.ok(prior.length <= 1, "ambiguous existing fixture account");
    let c = prior[0];
    if (!c) {
      await installedNavigate(page, "#providers");
      const providers = page.getByRole("region", {
        name: "Providers",
        exact: true,
      });
      await providers.locator('[data-provider="openai"] > button').click();
      await providers
        .getByLabel("Name", { exact: true })
        .fill("Installed account");
      await providers
        .getByLabel("API key", { exact: true })
        .fill("fased-test-installed-agent-key");
      await providers
        .getByLabel("Vault passphrase", { exact: true })
        .fill(passphrase);
      await providers
        .getByRole("button", { name: "Save", exact: true })
        .click();
      await expect
        .poll(async () => {
          const all = await request("ai/connection");
          c = all.connections?.find((c) => c.name === "Installed account");
          return !!c && !c.locked;
        })
        .toBe(true);
    }
    assert.equal(c.provider, "openai");
    assert.equal(c.locked, false);
    assert.equal(c.testOnly, true);
    assert.equal(c.liveCallsEnabled, false);
    state.ref = c.ref;
    state.profileId = "trading-analyst";
    c = await account();
    const model = c.admissionPolicy?.model;
    assert.ok(model);
    state.checks.providerConnection = "PASS";
    await installedNavigate(page, "#agents/settings");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption(state.profileId);
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
      (await view()).profiles.find((p) => p.id === state.profileId).name,
    );
    await page.getByLabel("Provider", { exact: true }).selectOption("openai");
    await page.getByLabel("Account", { exact: true }).selectOption(c.ref);
    await page.getByLabel("Model", { exact: true }).selectOption(model);
    await page.getByRole("button", { name: "Save agent", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await view()).profiles.find((p) => p.id === state.profileId)
            ?.connectionRef,
      )
      .toBe(c.ref);
    const assigned = (await view()).profiles.find(
      (p) => p.id === state.profileId,
    );
    assert.equal(assigned.model, model);
    state.checks.agentModel = "PASS";
    let v = await change({
      action: "session-create",
      name: "Installed journey",
    });
    const session = v.sessions.at(-1);
    assert.equal(session.name, "Installed journey");
    state.sessionId = session.id;
    await installedNavigate(page, "#agents/chat");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption(state.profileId);
    const conversations = page.getByRole("button", {
      name: "Conversations",
      exact: true,
    });
    if (
      (await conversations.isVisible()) &&
      (await conversations.getAttribute("aria-expanded")) !== "true"
    )
      await conversations.click();
    await page.getByRole("button", { name: "Installed journey" }).click();
    const send = async (text, allow = false) => {
      await page.getByLabel("Message", { exact: true }).fill(text);
      await page.getByRole("button", { name: "Send", exact: true }).click();
      const review = page.getByRole("dialog", {
        name: "Allow replies",
        exact: true,
      });
      if (allow) {
        await expect(review).toBeVisible();
        await review
          .getByRole("button", { name: "Allow and send", exact: true })
          .click();
      }
    };
    await send("Compare only these synthetic inputs: A=1 and B=2.", true);
    await expect
      .poll(
        async () =>
          (await view()).aiRequests
            .filter((q) => q.sessionId === session.id)
            .at(-1)?.state,
        { timeout: 30000 },
      )
      .toBe("completed");
    await expect(
      page.getByRole("button", { name: "Stop", exact: true }),
    ).toHaveCount(0);
    await expect(page.locator(".ai-answer").last()).toContainText("fixture");
    await send("Stop this synthetic comparison before completing it.");
    await stopInstalledReply(page, async () =>
      (await view()).aiRequests
        .filter((q) => q.sessionId === session.id)
        .at(-1),
    );
    await expect
      .poll(
        async () =>
          (await view()).aiRequests
            .filter((q) => q.sessionId === session.id)
            .at(-1)?.state,
        { timeout: 10000 },
      )
      .toMatch(/^(cancelled|completed)$/);
    const raced = (await view()).aiRequests
      .filter((q) => q.sessionId === session.id)
      .at(-1);
    if (raced.state === "completed") assert.match(raced.answer, /fixture/i);
    else assert.equal(raced.answer || "", "");
    // Fast compiled replies may win Stop. Also exercise a prepared reply where
    // no dispatch is possible, and require real cancellation with zero spend.
    const picker = page.getByRole("combobox", {
      name: "Provider",
      exact: true,
      includeHidden: true,
    });
    const modelMenu = picker.locator("xpath=ancestor::details").first();
    if ((await modelMenu.getAttribute("open")) === null)
      await modelMenu.locator(":scope > summary").click();
    await picker.selectOption("");
    const options = page.locator("details.message-options");
    if ((await options.getAttribute("open")) === null)
      await options.locator(":scope > summary").click();
    await page
      .getByRole("checkbox", { name: "Review each reply", exact: true })
      .check();
    await send("Cancel this prepared synthetic reply without dispatching it.");
    await page.getByRole("button", { name: "Stop", exact: true }).click();
    await expect
      .poll(
        async () =>
          (await view()).aiRequests
            .filter((q) => q.sessionId === session.id)
            .at(-1)?.state,
      )
      .toBe("cancelled");
    const prepared = (await view()).aiRequests
      .filter((q) => q.sessionId === session.id)
      .at(-1);
    assert.equal(prepared.connectionRef || "", "");
    assert.equal(prepared.spentMicrousd, 0);
    state.stopObservation = {
      transportTerminal: raced.state,
      preparedId: prepared.id,
      preparedState: prepared.state,
      preparedSpentMicrousd: prepared.spentMicrousd,
    };
    if ((await modelMenu.getAttribute("open")) === null)
      await modelMenu.locator(":scope > summary").click();
    await picker.selectOption(c.ref);
    state.checks.chatSendStop = "PASS";
    await installedNavigate(page, "#agents/tasks");
    await page.getByRole("button", { name: "New task", exact: true }).click();
    const editor = page.getByRole("complementary", {
      name: "Create a task",
      exact: true,
    });
    await editor.getByLabel("Type", { exact: true }).selectOption("analysis");
    await editor
      .getByLabel("Task name", { exact: true })
      .fill("Installed bounded task");
    await editor
      .getByLabel("Prompt", { exact: true })
      .fill("Compare only synthetic fixed inputs.");
    await installedTaskSchedule(page, editor);
    await editor
      .getByRole("button", { name: "Review task", exact: true })
      .click();
    const taskReview = page.getByRole("complementary", {
      name: "Review task",
      exact: true,
    });
    await expect(taskReview).toContainText("Installed bounded task");
    await taskReview
      .getByRole("button", { name: "Create task", exact: true })
      .click();
    await expect
      .poll(async () =>
        (await view()).tasks.some((t) => t.name === "Installed bounded task"),
      )
      .toBe(true);
    v = await view();
    const task = v.tasks.find((t) => t.name === "Installed bounded task");
    assert.ok(task?.ai);
    state.taskId = task.id;
    assert.equal(task.maxRuns, 2);
    // A scheduler must not consume the grant before the restart assertion.
    // Leave one finite request unspent for reboot/revocation verification.
    await page
      .getByRole("button", {
        name: "Pause: Installed bounded task",
        exact: true,
      })
      .click();
    await expect
      .poll(
        async () =>
          (await view()).tasks.find((t) => t.id === state.taskId)?.state,
      )
      .toBe("paused");
    assert.equal(
      (await view()).tasks.find((t) => t.id === state.taskId).runs,
      0,
    );
    state.checks.reviewedTask = "PASS";
    await installedNavigate(page, "#providers");
    const providerPage = page.getByRole("region", {
      name: "Providers",
      exact: true,
    });
    await providerPage.locator('[data-account="' + c.ref + '"]').click();
    const access = providerPage.locator("details").filter({
      has: page.locator("summary", { hasText: /^Background access$/ }),
    });
    await access.locator(":scope > summary").click();
    await access
      .getByRole("checkbox", { name: "Installed bounded task", exact: true })
      .check();
    await access
      .getByRole("checkbox", { name: /Continue after sign.?out/ })
      .check();
    await access
      .getByRole("checkbox", { name: "Continue after restart", exact: true })
      .check();
    await access
      .getByRole("button", { name: "Review access", exact: true })
      .click();
    await access
      .getByLabel("Vault passphrase", { exact: true })
      .fill(passphrase);
    await access
      .getByRole("button", { name: "Allow task access", exact: true })
      .click();
    await expect
      .poll(async () => {
        const access = (await account()).taskAccess;
        return (
          !!access?.continueAfterRestart &&
          access.tasks.some((t) => t.id === state.taskId)
        );
      })
      .toBe(true);
    const allowed = await account();
    assert.ok(allowed.taskAccess?.continueAfterRestart);
    assert.equal(allowed.taskAccess.maxRequests, 2);
    assert.equal(allowed.taskAccess.usedRequests, 0);
    state.grantId = allowed.taskAccess.id;
    state.grantExpiresAt = allowed.taskAccess.expiresAt;
    state.checks.protectedCredentials = "PASS";
    state.instanceId = health.instanceId;
    state.requestCount = (await view()).aiRequests.length;
  } else {
    assert.equal(
      health.instanceId,
      state.instanceId,
      "preserve the installed owner instance",
    );
    await installedNavigate(page, "#agents/settings");
    await page
      .getByLabel("Agent", { exact: true })
      .selectOption(state.profileId);
    await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
      (await view()).profiles.find((p) => p.id === state.profileId).name,
    );
    const c = await account();
    assert.equal(c.locked, true, "restart must not unlock Chat");
    if (phase === "agent-run") {
      assert.equal(c.taskAccess?.id, state.grantId);
      assert.equal(c.taskAccess.usedRequests, 0);
      assert.equal(c.taskAccess.maxRequests, 2);
      const waiting = (await view()).tasks.find((t) => t.id === state.taskId);
      assert.equal(waiting.state, "paused");
      assert.equal(waiting.runs, 0);
      await installedNavigate(page, "#agents/tasks");
      await page
        .getByRole("button", {
          name: "Resume: Installed bounded task",
          exact: true,
        })
        .click();
      await page
        .getByRole("button", {
          name: "Run now: Installed bounded task",
          exact: true,
        })
        .click();
      await expect
        .poll(
          async () =>
            (await view()).tasks.find((t) => t.id === state.taskId)?.runs,
          { timeout: 30000 },
        )
        .toBe(1);
      await page
        .getByRole("button", {
          name: "Pause: Installed bounded task",
          exact: true,
        })
        .click();
      await expect
        .poll(
          async () =>
            (await view()).tasks.find((t) => t.id === state.taskId)?.state,
        )
        .toBe("paused");
      const after = await account();
      assert.equal(after.taskAccess.usedRequests, 1);
      state.allocated = after.taskAccess.allocatedMicrousd;
      assert.ok(state.allocated > 0);
      const result = (await view()).aiRequests
        .filter((q) => q.taskId === state.taskId)
        .at(-1);
      assert.equal(result.state, "completed");
      state.resultId = result.id;
      await installedNavigate(page, "#agents/tasks");
      const withResult = await view();
      const run = withResult.runs.find((r) => r.taskId === state.taskId);
      assert.ok(run);
      await page
        .getByRole("navigation", { name: "Task views" })
        .getByRole("button", { name: "Results", exact: true })
        .click();
      const card = page.getByRole("article", {
        name: "Run " + run.id,
        exact: true,
      });
      await expect(card).toBeVisible();
      await card.getByRole("button", { name: "Discuss", exact: true }).click();
      const message = page
        .locator(".chat-message")
        .filter({ hasText: "Installed bounded task" })
        .last();
      await message
        .getByRole("button", { name: "Remember this", exact: true })
        .click();
      const memory = page.getByRole("dialog", { name: "Memory", exact: true });
      const note = "Retain only this synthetic preference.";
      await memory.getByLabel("Note", { exact: true }).fill(note);
      await memory
        .getByRole("button", { name: "Save memory", exact: true })
        .click();
      await expect
        .poll(async () => (await view()).memories.some((m) => m.text === note))
        .toBe(true);
      const remembered = (await view()).memories.find((m) => m.text === note);
      assert.ok(remembered);
      state.memoryId = remembered.id;
      await installedNavigate(page, "#agents/memory");
      await expect(page.getByText(note, { exact: true }).first()).toBeVisible();
      const other = await request("agents?profile=news-researcher");
      assert.ok(!other.memories.some((m) => m.id === state.memoryId));
      assert.ok(!other.tasks.some((t) => t.id === state.taskId));
      state.checks.memoryOptional = "PASS";
      state.checks.profileIsolation = "PASS";
      state.checks.taskResult = "PASS";
      state.serviceRestarted = true;
      state.requestCount = (await view()).aiRequests.length;
    } else if (phase === "agent-reboot") {
      const v = await view();
      assert.equal(v.aiRequests.length, state.requestCount);
      assert.equal(v.tasks.find((t) => t.id === state.taskId).runs, 1);
      if (c.taskAccess) {
        assert.equal(c.taskAccess.id, state.grantId);
        assert.equal(c.taskAccess.usedRequests, 1);
        assert.equal(c.taskAccess.allocatedMicrousd, state.allocated);
      } else {
        assert.ok(
          c.persistentTaskAccessAvailable === false ||
            Date.now() >= state.grantExpiresAt * 1000,
          "unexpired, unspent grant disappeared while Native access is available",
        );
      }
      // A desktop keyring may be locked after reboot: held is correct, not
      // unattended success. No reconnection/refill or duplicate dispatch.
      state.rebootCredentialState = c.taskAccess
        ? "retained-scoped"
        : Date.now() >= state.grantExpiresAt * 1000
          ? "retired-expired"
          : "held-native-unavailable";
      state.rebooted = true;
      state.checks.restartCredentialPolicy = "PASS";
      await request("ai/connection", "POST", {
        action: "task-access-revoke",
        ref: state.ref,
      });
      assert.equal((await account()).taskAccess, undefined);
      state.revoked = true;
    } else if (phase === "agent-finish") {
      assert.equal(
        c.taskAccess,
        undefined,
        "revocation survives real service restart",
      );
      const v = await view();
      assert.equal(v.aiRequests.length, state.requestCount);
      assert.ok(v.memories.some((m) => m.id === state.memoryId));
      assert.equal(v.tasks.find((t) => t.id === state.taskId).runs, 1);
      state.checks.revocation = "PASS";
      state.checks.preservation = "PASS";
      state.result = "PASS";
    } else throw Error("unknown installed-agent phase");
  }
  await writeFile(stateFile, JSON.stringify(state), { mode: 0o600 });
  await chmod(stateFile, 0o600);
  return { result: "PASS", ...state };
}

export async function runStagedBrowser(config, { chromium, expect }) {
  assert.equal(process.env.FASED_DISPOSABLE_VM_ACCEPTANCE, "1");
  assert.ok(process.getuid() > 0);
  const address = browserAddress(config);
  const directRemote = config.transport === "tailscale-https";
  const legacyRemotePasswordSetup = config.legacyRemotePasswordSetup === true;
  const preservedPasswordOwner =
    config.ownerAuthMode === "existing-password-account-preserved";
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
  if (!directRemote && existsSync(sessionFile)) {
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
  let page = await context.newPage();
  let detachedRequest;
  let automaticPolicyStarted = false;
  const request = (route, method, body) =>
    detachedRequest
      ? detachedRequest(route, method, body)
      : browserRequest(page, route, method, body);
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
      if (
        (directRemote && !legacyRemotePasswordSetup) ||
        new URL(config.url).hash.startsWith("#local=")
      ) {
        await waitAuthenticated();
        await expect(page.locator('input[type="password"]')).toHaveCount(0);
      } else {
        await page
          .getByLabel("Create owner password", { exact: true })
          .fill(password);
        await page
          .getByRole("button", { name: "Claim workspace", exact: true })
          .click();
      }
    }
    // Re-establish only the captured disposable account's initial UI sign-in.
    // Never supply a credential after Automatic has been enabled.
    if (
      preservedPasswordOwner &&
      !(await request("auth/state")).authenticated
    ) {
      await page.getByLabel("Owner password", { exact: true }).fill(password);
      await page.getByRole("button", { name: "Sign in", exact: true }).click();
    }
    await waitAuthenticated();
    if (config.action.startsWith("agent-")) {
      const observed = await runInstalledAgentPhase(config, {
        page,
        request,
        expect,
      });
      assert.deepEqual(
        external,
        [],
        "installed journey attempted an external browser request",
      );
      return observed;
    }
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
      if (config.reopenUrl) {
        const reopened = new URL(config.reopenUrl);
        assert.equal(reopened.origin, address.origin);
        assert.ok(reopened.hash.startsWith("#local="));
        await expect
          .poll(async () => (await request("auth/state")).authenticated)
          .toBe(false);
        await page.goto(reopened.href);
        await waitAuthenticated();
        await expect(page.locator('input[type="password"]')).toHaveCount(0);
      } else if (directRemote && !preservedPasswordOwner) {
        await expect
          .poll(async () => (await request("auth/state")).authenticated)
          .toBe(false);
        await expect(page.locator('input[type="password"]')).toHaveCount(0);
        await page
          .getByRole("button", { name: "Connect", exact: true })
          .click();
        await waitAuthenticated();
      } else {
        if (
          await page
            .getByRole("button", { name: "Use password", exact: true })
            .count()
        )
          await page
            .getByRole("button", { name: "Use password", exact: true })
            .click();
        await expect(
          page.getByRole("heading", { name: "Sign in", exact: true }),
        ).toBeVisible();
        assert.equal((await request("auth/state")).authenticated, false);
        await page.getByLabel("Owner password", { exact: true }).fill(password);
        await page
          .getByRole("button", { name: "Sign in", exact: true })
          .click();
        await waitAuthenticated();
      }
    }
    if (
      applying ||
      config.action === "check" ||
      config.action === "failed-check"
    ) {
      await page.goto(address.origin + "/#settings");
      let updates = page.getByRole("region", {
        name: "Updates",
        exact: true,
      });
      if (config.action === "automatic-update") {
        const original = await request("updates/application");
        assert.equal(updateMode(original), "manual");
        assert.equal(original.activationEnabled, false);
        automaticPolicyStarted = true;
        await updates
          .getByLabel("Update mode", { exact: true })
          .selectOption("automatic");
        if (config.closeForAutomatic) {
          await expect
            .poll(async () => {
              const state = await request("updates/application");
              return (
                updateMode(state) === "automatic" &&
                state.activationEnabled === true
              );
            })
            .toBe(true);
          assert.equal(
            completedApplication(
              await request("updates/application"),
              config.version,
              true,
            ),
            false,
            "detached completion must start before the target is complete",
          );
          const observer = directRemote
            ? detachedFixtureObserver(address.origin, {
                cookieRequest: context.request,
              })
            : detachedFixtureObserver(address.origin, {
                token: await page.evaluate(
                  (key) => sessionStorage.getItem(key),
                  sessionKey,
                ),
              });
          await page.close();
          assert.equal(context.pages().length, 0);
          // No tab/JavaScript client remains during completion. The observer
          // performs authenticated read-only checks; it cannot start an update.
          detachedRequest = observer;
        }
        await expect
          .poll(
            async () => {
              let state;
              try {
                state = await request("updates/application");
              } catch {
                return false;
              }
              const failure = applicationUpdateFailure(state);
              if (failure) throw failure;
              return (
                completedApplication(state, config.version, true) &&
                updateMode(state) === "automatic" &&
                state.activationEnabled === true
              );
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
        if (config.closeForAutomatic) {
          page = await context.newPage();
          detachedRequest = undefined;
          await page.goto(address.origin + "/#settings");
          updates = page.getByRole("region", { name: "Updates", exact: true });
        } else await page.reload();
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
        automaticPolicyStarted = false;
      } else if (config.action === "failed-check") {
        await updates
          .getByRole("button", { name: "Check", exact: true })
          .click();
        await expect
          .poll(
            async () => (await request("updates/application")).operation.phase,
            { timeout: 120000 },
          )
          .toBe("check-failed");
        assert.equal((await request("health")).version, config.version);
        uiOutcome = "acquisition-failed-preserved";
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
              let health, state;
              try {
                state = await request("updates/application");
              } catch {
                return false;
              }
              const failure = applicationUpdateFailure(state);
              if (failure) throw failure;
              try {
                health = await request("health");
              } catch {
                return false;
              }
              return (
                health.version === config.version &&
                completedApplication(state, config.version, false)
              );
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
    if (!directRemote) {
      const token = await page.evaluate(
        (key) => sessionStorage.getItem(key) ?? "",
        sessionKey,
      );
      await writeFile(sessionFile, JSON.stringify({ token }) + "\n", {
        mode: 0o600,
      });
      await chmod(sessionFile, 0o600);
    }
    return {
      result: "PASS",
      action: config.action,
      instanceId: health.instanceId,
      version: health.version,
      workspaceSha256,
      ownerAuth: true,
      ...(config.action === "claim"
        ? {
            passwordFreeSetup:
              !legacyRemotePasswordSetup &&
              (directRemote || new URL(config.url).hash.startsWith("#local=")),
            ...(legacyRemotePasswordSetup
              ? { legacyInitialPasswordSetup: true }
              : {}),
          }
        : {}),
      ...(config.action === "failed-check"
        ? { acquisitionFailurePreserved: true }
        : {}),
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
            browserClosedDuringApply: config.closeForAutomatic === true,
          }
        : {}),
      ...(config.action === "auth-cycle" ? { logoutSignIn: true } : {}),
      ...(config.action === "reconnect" ? { reconnected: true } : {}),
    };
  } finally {
    try {
      if (automaticPolicyStarted) {
        if (page.isClosed()) {
          page = await context.newPage();
          detachedRequest = undefined;
          await page.goto(address.origin + "/#settings");
        }
        await waitAuthenticated();
        await restoreFixtureManualPolicy(request);
      }
    } finally {
      await context.close();
      await browser.close();
    }
  }
}

if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href
) {
  const config = JSON.parse(await readFile(process.argv[2], "utf8"));
  const result =
    config.action === "agent-receipt"
      ? installedAgentReceipt(config)
      : await (
          config.action === "private-preflight"
            ? runPrivatePreflight
            : runStagedBrowser
        )(config, await import("@playwright/test"));
  console.log(JSON.stringify(result));
}
