import { useEffect, useRef, useState } from "react";
import type {
  AgentView,
  AgentEvidenceKind,
  AgentEvidenceView,
} from "../agents";
import { useWorkspace } from "../workspace";

const at = (n: number) => new Date(n * 1000).toLocaleString();
export function AgentEvidenceInspector({
  view,
  sessionId,
}: {
  view: AgentView;
  sessionId: string;
}) {
  return <EvidenceForm key={`${view.profileId}:${sessionId}`} view={view} />;
}
function EvidenceForm({ view }: { view: AgentView }) {
  const { core, agents } = useWorkspace();
  const [kind, setKind] = useState<AgentEvidenceKind>("memory");
  const [selection, setSelection] = useState("");
  const [result, setResult] = useState<AgentEvidenceView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );
  const choices =
    kind === "memory"
      ? view.memories.map((m) => ({
          value: JSON.stringify([m.id, ""]),
          label: m.text.slice(0, 80) + (m.expired ? " · expired" : ""),
        }))
      : kind === "headline"
        ? view.newsFetches
            .filter((f) => f.state === "completed")
            .flatMap((f) =>
              f.items.map((i) => ({
                value: JSON.stringify([f.id, i.id]),
                label: `${i.title} · ${at(f.fetchedAt)}`,
              })),
            )
        : view.runs
            .filter(
              (r) =>
                r.strategy === "reminder-v1" && !r.news && !r.botId && !r.wen,
            )
            .map((r) => ({
              value: JSON.stringify([r.id, ""]),
              label: `${view.tasks.find((t) => t.id === r.taskId)?.name ?? "Reminder"} · ${at(r.at)}`,
            }));
  function cancel() {
    generation.current++;
    setResult(null);
    setError("");
    setBusy(false);
  }
  async function inspect() {
    const epoch = ++generation.current;
    const [id, item] = JSON.parse(selection) as [string, string];
    setBusy(true);
    setResult(null);
    setError("");
    try {
      const q = new URLSearchParams({
        profile: view.profileId,
        evidence: kind,
        "evidence-id": id,
      });
      if (kind === "headline") q.set("evidence-item", item);
      const v = await core.request<AgentEvidenceView>(`agents?${q}`);
      if (epoch !== generation.current) return;
      if (
        v.profileId !== view.profileId ||
        v.kind !== kind ||
        v.id !== id ||
        (v.itemId ?? "") !== item
      )
        throw new Error("Evidence scope mismatch");
      setResult(v);
      if (v.revision !== view.revision) void agents.refresh().catch(() => {});
    } catch (e) {
      if (epoch === generation.current)
        setError(e instanceof Error ? e.message : "Evidence unavailable");
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  const current = result?.revision === view.revision;
  const text =
    result?.kind === "memory"
      ? result.data.text
      : result?.kind === "headline"
        ? result.data.headline.title
        : result?.data.task.text;
  return (
    <section className="ai-review" aria-label="Local agent evidence inspection">
      <h3>Saved evidence</h3>
      <form
        className="agent-form-grid"
        onSubmit={(e) => {
          e.preventDefault();
          if (selection) void inspect();
        }}
      >
        <label>
          Evidence type
          <select
            value={kind}
            onChange={(e) => {
              cancel();
              setKind(e.target.value as AgentEvidenceKind);
              setSelection("");
            }}
          >
            <option value="memory">Memory note</option>
            <option value="headline">Saved headline</option>
            <option value="reminder-run">Reminder run</option>
          </select>
        </label>
        <label>
          Saved record
          <select
            value={selection}
            required
            onChange={(e) => {
              cancel();
              setSelection(e.target.value);
            }}
          >
            <option value="">Select a record</option>
            {selection && !choices.some((c) => c.value === selection) && (
              <option value={selection} disabled>
                Selected evidence unavailable
              </option>
            )}
            {choices.map((c) => (
              <option key={c.value} value={c.value}>
                {c.label}
              </option>
            ))}
          </select>
        </label>
        <button
          className="secondary"
          disabled={busy || agents.busy || !selection}
        >
          Inspect evidence
        </button>
      </form>
      {(busy || result || error) && (
        <button type="button" className="secondary" onClick={cancel}>
          Cancel inspection
        </button>
      )}
      {busy && <p role="status">Inspecting saved evidence…</p>}
      {error && <p role="alert">{error}</p>}
      {result && !current && (
        <p role="status">Workspace changed. Inspect again.</p>
      )}
      {result && current && (
        <article aria-label="Inspected evidence">
          <p>
            {result.historical ? "Historical record" : "Current memory"} ·{" "}
            {result.freshness} · Untrusted data
          </p>
          <p>{text}</p>
          <p>
            Recorded {at(result.recordedAt)} · Inspected {at(result.observedAt)}
          </p>
          <details>
            <summary>Complete evidence response</summary>
            <pre>{JSON.stringify(result, null, 2)}</pre>
          </details>
        </article>
      )}
    </section>
  );
}
