import test from "node:test";
import assert from "node:assert/strict";
import {
  browserAddress,
  installedAccount,
  installedNavigate,
  stopInstalledReply,
  completedApplication,
  applicationUpdateFailure,
  privatePreflightAddress,
  runPrivatePreflight,
  retainedWorkspace,
  seedWorkspace,
  updateMode,
  restoreFixtureManualPolicy,
  detachedFixtureObserver,
} from "./staged-package-browser.mjs";

test("Apply observation reports a terminal refusal instead of waiting for restart", () => {
  for (const phase of ["check-failed", "recovery", "rolled-back"]) {
    const error = applicationUpdateFailure({
      operation: {
        id: "a0426465c8e3888946a5aca206aa63a4",
        phase,
        failure: "independent owner release differs from the approved update",
      },
    });
    assert.match(error.message, /a0426465c8e3888946a5aca206aa63a4/);
    assert.ok(error.message.includes(phase));
    assert.match(error.message, /independent owner release/);
  }
  for (const phase of ["applying", "completed", "idle"])
    assert.equal(applicationUpdateFailure({ operation: { phase } }), undefined);
});

test("the actual poller stops on a terminal update failure", async () => {
  const { expect } = await import("@playwright/test");
  let observations = 0;
  await assert.rejects(
    expect
      .poll(
        () => {
          observations++;
          const failure = applicationUpdateFailure({
            operation: {
              id: "retained-operation",
              phase: "check-failed",
              failure: "identity mismatch",
            },
          });
          if (failure) throw failure;
          return false;
        },
        { timeout: 1000, intervals: [1] },
      )
      .toBe(true),
    /retained-operation check-failed: identity mismatch/,
  );
  assert.equal(observations, 1, "terminal operation was polled again");
});

test("detached Remote cookie and Local bearer observers cannot dispatch", async () => {
  const calls = [];
  const remote = detachedFixtureObserver("https://node.tail-example.ts.net", {
    cookieRequest: {
      get: async (url, options) => {
        calls.push({ url, options });
        return { ok: () => true, json: async () => ({ readonly: true }) };
      },
    },
  });
  assert.deepEqual(await remote("health"), { readonly: true });
  assert.deepEqual(calls[0], {
    url: "https://node.tail-example.ts.net/api/v1/health",
    options: { maxRedirects: 0, timeout: 10000 },
  });
  await assert.rejects(remote("updates/application", "PUT", {}));
  await assert.rejects(remote("updates/preferences"));
  assert.equal(calls.length, 1);
  const local = detachedFixtureObserver("http://127.0.0.1:1234", {
    token: "A".repeat(43),
    fetchImpl: async (_, options) => {
      assert.equal(options.headers.Authorization, "Bearer " + "A".repeat(43));
      return { ok: true, json: async () => ({ readonly: true }) };
    },
  });
  assert.deepEqual(await local("health"), { readonly: true });
});

test("Automatic fixture failure cleanup restores only Manual policy", async () => {
  let state = {
    policyRevision: 7,
    activationEnabled: true,
    policy: {
      mode: "automatic",
      notify: true,
      pin: "0.1.9",
    },
  };
  const writes = [];
  const request = async (route, method = "GET", body) => {
    if (method === "GET") {
      assert.equal(route, "updates/application");
      return structuredClone(state);
    }
    assert.equal(route, "updates/preferences");
    assert.equal(method, "PUT");
    writes.push(body);
    state = { ...state, policy: body.policy, activationEnabled: false };
  };
  await restoreFixtureManualPolicy(request);
  assert.deepEqual(writes, [
    {
      revision: 7,
      action: "policy",
      policy: {
        mode: "manual",
        notify: true,
        pin: "0.1.9",
      },
    },
  ]);
  await restoreFixtureManualPolicy(request);
  assert.equal(writes.length, 1, "already-Manual state must not be rewritten");
  await assert.rejects(
    restoreFixtureManualPolicy(async () => ({
      policy: { mode: "manual" },
      activationEnabled: true,
    })),
  );
});

