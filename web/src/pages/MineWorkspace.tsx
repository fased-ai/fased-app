import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CoreError } from "../core";
import { Dialog } from "../components/shared";
import { setLocalOwnerPassword } from "../wallets-v2";
import {
  ArrowUpRight,
  BookOpen,
  Bot,
  Clock3,
  LockKeyhole,
  Pause,
  Pickaxe,
  Play,
  RefreshCw,
  ShieldCheck,
  Zap,
} from "../icons";
import {
  activateUniversalTask,
  createUniversalTask,
  listUniversalTasks,
  pauseUniversalTask,
  requestTaskStepUpToken,
  runUniversalTaskNow,
  statusPillTone,
  type ApprovalPolicy,
  type ExecMode,
  type ScheduleKind,
  type UniversalTask,
} from "../tasks-v2";
import {
  closeMiningSession,
  createMiningSession,
  DEFAULT_PRESETS,
  deleteMiningStrategy,
  depositMiningSession,
  fetchMiningCandles,
  fetchMiningLeaderboard,
  fetchMiningPortfolio,
  fetchMiningProtocolStats,
  fetchMiningStaking,
  fetchMiningStatus,
  listRoleMiningWallets,
  listSavedMiningStrategies,
  previewMiningCommit,
  requestMiningStepUpToken,
  saveMiningStrategy,
  simulateMiningStrategy,
  stopMiningSession,
  submitMiningActivateStake,
  submitMiningClaim,
  submitMiningClaimAll,
  submitMiningClaimStaking,
  submitMiningCommit,
  submitMiningStake,
  submitMiningUnstake,
  updateMiningPolicy,
  type CandleBar,
  type ClaimableRoundItem,
  type CommitPreview,
  type LeaderboardResponse,
  type MiningMode,
  type MiningStatus,
  type PortfolioResponse,
  type ProtocolStats,
  type SavedStrategy,
  type StakingOverviewResponse,
  type StrategyPreset,
} from "../mining-lv1x";
import { useUI } from "../ui-preferences";
import type { WalletV2 } from "../wallets-v2";
import { useWorkspace } from "../workspace";
import {
  rebalanceBucketsBps,
  weightsBpsToCurve80,
} from "./MineAllocationCurve";
import { MineAssetsPage } from "./mine/MineAssetsPage";
import { MineComposer, type ComposerTab } from "./mine/MineComposer";
import { MineLeftRail } from "./mine/MineLeftRail";
import { MineMarketChart, type ChartInterval } from "./mine/MineMarketChart";
import { MinePortfolioPage } from "./mine/MinePortfolioPage";
import { MineRecordsPanel, type RecordsTab } from "./mine/MineRecordsPanel";
import {
  computeScheduledSlots,
  MineScheduleGrid,
  type ScheduleDraft,
} from "./mine/MineScheduleGrid";
import { MineStakePage } from "./mine/MineStakePage";
import { MineStrategiesPage } from "./mine/MineStrategiesPage";
import "./MineWorkspace.css";

type ClusterChoice = "devnet" | "mainnet-beta";
type MineSubRoute = "mine" | "assets" | "strategies" | "portfolio" | "stake";

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

function parseSharedCurveFromHash(): number[] | null {
  try {
    const hash = window.location.hash || "";
    const qIdx = hash.indexOf("?");
    if (qIdx < 0) return null;
    const raw = new URLSearchParams(hash.slice(qIdx + 1)).get("curve");
    if (!raw) return null;
    const parts = raw.split(",").map((x) => Number(x.trim()));
    if (parts.length !== 5 || parts.some((n) => !Number.isFinite(n) || n < 0)) {
      return null;
    }
    const sum = parts.reduce((a, b) => a + b, 0);
    if (sum <= 0) return null;
    const bps = parts.map((n) => Math.round((n / sum) * 10000));
    const diff = 10000 - bps.reduce((a, b) => a + b, 0);
    if (diff !== 0) bps[2] = Math.max(0, bps[2] + diff);
    return bps;
  } catch {
    return null;
  }
}

