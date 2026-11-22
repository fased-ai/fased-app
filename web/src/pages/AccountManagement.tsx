import { RPCCheck } from "./RPCCheck";
import { WalletRead } from "./WalletSnapshot";
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
import { Badge, Dialog, SectionHead } from "../components/shared";
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
import "../accounts.css";

const FixtureAccountAdvanced = lazy(() =>
  import("./FixtureAccountAdvanced").then((m) => ({
    default: m.FixtureAccountAdvanced,
  })),
);

export function AccountManagement() {
  const { t: uiText } = useUI();
  const core = useCore();
  const { agents } = useWorkspace();
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
  const request = core.request;
  useEffect(() => {
    let active = true;
    const owner = core.instanceId;
    setRPCID("");
    setEditing("");
    setAdding(false);
    setRemoteWalletAction(undefined);
    setRemoteWalletOwner("");
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
    return () => {
      active = false;
    };
  }, [request, core.instanceId]);
  async function refresh() {
    const owner = core.instanceId;
    setBusy(true);
    setError("");
    try {
      acceptView(await request<AccountsView>("accounts"), owner);
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
      {rpcAccount && view && (
        <RPCCheck
          key={`${core.instanceId}:${rpcAccount.id}`}
          account={rpcAccount}
          revision={view.record.revision}
          onClose={() => setRPCID("")}
          onSaved={(v) => acceptView(v, core.instanceId)}
        />
      )}
      <SectionHead
        title={uiText("Wallets")}
        action={
          <div className="account-actions">
            <button
              className="secondary"
              disabled={busy}
              onClick={() => void refresh()}
            >
              {uiText("Refresh")}
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
        }
      />
      {!adding && error && <p role="alert">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {!view ? (
        <p role="status">{uiText("Loading…")}</p>
      ) : (
        <>
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
                    RPC
                  </button>
                  <button
                    className="text-button"
                    disabled={busy}
                    onClick={() => {
                      setEditing(a.id);
                      setNewName(a.name);
                    }}
                  >
                    {uiText("Rename")}
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
                  {(view?.setup?.packageOwner || view?.setup?.remotePackage) &&
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
                          onChange={(e) => setRemoteWalletOwner(e.target.value)}
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
                      (draft.mode !== "watch" && walletNetwork(managedNetwork))
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
    </div>
  );
}
