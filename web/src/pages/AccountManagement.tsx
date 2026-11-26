import { RPCCheck } from "./RPCCheck";
import { WalletRead } from "./WalletSnapshot";
import { SolanaWalletsPanel } from "../components/SolanaWalletsPanel";
import { useUI } from "../ui-preferences";
import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";
import { Badge, Dialog } from "../components/shared";
import { Pencil, Plug } from "../icons";
import {
  accountAddress,
  walletNetwork,
  walletNetworks,
  remoteWalletOwnerCommand,
  walletOwnerCommands,
  walletOwnerLink,
  walletSetupLabel,
  walletSetupReference,
  type WalletMode,
  type Account,
  type AccountsRecord,
  type AccountsView,
} from "../accounts";
import {
  getAgentVaults,
  getExecutionStatus,
  type AgentVaultsResponse,
  type ExecutionStatus,
} from "../execution";
import "../accounts.css";

const FixtureAccountAdvanced = lazy(() =>
  import("./FixtureAccountAdvanced").then((m) => ({
    default: m.FixtureAccountAdvanced,
  })),
);

export function AccountManagement() {
  const { t: uiText } = useUI();
  const core = useCore();
  const { agents, navigate } = useWorkspace();
  const instance = useRef(core.instanceId);
  instance.current = core.instanceId;
  const [loaded, setLoaded] = useState<{
    instance: string;
    view: AccountsView;
  }>();
  const view = loaded?.instance === core.instanceId ? loaded.view : null;
  const [rpcID, setRPCID] = useState("");
  const rpcAccount = view?.record.accounts.find((a) => a.id === rpcID);
  const [adding, setAdding] = useState(false);
  const [backupOpen, setBackupOpen] = useState(false);
  const [portableBundle, setPortableBundle] = useState<unknown>();
  const [drafts, setDrafts] = useState<
    Record<
      string,
      {
        name: string;
        network: string;
        mode: WalletMode;
        address: string;
      }
    >
  >({});
  const draft = drafts[core.instanceId] ?? {
    name: "",
    network: "devnet",
    mode: "watch",
    address: "",
  };
  function updateDraft(patch: Partial<typeof draft>) {
    const owner = core.instanceId;
    setDrafts((all) => ({ ...all, [owner]: { ...draft, ...patch } }));
  }
  function acceptView(next: AccountsView, owner: string) {
    if (instance.current === owner) setLoaded({ instance: owner, view: next });
  }
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState("");
  const [newName, setNewName] = useState("");
  const [notice, setNotice] = useState("");
  const [remoteWalletAction, setRemoteWalletAction] = useState<
    "setup" | "import" | "recover"
  >();
  const [remoteWalletOwner, setRemoteWalletOwner] = useState("");
  const [execStatus, setExecStatus] = useState<ExecutionStatus | null>(null);
  const [vaults, setVaults] = useState<AgentVaultsResponse | null>(null);
  const request = core.request;
  useEffect(() => {
    let active = true;
    const owner = core.instanceId;
    setRPCID("");
    setEditing("");
    setAdding(false);
    setBackupOpen(false);
    setPortableBundle(undefined);
    setRemoteWalletAction(undefined);
    setRemoteWalletOwner("");
    setExecStatus(null);
    setVaults(null);
    setBusy(false);
    setError("");
    setNotice("");
    request<AccountsView>("accounts")
      .then((v) => {
        if (active) acceptView(v, owner);
      })
      .catch((e) => {
        if (active) setError(String(e.message));
      });
    void Promise.all([
      getExecutionStatus(core).catch(() => null),
      getAgentVaults(core).catch(() => null),
    ]).then(([es, vs]) => {
      if (active) {
        setExecStatus(es);
        setVaults(vs);
      }
    });
    return () => {
      active = false;
    };
  }, [request, core.instanceId]);
  async function refresh() {
    const owner = core.instanceId;
    setBusy(true);
    setError("");
    try {
      const [v, es, vs] = await Promise.all([
        request<AccountsView>("accounts"),
        getExecutionStatus(core).catch(() => null),
        getAgentVaults(core).catch(() => null),
      ]);
      acceptView(v, owner);
      if (instance.current === owner) {
        setExecStatus(es);
        setVaults(vs);
      }
    } catch (e) {
      if (instance.current !== owner) return false;
      setError(e instanceof Error ? e.message : "Accounts unavailable");
    } finally {
      if (instance.current === owner) setBusy(false);
    }
  }
  async function save(record: AccountsRecord) {
    const owner = core.instanceId;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      acceptView(await request<AccountsView>("accounts", "PUT", record), owner);
      return instance.current === owner;
    } catch (e) {
      if (instance.current !== owner) return false;
      setError(e instanceof Error ? e.message : "Account change failed");
      // Refresh public state for review. Never retry the rejected mutation.
      try {
        acceptView(await request<AccountsView>("accounts"), owner);
      } catch {
        /* keep explicit failure */
      }
      return false;
    } finally {
      if (instance.current === owner) setBusy(false);
    }
  }
  async function addWallet(e: FormEvent) {
    e.preventDefault();
    if (!view || busy) return;
    const owner = core.instanceId;
    const reference = walletSetupReference(view);
    const network =
      draft.mode === "watch" || !walletNetwork(view.setup?.network ?? "")
        ? draft.network
        : view.setup!.network;
    if (draft.mode === "watch" ? !view.setup?.networkWatchEnabled : !reference)
      return;
    setBusy(true);
    setError("");
    try {
      const next = await request<AccountsView>("accounts/setup", "PUT", {
        revision: view.record.revision,
        action: draft.mode === "watch" ? "watch" : "attach",
        name: draft.name.trim(),
        network,
        ...(draft.mode === "watch"
          ? { address: draft.address.trim() }
          : { reference }),
      });
      if (instance.current !== owner) return;
      acceptView(next, owner);
      setAdding(false);
      setNotice(uiText("Wallet saved"));
      updateDraft({ name: "", address: "" });
    } catch (e) {
      if (instance.current === owner)
        setError(
          e instanceof Error ? e.message : uiText("Wallet setup failed"),
        );
    } finally {
      if (instance.current === owner) setBusy(false);
    }
  }
  const hasNetworkWallets = !!view?.record.accounts.some(
    (a) => a.network === "devnet" || a.network === "mainnet-beta",
  );
  async function exportWallets() {
    if (busy || !hasNetworkWallets) return;
    const owner = core.instanceId;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const bundle = await request<{ version: number; wallets: unknown[] }>(
        "accounts/portable",
      );
      if (instance.current !== owner) return;
      const url = URL.createObjectURL(
        new Blob([JSON.stringify(bundle, null, 2)], {
          type: "application/json",
        }),
      );
      try {
        const link = document.createElement("a");
        link.href = url;
        link.download = "fased-wallets-config.json";
        link.click();
      } finally {
        setTimeout(() => URL.revokeObjectURL(url), 0);
      }
      setNotice(uiText("Wallets exported"));
    } catch (e) {
      if (instance.current === owner)
        setError(
          e instanceof Error ? e.message : uiText("Accounts unavailable"),
        );
    } finally {
      if (instance.current === owner) setBusy(false);
    }
  }
  async function importWallets() {
    if (!view || busy || !portableBundle) return;
    const owner = core.instanceId;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const res = await request<
        AccountsView & { imported: number; skipped: number }
      >("accounts/portable", "PUT", {
        revision: view.record.revision,
        bundle: portableBundle,
      });
      if (instance.current !== owner) return;
      acceptView(res, owner);
      setPortableBundle(undefined);
      setBackupOpen(false);
      setNotice(
        res.imported > 0
          ? uiText("Imported {imported} wallets", { imported: res.imported })
          : uiText("All wallets already saved"),
      );
    } catch (e) {
      if (instance.current === owner)
        setError(
          e instanceof Error ? e.message : uiText("Invalid wallet backup"),
        );
    } finally {
      if (instance.current === owner) setBusy(false);
    }
  }
  async function remove(a: Account) {
    if (!view) return;
    if (
      await save({
        ...view.record,
        accounts: view.record.accounts.filter((x) => x.id !== a.id),
        proposals: view.record.proposals.filter((x) => x.accountId !== a.id),
      })
    )
      setNotice(
        "Public record removed. Signer keys, grants and spending history are unchanged.",
      );
  }
  async function rebind(a: Account) {
    const ref = view?.fixture.status?.account;
    if (
      !view ||
      !ref ||
      ref.network !== a.network ||
      ref.version !== a.signer?.version
    )
      return;
    await save({
      ...view.record,
      accounts: view.record.accounts.map((x) =>
        x.id === a.id
          ? {
              ...x,
              signer: ref,
              publicKey: ref.publicKey,
              network: ref.network,
            }
          : x,
      ),
      proposals: view.record.proposals.filter((x) => x.accountId !== a.id),
    });
  }
  const ref = view?.fixture.status?.account;
  const setupReference = view && walletSetupReference(view);
  const managedNetwork = view?.setup?.network ?? "";
  const network =
    draft.mode !== "watch" && walletNetwork(managedNetwork)
      ? managedNetwork
      : draft.network;
  const commands =
    view && draft.mode !== "watch"
      ? walletOwnerCommands(view, draft.mode, network)
      : [];
  const fixtureTools = agents.view?.aiFixtureAvailable === true;
  const protectedSetupHeld = !fixtureTools && !setupReference;
  const ownerWindowReady =
    !!view?.setup?.ownerSupported &&
    ["empty", "not-installed", "configuration-required"].includes(
      view.setup.state,
    ) &&
    walletNetwork(network) &&
    !!view.setup.source;
  const remoteWalletCommand =
    remoteWalletAction && view?.setup?.remotePackage
      ? remoteWalletOwnerCommand(
          remoteWalletOwner,
          view.setup.remotePackage,
          core.instanceId,
          network,
          remoteWalletAction,
          location.origin,
        )
      : "";
  async function openOwnerWindow(action: "setup" | "import" | "recover") {
    if (!view?.setup || !ownerWindowReady) return;
    if (view.setup.source === "remote" && view.setup.remotePackage) {
      setRemoteWalletAction(action);
      return;
    }
    const link = walletOwnerLink(
      core.instanceId,
      network,
      action,
      view.setup.source === "remote" ? location.origin : "",
      view.setup.packageOwner,
    );
    if (!link) return;
    if (["wails", "wails.localhost"].includes(location.hostname)) {
      try {
        await request("installation/wallet-owner", "POST", { link });
      } catch (e) {
        setError(
          e instanceof Error ? e.message : uiText("Wallet setup failed"),
        );
      }
    } else {
      location.href = link;
    }
  }
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  useEffect(() => {
    if (!adding || draft.mode === "watch") return;
    const reconnect = () => {
      void refreshRef.current();
    };
    window.addEventListener("focus", reconnect);
    return () => window.removeEventListener("focus", reconnect);
  }, [adding, draft.mode, core.instanceId]);
  const alreadyAttached =
    !!setupReference &&
    !!view?.record.accounts.some(
      (a) =>
        !!a.signer &&
        [2, 3].includes(a.signer.version) &&
        a.signer.signerId === setupReference.signerId &&
        a.signer.walletId === setupReference.walletId &&
        a.signer.epoch === setupReference.epoch &&
        a.signer.network === setupReference.network &&
        a.signer.publicKey === setupReference.publicKey,
    );
  return (
    <div className="wallet-workspace">
      <SolanaWalletsPanel />
      {rpcAccount && view && (
        <RPCCheck
          key={`${core.instanceId}:${rpcAccount.id}`}
          account={rpcAccount}
          revision={view.record.revision}
          onClose={() => setRPCID("")}
          onSaved={(v) => acceptView(v, core.instanceId)}
        />
      )}
      <div className="section-head">
        <div>
          <h2>{uiText("Host Signer")}</h2>
        </div>
        <div className="account-actions">
          <button
            className="secondary"
            disabled={busy}
            onClick={() => void refresh()}
          >
            {uiText("Refresh")}
          </button>
          <button
            className="secondary"
            disabled={!view || busy}
            onClick={() => {
              setError("");
              setNotice("");
              setPortableBundle(undefined);
              setBackupOpen(true);
            }}
          >
            {uiText("Backup")}
          </button>
          <button
            className="primary"
            disabled={!view || busy}
            onClick={() => {
              setError("");
              setAdding(true);
            }}
          >
            {uiText("Add wallet")}
          </button>
        </div>
      </div>
      <section className="panel account-panel host-signer-panel">
        {!adding && !backupOpen && error && <p role="alert">{error}</p>}
        {!adding && !backupOpen && notice && <p role="status">{notice}</p>}
        {!view ? (
          <p role="status">{uiText("Loading…")}</p>
        ) : (
          <>
            <div className="wallet-kpi-strip" data-testid="wallets-kpi-strip">
              <div className="wallet-kpi-tile">
                <span>{uiText("Typed wallets")}</span>
                <strong>{view.record.accounts.length}</strong>
              </div>
              <div className="wallet-kpi-tile">
                <span>{uiText("Watch / Manual")}</span>
                <strong>
                  {
                    view.record.accounts.filter(
                      (a) => a.mode === "watch" || a.mode === "external",
                    ).length
                  }
                </strong>
              </div>
              <div className="wallet-kpi-tile">
                <span>{uiText("Agent vaults")}</span>
                <strong>{vaults?.vaults?.length ?? 0}</strong>
              </div>
              <div className="wallet-kpi-tile">
                <span>{uiText("Execution profile")}</span>
                <strong className="mono">{execStatus?.profile || "off"}</strong>
              </div>
            </div>
            <section className="panel account-panel">
              <div className="panel-heading">
                <h2>{uiText("Accounts")}</h2>
                <Badge>{uiText("Live execution disabled")}</Badge>
              </div>
              {!view.record.accounts.length && (
                <p className="subtle">{uiText("No wallets")}</p>
              )}
              {view.record.accounts.map((a) => (
                <article
                  className="account-entry"
                  key={a.id}
                  aria-label={`Account ${a.name}`}
                >
                  <div className="panel-heading">
                    <h3>{a.name}</h3>
                    <div className="account-status">
                      <span className="subtle">
                        {a.network === "mainnet-beta"
                          ? uiText("Mainnet")
                          : a.network === "devnet"
                            ? uiText("Devnet")
                            : uiText("Demo")}
                      </span>
                      <Badge>
                        {uiText(
                          a.mode === "watch"
                            ? "Watch"
                            : a.mode === "external"
                              ? "Manual"
                              : view.accountStates[a.id] === "stale"
                                ? "Reconnect"
                                : [
                                      "current-fixture",
                                      "current-custody-held",
                                    ].includes(view.accountStates[a.id])
                                  ? walletSetupLabel(view)
                                  : "Unverified",
                        )}
                      </Badge>
                    </div>
                  </div>
                  <code className="account-key" title={a.publicKey}>
                    {accountAddress(a, view)}
                  </code>
                  <WalletRead
                    account={a}
                    revision={view.record.revision}
                    address={accountAddress(a, view)}
                    blocked={["stale", "unverified"].includes(
                      view.accountStates[a.id],
                    )}
                    settingsOpen={rpcID === a.id}
                  />
                  <div className="account-actions">
                    <button
                      className="secondary"
                      disabled={busy}
                      onClick={() => setRPCID(a.id)}
                    >
                      <Plug size={14} aria-hidden="true" /> RPC
                    </button>
                    <button
                      className="text-button"
                      disabled={busy}
                      onClick={() => {
                        setEditing(a.id);
                        setNewName(a.name);
                      }}
                    >
                      <Pencil size={14} aria-hidden="true" /> {uiText("Rename")}
                    </button>
                  </div>
                  {editing === a.id && (
                    <form
                      className="account-actions"
                      onSubmit={async (e) => {
                        e.preventDefault();
                        if (
                          await save({
                            ...view.record,
                            accounts: view.record.accounts.map((x) =>
                              x.id === a.id ? { ...x, name: newName } : x,
                            ),
                          })
                        )
                          setEditing("");
                      }}
                    >
                      <input
                        aria-label={uiText("New account name")}
                        value={newName}
                        onChange={(e) => setNewName(e.target.value)}
                        maxLength={80}
                        required
                      />
                      <button className="secondary" disabled={busy}>
                        {uiText("Save account name")}
                      </button>
                    </form>
                  )}
                  <details className="account-details">
                    <summary>{uiText("Details")}</summary>
                    <p>
                      {uiText(
                        (
                          {
                            "public-record-only": "Public reference only",
                            "current-fixture": "Current disposable reference",
                            "current-custody-held": view.fixture.status?.locked
                              ? "Locked"
                              : "Unavailable",
                            stale:
                              "Stale reference — refresh before proposing permissions",
                            unverified:
                              "Signer unavailable — reference unverified",
                          } as Record<string, string>
                        )[view.accountStates[a.id]] ?? "Reference unverified",
                      )}
                    </p>
                    {a.signer && (
                      <p className="account-key">
                        {uiText("Wallet ")}
                        {a.signer.walletId}
                        {uiText(" · Epoch ")}
                        {a.signer.epoch}
                      </p>
                    )}
                    {view.record.proposals
                      .filter((item) => item.accountId === a.id)
                      .map((item) => (
                        <div
                          className="account-permission"
                          key={item.proposal.id}
                        >
                          <span>{uiText("Owner approval")}</span>
                          <Badge>
                            {view.proposals.find(
                              (proof) => proof.id === item.proposal.id,
                            )?.receipt.state ?? uiText("Unverified")}
                          </Badge>
                        </div>
                      ))}
                    <div className="account-actions">
                      <button
                        className="text-button"
                        disabled={busy}
                        onClick={() => void remove(a)}
                      >
                        {uiText("Remove record")}
                      </button>
                      {a.mode === "fixture-managed" &&
                        view.accountStates[a.id] === "stale" &&
                        ref?.network === a.network &&
                        ref.version === a.signer?.version && (
                          <button
                            className="secondary"
                            disabled={busy}
                            onClick={() => void rebind(a)}
                          >
                            {uiText("Refresh fixture reference")}
                          </button>
                        )}
                    </div>
                  </details>
                </article>
              ))}
            </section>
            <section
              className="panel account-panel"
              data-testid="wallets-execution-panel"
            >
              <div className="panel-heading">
                <h2>{uiText("Execution & Agent Vaults")}</h2>
                <div className="account-status">
                  <Badge>
                    {uiText(
                      execStatus?.mainnetLocked === false
                        ? "Mainnet capped"
                        : "Paper / Off",
                    )}
                  </Badge>
                  <button
                    type="button"
                    className="secondary"
                    onClick={() => navigate("trade")}
                  >
                    {uiText("Trade")}
                  </button>
                </div>
              </div>
              <div className="wallet-venue-grid">
                {[
                  {
                    name: "Solana",
                    state: execStatus?.solanaMainnetReady
                      ? "Mainnet-Capped"
                      : execStatus?.solanaDevnetReady
                        ? "Devnet"
                        : "Paper / Off",
                  },
                  {
                    name: "Hyperliquid",
                    state: execStatus?.hyperliquidMainnet
                      ? "Mainnet-Capped"
                      : execStatus?.hyperliquidTestnet
                        ? "Testnet"
                        : "Paper / Off",
                  },
                  {
                    name: "Lighter",
                    state: execStatus?.lighterTestnetReady
                      ? "Testnet"
                      : "Paper / Off",
                  },
                  {
                    name: "Polymarket",
                    state: execStatus?.polymarketTestnetReady
                      ? "Testnet"
                      : "Paper / Off",
                  },
                  {
                    name: "Kalshi",
                    state: execStatus?.kalshiDemoReady ? "Demo" : "Paper / Off",
                  },
                  {
                    name: "WEN SAT",
                    state: "Held",
                  },
                ].map((venue) => (
                  <div className="wallet-venue-chip" key={venue.name}>
                    <span>{uiText(venue.name)}</span>
                    <Badge>{uiText(venue.state)}</Badge>
                  </div>
                ))}
              </div>
              <div className="wallet-vault-list">
                {(vaults?.vaults?.length ?? 0) === 0 ? (
                  <p className="subtle">{uiText("No agent vaults")}</p>
                ) : (
                  vaults!.vaults.map((v) => (
                    <div className="wallet-venue-chip" key={v.id}>
                      <strong>{v.label || v.id}</strong>
                      <span className="subtle mono">
                        {`${v.network || "paper"} · ${(v.allowedVenues ?? []).join(", ") || "paper"}`}
                      </span>
                    </div>
                  ))
                )}
              </div>
            </section>
            {adding && (
              <Dialog
                title={uiText("Add wallet")}
                onClose={() => {
                  if (!busy) setAdding(false);
                }}
              >
                <form
                  className="account-form wallet-setup"
                  onSubmit={(e) => void addWallet(e)}
                >
                  <div
                    className="account-mode"
                    role="group"
                    aria-label={uiText("Wallet type")}
                  >
                    {(["watch", "create", "import"] as const)
                      .filter(
                        (mode) =>
                          mode !== "import" || fixtureTools || !setupReference,
                      )
                      .map((mode) => (
                        <button
                          key={mode}
                          type="button"
                          className={
                            draft.mode === mode ? "primary" : "secondary"
                          }
                          aria-pressed={draft.mode === mode}
                          disabled={
                            busy ||
                            (mode !== "watch" &&
                              (protectedSetupHeld ||
                                view?.setup?.state ===
                                  "configuration-required") &&
                              !ownerWindowReady)
                          }
                          title={
                            mode !== "watch" &&
                            protectedSetupHeld &&
                            !ownerWindowReady
                              ? uiText("Protected setup unavailable")
                              : undefined
                          }
                          onClick={() => {
                            updateDraft({ mode });
                            if (mode === "watch")
                              setRemoteWalletAction(undefined);
                            if (
                              mode !== "watch" &&
                              (protectedSetupHeld ||
                                view?.setup?.state ===
                                  "configuration-required") &&
                              ownerWindowReady
                            )
                              void openOwnerWindow(
                                mode === "create" ? "setup" : "import",
                              );
                          }}
                        >
                          {uiText(
                            mode === "watch"
                              ? "Watch"
                              : mode === "create"
                                ? fixtureTools || !setupReference
                                  ? "Create"
                                  : "Attach"
                                : "Import",
                          )}
                        </button>
                      ))}
                    {(view?.setup?.packageOwner ||
                      view?.setup?.remotePackage) &&
                      ownerWindowReady && (
                        <button
                          type="button"
                          className="secondary"
                          disabled={busy}
                          onClick={() => void openOwnerWindow("recover")}
                        >
                          {uiText("Recover")}
                        </button>
                      )}
                  </div>
                  {remoteWalletAction &&
                    ownerWindowReady &&
                    view?.setup?.remotePackage && (
                      <div className="owner-helper">
                        <label>
                          {uiText("Remote SSH user")}
                          <input
                            autoComplete="off"
                            value={remoteWalletOwner}
                            onChange={(e) =>
                              setRemoteWalletOwner(e.target.value)
                            }
                            maxLength={32}
                          />
                        </label>
                        <label>
                          {uiText("Run on your computer")}
                          <input readOnly value={remoteWalletCommand || ""} />
                        </label>
                        <button
                          type="button"
                          className="secondary"
                          disabled={!remoteWalletCommand}
                          onClick={async () => {
                            try {
                              await navigator.clipboard.writeText(
                                remoteWalletCommand || "",
                              );
                              setNotice(uiText("Owner command copied"));
                            } catch {
                              setError(
                                uiText("Select and copy the owner command"),
                              );
                            }
                          }}
                        >
                          {uiText("Copy command")}
                        </button>
                      </div>
                    )}
                  {protectedSetupHeld && !ownerWindowReady && (
                    <p className="subtle" role="status">
                      {uiText("Protected setup unavailable")}
                    </p>
                  )}
                  <label>
                    {uiText("Name")}
                    <input
                      aria-label={uiText("Wallet name")}
                      value={draft.name}
                      onChange={(e) => updateDraft({ name: e.target.value })}
                      maxLength={80}
                      required
                      disabled={busy}
                    />
                  </label>
                  <label>
                    {uiText("Network")}
                    <select
                      aria-label={uiText("Network")}
                      value={network}
                      disabled={
                        busy ||
                        (draft.mode !== "watch" &&
                          walletNetwork(managedNetwork))
                      }
                      onChange={(e) => updateDraft({ network: e.target.value })}
                    >
                      {walletNetworks.map((n) => (
                        <option key={n} value={n}>
                          {uiText(n === "devnet" ? "Devnet" : "Mainnet")}
                        </option>
                      ))}
                    </select>
                  </label>
                  {draft.mode === "watch" ? (
                    <>
                      <label>
                        {uiText("Public address")}
                        <input
                          aria-label={uiText("Public address")}
                          value={draft.address}
                          onChange={(e) =>
                            updateDraft({ address: e.target.value })
                          }
                          maxLength={44}
                          autoComplete="off"
                          spellCheck={false}
                          required
                          disabled={busy}
                        />
                      </label>
                      {!view.setup?.networkWatchEnabled && (
                        <Badge>{uiText("Watch unavailable")}</Badge>
                      )}
                    </>
                  ) : (
                    <>
                      <div className="account-status">
                        <Badge>{uiText(walletSetupLabel(view))}</Badge>
                        {fixtureTools && <Badge>{uiText("Test only")}</Badge>}
                      </div>
                      {!view.setup?.ownerSupported && (
                        <p className="subtle">{uiText("Linux required")}</p>
                      )}
                      {fixtureTools && commands.length > 0 && (
                        <div className="wallet-terminal">
                          <span className="subtle">
                            {uiText("Instance terminal")}
                          </span>
                          {commands.map((command) => (
                            <code className="account-key" key={command}>
                              {command}
                            </code>
                          ))}
                          {view.setup?.state === "not-installed" && (
                            <span className="subtle">
                              {uiText("Return here, then refresh")}
                            </span>
                          )}
                        </div>
                      )}
                      {setupReference && (
                        <code className="account-key">
                          {view.fixture.status!.publicIdentity!.address}
                        </code>
                      )}
                      {alreadyAttached && (
                        <Badge>{uiText("Already added")}</Badge>
                      )}
                      <button
                        type="button"
                        className="secondary"
                        disabled={busy}
                        onClick={() => void refresh()}
                      >
                        {uiText("Refresh status")}
                      </button>
                    </>
                  )}
                  {error && <p role="alert">{error}</p>}
                  <button
                    className="primary"
                    disabled={
                      busy ||
                      !draft.name.trim() ||
                      view.record.accounts.length >= 8 ||
                      (draft.mode === "watch"
                        ? !view.setup?.networkWatchEnabled ||
                          !draft.address.trim()
                        : !setupReference || alreadyAttached)
                    }
                  >
                    {uiText(draft.mode === "watch" ? "Save" : "Attach")}
                  </button>
                </form>
              </Dialog>
            )}
            {backupOpen && (
              <Dialog
                title={uiText("Wallet backup")}
                onClose={() => {
                  if (!busy) setBackupOpen(false);
                }}
              >
                <div className="account-form">
                  <div className="account-actions">
                    <button
                      type="button"
                      className="secondary"
                      disabled={busy || !hasNetworkWallets}
                      onClick={() => void exportWallets()}
                    >
                      {uiText("Export wallets")}
                    </button>
                  </div>
                  <label>
                    {uiText("Wallet backup file")}
                    <input
                      type="file"
                      accept="application/json,.json"
                      aria-label={uiText("Wallet backup file")}
                      disabled={busy}
                      onChange={async (e) => {
                        const file = e.target.files?.[0];
                        const owner = core.instanceId;
                        setPortableBundle(undefined);
                        setError("");
                        if (!file) return;
                        try {
                          if (file.size > 16384) throw new Error();
                          const parsed: unknown = JSON.parse(await file.text());
                          if (instance.current === owner)
                            setPortableBundle(parsed);
                        } catch {
                          if (instance.current === owner)
                            setError(uiText("Invalid wallet backup"));
                        }
                      }}
                    />
                  </label>
                  {error && <p role="alert">{error}</p>}
                  {notice && <p role="status">{notice}</p>}
                  <button
                    type="button"
                    className="primary"
                    disabled={
                      busy ||
                      !portableBundle ||
                      !view.setup?.networkWatchEnabled
                    }
                    onClick={() => void importWallets()}
                  >
                    {uiText("Import wallets")}
                  </button>
                </div>
              </Dialog>
            )}
            {agents.view?.aiFixtureAvailable && (
              <Suspense fallback={null}>
                <FixtureAccountAdvanced
                  instanceId={core.instanceId}
                  view={view}
                  busy={busy}
                  save={save}
                  setError={setError}
                  setNotice={setNotice}
                />
              </Suspense>
            )}
          </>
        )}
      </section>
    </div>
  );
}
