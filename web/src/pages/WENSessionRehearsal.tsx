import { useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";
import { Badge } from "../components/shared";
import { RangeAllocations } from "../components/RangeAllocations";
import "./WENSessionRehearsal.css";

type Version = { digest: string; raw: string; name: string };
type Library = { revision: number; versions: Version[] };
type Budget = Record<string, string>;
type Scenario = {
  id: string;
  digest: string;
  hours: number;
  startsAt: string;
  expiresAt: string;
  recipe: string;
};
type Decision = {
  record: {
    decisionId: string;
    roundId: string;
    action: string;
    reason: string;
    amountsLamports: string[];
  };
  phase: string;
};
type Session = {
  name: string;
  mode: string;
  reason: string;
  clockAt: string;
  source: Version;
  scenarioId: string;
  pending: { decisionId: string; id: string } | null;
  decisions: Decision[];
  schedule: { acceptedRounds: number };
  record: {
    sessionId: string;
    status: string;
    walletId: string;
    budget: Budget;
  };
};
type View = {
  revision: number;
  libraryRevision: number;
  profile: string;
  sessions: Session[];
  pools: { id: string; revision: number; budget: Budget }[];
  poolOptions: string[];
  scenarios: Scenario[];
  nextEvents: Record<string, string>;
};
type Review = {
  schema: string;
  source: Version;
  scenario: Scenario;
  pool: { budget: Budget };
  input: { mode: string };
  reservedBytes: number;
  expiresAt: number;
  witness: string;
};
type ActionReview = {
  input: {
    profile: string;
    revision: number;
    poolRevision: number;
    sessionId: string;
    decisionId: string;
  };
  reviewId: string;
  offer: { decisionId: string; cutoff: string; expiresAt: number };
  decision: Decision;
  witness: string;
  budget: Budget;
};
const sol = (s: string) => {
  const n = BigInt(s);
  return `${n / 1000000000n}.${(n % 1000000000n).toString().padStart(9, "0")}`;
};

export function WENSessionRehearsal() {
  const core = useCore();
  const { state, agents } = useWorkspace();
  const profile = state.selectedProfile;
  const [view, setView] = useState<View | null>(null);
  const [library, setLibrary] = useState<Library | null>(null);
  const [digest, setDigest] = useState("");
  const [mode, setMode] = useState("review");
  const [scenario, setScenario] = useState("fixture-4h");
  const [pool, setPool] = useState("fixture-wallet:alpha");
  const [name, setName] = useState("Range rehearsal");
  const [draft, setDraft] = useState("");
  const [review, setReview] = useState<Review | null>(null);
  const [actionReview, setActionReview] = useState<ActionReview | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const current = library?.versions.find((v) => v.digest === digest);
  const book = view?.scenarios.find((s) => s.id === scenario);

  const load = async (ticket = generation.current) => {
    const [v, l] = await Promise.all([
      core.request<View>(`wen/sessions?profile=${encodeURIComponent(profile)}`),
      core.request<Library>(
        `wen/research?profile=${encodeURIComponent(profile)}`,
      ),
    ]);
    if (ticket !== generation.current) return;
    setView(v);
    setLibrary(l);
  };
  useEffect(() => {
    const ticket = ++generation.current;
    setView(null);
    setLibrary(null);
    setDigest("");
    setReview(null);
    setActionReview(null);
    setDraft("");
    setError("");
    if (core.mode === "ready")
      void load(ticket).catch((e) => {
        if (ticket === generation.current) setError(String(e));
      });
    return () => {
      generation.current++;
    };
  }, [profile, core.mode, core.instanceId]);
  const perform = async (fn: () => Promise<void>) => {
    const ticket = generation.current;
    setBusy(true);
    setError("");
    try {
      await fn();
      if (ticket === generation.current) await agents.refresh();
    } catch (e) {
      if (ticket === generation.current) {
        setError(String(e));
        setReview(null);
        setActionReview(null);
        await load(ticket).catch(() => {});
      }
    } finally {
      if (ticket === generation.current) setBusy(false);
    }
  };
  const post = async (action: string, body: object) => {
    const ticket = generation.current;
    const result = await core.request<View>(
      `wen/sessions/${action}`,
      "POST",
      body,
    );
    if (ticket === generation.current) {
      setView(result);
      setActionReview(null);
      setReview(null);
    }
  };
  const clear = () => {
    setReview(null);
    setActionReview(null);
    setDraft("");
  };
  const control = (
    s: Session,
    action: string,
    decisionId: string | null = null,
    reviewId: string | null = null,
    witness: string | null = null,
  ) => {
    if (!view) return;
    return post("control", {
      schema: "wen-session-control/1",
      profile,
      revision: view.revision,
      poolRevision: view.pools.find((p) => p.id === s.record.walletId)!
        .revision,
      sessionId: s.record.sessionId,
      action,
      decisionId,
      reviewId,
      witness,
    });
  };

  return (
    <section className="panel f4-rehearsal" aria-label="Session rehearsal">
      <div className="panel-heading">
        <h2>Session rehearsal</h2>
        <Badge>FIXTURE</Badge>
      </div>
      {core.mode !== "ready" ? (
        <p>Private core required</p>
      ) : (
        <div className="agent-record-body">
          {error && <p role="alert">{error}</p>}
          <div className="agent-inline">
            <button
              className="secondary"
              disabled={busy}
              onClick={() => void perform(() => load())}
            >
              Refresh rehearsals
            </button>
          </div>
          {view && library && (
            <>
              <div className="f4-composer">
                <label>
                  Strategy
                  <select
                    aria-label="Rehearsal strategy"
                    value={digest}
                    disabled={busy}
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
                    aria-label="Rehearsal name"
                    value={name}
                    maxLength={80}
                    required
                    disabled={busy}
                    onChange={(e) => {
                      setName(e.target.value);
                      clear();
                    }}
                  />
                </label>
                <label>
                  Mode
                  <select
                    aria-label="Rehearsal mode"
                    value={mode}
                    disabled={busy}
                    onChange={(e) => {
                      setMode(e.target.value);
                      clear();
                    }}
                  >
                    <option value="review">Review</option>
                    <option value="notify">Notify</option>
                  </select>
                </label>
                <label>
                  Scenario
                  <select
                    aria-label="Rehearsal scenario"
                    value={scenario}
                    disabled={busy}
                    onChange={(e) => {
                      setScenario(e.target.value);
                      clear();
                    }}
                  >
                    {view.scenarios.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.id}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Shadow wallet
                  <select
                    aria-label="Rehearsal shadow wallet"
                    value={pool}
                    disabled={busy}
                    onChange={(e) => {
                      setPool(e.target.value);
                      clear();
                    }}
                  >
                    {view.poolOptions.map((p) => (
                      <option key={p}>{p}</option>
                    ))}
                  </select>
                </label>
              </div>
              {current && book && (
                <>
                  <details>
                    <summary>Saved strategy source</summary>
                    <pre>{current.raw}</pre>
                  </details>
                  <div className="agent-inline">
                    <button
                      className="secondary"
                      disabled={busy || !name.trim()}
                      onClick={() => {
                        const next = JSON.parse(current.raw);
                        next.name = name;
                        next.execution = {
                          mode,
                          runner: "fased",
                          mandateId: null,
                        };
                        next.schedule.startsAt = book.startsAt;
                        next.schedule.expiresAt = book.expiresAt;
                        clear();
                        setDraft(JSON.stringify(next, null, 2));
                      }}
                    >
                      Review Fased draft
                    </button>
                    <button
                      className="primary"
                      disabled={busy || !name.trim()}
                      onClick={() =>
                        void perform(async () => {
                          const ticket = generation.current;
                          const r = await core.request<Review>(
                            "wen/sessions/review",
                            "POST",
                            {
                              schema: "wen-session-prepare/1",
                              profile,
                              revision: view.revision,
                              libraryRevision: library.revision,
                              strategyDigest: digest,
                              name,
                              mode,
                              scenarioId: scenario,
                              scenarioDigest: book.digest,
                              fixtureWalletId: pool,
                            },
                          );
                          if (ticket === generation.current) {
                            setReview(r);
                            setDraft("");
                          }
                        })
                      }
                    >
                      Review session
                    </button>
                  </div>
                </>
              )}
              {draft && (
                <section aria-label="Fased strategy draft">
                  <h3>Fased strategy draft</h3>
                  <pre>{draft}</pre>
                  <div className="agent-inline">
                    <button
                      className="primary"
                      disabled={busy}
                      onClick={() =>
                        void perform(async () => {
                          const ticket = generation.current;
                          const l = await core.request<Library>(
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
                            setLibrary(l);
                            const exact = l.versions.find(
                              (v) => v.raw === draft,
                            );
                            if (exact) setDigest(exact.digest);
                            setDraft("");
                            await load(ticket);
                          }
                        })
                      }
                    >
                      Save Fased draft
                    </button>
                    <button
                      className="secondary"
                      onClick={clear}
                      disabled={busy}
                    >
                      Cancel draft
                    </button>
                  </div>
                </section>
              )}
              {review && (
                <section aria-label="Session review">
                  <h3>Session review</h3>
                  <dl>
                    <dt>Mode</dt>
                    <dd>{review.input.mode}</dd>
                    <dt>Runner</dt>
                    <dd>Fased · manual fixture steps</dd>
                    <dt>Window</dt>
                    <dd>
                      {review.scenario.startsAt} — {review.scenario.expiresAt}
                    </dd>
                    <dt>Shadow available</dt>
                    <dd>{sol(review.pool.budget.availableLamports)} SOL</dd>
                    <dt>Review expiry</dt>
                    <dd>{new Date(review.expiresAt * 1000).toISOString()}</dd>
                  </dl>
                  <details>
                    <summary>Exact session review</summary>
                    <pre>{JSON.stringify(review, null, 2)}</pre>
                  </details>
                  <div className="agent-inline">
                    <button
                      className="primary"
                      disabled={busy}
                      onClick={() => void perform(() => post("create", review))}
                    >
                      Create rehearsal
                    </button>
                    <button
                      className="secondary"
                      onClick={clear}
                      disabled={busy}
                    >
                      Cancel session review
                    </button>
                  </div>
                </section>
              )}
              {view.sessions.map((s) => {
                const id = s.record.sessionId,
                  p = view.pools.find((p) => p.id === s.record.walletId)!;
                const last = s.decisions.at(-1);
                return (
                  <article
                    className="f4-session"
                    key={id}
                    aria-label={`Rehearsal ${s.name}`}
                  >
                    <div className="panel-heading">
                      <h3>{s.name}</h3>
                      <Badge>{s.record.status.toUpperCase()}</Badge>
                    </div>
                    <dl>
                      <dt>Mode / runner</dt>
                      <dd>{s.mode} / Fased manual</dd>
                      <dt>Fixture clock</dt>
                      <dd>{s.clockAt}</dd>
                      <dt>Accepted rounds</dt>
                      <dd data-testid={`accepted-${id}`}>
                        {s.schedule.acceptedRounds}
                      </dd>
                      <dt>State</dt>
                      <dd>{s.reason}</dd>
                      <dt>BTC / SAT</dt>
                      <dd>Unavailable</dd>
                      <dt>Next event</dt>
                      <dd>{view.nextEvents[id] || "—"}</dd>
                    </dl>
                    <table aria-label={`Shadow budget ${s.name}`}>
                      <thead>
                        <tr>
                          <th>Shadow amount</th>
                          <th>SOL</th>
                        </tr>
                      </thead>
                      <tbody>
                        {[
                          ["Available", "availableLamports"],
                          ["Turnover", "totalTurnoverLamports"],
                          ["Unsettled", "unsettledLamports"],
                          ["Gross deductions", "actualDeductionsLamports"],
                          ["User fees", "userFeesPaidLamports"],
                          ["Fee holds", "userFeeHoldsLamports"],
                          ["Deduction holds", "deductionHoldsLamports"],
                        ].map(([label, key]) => (
                          <tr key={key}>
                            <td>{label}</td>
                            <td>{sol(p.budget[key])}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    {last && (
                      <RangeAllocations
                        label={`Latest allocation ${s.name}`}
                        amounts={last.record.amountsLamports}
                      />
                    )}
                    <div className="agent-inline">
                      <button
                        className="primary"
                        disabled={busy || !view.nextEvents[id]}
                        onClick={() =>
                          void perform(() =>
                            post("step", {
                              schema: "wen-session-step/1",
                              profile,
                              revision: view.revision,
                              poolRevision: p.revision,
                              sessionId: id,
                              eventId: view.nextEvents[id],
                            }),
                          )
                        }
                      >
                        Step fixture
                      </button>
                      {s.pending && (
                        <button
                          className="secondary"
                          disabled={busy}
                          onClick={() =>
                            void perform(async () => {
                              const ticket = generation.current;
                              const a = await core.request<ActionReview>(
                                "wen/sessions/action-review",
                                "POST",
                                {
                                  schema: "wen-session-action-review/1",
                                  profile,
                                  revision: view.revision,
                                  poolRevision: p.revision,
                                  sessionId: id,
                                  decisionId: s.pending!.decisionId,
                                },
                              );
                              if (ticket === generation.current)
                                setActionReview(a);
                            })
                          }
                        >
                          Review proposal
                        </button>
                      )}
                      {last && (
                        <button
                          className="secondary"
                          disabled={busy}
                          onClick={() =>
                            void perform(async () => {
                              await control(
                                s,
                                "acknowledge",
                                last.record.decisionId,
                              );
                            })
                          }
                        >
                          Acknowledge decision
                        </button>
                      )}
                      {s.record.status !== "complete" && (
                        <>
                          <button
                            className="secondary"
                            disabled={busy}
                            onClick={() =>
                              void perform(async () => {
                                await control(s, "pause");
                              })
                            }
                          >
                            Pause rehearsal
                          </button>
                          <button
                            className="secondary"
                            disabled={
                              busy ||
                              !["held", "paused"].includes(s.record.status)
                            }
                            onClick={() =>
                              void perform(async () => {
                                await control(s, "resume");
                              })
                            }
                          >
                            Resume rehearsal
                          </button>
                          <button
                            className="secondary"
                            disabled={busy}
                            onClick={() =>
                              void perform(async () => {
                                await control(s, "stop");
                              })
                            }
                          >
                            Stop rehearsal
                          </button>
                        </>
                      )}
                    </div>
                    {actionReview?.input.sessionId === id && (
                      <section aria-label="Simulated allocation review">
                        <h4>Simulated allocation review</h4>
                        <RangeAllocations
                          label="Proposed SOL"
                          amounts={actionReview.decision.record.amountsLamports}
                        />
                        <details>
                          <summary>Exact proposal</summary>
                          <pre>{JSON.stringify(actionReview, null, 2)}</pre>
                        </details>
                        <div className="agent-inline">
                          <button
                            className="primary"
                            disabled={busy}
                            onClick={() =>
                              void perform(() =>
                                post("control", {
                                  ...actionReview.input,
                                  schema: "wen-session-control/1",
                                  action: "simulate-accept",
                                  decisionId: actionReview.offer.decisionId,
                                  reviewId: actionReview.reviewId,
                                  witness: actionReview.witness,
                                }),
                              )
                            }
                          >
                            Simulate acceptance
                          </button>
                          <button
                            className="secondary"
                            disabled={busy}
                            onClick={() => setActionReview(null)}
                          >
                            Cancel proposal review
                          </button>
                        </div>
                      </section>
                    )}
                    <details>
                      <summary>Rehearsal history</summary>
                      <div className="f4-history">
                        <table>
                          <thead>
                            <tr>
                              <th>Round</th>
                              <th>Phase</th>
                              <th>Action</th>
                              <th>Reason</th>
                            </tr>
                          </thead>
                          <tbody>
                            {s.decisions.map((row) => (
                              <tr key={row.record.decisionId}>
                                <td>{row.record.roundId}</td>
                                <td>{row.phase}</td>
                                <td>{row.record.action}</td>
                                <td>{row.record.reason}</td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </div>
                    </details>
                    <details>
                      <summary>Complete rehearsal evidence</summary>
                      <pre>{JSON.stringify(s, null, 2)}</pre>
                    </details>
                  </article>
                );
              })}
            </>
          )}
        </div>
      )}
    </section>
  );
}
