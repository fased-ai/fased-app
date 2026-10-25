import { useEffect, useRef, useState } from "react";
import type {
  AgentRun,
  AgentTask,
  AgentView,
  AgentMessage,
  AgentAIRequest,
} from "../agents";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";

type Summary = {
  messages: number;
  kind: string;
  id: number;
  taskId: string;
  name: string;
  runs: number;
  archivedAt: number;
  sha256: string;
};
type History = {
  profileId: string;
  records: Summary[];
  nextBefore: number;
  entry?: {
    version: string;
    session?: { id: string; name: string };
    memory?: {
      id: string;
      profileId: string;
      index: number;
      revision: { text: string; source: string; at: number; expiresAt: number };
    };
    messages?: AgentMessage[];
    aiRequests?: AgentAIRequest[];
    task: AgentTask;
    runs: AgentRun[];
    notices: unknown[];
  };
  sha256?: string;
};
export function TaskHistory({
  view: v,
  kind = "tasks",
  initialSearch = "",
  initialSource = "",
  onMemorySelected,
}: {
  view: AgentView;
  kind?: "tasks" | "conversations" | "memories";
  onMemorySelected?: (memory: {
    id: string;
    text: string;
    source: string;
    expiresAt: number;
  }) => void;
  initialSearch?: string;
  initialSource?: string;
}) {
  const core = useCore();
  const { agents } = useWorkspace();
  const { t } = useUI();
  const [query, setQuery] = useState(initialSearch);
  const [history, setHistory] = useState<History>();
  const [entry, setEntry] = useState<History>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const current =
    v.profiles.find((p) => p.id === v.profileId)?.historyDays ?? 0;
  const [days, setDays] = useState(current);
  const generation = useRef(0);
  async function load(before = 0, id = "", searchValue = query) {
    const request = ++generation.current;
    setBusy(true);
    setError("");
    try {
      const query = new URLSearchParams({
        profile: v.profileId,
        history: kind,
      });
      if (!id && searchValue) query.set("history-q", searchValue);
      if (before) query.set("before", String(before));
      if (id) query.set("history-task", id);
      const next = await core.request<History>("agents?" + query);
      if (request !== generation.current) return;
      if (id) setEntry(next);
      else {
        setHistory((old) =>
          before && old
            ? { ...next, records: [...old.records, ...next.records] }
            : next,
        );
        setEntry(undefined);
      }
    } catch (e) {
      if (request === generation.current)
        setError(e instanceof Error ? e.message : "History unavailable");
    } finally {
      if (request === generation.current) setBusy(false);
    }
  }
  useEffect(() => {
    void load(0, initialSource);
    return () => {
      generation.current++;
    };
  }, [v.profileId, kind]);
  useEffect(() => setDays(current), [current]);
  function download() {
    if (!entry?.entry) return;
    const blob = new Blob([JSON.stringify(entry, null, 2) + "\n"], {
      type: "application/json",
    });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = `fased-history-${entry.entry.memory?.id ?? entry.entry.session?.id ?? entry.entry.task.id}.json`;
    link.click();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }
  return (
    <section
      className="panel task-archive"
      aria-label={t(
        kind === "tasks"
          ? "Archived tasks"
          : kind === "memories"
            ? "Memory history"
            : "Archived conversations",
      )}
    >
      <div className="panel-heading">
        <h2>
          {t(
            kind === "tasks"
              ? "Archived tasks"
              : kind === "memories"
                ? "Memory history"
                : "Archived conversations",
          )}
        </h2>
        <button
          className="text-button"
          disabled={busy}
          onClick={() => void load()}
        >
          {t("Refresh")}
        </button>
      </div>
      <div className="task-detail-body">
        {error && <p role="alert">{error}</p>}
        {kind === "tasks" && (
          <details className="task-inspection">
            <summary>{t("History settings")}</summary>
            <div className="row-actions">
              <label>
                {t("Archive completed reminders")}
                <select
                  aria-label={t("Archive completed reminders")}
                  value={days}
                  disabled={agents.busy}
                  onChange={(e) => setDays(Number(e.target.value))}
                >
                  <option value={0}>{t("Manually")}</option>
                  {[7, 30, 90].map((n) => (
                    <option key={n} value={n}>
                      {n} {t("days after completion")}
                    </option>
                  ))}
                </select>
              </label>
              <button
                className="secondary"
                disabled={agents.busy || days === current}
                onClick={() =>
                  void agents.change({
                    action: "history-policy",
                    historyDays: days,
                  })
                }
              >
                {t("Save")}
              </button>
            </div>
            <p>
              {t(
                "Automatic archiving keeps unread results and linked evidence active. Archived records remain available.",
              )}
            </p>
          </details>
        )}
        <form
          className="row-actions"
          onSubmit={(e) => {
            e.preventDefault();
            void load();
          }}
        >
          <input
            type="search"
            aria-label={t("Search archive")}
            value={query}
            maxLength={200}
            onChange={(e) => setQuery(e.target.value)}
          />
          <button className="secondary" disabled={busy}>
            {t("Search")}
          </button>
        </form>
        {history?.records.length ? (
          <div className="task-history-list">
            {history.records.map((row) => (
              <button
                key={row.id}
                className="memory-row"
                disabled={busy}
                onClick={() => void load(0, row.taskId)}
              >
                <strong>{row.name}</strong>
                <span>
                  {kind !== "memories" && (
                    <>
                      {kind === "tasks" ? row.runs : row.messages}{" "}
                      {t(kind === "tasks" ? "runs" : "messages")} ·{" "}
                    </>
                  )}
                  {new Date(row.archivedAt * 1000).toLocaleDateString()}
                </span>
              </button>
            ))}
          </div>
        ) : !entry?.entry ? (
          <p className="empty-row">
            {busy
              ? t("Loading…")
              : t(
                  kind === "tasks"
                    ? "No archived tasks."
                    : kind === "memories"
                      ? "No earlier revisions."
                      : "No archived conversations.",
                )}
          </p>
        ) : null}
        {!!history?.nextBefore && (
          <button
            className="text-button"
            disabled={busy}
            onClick={() => void load(history.nextBefore)}
          >
            {t("Load more")}
          </button>
        )}
        {entry?.entry && (
          <section
            aria-label={t("Archived task result")}
            className="task-detail"
          >
            <div className="panel-heading">
              <h3>
                {entry.entry.session?.name ??
                  (entry.entry.memory
                    ? "Memory revision " + (entry.entry.memory.index + 1)
                    : entry.entry.task.name)}
              </h3>
              <button className="secondary" onClick={download}>
                {t("Export JSON")}
              </button>
            </div>
            <p className="task-result-text">
              {entry.entry.memory?.revision.text ?? entry.entry.task.text}
            </p>
            {entry.entry.memory && onMemorySelected && (
              <button
                className="primary"
                onClick={() => {
                  const m = entry.entry!.memory!;
                  onMemorySelected({ id: m.id, ...m.revision });
                }}
              >
                {t("Review revision")}
              </button>
            )}
            {entry.entry.messages?.map((m) => (
              <article key={m.id} className="agent-message">
                <small>{m.role}</small>
                <p>{m.text}</p>
              </article>
            ))}
            {entry.entry.aiRequests?.map((q) => (
              <article key={q.id} className="agent-message">
                <p>{q.prompt}</p>
                <p>{q.answer || q.failure}</p>
              </article>
            ))}
            <dl className="task-facts">
              <dt>{t("Status")}</dt>
              <dd>{t("Archived")}</dd>
              {kind === "tasks" && (
                <>
                  <dt>{t("Runs")}</dt>
                  <dd>{entry.entry.runs?.length ?? 0}</dd>
                </>
              )}
            </dl>
            {(entry.entry.runs ?? []).map((run) => (
              <p key={run.id}>
                {new Date(run.at * 1000).toLocaleString()} · {run.outcome}
              </p>
            ))}
            <details className="task-inspection">
              <summary>{t("Receipts")}</summary>
              <code>{entry.sha256}</code>
              <pre>{JSON.stringify(entry.entry, null, 2)}</pre>
            </details>
          </section>
        )}
      </div>
    </section>
  );
}
