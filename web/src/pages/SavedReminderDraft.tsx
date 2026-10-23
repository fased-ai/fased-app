import { useEffect, useRef, useState } from "react";
import type { AgentView, TaskReminderReview } from "../agents";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";

export function SavedReminderDraft({
  view,
  sourceID,
  close,
}: {
  view: AgentView;
  sourceID: string;
  close: () => void;
}) {
  const core = useCore();
  const { agents } = useWorkspace();
  const [draft, setDraft] = useState<TaskReminderReview>();
  const [review, setReview] = useState<TaskReminderReview>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [reload, setReload] = useState(0);
  const pendingReview = useRef(false);
  useEffect(() => {
    let active = true;
    setDraft(undefined);
    setReview(undefined);
    pendingReview.current = false;
    setError("");
    setLoading(true);
    const q = new URLSearchParams({
      profile: view.profileId,
      "reminder-draft": sourceID,
    });
    void core
      .request<TaskReminderReview>(`agents?${q}`)
      .then((v) => {
        if (active) setDraft(v);
      })
      .catch((e: unknown) => {
        if (active)
          setError(e instanceof Error ? e.message : "Reminder unavailable");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [core.request, view.profileId, sourceID, reload]);
  useEffect(() => {
    const v = view.taskReminderReview;
    if (
      pendingReview.current &&
      v?.source.id === sourceID &&
      v.profileId === view.profileId
    ) {
      setReview(v);
      pendingReview.current = false;
    }
  }, [view.taskReminderReview, sourceID, view.profileId]);
  const stale = !!draft && draft.revision !== view.revision;
  const blocked = agents.busy || loading || stale || !draft;
  const matching =
    draft &&
    review &&
    review.revision === view.revision &&
    review.expiresAt * 1000 > Date.now() &&
    review.sourceSha256 === draft.sourceSha256 &&
    review.name === draft.name &&
    review.text === draft.text &&
    review.intervalSeconds === draft.intervalSeconds &&
    review.maxRuns === draft.maxRuns;
  const command = draft && {
    id: sourceID,
    revision: draft.revision,
    name: draft.name,
    text: draft.text,
    intervalSeconds: draft.intervalSeconds,
    maxRuns: draft.maxRuns,
  };
  function edit(values: Partial<TaskReminderReview>) {
    if (draft) setDraft({ ...draft, ...values });
    setReview(undefined);
    pendingReview.current = false;
  }
  return (
    <section className="panel" aria-label="Reuse reminder">
      <div className="agent-record-body">
        <h3>Reuse reminder</h3>
        {loading && <p role="status">Loading saved reminder…</p>}
        {error && <p role="alert">{error}</p>}
        {stale && (
          <p role="alert">
            Records changed. Reload the reminder to review again.
          </p>
        )}
        <button
          className="text-button"
          disabled={agents.busy || loading}
          onClick={() => setReload((v) => v + 1)}
        >
          Reload reminder
        </button>
        {draft && (
          <>
            <details>
              <summary>Saved reminder source</summary>
              <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
                {JSON.stringify(
                  { source: draft.source, sha256: draft.sourceSha256 },
                  null,
                  2,
                )}
              </pre>
            </details>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                if (!blocked && command) {
                  pendingReview.current = true;
                  void agents
                    .change({ ...command, action: "task-reminder-review" })
                    .then((ok) => {
                      if (!ok) pendingReview.current = false;
                    });
                }
              }}
            >
              <fieldset disabled={blocked} style={{ minWidth: 0 }}>
                <label>
                  Reminder name
                  <input
                    required
                    maxLength={80}
                    value={draft.name}
                    onChange={(e) => edit({ name: e.target.value })}
                  />
                </label>
                <label>
                  Reminder text
                  <textarea
                    required
                    maxLength={4000}
                    value={draft.text}
                    onChange={(e) => edit({ text: e.target.value })}
                  />
                </label>
                <div className="agent-grid">
                  <label>
                    Interval in seconds
                    <input
                      type="number"
                      min={60}
                      max={604800}
                      required
                      value={draft.intervalSeconds}
                      onChange={(e) =>
                        edit({ intervalSeconds: Number(e.target.value) })
                      }
                    />
                  </label>
                  <label>
                    Maximum evaluations
                    <input
                      type="number"
                      min={1}
                      max={20}
                      required
                      value={draft.maxRuns}
                      onChange={(e) =>
                        edit({ maxRuns: Number(e.target.value) })
                      }
                    />
                  </label>
                </div>
                <button className="secondary">Review new reminder</button>
              </fieldset>
            </form>
            {matching && review && (
              <div aria-label="New reminder review">
                <h4>{review.name}</h4>
                <p>{review.text}</p>
                <p>
                  Every {review.intervalSeconds}s · up to {review.maxRuns}{" "}
                  evaluations
                </p>
                <p>
                  Review expires{" "}
                  {new Date(review.expiresAt * 1000).toLocaleString()}
                </p>
                <button
                  className="primary"
                  disabled={blocked}
                  onClick={async () => {
                    if (
                      command &&
                      (await agents.change({
                        ...command,
                        action: "task-reminder-create",
                        consentDigest: review.digest,
                        expiresAt: review.expiresAt,
                      }))
                    )
                      close();
                  }}
                >
                  Create reviewed reminder
                </button>
              </div>
            )}
          </>
        )}
        <button className="text-button" onClick={close}>
          Discard reminder draft
        </button>
      </div>
    </section>
  );
}
