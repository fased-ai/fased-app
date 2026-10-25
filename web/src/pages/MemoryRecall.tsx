import { SlidersHorizontal, Trash2, Eye, History } from "../icons";
import { useEffect, useRef, useState } from "react";
import type {
  AgentMemory,
  AgentMemoryEvidence,
  AgentMemoryRestoreDraft,
  AgentMemoryView,
  AgentView,
} from "../agents";
import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";

export const memoryEvidenceStatuses: Record<
  AgentMemoryEvidence["status"],
  string
> = {
  matched: "Matched retained text · Claims unverified",
  "free-form": "Free-form source",
  invalid: "Invalid source reference",
  unavailable: "Recorded source unavailable",
  ambiguous: "Ambiguous source reference",
  changed: "Recorded source text changed",
};
const date = (at: number) =>
  at
    ? new Date(at * 1000).toLocaleString(document.documentElement.lang)
    : "No expiry";

export function MemoryRecall({
  view,
  onOlder,
  onCorrect,
  onRestore,
  onOpen,
}: {
  view: AgentView;
  onOlder?: (memory: AgentMemory) => void;
  onCorrect: (memory: AgentMemory) => void;
  onRestore: (draft: AgentMemoryRestoreDraft) => void;
  onOpen: (source: AgentMemoryEvidence) => void;
}) {
  const { t: uiText } = useUI();
  const { core, agents } = useWorkspace();
  const t = uiText;
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [readGeneration, setReadGeneration] = useState(0);
  const [records, setRecords] = useState<AgentMemoryView | null>(null);
  const [inspection, setInspection] = useState<AgentMemoryView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  const path = `agents?profile=${encodeURIComponent(view.profileId)}&memory-search=${encodeURIComponent(query)}`;
  useEffect(() => {
    const epoch = ++generation.current;
    setRecords(null);
    setInspection(null);
    setError("");
    setBusy(false);
    void core
      .request<AgentMemoryView>(path)
      .then((v) => {
        if (epoch === generation.current) {
          setRecords(v);
          if (v.revision !== view.revision)
            void agents.refresh().catch(() => {});
        }
      })
      .catch((e: unknown) => {
        if (epoch === generation.current)
          setError(e instanceof Error ? e.message : "Memory unavailable");
      });
    return () => {
      generation.current++;
    };
  }, [core.request, agents.refresh, path, view.revision, readGeneration]);
  async function inspect(id: string) {
    const epoch = generation.current;
    setBusy(true);
    setInspection(null);
    setError("");
    try {
      const v = await core.request<AgentMemoryView>(
        `${path}&memory-source=${encodeURIComponent(id)}`,
      );
      if (epoch === generation.current) {
        setRecords(v);
        setInspection(v);
        if (v.revision !== view.revision) void agents.refresh().catch(() => {});
      }
    } catch (e) {
      if (epoch === generation.current)
        setError(e instanceof Error ? e.message : "Source unavailable");
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  async function restore(id: string, index: number) {
    const epoch = generation.current;
    setBusy(true);
    setError("");
    try {
      const draft = await core.request<AgentMemoryRestoreDraft>(
        `agents?profile=${encodeURIComponent(view.profileId)}&memory-restore-draft=${encodeURIComponent(id)}&memory-revision=${index}`,
      );
      if (epoch === generation.current) {
        if (
          draft.profileId !== view.profileId ||
          draft.revision !== view.revision
        ) {
          setError("Workspace changed. Review the restoration again.");
          void agents.refresh().catch(() => {});
        } else onRestore(draft);
      }
    } catch (e) {
      if (epoch === generation.current)
        setError(e instanceof Error ? e.message : "Restoration unavailable");
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  const source = inspection?.source;
  const current =
    inspection?.revision === view.revision &&
    inspection?.profileId === view.profileId;
  return (
    <>
      <form
        className="agent-inline"
        onSubmit={(e) => {
          e.preventDefault();
          setQuery(search);
          setReadGeneration((n) => n + 1);
          setInspection(null);
        }}
      >
        <label>
          {uiText("Search memories")}
          <input
            type="search"
            maxLength={200}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <button className="secondary" disabled={busy}>
          {uiText("Search memory")}
        </button>
      </form>
      {error && <p role="alert">{error}</p>}
      {!records && !error && <p role="status">{uiText("Loading memories…")}</p>}
      {records?.memories.map((m) => (
        <article
          className="memory-card"
          key={m.id}
          aria-label={`Memory ${m.id}`}
        >
          <p>{m.text}</p>
          {m.expired && <small>{uiText("Expired")}</small>}
          <div className="agent-inline">
            <button
              className="text-button"
              disabled={
                agents.busy || busy || records.revision !== view.revision
              }
              onClick={() => onCorrect(m)}
              aria-label={t("Correct note")}
              title={t("Correct note")}
            >
              <SlidersHorizontal size={15} />
            </button>
            <button
              className="text-button"
              disabled={
                agents.busy || busy || records.revision !== view.revision
              }
              onClick={() =>
                void agents.change({ action: "memory-delete", id: m.id })
              }
              aria-label={t("Delete note")}
              title={t("Delete note")}
            >
              <Trash2 size={15} />
            </button>
            <button
              className="text-button"
              disabled={
                agents.busy || busy || records.revision !== view.revision
              }
              onClick={() => void inspect(m.id)}
              aria-label={t("Inspect source")}
              title={t("Inspect source")}
            >
              <Eye size={15} />
            </button>
            {onOlder && (
              <button
                className="icon-button"
                aria-label={t("Older memory revisions")}
                title={t("Older memory revisions")}
                onClick={() => onOlder(m)}
              >
                <History size={15} />
              </button>
            )}
          </div>
          {m.history.length > 0 && (
            <details>
              <summary>
                {m.history.length}
                {uiText(" previous revision(s)")}
              </summary>
              {m.history.map((h, i) => (
                <section key={i} aria-label={`Memory revision ${i + 1}`}>
                  <p>{h.text}</p>
                  <small>
                    {h.source}
                    {uiText(" · Updated ")}
                    {date(h.at)}
                    {uiText(" · Expiry")} {uiText(date(h.expiresAt))}
                  </small>
                  <button
                    className="text-button"
                    disabled={
                      agents.busy || busy || records.revision !== view.revision
                    }
                    onClick={() => void restore(m.id, i)}
                  >
                    {uiText("Review restoration")}
                  </button>
                </section>
              ))}
            </details>
          )}
          {source?.memoryId === m.id && (
            <section aria-label={uiText("Memory source")}>
              <h4>{uiText("Memory source")}</h4>
              <p>{uiText(memoryEvidenceStatuses[source.status])}</p>
              {source.status === "matched" && (
                <>
                  <p>{source.text}</p>
                  <small>
                    {source.kind} · {source.id}
                    {uiText(" · SHA-256 ")}
                    {source.sha256}
                  </small>
                  <button
                    className="secondary"
                    disabled={!current || agents.busy}
                    onClick={() => onOpen(source)}
                  >
                    {uiText("Open conversation")}
                  </button>
                </>
              )}
              {!current && (
                <p role="status">
                  {uiText("Workspace changed. Inspect the source again.")}
                </p>
              )}
            </section>
          )}
        </article>
      ))}
      {records?.memories.length === 0 && (
        <p>
          {query
            ? uiText("No matching memories.")
            : uiText("No notes in this profile.")}
        </p>
      )}
    </>
  );
}