test("private preflight accepts only the verified node's bare nonce URL", () => {
  const config = {
    fixtureOnly: true,
    action: "private-preflight",
    expectedOrigin: "https://node.tail-example.ts.net",
    url: "https://node.tail-example.ts.net/#" + "A".repeat(43),
  };
  assert.equal(privatePreflightAddress(config).origin, config.expectedOrigin);
  for (const url of [
    "http://node.tail-example.ts.net/#" + "A".repeat(43),
    "https://other.tail-example.ts.net/#" + "A".repeat(43),
    "https://node.other.ts.net/#" + "A".repeat(43),
    "https://node.tail-example.ts.net:8443/#" + "A".repeat(43),
    "https://user@node.tail-example.ts.net/#" + "A".repeat(43),
    "https://node.tail-example.ts.net/?instance=owner#setup=proof",
    "https://node.tail-example.ts.net/approve#" + "A".repeat(43),
    "https://node.tail-example.ts.net/?other=value#" + "A".repeat(43),
    "https://node.tail-example.ts.net/#" + "A".repeat(42),
  ])
    assert.throws(() => privatePreflightAddress({ ...config, url }));
  for (const expectedOrigin of [
    "https://node.tail-example.ts.net/path",
    "https://node.example.org",
    "https://node.tail-example.ts.net/#proof",
  ])
    assert.throws(() => privatePreflightAddress({ ...config, expectedOrigin }));
  assert.throws(() =>
    privatePreflightAddress({ ...config, fixtureOnly: false }),
  );
});

test("private preflight uses fresh normal-TLS browser and real approval response only", async () => {
  const previous = process.env.FASED_DISPOSABLE_VM_ACCEPTANCE;
  process.env.FASED_DISPOSABLE_VM_ACCEPTANCE = "1";
  const config = {
    fixtureOnly: true,
    action: "private-preflight",
    expectedOrigin: "https://node.tail-example.ts.net",
    url: "https://node.tail-example.ts.net/#" + "A".repeat(43),
  };
  try {
    for (const status of [204, 403]) {
      const calls = [],
        response = {
          url: () => config.expectedOrigin + "/approve",
          request: () => ({ method: () => "POST" }),
          status: () => status,
        };
      const page = {
        waitForResponse: (accept) => {
          assert.equal(accept(response), true);
          assert.equal(
            accept({ ...response, url: () => "https://other.ts.net/approve" }),
            false,
          );
          assert.equal(
            accept({ ...response, request: () => ({ method: () => "GET" }) }),
            false,
          );
          calls.push("response registered");
          return Promise.resolve(response);
        },
        goto: async (url) => {
          assert.equal(url, config.url);
          calls.push("visit exact proof");
        },
        locator: (selector) => {
          assert.equal(selector, "#status");
          return "connected status";
        },
        url: () => config.expectedOrigin + "/",
      };
      const context = {
        route: async (pattern, handler) => {
          assert.equal(pattern, "**/*");
          await handler({
            request: () => ({ url: () => config.expectedOrigin + "/" }),
            continue: () => calls.push("same origin only"),
          });
        },
        newPage: async () => page,
        close: async () => calls.push("context closed"),
      };
      const browser = {
        newContext: async (options) => {
          assert.deepEqual(options, { locale: "en-US" });
          return context;
        },
        close: async () => calls.push("browser closed"),
      };
      const runtime = {
        chromium: {
          launch: async (options) => {
            assert.equal(options.headless, true);
            assert.equal(options.ignoreHTTPSErrors, undefined);
            assert.equal(options.args, undefined);
            return browser;
          },
        },
        expect: (value) => ({
          toHaveText: async (text) => {
            assert.equal(value, "connected status");
            assert.equal(
              text,
              "Connected. Continue in your installation terminal.",
            );
            calls.push("real connected response");
          },
        }),
      };
      if (status === 204) {
        assert.deepEqual(await runPrivatePreflight(config, runtime), {
          result: "PASS",
          action: "private-preflight",
          privateBrowserApproved: true,
        });
        assert.ok(calls.includes("real connected response"));
        assert.ok(calls.includes("context closed"));
      } else await assert.rejects(runPrivatePreflight(config, runtime));
      assert.ok(calls.includes("browser closed"));
      assert.ok(
        calls.indexOf("response registered") <
          calls.indexOf("visit exact proof"),
      );
    }
  } finally {
    if (previous === undefined)
      delete process.env.FASED_DISPOSABLE_VM_ACCEPTANCE;
    else process.env.FASED_DISPOSABLE_VM_ACCEPTANCE = previous;
  }
});