export function MineWorkspace() {
  const { t } = useUI();
  const { core, streamClient, navigate, tab } = useWorkspace();

  const subRoute: MineSubRoute =
    tab === "assets" ||
    tab === "strategies" ||
    tab === "portfolio" ||
    tab === "stake"
      ? tab
      : "mine";

  const [cluster, setCluster] = useState<ClusterChoice>("devnet");
  const [status, setStatus] = useState<MiningStatus | null>(null);
  const [protoStats, setProtoStats] = useState<ProtocolStats | null>(null);
  const [candles, setCandles] = useState<CandleBar[]>([]);
  const [candleInterval, setCandleInterval] = useState<ChartInterval>("10m");
  const [leaderboard, setLeaderboard] = useState<LeaderboardResponse | null>(
    null,
  );
  const [lbWindow, setLbWindow] = useState<"24h" | "7d" | "30d" | "all">("24h");
  const [portfolio, setPortfolio] = useState<PortfolioResponse | null>(null);
  const [staking, setStaking] = useState<StakingOverviewResponse | null>(null);
  const [savedStrategies, setSavedStrategies] = useState<SavedStrategy[]>([]);

  const [wallets, setWallets] = useState<WalletV2[]>([]);
  const [selectedWalletId, setSelectedWalletId] = useState("");
  const [composerTab, setComposerTab] = useState<ComposerTab>("mine");
  const [recordsTab, setRecordsTab] = useState<RecordsTab>("current");
  const [mode, setMode] = useState<MiningMode>("manual");
  const [presetId, setPresetId] = useState<string>("wide");
  const [weightsBps, setWeightsBps] = useState<number[]>(
    () => parseSharedCurveFromHash() ?? [2000, 2000, 2000, 2000, 2000],
  );
  const [selectedBucket, setSelectedBucket] = useState<number | null>(null);
  const [amountStr, setAmountStr] = useState("0.05");
  const [roundsCount, setRoundsCount] = useState(1);
  const [scheduleDraft, setScheduleDraft] = useState<ScheduleDraft>({
    horizonLabel: "4h",
    scheduleMode: "every",
    startMode: "now",
    selectedOffsets: [0, 1, 2, 3],
    everyN: 1,
    cycleOn: 3,
    cycleOff: 1,
    randomCount: 6,
    rollingBudget: true,
  });
  const [autoClaim, setAutoClaim] = useState(true);
  const [stopOnReversal, setStopOnReversal] = useState(true);
  const [preview, setPreview] = useState<CommitPreview | null>(null);
  const [countdownSec, setCountdownSec] = useState(0);

  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  // Modals
  const [marketsOpen, setMarketsOpen] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [saveStratOpen, setSaveStratOpen] = useState(false);
  const [saveStratName, setSaveStratName] = useState("My Custom Curve");
  const [policyOpen, setPolicyOpen] = useState(false);
  const [policyMaxRoundSol, setPolicyMaxRoundSol] = useState("0.5");
  const [policyMaxDailySol, setPolicyMaxDailySol] = useState("5");
  const [policyAutoClaim, setPolicyAutoClaim] = useState(true);
  const [miningTasks, setMiningTasks] = useState<UniversalTask[]>([]);
  const [tasksDrawerOpen, setTasksDrawerOpen] = useState(false);
  const [automateOpen, setAutomateOpen] = useState(false);
  const [autoName, setAutoName] = useState("Mining: Wide Range");
  const [autoExecMode, setAutoExecMode] = useState<ExecMode>("hybrid");
  const [autoApproval, setAutoApproval] = useState<ApprovalPolicy>("semi_auto");
  const [autoSchedule, setAutoSchedule] = useState<ScheduleKind>("interval");
  const [autoIntervalSec, setAutoIntervalSec] = useState("300");
  const [autoEventFilter, setAutoEventFilter] = useState("mining.round.opened");

  const [stepUpPending, setStepUpPending] = useState<{
    action:
      "policy" | "execute" | "mining-session" | "wallet" | "task-activate";
    mode: "step_up" | "password_setup";
    bindingDigest?: string;
    targetId?: string;
    retry: (token?: string) => Promise<void>;
  } | null>(null);
  const [stepUpPassword, setStepUpPassword] = useState("");
  const [stepUpPasswordConfirm, setStepUpPasswordConfirm] = useState("");
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

  const crowdWeightsBps = useMemo(() => {
    const b = status?.round?.buckets;
    if (!b || b.length !== 5) return [2000, 2000, 2000, 2000, 2000];
    const raw = b.map((x) => Math.max(0, Math.round(x.crowdShare * 50) * 200));
    const sum = raw.reduce((acc, v) => acc + v, 0);
    if (sum !== 10000) raw[2] = Math.max(0, raw[2] + (10000 - sum));
    return raw;
  }, [status]);

  const loadState = useCallback(async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const [st, miningWallets, ps, cd, mTasks] = await Promise.all([
        fetchMiningStatus(core, cluster),
        listRoleMiningWallets(core).catch(() => [] as WalletV2[]),
        fetchMiningProtocolStats(core, cluster).catch(() => null),
        fetchMiningCandles(core, candleInterval, 120).catch(() => null),
        listUniversalTasks(core, { goal: "mining", limit: 30 }).catch(
          () => [] as UniversalTask[],
        ),
      ]);
      setStatus(st);
      setCountdownSec(st.round.remainingSec);
      setWallets(miningWallets);
      setMiningTasks(mTasks);
      if (ps) setProtoStats(ps);
      if (cd?.candles) setCandles(cd.candles);
      if (st.savedStrategies) setSavedStrategies(st.savedStrategies);

      const activeWalletId =
        selectedWalletId && miningWallets.some((w) => w.id === selectedWalletId)
          ? selectedWalletId
          : (miningWallets[0]?.id ?? "");
      setSelectedWalletId(activeWalletId);

      const [pf, stk, lb, ss] = await Promise.all([
        fetchMiningPortfolio(core, cluster, activeWalletId).catch(() => null),
        fetchMiningStaking(core, cluster, activeWalletId).catch(() => null),
        fetchMiningLeaderboard(core, cluster, lbWindow).catch(() => null),
        listSavedMiningStrategies(core).catch(() => null),
      ]);
      if (pf) setPortfolio(pf);
      if (stk) setStaking(stk);
      if (lb) setLeaderboard(lb);
      if (ss?.strategies) setSavedStrategies(ss.strategies);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : t("Failed to load mining status"),
      );
    }
  }, [core, cluster, candleInterval, lbWindow, selectedWalletId, t]);

  useEffect(() => {
    void loadState();
  }, [loadState]);

  const reloadRef = useRef(loadState);
  reloadRef.current = loadState;

  useEffect(() => {
    if (!streamClient || core.mode !== "ready") return;
    let timer: number | undefined;
    const scheduleReload = () => {
      if (timer !== undefined) window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        timer = undefined;
        void reloadRef.current();
      }, 250);
    };
    const unsubMining = streamClient.subscribe("mining", scheduleReload);
    const unsubTasks = streamClient.subscribe("tasks", scheduleReload);
    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
      unsubMining();
      unsubTasks();
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

  function maybeHandleStepUp(
    err: unknown,
    action:
      "policy" | "execute" | "mining-session" | "wallet" | "task-activate",
    retry: (tok?: string) => Promise<void>,
    existingToken?: string,
    targetId?: string,
  ): boolean {
    const code = err instanceof CoreError ? err.code : undefined;
    const bindingDigest =
      err instanceof CoreError ? err.bindingDigest : undefined;
    const msg = err instanceof Error ? err.message : String(err);
    if (code === "owner_password_required") {
      setStepUpError("");
      setStepUpPending({
        action,
        mode: "password_setup",
        bindingDigest,
        targetId,
        retry,
      });
      return true;
    }
    if (
      !existingToken &&
      (code === "step_up_required" ||
        msg.toLowerCase().includes("step-up") ||
        msg.toLowerCase().includes("step_up"))
    ) {
      setStepUpError("");
      setStepUpPending({
        action,
        mode: "step_up",
        bindingDigest,
        targetId,
        retry,
      });
      return true;
    }
    setError(msg);
    return false;
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
        const plannedSlots = computeScheduledSlots(
          scheduleDraft,
          status?.round.roundSlot ?? 0,
        ).slice(0, Math.max(1, roundsCount));
        await createMiningSession(
          core,
          {
            walletId: selectedWalletId,
            walletHandle: wallet?.handle ?? "",
            cluster,
            strategyId: presetId,
            mode,
            scheduleMode: scheduleDraft.scheduleMode,
            horizonLabel: scheduleDraft.horizonLabel,
            startMode: scheduleDraft.startMode,
            selectedSlots: plannedSlots,
            roundsCount,
            perRoundSol: parsedAmount,
            depositSol: parsedAmount * Math.max(1, roundsCount),
            rollingBudget: scheduleDraft.rollingBudget,
            autoClaim,
            stopOnReversal,
            curve: weightsBpsToCurve80(weightsBps),
          },
          stepUpToken,
        );
        setNotice(
          `Started ${mode === "auto" ? "Auto" : "Semi-Auto"} session (${presetId}, ${amountStr} SOL/round × ${roundsCount}r).`,
        );
        setRecordsTab("sessions");
      }
      setStepUpPending(null);
      setStepUpPassword("");
      setStepUpPasswordConfirm("");
      await loadState();
    } catch (err) {
      maybeHandleStepUp(
        err,
        mode === "manual" ? "execute" : "mining-session",
        (tok) => runPrimaryAction(tok),
        stepUpToken,
      );
    } finally {
      setBusy(false);
    }
  }

  async function handleClaimAsset(
    asset: "sol" | "sat" | "btc",
    item?: ClaimableRoundItem,
    stepUpToken?: string,
  ) {
    if (!selectedWalletId) {
      setError(t("Select a Mining role wallet first."));
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const slot =
        item?.roundSlot ??
        portfolio?.claimItems?.[0]?.roundSlot ??
        status?.streak?.[0]?.roundSlot ??
        status?.round.roundSlot ??
        0;
      await submitMiningClaim(
        core,
        {
          walletId: selectedWalletId,
          cluster,
          asset,
          roundSlot: slot,
          shardId: item?.shardId ?? 0,
          page: item?.page ?? 0,
          entryRow: item?.entryRow ?? 0,
          createAta: asset !== "sol",
        },
        stepUpToken,
      );
      setNotice(`Submitted ${asset.toUpperCase()} claim for round #${slot}.`);
      setStepUpPending(null);
      setStepUpPassword("");
      setStepUpPasswordConfirm("");
      await loadState();
    } catch (err) {
      maybeHandleStepUp(
        err,
        "execute",
        (tok) => handleClaimAsset(asset, item, tok),
        stepUpToken,
      );
    } finally {
      setBusy(false);
    }
  }

  async function handleClaimAll() {
    if (!selectedWalletId) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const res = await submitMiningClaimAll(core, selectedWalletId, cluster);
      setNotice(
        `Submitted ${res.count} claim transactions across SOL/SAT/cbBTC.`,
      );
      await loadState();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Claim All failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleStakeAction(
    kind: "stake" | "unstake" | "activate" | "claim_btc",
    amountSat = 0,
    stepUpToken?: string,
  ) {
    if (!selectedWalletId) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      if (kind === "stake") {
        await submitMiningStake(
          core,
          { walletId: selectedWalletId, cluster, amountSat },
          stepUpToken,
        );
        setNotice(`Staked ${amountSat} WENSAT.`);
      } else if (kind === "unstake") {
        await submitMiningUnstake(
          core,
          { walletId: selectedWalletId, cluster, amountSat, createAta: true },
          stepUpToken,
        );
        setNotice(`Unstaked ${amountSat} WENSAT.`);
      } else if (kind === "activate") {
        await submitMiningActivateStake(
          core,
          {
            walletId: selectedWalletId,
            cluster,
          },
          stepUpToken,
        );
        setNotice("Activated matured stake position.");
      } else {
        await submitMiningClaimStaking(
          core,
          {
            walletId: selectedWalletId,
            cluster,
            createAta: true,
          },
          stepUpToken,
        );
        setNotice("Claimed staking cbBTC rewards.");
      }
      setStepUpPending(null);
      setStepUpPassword("");
      setStepUpPasswordConfirm("");
      await loadState();
    } catch (err) {
      maybeHandleStepUp(
        err,
        "execute",
        (tok) => handleStakeAction(kind, amountSat, tok),
        stepUpToken,
      );
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
          confirmLoosen: true,
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
      setStepUpPasswordConfirm("");
      setNotice(t("Updated Mining wallet policy."));
      await loadState();
    } catch (err) {
      maybeHandleStepUp(err, "policy", (tok) => savePolicy(tok), stepUpToken);
    } finally {
      setBusy(false);
    }
  }

  async function submitStepUp() {
    if (!stepUpPending || !stepUpPassword.trim()) {
      setStepUpError(t("Owner password is required."));
      return;
    }
    if (
      stepUpPending.mode === "password_setup" &&
      stepUpPassword !== stepUpPasswordConfirm
    ) {
      setStepUpError(t("Passwords do not match."));
      return;
    }
    setStepUpError("");
    try {
      if (stepUpPending.mode === "password_setup") {
        await setLocalOwnerPassword(core, stepUpPassword);
      }
      if (
        stepUpPending.bindingDigest !== undefined ||
        stepUpPending.mode === "step_up"
      ) {
        const token =
          stepUpPending.action === "task-activate"
            ? await requestTaskStepUpToken(
                core,
                stepUpPassword,
                stepUpPending.targetId ?? selectedWalletId,
              )
            : await requestMiningStepUpToken(
                core,
                stepUpPassword,
                stepUpPending.action,
                selectedWalletId,
                stepUpPending.bindingDigest ?? "",
              );
        await stepUpPending.retry(token);
      } else {
        await stepUpPending.retry(undefined);
      }
    } catch (err) {
      setStepUpError(
        err instanceof Error ? err.message : t("Step-up verification failed"),
      );
    }
  }

  function handleShareCurve() {
    const pcts = weightsBps.map((w) => Math.round(w / 100)).join(",");
    const link = `${window.location.origin}/#mine?curve=${pcts}`;
    void navigator.clipboard?.writeText(link);
    setNotice(`Copied shared curve URL (?curve=${pcts}).`);
  }

  async function handleSaveCurrentStrategy() {
    setBusy(true);
    try {
      await saveMiningStrategy(core, {
        name: saveStratName.trim() || "Custom Strategy",
        description: `${presetId} curve saved from #mine`,
        bucketsBps: weightsBps,
      });
      setSaveStratOpen(false);
      setNotice("Saved strategy to library.");
      await loadState();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save strategy");
    } finally {
      setBusy(false);
    }
  }

  function openAutomateDialog() {
    const presetLabel =
      presets.find((p) => p.id === presetId)?.name ?? presetId;
    setAutoName(`Mining · ${presetLabel}`);
    setAutoExecMode(mode === "auto" ? "hybrid" : "standalone");
    setAutoApproval(mode === "auto" ? "auto" : "semi_auto");
    setAutoSchedule("interval");
    setAutoIntervalSec("300");
    setAutoEventFilter("mining.round.opened");
    setAutomateOpen(true);
  }

  async function submitAutomateTask(stepUpToken?: string) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const ivSec = Math.max(15, Math.round(Number(autoIntervalSec) || 300));
      const scheduleSpec =
        autoSchedule === "interval"
          ? { intervalSec: ivSec }
          : autoSchedule === "continuous"
            ? { intervalSec: 60, eventFilter: autoEventFilter.trim() }
            : { eventFilter: autoEventFilter.trim() || "mining.round.opened" };
      const created = await createUniversalTask(
        core,
        {
          name: autoName.trim() || `Mining · ${presetId}`,
          goalDomain: "mining",
          goalObjective: `Automate ${presetId} LV1X mining (${amountStr} SOL/round on ${cluster})`,
          scheduleKind: autoSchedule,
          scheduleSpec,
          execMode: autoExecMode,
          approvalPolicy: autoApproval,
          executionBinding: {
            walletHandle:
              currentWallet?.handle || currentWallet?.id || selectedWalletId,
            venue: "wen_lv1x",
            cluster,
          },
          attachments: {
            personaId:
              autoExecMode === "standalone" ? undefined : "mining_strategist",
            skillIds:
              autoExecMode === "standalone"
                ? []
                : ["sk-mining-trend", "sk-mining-claim"],
            strategyId: presetId,
          },
          constraints: {
            stopOnReversal,
            cooldownSec: 30,
          },
          status: "active",
          createdVia: "ui",
        },
        stepUpToken,
      );
      setAutomateOpen(false);
      setTasksDrawerOpen(true);
      setStepUpPending(null);
      setStepUpPassword("");
      setStepUpPasswordConfirm("");
      setNotice(`Created mining task: ${created.name}`);
      await loadState();
    } catch (err) {
      maybeHandleStepUp(
        err,
        "task-activate",
        (tok) => submitAutomateTask(tok),
        stepUpToken,
        currentWallet?.id || selectedWalletId || "mining",
      );
    } finally {
      setBusy(false);
    }
  }

  async function handleToggleMiningTask(
    task: UniversalTask,
    stepUpToken?: string,
  ) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      if (task.status === "active") {
        await pauseUniversalTask(core, task.id, "Paused from #mine drawer");
        setNotice(`Paused task: ${task.name}`);
      } else {
        await activateUniversalTask(core, task.id, stepUpToken);
        setNotice(`Activated task: ${task.name}`);
      }
      setStepUpPending(null);
      setStepUpPassword("");
      setStepUpPasswordConfirm("");
      await loadState();
    } catch (err) {
      maybeHandleStepUp(
        err,
        "task-activate",
        (tok) => handleToggleMiningTask(task, tok),
        stepUpToken,
        task.id,
      );
    } finally {
      setBusy(false);
    }
  }

  async function handleRunMiningTask(task: UniversalTask) {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const run = await runUniversalTaskNow(core, task.id);
      setNotice(
        `${task.name}: ${run.decisionSummary || run.actionTaken || run.status}`,
      );
      await loadState();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : t("Failed to execute task"),
      );
    } finally {
      setBusy(false);
    }
  }

  const mainnetLocked =
    cluster === "mainnet-beta" && !(status?.mainnetEnabled ?? false);
  const activeMiningTaskCount = miningTasks.filter(
    (tk) => tk.status === "active",
  ).length;
  const round = status?.round;
  const moveBps = round?.moveBps ?? 0;
  const currentWallet = wallets.find((w) => w.id === selectedWalletId);

  const navSubTabs: Array<{ id: MineSubRoute; label: string }> = [
    { id: "mine", label: "Mine" },
    { id: "assets", label: "Assets" },
    { id: "strategies", label: "Strategies" },
    { id: "portfolio", label: "Portfolio" },
    { id: "stake", label: "Stake" },
  ];

  return (
    <section className="mine-shell" aria-label={t("Mine")}>
      <header className="mine-topbar">
        <div className="mine-title-row">
          <Pickaxe size={18} aria-hidden="true" />
          <h1>{t("Mine")}</h1>

          <nav className="mine-subnav" aria-label="Mining workspace tabs">
            {navSubTabs.map((item) => (
              <button
                key={item.id}
                type="button"
                className="mine-chip"
                aria-pressed={subRoute === item.id}
                onClick={() =>
                  navigate(item.id === "mine" ? "mine" : `mine/${item.id}`)
                }
              >
                {item.label}
              </button>
            ))}
          </nav>

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
              <span>{round?.bpfVerified ? "BPF Verified" : "Cached"}</span>
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
          <button
            type="button"
            className="mine-chip mine-tasks-pill"
            aria-expanded={tasksDrawerOpen}
            aria-label="Mining tasks drawer"
            onClick={() => setTasksDrawerOpen((v) => !v)}
          >
            <Zap size={13} aria-hidden="true" />
            <span>Tasks · {activeMiningTaskCount}</span>
          </button>
          <button
            type="button"
            className="mine-chip"
            aria-label="Automate mining task"
            onClick={openAutomateDialog}
          >
            <Bot size={13} aria-hidden="true" />
            <span>Automate</span>
          </button>
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
            className="icon-btn"
            aria-label="Open mining rules"
            title="Mining rules"
            onClick={() => setRulesOpen(true)}
          >
            <BookOpen size={14} aria-hidden="true" />
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

      {tasksDrawerOpen && (
        <div
          className="wt-panel mine-tasks-drawer"
          role="region"
          aria-label="Mining tasks"
        >
          <div className="mine-tasks-drawer-head">
            <strong>
              Mining Tasks ({activeMiningTaskCount}/{miningTasks.length} active)
            </strong>
            <div className="mine-task-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={openAutomateDialog}
              >
                + Automate
              </button>
              <button
                type="button"
                className="icon-btn"
                aria-label="Open Tasks workspace"
                title="Open #tasks"
                onClick={() => navigate("tasks")}
              >
                <ArrowUpRight size={14} aria-hidden="true" />
              </button>
            </div>
          </div>
          {miningTasks.length === 0 ? (
            <p className="mine-muted-note">
              No mining tasks configured yet. Use Automate to create one from
              your active curve.
            </p>
          ) : (
            <div className="wt-hairline-table">
              {miningTasks.map((tk) => (
                <div key={tk.id} className="wt-hairline-row mine-task-row">
                  <div className="mine-task-meta">
                    <span className="mine-task-title">{tk.name}</span>
                    <span className="mine-muted-note">
                      {tk.execMode} · {tk.scheduleKind} · {tk.approvalPolicy} ·{" "}
                      {tk.runCount} runs
                    </span>
                  </div>
                  <div className="mine-task-actions">
                    <span className={`wt-pill ${statusPillTone(tk.status)}`}>
                      {tk.status}
                    </span>
                    <button
                      type="button"
                      className="icon-btn"
                      aria-label={
                        tk.status === "active"
                          ? `Pause task ${tk.name}`
                          : `Activate task ${tk.name}`
                      }
                      title={tk.status === "active" ? "Pause" : "Activate"}
                      disabled={busy}
                      onClick={() => void handleToggleMiningTask(tk)}
                    >
                      {tk.status === "active" ? (
                        <Pause size={13} aria-hidden="true" />
                      ) : (
                        <Play size={13} aria-hidden="true" />
                      )}
                    </button>
                    <button
                      type="button"
                      className="icon-btn"
                      aria-label={`Run task ${tk.name}`}
                      title="Run once"
                      disabled={busy}
                      onClick={() => void handleRunMiningTask(tk)}
                    >
                      <Zap size={13} aria-hidden="true" />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {subRoute === "mine" && (
        <div className="mine-grid-3col">
          <MineLeftRail
            round={round}
            stats={protoStats}
            portfolio={portfolio}
            selectedWalletId={selectedWalletId}
            onOpenMarkets={() => setMarketsOpen(true)}
            onOpenRules={() => setRulesOpen(true)}
            onOpenClaim={() => {
              setComposerTab("claim");
              setRecordsTab("claims");
            }}
            onApplyCrowd={(w) => {
              setPresetId("custom");
              setSelectedBucket(null);
              setWeightsBps(w);
              setNotice("Applied live crowd distribution curve.");
            }}
            onNavigateSub={(sub) => navigate(`mine/${sub}`)}
          />

          <div className="mine-center-col">
            <MineMarketChart
              candles={candles}
              interval={candleInterval}
              onIntervalChange={setCandleInterval}
              round={round}
              streak={status?.streak ?? []}
              crowdWeights={crowdWeightsBps}
              onApplyPresetFromTechnicals={(w) => {
                setPresetId("custom");
                setWeightsBps(w);
                setNotice("Applied technical bias curve.");
              }}
            />

            <MineRecordsPanel
              activeTab={recordsTab}
              onTabChange={setRecordsTab}
              round={round}
              weightsBps={weightsBps}
              sessions={status?.sessions ?? []}
              portfolio={portfolio}
              protocolRounds={leaderboard?.rounds ?? []}
              streak={status?.streak ?? []}
              cluster={cluster}
              mainnetGateNote={status?.mainnetGateNote}
              selectedWalletId={selectedWalletId}
              busy={busy}
              onDepositSession={(id, sol) =>
                void depositMiningSession(core, id, sol).then(loadState)
              }
              onStopSession={(id) =>
                void stopMiningSession(core, id).then(loadState)
              }
              onCloseSession={(id) =>
                void closeMiningSession(core, id).then(loadState)
              }
              onClaimAsset={(asset, item) => void handleClaimAsset(asset, item)}
              onClaimAll={() => void handleClaimAll()}
              onLoadCurveFromSlot={(sel) => {
                const next = [500, 500, 500, 500, 500];
                next[Math.max(0, Math.min(4, sel))] = 8000;
                setPresetId("custom");
                setWeightsBps(next);
                setNotice(
                  `Loaded Range ${String.fromCharCode(65 + sel)} bias.`,
                );
              }}
            />
          </div>

          <MineComposer
            composerTab={composerTab}
            onComposerTabChange={setComposerTab}
            mode={mode}
            onModeChange={setMode}
            presets={presets}
            presetId={presetId}
            weightsBps={weightsBps}
            selectedBucket={selectedBucket}
            round={round}
            roundsCount={roundsCount}
            onQuickRounds={(n) => {
              setRoundsCount(n);
              if (n > 1 && mode === "manual") setMode("semi_auto");
            }}
            onOpenScheduleGrid={() => setScheduleOpen(true)}
            amountStr={amountStr}
            onAmountChange={setAmountStr}
            walletSolBalance={portfolio?.solBalanceSol ?? 0}
            rollingBudget={scheduleDraft.rollingBudget}
            autoClaim={autoClaim}
            onAutoClaimChange={setAutoClaim}
            stopOnReversal={stopOnReversal}
            onStopOnReversalChange={setStopOnReversal}
            wallets={wallets}
            selectedWalletId={selectedWalletId}
            onSelectPreset={applyPreset}
            onCurveRebalance={handleCurveRebalance}
            onSelectBucket={handleBucketClick}
            onShareCurve={handleShareCurve}
            onOpenSaveStrategy={() => setSaveStratOpen(true)}
            onOpenPolicy={() => setPolicyOpen(true)}
            onCreateTask={openAutomateDialog}
            onPrimarySubmit={() => void runPrimaryAction()}
            onClaimAsset={(asset) => void handleClaimAsset(asset)}
            onClaimAll={() => void handleClaimAll()}
            onCopyMint={(mint) => {
              void navigator.clipboard?.writeText(mint);
              setNotice("Copied WENSAT mint address.");
            }}
            onOpenJupiter={() => navigate("terminals/jupiter")}
            portfolio={portfolio}
            preview={preview}
            countdownSec={countdownSec}
            mainnetLocked={mainnetLocked}
            mainnetGateNote={status?.mainnetGateNote}
            mintAddress={protoStats?.mintAddress ?? round?.ledgerId ?? ""}
            btcMintAddress={protoStats?.btcMintAddress ?? ""}
            busy={busy}
            notice={notice}
            error={error}
          />
        </div>
      )}

      {subRoute === "assets" && (
        <MineAssetsPage
          leaderboard={leaderboard}
          stats={protoStats}
          windowChoice={lbWindow}
          onWindowChange={setLbWindow}
        />
      )}

      {subRoute === "strategies" && (
        <MineStrategiesPage
          presets={presets}
          savedStrategies={savedStrategies}
          onSaveStrategy={async (inp) => {
            await saveMiningStrategy(core, inp);
            await loadState();
          }}
          onDeleteStrategy={async (id) => {
            await deleteMiningStrategy(core, id);
            await loadState();
          }}
          onSimulate={(b, sol) =>
            simulateMiningStrategy(core, {
              bucketsBps: b,
              perRoundSol: sol,
              limit: 24,
            })
          }
          onUseInMine={(id, weights) => {
            const matched = presets.some((p) => p.id === id);
            setPresetId(matched ? id : "custom");
            setSelectedBucket(null);
            setWeightsBps([...weights]);
            navigate("mine");
          }}
          onLaunchTask={openAutomateDialog}
        />
      )}

      {subRoute === "portfolio" && (
        <MinePortfolioPage
          portfolio={portfolio}
          wallet={currentWallet}
          busy={busy}
          onClaimAll={() => void handleClaimAll()}
          onOpenPolicy={() => setPolicyOpen(true)}
        />
      )}

      {subRoute === "stake" && (
        <MineStakePage
          staking={staking}
          selectedWalletId={selectedWalletId}
          mainnetLocked={mainnetLocked}
          busy={busy}
          onStake={(sat) => void handleStakeAction("stake", sat)}
          onUnstake={(sat) => void handleStakeAction("unstake", sat)}
          onActivate={() => void handleStakeAction("activate")}
          onClaimBtc={() => void handleStakeAction("claim_btc")}
        />
      )}

      {scheduleOpen && (
        <MineScheduleGrid
          draft={scheduleDraft}
          currentSlot={round?.roundSlot ?? 0}
          perRoundSol={parsedAmount}
          depositSol={parsedAmount * roundsCount}
          onChange={(next) => {
            setScheduleDraft(next);
            const cnt = computeScheduledSlots(
              next,
              round?.roundSlot ?? 0,
            ).length;
            setRoundsCount(Math.max(1, cnt));
            if (cnt > 1 && mode === "manual") setMode("semi_auto");
          }}
          onClose={() => setScheduleOpen(false)}
        />
      )}

      {marketsOpen && (
        <Dialog title="Mining Markets" onClose={() => setMarketsOpen(false)}>
          <div className="wt-hairline-table">
            <div className="wt-hairline-row">
              <strong>WEN Satcoin · BTC/USD</strong>
              <span>10m LV1X · {cluster}</span>
            </div>
            <div className="wt-hairline-row">
              <span>Range Bounds</span>
              <span>
                ±{round?.innerBps ?? 5} bps / ±{round?.outerBps ?? 20} bps
              </span>
            </div>
          </div>
        </Dialog>
      )}

      {rulesOpen && (
        <Dialog title="LV1X Mining Rules" onClose={() => setRulesOpen(false)}>
          <div className="mine-modal-form">
            <p className="mine-muted-note">
              Each 10-minute round settles BTC/USD move into five buckets (A–E)
              at ±{round?.innerBps ?? 5} / ±{round?.outerBps ?? 20} bps. Exact
              hits retain 99.5% SOL and earn max credit weight toward WENSAT and
              cbBTC yield; adjacent near-misses earn pro-rata credit; misses
              return 90% SOL and fund winner forfeitures.
            </p>
            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-rose-sm"
                onClick={() => setRulesOpen(false)}
              >
                Got it
              </button>
            </div>
          </div>
        </Dialog>
      )}

      {saveStratOpen && (
        <Dialog
          title="Save Mining Strategy"
          onClose={() => setSaveStratOpen(false)}
        >
          <div className="mine-modal-form">
            <div className="mine-field">
              <label htmlFor="save-strat-name">Strategy Name</label>
              <input
                id="save-strat-name"
                className="mine-modal-input"
                value={saveStratName}
                onChange={(e) => setSaveStratName(e.target.value)}
              />
            </div>
            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => setSaveStratOpen(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                disabled={busy}
                onClick={() => void handleSaveCurrentStrategy()}
              >
                Save Strategy
              </button>
            </div>
          </div>
        </Dialog>
      )}

      {policyOpen && (
        <Dialog
          title="Mining Wallet Policy"
          onClose={() => setPolicyOpen(false)}
        >
          <div className="mine-modal-form">
            <div className="mine-field">
              <label htmlFor="pol-round-cap">Max SOL Per Round</label>
              <input
                id="pol-round-cap"
                className="mine-modal-input"
                value={policyMaxRoundSol}
                onChange={(e) => setPolicyMaxRoundSol(e.target.value)}
              />
            </div>
            <div className="mine-field">
              <label htmlFor="pol-daily-cap">Max Daily Mining SOL</label>
              <input
                id="pol-daily-cap"
                className="mine-modal-input"
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
              <span>Permit Semi-Auto unattended claims</span>
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

      {automateOpen && (
        <Dialog
          title="Automate Mining Task"
          onClose={() => setAutomateOpen(false)}
        >
          <div className="mine-modal-form">
            <div className="mine-field">
              <label htmlFor="mine-auto-name">Task Name</label>
              <input
                id="mine-auto-name"
                className="mine-modal-input"
                value={autoName}
                onChange={(e) => setAutoName(e.target.value)}
              />
            </div>
            <div className="mine-automate-grid">
              <div className="mine-field">
                <label htmlFor="mine-auto-mode">Engine Mode</label>
                <select
                  id="mine-auto-mode"
                  className="mine-modal-input"
                  value={autoExecMode}
                  onChange={(e) => setAutoExecMode(e.target.value as ExecMode)}
                >
                  <option value="standalone">Standalone (No AI)</option>
                  <option value="hybrid">Hybrid (Algo + AI Check)</option>
                  <option value="ai">AI Strategist</option>
                </select>
              </div>
              <div className="mine-field">
                <label htmlFor="mine-auto-approval">Approval</label>
                <select
                  id="mine-auto-approval"
                  className="mine-modal-input"
                  value={autoApproval}
                  onChange={(e) =>
                    setAutoApproval(e.target.value as ApprovalPolicy)
                  }
                >
                  <option value="semi_auto">Semi-Auto</option>
                  <option value="manual">Manual</option>
                  <option value="auto">Autonomous</option>
                </select>
              </div>
              <div className="mine-field">
                <label htmlFor="mine-auto-schedule">Trigger</label>
                <select
                  id="mine-auto-schedule"
                  className="mine-modal-input"
                  value={autoSchedule}
                  onChange={(e) =>
                    setAutoSchedule(e.target.value as ScheduleKind)
                  }
                >
                  <option value="interval">Interval</option>
                  <option value="continuous">Continuous + Event</option>
                  <option value="event">Event Push Only</option>
                </select>
              </div>
              {autoSchedule === "interval" ? (
                <div className="mine-field">
                  <label htmlFor="mine-auto-interval">Interval (sec)</label>
                  <input
                    id="mine-auto-interval"
                    type="number"
                    min={15}
                    className="mine-modal-input"
                    value={autoIntervalSec}
                    onChange={(e) => setAutoIntervalSec(e.target.value)}
                  />
                </div>
              ) : (
                <div className="mine-field">
                  <label htmlFor="mine-auto-event">Event Filter</label>
                  <input
                    id="mine-auto-event"
                    className="mine-modal-input"
                    value={autoEventFilter}
                    onChange={(e) => setAutoEventFilter(e.target.value)}
                  />
                </div>
              )}
            </div>
            <div className="mine-muted-note">
              Binding: {currentWallet?.handle || "default mining"} · {cluster} ·{" "}
              {presetId} ({amountStr} SOL)
            </div>
            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => setAutomateOpen(false)}
              >
                Cancel
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                disabled={busy}
                onClick={() => void submitAutomateTask()}
              >
                Create &amp; Activate
              </button>
            </div>
          </div>
        </Dialog>
      )}

      {stepUpPending && (
        <Dialog
          title={
            stepUpPending.mode === "password_setup"
              ? "Set Owner Password"
              : "Owner Step-Up Confirmation"
          }
          onClose={() => setStepUpPending(null)}
        >
          <div className="mine-modal-form">
            {stepUpPending.bindingDigest && (
              <div className="mine-field">
                <span>Digest</span>
                <code className="mine-mono">
                  {stepUpPending.bindingDigest.slice(0, 16)}…
                </code>
              </div>
            )}
            <div className="mine-field">
              <label htmlFor="mine-stepup-pass">Owner Password</label>
              <input
                id="mine-stepup-pass"
                type="password"
                className="mine-modal-input"
                value={stepUpPassword}
                onChange={(e) => setStepUpPassword(e.target.value)}
              />
            </div>
            {stepUpPending.mode === "password_setup" && (
              <div className="mine-field">
                <label htmlFor="mine-stepup-pass-confirm">
                  Confirm Password
                </label>
                <input
                  id="mine-stepup-pass-confirm"
                  type="password"
                  className="mine-modal-input"
                  value={stepUpPasswordConfirm}
                  onChange={(e) => setStepUpPasswordConfirm(e.target.value)}
                />
              </div>
            )}
            {stepUpError && <p className="mine-error">{stepUpError}</p>}
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
                disabled={busy}
                onClick={() => void submitStepUp()}
              >
                Verify &amp; Execute
              </button>
            </div>
          </div>
        </Dialog>
      )}
    </section>
  );
}
