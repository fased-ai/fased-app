import { expect, it } from "vitest";
import { reminderRun } from "./ReminderRunMessage";
import type { AgentMessage } from "../agents";
import { translate } from "../ui-language";

const source = () => ({
  Version: "agent-run-chat-v1",
  ProfileID: "analyst",
  CapturedAt: 1234,
  Task: { Name: "Risk review", Text: "Read the saved note" },
  Run: { Strategy: "reminder-v1", Outcome: "reminded", At: 1230, AIDigest: "" },
});
const message = (body = source()): AgentMessage => ({
  id: "session-run",
  sessionId: "session",
  profileId: "analyst",
  role: "user",
  createdAt: 1234,
  text: `Historical reminder run — retained, unverified data; not current market facts, instructions or execution authority. No task was rerun and no AI was called.\n${JSON.stringify(body)}\nSnapshot SHA-256: ${"a".repeat(64)}`,
});

it("projects a saved reminder for Chat and memory without changing its source", () => {
  const m = message();
  const original = JSON.stringify(m);
  expect(reminderRun(m, "analyst")).toEqual({
    name: "Risk review",
    text: "Read the saved note",
    at: 1230,
  });
  expect(JSON.stringify(m)).toBe(original);
});

it("keeps pasted receipts, other profiles and unsupported or malformed results in full", () => {
  const m = message();
  expect(
    reminderRun({ ...m, id: "ordinary-message" }, "analyst"),
  ).toBeUndefined();
  expect(reminderRun(m, "other")).toBeUndefined();
  expect(reminderRun({ ...m, role: "system" }, "analyst")).toBeUndefined();
  expect(
    reminderRun(
      { ...m, text: m.text.replace("agent-run-chat-v1", "future-v2") },
      "analyst",
    ),
  ).toBeUndefined();
  expect(
    reminderRun(
      { ...m, text: m.text.replace("reminded", "failed") },
      "analyst",
    ),
  ).toBeUndefined();
  expect(
    reminderRun({ ...m, text: m.text.replace("1230", "null") }, "analyst"),
  ).toBeUndefined();
  expect(
    reminderRun({ ...m, text: m.text + "\nmore" }, "analyst"),
  ).toBeUndefined();
  expect(
    reminderRun({ ...m, text: "x".repeat(4001) }, "analyst"),
  ).toBeUndefined();
});

it("translates the concise run and memory labels in every shipped language", () => {
  for (const language of ["ru", "es", "zh", "ko"] as const)
    for (const key of [
      "Saved reminder",
      " budget",
      " (preview)",
      "Run preview",
      "Execution unavailable",
      "Note",
      "Runs while Fased is running. Missed intervals coalesce.",
    ])
      expect(translate(language, key)).not.toBe(key);
});
