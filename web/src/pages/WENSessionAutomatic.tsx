import { useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";
import { Badge } from "../components/shared";
import "./WENSessionRehearsal.css";
import "./WENSessionAutomatic.css";

type Version = { digest: string; raw: string; name: string };
type Library = { revision: number; versions: Version[] };
type Scenario = {
  id: string;
  digest: string;
  startsAt: string;
  expiresAt: string;
  hours: number;
};
type Pool = { id: string; revision: number; budget: Record<string, string> };
type Run = {
  name: string;
  source: Version;
  scenarioId: string;
  scenarioDigest: string;
  cursor: number;
  clockAt: string;
  deadline: number;
  reason: string;
  completionReason: string;
  stopRequested: boolean;
  consentHistory: { id: string; revokedAt: number | null }[];
  operations: { id: string; status: string }[];
  decisions: {
    record: {
      decisionId: string;
      roundId: string;
      action: string;
      reason: string;
      amountsLamports: string[] | null;
    };
  }[];
  record: {
    sessionId: string;
    status: string;
    walletId: string;
    budget: Record<string, string>;
  };
};
type View = {
  profile: string;
  revision: number;
  libraryRevision: number;
  capability: { enabled: boolean };
  runs: Run[];
  pools: Pool[];
};
type Catalog = { scenarios: Scenario[]; poolOptions: string[] };
type Review = {
  source: Version;
  scenario: Scenario;
  pool: Pool;
  expiresAt: number;
  runDeadline: number | null;
  maxWallSeconds: number;
};
const iso = (at: number) => new Date(at * 1000).toISOString();

export function WENSessionAutomatic() {
  const core = useCore();
  const { state, agents } = useWorkspace();
  const profile = state.selectedProfile;
  const [view, setView] = useState<View | null>(null);
  const [library, setLibrary] = useState<Library | null>(null);
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [digest, setDigest] = useState("");
  const [name, setName] = useState("Automatic rehearsal");
  const [scenario, setScenario] = useState("fixture-4h");
  const [pool, setPool] = useState("fixture-wallet:alpha");
  const [draft, setDraft] = useState("");
  const [review, setReview] = useState<Review | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [connected, setConnected] = useState(true);
  const [now, setNow] = useState(Date.now());
  const generation = useRef(0);
  const readSequence = useRef(0);
  const polling = useRef(false);
  const current = library?.versions.find((v) => v.digest === digest);
  const book = catalog?.scenarios.find((s) => s.id === scenario);
  const clear = () => {
    setDraft("");
    setReview(null);
  };
  const load = async (ticket = generation.current) => {
    const sequence = ++readSequence.current;
    const result = await Promise.all([
      core.request<View>(
        `wen/session-runs?profile=${encodeURIComponent(profile)}`,
      ),
      core.request<Library>(
        `wen/research?profile=${encodeURIComponent(profile)}`,
      ),
      core.request<Catalog>(
        `wen/sessions?profile=${encodeURIComponent(profile)}`,
      ),
    ]).catch((e: unknown) => {
      if (ticket !== generation.current || sequence !== readSequence.current)
        return null;
      throw e;
    });
    if (
      !result ||
      ticket !== generation.current ||
      sequence !== readSequence.current
    )
      return;
    const [v, l, c] = result;
    setView(v);
    setLibrary(l);
    setCatalog(c);
    setConnected(true);
  };
  useEffect(() => {
    const ticket = ++generation.current;
    setView(null);
    setLibrary(null);
    setCatalog(null);
    setDigest("");
    setBusy(false);
    setError("");
    setConnected(true);
    clear();
    if (core.mode === "ready")
      void load(ticket).catch((e) => {
        if (ticket === generation.current) {
          setError(String(e));
          setConnected(false);
        }
      });
    return () => {
      generation.current++;
    };
  }, [profile, core.mode, core.instanceId]);
  useEffect(() => {
    if (core.mode !== "ready") return;
    const ticket = generation.current;
    const timer = window.setInterval(() => {
      setNow(Date.now());
      // A failed read keeps the last-known record. Reconnection is explicit.
      if (connected && !busy && !review && !draft && !polling.current) {
        polling.current = true;
        void load(ticket)
          .catch(() => {
            if (ticket === generation.current) setConnected(false);
          })
          .finally(() => {
            polling.current = false;
          });
      }
    }, 1000);
    return () => window.clearInterval(timer);
  }, [profile, core.mode, core.instanceId, connected, busy, review, draft]);
  const perform = async (fn: (ticket: number) => Promise<void>) => {
    const ticket = generation.current;
    setBusy(true);
    setError("");
    try {
      await fn(ticket);
      if (ticket === generation.current) await agents.refresh();
    } catch (e) {
      if (ticket === generation.current) {
        setError(String(e));
        clear();
        await load(ticket).catch(() => {
          if (ticket === generation.current) setConnected(false);
        });
      }
    } finally {
      if (ticket === generation.current) setBusy(false);
    }
  };
  const post = async (action: string, body: object, ticket: number) => {
    const v = await core.request<View>(
      `wen/session-runs/${action}`,
      "POST",
      body,
    );
    if (ticket === generation.current) {
      setView(v);
      clear();
      await load(ticket);
    }
  };
  const prepare = async (r: Run | null, ticket: number) => {
    if (!view || !library || !book) return;
    const result = await core.request<Review>(
      "wen/session-runs/review",
      "POST",
      {
        schema: "wen-fixture-run-prepare/1",
        profile,
        revision: view.revision,
        libraryRevision: library.revision,
        strategyDigest: r?.source.digest ?? digest,
        name: r?.name ?? name,
        scenarioId: r?.scenarioId ?? scenario,
        scenarioDigest: r?.scenarioDigest ?? book.digest,
        fixtureWalletId: r?.record.walletId ?? pool,
        runId: r?.record.sessionId ?? null,
      },
    );
    if (ticket === generation.current) {
      setReview(result);
      setDraft("");
    }
  };
  const control = (
    r: Run,
    action: string,
    ticket: number,
    decisionId: string | null = null,
  ) =>
    post(
      "control",
      {
        schema: "wen-fixture-run-control/1",
        profile,
        revision: view!.revision,
        poolRevision: view!.pools.find((p) => p.id === r.record.walletId)!
          .revision,
        runId: r.record.sessionId,
        action,
        decisionId,
      },
      ticket,
    );
  const disabled = busy || !connected;
  return (
    <section
      className="panel f4-rehearsal f4-automatic"
      aria-label="Automatic fixture runs"
    >
      <div className="panel-heading">
        <h2>Automatic fixture runs</h2>
        <Badge>FIXTURE</Badge>
      </div>
      {core.mode !== "ready" ? (
        <p>Private core required</p>
      ) : (
        <div className="agent-record-body">
          {error && <p role="alert">{error}</p>}
          {!connected && <p role="status">Disconnected · last-known records</p>}
          <button
            className="secondary"
            disabled={busy}
            onClick={() => {
              clear();
              void perform((ticket) => load(ticket));
            }}
          >
            Reload fixture runs
          </button>
          {view && library && catalog && (
            <>
              <p role="status">
                {view.capability.enabled
                  ? "Fixture capability enabled"
                  : "Fixture capability disabled"}
              </p>
              <div className="f4-composer">
                <label>
                  Strategy
                  <select
                    aria-label="Automatic strategy"
                    value={digest}
                    disabled={disabled}
                    onChange={(e) => {
                      setDigest(e.target.value);
                      clear();
                    }}
                  >
                    <option value="">Select saved version</option>
                    {library.versions.map((v) => (
                      <option key={v.digest} value={v.digest}>
                        {v.name} · {v.digest.slice(0, 8)}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Name
                  <input
                    aria-label="Automatic run name"
                    value={name}
                    maxLength={80}
                    disabled={disabled}
                    onChange={(e) => {
                      setName(e.target.value);
                      clear();
                    }}
                  />
                </label>
                <label>
                  Scenario
                  <select
                    aria-label="Automatic scenario"
                    value={scenario}
                    disabled={disabled}
                    onChange={(e) => {
                      setScenario(e.target.value);
                      clear();
                    }}
                  >
                    {catalog.scenarios.map((s) => (
                      <option key={s.id}>{s.id}</option>
                    ))}
                  </select>
                </label>
                <label>
                  Shadow pool
                  <select
                    aria-label="Automatic shadow pool"
                    value={pool}
                    disabled={disabled}
                    onChange={(e) => {
                      setPool(e.target.value);
                      clear();
                    }}
                  >
                    {catalog.poolOptions.map((p) => (
                      <option key={p}>{p}</option>
                    ))}
                  </select>
                </label>
              </div>
              {current && book && (
                <>
                  <details>
                    <summary>Saved automatic source</summary>
                    <pre>{current.raw}</pre>
                  </details>
                  <div className="agent-inline">
                    <button
                      className="secondary"
                      disabled={
                        disabled || !name.trim() || !view.capability.enabled
                      }
                      onClick={() => {
                        const next = JSON.parse(current.raw);
                        next.name = name;
                        next.execution = {
                          mode: "bounded-auto",
                          runner: "fased",
                          mandateId: null,
                        };
                        next.schedule.startsAt = book.startsAt;
                        next.schedule.expiresAt = book.expiresAt;
                        clear();
                        setDraft(JSON.stringify(next, null, 2));
                      }}
                    >
                      Review automatic draft
                    </button>
                    <button
                      className="primary"
                      disabled={
                        disabled || !name.trim() || !view.capability.enabled
                      }
                      onClick={() =>
                        void perform((ticket) => prepare(null, ticket))
                      }
                    >
                      Review fixture run
                    </button>
                  </div>
                </>
              )}
              {draft && (
                <section aria-label="Automatic strategy draft">
                  <h3>Automatic strategy draft</h3>
                  <pre>{draft}</pre>
                  <div className="agent-inline">
                    <button
                      className="primary"
                      disabled={disabled}
                      onClick={() =>
                        void perform(async (ticket) => {
                          const saved = await core.request<Library>(
                            "wen/research",
                            "POST",
                            {
                              profile,
                              revision: library.revision,
                              action: "save",
                              raw: draft,
                            },
                          );
                          if (ticket === generation.current) {
                            const exact = saved.versions.find(
                              (v) => v.raw === draft,
                            );
                            setLibrary(saved);
                            if (exact) setDigest(exact.digest);
                            clear();
                            await load(ticket);
                          }
                        })
                      }
                    >
                      Save automatic draft
                    </button>
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={clear}
                    >
                      Cancel automatic draft
                    </button>
                  </div>
                </section>
              )}
              {review && (
                <section aria-label="Fixture run review">
                  <h3>Fixture run review</h3>
                  <dl>
                    <dt>Scenario playback</dt>
                    <dd>
                      {review.scenario.hours}h · {review.scenario.startsAt} —{" "}
                      {review.scenario.expiresAt}
                    </dd>
                    <dt>Wall-clock maximum</dt>
                    <dd>{review.maxWallSeconds} seconds</dd>
                    <dt>Original deadline</dt>
                    <dd>
                      {review.runDeadline === null
                        ? "Set by first Start"
                        : iso(review.runDeadline)}
                    </dd>
                    <dt>Review expires</dt>
                    <dd>{iso(review.expiresAt)}</dd>
                    <dt>Shadow pool</dt>
                    <dd>{review.pool.id}</dd>
                    <dt>Available lamports</dt>
                    <dd>{review.pool.budget.availableLamports}</dd>
                  </dl>
                  <details>
                    <summary>Complete fixture run review</summary>
                    <pre>{JSON.stringify(review, null, 2)}</pre>
                  </details>
                  <div className="agent-inline">
                    <button
                      className="primary"
                      disabled={disabled || now >= review.expiresAt * 1000}
                      onClick={() =>
                        void perform((ticket) => post("start", review, ticket))
                      }
                    >
                      Start fixture run
                    </button>
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={() => setReview(null)}
                    >
                      Cancel run review
                    </button>
                  </div>
                </section>
              )}
              <div className="f4-runs">
                {view.runs.map((r) => {
                  const id = r.record.sessionId;
                  const pending = r.operations.find(
                    (op) => op.status !== "reconciled",
                  );
                  const recoveryEvent = pending
                    ? `${pending.status === "reserved" && r.scenarioId === "fixture-4h-unknown" ? "uncertain" : "resolve"}:${pending.id}`
                    : null;
                  const terminal = r.record.status === "complete";
                  return (
                    <article
                      key={id}
                      className="f4-session"
                      aria-label={`Automatic run ${r.name}`}
                    >
                      <h3>{r.name}</h3>
                      <p data-testid={`run-status-${id}`}>
                        {r.record.status} · {r.reason}
                      </p>
                      <dl>
                        <dt>Original wall deadline</dt>
                        <dd>{iso(r.deadline)}</dd>
                        <dt>Fixture clock</dt>
                        <dd>{r.clockAt}</dd>
                        <dt>Cursor</dt>
                        <dd data-testid={`run-cursor-${id}`}>{r.cursor}</dd>
                        <dt>Accepted operations</dt>
                        <dd data-testid={`run-accepted-${id}`}>
                          {r.operations.length}
                        </dd>
                        <dt>Shadow pool</dt>
                        <dd>{r.record.walletId}</dd>
                        <dt>Available lamports</dt>
                        <dd data-testid={`run-available-${id}`}>
                          {r.record.budget.availableLamports}
                        </dd>
                      </dl>
                      <div className="agent-inline">
                        {!terminal && (
                          <>
                            <button
                              className="secondary"
                              disabled={
                                disabled || r.record.status === "paused"
                              }
                              onClick={() =>
                                void perform((ticket) =>
                                  control(r, "pause", ticket),
                                )
                              }
                            >
                              Pause fixture run
                            </button>
                            <button
                              className="secondary"
                              disabled={disabled || r.stopRequested}
                              onClick={() =>
                                void perform((ticket) =>
                                  control(r, "stop", ticket),
                                )
                              }
                            >
                              Stop fixture run
                            </button>
                            <button
                              className="secondary"
                              disabled={
                                disabled ||
                                !view.capability.enabled ||
                                !!pending ||
                                r.stopRequested ||
                                now >= r.deadline * 1000 ||
                                !["paused", "held"].includes(r.record.status)
                              }
                              onClick={() =>
                                void perform((ticket) => prepare(r, ticket))
                              }
                            >
                              Review resume
                            </button>
                          </>
                        )}
                        {pending && recoveryEvent && (
                          <button
                            className="secondary"
                            disabled={disabled}
                            onClick={() =>
                              void perform((ticket) =>
                                post(
                                  "reconcile",
                                  {
                                    schema: "wen-fixture-run-reconcile/1",
                                    profile,
                                    revision: view.revision,
                                    poolRevision: view.pools.find(
                                      (p) => p.id === r.record.walletId,
                                    )!.revision,
                                    runId: id,
                                    eventId: recoveryEvent,
                                  },
                                  ticket,
                                ),
                              )
                            }
                          >
                            {recoveryEvent.startsWith("uncertain:")
                              ? "Inspect fixture operation"
                              : "Reconcile fixture operation"}
                          </button>
                        )}
                        {r.decisions.length > 0 && (
                          <button
                            className="secondary"
                            disabled={disabled}
                            onClick={() =>
                              void perform((ticket) =>
                                control(
                                  r,
                                  "acknowledge",
                                  ticket,
                                  r.decisions.at(-1)!.record.decisionId,
                                ),
                              )
                            }
                          >
                            Acknowledge decision
                          </button>
                        )}
                      </div>
                      <div className="f4-history">
                        <table aria-label={`Automatic decisions ${r.name}`}>
                          <thead>
                            <tr>
                              <th>Slot</th>
                              <th>Action</th>
                              <th>Reason</th>
                              <th>Lamports</th>
                            </tr>
                          </thead>
                          <tbody>
                            {r.decisions.map((d) => (
                              <tr key={d.record.decisionId}>
                                <td>{d.record.roundId}</td>
                                <td>{d.record.action}</td>
                                <td>{d.record.reason}</td>
                                <td>
                                  {(d.record.amountsLamports ?? []).join(" / ")}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                      <details>
                        <summary>Complete automatic record</summary>
                        <pre data-testid={`run-record-${id}`}>
                          {JSON.stringify(r, null, 2)}
                        </pre>
                      </details>
                    </article>
                  );
                })}
              </div>
              <details>
                <summary>Complete fixture run view</summary>
                <pre data-testid="fixture-run-view">
                  {JSON.stringify(view, null, 2)}
                </pre>
              </details>
            </>
          )}
        </div>
      )}
    </section>
  );
}
