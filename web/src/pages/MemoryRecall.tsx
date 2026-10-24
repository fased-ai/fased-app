import { useEffect, useRef, useState } from "react";
import type {
  AgentMemory,
  AgentMemoryEvidence,
  AgentMemoryRestoreDraft,
  AgentMemoryView,
  AgentView,
} from "../agents";
import { useWorkspace } from "../workspace";

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
  at ? new Date(at * 1000).toLocaleString() : "No expiry";

export function MemoryRecall({
  view,
  onCorrect,
  onRestore,
  onOpen,
}: {
  view: AgentView;
  onCorrect: (memory: AgentMemory) => void;
  onRestore: (draft: AgentMemoryRestoreDraft) => void;
  onOpen: (source: AgentMemoryEvidence) => void;
}) {
  const { core, agents } = useWorkspace();
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
          Search memories
          <input
            type="search"
            maxLength={200}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <button className="secondary" disabled={busy}>
          Search memory
        </button>
      </form>
      {error && <p role="alert">{error}</p>}
      {!records && !error && <p role="status">Loading memories…</p>}
      {records?.memories.map((m) => (
        <article
          className="memory-card"
          key={m.id}
          aria-label={`Memory ${m.id}`}
        >
          <p>{m.text}</p>
          <small>
            {m.source} · Updated {date(m.updatedAt)} ·{" "}
            {m.expired ? "Expired" : date(m.expiresAt)}
          </small>
          <div className="agent-inline">
            <button
              className="text-button"
              disabled={
                agents.busy || busy || records.revision !== view.revision
              }
              onClick={() => onCorrect(m)}
            >
              Correct note
            </button>
            <button
              className="text-button"
              disabled={
                agents.busy || busy || records.revision !== view.revision
              }
              onClick={() =>
                void agents.change({ action: "memory-delete", id: m.id })
              }
            >
              Delete note
            </button>
            <button
              className="text-button"
              disabled={
                agents.busy || busy || records.revision !== view.revision
              }
              onClick={() => void inspect(m.id)}
            >
              Inspect source
            </button>
          </div>
          {m.history.length > 0 && (
            <details>
              <summary>{m.history.length} previous revision(s)</summary>
              {m.history.map((h, i) => (
                <section key={i} aria-label={`Memory revision ${i + 1}`}>
                  <p>{h.text}</p>
                  <small>
                    {h.source} · Updated {date(h.at)} · Expiry{" "}
                    {date(h.expiresAt)}
                  </small>
                  <button
                    className="text-button"
                    disabled={
                      agents.busy ||
                      busy ||
                      records.revision !== view.revision ||
                      m.history.length >= 8
                    }
                    onClick={() => void restore(m.id, i)}
                  >
                    Review restoration
                  </button>
                </section>
              ))}
              {m.history.length >= 8 && (
                <p role="status">Correction history full.</p>
              )}
            </details>
          )}
          {source?.memoryId === m.id && (
            <section aria-label="Memory source">
              <h4>Memory source</h4>
              <p>{memoryEvidenceStatuses[source.status]}</p>
              {source.status === "matched" && (
                <>
                  <p>{source.text}</p>
                  <small>
                    {source.kind} · {source.id} · SHA-256 {source.sha256}
                  </small>
                  <button
                    className="secondary"
                    disabled={!current || agents.busy}
                    onClick={() => onOpen(source)}
                  >
                    Open conversation
                  </button>
                </>
              )}
              {!current && (
                <p role="status">
                  Workspace changed. Inspect the source again.
                </p>
              )}
            </section>
          )}
        </article>
      ))}
      {records?.memories.length === 0 && (
        <p>{query ? "No matching memories." : "No notes in this profile."}</p>
      )}
    </>
  );
}