test("the installed policy's omitted default is Manual, never Automatic", () => {
  for (const mode of [undefined, "manual", "automatic"])
    assert.equal(updateMode({ policy: { mode } }), mode ?? "manual");
  for (const mode of [null, "", "scheduled", false])
    assert.throws(() => updateMode({ policy: { mode } }));
  for (const policy of [undefined, null, [], "bad", 0, false])
    assert.throws(() => updateMode({ policy }));
  assert.throws(() => updateMode({}));
});

test("legacy public bridge permits only its exact claim and preservation port", () => {
  const config = {
    fixtureOnly: true,
    action: "claim",
    url: "http://127.0.0.1:17483",
    version: "0.1.5",
  };
  assert.equal(browserAddress(config).port, "17483");
  assert.equal(
    browserAddress({ ...config, action: "preserve", version: "0.1.6" }).port,
    "17483",
  );
  for (const action of [
    "ui-update",
    "automatic-update",
    "check",
    "auth-cycle",
    "reconnect",
  ])
    assert.throws(
      () =>
        browserAddress({
          ...config,
          action,
          fromVersion: "0.1.4",
          version: "0.1.6",
        }),
      /legacy browser only/,
    );
  for (const port of ["17481", "17482"])
    assert.equal(
      browserAddress({
        ...config,
        action: "ui-update",
        url: `http://127.0.0.1:${port}`,
        fromVersion: "0.1.4",
        version: "0.1.6",
      }).port,
      port,
    );
  assert.throws(() =>
    browserAddress({ ...config, url: "http://127.0.0.1:17485" }),
  );
  assert.throws(() =>
    browserAddress({ ...config, url: "http://127.0.1.4:17483" }),
  );
});

const workspace = () => ({
  revision: 1,
  profileId: "owner-profile",
  profiles: [{ id: "owner-profile" }],
  sessions: [],
  messages: [],
  memories: [],
  tasks: [],
  runs: [],
});

test("preservation includes real conversations and messages, not an empty alias", () => {
  const original = workspace(),
    before = retainedWorkspace(original);
  for (const key of [
    "profiles",
    "sessions",
    "messages",
    "memories",
    "tasks",
    "runs",
  ]) {
    const changed = structuredClone(original);
    changed[key].push({ id: "preserved-record", text: "harmless data" });
    assert.notEqual(retainedWorkspace(changed), before, key);
  }
  assert.equal(
    retainedWorkspace({ ...original, conversations: ["irrelevant alias"] }),
    before,
  );
  const missing = workspace();
  delete missing.messages;
  assert.throws(() => retainedWorkspace(missing), /messages array required/);
});

test("automatic application requires the target and its matching completed receipt", () => {
  const receipt = {
    id: "operation",
    phase: "completed",
    sequence: "7",
    manifestSha256: "a".repeat(64),
    engineSha256: "b".repeat(64),
  };
  const state = {
    installedVersion: "0.1.6",
    operation: {
      id: receipt.id,
      phase: "completed",
      automatic: true,
      release: { ...receipt, version: "0.1.6" },
      receipt,
    },
  };
  assert.equal(completedApplication(state, "0.1.6", true), true);
  assert.equal(completedApplication(state, "0.1.6", false), false);
  for (const change of [
    { installedVersion: "0.1.4" },
    { operation: { ...state.operation, phase: "current" } },
    { operation: { ...state.operation, receipt: undefined } },
    { operation: { ...state.operation, automatic: false } },
    { operation: { ...state.operation, receipt: { ...receipt, id: "other" } } },
    {
      operation: {
        ...state.operation,
        receipt: { ...receipt, engineSha256: "c".repeat(64) },
      },
    },
  ])
    assert.equal(
      completedApplication({ ...state, ...change }, "0.1.6", true),
      false,
    );
});

