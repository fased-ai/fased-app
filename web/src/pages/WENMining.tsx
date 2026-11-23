import { useEffect, useRef, useState } from "react";
import type { Account, AccountsView } from "../accounts";
import { hasActiveOperations, useVisiblePoll, type AgentView } from "../agents";
import {
  lamportsToSOL,
  miningState,
  solToLamports,
  type WENMiningCommand,
  type WENMiningExecution,
  type WENMiningOperation,
  type WENMiningRecovery,
  type WENMiningReview,
  type WENMiningView,
} from "../wen-mining";
import { Badge } from "../components/shared";
import { RefreshCw } from "../icons";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import "./WENMining.css";
import {
  MINING_PRESETS,
  computePresetAllocations,
  RetailAllocationChart,
  RetailMarketChart,
  type MiningPresetId,
} from "../components/MiningHistogramCharts";

type StrategyVersion = { digest: string; name: string };
type StrategyLibrary = { revision: number; versions: StrategyVersion[] };

export function WENMiningResultFacts({
  result,
  operation,
  recovery,
}: {
  result: WENMiningExecution;
  operation?: WENMiningOperation;
  recovery?: WENMiningRecovery;
}) {
  const { t } = useUI();
  return (
    <dl className="task-facts">
      <dt>{t("Status")}</dt>
      <dd>{t(miningState(recovery?.status ?? result.state))}</dd>
      {operation && (
        <>
          <dt>{t("Amount")}</dt>
          <dd>{lamportsToSOL(operation.amountLamports)}</dd>
          <dt>{t("Slot")}</dt>
          <dd>{operation.slot}</dd>
        </>
      )}
      {result.observation?.receiptSha256 && (
        <>
          <dt>{t("Receipt")}</dt>
          <dd className="mono" style={{ overflowWrap: "anywhere" }}>
            {result.observation.receiptSha256.slice(0, 16)}…
          </dd>
        </>
      )}
    </dl>
  );
}

export function WENMiningRecoveryActions({
  recovery,
  disabled,
  onAction,
}: {
  recovery?: WENMiningRecovery;
  disabled: boolean;
  onAction: (
    action: "wen-mining-cancel" | "wen-mining-reconcile",
    id: string,
  ) => void;
}) {
  const { t } = useUI();
  if (!recovery) return null;
  return (
    <>
      {recovery.cancel && (
        <button
          type="button"
          className="text-button"
          disabled={disabled}
          onClick={() => onAction("wen-mining-cancel", recovery.taskId)}
        >
          {t("Cancel")}
        </button>
      )}
      {recovery.reconcile && (
        <button
          type="button"
          className="text-button"
          disabled={disabled}
          onClick={() => onAction("wen-mining-reconcile", recovery.taskId)}
        >
          {t("Reconcile")}
        </button>
      )}
    </>
  );
}

