import { useCallback, useEffect, useRef, useState } from "react";
import { CoreError, useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import {
  Check,
  Copy,
  Download,
  LockKeyhole,
  RefreshCw,
  Shield,
  ShieldCheck,
  Trash2,
  Wallet,
} from "../icons";
import {
  connectWalletStandardAccount,
  discoverSolanaWalletProviders,
  signWalletStandardTransaction,
} from "../wallet-standard";
import {
  attachExternalWallet,
  checkWalletBalanceV2,
  createNativeWallet,
  deleteWalletV2,
  executeNativeWalletReview,
  exportEncryptedBackupV2,
  listWalletActivity,
  listWalletsV2,
  prepareWalletReview,
  reconcileWalletLedger,
  requestStepUpToken,
  restoreEncryptedBackupV2,
  setLocalOwnerPassword,
  setWalletFreeze,
  submitHardwareSignedReview,
  updateWalletConnections,
  updateWalletPolicy,
  updateWalletRPC,
  type ApprovalMode,
  type AuditEntry,
  type LedgerEntry,
  type WalletBalanceResult,
  type WalletConnections,
  type WalletPolicy,
  type WalletReview,
  type WalletRole,
  type WalletSecurityPosture,
  type WalletV2,
} from "../wallets-v2";
import { Dialog } from "./shared";

type CenterTopTab = "policy" | "connections" | "rpc";
type CenterBottomTab = "reviews" | "ledger" | "audit";
type RightTab = "create" | "hardware" | "send";

const RPC_PRESETS = [
  { label: "Mainnet", url: "https://api.mainnet-beta.solana.com" },
  { label: "Devnet", url: "https://api.devnet.solana.com" },
  { label: "Helius", url: "https://mainnet.helius-rpc.com/?api-key=" },
  { label: "QuickNode", url: "https://example.solana-mainnet.quiknode.pro/" },
] as const;

const SYSTEM_PROG = "11111111111111111111111111111111";
const COMPUTE_PROG = "ComputeBudget111111111111111111111111111111";
const TOKEN_PROG = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA";
const ATA_PROG = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL";

function formatSolFromLamports(raw: string): string {
  const n = Number(raw || "0");
  if (!Number.isFinite(n) || n <= 0) return "0";
  return (n / 1e9).toFixed(4).replace(/\.?0+$/, "");
}

function shortAddr(pk: string): string {
  if (!pk || pk.length < 14) return pk;
  return `${pk.slice(0, 6)}...${pk.slice(-6)}`;
}

export function SolanaWalletsPanel() {
  const { t } = useUI();
  const core = useCore();
  const { agents } = useWorkspace();

  const [wallets, setWallets] = useState<WalletV2[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [balances, setBalances] = useState<Record<string, WalletBalanceResult>>(
    {},
  );
  const [centerTopTab, setCenterTopTab] = useState<CenterTopTab>("policy");
  const [centerBottomTab, setCenterBottomTab] =
    useState<CenterBottomTab>("reviews");
  const [rightTab, setRightTab] = useState<RightTab>("create");

  const [reviews, setReviews] = useState<WalletReview[]>([]);
  const [ledger, setLedger] = useState<LedgerEntry[]>([]);
  const [audit, setAudit] = useState<AuditEntry[]>([]);

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [copiedId, setCopiedId] = useState("");

  // Create form state (Native Signer)
  const [createRole, setCreateRole] = useState<WalletRole>("agent");
  const [createName, setCreateName] = useState("");
  const [createRpc, setCreateRpc] = useState(
    "https://api.mainnet-beta.solana.com",
  );

  // Hardware / Watch attach state
  const [hwCustody, setHwCustody] = useState<
    "hardware_standard" | "watch_only"
  >("hardware_standard");
  const [hwRole, setHwRole] = useState<WalletRole>("vault");
  const [hwName, setHwName] = useState("");
  const [hwPubKey, setHwPubKey] = useState("");
  const [hwRpc, setHwRpc] = useState("https://api.mainnet-beta.solana.com");
  const [detectedProvider, setDetectedProvider] = useState("");

  // Send / Review state
  const [sendAssetMode, setSendAssetMode] = useState<"sol" | "spl">("sol");
  const [sendMint, setSendMint] = useState("");
  const [sendDecimals, setSendDecimals] = useState("6");
  const [sendCreateAta, setSendCreateAta] = useState(true);
  const [sendDest, setSendDest] = useState("");
  const [sendAmount, setSendAmount] = useState("0.1");
  const [activeReview, setActiveReview] = useState<WalletReview | null>(null);

  // Security posture & step-up authorization state
  const [securityPosture, setSecurityPosture] =
    useState<WalletSecurityPosture | null>(null);
  const [auditChainVerified, setAuditChainVerified] = useState(false);
  const [ownerPasswordSet, setOwnerPasswordSet] = useState(true);
  const [stepUpModalMode, setStepUpModalMode] = useState<
    "" | "password_setup" | "step_up"
  >("");
  const [stepUpPassword, setStepUpPassword] = useState("");
  const stepUpTokenRef = useRef<{ token: string; expiresAt: number }>({
    token: "",
    expiresAt: 0,
  });
  const pendingStepUpActionRef = useRef<(() => Promise<void>) | null>(null);

  // Policy draft state
  const [polDraft, setPolDraft] = useState<WalletPolicy | null>(null);
  const [solMaxPerTx, setSolMaxPerTx] = useState("0");
  const [solMaxDaily, setSolMaxDaily] = useState("0");
  const [destText, setDestText] = useState("");
  const [progText, setProgText] = useState("");
  const [confirmLoosenModal, setConfirmLoosenModal] = useState(false);

  // Connections draft state
  const [connDraft, setConnDraft] = useState<WalletConnections | null>(null);

  // RPC edit state
  const [editPrimaryRpc, setEditPrimaryRpc] = useState("");
  const [editFallbackRpc, setEditFallbackRpc] = useState("");

  // Encrypted Backup / Restore modal state
  const [backupOpen, setBackupOpen] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState(false);
  const [passphrase, setPassphrase] = useState("");
  const [backupResult, setBackupResult] = useState<{
    fingerprint: string;
    filename: string;
  } | null>(null);
  const [restoreArchiveText, setRestoreArchiveText] = useState("");
  const [restoreName, setRestoreName] = useState("");
  const [restoreRpc, setRestoreRpc] = useState(
    "https://api.mainnet-beta.solana.com",
  );

  const selected =
    wallets.find((w) => w.id === selectedId) ?? wallets[0] ?? null;

  const loadWallets = useCallback(async () => {
    if (core.mode !== "ready") return;
    try {
      const res = await listWalletsV2(core);
      const list = res.wallets ?? [];
      setWallets(list);
      if (res.security) setSecurityPosture(res.security);
      if (typeof res.auditChainVerified === "boolean") {
        setAuditChainVerified(res.auditChainVerified);
      }
      if (typeof res.ownerPasswordSet === "boolean") {
        setOwnerPasswordSet(res.ownerPasswordSet);
      }
      if (list.length > 0) {
        setSelectedId((prev) =>
          list.some((w) => w.id === prev) ? prev : list[0].id,
        );
      } else {
        setSelectedId("");
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load wallets");
    }
  }, [core.mode, core.request]);

  const loadActivity = useCallback(
    async (walletId: string) => {
      if (!walletId || core.mode !== "ready") return;
      try {
        const res = await listWalletActivity(core, walletId);
        setReviews(res.reviews ?? []);
        setLedger(res.ledger ?? []);
        setAudit(res.audit ?? []);
      } catch {
        setReviews([]);
        setLedger([]);
        setAudit([]);
      }
    },
    [core.mode, core.request],
  );

  useEffect(() => {
    void loadWallets();
  }, [loadWallets]);

  useEffect(() => {
    if (!selected) {
      setPolDraft(null);
      setConnDraft(null);
      return;
    }
    setPolDraft(selected.policy);
    setSolMaxPerTx(formatSolFromLamports(selected.policy.solCap.maxPerTxRaw));
    setSolMaxDaily(formatSolFromLamports(selected.policy.solCap.maxDailyRaw));
    setDestText((selected.policy.destinations ?? []).join("\n"));
    setProgText((selected.policy.programs ?? []).join("\n"));
    setConnDraft(selected.connections);
    setEditPrimaryRpc("");
    setEditFallbackRpc("");
    setActiveReview(null);
    void loadActivity(selected.id);
  }, [
    selected?.id,
    selected?.policy.policyHash,
    selected?.updatedAt,
    loadActivity,
  ]);

  async function refreshBalance(w: WalletV2) {
    setBusy(true);
    setError("");
    try {
      const bal = await checkWalletBalanceV2(core, w.id);
      setBalances((prev) => ({ ...prev, [w.id]: bal }));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Balance query failed");
    } finally {
      setBusy(false);
    }
  }

  async function copyText(id: string, val: string) {
    try {
      await navigator.clipboard.writeText(val);
      setCopiedId(id);
      window.setTimeout(() => setCopiedId(""), 1400);
    } catch {
      setError(t("Copy failed"));
    }
  }

  async function onCreateNative(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const res = await createNativeWallet(core, {
        name: createName.trim() || `${createRole} wallet`,
        role: createRole,
        rpcUrl: createRpc.trim(),
      });
      setCreateName("");
      setSelectedId(res.wallet.id);
      setNotice(
        `${res.wallet.handle} (${res.wallet.rpc.cluster}) created inside signer`,
      );
      await loadWallets();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function onConnectWalletStandard() {
    setError("");
    try {
      const available = discoverSolanaWalletProviders();
      const { provider, account } = await connectWalletStandardAccount(
        available[0]?.name,
      );
      setDetectedProvider(provider.name);
      setHwPubKey(account.address);
      if (!hwName.trim()) {
        setHwName(`${provider.name} Vault`);
      }
      setNotice(`Connected ${provider.name} (${shortAddr(account.address)})`);
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : "Wallet Standard connection failed",
      );
    }
  }

  async function onAttachExternal(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const res = await attachExternalWallet(core, {
        custody: hwCustody,
        name:
          hwName.trim() ||
          (hwCustody === "watch_only" ? "Watch Wallet" : "Hardware Vault"),
        role: hwCustody === "watch_only" ? "watch" : hwRole,
        publicKey: hwPubKey.trim(),
        rpcUrl: hwRpc.trim(),
      });
      setHwName("");
      setHwPubKey("");
      setSelectedId(res.wallet.id);
      setNotice(`${res.wallet.handle} attached`);
      await loadWallets();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Attach failed");
    } finally {
      setBusy(false);
    }
  }

  function buildEditedPolicy(): WalletPolicy | null {
    if (!polDraft) return null;
    const perTxLamports = Math.max(
      0,
      Math.round((Number(solMaxPerTx) || 0) * 1e9),
    );
    const dailyLamports = Math.max(
      0,
      Math.round((Number(solMaxDaily) || 0) * 1e9),
    );
    return {
      ...polDraft,
      solCap: {
        ...polDraft.solCap,
        maxPerTxRaw: String(perTxLamports),
        maxDailyRaw: String(dailyLamports),
      },
      destinations: destText
        .split(/[\n,]+/)
        .map((s) => s.trim())
        .filter(Boolean),
      programs: progText
        .split(/[\n,]+/)
        .map((s) => s.trim())
        .filter(Boolean),
    };
  }

  async function runWithStepUp(
    fn: (stepUpToken?: string) => Promise<void>,
  ): Promise<void> {
    const cached =
      stepUpTokenRef.current.expiresAt * 1000 > Date.now() + 5_000
        ? stepUpTokenRef.current.token
        : "";
    try {
      await fn(cached || undefined);
    } catch (err) {
      if (
        err instanceof CoreError &&
        (err.code === "owner_password_required" ||
          err.code === "step_up_required")
      ) {
        pendingStepUpActionRef.current = () => runWithStepUp(fn);
        setStepUpPassword("");
        setStepUpModalMode(
          err.code === "owner_password_required" ? "password_setup" : "step_up",
        );
        return;
      }
      throw err;
    }
  }

  async function onSubmitStepUp(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (stepUpModalMode === "password_setup") {
        await setLocalOwnerPassword(core, stepUpPassword);
        setOwnerPasswordSet(true);
      }
      const res = await requestStepUpToken(core, stepUpPassword);
      stepUpTokenRef.current = {
        token: res.stepUpToken,
        expiresAt: res.expiresAt,
      };
      const pending = pendingStepUpActionRef.current;
      pendingStepUpActionRef.current = null;
      setStepUpPassword("");
      setStepUpModalMode("");
      if (pending) {
        await pending();
      } else {
        setNotice("Owner authorization configured");
        await loadWallets();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Authorization failed");
    } finally {
      setBusy(false);
    }
  }

  async function submitPolicySave(confirmLoosen: boolean) {
    if (!selected) return;
    const nextPol = buildEditedPolicy();
    if (!nextPol) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await runWithStepUp(async (stepUp) => {
        await updateWalletPolicy(
          core,
          selected.id,
          {
            policy: nextPol,
            confirmLoosen,
            expectedPolicyHash: selected.policy.policyHash,
          },
          stepUp,
        );
        setConfirmLoosenModal(false);
        setNotice("Signer policy updated");
        await loadWallets();
      });
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Policy update failed";
      if (!confirmLoosen && msg.toLowerCase().includes("confirmation")) {
        setConfirmLoosenModal(true);
      } else {
        setError(msg);
      }
    } finally {
      setBusy(false);
    }
  }

  async function onSaveConnections() {
    if (!selected || !connDraft) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await updateWalletConnections(core, selected.id, connDraft);
      setNotice("Wallet module connections saved");
      await loadWallets();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Connections failed");
    } finally {
      setBusy(false);
    }
  }

  async function onSaveRPC(e: React.FormEvent) {
    e.preventDefault();
    if (!selected) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await runWithStepUp(async (stepUp) => {
        const res = await updateWalletRPC(
          core,
          selected.id,
          editPrimaryRpc.trim(),
          editFallbackRpc.trim(),
          stepUp,
        );
        setEditPrimaryRpc("");
        setEditFallbackRpc("");
        setNotice(
          `RPC verified (${res.wallet.rpc.cluster} · ${res.wallet.rpc.host})`,
        );
        await loadWallets();
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : "RPC verification failed");
    } finally {
      setBusy(false);
    }
  }

  async function onPrepareSend(e: React.FormEvent) {
    e.preventDefault();
    if (!selected) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const res = await prepareWalletReview(core, selected.id, {
        operation:
          sendAssetMode === "spl"
            ? "solana.splTransferChecked"
            : "solana.nativeTransfer",
        destination: sendDest.trim(),
        amountHuman: sendAmount.trim(),
        mint: sendAssetMode === "spl" ? sendMint.trim() : undefined,
        decimals:
          sendAssetMode === "spl" ? Number(sendDecimals || "6") : undefined,
        createDestAta: sendAssetMode === "spl" ? sendCreateAta : undefined,
      });
      setActiveReview(res.review);
      await loadActivity(selected.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Review failed");
    } finally {
      setBusy(false);
    }
  }

  async function onExecuteReview(rev: WalletReview) {
    if (!selected) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      if (rev.custody === "hardware_standard") {
        const signedB64 = await signWalletStandardTransaction({
          unsignedTxBase64: rev.unsignedTxBase64,
          expectedAddress: selected.publicKey,
          cluster: selected.rpc.cluster,
          preferredWalletName: detectedProvider || undefined,
        });
        const out = await submitHardwareSignedReview(
          core,
          selected.id,
          rev.id,
          signedB64,
        );
        setNotice(
          `Broadcast ${out.entry.state} · sig=${out.entry.txSignature ?? ""}`,
        );
        setActiveReview(null);
        await Promise.all([loadWallets(), loadActivity(selected.id)]);
      } else {
        await runWithStepUp(async (stepUp) => {
          const out = await executeNativeWalletReview(
            core,
            selected.id,
            rev.id,
            stepUp,
          );
          setNotice(
            `Signer executed (${out.entry.state}) · sig=${out.entry.txSignature ?? ""}`,
          );
          setActiveReview(null);
          await Promise.all([loadWallets(), loadActivity(selected.id)]);
        });
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Execution failed");
      await loadActivity(selected.id);
    } finally {
      setBusy(false);
    }
  }

  async function onReconcile(entry: LedgerEntry) {
    if (!selected) return;
    setBusy(true);
    setError("");
    try {
      const out = await reconcileWalletLedger(core, selected.id, entry.id);
      setNotice(`Reconciled ${out.entry.id} -> ${out.entry.state}`);
      await Promise.all([loadWallets(), loadActivity(selected.id)]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Reconcile failed");
    } finally {
      setBusy(false);
    }
  }

  async function onToggleFreeze() {
    if (!selected) return;
    setBusy(true);
    setError("");
    const willFreeze = !selected.policy.frozen;
    try {
      if (willFreeze) {
        await setWalletFreeze(core, selected.id, true, "Owner freeze");
        await loadWallets();
      } else {
        await runWithStepUp(async (stepUp) => {
          await setWalletFreeze(core, selected.id, false, "", stepUp);
          await loadWallets();
        });
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Freeze update failed");
    } finally {
      setBusy(false);
    }
  }

  async function onExportBackup(e: React.FormEvent) {
    e.preventDefault();
    if (!selected) return;
    setBusy(true);
    setError("");
    try {
      await runWithStepUp(async (stepUp) => {
        const res = await exportEncryptedBackupV2(
          core,
          selected.id,
          passphrase,
          stepUp,
        );
        const blob = new Blob([JSON.stringify(res.archive, null, 2)], {
          type: "application/json",
        });
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = res.filename;
        a.click();
        URL.revokeObjectURL(url);
        setBackupResult({
          fingerprint: res.fingerprint,
          filename: res.filename,
        });
        setPassphrase("");
        await loadActivity(selected.id);
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Backup failed");
    } finally {
      setBusy(false);
    }
  }

  async function onRestoreBackup(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const parsed = JSON.parse(restoreArchiveText);
      await runWithStepUp(async (stepUp) => {
        const res = await restoreEncryptedBackupV2(
          core,
          {
            archive: parsed,
            passphrase,
            name: restoreName.trim(),
            rpcUrl: restoreRpc.trim(),
          },
          stepUp,
        );
        setRestoreOpen(false);
        setPassphrase("");
        setRestoreArchiveText("");
        setSelectedId(res.wallet.id);
        setNotice(`Restored ${res.wallet.handle}`);
        await loadWallets();
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Restore failed");
    } finally {
      setBusy(false);
    }
  }

  const selBalance = selected ? balances[selected.id] : undefined;
  const vaultLocked = selected?.role === "vault" || selected?.role === "watch";

  return (
    <div className="wt-wallets-shell">
      {error && <div className="wt-notice-err">{error}</div>}
      {notice && <div className="wt-notice-ok">{notice}</div>}

      <div className="wt-wallets-grid">
        {/* LEFT RAIL (232px) — Switcher, Identity, 2x2 KPIs, Backup/Freeze */}
        <aside className="wt-wpanel" aria-label={t("Wallets")}>
          <div className="wt-whead">
            <h1>{t("Wallets")}</h1>
            <button
              type="button"
              className="wt-btn-soft"
              onClick={() => {
                setRestoreOpen(true);
                setError("");
              }}
            >
              {t("Restore Backup")}
            </button>
          </div>

          <div className="wt-posture-row">
            <span
              className={`wt-posture-pill${securityPosture?.processIsolated ? " is-ok" : " is-warn"}`}
            >
              {t(
                securityPosture?.processIsolated
                  ? "Isolated Signer"
                  : "In-Process",
              )}
            </span>
            {securityPosture?.kekProtection && (
              <span
                className={`wt-posture-pill${securityPosture.kekProtection === "file-dev-only" ? " is-warn" : " is-ok"}`}
              >
                {securityPosture.kekProtection === "systemd-creds"
                  ? "systemd-creds"
                  : securityPosture.kekProtection === "os-keychain"
                    ? t("OS Keychain")
                    : t("Dev File")}
              </span>
            )}
            {auditChainVerified && (
              <span className="wt-posture-pill is-ok">
                {t("Audit Chain Verified")}
              </span>
            )}
            {!ownerPasswordSet && (
              <button
                type="button"
                className="wt-btn-soft"
                onClick={() => {
                  pendingStepUpActionRef.current = null;
                  setStepUpPassword("");
                  setStepUpModalMode("password_setup");
                }}
              >
                {t("Set Owner Password")}
              </button>
            )}
          </div>

          <div className="wt-wallet-list">
            {wallets.length === 0 ? (
              <div className="wt-cluster-line">
                <span className="wt-dot is-off" />
                <span>{t("No wallets")}</span>
              </div>
            ) : (
              wallets.map((w) => (
                <button
                  key={w.id}
                  type="button"
                  className={`wt-wallet-item${selected?.id === w.id ? " is-selected" : ""}`}
                  onClick={() => setSelectedId(w.id)}
                >
                  <span className="wt-wallet-item-name">
                    <Wallet size={14} aria-hidden="true" />
                    <span>{w.name}</span>
                  </span>
                  <span
                    className={`wt-role-pill${w.policy.frozen ? " is-frozen" : w.role === "vault" ? " is-vault" : w.role === "agent" ? " is-agent" : ""}`}
                  >
                    {w.policy.frozen ? "FROZEN" : w.role}
                  </span>
                </button>
              ))
            )}
          </div>

          {selected && (
            <>
              <div className="wt-cluster-line">
                <span
                  className={`wt-dot${selected.policy.frozen ? " is-danger" : ""}`}
                />
                <span>
                  {selected.rpc.cluster || "unverified"} ·{" "}
                  {selected.custody === "hardware_standard"
                    ? "Hardware"
                    : selected.custody === "watch_only"
                      ? "Watch"
                      : "Signer"}
                </span>
              </div>

              <div className="wt-addr-row">
                <code title={selected.publicKey}>
                  {shortAddr(selected.publicKey)}
                </code>
                <button
                  type="button"
                  className="wt-btn-soft"
                  aria-label={t("Copy")}
                  onClick={() => copyText(selected.id, selected.publicKey)}
                >
                  {copiedId === selected.id ? (
                    <Check size={12} aria-hidden="true" />
                  ) : (
                    <Copy size={12} aria-hidden="true" />
                  )}
                </button>
              </div>

              <div className="wt-kpi-grid">
                <div className="wt-kpi-box">
                  <span>SOL</span>
                  <strong>{selBalance ? selBalance.sol : "—"}</strong>
                </div>
                <div className="wt-kpi-box">
                  <span>Daily Used</span>
                  <strong>
                    {formatSolFromLamports(selected.dailySpentLamports)}
                  </strong>
                </div>
                <div className="wt-kpi-box">
                  <span>Daily Cap</span>
                  <strong>
                    {formatSolFromLamports(selected.policy.solCap.maxDailyRaw)}
                  </strong>
                </div>
                <div className="wt-kpi-box">
                  <span>Policy</span>
                  <strong>{`v${selected.policy.version}`}</strong>
                </div>
              </div>

              <div className="wt-rail-btns">
                <button
                  type="button"
                  className="wt-btn-soft"
                  disabled={busy}
                  onClick={() => refreshBalance(selected)}
                >
                  <RefreshCw size={12} aria-hidden="true" />
                  <span>{t("Balance")}</span>
                </button>
                <button
                  type="button"
                  className={`wt-btn-soft${selected.policy.frozen ? "" : " is-danger"}`}
                  disabled={busy}
                  onClick={onToggleFreeze}
                >
                  <LockKeyhole size={12} aria-hidden="true" />
                  <span>
                    {t(selected.policy.frozen ? "Unfreeze" : "Freeze")}
                  </span>
                </button>
                {selected.custody === "native_signer" && (
                  <button
                    type="button"
                    className="wt-btn-soft is-span2"
                    disabled={busy}
                    onClick={() => {
                      setBackupResult(null);
                      setBackupOpen(true);
                    }}
                  >
                    <Download size={12} aria-hidden="true" />
                    <span>{t("Encrypted Backup")}</span>
                  </button>
                )}
                <button
                  type="button"
                  className="wt-btn-soft is-danger is-span2"
                  disabled={busy}
                  onClick={async () => {
                    try {
                      await runWithStepUp(async (stepUp) => {
                        await deleteWalletV2(core, selected.id, stepUp);
                        await loadWallets();
                      });
                    } catch (e) {
                      setError(
                        e instanceof Error ? e.message : "Delete failed",
                      );
                    }
                  }}
                >
                  <Trash2 size={12} aria-hidden="true" />
                  <span>{t("Remove")}</span>
                </button>
              </div>
            </>
          )}
        </aside>

        {/* CENTER COLUMN (1fr) — Top Policy/Connections/RPC + Bottom Reviews/Ledger/Audit */}
        <section className="wt-wcenter">
          <div className="wt-wpanel">
            <div className="wt-wtabs" role="tablist">
              {(
                [
                  { id: "policy", label: "Policy & Caps" },
                  { id: "connections", label: "Connections" },
                  { id: "rpc", label: "RPC & Network" },
                ] as const
              ).map((tb) => (
                <button
                  key={tb.id}
                  type="button"
                  role="tab"
                  aria-selected={centerTopTab === tb.id}
                  onClick={() => setCenterTopTab(tb.id)}
                >
                  {t(tb.label)}
                </button>
              ))}
            </div>

            {!selected || !polDraft || !connDraft ? (
              <div className="wt-cluster-line">
                {t(
                  "Create a wallet in the right panel to configure policy & RPC.",
                )}
              </div>
            ) : centerTopTab === "policy" ? (
              <div className="wt-form-stack">
                <div className="wt-preset-chips">
                  {[
                    "solana.nativeTransfer",
                    "solana.splTransferChecked",
                    "jupiter.swap",
                    "jupiter.trigger",
                    "trade.order",
                  ].map((op) => {
                    const on = polDraft.operations.includes(op);
                    return (
                      <button
                        key={op}
                        type="button"
                        aria-pressed={on}
                        disabled={vaultLocked && op === "trade.order"}
                        onClick={() =>
                          setPolDraft({
                            ...polDraft,
                            operations: on
                              ? polDraft.operations.filter((x) => x !== op)
                              : [...polDraft.operations, op],
                          })
                        }
                      >
                        {op}
                      </button>
                    );
                  })}
                </div>

                <div className="wt-tile-grid">
                  {(
                    [
                      { id: "manual_review", label: "Manual Review" },
                      { id: "semi_auto", label: "Semi-Auto" },
                      { id: "bounded_auto", label: "Bounded Auto" },
                    ] as const
                  ).map((m) => (
                    <button
                      key={m.id}
                      type="button"
                      aria-pressed={polDraft.approvalMode === m.id}
                      disabled={vaultLocked && m.id !== "manual_review"}
                      onClick={() =>
                        setPolDraft({
                          ...polDraft,
                          approvalMode: m.id as ApprovalMode,
                        })
                      }
                    >
                      {t(m.label)}
                    </button>
                  ))}
                </div>

                <div className="wt-policy-grid">
                  <label className="wt-field">
                    <span>SOL Max / Tx</span>
                    <input
                      value={solMaxPerTx}
                      onChange={(e) => setSolMaxPerTx(e.target.value)}
                    />
                  </label>
                  <label className="wt-field">
                    <span>SOL Daily Cap</span>
                    <input
                      value={solMaxDaily}
                      onChange={(e) => setSolMaxDaily(e.target.value)}
                    />
                  </label>
                </div>

                <div className="wt-policy-grid">
                  <label className="wt-field">
                    <span>Allowed Destinations (@wallet:id or Base58)</span>
                    <textarea
                      rows={2}
                      value={destText}
                      onChange={(e) => setDestText(e.target.value)}
                      placeholder="@wallet:vault or Base58"
                    />
                  </label>
                  <label className="wt-field">
                    <span>Allowed Program IDs</span>
                    <textarea
                      rows={2}
                      value={progText}
                      onChange={(e) => setProgText(e.target.value)}
                      placeholder={`${SYSTEM_PROG}`}
                    />
                  </label>
                </div>

                <div className="wt-preset-chips">
                  <button
                    type="button"
                    onClick={() =>
                      setProgText(
                        [SYSTEM_PROG, COMPUTE_PROG, TOKEN_PROG, ATA_PROG].join(
                          "\n",
                        ),
                      )
                    }
                  >
                    + Core Solana Programs
                  </button>
                  {wallets
                    .filter((w) => w.id !== selected.id)
                    .map((w) => (
                      <button
                        key={w.id}
                        type="button"
                        onClick={() =>
                          setDestText((prev) =>
                            prev ? `${prev}\n${w.handle}` : w.handle,
                          )
                        }
                      >
                        + {w.handle}
                      </button>
                    ))}
                </div>

                <button
                  type="button"
                  className="wt-btn-soft"
                  disabled={busy}
                  onClick={() => submitPolicySave(false)}
                >
                  <ShieldCheck size={14} aria-hidden="true" />
                  <span>{`Apply Policy (hash ${selected.policy.policyHash.slice(0, 8)})`}</span>
                </button>
              </div>
            ) : centerTopTab === "connections" ? (
              <div className="wt-form-stack">
                <div className="wt-toggle-list">
                  <label className="wt-toggle-row">
                    <span>#trade Manual Orders</span>
                    <input
                      type="checkbox"
                      checked={connDraft.allowTradeManual}
                      disabled={selected.role === "watch"}
                      onChange={(e) =>
                        setConnDraft({
                          ...connDraft,
                          allowTradeManual: e.target.checked,
                        })
                      }
                    />
                  </label>
                  <label className="wt-toggle-row">
                    <span>Automated Bots & Tasks (Agentic Auto)</span>
                    <input
                      type="checkbox"
                      checked={connDraft.allowTradeBots}
                      disabled={vaultLocked}
                      onChange={(e) =>
                        setConnDraft({
                          ...connDraft,
                          allowTradeBots: e.target.checked,
                        })
                      }
                    />
                  </label>
                  <label className="wt-toggle-row">
                    <span>Default Agent Wallet</span>
                    <input
                      type="checkbox"
                      checked={connDraft.defaultAgentWallet}
                      disabled={vaultLocked}
                      onChange={(e) =>
                        setConnDraft({
                          ...connDraft,
                          defaultAgentWallet: e.target.checked,
                        })
                      }
                    />
                  </label>
                </div>

                <div className="wt-field">
                  <span>Assigned Agents</span>
                  <div className="wt-preset-chips">
                    {(agents.view?.profiles ?? []).map((ag) => {
                      const on = connDraft.agentIds.includes(ag.id);
                      return (
                        <button
                          key={ag.id}
                          type="button"
                          aria-pressed={on}
                          disabled={vaultLocked}
                          onClick={() =>
                            setConnDraft({
                              ...connDraft,
                              agentIds: on
                                ? connDraft.agentIds.filter((x) => x !== ag.id)
                                : [...connDraft.agentIds, ag.id],
                            })
                          }
                        >
                          {ag.name}
                        </button>
                      );
                    })}
                  </div>
                </div>

                <div className="wt-field">
                  <span>Allowed Trading Venues</span>
                  <div className="wt-preset-chips">
                    {[
                      "solana",
                      "hyperliquid",
                      "lighter",
                      "polymarket",
                      "kalshi",
                    ].map((v) => {
                      const on = connDraft.allowedVenues.includes(v);
                      return (
                        <button
                          key={v}
                          type="button"
                          aria-pressed={on}
                          disabled={selected.role === "watch"}
                          onClick={() =>
                            setConnDraft({
                              ...connDraft,
                              allowedVenues: on
                                ? connDraft.allowedVenues.filter((x) => x !== v)
                                : [...connDraft.allowedVenues, v],
                            })
                          }
                        >
                          {v}
                        </button>
                      );
                    })}
                  </div>
                </div>

                <button
                  type="button"
                  className="wt-btn-soft"
                  disabled={busy}
                  onClick={onSaveConnections}
                >
                  {t("Save")}
                </button>
              </div>
            ) : (
              <form className="wt-form-stack" onSubmit={onSaveRPC}>
                <div className="wt-kpi-grid">
                  <div className="wt-kpi-box">
                    <span>Active Execution Host</span>
                    <strong>{selected.rpc.host || "—"}</strong>
                  </div>
                  <div className="wt-kpi-box">
                    <span>Pinned Genesis / Witness</span>
                    <strong>{`${selected.rpc.cluster} (${selected.rpc.witnessStatus})`}</strong>
                  </div>
                </div>

                <label className="wt-field">
                  <span>Primary Execution Solana RPC</span>
                  <input
                    value={editPrimaryRpc}
                    onChange={(e) => setEditPrimaryRpc(e.target.value)}
                    placeholder="https://mainnet.helius-rpc.com/?api-key=..."
                    required
                  />
                </label>

                <div className="wt-preset-chips">
                  {RPC_PRESETS.map((p) => (
                    <button
                      key={p.label}
                      type="button"
                      onClick={() => setEditPrimaryRpc(p.url)}
                    >
                      {p.label}
                    </button>
                  ))}
                </div>

                <label className="wt-field">
                  <span>Optional Distinct Fallback RPC</span>
                  <input
                    value={editFallbackRpc}
                    onChange={(e) => setEditFallbackRpc(e.target.value)}
                    placeholder="https://..."
                  />
                </label>

                <button type="submit" className="wt-btn-soft" disabled={busy}>
                  {t("Verify Genesis & Save")}
                </button>
              </form>
            )}
          </div>

          <div className="wt-wpanel">
            <div className="wt-wtabs" role="tablist">
              {(
                [
                  { id: "reviews", label: "Pending Reviews" },
                  { id: "ledger", label: "Transactions" },
                  { id: "audit", label: "Audit Log" },
                ] as const
              ).map((tb) => (
                <button
                  key={tb.id}
                  type="button"
                  role="tab"
                  aria-selected={centerBottomTab === tb.id}
                  onClick={() => setCenterBottomTab(tb.id)}
                >
                  {t(tb.label)}
                </button>
              ))}
            </div>

            <div className="wt-table-scroll">
              {centerBottomTab === "reviews" ? (
                <table className="wt-table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Destination</th>
                      <th>Amount</th>
                      <th>Status</th>
                      <th>Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {reviews.length === 0 ? (
                      <tr>
                        <td colSpan={5}>{t("None")}</td>
                      </tr>
                    ) : (
                      reviews.map((rv) => (
                        <tr key={rv.id}>
                          <td>
                            {new Date(rv.createdAt * 1000).toLocaleTimeString()}
                          </td>
                          <td>{shortAddr(rv.destination)}</td>
                          <td>{`${rv.amountHuman} SOL`}</td>
                          <td>{rv.status}</td>
                          <td>
                            {rv.status === "prepared" && (
                              <button
                                type="button"
                                className="wt-btn-soft"
                                disabled={busy}
                                onClick={() => onExecuteReview(rv)}
                              >
                                {t(
                                  rv.custody === "hardware_standard"
                                    ? "Sign on Hardware"
                                    : "Approve & Execute",
                                )}
                              </button>
                            )}
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              ) : centerBottomTab === "ledger" ? (
                <table className="wt-table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Destination</th>
                      <th>State</th>
                      <th>Signature / Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ledger.length === 0 ? (
                      <tr>
                        <td colSpan={4}>{t("None")}</td>
                      </tr>
                    ) : (
                      ledger.map((le) => (
                        <tr key={le.id}>
                          <td>
                            {new Date(le.createdAt * 1000).toLocaleTimeString()}
                          </td>
                          <td>{shortAddr(le.destination)}</td>
                          <td>{le.state}</td>
                          <td>
                            {le.state === "broadcast" ||
                            le.state === "unknown" ? (
                              <button
                                type="button"
                                className="wt-btn-soft"
                                disabled={busy}
                                onClick={() => onReconcile(le)}
                              >
                                {t("Reconcile")}
                              </button>
                            ) : (
                              shortAddr(le.txSignature || le.id)
                            )}
                          </td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              ) : (
                <table className="wt-table">
                  <thead>
                    <tr>
                      <th>Time</th>
                      <th>Action</th>
                      <th>Summary</th>
                    </tr>
                  </thead>
                  <tbody>
                    {audit.length === 0 ? (
                      <tr>
                        <td colSpan={3}>{t("None")}</td>
                      </tr>
                    ) : (
                      audit.map((a) => (
                        <tr key={a.id}>
                          <td>
                            {new Date(a.timestamp * 1000).toLocaleTimeString()}
                          </td>
                          <td>{a.action}</td>
                          <td>{a.summary}</td>
                        </tr>
                      ))
                    )}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </section>

        {/* RIGHT RAIL (334px) — WEN Action Composer (Create / Hardware / Send & Review) */}
        <aside className="wt-wpanel">
          <div className="wt-wtabs" role="tablist">
            {(
              [
                { id: "create", label: "Create" },
                { id: "hardware", label: "Hardware" },
                { id: "send", label: "Send" },
              ] as const
            ).map((tb) => (
              <button
                key={tb.id}
                type="button"
                role="tab"
                aria-selected={rightTab === tb.id}
                onClick={() => setRightTab(tb.id)}
              >
                {t(tb.label)}
              </button>
            ))}
          </div>

          {rightTab === "create" ? (
            <form className="wt-form-stack" onSubmit={onCreateNative}>
              <div className="wt-tile-grid">
                {(
                  [
                    { id: "agent", label: "Agentic Auto" },
                    { id: "trading", label: "Trading" },
                    { id: "vault", label: "Vault" },
                  ] as const
                ).map((r) => (
                  <button
                    key={r.id}
                    type="button"
                    aria-pressed={createRole === r.id}
                    onClick={() => setCreateRole(r.id)}
                  >
                    {t(r.label)}
                  </button>
                ))}
              </div>

              <label className="wt-field">
                <span>{t("Wallet name")}</span>
                <input
                  value={createName}
                  onChange={(e) => setCreateName(e.target.value)}
                  placeholder="Solana Bot 1"
                />
              </label>

              <label className="wt-field">
                <span>Solana RPC URL</span>
                <input
                  value={createRpc}
                  onChange={(e) => setCreateRpc(e.target.value)}
                  placeholder="https://api.mainnet-beta.solana.com"
                  required
                />
              </label>

              <div className="wt-preset-chips">
                {RPC_PRESETS.map((p) => (
                  <button
                    key={p.label}
                    type="button"
                    aria-pressed={createRpc === p.url}
                    onClick={() => setCreateRpc(p.url)}
                  >
                    {p.label}
                  </button>
                ))}
              </div>

              <button type="submit" className="wt-btn-rose" disabled={busy}>
                <Shield size={15} aria-hidden="true" />
                <span>{`${t("Create in Signer")} →`}</span>
              </button>
            </form>
          ) : rightTab === "hardware" ? (
            <form className="wt-form-stack" onSubmit={onAttachExternal}>
              <div className="wt-tile-grid">
                <button
                  type="button"
                  aria-pressed={hwCustody === "hardware_standard"}
                  onClick={() => setHwCustody("hardware_standard")}
                >
                  {t("Hardware")}
                </button>
                <button
                  type="button"
                  aria-pressed={hwCustody === "watch_only"}
                  onClick={() => setHwCustody("watch_only")}
                >
                  {t("Watch Only")}
                </button>
                <button type="button" onClick={onConnectWalletStandard}>
                  Detect
                </button>
              </div>

              {hwCustody === "hardware_standard" && (
                <div className="wt-tile-grid">
                  {(
                    [
                      { id: "vault", label: "Vault" },
                      { id: "trading", label: "Trading" },
                    ] as const
                  ).map((r) => (
                    <button
                      key={r.id}
                      type="button"
                      aria-pressed={hwRole === r.id}
                      onClick={() => setHwRole(r.id)}
                    >
                      {t(r.label)}
                    </button>
                  ))}
                </div>
              )}

              <label className="wt-field">
                <span>{t("Wallet name")}</span>
                <input
                  value={hwName}
                  onChange={(e) => setHwName(e.target.value)}
                  placeholder="Ledger Vault"
                />
              </label>

              <label className="wt-field">
                <span>Solana Public Address</span>
                <input
                  value={hwPubKey}
                  onChange={(e) => setHwPubKey(e.target.value)}
                  placeholder="Base58 address..."
                  required
                />
              </label>

              <label className="wt-field">
                <span>Solana RPC URL</span>
                <input
                  value={hwRpc}
                  onChange={(e) => setHwRpc(e.target.value)}
                  required
                />
              </label>

              <button type="submit" className="wt-btn-rose" disabled={busy}>
                <span>{`${t("Attach Wallet")} →`}</span>
              </button>
            </form>
          ) : (
            <div className="wt-form-stack">
              <div className="wt-seg">
                <button
                  type="button"
                  aria-pressed={sendAssetMode === "sol"}
                  onClick={() => setSendAssetMode("sol")}
                >
                  SOL
                </button>
                <button
                  type="button"
                  aria-pressed={sendAssetMode === "spl"}
                  onClick={() => setSendAssetMode("spl")}
                >
                  SPL
                </button>
              </div>
              <form className="wt-form-stack" onSubmit={onPrepareSend}>
                {sendAssetMode === "spl" && (
                  <>
                    <label className="wt-field">
                      <span>SPL Mint</span>
                      <input
                        value={sendMint}
                        onChange={(e) => setSendMint(e.target.value)}
                        placeholder="EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
                        required
                      />
                    </label>
                    {(selected?.policy.tokenCaps ?? []).length > 0 && (
                      <div className="wt-preset-chips">
                        {(selected?.policy.tokenCaps ?? []).map((tc) => (
                          <button
                            key={tc.mint || tc.asset}
                            type="button"
                            onClick={() => {
                              if (tc.mint) setSendMint(tc.mint);
                              if (tc.decimals)
                                setSendDecimals(String(tc.decimals));
                            }}
                          >
                            {tc.symbol || shortAddr(tc.mint || "")}
                          </button>
                        ))}
                      </div>
                    )}
                    <label className="wt-field">
                      <span>Decimals</span>
                      <input
                        type="number"
                        min={0}
                        max={18}
                        value={sendDecimals}
                        onChange={(e) => setSendDecimals(e.target.value)}
                        required
                      />
                    </label>
                    <label className="wt-toggle-row">
                      <span>Create Destination ATA</span>
                      <input
                        type="checkbox"
                        checked={sendCreateAta}
                        onChange={(e) => setSendCreateAta(e.target.checked)}
                      />
                    </label>
                  </>
                )}

                <label className="wt-field">
                  <span>{t("Amount")}</span>
                  <div className="wt-amount-wrap">
                    <input
                      value={sendAmount}
                      onChange={(e) => setSendAmount(e.target.value)}
                      required
                    />
                    <span className="wt-amount-suffix">
                      {sendAssetMode === "spl" ? "SPL" : "SOL"}
                    </span>
                  </div>
                </label>

                <label className="wt-field">
                  <span>Destination (@wallet:id or Base58)</span>
                  <input
                    value={sendDest}
                    onChange={(e) => setSendDest(e.target.value)}
                    placeholder="@wallet:vault"
                    required
                  />
                </label>

                <div className="wt-preset-chips">
                  {wallets
                    .filter((w) => !selected || w.id !== selected.id)
                    .map((w) => (
                      <button
                        key={w.id}
                        type="button"
                        onClick={() => setSendDest(w.handle)}
                      >
                        {w.handle}
                      </button>
                    ))}
                </div>

                <button
                  type="submit"
                  className="wt-btn-rose"
                  disabled={busy || !selected}
                >
                  <span>{`${t("Prepare Review")} →`}</span>
                </button>
              </form>

              {activeReview && (
                <div className="wt-review-box">
                  <div className="wt-review-kv">
                    <span>Policy Hash</span>
                    <code>{activeReview.policyHash.slice(0, 12)}</code>
                  </div>
                  <div className="wt-review-kv">
                    <span>Simulation</span>
                    <strong>
                      {activeReview.simulationOk
                        ? "PASS"
                        : activeReview.simulationError || "FAIL"}
                    </strong>
                  </div>
                  <div className="wt-review-kv">
                    <span>Destination</span>
                    <code>{shortAddr(activeReview.destination)}</code>
                  </div>
                  <button
                    type="button"
                    className="wt-btn-rose"
                    disabled={busy}
                    onClick={() => onExecuteReview(activeReview)}
                  >
                    <span>
                      {`${t(
                        activeReview.custody === "hardware_standard"
                          ? "Sign on Hardware"
                          : "Approve & Execute",
                      )} →`}
                    </span>
                  </button>
                </div>
              )}
            </div>
          )}
        </aside>
      </div>

      {/* Confirm Policy Expansion Modal */}
      {confirmLoosenModal && selected && (
        <Dialog
          title="Confirm Policy Expansion"
          onClose={() => setConfirmLoosenModal(false)}
        >
          <div className="wt-form-stack">
            <div className="wt-review-box">
              <div className="wt-review-kv">
                <span>Wallet</span>
                <strong>{selected.handle}</strong>
              </div>
              <div className="wt-review-kv">
                <span>Current Policy Hash</span>
                <code>{selected.policy.policyHash.slice(0, 16)}</code>
              </div>
              <div className="wt-review-kv">
                <span>New SOL Cap / Daily</span>
                <strong>{`${solMaxPerTx} / ${solMaxDaily} SOL`}</strong>
              </div>
            </div>
            <button
              type="button"
              className="wt-btn-rose"
              disabled={busy}
              onClick={() => submitPolicySave(true)}
            >
              Confirm Authority Expansion →
            </button>
          </div>
        </Dialog>
      )}

      {/* Encrypted Backup Dialog */}
      {backupOpen && selected && (
        <Dialog
          title={t("Encrypted Backup")}
          onClose={() => setBackupOpen(false)}
        >
          <form className="wt-form-stack" onSubmit={onExportBackup}>
            <label className="wt-field">
              <span>{t("Passphrase")}</span>
              <input
                type="password"
                minLength={12}
                value={passphrase}
                onChange={(e) => setPassphrase(e.target.value)}
                required
              />
            </label>
            <button type="submit" className="wt-btn-rose" disabled={busy}>
              Download Argon2id Backup (.fased-recovery.json) →
            </button>
            {backupResult && (
              <div className="wt-review-box">
                <div className="wt-review-kv">
                  <span>File</span>
                  <code>{backupResult.filename}</code>
                </div>
                <div className="wt-review-kv">
                  <span>SHA-256</span>
                  <code>{backupResult.fingerprint.slice(0, 20)}...</code>
                </div>
              </div>
            )}
            <div className="wt-cluster-line">
              <code>
                {`fased-app wallet recovery export-raw --wallet-id ${selected.id} --output /abs/key.json --acknowledge-custody-reduction`}
              </code>
            </div>
          </form>
        </Dialog>
      )}

      {/* Restore Encrypted Backup Dialog */}
      {restoreOpen && (
        <Dialog
          title={t("Restore Backup")}
          onClose={() => setRestoreOpen(false)}
        >
          <form className="wt-form-stack" onSubmit={onRestoreBackup}>
            <label className="wt-field">
              <span>Encrypted Backup File (.fased-recovery.json)</span>
              <input
                type="file"
                accept=".json,application/json"
                onChange={async (e) => {
                  const file = e.target.files?.[0];
                  if (!file) return;
                  setRestoreArchiveText(await file.text());
                }}
              />
            </label>
            <label className="wt-field">
              <span>{t("Wallet name")}</span>
              <input
                value={restoreName}
                onChange={(e) => setRestoreName(e.target.value)}
                placeholder="Restored Wallet"
              />
            </label>
            <label className="wt-field">
              <span>Solana RPC URL</span>
              <input
                value={restoreRpc}
                onChange={(e) => setRestoreRpc(e.target.value)}
                required
              />
            </label>
            <label className="wt-field">
              <span>{t("Passphrase")}</span>
              <input
                type="password"
                minLength={12}
                value={passphrase}
                onChange={(e) => setPassphrase(e.target.value)}
                required
              />
            </label>
            <button
              type="submit"
              className="wt-btn-rose"
              disabled={busy || !restoreArchiveText}
            >
              {`${t("Restore Backup")} →`}
            </button>
          </form>
        </Dialog>
      )}

      {/* Owner Password Setup / Step-Up Confirmation Dialog */}
      {stepUpModalMode !== "" && (
        <Dialog
          title={t(
            stepUpModalMode === "password_setup"
              ? "Set Owner Password"
              : "Authorization",
          )}
          onClose={() => {
            pendingStepUpActionRef.current = null;
            setStepUpPassword("");
            setStepUpModalMode("");
          }}
        >
          <form className="wt-form-stack" onSubmit={onSubmitStepUp}>
            <label className="wt-field">
              <span>
                {t(
                  stepUpModalMode === "password_setup"
                    ? "Set Owner Password"
                    : "Confirm Owner Password",
                )}
              </span>
              <input
                type="password"
                minLength={12}
                autoComplete="current-password"
                value={stepUpPassword}
                onChange={(e) => setStepUpPassword(e.target.value)}
                required
              />
            </label>
            <button type="submit" className="wt-btn-rose" disabled={busy}>
              {`${t(stepUpModalMode === "password_setup" ? "Save" : "Approve & Execute")} →`}
            </button>
          </form>
        </Dialog>
      )}
    </div>
  );
}