test("baseline seeding appends harmless owner records and leaves its reminder paused", async () => {
  const view = workspace();
  view.sessions.push({ id: "existing-session", name: "Owner original" });
  view.messages.push({
    id: "existing-message",
    text: "Preserve my original bytes",
  });
  view.memories.push({ id: "existing-memory", text: "Owner original note" });
  const originals = structuredClone(view),
    commands = [];
  const request = async (route, method = "GET", command) => {
    assert.equal(route, "agents");
    if (method === "GET") return structuredClone(view);
    assert.equal(method, "POST");
    assert.equal(command.profileId, view.profileId);
    assert.equal(command.revision, view.revision);
    commands.push(command);
    if (command.action === "session-create")
      view.sessions.push({ id: "new-session", name: command.name });
    else if (command.action === "message-add") {
      assert.equal(command.id, "new-session");
      view.messages.push({
        id: "new-message",
        sessionId: command.id,
        role: "user",
        text: command.text,
      });
    } else if (command.action === "task-create")
      view.tasks.push({ id: "new-task", state: "scheduled" });
    else if (command.action === "task-pause") {
      assert.equal(command.id, "new-task");
      view.tasks[0].state = "paused";
    } else if (command.action === "memory-put")
      view.memories.push({ id: "new-memory", text: command.text });
    else assert.fail("unexpected executable or provider action");
    view.revision++;
    return structuredClone(view);
  };
  assert.deepEqual(await seedWorkspace(request), {
    profileId: "owner-profile",
    sessionId: "new-session",
    taskId: "new-task",
    memoryId: "new-memory",
  });
  assert.deepEqual(
    commands.map((value) => value.action),
    [
      "session-create",
      "message-add",
      "task-create",
      "task-pause",
      "memory-put",
    ],
  );
  assert.equal(view.tasks[0].state, "paused");
  assert.deepEqual(view.sessions[0], originals.sessions[0]);
  assert.deepEqual(view.messages[0], originals.messages[0]);
  assert.deepEqual(view.memories[0], originals.memories[0]);
  assert.notEqual(retainedWorkspace(view), retainedWorkspace(originals));
});

test("ordinary Remote browser uses exact private HTTPS and no copied CLI session", () => {
  const config = {
    fixtureOnly: true,
    action: "claim",
    transport: "tailscale-https",
    expectedOrigin: "https://node.tail-example.ts.net",
    instanceId: "I".repeat(43),
    version: "0.1.9",
    url:
      "https://node.tail-example.ts.net/?instance=" +
      "I".repeat(43) +
      "#setup=" +
      "S".repeat(43),
  };
  assert.equal(browserAddress(config).origin, config.expectedOrigin);
  for (const change of [
    { url: config.url.replace("https:", "http:") },
    { url: config.url.replace("node.tail-example", "other.tail-example") },
    { url: config.url.replace(".ts.net/", ".ts.net:8443/") },
    { url: config.url.replace("?instance=", "?other=") },
    { url: config.url + "&unexpected=1" },
    { instanceId: "J".repeat(43) },
    { sessionFile: "/tmp/copied-session.json" },
    { expectedOrigin: config.expectedOrigin + "/extra" },
    { transport: "ssh-forwarded" },
  ])
    assert.throws(() => browserAddress({ ...config, ...change }));
  assert.equal(
    browserAddress({
      ...config,
      action: "ui-update",
      url: config.expectedOrigin + "/?instance=" + config.instanceId,
      fromVersion: "0.1.8",
    }).protocol,
    "https:",
  );
});

