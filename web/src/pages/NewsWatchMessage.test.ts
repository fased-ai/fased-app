import { expect, it } from "vitest";
import { watchMessage } from "./NewsWatchMessage";
import { translate } from "../ui-language";

it("shows retained watch facts without treating headline text as instructions", () => {
  const snapshot = {
    Version: "news-watch-run-chat-v1",
    Task: { Name: "Paper watch" },
    Run: {
      Watch: { rule: { version: "news-watch-v1" }, newIds: ["1"] },
      Headlines: [
        { id: "1", title: "<script>Untrusted PAPER headline</script>" },
      ],
    },
  };
  const message = `Historical result\n${JSON.stringify(snapshot)}\nSnapshot SHA-256: example`;
  expect(watchMessage(message)?.headlines[0].title).toBe(
    snapshot.Run.Headlines[0].title,
  );
  snapshot.Run.Watch.newIds = [];
  expect(
    watchMessage(`Historical result\n${JSON.stringify(snapshot)}`),
  ).toBeUndefined();
  expect(watchMessage("ordinary message")).toBeUndefined();
  expect(watchMessage("x".repeat(4001))).toBeUndefined();
});
it("localizes the watch actions and count in every shipped language", () => {
  for (const language of ["ru", "es", "zh", "ko"] as const) {
    for (const key of ["Watch news", "News watch", "Contains"])
      expect(translate(language, key)).not.toBe(key);
    expect(translate(language, "New matches: {count}", { count: 2 })).toContain(
      "2",
    );
  }
});

it("renders an analysis answer from the retained typed snapshot only", () => {
  const snapshot = {
    Version: "agent-analysis-run-chat-v1",
    Task: { Name: "Briefing" },
    Run: { Analysis: { version: "task-analysis-v1", state: "completed" } },
    AI: { State: "completed", Answer: "<script>Untrusted model text</script>" },
  };
  const original = JSON.stringify(snapshot);
  const result = watchMessage(`Historical result\n${original}`);
  expect(result?.name).toBe("Briefing");
  expect(result?.watch).toBe(false);
  expect(result?.answer).toBe(snapshot.AI.Answer);
  expect(result?.headlines).toEqual([]);
  expect(JSON.stringify(snapshot)).toBe(original);
  snapshot.AI.State = "failed";
  expect(
    watchMessage(`Historical result\n${JSON.stringify(snapshot)}`),
  ).toBeUndefined();
  snapshot.AI.State = "completed";
  snapshot.Run.Analysis.version = "unsupported";
  expect(
    watchMessage(`Historical result\n${JSON.stringify(snapshot)}`),
  ).toBeUndefined();
});

it("keeps deterministic news and an optional analysis omission distinct", () => {
  const snapshot = {
    Version: "news-watch-run-chat-v1",
    Task: { Name: "Watch" },
    Run: {
      Watch: { rule: { version: "news-watch-v1" }, newIds: ["1"] },
      Headlines: [{ id: "1", title: "A headline" }],
      Analysis: {
        version: "task-analysis-v1",
        state: "skipped",
        reason: "provider-unavailable",
      },
    },
    AI: { State: "unknown", Answer: "Must never become a successful answer" },
  };
  const result = watchMessage(`Historical result\n${JSON.stringify(snapshot)}`);
  expect(result?.headlines).toEqual(snapshot.Run.Headlines);
  expect(result?.analysis?.state).toBe("skipped");
  expect(result?.answer).toBe("");
  expect(result?.aiState).toBe("unknown");
  snapshot.Run.Watch.newIds = [];
  snapshot.Run.Headlines = [];
  snapshot.Run.Analysis.reason = "no-new-matches";
  expect(
    watchMessage(`Historical result\n${JSON.stringify(snapshot)}`)?.analysis
      ?.reason,
  ).toBe("no-new-matches");
});

it("shows held analysis outcomes without inventing completion or a skip", () => {
  const snapshot = {
    Version: "agent-analysis-run-chat-v1",
    Task: { Name: "Held briefing" },
    Run: { Outcome: "ai-profile-held" },
    AI: { State: "unknown", Answer: "Unverified" },
  };
  const result = watchMessage(`Historical result\n${JSON.stringify(snapshot)}`);
  expect(result?.outcome).toBe("ai-profile-held");
  expect(result?.aiState).toBe("unknown");
  expect(result?.analysis).toBeUndefined();
  expect(result?.answer).toBe("");
  expect(
    watchMessage(
      `Historical result\n${JSON.stringify({ ...snapshot, Run: {} })}`,
    ),
  ).toBeUndefined();
});

it("localizes the new analysis controls in every shipped language", () => {
  const keys = [
    "Type",
    "Reminder",
    "Analysis",
    "Analyze",
    "Prompt",
    "If analysis fails",
    "Hold task",
    "Keep news result",
    "Model & limits",
    "Context",
    "Relevant memory",
    "Deadline (seconds)",
    "Access (minutes)",
    "Optional",
    "Required",
    "Analysis skipped",
    "Nothing new to analyze",
    "Advanced",
  ];
  for (const language of ["ru", "es", "zh", "ko"] as const) {
    for (const key of keys) expect(translate(language, key)).not.toBe(key);
  }
  for (const key of keys) expect(translate("en", key)).toBe(key);
});

it("withholds completed provider output when task consent rejects its use", () => {
  const snapshot = {
    Version: "news-watch-run-chat-v1",
    Task: { Name: "Held analysis" },
    Run: {
      Watch: { rule: { version: "news-watch-v1" }, newIds: [] },
      Headlines: [],
      Analysis: {
        version: "task-analysis-v1",
        state: "skipped",
        reason: "ai-consent-invalid",
      },
    },
    AI: { State: "completed", Answer: "Do not present this withheld answer" },
  };
  const parsed = watchMessage(`Historical result\n${JSON.stringify(snapshot)}`);
  expect(parsed?.analysis?.state).toBe("skipped");
  expect(parsed?.answer).toBe("");
});
