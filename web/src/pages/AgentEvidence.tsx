import { useUI } from "../ui-preferences";
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
  onUse,
}: {
  view: AgentView;
  sessionId: string;
  onUse?: (evidence: AgentEvidenceView) => void;
}) {
  return (
    <EvidenceForm
      key={`${view.profileId}:${sessionId}`}
      view={view}
      onUse={onUse}
    />
  );
}
function EvidenceForm({
  view,
  onUse,
}: {
  view: AgentView;
  onUse?: (evidence: AgentEvidenceView) => void;
}) {
  const { t: uiText } = useUI();
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
    <section
      className="ai-review"
      aria-label={uiText("Local agent evidence inspection")}
    >
      <h3>{uiText("Sources")}</h3>
      <div className="agent-form-grid">
        <label>
          {uiText("Evidence type")}
          <select
            value={kind}
            onChange={(e) => {
              cancel();
              setKind(e.target.value as AgentEvidenceKind);
              setSelection("");
            }}
          >
            <option value="memory">{uiText("Fact")}</option>
            <option value="headline">{uiText("Saved headline")}</option>
            <option value="reminder-run">{uiText("Reminder run")}</option>
          </select>
        </label>
        <label>
          {uiText("Saved record")}
          <select
            value={selection}
            onChange={(e) => {
              cancel();
              setSelection(e.target.value);
            }}
          >
            <option value="">{uiText("Select a record")}</option>
            {selection && !choices.some((c) => c.value === selection) && (
              <option value={selection} disabled>
                {uiText("Selected evidence unavailable")}
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
          type="button"
          className="secondary"
          disabled={busy || agents.busy || !selection}
          onClick={() => void inspect()}
        >
          {uiText("Inspect evidence")}
        </button>
      </div>
      {(busy || result || error) && (
        <button type="button" className="secondary" onClick={cancel}>
          {uiText("Cancel inspection")}
        </button>
      )}
      {busy && <p role="status">{uiText("Inspecting saved evidence…")}</p>}
      {error && <p role="alert">{error}</p>}
      {result && !current && (
        <p role="status">{uiText("Workspace changed. Inspect again.")}</p>
      )}
      {result && current && (
        <article aria-label={uiText("Inspected evidence")}>
          <p>
            {result.historical
              ? uiText("Historical record")
              : uiText("Current memory")}{" "}
            · {result.freshness}
            {uiText(" · Untrusted data")}
          </p>
          <p>{text}</p>
          <p>
            {uiText("Recorded ")}
            {at(result.recordedAt)}
            {uiText(" · Inspected ")}
            {at(result.observedAt)}
          </p>
          {onUse && (
            <button
              type="button"
              className="secondary"
              disabled={
                agents.busy ||
                (result.kind === "memory" && result.freshness === "expired")
              }
              onClick={() => onUse(result)}
            >
              {uiText("Use in AI review")}
            </button>
          )}
          <details>
            <summary>{uiText("Complete evidence response")}</summary>
            <pre>{JSON.stringify(result, null, 2)}</pre>
          </details>
        </article>
      )}
    </section>
  );
}
