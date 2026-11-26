import { useCallback, useEffect, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import { DATA_PLUGINS_CHANGED_EVENT } from "../data-plugins";
import {
  Check,
  Copy,
  Download,
  Eye,
  KeyRound,
  Plus,
  RefreshCw,
  Settings,
  Trash2,
  Wallet,
} from "../icons";
import {
  checkWalletBalanceV2,
  createWalletV2,
  deleteWalletV2,
  exportWalletV2,
  importWalletV2,
  listWalletsV2,
  type ExportWalletV2Result,
  type SolanaRPCStatus,
  type WalletV2,
  type WalletV2BalanceResult,
} from "../wallets-v2";
import { Badge, Dialog, SectionHead } from "./shared";

type ImportTab = "mnemonic" | "privateKey" | "watch";

export function SolanaWalletsPanel() {
  const { t } = useUI();
  const core = useCore();
  const { openSettingsModal } = useWorkspace();

  const [wallets, setWallets] = useState<WalletV2[]>([]);
  const [rpcStatus, setRpcStatus] = useState<SolanaRPCStatus | null>(null);
  const [cluster, setCluster] = useState<"mainnet-beta" | "devnet">(
    "mainnet-beta",
  );
  const [balances, setBalances] = useState<
    Record<string, WalletV2BalanceResult>
  >({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [copiedKey, setCopiedKey] = useState("");

  // Create Wallet Wizard state
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState("");
  const [createRole, setCreateRole] = useState<"personal" | "agent_vault">(
    "personal",
  );
  const [createWords, setCreateWords] = useState<12 | 24>(12);
  const [createPass, setCreatePass] = useState("");
  const [createConfirm, setCreateConfirm] = useState("");
  const [backupMnemonic, setBackupMnemonic] = useState("");
  const [backupAddress, setBackupAddress] = useState("");
  const [backupAck, setBackupAck] = useState(false);

  // Import / Watch Modal state
  const [importOpen, setImportOpen] = useState(false);
  const [importTab, setImportTab] = useState<ImportTab>("mnemonic");
  const [importName, setImportName] = useState("");
  const [importRole, setImportRole] = useState<"personal" | "agent_vault">(
    "personal",
  );
  const [importSecret, setImportSecret] = useState("");
  const [importPass, setImportPass] = useState("");

  // Export Secret Modal state
  const [exportWallet, setExportWallet] = useState<WalletV2 | null>(null);
  const [exportPass, setExportPass] = useState("");
  const [exported, setExported] = useState<ExportWalletV2Result | null>(null);

  const load = useCallback(async () => {
    if (core.mode !== "ready") return;
    setError("");
    try {
      const res = await listWalletsV2(core);
      setWallets(res.wallets ?? []);
      setRpcStatus(res.rpc ?? null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load wallets");
    }
  }, [core.mode, core.request]);

  useEffect(() => {
    void load();
    const onPluginsChanged = () => void load();
    window.addEventListener(DATA_PLUGINS_CHANGED_EVENT, onPluginsChanged);
    return () =>
      window.removeEventListener(DATA_PLUGINS_CHANGED_EVENT, onPluginsChanged);
  }, [load]);

  async function copyText(id: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      setCopiedKey(id);
      window.setTimeout(() => setCopiedKey(""), 1600);
    } catch {
      setError(t("Copy failed"));
    }
  }

  function downloadBackupFile(name: string, address: string, content: string) {
    const blob = new Blob(
      [JSON.stringify({ name, address, mnemonic: content }, null, 2)],
      { type: "application/json" },
    );
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${name.toLowerCase().replace(/[^a-z0-9_-]+/g, "-") || "solana-wallet"}-backup.json`;
    a.click();
    URL.revokeObjectURL(url);
  }

  async function handleCreateSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (createPass !== createConfirm) {
      setError(t("Passphrases do not match"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const res = await createWalletV2(core, {
        name: createName.trim(),
        role: createRole,
        words: createWords,
        passphrase: createPass,
      });
      setBackupMnemonic(res.mnemonic);
      setBackupAddress(res.publicKey);
      setBackupAck(false);
      setCreatePass("");
      setCreateConfirm("");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleImportSubmit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (importTab === "watch") {
        await importWalletV2(core, {
          name: importName.trim(),
          watchAddress: importSecret.trim(),
        });
      } else if (importTab === "mnemonic") {
        await importWalletV2(core, {
          name: importName.trim(),
          role: importRole,
          mnemonic: importSecret.trim(),
          passphrase: importPass,
        });
      } else {
        await importWalletV2(core, {
          name: importName.trim(),
          role: importRole,
          privateKey: importSecret.trim(),
          passphrase: importPass,
        });
      }
      setImportOpen(false);
      setImportName("");
      setImportSecret("");
      setImportPass("");
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Import failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleCheckBalance(w: WalletV2) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const res = await checkWalletBalanceV2(core, w.id, cluster);
      setBalances((cur) => ({ ...cur, [w.id]: res }));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Balance check failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleExportSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!exportWallet) return;
    setBusy(true);
    setError("");
    try {
      const res = await exportWalletV2(core, exportWallet.id, exportPass);
      setExported(res);
      setExportPass("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Export failed");
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete(w: WalletV2) {
    setBusy(true);
    setError("");
    try {
      await deleteWalletV2(core, w.id);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    } finally {
      setBusy(false);
    }
  }

  const activeRpcConfigured =
    cluster === "mainnet-beta"
      ? rpcStatus?.mainnetConfigured
      : rpcStatus?.devnetConfigured;
  const activeRpcHost =
    cluster === "mainnet-beta" ? rpcStatus?.mainnetHost : rpcStatus?.devnetHost;

  return (
    <div className="solana-wallets-v2">
      <SectionHead
        title={t("Wallets")}
        action={
          <div className="account-actions">
            <div className="appearance-segmented" role="group">
              <button
                type="button"
                className={cluster === "mainnet-beta" ? "active" : undefined}
                onClick={() => setCluster("mainnet-beta")}
              >
                {t("Mainnet")}
              </button>
              <button
                type="button"
                className={cluster === "devnet" ? "active" : undefined}
                onClick={() => setCluster("devnet")}
              >
                {t("Devnet")}
              </button>
            </div>
            <button
              type="button"
              className="secondary"
              onClick={() => openSettingsModal("data")}
              title={activeRpcHost || t("Chain RPCs")}
            >
              <Settings size={14} aria-hidden="true" />
              <span>
                {activeRpcConfigured
                  ? activeRpcHost?.replace(/^https?:\/\//, "") || "RPC"
                  : t("Chain RPCs")}
              </span>
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setError("");
                setImportTab("mnemonic");
                setImportOpen(true);
              }}
            >
              <span>{t("Import")}</span>
            </button>
            <button
              type="button"
              className="primary"
              onClick={() => {
                setError("");
                setCreateName("");
                setCreatePass("");
                setCreateConfirm("");
                setBackupMnemonic("");
                setBackupAddress("");
                setBackupAck(false);
                setCreateOpen(true);
              }}
            >
              <Plus size={15} aria-hidden="true" />
              <span>{t("Create wallet")}</span>
            </button>
          </div>
        }
      />

      {error && !createOpen && !importOpen && !exportWallet && (
        <p role="alert">{error}</p>
      )}
      {notice && <p role="status">{notice}</p>}

      {wallets.length === 0 ? (
        <section className="panel account-panel">
          <div className="wallet-empty-state">
            <Wallet size={24} aria-hidden="true" />
            <strong>{t("No wallets")}</strong>
            <div className="account-actions">
              <button
                type="button"
                className="primary"
                onClick={() => {
                  setError("");
                  setBackupMnemonic("");
                  setCreateOpen(true);
                }}
              >
                <Plus size={14} aria-hidden="true" />
                <span>{t("Create wallet")}</span>
              </button>
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  setError("");
                  setImportTab("watch");
                  setImportOpen(true);
                }}
              >
                <Eye size={14} aria-hidden="true" />
                <span>{t("Watch Only")}</span>
              </button>
            </div>
          </div>
        </section>
      ) : (
        <div className="wallet-v2-grid">
          {wallets.map((w) => {
            const bal = balances[w.id];
            return (
              <article className="panel wallet-v2-card" key={w.id}>
                <div className="panel-heading">
                  <div className="wallet-v2-title">
                    <Wallet size={16} aria-hidden="true" />
                    <strong>{w.name}</strong>
                  </div>
                  <Badge>
                    {w.kind === "watch"
                      ? t("Watch Only")
                      : w.role === "agent_vault"
                        ? "Agent Vault"
                        : "Solana"}
                  </Badge>
                </div>

                <div className="wallet-v2-address-row">
                  <code className="account-key">{w.publicKey}</code>
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={t("Copy")}
                    onClick={() => void copyText(w.id, w.publicKey)}
                  >
                    {copiedKey === w.id ? (
                      <Check size={14} />
                    ) : (
                      <Copy size={14} />
                    )}
                  </button>
                </div>

                <div className="wallet-v2-balance-row">
                  {bal?.rpcRequired ? (
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => openSettingsModal("data")}
                    >
                      {t("Enable RPC in Settings")}
                    </button>
                  ) : bal && !bal.rpcRequired ? (
                    <span className="mono">
                      {`${(bal.sol ?? 0).toFixed(4)} SOL · ${bal.cluster}`}
                    </span>
                  ) : (
                    <span className="subtle">{cluster}</span>
                  )}
                </div>

                <div className="account-actions">
                  <button
                    type="button"
                    className="secondary"
                    disabled={busy}
                    onClick={() => void handleCheckBalance(w)}
                  >
                    <RefreshCw size={13} aria-hidden="true" />
                    <span>{t("Balance")}</span>
                  </button>
                  {w.kind !== "watch" && (
                    <button
                      type="button"
                      className="secondary"
                      disabled={busy}
                      onClick={() => {
                        setError("");
                        setExportPass("");
                        setExported(null);
                        setExportWallet(w);
                      }}
                    >
                      <KeyRound size={13} aria-hidden="true" />
                      <span>{t("Export")}</span>
                    </button>
                  )}
                  <button
                    type="button"
                    className="icon-button"
                    aria-label={t("Remove")}
                    disabled={busy}
                    onClick={() => void handleDelete(w)}
                  >
                    <Trash2 size={14} aria-hidden="true" />
                  </button>
                </div>
              </article>
            );
          })}
        </div>
      )}

      {createOpen && (
        <Dialog
          title={t(backupMnemonic ? "Recovery Phrase" : "Create wallet")}
          onClose={() => {
            if (!busy && (!backupMnemonic || backupAck)) {
              setCreateOpen(false);
              setBackupMnemonic("");
            }
          }}
        >
          {backupMnemonic ? (
            <div className="account-form">
              <code className="account-key">{backupAddress}</code>
              <div className="mnemonic-grid" data-testid="mnemonic-grid">
                {backupMnemonic.split(" ").map((word, idx) => (
                  <div className="mnemonic-cell" key={`${idx}-${word}`}>
                    <span>{idx + 1}</span>
                    <strong>{word}</strong>
                  </div>
                ))}
              </div>
              <div className="account-actions">
                <button
                  type="button"
                  className="secondary"
                  onClick={() => void copyText("mnemonic", backupMnemonic)}
                >
                  <Copy size={14} aria-hidden="true" />
                  <span>{copiedKey === "mnemonic" ? t("Copied") : t("Copy")}</span>
                </button>
                <button
                  type="button"
                  className="secondary"
                  onClick={() =>
                    downloadBackupFile(createName, backupAddress, backupMnemonic)
                  }
                >
                  <Download size={14} aria-hidden="true" />
                  <span>{t("Backup")}</span>
                </button>
              </div>
              <label className="settings-checkbox-label">
                <input
                  type="checkbox"
                  checked={backupAck}
                  onChange={(e) => setBackupAck(e.target.checked)}
                />
                <span>{t("I saved my recovery phrase")}</span>
              </label>
              <button
                type="button"
                className="primary"
                disabled={!backupAck}
                onClick={() => {
                  setCreateOpen(false);
                  setBackupMnemonic("");
                }}
              >
                {t("Done")}
              </button>
            </div>
          ) : (
            <form
              className="account-form"
              onSubmit={(e) => void handleCreateSubmit(e)}
            >
              <label>
                {t("Name")}
                <input
                  value={createName}
                  onChange={(e) => setCreateName(e.target.value)}
                  required
                  placeholder="Solana Main"
                />
              </label>
              <div className="trade-ticket-pair">
                <label>
                  {t("Role")}
                  <select
                    value={createRole}
                    onChange={(e) =>
                      setCreateRole(
                        e.target.value as "personal" | "agent_vault",
                      )
                    }
                  >
                    <option value="personal">{t("Personal")}</option>
                    <option value="agent_vault">Agent Vault</option>
                  </select>
                </label>
                <label>
                  {t("Recovery Phrase")}
                  <select
                    value={createWords}
                    onChange={(e) =>
                      setCreateWords(Number(e.target.value) === 24 ? 24 : 12)
                    }
                  >
                    <option value={12}>12 words</option>
                    <option value={24}>24 words</option>
                  </select>
                </label>
              </div>
              <label>
                {t("Passphrase")}
                <input
                  type="password"
                  autoComplete="new-password"
                  minLength={8}
                  required
                  value={createPass}
                  onChange={(e) => setCreatePass(e.target.value)}
                />
              </label>
              <label>
                {t("Confirm passphrase")}
                <input
                  type="password"
                  autoComplete="new-password"
                  minLength={8}
                  required
                  value={createConfirm}
                  onChange={(e) => setCreateConfirm(e.target.value)}
                />
              </label>
              {error && <p role="alert">{error}</p>}
              <button type="submit" className="primary" disabled={busy}>
                {t("Create")}
              </button>
            </form>
          )}
        </Dialog>
      )}

      {importOpen && (
        <Dialog
          title={t("Import")}
          onClose={() => {
            if (!busy) setImportOpen(false);
          }}
        >
          <form
            className="account-form"
            onSubmit={(e) => void handleImportSubmit(e)}
          >
            <div className="appearance-segmented" role="tablist">
              <button
                type="button"
                role="tab"
                className={importTab === "mnemonic" ? "active" : undefined}
                onClick={() => setImportTab("mnemonic")}
              >
                {t("Recovery Phrase")}
              </button>
              <button
                type="button"
                role="tab"
                className={importTab === "privateKey" ? "active" : undefined}
                onClick={() => setImportTab("privateKey")}
              >
                {t("Private Key")}
              </button>
              <button
                type="button"
                role="tab"
                className={importTab === "watch" ? "active" : undefined}
                onClick={() => setImportTab("watch")}
              >
                {t("Watch Only")}
              </button>
            </div>
            <label>
              {t("Name")}
              <input
                value={importName}
                onChange={(e) => setImportName(e.target.value)}
                required
              />
            </label>
            {importTab !== "watch" && (
              <label>
                {t("Role")}
                <select
                  value={importRole}
                  onChange={(e) =>
                    setImportRole(e.target.value as "personal" | "agent_vault")
                  }
                >
                  <option value="personal">{t("Personal")}</option>
                  <option value="agent_vault">Agent Vault</option>
                </select>
              </label>
            )}
            <label>
              {importTab === "mnemonic"
                ? t("Recovery Phrase")
                : importTab === "privateKey"
                  ? t("Private Key")
                  : t("Public address")}
              <textarea
                rows={3}
                value={importSecret}
                onChange={(e) => setImportSecret(e.target.value)}
                required
                placeholder={
                  importTab === "mnemonic"
                    ? "12 or 24 words"
                    : importTab === "privateKey"
                      ? "Base58 or [b0, ...]"
                      : "Solana Base58 public address"
                }
              />
            </label>
            {importTab !== "watch" && (
              <label>
                {t("Passphrase")}
                <input
                  type="password"
                  autoComplete="new-password"
                  minLength={8}
                  required
                  value={importPass}
                  onChange={(e) => setImportPass(e.target.value)}
                />
              </label>
            )}
            {error && <p role="alert">{error}</p>}
            <button type="submit" className="primary" disabled={busy}>
              {t("Import")}
            </button>
          </form>
        </Dialog>
      )}

      {exportWallet && (
        <Dialog
          title={`${t("Export")} · ${exportWallet.name}`}
          onClose={() => {
            if (!busy) {
              setExportWallet(null);
              setExported(null);
              setExportPass("");
            }
          }}
        >
          {exported ? (
            <div className="account-form">
              {exported.mnemonic && (
                <div>
                  <span className="subtle">{t("Recovery Phrase")}</span>
                  <div className="mnemonic-grid">
                    {exported.mnemonic.split(" ").map((w, i) => (
                      <div className="mnemonic-cell" key={`${i}-${w}`}>
                        <span>{i + 1}</span>
                        <strong>{w}</strong>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              <label>
                {t("Private Key")} (Base58)
                <input readOnly value={exported.privateKeyBase58} />
              </label>
              <label>
                Solana CLI JSON
                <input readOnly value={exported.keypairJson} />
              </label>
              <div className="account-actions">
                {exported.mnemonic && (
                  <button
                    type="button"
                    className="secondary"
                    onClick={() =>
                      void copyText("exp-seed", exported.mnemonic!)
                    }
                  >
                    <Copy size={14} /> {t("Recovery Phrase")}
                  </button>
                )}
                <button
                  type="button"
                  className="secondary"
                  onClick={() =>
                    void copyText("exp-pk", exported.privateKeyBase58)
                  }
                >
                  <Copy size={14} /> {t("Private Key")}
                </button>
                <button
                  type="button"
                  className="primary"
                  onClick={() => {
                    setExportWallet(null);
                    setExported(null);
                  }}
                >
                  {t("Done")}
                </button>
              </div>
            </div>
          ) : (
            <form
              className="account-form"
              onSubmit={(e) => void handleExportSubmit(e)}
            >
              <label>
                {t("Passphrase")}
                <input
                  type="password"
                  autoComplete="current-password"
                  required
                  value={exportPass}
                  onChange={(e) => setExportPass(e.target.value)}
                />
              </label>
              {error && <p role="alert">{error}</p>}
              <button type="submit" className="primary" disabled={busy}>
                {t("Unlock")}
              </button>
            </form>
          )}
        </Dialog>
      )}
    </div>
  );
}