export function WENMining() {
  const { t } = useUI();
  const core = useCore();
  const { agents } = useWorkspace();
  const profile = agents.view?.profileId ?? "wen-operator";
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [strategies, setStrategies] = useState<StrategyVersion[]>([]);
  const [view, setView] = useState<WENMiningView>();
  const [review, setReview] = useState<WENMiningReview>();
  const [accountId, setAccountId] = useState("");
  const [strategyDigest, setStrategyDigest] = useState("");
  const [amountSOL, setAmountSOL] = useState("0.2");
  const [walletBudgetSOL, setWalletBudgetSOL] = useState("1");
  const [maxOpSOL, setMaxOpSOL] = useState("0.5");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const [presetId, setPresetId] = useState<MiningPresetId>("balanced");
  const writing = useRef(false);

  const active = view ?? agents.view?.wenMining;
  const hasActive = busy || hasActiveOperations(active);

  async function load() {
    const stamp = generation.current;
    if (writing.current) return;
    try {
      const [acct, lib, ag] = await Promise.all([
        core.request<AccountsView>("accounts"),
        core.request<StrategyLibrary>(
          `wen/research?profile=${encodeURIComponent(profile)}`,
        ),
        core.request<AgentView>(
          `agents?profile=${encodeURIComponent(profile)}`,
        ),
      ]);
      if (stamp !== generation.current) return;
      const managed = acct.record.accounts.filter((a) => !!a.signer && !!a.rpc);
      setAccounts(managed);
      setAccountId((cur) =>
        managed.some((a) => a.id === cur) ? cur : (managed[0]?.id ?? ""),
      );
      setStrategies(lib.versions);
      setStrategyDigest((cur) =>
        lib.versions.some((v) => v.digest === cur)
          ? cur
          : (lib.versions[0]?.digest ?? ""),
      );
      setView(ag.wenMining);
      return hasActiveOperations(ag.wenMining);
    } catch (e) {
      if (stamp === generation.current)
        setError(e instanceof Error ? e.message : t("Unavailable"));
    }
  }

  useEffect(() => {
    generation.current++;
    setReview(undefined);
    setError("");
    return () => {
      generation.current++;
    };
  }, [profile, core.instanceId, core.request]);

  useVisiblePoll({
    resetKey: `${profile}:${core.instanceId}`,
    request: core.request,
    active: hasActive,
    onPoll: load,
  });

  async function command(action: string, wenMining: WENMiningCommand) {
    if (writing.current) return;
    const stamp = generation.current;
    writing.current = true;
    setBusy(true);
    setError("");
    try {
      const current = await core.request<AgentView>(
        `agents?profile=${encodeURIComponent(profile)}`,
      );
      const next = await core.request<AgentView>("agents", "POST", {
        action,
        profileId: profile,
        revision: current.revision,
        wenMining,
      });
      if (stamp !== generation.current) return;
      setView(next.wenMining);
      setReview(
        action === "wen-mining-review" ? next.wenMining?.review : undefined,
      );
      await agents.refresh();
    } catch (e) {
      if (stamp === generation.current)
        setError(e instanceof Error ? e.message : t("Unavailable"));
    } finally {
      if (stamp === generation.current) setBusy(false);
      writing.current = false;
    }
  }

  const amountLamports = solToLamports(amountSOL);
  const walletBudgetLamports = solToLamports(walletBudgetSOL);
  const maxOperationLamports = solToLamports(maxOpSOL);
  const activePreset =
    MINING_PRESETS.find((preset) => preset.id === presetId) ??
    MINING_PRESETS[0];
  const presetAllocations = computePresetAllocations(
    amountLamports,
    activePreset.weightsBps,
  );
  const marketPhase = (() => {
    const first = active?.tasks?.[0];
    if (!first) return "Open";
    const run = active?.runs.find((r) => r.taskId === first.id)?.wenMining;
    const rec = active?.recovery?.find((r) => r.taskId === first.id);
    const raw = (rec?.status ?? run?.state ?? "open").toLowerCase();
    if (raw.includes("claim") || raw === "completed" || raw === "claimed") {
      return "Claim";
    }
    if (raw.includes("settl")) return "Settle";
    if (raw.includes("reveal")) return "Reveal";
    if (raw.includes("commit")) return "Commit";
    return "Open";
  })();

  return (
    <>
      <div className="section-head">
        <h1>{t("Mining")}</h1>
        <div className="row-actions">
          <Badge>
            {t(
              active?.miningActivation
                ? "Mining active"
                : "Mining activation disabled",
            )}
          </Badge>
          <button
            type="button"
            className="icon-btn"
            aria-label={t("Refresh Mining")}
            onClick={() => void load()}
          >
            <RefreshCw size={16} />
          </button>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      <div className="wen-mining-grid">
        <section
          className="panel task-board wen-pane-col"
          aria-label={t("WEN Mining")}
        >
          <div className="account-form">
            <label>
              {t("Wallet")}
              <select
                aria-label={t("Wallet")}
                value={accountId}
                disabled={busy}
                onChange={(e) => {
                  setAccountId(e.target.value);
                  setReview(undefined);
                }}
              >
                {accounts.map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name} ({a.network})
                  </option>
                ))}
              </select>
            </label>
            <label>
              {t("Strategy")}
              <select
                aria-label={t("Strategy")}
                value={strategyDigest}
                disabled={busy}
                onChange={(e) => {
                  setStrategyDigest(e.target.value);
                  setReview(undefined);
                }}
              >
                {strategies.map((s) => (
                  <option key={s.digest} value={s.digest}>
                    {s.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {t("Amount (SOL)")}
              <input
                aria-label={t("Amount (SOL)")}
                value={amountSOL}
                disabled={busy}
                onChange={(e) => {
                  setAmountSOL(e.target.value);
                  setReview(undefined);
                }}
              />
            </label>
            <div className="wen-chip-row">
              {(["0.1", "0.2", "0.5", "1"] as const).map((chip) => (
                <button
                  key={chip}
                  type="button"
                  className={amountSOL === chip ? "primary" : "secondary"}
                  disabled={busy}
                  onClick={() => {
                    setAmountSOL(chip);
                    setReview(undefined);
                  }}
                >
                  {chip} {t("SOL")}
                </button>
              ))}
            </div>
            <div
              className="wen-preset-bar"
              role="group"
              aria-label={t("Preset")}
            >
              {MINING_PRESETS.map((preset) => (
                <button
                  key={preset.id}
                  type="button"
                  className={
                    preset.id === activePreset.id ? "primary" : "secondary"
                  }
                  aria-pressed={preset.id === activePreset.id}
                  disabled={busy}
                  onClick={() => setPresetId(preset.id)}
                >
                  {t(preset.label)}
                </button>
              ))}
            </div>
            <label>
              {t("Wallet cap (SOL)")}
              <input
                aria-label={t("Wallet cap (SOL)")}
                value={walletBudgetSOL}
                disabled={busy}
                onChange={(e) => {
                  setWalletBudgetSOL(e.target.value);
                  setReview(undefined);
                }}
              />
            </label>
            <label>
              {t("Per-operation cap (SOL)")}
              <input
                aria-label={t("Per-operation cap (SOL)")}
                value={maxOpSOL}
                disabled={busy}
                onChange={(e) => {
                  setMaxOpSOL(e.target.value);
                  setReview(undefined);
                }}
              />
            </label>
            <div className="row-actions">
              <button
                type="button"
                className="secondary"
                disabled={
                  busy ||
                  !active?.available ||
                  !accountId ||
                  !strategyDigest ||
                  !amountLamports ||
                  !walletBudgetLamports ||
                  !maxOperationLamports
                }
                onClick={() =>
                  void command("wen-mining-review", {
                    request: {
                      accountId,
                      strategyDigest,
                      amountLamports: amountLamports!,
                      walletBudgetLamports: walletBudgetLamports!,
                      maxOperationLamports: maxOperationLamports!,
                      policyExpiresAt: Math.floor(Date.now() / 1000) + 3600,
                    },
                  })
                }
              >
                {t("Review")}
              </button>
              {review && (
                <button
                  type="button"
                  className="primary"
                  disabled={busy}
                  onClick={() =>
                    void command("wen-mining-submit", {
                      id: review.operation.id,
                      digest: review.digest,
                    })
                  }
                >
                  {t("Submit")}
                </button>
              )}
            </div>
            {review && (
              <div className="memory-card" aria-label={t("Mining review")}>
                <dl className="task-facts">
                  <dt>{t("Amount")}</dt>
                  <dd>{lamportsToSOL(review.operation.amountLamports)}</dd>
                  <dt>{t("Slot")}</dt>
                  <dd>{review.operation.slot}</dd>
                  <dt>{t("Estimated fee")}</dt>
                  <dd>
                    {lamportsToSOL(review.operation.estimatedFeeLamports)}
                  </dd>
                  {review.prepared.entries &&
                    review.prepared.entries.length > 0 && (
                      <>
                        <dt>{t("Ranges")}</dt>
                        <dd>
                          {review.prepared.entries
                            .map(
                              (entry) =>
                                `#${entry.rangeIndex + 1} ${lamportsToSOL(entry.amountLamports)}`,
                            )
                            .join(" · ")}
                        </dd>
                      </>
                    )}
                </dl>
                <details>
                  <summary>{t("Evidence")}</summary>
                  <p className="mono">{review.prepared.commitment}</p>
                  <p className="mono">{review.digest}</p>
                </details>
              </div>
            )}
          </div>
        </section>
        <div className="wen-pane-col">
          <RetailAllocationChart
            allocationsLamports={presetAllocations}
            weightsBps={activePreset.weightsBps}
          />
          <RetailMarketChart activePhase={marketPhase} />
        </div>
        <section className="panel wen-pane-col" aria-label={t("Allocation")}>
          <div className="agent-record-body">
            {(active?.tasks ?? []).length === 0 && (
              <p>{t("No mining operations")}</p>
            )}
            {(active?.tasks ?? []).map((task) => {
              const run = active?.runs.find(
                (r) => r.taskId === task.id,
              )?.wenMining;
              const rec = active?.recovery?.find((r) => r.taskId === task.id);
              if (!run || !task.wenMining) return null;
              return (
                <article
                  className="memory-card"
                  key={task.id}
                  aria-label={`Mining ${task.name}`}
                >
                  <header className="task-result-heading">
                    <strong>{task.name}</strong>
                    <Badge>{t(miningState(rec?.status ?? run.state))}</Badge>
                  </header>
                  <WENMiningResultFacts
                    result={run}
                    operation={task.wenMining.operation}
                    recovery={rec}
                  />
                  <div className="row-actions">
                    <WENMiningRecoveryActions
                      recovery={rec}
                      disabled={busy}
                      onAction={(action, id) => void command(action, { id })}
                    />
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      </div>
    </>
  );
}
