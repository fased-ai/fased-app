import test from "node:test";
import assert from "node:assert/strict";
import {
  browserAddress,
  completedApplication,
  privatePreflightAddress,
  runPrivatePreflight,
  retainedWorkspace,
  seedWorkspace,
  updateMode,
} from "./staged-package-browser.mjs";

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