test("older Remote password setup is limited to the declared public 0.1.6 claim", () => {
  const config = {
    fixtureOnly: true,
    action: "claim",
    transport: "tailscale-https",
    expectedOrigin: "https://node.tail-example.ts.net",
    instanceId: "I".repeat(43),
    version: "0.1.6",
    legacyRemotePasswordSetup: true,
    url:
      "https://node.tail-example.ts.net/?instance=" +
      "I".repeat(43) +
      "#setup=" +
      "S".repeat(43),
  };
  assert.equal(browserAddress(config).origin, config.expectedOrigin);
  for (const change of [
    { version: "0.1.9" },
    { legacyRemotePasswordSetup: "true" },
    { legacyRemotePasswordSetup: false },
    {
      action: "reconnect",
      url: config.expectedOrigin + "/?instance=" + config.instanceId,
    },
    { transport: "loopback", url: "http://127.0.0.1:17482/" },
  ])
    assert.throws(() => browserAddress({ ...config, ...change }));
});

test("preserved password fixture access is pinned to the real predecessor", () => {
  const instanceId = "I".repeat(43),
    origin = "https://node.tail-example.ts.net";
  const config = {
    fixtureOnly: true,
    action: "auth-cycle",
    transport: "tailscale-https",
    expectedOrigin: origin,
    url: origin + "/?instance=" + instanceId,
    instanceId,
    version: "0.1.9",
    ownerAuthMode: "existing-password-account-preserved",
    predecessor: {
      version: "0.1.6",
      ownerAuthMode: "password-v1",
      source: "738bd835b8c86c7cf7b3abfb2945240635f51fe7",
      artifactSetSha256:
        "aea00a0d9e1b7ed05a1346428cdf8cc277cbe40f93cfcd86d40fc9b45c559460",
    },
  };
  assert.doesNotThrow(() => browserAddress(config));
  for (const mutate of [
    (v) => {
      v.action = "claim";
    },
    (v) => {
      v.ownerAuthMode = "arbitrary";
    },
    (v) => {
      v.predecessor.source = "0".repeat(40);
    },
    (v) => {
      v.predecessor.ownerAuthMode = "tailscale-device-v1";
    },
  ]) {
    const bad = structuredClone(config);
    mutate(bad);
    assert.throws(() => browserAddress(bad));
  }
});

