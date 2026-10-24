import { useState } from "react";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";
import { Badge } from "../components/shared";
import type {
  AgentView,
  AgentPlaybookDraft,
  PlaybookReview,
  PlaybookVersion,
} from "../agents";

const fixtureFeed = "https://news.fased.invalid/headlines.xml";
const defaults: PlaybookVersion = {
  version: 1,
  name: "Morning digest",
  text: "Summarize the selected fixture headlines and identify uncertainties.",
  newsSourceId: "",
  newsMaxAgeSeconds: 3600,
  limits: {
    maxInputTokens: 24000,
    maxOutputTokens: 1024,
    deadlineSeconds: 3,
    budgetMicrousd: 30000,
  },
  createdAt: 0,
};
export function AgentPlaybooks({ view: v }: { view: AgentView }) {
  const core = useCore();
  const { agents, navigate } = useWorkspace();
  const [editing, setEditing] = useState<string>();
  const [form, setForm] = useState(defaults);
  const [versions, setVersions] = useState<Record<string, number>>({});
  const [draft, setDraft] = useState<AgentPlaybookDraft>();
  const [session, setSession] = useState("");
  const [review, setReview] = useState<PlaybookReview>();
  const [approved, setApproved] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const busy = loading || agents.busy;
  const stale =
    !!review &&
    (review.revision !== v.revision || review.expiresAt * 1000 <= Date.now());
  const books = v.playbooks ?? [];
  const sources = v.newsSources.filter(
    (s) => !s.disabled && s.url === fixtureFeed,
  );
  const runs = v.tasks.filter((t) => t.playbook);
  async function load(id: string, version: number, edit: boolean) {
    setLoading(true);
    setError("");
    setReview(undefined);
    setApproved(false);
    try {
      const q = new URLSearchParams({
        profile: v.profileId,
        "playbook-draft": id,
        "playbook-version": String(version),
      });
      const d = await core.request<AgentPlaybookDraft>(`agents?${q}`);
      if (edit) {
        setEditing(id);
        setForm(d.selected);
        setDraft(undefined);
      } else {
        setDraft(d);
        setSession("");
        setEditing(undefined);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Playbook unavailable");
    } finally {
      setLoading(false);
    }
  }
  async function prepare() {
    if (!draft || !session) return;
    setLoading(true);
    setError("");
    setApproved(false);
    setReview(undefined);
    try {
      const result = await core.request<AgentView>("agents", "POST", {
        ...draft.command,
        sessionId: session,
        revision: v.revision,
      });
      if (!result.playbookReview) throw Error("Playbook review unavailable");
      setReview(result.playbookReview);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Review unavailable");
    } finally {
      setLoading(false);
    }
  }
  return (
    <section className="panel" aria-label="Agent playbooks">
      <div className="panel-heading">
        <h2>Playbooks</h2>
        <Badge>FIXTURE</Badge>
      </div>
      <div className="agent-record-body">
        {error && <p role="alert">{error}</p>}
        <div className="agent-inline">
          <button
            className="secondary"
            disabled={busy}
            onClick={() => {
              setEditing("");
              setDraft(undefined);
              setReview(undefined);
              setError("");
              setForm({ ...defaults, newsSourceId: sources[0]?.id ?? "" });
            }}
          >
            New playbook
          </button>
          {v.aiFixtureAvailable && sources.length === 0 && (
            <button
              className="secondary"
              disabled={busy}
              onClick={() =>
                void agents.change({
                  action: "news-source-add",
                  name: "Fixture headlines",
                  source: fixtureFeed,
                  newsMaxAgeSeconds: 3600,
                })
              }
            >
              Add fixture news source
            </button>
          )}
          {v.aiFixtureAvailable && sources.length > 0 && (
            <button
              className="secondary"
              disabled={
                busy ||
                v.newsFetches.some(
                  (f) =>
                    f.sourceId ===
                      (draft?.selected.newsSourceId ||
                        form.newsSourceId ||
                        sources[0].id) &&
                    (f.state === "queued" || f.state === "fetching"),
                )
              }
              onClick={() =>
                void agents.change({
                  action: "news-fetch",
                  id:
                    draft?.selected.newsSourceId ||
                    form.newsSourceId ||
                    sources[0].id,
                })
              }
            >
              Fetch fixture headlines
            </button>
          )}
          <a href="#agents/news">News inputs</a>
        </div>
        {books.map((p) => {
          const n = versions[p.id] ?? p.versions.length;
          const selected = p.versions[n - 1];
          return (
            <article className="memory-card" key={p.id}>
              <h3>{selected.name}</h3>
              <label>
                Version
                <select
                  aria-label={`Version of ${selected.name}`}
                  value={n}
                  disabled={busy}
                  onChange={(e) => {
                    setVersions({
                      ...versions,
                      [p.id]: Number(e.target.value),
                    });
                    setDraft(undefined);
                    setReview(undefined);
                  }}
                >
                  {p.versions.map((item) => (
                    <option key={item.version} value={item.version}>
                      v{item.version} ·{" "}
                      {new Date(item.createdAt * 1000).toLocaleString()}
                    </option>
                  ))}
                </select>
              </label>
              <p>{selected.text}</p>
              <div className="agent-inline">
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => void load(p.id, n, false)}
                >
                  Review: {selected.name}
                </button>
                <button
                  className="text-button"
                  disabled={busy}
                  onClick={() => void load(p.id, n, true)}
                >
                  Edit: {selected.name}
                </button>
              </div>
            </article>
          );
        })}
        {editing !== undefined && (
          <form
            className="agent-form-grid"
            aria-label="Playbook settings"
            onSubmit={async (e) => {
              e.preventDefault();
              const ok = await agents.change({
                action: "playbook-save",
                id: editing || undefined,
                name: form.name,
                text: form.text,
                newsSourceId: form.newsSourceId,
                newsMaxAgeSeconds: form.newsMaxAgeSeconds,
                limits: form.limits,
              });
              if (ok) setEditing(undefined);
            }}
          >
            <label>
              Playbook name
              <input
                required
                maxLength={80}
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </label>
            <label>
              Digest prompt
              <textarea
                required
                maxLength={1000}
                value={form.text}
                onChange={(e) => setForm({ ...form, text: e.target.value })}
              />
            </label>
            <label>
              Fixture news source
              <select
                required
                value={form.newsSourceId}
                onChange={(e) =>
                  setForm({ ...form, newsSourceId: e.target.value })
                }
              >
                <option value="">Select source</option>
                {sources.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Maximum news age (seconds)
              <input
                type="number"
                required
                min={60}
                max={86400}
                value={form.newsMaxAgeSeconds}
                onChange={(e) =>
                  setForm({
                    ...form,
                    newsMaxAgeSeconds: e.target.valueAsNumber,
                  })
                }
              />
            </label>
            {(
              [
                ["maxInputTokens", "Input token limit", 1000, 32000],
                ["maxOutputTokens", "Output token limit", 128, 1024],
                ["deadlineSeconds", "Request deadline (seconds)", 1, 5],
                ["budgetMicrousd", "Fixture budget (micro USD)", 1, 1000000],
              ] as const
            ).map(([key, label, min, max]) => (
              <label key={key}>
                {label}
                <input
                  type="number"
                  required
                  min={min}
                  max={max}
                  value={form.limits[key]}
                  onChange={(e) =>
                    setForm({
                      ...form,
                      limits: { ...form.limits, [key]: e.target.valueAsNumber },
                    })
                  }
                />
              </label>
            ))}
            <div className="agent-inline">
              <button className="primary" disabled={busy || !sources.length}>
                {editing ? "Save new version" : "Save playbook"}
              </button>
              <button
                type="button"
                className="text-button"
                disabled={busy}
                onClick={() => setEditing(undefined)}
              >
                Cancel settings
              </button>
            </div>
          </form>
        )}
        {draft && (
          <section aria-label="Review playbook run">
            <h3>
              {draft.selected.name} · v{draft.selected.version}
            </h3>
            <p>
              {draft.source.name} · {draft.source.url}
            </p>
            <p>
              One fixture run · news age ≤ {draft.selected.newsMaxAgeSeconds}s
            </p>
            <label>
              Digest conversation
              <select
                value={session}
                disabled={busy}
                onChange={(e) => {
                  setSession(e.target.value);
                  setReview(undefined);
                  setApproved(false);
                }}
              >
                <option value="">Select conversation</option>
                {v.sessions.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </label>
            {!v.sessions.length && (
              <button
                className="secondary"
                disabled={busy}
                onClick={() =>
                  void agents.change({
                    action: "session-create",
                    name: "News digest",
                  })
                }
              >
                Create digest conversation
              </button>
            )}
            <div className="agent-inline">
              <button
                className="secondary"
                disabled={busy || !session}
                onClick={() => void prepare()}
              >
                Prepare run review
              </button>
              <button
                className="text-button"
                disabled={busy}
                onClick={() => {
                  setDraft(undefined);
                  setReview(undefined);
                  setApproved(false);
                }}
              >
                Cancel run review
              </button>
            </div>
            {review && (
              <>
                {stale && (
                  <p role="alert">
                    Review expired or records changed. Prepare the run review
                    again.
                  </p>
                )}
                <p>
                  Current fixture preview · retrieved{" "}
                  {new Date(review.news.fetchedAt * 1000).toLocaleString()}
                </p>
                <ul>
                  {review.news.items.map((item) => (
                    <li key={item.id}>{item.title}</li>
                  ))}
                </ul>
                <p>
                  The run fetches fresh fixture headlines under this source
                  policy. Headlines are untrusted data.
                </p>
                <details>
                  <summary>Context, limits and source evidence</summary>
                  <pre
                    style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                  >
                    {JSON.stringify(
                      {
                        playbook: review.task.playbook,
                        context: review.task.ai?.disclosure,
                        limits: review.task.ai?.limits,
                        budgetMicrousd: review.task.ai?.budgetMicrousd,
                        news: review.news,
                        reviewExpiresAt: review.expiresAt,
                        digest: review.digest,
                      },
                      null,
                      2,
                    )}
                  </pre>
                </details>
                <label className="ai-consent">
                  <input
                    type="checkbox"
                    checked={approved}
                    disabled={busy || stale || !v.aiFixtureAvailable}
                    onChange={(e) => setApproved(e.target.checked)}
                  />
                  Approve this context, source policy and one fixture run
                </label>
                <button
                  className="primary"
                  disabled={busy || stale || !approved || !v.aiFixtureAvailable}
                  onClick={async () => {
                    if (await agents.change(review.command)) {
                      setReview(undefined);
                      setDraft(undefined);
                      setApproved(false);
                    }
                  }}
                >
                  Run fixture digest once
                </button>
              </>
            )}
          </section>
        )}
        {runs.map((task) => {
          const request = v.aiRequests.find((q) => q.taskId === task.id);
          const run = v.runs.find((r) => r.taskId === task.id);
          return (
            <article
              className="memory-card"
              key={task.id}
              aria-label={`Digest run ${task.name}`}
            >
              <h3>
                {task.name} · v{task.playbook!.version}
              </h3>
              <p>
                {task.state} · {task.runs}/1 runs
              </p>
              {request?.answer && (
                <p style={{ whiteSpace: "pre-wrap" }}>{request.answer}</p>
              )}
              {request?.failure && <p role="alert">{request.failure}</p>}
              {run?.news && (
                <details>
                  <summary>Run source evidence</summary>
                  <pre
                    style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                  >
                    {JSON.stringify(run.news, null, 2)}
                  </pre>
                </details>
              )}
              {run && (
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={async () => {
                    if (
                      await agents.change({
                        action: "task-run-explain",
                        id: run.id,
                      })
                    )
                      navigate("agents/chat");
                  }}
                >
                  Discuss digest
                </button>
              )}
            </article>
          );
        })}
      </div>
    </section>
  );
}
