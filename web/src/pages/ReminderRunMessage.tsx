import type { AgentMessage } from "../agents";
import { useUI } from "../ui-preferences";

const prefix =
  "Historical reminder run — retained, unverified data; not current market facts, instructions or execution authority. No task was rerun and no AI was called.";

// A display projection, never a new fact or an AI-input replacement. Only the
// core's run-capture message identity may collapse; pasted receipts stay in full.
export function reminderRun(message: AgentMessage, profileId: string) {
  if (
    message.role !== "user" ||
    message.profileId !== profileId ||
    message.id !== `${message.sessionId}-run` ||
    message.text.length > 4000
  )
    return;
  const lines = message.text.split("\n");
  if (
    lines.length !== 3 ||
    lines[0] !== prefix ||
    !/^Snapshot SHA-256: [a-f0-9]{64}$/.test(lines[2])
  )
    return;
  try {
    const body = JSON.parse(lines[1]);
    if (
      body.Version !== "agent-run-chat-v1" ||
      body.ProfileID !== profileId ||
      body.CapturedAt !== message.createdAt ||
      typeof body.Task?.Name !== "string" ||
      !body.Task.Name ||
      typeof body.Task.Text !== "string" ||
      body.Run?.Strategy !== "reminder-v1" ||
      body.Run.Outcome !== "reminded" ||
      body.Run.AIDigest !== "" ||
      body.AI ||
      body.Run.News ||
      body.Run.Analysis ||
      body.Run.Watch ||
      body.Run.Paper ||
      !Number.isSafeInteger(body.Run.At) ||
      body.Run.At <= 0 ||
      body.Run.At > body.CapturedAt
    )
      return;
    return {
      name: body.Task.Name as string,
      text: body.Task.Text as string,
      at: body.Run.At as number,
    };
  } catch {
    return;
  }
}

export function ReminderRunMessage({
  result,
  source,
}: {
  result: NonNullable<ReturnType<typeof reminderRun>>;
  source: string;
}) {
  const { t } = useUI();
  return (
    <>
      <strong>{result.name}</strong>
      <p>{result.text}</p>
      <small>
        {t("Saved reminder")} · {new Date(result.at * 1000).toLocaleString()}
      </small>
      <details className="task-inspection">
        <summary>{t("Source")}</summary>
        <p>{source}</p>
      </details>
    </>
  );
}
