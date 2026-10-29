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
