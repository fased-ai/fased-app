import test from "node:test";
import assert from "node:assert/strict";
import {
  browserAddress,
  completedApplication,
  retainedWorkspace,
  seedWorkspace,
  updateMode,
} from "./staged-package-browser.mjs";

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