test("installed-agent producer binds observed stages and refuses missing or substituted proof", async () => {
  const { installedAgentReceipt } =
    await import("./staged-package-browser.mjs");
  const {
    acceptanceBinding,
    stagedAcceptanceContract,
    validateInstalledAgent,
  } = await import("./release-acceptance.mjs");
  const { createHash } = await import("node:crypto");
  const hash = (s) => createHash("sha256").update(s).digest("hex");
  const bundleManifest = JSON.stringify({
    files: { "fased-app": { sha256: "1".repeat(64), bytes: 1 } },
  });
  const packet = {
    source: "a".repeat(40),
    productVersion: "0.1.11",
    bundleSha256: hash(bundleManifest),
    release: { sourceTree: "b".repeat(40), runId: "123", runAttempt: "1" },
  };
  const inventory = {
    assets: [{ name: "engine", sha256: "1".repeat(64), size: 1 }],
  };
  const observed = {
    ...acceptanceBinding(packet, inventory),
    result: "PASS",
    profile: "local",
    instanceId: "disposable-instance",
    engineSha256: "1".repeat(64),
    installerOwnedProviderMode: true,
    sandboxed: true,
    testCodeOverlay: false,
    realProviderCalls: false,
    serviceRestarted: true,
    serviceRestart: { beforePID: "101", afterPID: "102" },
    kernelReboot: {
      before: "11111111-1111-1111-1111-111111111111",
      after: "22222222-2222-2222-2222-222222222222",
    },
    rebooted: true,
    revoked: true,
    checks: Object.fromEntries(
      stagedAcceptanceContract.installedAgent.map((k) => [k, "PASS"]),
    ),
  };
  const input = {
    packet,
    inventory,
    localReceiptSha256: "c".repeat(64),
    bundleManifest,
    observed,
    observedRaw: JSON.stringify(observed),
  };
  const receipt = installedAgentReceipt(input);
  assert.doesNotThrow(() =>
    validateInstalledAgent(
      receipt,
      packet,
      inventory,
      input.localReceiptSha256,
    ),
  );
  assert.equal(receipt.instanceIdentitySha256, hash(observed.instanceId));
  assert.equal(receipt.evidenceSha256, hash(input.observedRaw));
  assert.throws(() => installedAgentReceipt({ ...input, observedRaw: "{}" }));
  for (const change of [
    { result: "FAIL" },
    { serviceRestart: undefined },
    { serviceRestart: { beforePID: "1", afterPID: "1" } },
    { serviceRestart: { beforePID: "0", afterPID: "2" } },
    { kernelReboot: undefined },
    {
      kernelReboot: {
        before: "11111111-1111-1111-1111-111111111111",
        after: "11111111-1111-1111-1111-111111111111",
      },
    },
    {
      kernelReboot: {
        before: "invalid",
        after: "22222222-2222-2222-2222-222222222222",
      },
    },
    { profile: "remote" },
    { installerOwnedProviderMode: false },
    { serviceRestarted: false },
    { rebooted: false },
    { revoked: false },
    { source: "d".repeat(40) },
    { engineSha256: "2".repeat(64) },
    { realProviderCalls: true },
    { testCodeOverlay: true },
    { checks: { ...observed.checks, protectedCredentials: "OPEN" } },
  ])
    assert.throws(() =>
      installedAgentReceipt({
        ...input,
        observed: { ...observed, ...change },
        observedRaw: JSON.stringify({ ...observed, ...change }),
      }),
    );
});

test("installed provider browser actions require their dedicated profile and retain legacy refusal", () => {
  const config = {
    fixtureOnly: true,
    action: "agent-prepare",
    installedProviderProfile: true,
    url: "http://127.0.0.1:17485",
    version: "0.1.11",
  };
  assert.equal(browserAddress(config).port, "17485");
  assert.throws(() =>
    browserAddress({ ...config, installedProviderProfile: false }),
  );
  assert.throws(() =>
    browserAddress({ ...config, url: "http://127.0.0.1:17481" }),
  );
  assert.throws(() =>
    browserAddress({ ...config, action: "ui-update", fromVersion: "0.1.10" }),
  );
});

test("receipt CLI writes the same typed receipt consumed by the gate without opening a browser", async () => {
  const { mkdtemp, writeFile, rm } = await import("node:fs/promises");
  const { tmpdir } = await import("node:os");
  const { join } = await import("node:path");
  const { execFileSync } = await import("node:child_process");
  const { createHash } = await import("node:crypto");
  const {
    acceptanceBinding,
    stagedAcceptanceContract,
    validateInstalledAgent,
  } = await import("./release-acceptance.mjs");
  const hash = (s) => createHash("sha256").update(s).digest("hex");
  const bundleManifest = JSON.stringify({
    files: { "fased-app": { sha256: "1".repeat(64), bytes: 1 } },
  });
  const packet = {
    source: "a".repeat(40),
    productVersion: "0.1.11",
    bundleSha256: hash(bundleManifest),
    release: { sourceTree: "b".repeat(40), runId: "123", runAttempt: "1" },
  };
  const inventory = {
    assets: [{ name: "engine", sha256: "1".repeat(64), size: 1 }],
  };
  const observed = {
    ...acceptanceBinding(packet, inventory),
    result: "PASS",
    profile: "local",
    instanceId: "disposable-contract-instance",
    engineSha256: "1".repeat(64),
    installerOwnedProviderMode: true,
    sandboxed: true,
    testCodeOverlay: false,
    realProviderCalls: false,
    serviceRestarted: true,
    serviceRestart: { beforePID: "101", afterPID: "102" },
    kernelReboot: {
      before: "11111111-1111-1111-1111-111111111111",
      after: "22222222-2222-2222-2222-222222222222",
    },
    rebooted: true,
    revoked: true,
    checks: Object.fromEntries(
      stagedAcceptanceContract.installedAgent.map((k) => [k, "PASS"]),
    ),
  };
  const observedRaw = JSON.stringify(observed, null, 2) + "\n";
  const work = await mkdtemp(join(tmpdir(), "fased-agent-producer-"));
  try {
    const request = join(work, "request.json");
    await writeFile(
      request,
      JSON.stringify({
        action: "agent-receipt",
        packet,
        inventory,
        localReceiptSha256: "c".repeat(64),
        bundleManifest,
        observed,
        observedRaw,
      }),
      { mode: 0o600 },
    );
    const receipt = JSON.parse(
      execFileSync(
        process.execPath,
        ["scripts/staged-package-browser.mjs", request],
        { encoding: "utf8", timeout: 10000 },
      ),
    );
    validateInstalledAgent(receipt, packet, inventory, "c".repeat(64));
    assert.equal(receipt.evidenceSha256, hash(observedRaw));
    assert.notEqual(receipt.evidenceSha256, hash(JSON.stringify(observed)));
  } finally {
    await rm(work, { recursive: true, force: true });
  }
});

