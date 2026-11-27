import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Dialog } from "../components/shared";
import {
  Clock3,
  LockKeyhole,
  Pause,
  Pickaxe,
  Plus,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
} from "../icons";
import {
  closeMiningSession,
  createMiningSession,
  DEFAULT_PRESETS,
  fetchMiningStatus,
  listRoleMiningWallets,
  previewMiningCommit,
  requestMiningStepUpToken,
  stopMiningSession,
  submitMiningClaim,
  submitMiningCommit,
  updateMiningPolicy,
  type CommitPreview,
  type MiningMode,
  type MiningStatus,
  type StrategyPreset,
} from "../mining-lv1x";
import { useUI } from "../ui-preferences";
import type { WalletV2 } from "../wallets-v2";
import { useWorkspace } from "../workspace";
import {
  MineAllocationCurve,
  MinePresetIcon,
  RANGE_LABELS,
  RANGE_NAMES,
  rebalanceBucketsBps,
  weightsBpsToCurve80,
} from "./MineAllocationCurve";
import "./MineWorkspace.css";

type ClusterChoice = "devnet" | "mainnet-beta";
type BottomTab = "buckets" | "sessions" | "deployment";

const QUICK_AMOUNTS = ["0.05", "0.1", "0.25", "0.5"] as const;

