import { useEffect, useRef, useState } from "react";
import {
  StrategyCompare,
  cloneStrategy,
  type Library,
  type Version,
} from "../vendor/wen-terminal/index.js";
import "../vendor/wen-terminal/style.css";
import "./ChatStrategyRevision.css";
import type { AgentView } from "../agents";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";

type RevisionReview = {
  schema: "wen-strategy-revision-review/1";
  profile: string;
  revision: number;
  libraryRevision: number;
  preparedAt: string;
  expiresAt: number;
  source: AgentView["messages"][number];
  sourceSha256: string;
  parent: Version;
  candidate: Version;
  witness: string;
};
type ProposalSource = { requestId: string; cardId: string };
type ProposalReview = Omit<
  RevisionReview,
  "schema" | "source" | "profile" | "revision" | "libraryRevision"
> & {
  schema: "wen-strategy-proposal-review/1" | "wen-strategy-proposal-review/2";
  input: {
    profile: string;
    revision: number;
    libraryRevision: number;
    requestId: string;
    cardId: string;
    name: string;
    parentDigest: string;
    changes?: string;
  };
  source: {
    requestId: string;
    sessionId: string;
    prompt: string;
    answer: string;
    context: string;
    disclosure: string;
    requestDigest: string;
    contextSha256: string;
    disclosureSha256: string;
    card: { id: string; text: string };
  };
  changes: unknown;
  amountsLamports: string[];
  entryConditions?: {
    metric: string;
    lookbackMinutes: number;
    operator: string;
    valueBps: string;
  }[];
  branchAmounts?: {
    index: number;
    when: {
      metric: string;
      lookbackMinutes: number;
      operator: string;
      valueBps: string;
    }[];
    amountsLamports: string[];
  }[];
};
type RevisionResult = {
  library: Library;
  agentRevision: number;
  receipt: AgentView["messages"][number];
};

