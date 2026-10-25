import { useUI } from "../ui-preferences";
import { useEffect, useState } from "react";
import type { AgentView, AgentAIRequest, ReminderReview } from "../agents";
import { useWorkspace } from "../workspace";

export function ReminderDraftReview({
  view,
  card,
}: {
  view: AgentView;
  card: AgentAIRequest["cards"][number];
}) {
  const { t: uiText } = useUI();
  const { agents, navigate } = useWorkspace();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(card.title);
  const [text, setText] = useState(card.text);
  const [interval, setInterval] = useState(3600);
  const [runs, setRuns] = useState(1);
  const [review, setReview] = useState<ReminderReview>();
  const [consent, setConsent] = useState("");
  useEffect(() => {
    if (view.reminderReview?.cardId === card.id) setReview(view.reminderReview);
  }, [view.reminderReview, card.id]);
  const created = view.tasks.find((t) => t.id === `chat-reminder:${card.id}`);
  if (created)
    return (
      <div className="agent-inline">
        <p role="status">
          {uiText("Reminder task created: ")}
          {created.name}.
        </p>
        <button
          type="button"
          className="secondary"
          onClick={() => navigate("agents/tasks")}
        >
          {uiText("Open Tasks")}
        </button>
      </div>
    );
  if (!open)
    return (
      <button
        className="secondary"
        disabled={agents.busy}
        onClick={() => setOpen(true)}
      >
        {uiText("Review reminder task")}
      </button>
    );
  const clear = () => {
    setReview(undefined);
    setConsent("");
  };
  const valid =
    !!review &&
    review.revision === view.revision &&
    review.expiresAt > Date.now() / 1000;
  return (
    <section aria-label={`Reminder task review: ${card.title}`}>
      <h4>{uiText("Review a local reminder")}</h4>
      <p>
        {uiText(
          "Edit this draft, review the exact schedule, then explicitly create the task. It only writes a reminder to your inbox; it cannot call AI, fetch news or trade.",
        )}
      </p>
      <form
        className="agent-form"
        onSubmit={(e) => {
          e.preventDefault();
          clear();
          void agents.change({
            action: "ai-reminder-review",
            id: card.id,
            name,
            text,
            intervalSeconds: interval,
            maxRuns: runs,
          });
        }}
      >
        <label>
          {uiText("Reminder task name")}
          <input
            required
            maxLength={80}
            value={name}
            onChange={(e) => {
              clear();
              setName(e.target.value);
            }}
          />
        </label>
        <label>
          {uiText("Reminder text")}
          <textarea
            aria-label={uiText("Reminder text")}
            required
            maxLength={4000}
            value={text}
            onChange={(e) => {
              clear();
              setText(e.target.value);
            }}
          />
        </label>
        <label>
          {uiText("Repeat every (seconds)")}
          <input
            type="number"
            required
            min={60}
            max={604800}
            step={1}
            value={interval}
            onChange={(e) => {
              clear();
              setInterval(Number(e.target.value));
            }}
          />
        </label>
        <label>
          {uiText("Maximum reminder runs")}
          <input
            type="number"
            required
            min={1}
            max={20}
            step={1}
            value={runs}
            onChange={(e) => {
              clear();
              setRuns(Number(e.target.value));
            }}
          />
        </label>
        <button className="secondary" disabled={agents.busy}>
          {uiText("Review reminder schedule")}
        </button>
      </form>
      {review && (
        <div className="agent-record-body">
          <strong>{review.name}</strong>
          <p>{review.text}</p>
          <p>
            {uiText("First reminder ")}
            {review.intervalSeconds}
            {uiText(
              " seconds after creation; repeat at that interval for at most ",
            )}
            {review.maxRuns}
            {uiText(" runs while the core runs. Missed intervals coalesce.")}
          </p>
          <p>
            {uiText("Review expires ")}
            {new Date(review.expiresAt * 1000).toLocaleString()}
            {uiText(". No task has been created.")}
          </p>
          <details>
            <summary>{uiText("Reminder source and review evidence")}</summary>
            <p>
              {uiText("Chat request: ")}
              <code>{review.requestId}</code>
            </p>
            <p>
              {uiText("Conversation: ")}
              <code>{review.sessionId}</code>
            </p>
            <p>
              {uiText("Source draft: ")}
              {review.sourceTitle}
            </p>
            <p>{review.sourceText}</p>
            <p>
              {uiText("Review SHA-256: ")}
              <code>{review.digest}</code>
            </p>
          </details>
          {!valid && (
            <p role="status">
              {uiText(
                "Workspace changed or review expired. Review the schedule again.",
              )}
            </p>
          )}
          <label className="check-label">
            <input
              type="checkbox"
              checked={consent === review.digest}
              disabled={!valid || agents.busy}
              onChange={(e) =>
                setConsent(e.target.checked ? review.digest : "")
              }
            />
            {uiText("I reviewed this reminder text and schedule")}
          </label>
          <button
            className="primary"
            disabled={!valid || consent !== review.digest || agents.busy}
            onClick={() =>
              void agents.change({
                action: "ai-reminder-create",
                id: card.id,
                revision: review.revision,
                name: review.name,
                text: review.text,
                intervalSeconds: review.intervalSeconds,
                maxRuns: review.maxRuns,
                expiresAt: review.expiresAt,
                consentDigest: review.digest,
              })
            }
          >
            {uiText("Create reviewed reminder")}
          </button>
        </div>
      )}
      <button
        className="text-button"
        disabled={agents.busy}
        onClick={() => {
          clear();
          setOpen(false);
        }}
      >
        {uiText("Cancel reminder review")}
      </button>
    </section>
  );
}