test("installed account reads use existing collection API and preserve account isolation", async () => {
  const accounts = [
    { ref: "account-a", provider: "openai" },
    { ref: "account-b", provider: "anthropic" },
  ];
  const request = async (route) => {
    assert.equal(route, "ai/connection");
    return { connections: accounts };
  };
  assert.deepEqual(await installedAccount(request, "account-b"), accounts[1]);
  await assert.rejects(installedAccount(request, "missing"), /exact unique/);
  await assert.rejects(installedAccount(request, ""), /exact installed/);
  await assert.rejects(
    installedAccount(
      async () => ({ connections: [accounts[0], accounts[0]] }),
      "account-a",
    ),
    /exact unique/,
  );
});

test("installed navigation keeps one UI profile context across internal pages", async () => {
  const target = { location: { hash: "#agents/settings" } };
  const original = globalThis.window;
  globalThis.window = target;
  try {
    const page = {
      evaluate: async (fn, arg) => fn(arg),
      goto: () =>
        assert.fail(
          "internal navigation must not reload and reset the selected profile",
        ),
    };
    for (const hash of [
      "#providers",
      "#agents/chat",
      "#agents/tasks",
      "#agents/memory",
    ]) {
      await installedNavigate(page, hash);
      assert.equal(target.location.hash, hash);
      assert.equal(globalThis.window, target);
    }
    await assert.rejects(installedNavigate(page, "https://foreign.invalid"));
  } finally {
    globalThis.window = original;
  }
});

test("installed Stop preserves completion races but refuses unrelated click failures", async () => {
  const failure = new Error("Stop disappeared");
  let clicks = 0;
  const page = {
    getByRole: () => ({
      click: async () => {
        clicks++;
        throw failure;
      },
    }),
  };
  await stopInstalledReply(page, async () => ({
    id: "reply",
    state: "completed",
  }));
  assert.equal(clicks, 0);
  for (const after of [
    { id: "reply", state: "completed" },
    { id: "reply", state: "running" },
    { id: "different", state: "completed" },
    undefined,
  ]) {
    let reads = 0;
    const latest = async () =>
      ++reads === 1 ? { id: "reply", state: "running" } : after;
    if (after?.id === "reply" && after.state === "completed")
      await stopInstalledReply(page, latest);
    else await assert.rejects(stopInstalledReply(page, latest), failure);
    assert.equal(reads, 2);
  }
  let actual = false;
  await stopInstalledReply(
    {
      getByRole: (_, options) => {
        assert.equal(options.name, "Stop");
        return {
          click: async () => {
            actual = true;
          },
        };
      },
    },
    async () => ({ id: "reply", state: "running" }),
  );
  assert.equal(actual, true);
});
