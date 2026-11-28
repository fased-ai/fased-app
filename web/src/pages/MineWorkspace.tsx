import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CoreError } from "../core";
import { Dialog } from "../components/shared";
import { setLocalOwnerPassword } from "../wallets-v2";
import {
  ArrowUpRight,
  Clock3,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Wallet,
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
  fetchMiningManifest,
  fetchMiningPortfolio,
  fetchMiningProtocolStats,
  fetchMiningStaking,
  fetchMiningStatus,
  fetchSelfKeeperStatus,
  listRoleMiningWallets,
  listSavedMiningStrategies,
  requestMiningStepUpToken,
  saveMiningStrategy,
  simulateMiningStrategy,
  stopMiningSession,
  submitMiningActivateStake,
  submitMiningClaim,
  submitMiningClaimAll,
  submitMiningClaimStaking,
  submitMiningCloseNonce,
  submitMiningCommit,
  submitMiningStake,
  submitMiningUnstake,
  syncMiningManifest,
  triggerSelfKeeperSweep,
  updateMiningPolicy,
  type CandleBar,
  type ClaimableRoundItem,
  type LeaderboardResponse,
  type ManifestStatus,
  type MiningStatus,
  type PortfolioResponse,
  type ProtocolStats,
  type SavedStrategy,
  type SelfKeeperStatus,
  type StakingOverviewResponse,
  type StrategyPreset,
} from "../mining-lv1x";
import { useUI } from "../ui-preferences";
import type { WalletV2 } from "../wallets-v2";
import { useWorkspace } from "../workspace";
import {
  RANGE_LABELS,
  RANGE_NAMES,
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
import "../vendor/wen-mining/wen-retail-scoped.css";
import "./MineWorkspace.css";

type ClusterChoice = "devnet" | "mainnet-beta";
type MineSubRoute = "mine" | "assets" | "strategies" | "portfolio" | "stake";

function formatRevealTime(unixSec?: number): string {
  if (!unixSec || unixSec <= 0) return "";
  const d = new Date(unixSec * 1000);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function shortAddr(addr: string): string {
  if (!addr || addr.length <= 10) return addr;
  return `${addr.slice(0, 4)}…${addr.slice(-4)}`;
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

  const [cluster] = useState<ClusterChoice>("devnet");
  const [status, setStatus] = useState<MiningStatus | null>(null);
  const [protoStats, setProtoStats] = useState<ProtocolStats | null>(null);
  const [candles, setCandles] = useState<CandleBar[]>([]);
  const [candleInterval, setCandleInterval] = useState<ChartInterval>("1m");
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
  const [recordsTab, setRecordsTab] = useState<RecordsTab>("entries");
  const [presetId, setPresetId] = useState<string>("wide");
  const [weightsBps, setWeightsBps] = useState<number[]>(
    () => parseSharedCurveFromHash() ?? [2000, 2000, 2000, 2000, 2000],
  );
  const [selectedBucket, setSelectedBucket] = useState<number | null>(null);
  const [amountStr, setAmountStr] = useState("0.1");
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
  const [countdownSec, setCountdownSec] = useState(0);

  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  // Modals
  const [marketsOpen, setMarketsOpen] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [allocModalOpen, setAllocModalOpen] = useState(false);
  const [saveStratOpen, setSaveStratOpen] = useState(false);
  const [saveStratName, setSaveStratName] = useState("My Custom Curve");
  const [walletPolicyOpen, setWalletPolicyOpen] = useState(false);
  const [policyMaxRoundSol, setPolicyMaxRoundSol] = useState("0.5");
  const [policyMaxDailySol, setPolicyMaxDailySol] = useState("5");
  const [policyAutoClaim, setPolicyAutoClaim] = useState(true);
  const [policyAutoStakeWensat, setPolicyAutoStakeWensat] = useState(false);
  const [policyAllowFallbackCrank, setPolicyAllowFallbackCrank] =
    useState(false);
  const [policyAllowMainnet, setPolicyAllowMainnet] = useState(false);
  const [manifest, setManifest] = useState<ManifestStatus | null>(null);
  const [selfKeeper, setSelfKeeper] = useState<SelfKeeperStatus | null>(null);
  const [automationModalOpen, setAutomationModalOpen] = useState(false);
  const [miningTasks, setMiningTasks] = useState<UniversalTask[]>([]);
  const [automateOpen, setAutomateOpen] = useState(false);
  const [autoName, setAutoName] = useState("Mining · Wide");
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

  const plannedSlots = useMemo(() => {
    const cur = status?.round?.roundSlot ?? 0;
    if (cur <= 0) return [];
    const offset =
      scheduleDraft.startMode === "now"
        ? 0
        : scheduleDraft.startMode === "next"
          ? 1
          : 2;
    if (roundsCount <= 1) {
      return offset > 0 ? [cur + offset] : [cur];
    }
    return computeScheduledSlots(scheduleDraft, cur).slice(
      0,
      Math.max(1, roundsCount),
    );
  }, [status?.round?.roundSlot, scheduleDraft, roundsCount]);

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

      const [pf, stk, lb, ss, mf, sk] = await Promise.all([
        fetchMiningPortfolio(core, cluster, activeWalletId).catch(() => null),
        fetchMiningStaking(core, cluster, activeWalletId).catch(() => null),
        fetchMiningLeaderboard(core, cluster, lbWindow).catch(() => null),
        listSavedMiningStrategies(core).catch(() => null),
        fetchMiningManifest(core).catch(() => null),
        fetchSelfKeeperStatus(core).catch(() => null),
      ]);
      if (pf) setPortfolio(pf);
      if (stk) setStaking(stk);
      if (lb) setLeaderboard(lb);
      if (ss?.strategies) setSavedStrategies(ss.strategies);
      if (mf) setManifest(mf);
      if (sk) setSelfKeeper(sk);
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

  function dominantBucketFromWeights(bps: number[]): number {
    let best = 2;
    let bestVal = -1;
    for (let i = 0; i < bps.length; i++) {
      if ((bps[i] ?? 0) > bestVal) {
        bestVal = bps[i] ?? 0;
        best = i;
      }
    }
    return best;
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
    const isMultiRound = roundsCount > 1;
    try {
      if (!isMultiRound) {
        if (!selectedWalletId) {
          setError(t("Attach a Mining wallet first (+ Add Wallet)."));
          return;
        }
        await submitMiningCommit(
          core,
          {
            walletId: selectedWalletId,
            cluster,
            amountSol: parsedAmount,
            selection: selectedBucket ?? dominantBucketFromWeights(weightsBps),
            strategyId: presetId,
            bucketWeightsBps: weightsBps,
            curve: weightsBpsToCurve80(weightsBps),
          },
          stepUpToken,
        );
        setNotice(
          `Prepared commit for round #${status?.round.roundSlot ?? 0} (${amountStr} SOL).`,
        );
      } else {
        const wallet = wallets.find((w) => w.id === selectedWalletId);
        const perRound = parsedAmount / Math.max(1, roundsCount);
        await createMiningSession(
          core,
          {
            walletId: selectedWalletId,
            walletHandle: wallet?.handle ?? "",
            cluster,
            strategyId: presetId,
            mode: "semi_auto",
            scheduleMode: scheduleDraft.scheduleMode,
            horizonLabel: scheduleDraft.horizonLabel,
            startMode: scheduleDraft.startMode,
            selectedSlots: plannedSlots,
            roundsCount,
            perRoundSol: perRound,
            depositSol: parsedAmount,
            rollingBudget: scheduleDraft.rollingBudget,
            autoClaim: policyAutoClaim,
            stopOnReversal: false,
            curve: weightsBpsToCurve80(weightsBps),
          },
          stepUpToken,
        );
        setNotice(
          `Started ${roundsCount}r session (${presetId}, ${perRound.toFixed(3)} SOL/r).`,
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
        isMultiRound ? "mining-session" : "execute",
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
      setError(t("Attach a Mining wallet first."));
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
        `Submitted ${res.count} claim transactions across SOL/SAT/BTC.`,
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
          { walletId: selectedWalletId, cluster },
          stepUpToken,
        );
        setNotice("Activated matured stake position.");
      } else {
        await submitMiningClaimStaking(
          core,
          { walletId: selectedWalletId, cluster, createAta: true },
          stepUpToken,
        );
        setNotice("Claimed staking BTC rewards.");
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

  function openWalletPolicyModal() {
    const mp = currentWallet?.policy.mining;
    if (mp) {
      setPolicyMaxRoundSol(
        (Math.max(1e7, Number(mp.maxSolPerRoundLamports) || 5e8) / 1e9).toFixed(
          2,
        ),
      );
      setPolicyMaxDailySol(
        (Math.max(5e7, Number(mp.maxDailySolLamports) || 5e9) / 1e9).toFixed(2),
      );
      setPolicyAutoClaim(mp.autoClaim ?? true);
      setPolicyAutoStakeWensat(mp.autoStakeWensat ?? false);
      setPolicyAllowFallbackCrank(mp.allowFallbackCrank ?? false);
      setPolicyAllowMainnet(mp.allowMainnet ?? false);
    }
    setWalletPolicyOpen(true);
  }

  async function handleCloseNonce(
    item: ClaimableRoundItem,
    stepUpToken?: string,
  ) {
    if (!selectedWalletId) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await submitMiningCloseNonce(
        core,
        {
          walletId: selectedWalletId,
          cluster,
          roundSlot: item.roundSlot,
          nonce: item.nonce,
        },
        stepUpToken,
      );
      setNotice(
        `Closed nonce PDA and reclaimed rent for round #${item.roundSlot}.`,
      );
      setStepUpPending(null);
      await loadState();
    } catch (err) {
      maybeHandleStepUp(
        err,
        "execute",
        (tok) => handleCloseNonce(item, tok),
        stepUpToken,
      );
    } finally {
      setBusy(false);
    }
  }

  async function handleTriggerSweep() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const sk = await triggerSelfKeeperSweep(core);
      setSelfKeeper(sk);
      setNotice(
        sk.lastSweepSummary || "Completed background mining task sweep.",
      );
      await loadState();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Task sweep failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleSyncManifest() {
    if (core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      const res = await syncMiningManifest(core);
      setManifest(res.manifest);
      setNotice(
        res.synced
          ? `Manifest seq #${res.manifest.sequence} verified.`
          : res.error || "Manifest pinned without remote change.",
      );
      await loadState();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Manifest sync failed");
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
      const activeWallet = wallets.find((w) => w.id === selectedWalletId);
      await updateMiningPolicy(
        core,
        {
          walletId: selectedWalletId,
          maxPerTxLamports: perRoundLamports,
          maxDailyLamports: dailyLamports,
          confirmLoosen: true,
          expectedPolicyHash: activeWallet?.policy.policyHash,
          mining: {
            cluster: policyAllowMainnet ? "mainnet-beta" : cluster,
            maxSolPerRoundLamports: perRoundLamports,
            maxDailySolLamports: dailyLamports,
            allowedStrategies: presets.map((p) => p.id),
            autoClaim: policyAutoClaim,
            autoStakeWensat: policyAutoStakeWensat,
            allowFallbackCrank: policyAllowFallbackCrank,
            allowMainnet: policyAllowMainnet,
          },
        },
        stepUpToken,
      );
      setWalletPolicyOpen(false);
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
    setAutoExecMode("hybrid");
    setAutoApproval("semi_auto");
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
          goalObjective: `Automate ${presetId} LV1X mining (${amountStr} SOL on ${cluster})`,
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
            stopOnReversal: false,
            cooldownSec: 30,
          },
          status: "active",
          createdVia: "ui",
        },
        stepUpToken,
      );
      setAutomateOpen(false);
      setAutomationModalOpen(true);
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
        await pauseUniversalTask(core, task.id, "Paused from #mine");
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
  const currentWallet = wallets.find((w) => w.id === selectedWalletId) ?? null;
  const revealUntilStr = formatRevealTime(selfKeeper?.nextRevealAt);
  const stakedSat =
    (staking?.userActiveSat ?? 0) + (staking?.userPendingSat ?? 0);

  const navSubTabs: Array<{ id: MineSubRoute; label: string }> = [
    { id: "mine", label: "Mine" },
    { id: "assets", label: "Assets" },
    { id: "stake", label: "Stake" },
    { id: "portfolio", label: "Portfolio" },
    { id: "strategies", label: "Strategies" },
  ];

  return (
    <section
      className={`wrx fased-mine-root mine-shell${subRoute === "mine" ? " is-mine-terminal" : ""}`}
      aria-label={t("Mine")}
    >
      <h1 className="sr-only">{t("Mine")}</h1>

      <header className="mine-topbar">
        <div className="mine-title-row">
          <nav className="mine-subnav" aria-label="Mining workspace tabs">
            {navSubTabs.map((item) => (
              <button
                key={item.id}
                type="button"
                className={`mine-subnav-tab${subRoute === item.id ? " is-active" : ""}`}
                aria-current={subRoute === item.id ? "page" : undefined}
                onClick={() =>
                  navigate(item.id === "mine" ? "mine" : `mine/${item.id}`)
                }
              >
                {item.label}
              </button>
            ))}
          </nav>

          {cluster !== "mainnet-beta" && (
            <span className="mine-devnet-badge">DEVNET</span>
          )}
        </div>

        <div className="mine-top-actions">
          {revealUntilStr && (
            <span
              className="mine-awake-pill"
              title="Keep machine awake until Auto-Reveal window closes"
            >
              <Clock3 size={12} aria-hidden="true" />
              <span>Keep awake until {revealUntilStr}</span>
            </span>
          )}

          <button
            type="button"
            className={`mine-icon-pill${selfKeeper?.running || activeMiningTaskCount > 0 ? " is-active" : ""}`}
            aria-label="Mining tasks and automation"
            title="Mining Tasks & Automation"
            onClick={() => setAutomationModalOpen(true)}
          >
            <Zap size={14} aria-hidden="true" />
            {(selfKeeper?.running || activeMiningTaskCount > 0) && (
              <i className="mine-icon-dot" aria-hidden="true" />
            )}
            {activeMiningTaskCount > 0 && (
              <em className="mine-icon-count">{activeMiningTaskCount}</em>
            )}
          </button>

          {currentWallet ? (
            <button
              type="button"
              className="mine-wallet-pill"
              aria-label="Attached mining wallet"
              title="Attached Mining wallet & policy"
              onClick={openWalletPolicyModal}
            >
              <Wallet size={13} aria-hidden="true" />
              <span className="mine-wallet-addr">
                {shortAddr(currentWallet.publicKey)}
              </span>
              <span className="mine-wallet-bal">
                {(portfolio?.solBalanceSol ?? 0).toFixed(2)} SOL
              </span>
            </button>
          ) : (
            <button
              type="button"
              className="mine-add-wallet-btn"
              aria-label="Add mining wallet"
              title="Attach a Mining wallet"
              onClick={() => navigate("wallets/accounts")}
            >
              <Plus size={13} aria-hidden="true" />
              <span>Add Wallet</span>
            </button>
          )}

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

      {subRoute === "mine" && (
        <div className="mine-grid-3col">
          <MineLeftRail
            round={round}
            stats={protoStats}
            portfolio={portfolio}
            stakedSat={stakedSat}
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
              userWeightsBps={weightsBps}
              plannedSlots={plannedSlots}
              onApplyPresetFromTechnicals={(w) => {
                setPresetId("custom");
                setWeightsBps(w);
                setNotice("Applied distribution curve.");
              }}
            />

            <MineRecordsPanel
              activeTab={recordsTab}
              onTabChange={setRecordsTab}
              round={round}
              stats={protoStats}
              staking={staking}
              sessions={status?.sessions ?? []}
              portfolio={portfolio}
              protocolRounds={leaderboard?.rounds ?? []}
              streak={status?.streak ?? []}
              cluster={cluster}
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
              onClaimStakingBtc={() => void handleStakeAction("claim_btc")}
              onCloseNonce={(it) => void handleCloseNonce(it)}
              onLoadCurveFromSlot={(sel) => {
                const next = [500, 500, 500, 500, 500];
                next[Math.max(0, Math.min(4, sel))] = 8000;
                setPresetId("custom");
                setWeightsBps(next);
                setNotice(
                  `Loaded Range ${String.fromCharCode(65 + sel)} bias.`,
                );
              }}
              onNavigateSub={(sub) => navigate(`mine/${sub}`)}
            />
          </div>

          <MineComposer
            composerTab={composerTab}
            onComposerTabChange={setComposerTab}
            presets={presets}
            presetId={presetId}
            weightsBps={weightsBps}
            selectedBucket={selectedBucket}
            round={round}
            roundsCount={roundsCount}
            startMode={scheduleDraft.startMode}
            onStartModeChange={(m) =>
              setScheduleDraft((d) => ({ ...d, startMode: m }))
            }
            onQuickRounds={(n) => setRoundsCount(n)}
            onOpenScheduleGrid={() => setScheduleOpen(true)}
            amountStr={amountStr}
            onAmountChange={setAmountStr}
            walletSolBalance={portfolio?.solBalanceSol ?? 0}
            selectedWalletId={selectedWalletId}
            onSelectPreset={applyPreset}
            onCurveRebalance={handleCurveRebalance}
            onOpenAllocationModal={(idx) => {
              if (typeof idx === "number") setSelectedBucket(idx);
              setAllocModalOpen(true);
            }}
            onShareCurve={handleShareCurve}
            onOpenSaveStrategy={() => setSaveStratOpen(true)}
            onOpenRules={() => setRulesOpen(true)}
            onPrimarySubmit={() => void runPrimaryAction()}
            onClaimAsset={(asset) => void handleClaimAsset(asset)}
            onClaimAll={() => void handleClaimAll()}
            onCopyMint={(mint) => {
              void navigator.clipboard?.writeText(mint);
              setNotice("Copied WENSAT mint address.");
            }}
            onOpenJupiter={() => navigate("terminals/jupiter")}
            portfolio={portfolio}
            countdownSec={countdownSec}
            mainnetLocked={mainnetLocked}
            mainnetGateNote={status?.mainnetGateNote}
            mintAddress={protoStats?.mintAddress ?? round?.ledgerId ?? ""}
            busy={busy}
            notice={notice}
            error={error}
          />
        </div>
      )}

      {subRoute === "mine" && (
        <footer
          className="mine-telemetry-bar"
          role="region"
          aria-label="Mining status bar"
        >
          <div className="mine-telemetry-left">
            <span className="mine-telemetry-live">
              <i aria-hidden="true" />
              <span>Live</span>
            </span>
            <button
              type="button"
              className="mine-telemetry-link"
              onClick={() => setRulesOpen(true)}
            >
              Rules
            </button>
            <button
              type="button"
              className="mine-telemetry-link"
              onClick={() => setAutomationModalOpen(true)}
            >
              Tasks
            </button>
            {currentWallet && (
              <button
                type="button"
                className="mine-telemetry-link"
                onClick={openWalletPolicyModal}
              >
                Policy
              </button>
            )}
          </div>
          <div className="mine-telemetry-right">
            <span>{cluster === "mainnet-beta" ? "Mainnet" : "Devnet"}</span>
            <span>·</span>
            <span>Round #{round?.roundSlot ?? "—"}</span>
          </div>
        </footer>
      )}

      {subRoute === "assets" && (
        <div className="mine-subpage-scroll">
          <MineAssetsPage
            leaderboard={leaderboard}
            stats={protoStats}
            portfolio={portfolio}
            windowChoice={lbWindow}
            onWindowChange={setLbWindow}
          />
        </div>
      )}

      {subRoute === "strategies" && (
        <div className="mine-subpage-scroll">
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
        </div>
      )}

      {subRoute === "portfolio" && (
        <div className="mine-subpage-scroll">
          <MinePortfolioPage
            portfolio={portfolio}
            wallet={currentWallet ?? undefined}
            busy={busy}
            onClaimAll={() => void handleClaimAll()}
            onOpenPolicy={openWalletPolicyModal}
          />
        </div>
      )}

      {subRoute === "stake" && (
        <div className="mine-subpage-scroll">
          <MineStakePage
            staking={staking}
            rounds={leaderboard?.rounds ?? []}
            btcUsd={round?.markPriceUsd}
            selectedWalletId={selectedWalletId}
            mainnetLocked={mainnetLocked}
            busy={busy}
            onStake={(sat) => void handleStakeAction("stake", sat)}
            onUnstake={(sat) => void handleStakeAction("unstake", sat)}
            onActivate={() => void handleStakeAction("activate")}
            onClaimBtc={() => void handleStakeAction("claim_btc")}
          />
        </div>
      )}

      {scheduleOpen && (
        <MineScheduleGrid
          draft={scheduleDraft}
          currentSlot={round?.roundSlot ?? 0}
          perRoundSol={parsedAmount / Math.max(1, roundsCount)}
          depositSol={parsedAmount}
          onChange={(next) => {
            setScheduleDraft(next);
            const cnt = computeScheduledSlots(
              next,
              round?.roundSlot ?? 0,
            ).length;
            setRoundsCount(Math.max(1, cnt));
          }}
          onClose={() => setScheduleOpen(false)}
        />
      )}

      {allocModalOpen && (
        <Dialog
          title="Fine-Tune Allocation"
          onClose={() => setAllocModalOpen(false)}
        >
          <div className="mine-modal-form">
            {RANGE_LABELS.map((code, idx) => {
              const pct = Math.round((weightsBps[idx] ?? 0) / 100);
              return (
                <div key={code} className="mine-alloc-slider-row">
                  <div className="mine-alloc-slider-label">
                    <strong>{code}</strong>
                    <span className="mine-muted-note">{RANGE_NAMES[idx]}</span>
                  </div>
                  <input
                    type="range"
                    min={0}
                    max={100}
                    step={2}
                    aria-label={`Range ${code} allocation percent`}
                    value={pct}
                    onChange={(e) =>
                      handleCurveRebalance(idx, Number(e.target.value) * 100)
                    }
                  />
                  <span className="mine-alloc-slider-pct">{pct}%</span>
                </div>
              );
            })}
            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => applyPreset(presets[0])}
              >
                Reset Wide
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                onClick={() => setAllocModalOpen(false)}
              >
                Done
              </button>
            </div>
          </div>
        </Dialog>
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
        <Dialog title="WEN Mining Rules" onClose={() => setRulesOpen(false)}>
          <div className="mine-modal-form">
            <p className="mine-muted-note">
              Each 10-minute round settles BTC/USD move into five buckets (A–E)
              at ±{round?.innerBps ?? 5} / ±{round?.outerBps ?? 20} bps. Exact
              hits retain 99.5% SOL and earn max credit weight toward WENSAT and
              BTC yield; adjacent near-misses earn pro-rata credit; misses
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

      {walletPolicyOpen && (
        <Dialog
          title="Mining Wallet & Policy"
          onClose={() => setWalletPolicyOpen(false)}
        >
          <div className="mine-modal-form">
            {currentWallet && (
              <div className="wt-hairline-table">
                <div className="wt-hairline-row">
                  <span>Attached Wallet</span>
                  <span className="f-mono">
                    {shortAddr(currentWallet.publicKey)}
                  </span>
                </div>
                <div className="wt-hairline-row">
                  <span>Balance</span>
                  <span>{(portfolio?.solBalanceSol ?? 0).toFixed(4)} SOL</span>
                </div>
              </div>
            )}

            {wallets.length > 1 && (
              <div className="mine-field">
                <label htmlFor="mine-attached-wallet-sel">
                  Active Mining Wallet (1 attached)
                </label>
                <select
                  id="mine-attached-wallet-sel"
                  className="mine-modal-input"
                  value={selectedWalletId}
                  onChange={(e) => setSelectedWalletId(e.target.value)}
                >
                  {wallets.map((w) => (
                    <option key={w.id} value={w.id}>
                      {w.name || w.handle} ({shortAddr(w.publicKey)})
                    </option>
                  ))}
                </select>
              </div>
            )}

            <div className="mine-automate-grid">
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
            </div>

            <label className="mine-toggle-row">
              <input
                type="checkbox"
                checked={policyAutoClaim}
                onChange={(e) => setPolicyAutoClaim(e.target.checked)}
              />
              <span>Auto-reveal &amp; auto-claim rewards</span>
            </label>
            <label className="mine-toggle-row">
              <input
                type="checkbox"
                checked={policyAutoStakeWensat}
                onChange={(e) => setPolicyAutoStakeWensat(e.target.checked)}
              />
              <span>
                Auto-stake newly claimed WENSAT (&ge; 1 SAT; unstake stays
                manual-only)
              </span>
            </label>
            <label className="mine-toggle-row">
              <input
                type="checkbox"
                checked={policyAllowFallbackCrank}
                onChange={(e) => setPolicyAllowFallbackCrank(e.target.checked)}
              />
              <span>
                Allow fallback settlement crank (&gt;180s stalled, &le; 0.005
                SOL/day)
              </span>
            </label>
            <label className="mine-toggle-row">
              <input
                type="checkbox"
                checked={policyAllowMainnet}
                onChange={(e) => setPolicyAllowMainnet(e.target.checked)}
              />
              <span>
                Allow Mainnet LV1X (requires verified manifest + BPF ELF pin)
              </span>
            </label>

            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => {
                  setWalletPolicyOpen(false);
                  navigate("wallets/accounts");
                }}
              >
                Manage Wallets
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                disabled={busy || !selectedWalletId}
                onClick={() => void savePolicy()}
              >
                Save Policy
              </button>
            </div>
          </div>
        </Dialog>
      )}

      {automationModalOpen && (
        <Dialog
          title="Mining Tasks & Automation"
          onClose={() => setAutomationModalOpen(false)}
        >
          <div className="mine-modal-form">
            <div className="wt-hairline-table">
              <div className="wt-hairline-row">
                <span>Background Automation</span>
                <strong>{selfKeeper?.running ? "Running" : "Standby"}</strong>
              </div>
              <div className="wt-hairline-row">
                <span>Pending Reveal / Claims</span>
                <span>
                  {selfKeeper?.activeRoundCommits ?? 0} reveals ·{" "}
                  {selfKeeper?.pendingClaimsCount ?? 0} claims
                </span>
              </div>
              <div className="wt-hairline-row">
                <span>Next Auto-Reveal</span>
                <span>
                  {revealUntilStr
                    ? `Until ${revealUntilStr} (Keep machine awake)`
                    : "No open reveal"}
                </span>
              </div>
              <div className="wt-hairline-row">
                <span>Manifest Pin</span>
                <span>
                  seq #{manifest?.sequence ?? selfKeeper?.manifestSequence ?? 0}{" "}
                  · {manifest?.status || selfKeeper?.manifestStatus || "pinned"}
                </span>
              </div>
              {selfKeeper?.lastSweepSummary && (
                <div className="wt-hairline-row">
                  <span>Last Run</span>
                  <span>{selfKeeper.lastSweepSummary}</span>
                </div>
              )}
            </div>

            <div className="mine-tasks-drawer-head">
              <strong>
                Scheduled Mining Tasks ({activeMiningTaskCount}/
                {miningTasks.length} active)
              </strong>
              <div className="mine-task-actions">
                <button
                  type="button"
                  className="wt-btn-soft"
                  onClick={() => {
                    setAutomationModalOpen(false);
                    openAutomateDialog();
                  }}
                >
                  + New Task
                </button>
                <button
                  type="button"
                  className="icon-btn"
                  aria-label="Open Tasks workspace"
                  title="Open Tasks"
                  onClick={() => {
                    setAutomationModalOpen(false);
                    navigate("tasks");
                  }}
                >
                  <ArrowUpRight size={14} aria-hidden="true" />
                </button>
              </div>
            </div>

            {miningTasks.length === 0 ? (
              <p className="mine-muted-note">
                No scheduled mining tasks yet. Create one from your active
                curve.
              </p>
            ) : (
              <div className="wt-hairline-table">
                {miningTasks.map((tk) => (
                  <div key={tk.id} className="wt-hairline-row mine-task-row">
                    <div className="mine-task-meta">
                      <span className="mine-task-title">{tk.name}</span>
                      <span className="mine-muted-note">
                        {tk.execMode} · {tk.scheduleKind} · {tk.runCount} runs
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

            <div className="mine-modal-actions">
              <button
                type="button"
                className="wt-btn-soft"
                disabled={busy}
                onClick={() => void handleSyncManifest()}
              >
                Sync Manifest
              </button>
              <button
                type="button"
                className="wt-btn-rose-sm"
                disabled={busy}
                onClick={() => void handleTriggerSweep()}
              >
                Run Sweep Now
              </button>
            </div>
          </div>
        </Dialog>
      )}

      {automateOpen && (
        <Dialog title="New Mining Task" onClose={() => setAutomateOpen(false)}>
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
