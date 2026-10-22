import { useState } from "react";
import { Badge } from "../components/shared";
import { useWorkspace } from "../workspace";

const ranges = [
  "A · below −0.20%",
  "B · −0.20% to below −0.05%",
  "C · −0.05% to below +0.05%",
  "D · +0.05% to below +0.20%",
  "E · +0.20% or above",
];
export function WENPaperJourney() {
  const { core, agents, navigate } = useWorkspace();
  const [name, setName] = useState("Range rehearsal");
  const [budget, setBudget] = useState("0.01");
  const [selection, setSelection] = useState("2");
  const [expiry, setExpiry] = useState(900);
  const v = agents.view;
  const ready = core.mode === "ready" && !!v;
  return (
    <section className="panel wen-paper" aria-label="WEN paper journey">
      <div className="panel-heading">
        <h2>WEN paper journey</h2>
        <Badge>LOCAL FIXTURES ONLY</Badge>
      </div>
      <div className="agent-record-body">
        <p>
          Rehearse one PM1 entry: prepare, review, approve paper only, then run
          once. No RPC, wallet, AI call or transaction. No live round or
          settlement is simulated. Returns are conditional on the actual round
          and crowding; this rehearsal predicts none.
        </p>
        {!ready ? (
          <p>
            Connect and unlock your private core to save paper entries. Demo
            mode cannot run them.
          </p>
        ) : (
          <>
            <label>
              Paper agent profile
              <select
                aria-label="Paper agent profile"
                value={v.profileId}
                disabled={agents.busy}
                onChange={(e) => void agents.select(e.target.value)}
              >
                {v.profiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <form
              className="agent-form"
              onSubmit={(e) => {
                e.preventDefault();
                void agents.change({
                  action: "wen-paper-prepare",
                  name,
                  wen: {
                    budgetSOL: budget,
                    selection,
                    validForSeconds: expiry,
                  },
                });
              }}
            >
              <label>
                Paper entry name
                <input
                  required
                  maxLength={80}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </label>
              <label>
                Paper SOL budget
                <input
                  required
                  inputMode="decimal"
                  value={budget}
                  onChange={(e) => setBudget(e.target.value)}
                />
              </label>
              <small>
                0.01–1 SOL, up to 9 decimal places. No funds are reserved or
                spent.
              </small>
              <label>
                Paper BTC range
                <select
                  value={selection}
                  onChange={(e) => setSelection(e.target.value)}
                >
                  {ranges.map((range, i) => (
                    <option key={i} value={String(i)}>
                      {range}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Paper review validity
                <select
                  value={expiry}
                  onChange={(e) => setExpiry(Number(e.target.value))}
                >
                  <option value={300}>5 minutes</option>
                  <option value={900}>15 minutes</option>
                  <option value={1800}>30 minutes</option>
                </select>
              </label>
              <button className="primary" disabled={agents.busy}>
                Prepare paper review
              </button>
            </form>
            {agents.error && <p role="alert">{agents.error}</p>}
            {v.tasks
              .filter((t) => t.wen)
              .map((t) => {
                const p = t.wen!;
                return (
                  <article
                    className="memory-card"
                    key={t.id}
                    aria-label={`Paper entry ${t.name}`}
                  >
                    <strong>{t.name}</strong>
                    <p>
                      {t.state} · {p.budgetSOL} SOL ·{" "}
                      {ranges[Number(p.selection)]} · {t.runs}/1 evaluations
                    </p>
                    <p>
                      Paper review expires{" "}
                      {new Date(p.expiresAt * 1000).toLocaleString()}. After
                      approval, one evaluation runs in 60 seconds while the core
                      is running, or you can run it now. An expired review holds
                      without retry.
                    </p>
                    <details>
                      <summary>Paper plan evidence</summary>
                      <p>Template: {p.version}</p>
                      <p>
                        Canonical source: <code>{p.fixtureSource}</code>
                      </p>
                      <p>
                        Fixture SHA-256: <code>{p.fixtureSha256}</code>
                      </p>
                      <p>
                        Review digest: <code>{p.digest}</code>
                      </p>
                    </details>
                    <div className="agent-inline">
                      <button
                        className="secondary"
                        disabled={agents.busy}
                        onClick={() =>
                          void agents
                            .change({ action: "wen-paper-explain", id: t.id })
                            .then((ok) => {
                              if (ok) navigate("agents/chat");
                            })
                        }
                      >
                        Explain this paper entry: {t.name}
                      </button>
                      {t.state === "review" && (
                        <button
                          className="primary"
                          disabled={agents.busy}
                          onClick={() =>
                            void agents.change({
                              action: "wen-paper-approve",
                              id: t.id,
                              consentDigest: p.digest,
                            })
                          }
                        >
                          Approve paper only: {t.name}
                        </button>
                      )}
                      {t.state === "scheduled" && (
                        <>
                          <button
                            className="secondary"
                            disabled={agents.busy}
                            onClick={() =>
                              void agents.change({
                                action: "task-run",
                                id: t.id,
                              })
                            }
                          >
                            Run paper once: {t.name}
                          </button>
                          <button
                            className="text-button"
                            disabled={agents.busy}
                            onClick={() =>
                              void agents.change({
                                action: "task-pause",
                                id: t.id,
                              })
                            }
                          >
                            Pause paper: {t.name}
                          </button>
                        </>
                      )}
                      {t.state === "paused" && (
                        <button
                          className="secondary"
                          disabled={agents.busy}
                          onClick={() =>
                            void agents.change({
                              action: "task-resume",
                              id: t.id,
                            })
                          }
                        >
                          Resume paper: {t.name}
                        </button>
                      )}
                      {["review", "scheduled", "paused"].includes(t.state) && (
                        <button
                          className="text-button"
                          disabled={agents.busy}
                          onClick={() =>
                            void agents.change({
                              action: "wen-paper-cancel",
                              id: t.id,
                            })
                          }
                        >
                          Cancel paper: {t.name}
                        </button>
                      )}
                    </div>
                    {t.state === "held" && (
                      <p>
                        Paper review expired or changed. Prepare a fresh entry;
                        this one cannot resume.
                      </p>
                    )}
                  </article>
                );
              })}
            <h3>Paper receipts</h3>
            <p>
              Unsigned local validation only. Pending means unsettled, with no
              reward, P&amp;L or claim authority.
            </p>
            {!v.runs.some((r) => r.wen) && <p>No paper receipts yet.</p>}
            {v.runs
              .filter((r) => r.wen)
              .slice()
              .reverse()
              .map((r) => {
                const result = r.wen!;
                return (
                  <article
                    className="memory-card"
                    key={r.id}
                    aria-label="WEN paper receipt"
                  >
                    <strong>Paper pending · unsettled</strong>
                    <p>
                      Amount: {result.receipt.amount} lamports · range{" "}
                      {ranges[result.receipt.selection!]} · no settlement or
                      rewards.
                    </p>
                    <p>
                      Open → commit → reveal templates validated locally.
                      Nothing was signed or submitted.
                    </p>
                    <details>
                      <summary>Receipt evidence</summary>
                      <p>
                        Run: <code>{r.id}</code>
                      </p>
                      <p>
                        Plan: <code>{result.planDigest}</code>
                      </p>
                      <p>
                        Receipt SHA-256: <code>{result.receiptSha256}</code>
                      </p>
                      <p>
                        Account SHA-256: <code>{result.accountSha256}</code>
                      </p>
                      {result.instructions.map((i) => (
                        <p key={i.operation}>
                          {i.operation}: <code>{i.requestSha256}</code>
                        </p>
                      ))}
                    </details>
                  </article>
                );
              })}
          </>
        )}
      </div>
    </section>
  );
}