export function ChatStrategyRevision({
  view,
  sourceId = "",
  proposalSource,
  onClose,
}: {
  view: AgentView;
  sourceId?: string;
  proposalSource?: ProposalSource;
  onClose: () => void;
}) {
  const core = useCore();
  const { agents } = useWorkspace();
  const [library, setLibrary] = useState<Library | null>(null);
  const [revision, setRevision] = useState(0);
  const [parent, setParent] = useState("");
  const [raw, setRaw] = useState("");
  const [name, setName] = useState("");
  const [review, setReview] = useState<RevisionReview | ProposalReview | null>(
    null,
  );
  const [saved, setSaved] = useState<RevisionResult | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const epoch = useRef(0);
  const panel = useRef<HTMLElement>(null);
  useEffect(() => {
    epoch.current++;
    setLibrary(null);
    setReview(null);
    setSaved(null);
    setRaw("");
    setName("");
    setError("");
    if (core.mode === "ready") void start();
    return () => {
      epoch.current++;
    };
  }, [
    view.profileId,
    sourceId,
    proposalSource?.requestId,
    proposalSource?.cardId,
    core.mode,
    core.instanceId,
  ]);
  useEffect(() => {
    if (core.mode === "ready") {
      panel.current?.scrollIntoView({ block: "start" });
      panel.current?.focus();
    }
  }, [core.mode, review]);
  if (core.mode !== "ready") return null;

  async function start() {
    const generation = epoch.current;
    setBusy(true);
    setReview(null);
    setSaved(null);
    setError("");
    setParent("");
    setRaw("");
    setRevision(view.revision);
    try {
      const result = await core.request<Library>(
        `wen/research?profile=${encodeURIComponent(view.profileId)}`,
      );
      if (generation === epoch.current) setLibrary(result);
    } catch (e) {
      if (generation === epoch.current) setError((e as Error).message);
    } finally {
      if (generation === epoch.current) setBusy(false);
    }
  }
  const prepare = async () => {
    if (!library) return;
    const generation = epoch.current;
    setBusy(true);
    setError("");
    try {
      const result = await core.request<RevisionReview | ProposalReview>(
        proposalSource
          ? "wen/research/proposal-review"
          : "wen/research/revision-review",
        "POST",
        {
          profile: view.profileId,
          revision,
          libraryRevision: library.revision,
          parentDigest: parent,
          ...(proposalSource
            ? {
                requestId: proposalSource.requestId,
                cardId: proposalSource.cardId,
                name,
                ...(raw ? { changes: raw } : {}),
              }
            : { sourceMessageId: sourceId, raw }),
        },
      );
      if (generation === epoch.current) setReview(result);
    } catch (e) {
      if (generation === epoch.current) setError((e as Error).message);
    } finally {
      if (generation === epoch.current) setBusy(false);
    }
  };
  const save = async () => {
    if (!review) return;
    const generation = epoch.current;
    setBusy(true);
    setError("");
    try {
      const result = await core.request<RevisionResult>(
        proposalSource
          ? "wen/research/proposal-save"
          : "wen/research/revision-save",
        "POST",
        review,
      );
      if (generation !== epoch.current) return;
      setSaved(result);
      setLibrary(result.library);
      try {
        await agents.refresh();
      } catch {
        if (generation === epoch.current)
          setError(
            "Version saved. Refresh Chat to inspect the source receipt.",
          );
      }
    } catch (e) {
      if (generation === epoch.current)
        setError(
          `${(e as Error).message}. Reconcile Mine and this conversation before preparing another revision.`,
        );
    } finally {
      if (generation === epoch.current) setBusy(false);
    }
  };
  const stale = !saved && revision !== view.revision;
  const reviewedSource =
    review &&
    ("input" in review ? review.source.card.text : review.source.text);
  const sourceProfile =
    review && ("input" in review ? review.input.profile : review.profile);
  const sourceIdentity =
    review &&
    ("input" in review
      ? `${review.source.requestId} / ${review.source.card.id}`
      : review.source.id);
  const candidate =
    saved?.library.versions.find(
      (v) => v.digest === review?.candidate.digest,
    ) ?? review?.candidate;
  return (
    <section
      className="wen-chat-revision"
      aria-label={
        proposalSource ? "AI strategy revision" : "Chat strategy revision"
      }
      ref={panel}
      tabIndex={-1}
    >
      <h4>{saved ? "Saved strategy revision" : "Strategy revision"}</h4>
      <span className="badge">
        {saved ? "SAVED · INERT" : "UNSAVED · NO EXECUTION"}
      </span>
      {error && <p role="alert">{error}</p>}
      {stale && (
        <p role="status">
          Workspace changed. Preserve the candidate JSON, then cancel and reopen
          the source.
        </p>
      )}
      {!review ? (
        <form
          className="agent-form"
          onSubmit={(e) => {
            e.preventDefault();
            void prepare();
          }}
        >
          <label>
            Original strategy
            <select
              aria-label="Original strategy"
              value={parent}
              disabled={busy}
              onChange={(e) => {
                const id = e.target.value;
                setParent(id);
                setError("");
                const version = library?.versions.find((v) => v.digest === id);
                try {
                  if (proposalSource) {
                    setRaw("");
                    return;
                  }
                  setRaw(
                    version
                      ? cloneStrategy(
                          version.raw,
                          `${version.name.slice(0, 60)} revision`,
                        )
                      : "",
                  );
                } catch (err) {
                  setError((err as Error).message);
                }
              }}
            >
              <option value="">Select saved version</option>
              {library?.versions.map((v) => (
                <option key={v.digest} value={v.digest}>
                  {v.name} · {v.digest.slice(0, 12)}
                </option>
              ))}
            </select>
          </label>
          {library?.versions.length === 0 && (
            <p>Save an original strategy in Mine first.</p>
          )}
          {proposalSource && (
            <label>
              Candidate name
              <input
                aria-label="Candidate name"
                required
                value={name}
                maxLength={80}
                disabled={busy}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
          )}
          <label>
            {proposalSource
              ? "Changes JSON (optional)"
              : "Candidate strategy JSON"}
            <textarea
              aria-label={
                proposalSource ? "Changes JSON" : "Candidate strategy JSON"
              }
              value={raw}
              onChange={(e) => setRaw(e.target.value)}
              spellCheck={false}
              disabled={busy}
            />
          </label>
          <div className="agent-inline">
            <button
              className="primary"
              disabled={
                busy ||
                stale ||
                !parent ||
                (proposalSource ? !name.trim() : !raw)
              }
            >
              Review strategy revision
            </button>
            <button
              className="secondary"
              type="button"
              disabled={busy}
              onClick={onClose}
            >
              Cancel revision
            </button>
          </div>
        </form>
      ) : (
        <>
          <dl className="wr-meta">
            <dt>Source profile / message</dt>
            <dd>
              {sourceProfile} / {sourceIdentity}
            </dd>
            <dt>Source SHA-256</dt>
            <dd>
              <code>{review.sourceSha256}</code>
            </dd>
            <dt>Original / candidate SHA-256</dt>
            <dd>
              <code>
                {review.parent.digest} / {review.candidate.digest}
              </code>
            </dd>
            <dt>Review expires</dt>
            <dd>{new Date(review.expiresAt * 1000).toLocaleTimeString()}</dd>
          </dl>
          <details>
            <summary>
              {proposalSource ? "Source strategy card" : "Source message"}
            </summary>
            <pre>{reviewedSource}</pre>
          </details>
          {"input" in review && (
            <>
              <details>
                <summary>Original AI disclosure</summary>
                <pre>{review.source.disclosure}</pre>
              </details>
              <details>
                <summary>Reviewed changes</summary>
                <pre>{JSON.stringify(review.changes, null, 2)}</pre>
              </details>
              {review.schema === "wen-strategy-proposal-review/2" && (
                <>
                  <h4>Entry conditions</h4>
                  {review.entryConditions?.length ? (
                    <p>
                      {review.entryConditions.map(conditionText).join(" AND ")}
                    </p>
                  ) : (
                    <p>None</p>
                  )}
                  <h4>Fallback allocation</h4>
                </>
              )}
              <RangeAllocations
                label={
                  review.schema === "wen-strategy-proposal-review/2"
                    ? "Fallback range allocations"
                    : "Compiled range allocations"
                }
                amounts={review.amountsLamports}
              />
              {review.branchAmounts?.map((branch) => (
                <section
                  key={branch.index}
                  aria-label={`Rule ${branch.index + 1}`}
                >
                  <h4>Rule {branch.index + 1}</h4>
                  <p>{branch.when.map(conditionText).join(" AND ")}</p>
                  <RangeAllocations
                    label={`Rule ${branch.index + 1} range allocations`}
                    amounts={branch.amountsLamports}
                  />
                </section>
              ))}
            </>
          )}
          <div className="wt wr">
            <StrategyCompare
              versions={[review.parent, candidate!]}
              history={library?.bundles ?? []}
            />
          </div>
          <details>
            <summary>Original strategy JSON</summary>
            <pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
              {review.parent.raw}
            </pre>
          </details>
          <details>
            <summary>Reviewed candidate JSON</summary>
            <pre
              aria-label="Reviewed candidate JSON"
              style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
            >
              {review.candidate.raw}
            </pre>
          </details>
          {saved ? (
            <p role="status">
              Saved in Mine with a source receipt in this conversation.
            </p>
          ) : null}
          <div className="agent-inline">
            {!saved && (
              <>
                <button
                  className="primary"
                  disabled={
                    busy || stale || review.expiresAt * 1000 <= Date.now()
                  }
                  onClick={() => void save()}
                >
                  Save revised version
                </button>
                <button
                  className="secondary"
                  disabled={busy}
                  onClick={() => {
                    setReview(null);
                    setError("");
                  }}
                >
                  Edit candidate
                </button>
              </>
            )}
            <button className="secondary" disabled={busy} onClick={onClose}>
              {saved ? "Close revision" : "Cancel revision"}
            </button>
          </div>
        </>
      )}
    </section>
  );
}

export function RangeAllocations({
  label,
  amounts,
}: {
  label: string;
  amounts: string[];
}) {
  return (
    <table aria-label={label}>
      <thead>
        <tr>
          <th>Range</th>
          <th>SOL</th>
          <th>Lamports</th>
        </tr>
      </thead>
      <tbody>
        {amounts.map((value, i) => {
          const n = BigInt(value);
          const sol = `${n / 1000000000n}.${(n % 1000000000n).toString().padStart(9, "0")}`;
          return (
            <tr key={i}>
              <td>{String.fromCharCode(65 + i)}</td>
              <td>{sol}</td>
              <td>{value}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function conditionText(c: {
  metric: string;
  lookbackMinutes: number;
  operator: string;
  valueBps: string;
}) {
  return `${c.metric === "returnBps" ? "Return" : "Volatility"} ${c.lookbackMinutes}m ${c.operator === "gte" ? "≥" : "≤"} ${c.valueBps} bps`;
}