function formatCountdown(remainingSec: number): string {
  const safe = Math.max(0, Math.floor(remainingSec));
  const m = Math.floor(safe / 60);
  const s = safe % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

function shortId(id: string): string {
  if (!id || id.length <= 14) return id;
  return `${id.slice(0, 6)}...${id.slice(-6)}`;
}

export function MineWorkspace() {
  const { t } = useUI();
  const { core, streamClient, navigate } = useWorkspace();

  const [cluster, setCluster] = useState<ClusterChoice>("devnet");
  const [status, setStatus] = useState<MiningStatus | null>(null);
  const [wallets, setWallets] = useState<WalletV2[]>([]);
  const [selectedWalletId, setSelectedWalletId] = useState("");
  const [mode, setMode] = useState<MiningMode>("manual");
  const [presetId, setPresetId] = useState<string>("wide");
  const [weightsBps, setWeightsBps] = useState<number[]>([
    2000, 2000, 2000, 2000, 2000,
  ]);
  const [selectedBucket, setSelectedBucket] = useState<number | null>(null);
  const [amountStr, setAmountStr] = useState("0.05");
  const [autoClaim, setAutoClaim] = useState(true);
  const [stopOnReversal, setStopOnReversal] = useState(true);
  const [preview, setPreview] = useState<CommitPreview | null>(null);
  const [bottomTab, setBottomTab] = useState<BottomTab>("buckets");
  const [countdownSec, setCountdownSec] = useState(0);

  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  // Policy modal
  const [policyOpen, setPolicyOpen] = useState(false);
  const [policyMaxRoundSol, setPolicyMaxRoundSol] = useState("0.5");
  const [policyMaxDailySol, setPolicyMaxDailySol] = useState("5");
  const [policyAutoClaim, setPolicyAutoClaim] = useState(true);

  // Step-up modal
  const [stepUpPending, setStepUpPending] = useState<{
    action: "mining-commit" | "mining-session" | "mining-policy";
    retry: (token: string) => Promise<void>;
  } | null>(null);
  const [stepUpPassword, setStepUpPassword] = useState("");
  const [stepUpError, setStepUpError] = useState("");

  const presets: StrategyPreset[] = useMemo(
    () =>
      status?.presets && status.presets.length >= 6
        ? status.presets
        : DEFAULT_PRESETS,
    [status],
  );

  const parsedAmount = useMemo(() => {
    const n = Number(amountStr);
    return Number.isFinite(n) && n > 0 ? n : 0;
  }, [amountStr]);

  const loadState = useCallback(async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const [st, miningWallets] = await Promise.all([
        fetchMiningStatus(core, cluster),
        listRoleMiningWallets(core).catch(() => [] as WalletV2[]),
      ]);
      setStatus(st);
      setCountdownSec(st.round.remainingSec);
      setWallets(miningWallets);
      setSelectedWalletId((prev) => {
        if (prev && miningWallets.some((w) => w.id === prev)) return prev;
        return miningWallets[0]?.id ?? "";
      });
    } catch (err) {
      setError(
        err instanceof Error ? err.message : t("Failed to load mining status"),
      );
    }
  }, [core, cluster, t]);

  useEffect(() => {
    void loadState();
  }, [loadState]);

  const reloadRef = useRef(loadState);
  reloadRef.current = loadState;

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    let timer: number | undefined;
    const unsub = streamClient.subscribe("mining", () => {
      if (timer !== undefined) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        timer = undefined;
        void reloadRef.current();
      }, 250);
    });
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
      unsub();
    };
  }, [streamClient, core.mode]);

  useEffect(() => {
    const id = window.setInterval(() => {
      setCountdownSec((prev) => (prev > 0 ? prev - 1 : 0));
    }, 1000);
    return () => window.clearInterval(id);
  }, []);

  useEffect(() => {
    if (core.mode !== "ready") return;
    let cancelled = false;
    const handle = window.setTimeout(() => {
      void previewMiningCommit(core, {
        walletId: selectedWalletId,
        cluster,
        amountSol: parsedAmount,
        selection: selectedBucket ?? undefined,
        strategyId: presetId,
        curve: weightsBpsToCurve80(weightsBps),
      })
        .then((res) => {
          if (!cancelled) setPreview(res);
        })
        .catch(() => {});
    }, 120);
    return () => {
      cancelled = true;
      window.clearTimeout(handle);
    };
  }, [
    core,
    cluster,
    selectedWalletId,
    parsedAmount,
    selectedBucket,
    presetId,
    weightsBps,
  ]);

  function applyPreset(p: StrategyPreset) {
    setPresetId(p.id);
    setSelectedBucket(null);
    if (p.bucketsBps.length === 5) {
      setWeightsBps([...p.bucketsBps]);
    }
  }

  function handleCurveRebalance(rangeIndex: number, nextBps: number) {
    setPresetId("custom");
    setSelectedBucket(null);
    setWeightsBps((prev) => rebalanceBucketsBps(prev, rangeIndex, nextBps));
  }

  function handleBucketClick(index: number) {
    if (selectedBucket === index) {
      setSelectedBucket(null);
      const found = presets.find((p) => p.id === presetId) ?? presets[0];
      setWeightsBps([...found.bucketsBps]);
      return;
    }
    setSelectedBucket(index);
    setPresetId("custom");
    const next = [0, 0, 0, 0, 0];
    next[index] = 10000;
    setWeightsBps(next);
  }

  async function runPrimaryAction(stepUpToken?: string) {
    if (core.mode !== "ready") return;
    if (cluster === "mainnet-beta" && !status?.mainnetEnabled) {
      setError(
        status?.mainnetGateNote ||
          t("Mainnet LV1X mining is locked until official pins are published."),
      );
      return;
    }
    if (parsedAmount <= 0) {
      setError(t("Enter a valid SOL amount."));
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      if (mode === "manual") {
        if (!selectedWalletId) {
          setError(t("Create a Mining role wallet in Wallets to commit."));
          return;
        }
        await submitMiningCommit(
          core,
          {
            walletId: selectedWalletId,
            cluster,
            amountSol: parsedAmount,
            selection: selectedBucket ?? preview?.dominantBucket ?? 2,
            strategyId: presetId,
            curve: weightsBpsToCurve80(weightsBps),
          },
          stepUpToken,
        );
        setNotice(
          `Prepared commit for round #${status?.round.roundSlot ?? 0} (${amountStr} SOL).`,
        );
      } else {
        const wallet = wallets.find((w) => w.id === selectedWalletId);
        await createMiningSession(
          core,
          {
            walletId: selectedWalletId,
            walletHandle: wallet?.handle ?? "",
            cluster,
            strategyId: presetId,
            mode,
            perRoundSol: parsedAmount,
            autoClaim,
            stopOnReversal,
            curve: weightsBpsToCurve80(weightsBps),
          },
          stepUpToken,
        );
        setNotice(
          `Started ${mode === "auto" ? "Auto" : "Semi-Auto"} session (${presetId}, ${amountStr} SOL/round).`,
        );
        setBottomTab("sessions");
      }
      setStepUpPending(null);
      setStepUpPassword("");
      await loadState();
    } catch (err) {
      const msg =
        err instanceof Error ? err.message : t("Mining action refused");
      if (
        !stepUpToken &&
        (msg.toLowerCase().includes("step-up") ||
          msg.toLowerCase().includes("step_up"))
      ) {
        setStepUpError("");
        setStepUpPending({
          action: mode === "manual" ? "mining-commit" : "mining-session",
          retry: (tok) => runPrimaryAction(tok),
        });
      } else {
        setError(msg);
      }
    } finally {
      setBusy(false);
    }
  }

  async function handleClaimLatest() {
    if (!selectedWalletId || !status) {
      setError(t("Select a Mining role wallet first."));
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const latestSlot = status.streak[0]?.roundSlot ?? status.round.roundSlot;
      await submitMiningClaim(core, {
        walletId: selectedWalletId,
        roundSlot: latestSlot,
      });
      setNotice(`Prepared claim review for round #${latestSlot}.`);
      await loadState();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Claim refused"));
    } finally {
      setBusy(false);
    }
  }

  async function savePolicy(stepUpToken?: string) {
    if (!selectedWalletId) return;
    setBusy(true);
    setError("");
    try {
      const perRoundLamports = String(
        Math.round(Math.max(0.01, Number(policyMaxRoundSol) || 0.5) * 1e9),
      );
      const dailyLamports = String(
        Math.round(Math.max(0.05, Number(policyMaxDailySol) || 5) * 1e9),
      );
      const currentWallet = wallets.find((w) => w.id === selectedWalletId);
      await updateMiningPolicy(
        core,
        {
          walletId: selectedWalletId,
          maxPerTxLamports: perRoundLamports,
          maxDailyLamports: dailyLamports,
          expectedPolicyHash: currentWallet?.policy.policyHash,
          mining: {
            cluster: "devnet",
            maxSolPerRoundLamports: perRoundLamports,
            maxDailySolLamports: dailyLamports,
            allowedStrategies: presets.map((p) => p.id),
            autoClaim: policyAutoClaim,
          },
        },
        stepUpToken,
      );
      setPolicyOpen(false);
      setStepUpPending(null);
      setStepUpPassword("");
      setNotice(t("Updated Mining wallet policy."));
      await loadState();
    } catch (err) {
      const msg =
        err instanceof Error ? err.message : t("Policy update failed");
      if (
        !stepUpToken &&
        (msg.toLowerCase().includes("step-up") ||
          msg.toLowerCase().includes("step_up"))
      ) {
        setStepUpError("");
        setStepUpPending({
          action: "mining-policy",
          retry: (tok) => savePolicy(tok),
        });
      } else {
        setError(msg);
      }
    } finally {
      setBusy(false);
    }
  }

  async function submitStepUp() {
    if (!stepUpPending || !stepUpPassword.trim()) {
      setStepUpError(t("Owner password is required."));
      return;
    }
    setStepUpError("");
    try {
      const token = await requestMiningStepUpToken(
        core,
        stepUpPassword,
        stepUpPending.action,
        selectedWalletId,
      );
      await stepUpPending.retry(token);
    } catch (err) {
      setStepUpError(
        err instanceof Error ? err.message : t("Step-up verification failed"),
      );
    }
  }

  const mainnetLocked =
    cluster === "mainnet-beta" && !(status?.mainnetEnabled ?? false);
  const round = status?.round;
  const moveBps = round?.moveBps ?? 0;

  return (
    <section className="mine-shell" aria-label={t("Mine")}>
      <header className="mine-topbar">
        <div className="mine-title-row">
          <Pickaxe size={18} aria-hidden="true" />
          <h1>{t("Mine")}</h1>
          <div
            className="wt-seg mine-cluster-seg"
            role="group"
            aria-label="Mining cluster"
          >
            <button
              type="button"
              aria-pressed={cluster === "devnet"}
              onClick={() => setCluster("devnet")}
            >
              Devnet
            </button>
            <button
              type="button"
              aria-pressed={cluster === "mainnet-beta"}
              onClick={() => setCluster("mainnet-beta")}
            >
              Mainnet
            </button>
          </div>
          <div className="mine-meta-row">
            <span className="mine-stat-pill">
              <Clock3 size={13} aria-hidden="true" />
              <strong>#{round?.roundSlot ?? "—"}</strong>
              <span>{formatCountdown(countdownSec)}</span>
            </span>
            <span
              className={`mine-stat-pill ${moveBps >= 0 ? "is-up" : "is-down"}`}
            >
              <span>BTC</span>
              <strong>
                ${round ? round.markPriceUsd.toLocaleString("en-US") : "—"}
              </strong>
              <span>
                {moveBps >= 0 ? "+" : ""}
                {moveBps} bps
              </span>
            </span>
            <span className="mine-stat-pill">
              <ShieldCheck size={13} aria-hidden="true" />
              <span>{round?.bpfVerified ? "BPF Verified" : "Fixture"}</span>
            </span>
            {mainnetLocked && (
              <span className="mine-stat-pill is-locked">
                <LockKeyhole size={13} aria-hidden="true" />
                <span>Mainnet Gated</span>
              </span>
            )}
          </div>
        </div>

        <div className="mine-top-actions">
          <select
            className="mine-wallet-select"
            aria-label="Mining wallet"
            value={selectedWalletId}
            onChange={(e) => setSelectedWalletId(e.target.value)}
          >
            {wallets.length === 0 ? (
              <option value="">No Mining wallet</option>
            ) : (
              wallets.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name || w.handle} ({shortId(w.publicKey)})
                </option>
              ))
            )}
          </select>
          <button
            type="button"
            className="wt-btn-soft"
            title="Manage Mining wallets"
            onClick={() => navigate("wallets/accounts")}
          >
            Wallets
          </button>
          <button
            type="button"
            className="wt-btn-soft"
            onClick={() => navigate("tasks")}
          >
            <Plus size={14} aria-hidden="true" />
            <span>Create Mining Task</span>
          </button>
          <button
            type="button"
            className="icon-btn"
            aria-label="Refresh round telemetry"
            title="Refresh"
            onClick={() => void loadState()}
          >
            <RefreshCw size={14} aria-hidden="true" />
          </button>
        </div>
      </header>

      <div className="mine-grid">
        <div className="wt-wpanel mine-canvas">
          <div className="mine-section-head">
            <h2>24-Round Streak</h2>
            <span className="mine-muted-note">
              Pool {round ? `${round.totalPoolSol.toFixed(2)} SOL` : "0.00 SOL"}{" "}
              · {round?.minerCount ?? 0} miners
            </span>
          </div>

          <div
            className="mine-streak-grid"
            role="list"
            aria-label="24-round streak"
          >
            {(status?.streak ?? []).slice(0, 24).map((entry) => (
              <div
                key={entry.roundSlot}
                role="listitem"
                className={`mine-streak-chip range-${entry.winningRange}`}
                title={`#${entry.roundSlot} · Range ${entry.code} · ${entry.moveBps >= 0 ? "+" : ""}${entry.moveBps} bps`}
              >
                <span>{entry.code}</span>
                <small>
                  {entry.moveBps >= 0 ? "+" : ""}
                  {entry.moveBps}
                </small>
              </div>
            ))}
          </div>

          <div className="mine-section-head">
            <h2>Allocation Curve (80 Microbars)</h2>
            <span className="mine-muted-note">
              Drag curve or press arrow keys to rebalance A–E
            </span>
          </div>

          <MineAllocationCurve
            weightsBps={weightsBps}
            innerBps={round?.innerBps ?? 5}
            outerBps={round?.outerBps ?? 20}
            onRebalance={handleCurveRebalance}
          />

          <div
            className="mine-bucket-strip"
            role="group"
            aria-label="Range buckets"
          >
            {RANGE_LABELS.map((code, i) => {
              const bucket = round?.buckets?.[i];
              const pct = ((weightsBps[i] ?? 0) / 100).toFixed(0);
              return (
                <button
                  key={code}
                  type="button"
                  className={`mine-bucket-cell${selectedBucket === i ? " is-selected" : ""}`}
                  onClick={() => handleBucketClick(i)}
                >
                  <span>
                    {code} · {RANGE_NAMES[i]}
                  </span>
                  <strong>{pct}%</strong>
                  <small>
                    {bucket ? `${bucket.evMultiple.toFixed(2)}×` : "—"}
                  </small>
                </button>
              );
            })}
          </div>

          <div
            className="wt-tabs-underline"
            role="tablist"
            aria-label="Mining telemetry tabs"
          >
            <button
              type="button"
              role="tab"
              aria-selected={bottomTab === "buckets"}
              onClick={() => setBottomTab("buckets")}
            >
              Buckets
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={bottomTab === "sessions"}
              onClick={() => setBottomTab("sessions")}
            >
              Sessions ({status?.sessions?.length ?? 0})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={bottomTab === "deployment"}
              onClick={() => setBottomTab("deployment")}
            >
              Deployment
            </button>
          </div>

          {bottomTab === "buckets" && (
            <div className="mine-table-scroll">
              <table className="mine-table" aria-label="Round buckets">
                <thead>
                  <tr>
                    <th>Range</th>
                    <th>Return Band</th>
                    <th>Weight</th>
                    <th>Pool SOL</th>
                    <th>Crowd %</th>
                    <th>Model %</th>
                    <th>EV ×</th>
                  </tr>
                </thead>
                <tbody>
                  {(round?.buckets ?? []).map((b) => (
                    <tr key={b.code}>
                      <td>
                        <strong>{b.code}</strong> {RANGE_NAMES[b.index] ?? ""}
                      </td>
                      <td>{b.label}</td>
                      <td>{((weightsBps[b.index] ?? 0) / 100).toFixed(1)}%</td>
                      <td>{b.poolSol.toFixed(2)} SOL</td>
                      <td>{(b.crowdShare * 100).toFixed(1)}%</td>
                      <td>{(b.modelProb * 100).toFixed(1)}%</td>
                      <td>{b.evMultiple.toFixed(2)}×</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {bottomTab === "sessions" && (
            <div className="mine-table-scroll">
              <table className="mine-table" aria-label="Mining sessions">
                <thead>
                  <tr>
                    <th>Strategy</th>
                    <th>Mode</th>
                    <th>Per Round</th>
                    <th>Rounds</th>
                    <th>Status</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {(status?.sessions ?? []).length === 0 ? (
                    <tr>
                      <td colSpan={6} className="mine-muted-note">
                        No active mining sessions. Switch composer to Semi-Auto
                        or Auto to run continuous rounds.
                      </td>
                    </tr>
                  ) : (
                    (status?.sessions ?? []).map((sess) => (
                      <tr key={sess.id}>
                        <td>{sess.strategyId}</td>
                        <td>{sess.mode}</td>
                        <td>{sess.perRoundSol.toFixed(2)} SOL</td>
                        <td>{sess.roundsPlayed}</td>
                        <td>{sess.status}</td>
                        <td>
                          {sess.status === "active" && (
                            <button
                              type="button"
                              className="icon-btn"
                              aria-label={`Stop session ${sess.id}`}
                              title="Stop session"
                              onClick={() =>
                                void stopMiningSession(core, sess.id).then(
                                  loadState,
                                )
                              }
                            >
                              <Pause size={13} aria-hidden="true" />
                            </button>
                          )}
                          {sess.status !== "closed" && (
                            <button
                              type="button"
                              className="icon-btn"
                              aria-label={`Close session ${sess.id}`}
                              title="Close session"
                              onClick={() =>
                                void closeMiningSession(core, sess.id).then(
                                  loadState,
                                )
                              }
                            >
                              <Trash2 size={13} aria-hidden="true" />
                            </button>
                          )}
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          )}

          {bottomTab === "deployment" && (
            <div className="wt-hairline-table">
              <div className="wt-hairline-row">
                <span>Cluster</span>
                <strong>{round?.cluster ?? cluster}</strong>
              </div>
              <div className="wt-hairline-row">
                <span>LV1X Program ID</span>
                <span className="f-mono">{round?.programId ?? "—"}</span>
              </div>
              <div className="wt-hairline-row">
                <span>Ledger PDA</span>
                <span className="f-mono">{round?.ledgerId ?? "—"}</span>
              </div>
              <div className="wt-hairline-row">
                <span>ELF SHA-256</span>
                <span className="f-mono">
                  {shortId(round?.deployedSha256 ?? "")}
                </span>
              </div>
              <div className="wt-hairline-row">
                <span>Mainnet Gate</span>
                <span>{status?.mainnetGateNote ?? "Disabled by policy"}</span>
              </div>
            </div>
          )}
        </div>

        <aside className="wt-wpanel mine-composer" aria-label="Mining composer">
          <div className="mine-section-head">
            <h2>{mode === "manual" ? "Manual Commit" : "Session Runner"}</h2>
            <button
              type="button"
              className="icon-btn"
              aria-label="Reset curve to Wide preset"
              title="Reset to Wide"
              onClick={() => applyPreset(presets[0])}
            >
              <RotateCcw size={13} aria-hidden="true" />
            </button>
          </div>

          <div className="wt-seg" role="group" aria-label="Execution mode">
            <button
              type="button"
              aria-pressed={mode === "manual"}
              onClick={() => setMode("manual")}
            >
              Manual
            </button>
            <button
              type="button"
              aria-pressed={mode === "semi_auto"}
              onClick={() => setMode("semi_auto")}
            >
              Semi-Auto
            </button>
            <button
              type="button"
              aria-pressed={mode === "auto"}
              onClick={() => setMode("auto")}
            >
              Auto
            </button>
          </div>

          <div
            className="mine-presets-grid"
            role="group"
            aria-label="Strategy presets"
          >
            {presets.slice(0, 6).map((p) => (
              <button
                key={p.id}
                type="button"
                className="mine-preset-btn"
                aria-pressed={presetId === p.id}
                onClick={() => applyPreset(p)}
              >
                <MinePresetIcon
                  weights={p.id === "custom" ? weightsBps : p.bucketsBps}
                />
                <span>{p.name}</span>
              </button>
            ))}
          </div>

          <div className="mine-field">
            <label htmlFor="mine-amount-input">Amount per round (SOL)</label>
            <input
              id="mine-amount-input"
              className="mine-amount-input"
              type="number"
              min="0.01"
              step="0.01"
              value={amountStr}
              onChange={(e) => setAmountStr(e.target.value)}
            />
            <div className="mine-quick-chips">
              {QUICK_AMOUNTS.map((amt) => (
                <button
                  key={amt}
                  type="button"
                  className="mine-chip"
                  aria-pressed={amountStr === amt}
                  onClick={() => setAmountStr(amt)}
                >
                  {amt} SOL
                </button>
              ))}
            </div>
          </div>

          {mode !== "manual" && (
            <>
              <label className="mine-toggle-row">
                <input
                  type="checkbox"
                  checked={autoClaim}
                  onChange={(e) => setAutoClaim(e.target.checked)}
                />
                <span>Auto-claim settled rewards</span>
              </label>
              <label className="mine-toggle-row">
                <input
                  type="checkbox"
                  checked={stopOnReversal}
                  onChange={(e) => setStopOnReversal(e.target.checked)}
                />
                <span>Stop on AI trend reversal</span>
              </label>
            </>
          )}

          <div className="wt-hairline-table">
            <div className="wt-hairline-row">
              <span>Dominant range</span>
              <strong>
                {preview
                  ? `${preview.dominantCode} (${RANGE_NAMES[preview.dominantBucket] ?? ""})`
                  : "A"}
              </strong>
            </div>
            <div className="wt-hairline-row">
              <span>Expected EV</span>
              <strong>
                {preview ? `${preview.expectedEvMultiple.toFixed(2)}×` : "—"}
              </strong>
            </div>
            <div className="wt-hairline-row">
              <span>Lamports</span>
              <span className="f-mono">{preview?.amountLamports ?? "0"}</span>
            </div>
          </div>

          {(mainnetLocked || preview?.mainnetBlocked) && (
            <div className="mine-gate-note" role="status">
              {preview?.mainnetReason ||
                status?.mainnetGateNote ||
                "Mainnet LV1X mining is gated until official program pins are verified."}
            </div>
          )}

          {notice && (
            <p className="mine-notice" role="status">
              {notice}
            </p>
          )}
          {error && (
            <p className="mine-error" role="alert">
              {error}
            </p>
          )}

          <div className="mine-composer-footer">
            <button
              type="button"
              className="wt-btn-rose"
              disabled={busy || mainnetLocked || parsedAmount <= 0}
              onClick={() => void runPrimaryAction()}
            >
              {mode === "manual" ? "Commit Round" : "Start Session"}
            </button>
            <div className="mine-secondary-actions">
              <button
                type="button"
                className="wt-btn-soft"
                disabled={busy || !selectedWalletId || mainnetLocked}
                onClick={() => void handleClaimLatest()}
              >
                Claim
              </button>
              <button
                type="button"
                className="wt-btn-soft"
                disabled={!selectedWalletId}
                onClick={() => setPolicyOpen(true)}
              >
                <SlidersHorizontal size={13} aria-hidden="true" />
                <span>Policy</span>
              </button>
            </div>
          </div>
        </aside>
      </div>

      {policyOpen && (
        <Dialog
          title="Mining Wallet Policy"
          onClose={() => setPolicyOpen(false)}
        >
          <div className="mine-modal-form">
            <div className="mine-field">
              <label htmlFor="mine-policy-round">Max SOL per round</label>
              <input
                id="mine-policy-round"
                className="mine-modal-input"
                type="number"
                step="0.05"
                value={policyMaxRoundSol}
                onChange={(e) => setPolicyMaxRoundSol(e.target.value)}
              />
            </div>
            <div className="mine-field">
              <label htmlFor="mine-policy-daily">Max daily SOL cap</label>
              <input
                id="mine-policy-daily"
                className="mine-modal-input"
                type="number"
                step="0.5"
                value={policyMaxDailySol}
                onChange={(e) => setPolicyMaxDailySol(e.target.value)}
              />
            </div>
            <label className="mine-toggle-row">
              <input
                type="checkbox"
                checked={policyAutoClaim}
                onChange={(e) => setPolicyAutoClaim(e.target.checked)}
              />
              <span>Allow autonomous claim</span>
            </label>
            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => setPolicyOpen(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                disabled={busy}
                onClick={() => void savePolicy()}
              >
                Save Policy
              </button>
            </div>
          </div>
        </Dialog>
      )}

      {stepUpPending && (
        <Dialog
          title="Owner Step-Up Verification"
          onClose={() => setStepUpPending(null)}
        >
          <div className="mine-modal-form">
            <div className="mine-field">
              <label htmlFor="mine-stepup-pw">Owner password</label>
              <input
                id="mine-stepup-pw"
                className="mine-modal-input"
                type="password"
                value={stepUpPassword}
                onChange={(e) => setStepUpPassword(e.target.value)}
              />
            </div>
            {stepUpError && (
              <p className="mine-error" role="alert">
                {stepUpError}
              </p>
            )}
            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => setStepUpPending(null)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                onClick={() => void submitStepUp()}
              >
                Confirm
              </button>
            </div>
          </div>
        </Dialog>
      )}
    </section>
  );
}
