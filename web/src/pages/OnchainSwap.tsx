import "./OnchainSwap.css";
import { useEffect, useRef, useState } from "react";
import type { Account, AccountsView } from "../accounts";
import type { AgentView } from "../agents";
import type {
  OnchainView,
  SwapCommand,
  SwapReview,
  SwapExecution,
  SwapQuote,
  SwapRecovery,
} from "../onchain";
import {
  atomic,
  units,
  symbol,
  swapState,
  USDC,
  WRAPPED_SOL,
} from "../onchain";
import { Badge } from "../components/shared";
import { RefreshCw, Settings2, Download, Copy } from "../icons";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";

type Draft = { wallet: string; pair: string; amount: string; slippage: string };
const emptyDraft: Draft = {
  wallet: "",
  pair: "usdc-sol",
  amount: "",
  slippage: "50",
};
export function SwapResultFacts({
  result,
  quote,
  recovery,
}: {
  result: SwapExecution;
  quote?: SwapQuote;
  recovery?: SwapRecovery;
}) {
  const { t } = useUI();
  return (
    <dl className="task-facts">
      <dt>{t("Status")}</dt>
      <dd>{t(swapState(recovery?.status ?? result.state))}</dd>
      {result.signature && (
        <>
          <dt>{t("Signature")}</dt>
          <dd className="mono" style={{ overflowWrap: "anywhere" }}>
            {result.signature}
          </dd>
        </>
      )}
      {result.observation?.output && (
        <>
          <dt>{t("Received")}</dt>
          <dd>
            {quote
              ? `${units(result.observation.output, quote.outputMint)} ${symbol(quote.outputMint)}`
              : result.observation.output}
          </dd>
        </>
      )}
    </dl>
  );
}
export function SwapRecoveryActions({
  recovery,
  disabled,
  onAction,
}: {
  recovery?: SwapRecovery;
  disabled: boolean;
  onAction: (action: "swap-cancel" | "swap-reconcile", id: string) => void;
}) {
  const { t } = useUI();
  if (!recovery) return null;
  return (
    <>
      {recovery.cancel && (
        <button
          className="text-button"
          disabled={disabled}
          onClick={() => onAction("swap-cancel", recovery.taskId)}
        >
          {t("Cancel")}
        </button>
      )}
      {recovery.reconcile && (
        <button
          className="text-button"
          disabled={disabled}
          onClick={() => onAction("swap-reconcile", recovery.taskId)}
        >
          {t("Reconcile")}
        </button>
      )}
    </>
  );
}
export function OnchainSwap() {
  const { t } = useUI();
  const core = useCore();
  const { agents, navigate } = useWorkspace();
  const profile = agents.view?.profileId ?? "trading-analyst";
  const storageKey = `fased.swap-draft.v1:${core.instanceId}:${profile}`;
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [loaded, setLoaded] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [view, setView] = useState<OnchainView>();
  const [review, setReview] = useState<SwapReview>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState("");
  const generation = useRef(0);
  const writing = useRef(false);
  const input = draft.pair === "usdc-sol" ? USDC : WRAPPED_SOL;
  const output = input === USDC ? WRAPPED_SOL : USDC;
  const amount = atomic(draft.amount, input);
  const expired =
    !!review && review.proposal.quote.expiresAt * 1000 <= Date.now();
  const active = view ?? agents.view?.onchain;
  const order =
    active?.tasks.find((x) => x.id === selected) ?? active?.tasks.at(-1);
  const result = active?.runs.find((x) => x.taskId === order?.id)?.swap;
  async function read() {
    const stamp = generation.current;
    try {
      const [v, a] = await Promise.all([
        core.request<AgentView>(
          `agents?profile=${encodeURIComponent(profile)}`,
        ),
        core.request<AccountsView>("accounts"),
      ]);
      if (stamp !== generation.current) return;
      setView(v.onchain);
      setAccounts(
        a.record.accounts.filter(
          (x) =>
            [2, 3].includes(x.version) &&
            ["mainnet-beta", "devnet"].includes(x.network),
        ),
      );
    } catch (e) {
      if (stamp === generation.current)
        setError(e instanceof Error ? e.message : t("Unavailable"));
    }
  }
  async function command(action: string, swap: SwapCommand) {
    if (writing.current) return;
    const stamp = generation.current;
    writing.current = true;
    setBusy(true);
    setError("");
    try {
      const current = await core.request<AgentView>(
        `agents?profile=${encodeURIComponent(profile)}`,
      );
      const v = await core.request<AgentView>("agents", "POST", {
        action,
        profileId: profile,
        revision: current.revision,
        swap,
      });
      if (stamp !== generation.current) return;
      if (v.onchain) {
        setView(v.onchain);
        if (v.onchain.review) setReview(v.onchain.review);
      }
      return v.onchain;
    } catch (e) {
      if (stamp === generation.current)
        setError(e instanceof Error ? e.message : t("Unavailable"));
    } finally {
      if (stamp === generation.current) setBusy(false);
      writing.current = false;
    }
  }
  useEffect(() => {
    generation.current++;
    setReview(undefined);
    setSelected("");
    setView(undefined);
    setError("");
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) ?? "null");
      setDraft(
        saved &&
          typeof saved.amount === "string" &&
          typeof saved.wallet === "string" &&
          ["usdc-sol", "sol-usdc"].includes(saved.pair)
          ? { ...emptyDraft, ...saved }
          : emptyDraft,
      );
    } catch {
      setDraft(emptyDraft);
    }
    setLoaded(storageKey);
    void read();
    const timer = setInterval(() => {
      if (!writing.current) void read();
    }, 1500);
    return () => {
      generation.current++;
      clearInterval(timer);
    };
  }, [storageKey, core.request]);
  useEffect(() => {
    if (loaded === storageKey) {
      try {
        localStorage.setItem(storageKey, JSON.stringify(draft));
      } catch {}
    }
  }, [draft, loaded, storageKey]);
  function edit(next: Partial<Draft>) {
    setDraft((x) => ({ ...x, ...next }));
    setReview(undefined);
    setError("");
  }
  function download() {
    if (!review) return;
    const blob = new Blob([JSON.stringify(review.proposal, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `swap-${review.proposal.quote.id}.json`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
  const selectedAccount = accounts.find((a) => a.id === draft.wallet);
  return (
    <>
      <div className="section-head">
        <h1>{t("Onchain")}</h1>
        <div className="row-actions">
          <Badge>{t(active?.available ? "Fixture" : "Unavailable")}</Badge>
          <button
            type="button"
            className="icon-btn"
            title={t("Refresh")}
            aria-label={t("Refresh Onchain")}
            onClick={() => void read()}
          >
            <RefreshCw size={16} />
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      <section
        className="panel task-board onchain-workspace"
        aria-label={t("Onchain swaps")}
      >
        <div className="task-workspace with-detail">
          <div className="task-list">
            <div className="panel-heading">
              <h2>{t("Orders")}</h2>
              <button
                className="text-button"
                onClick={() => navigate("agents/tasks")}
              >
                {t("Tasks")}
              </button>
            </div>
            {!active?.tasks.length ? (
              <p className="empty-row">{t("No orders")}</p>
            ) : (
              <table className="task-table" aria-label={t("Swap orders")}>
                <thead>
                  <tr>
                    <th>{t("Pair")}</th>
                    <th>{t("Amount")}</th>
                    <th>{t("Status")}</th>
                    <th>{t("Actions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {active.tasks.map((task) => {
                    const q = task.swap!.proposal.quote;
                    const r = active.runs.find(
                      (x) => x.taskId === task.id,
                    )?.swap;
                    return (
                      <tr key={task.id}>
                        <td>
                          {symbol(q.inputMint)} / {symbol(q.outputMint)}
                        </td>
                        <td>
                          {units(q.amount, q.inputMint)} {symbol(q.inputMint)}
                        </td>
                        <td>
                          <Badge>
                            {t(
                              swapState(
                                active?.recovery?.find(
                                  (x) => x.taskId === task.id,
                                )?.status ??
                                  r?.state ??
                                  "unknown",
                              ),
                            )}
                          </Badge>
                        </td>
                        <td>
                          <div className="row-actions">
                            <button
                              className="text-button"
                              onClick={() => setSelected(task.id)}
                            >
                              {t("Details")}
                            </button>
                            <SwapRecoveryActions
                              recovery={active?.recovery?.find(
                                (x) => x.taskId === task.id,
                              )}
                              disabled={busy}
                              onAction={(action, id) => {
                                void command(action, { id });
                              }}
                            />
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}
            {order && result && (
              <article
                className="task-detail-body"
                aria-label={t("Swap result")}
              >
                <div className="panel-heading">
                  <h3>{t("Result")}</h3>
                  <Badge>
                    {t(
                      swapState(
                        active?.recovery?.find((x) => x.taskId === order.id)
                          ?.status ?? result.state,
                      ),
                    )}
                  </Badge>
                </div>
                <dl className="task-facts">
                  <dt>{t("Wallet")}</dt>
                  <dd>
                    {accounts.find(
                      (x) => x.id === order.swap!.proposal.quote.accountId,
                    )?.name ?? order.swap!.proposal.quote.owner}
                  </dd>
                  <dt>{t("Network")}</dt>
                  <dd>
                    {t(
                      order.swap!.proposal.quote.network === "devnet"
                        ? "Devnet"
                        : "Mainnet",
                    )}
                  </dd>
                  <dt>{t("Received")}</dt>
                  <dd>
                    {result.state === "confirmed" && result.observation?.output
                      ? `${units(result.observation.output, order.swap!.proposal.quote.outputMint)} ${symbol(order.swap!.proposal.quote.outputMint)}`
                      : "—"}
                  </dd>
                </dl>
                <div className="row-actions">
                  <SwapRecoveryActions
                    recovery={active?.recovery?.find(
                      (x) => x.taskId === order.id,
                    )}
                    disabled={busy}
                    onAction={(action, id) => void command(action, { id })}
                  />
                </div>
                {result.signature && (
                  <div className="row-actions">
                    <span className="mono" title={result.signature}>
                      {result.signature.slice(0, 8)}…
                      {result.signature.slice(-8)}
                    </span>
                    <button
                      type="button"
                      className="icon-btn"
                      title={t("Copy signature")}
                      aria-label={t("Copy signature")}
                      onClick={() =>
                        void navigator.clipboard.writeText(result.signature!)
                      }
                    >
                      <Copy size={14} />
                    </button>
                  </div>
                )}
                <details>
                  <summary>{t("History")}</summary>
                  <ol>
                    {result.events.map((e, i) => (
                      <li key={i}>
                        {t(swapState(e.state))} ·{" "}
                        {new Date(e.at * 1000).toLocaleTimeString()}
                      </li>
                    ))}
                  </ol>
                </details>
              </article>
            )}
          </div>
          <aside className="task-composer" aria-label={t("Swap")}>
            <div className="panel-heading">
              <h2>{t("Swap")}</h2>
              <Badge>
                {selectedAccount?.network === "devnet"
                  ? t("Devnet")
                  : t("Mainnet")}
              </Badge>
            </div>
            {!review ? (
              <form
                className="task-detail-body"
                onSubmit={async (e) => {
                  e.preventDefault();
                  if (!amount) return;
                  await command("swap-quote", {
                    request: {
                      accountId: draft.wallet,
                      inputMint: input,
                      outputMint: output,
                      amount,
                      slippageBps: Number(draft.slippage),
                      maxFeeLamports: "5000",
                    },
                  });
                }}
              >
                <label>
                  {t("Wallet")}
                  <select
                    aria-label={t("Wallet")}
                    value={draft.wallet}
                    onChange={(e) => edit({ wallet: e.target.value })}
                  >
                    <option value="">{t("Select wallet")}</option>
                    {accounts.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name}
                      </option>
                    ))}
                  </select>
                </label>
                {!accounts.length && (
                  <button
                    type="button"
                    className="text-button"
                    onClick={() => navigate("wallets/accounts")}
                  >
                    {t("Wallets")}
                  </button>
                )}
                <label>
                  {t("Pair")}
                  <select
                    aria-label={t("Pair")}
                    value={draft.pair}
                    onChange={(e) => edit({ pair: e.target.value })}
                  >
                    <option value="usdc-sol">USDC → wSOL</option>
                    <option value="sol-usdc">wSOL → USDC</option>
                  </select>
                </label>
                <label>
                  {t("Amount")}
                  <input
                    aria-label={t("Amount")}
                    inputMode="decimal"
                    autoComplete="off"
                    value={draft.amount}
                    onChange={(e) => edit({ amount: e.target.value })}
                  />
                </label>
                <details>
                  <summary>
                    <Settings2 size={14} /> {t("Slippage")}
                  </summary>
                  <label>
                    {t("Basis points")}
                    <input
                      aria-label={t("Slippage basis points")}
                      type="number"
                      min="0"
                      max="100"
                      value={draft.slippage}
                      onChange={(e) => edit({ slippage: e.target.value })}
                    />
                  </label>
                </details>
                <button
                  className="primary"
                  disabled={
                    busy ||
                    !active?.available ||
                    !draft.wallet ||
                    !amount ||
                    !/^\d+$/.test(draft.slippage) ||
                    Number(draft.slippage) > 100
                  }
                >
                  {t("Quote")}
                </button>
              </form>
            ) : (
              <div className="task-detail-body" aria-label={t("Swap review")}>
                <dl className="task-facts">
                  <dt>{t("Pay")}</dt>
                  <dd>
                    {units(
                      review.proposal.quote.amount,
                      review.proposal.quote.inputMint,
                    )}{" "}
                    {symbol(review.proposal.quote.inputMint)}
                  </dd>
                  <dt>{t("Receive")}</dt>
                  <dd>
                    {units(
                      review.proposal.quote.output,
                      review.proposal.quote.outputMint,
                    )}{" "}
                    {symbol(review.proposal.quote.outputMint)}
                  </dd>
                  <dt>{t("Minimum")}</dt>
                  <dd>
                    {units(
                      review.proposal.quote.minimumOutput,
                      review.proposal.quote.outputMint,
                    )}{" "}
                    {symbol(review.proposal.quote.outputMint)}
                  </dd>
                  <dt>{t("Network cost")}</dt>
                  <dd>
                    {units(
                      String(
                        BigInt(review.proposal.quote.feeLamports) +
                          BigInt(review.proposal.quote.rentLamports),
                      ),
                      WRAPPED_SOL,
                    )}{" "}
                    SOL
                  </dd>
                  <dt>{t("Expires")}</dt>
                  <dd>
                    {new Date(
                      review.proposal.quote.expiresAt * 1000,
                    ).toLocaleTimeString()}
                  </dd>
                  <dt>{t("Approval")}</dt>
                  <dd>
                    {t(
                      review.approval === "approved" ? "Approved" : "Required",
                    )}
                  </dd>
                </dl>
                <details>
                  <summary>{t("Owner approval")}</summary>
                  <div className="row-actions">
                    <button
                      type="button"
                      className="secondary"
                      onClick={download}
                    >
                      <Download size={14} />
                      {t("Download approval")}
                    </button>
                    <button
                      type="button"
                      className="icon-btn"
                      title={t("Copy command")}
                      aria-label={t("Copy command")}
                      onClick={() =>
                        void navigator.clipboard.writeText(
                          `fased-app custody approve-swap --proposal-file swap-${review.proposal.quote.id}.json --disposable-fixture`,
                        )
                      }
                    >
                      <Copy size={14} />
                    </button>
                  </div>
                </details>
                <div className="row-actions">
                  <button
                    className="text-button"
                    disabled={busy}
                    onClick={() => setReview(undefined)}
                  >
                    {t("Edit")}
                  </button>
                  <button
                    className="secondary"
                    disabled={busy || expired}
                    onClick={() =>
                      void command("swap-review", {
                        id: review.proposal.quote.id,
                        digest: review.digest,
                      })
                    }
                  >
                    {t("Check approval")}
                  </button>
                  <button
                    className="primary"
                    disabled={busy || expired || review.approval !== "approved"}
                    onClick={async () => {
                      const v = await command("swap-submit", {
                        id: review.proposal.quote.id,
                        digest: review.digest,
                      });
                      if (v) {
                        setSelected(v.tasks.at(-1)?.id ?? "");
                        setReview(undefined);
                      }
                    }}
                  >
                    {t(expired ? "Expired" : "Submit")}
                  </button>
                </div>
              </div>
            )}
          </aside>
        </div>
      </section>
    </>
  );
}
