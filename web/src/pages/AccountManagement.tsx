import { RPCCheck } from "./RPCCheck";
import { useUI } from "../ui-preferences";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useCore } from "../core";
import { Badge, Dialog, SectionHead } from "../components/shared";
import {
  accountAddress,
  walletNetwork,
  walletNetworks,
  walletOwnerCommands,
  walletSetupLabel,
  walletSetupReference,
  type WalletMode,
  fixtureNetwork,
  publicID,
  type Account,
  type AccountsRecord,
  type AccountsView,
  type Proposal,
} from "../accounts";
import "../accounts.css";

export function AccountManagement() {
  const { t: uiText } = useUI();
  const core = useCore();
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
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [mode, setMode] = useState<"watch" | "external">("watch");
  const [selected, setSelected] = useState("");
  const [walletBudget, setWalletBudget] = useState("100");
  const [botBudget, setBotBudget] = useState("50");
  const [maxOperation, setMaxOperation] = useState("10");
  const [minutes, setMinutes] = useState("15");
  const [botID, setBotID] = useState(publicID);
  const [editing, setEditing] = useState("");
  const [newName, setNewName] = useState("");
  const [notice, setNotice] = useState("");
  const request = core.request;
  useEffect(() => {
    let active = true;
    const owner = core.instanceId;
    setRPCID("");
    setSelected("");
    setEditing("");
    setAdding(false);
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
  async function addPublic(e: FormEvent) {
    e.preventDefault();
    if (!view) return;
    if (
      await save({
        ...view.record,
        accounts: [
          ...view.record.accounts,
          {
            version: 1,
            id: publicID(),
            name,
            mode,
            publicKey: key,
            network: fixtureNetwork,
          },
        ],
      })
    ) {
      setName("");
      setKey("");
      setNotice(
        "Public record saved. No wallet connected or permission granted.",
      );
    }
  }
  async function addSigner() {
    const ref = view?.fixture.status?.account;
    if (!view || ref?.version !== 1) return;
    await save({
      ...view.record,
      accounts: [
        ...view.record.accounts,
        {
          version: 1,
          id: publicID(),
          name: "Wallet",
          mode: "fixture-managed",
          publicKey: ref.publicKey,
          network: ref.network,
          signer: ref,
        },
      ],
    });
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
  async function propose(e: FormEvent) {
    e.preventDefault();
    if (!view) return;
    const a = view.record.accounts.find((x) => x.id === selected);
    if (!a?.signer) return;
    const duration = Number(minutes);
    if (!Number.isInteger(duration) || duration < 1 || duration > 1440) {
      setError("Choose an expiry from 1 to 1440 minutes.");
      return;
    }
    const id = publicID(),
      policy = {
        account: a.signer,
        asset: "fixture-credit:v1",
        budget: walletBudget,
        maxOperation,
        destinations: ["fixture:treasury"],
        expiresAt: Math.floor(Date.now() / 1000) + duration * 60,
      };
    const proposal: Proposal = {
      version: 1,
      id,
      policy,
      grant: { ...policy, id, botId: botID, budget: botBudget, revoked: false },
    };
    if (
      await save({
        ...view.record,
        proposals: [...view.record.proposals, { accountId: a.id, proposal }],
      })
    )
      setNotice("Proposal saved for owner review. No authority granted.");
  }
  function download(p: Proposal) {
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(p, null, 2) + "\n"], {
        type: "application/json",
      }),
    );
    const a = document.createElement("a");
    a.href = url;
    a.download = `fased-fixture-proposal-${p.id}.json`;
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  const ref = view?.fixture.status?.account;
  const managed =
    view?.record.accounts.filter(
      (a) =>
        a.mode === "fixture-managed" &&
        a.signer?.version === 1 &&
        view.accountStates[a.id] === "current-fixture",
    ) ?? [];
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
  const alreadyAttached =
    !!setupReference &&
    !!view?.record.accounts.some(
      (a) =>
        a.signer?.version === 2 &&
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
              <Badge>{uiText("Test only")}</Badge>
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
                              : view.accountStates[a.id] === "current-fixture"
                                ? walletSetupLabel(view)
                                : "Unverified",
                      )}
                    </Badge>
                  </div>
                </div>
                <code className="account-key" title={a.publicKey}>
                  {accountAddress(a, view)}
                </code>
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
                          stale:
                            "Stale reference — refresh before proposing permissions",
                          unverified:
                            "Signer unavailable — reference unverified",
                        } as Record<string, string>
                      )[view.accountStates[a.id]] ?? "Reference unverified",
                    )}{" "}
                    {uiText("· Balance unavailable")}
                  </p>
                  {a.signer && (
                    <p className="account-key">
                      {uiText("Wallet ")}
                      {a.signer.walletId}
                      {uiText(" · Epoch ")}
                      {a.signer.epoch}
                    </p>
                  )}
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
                  {(["watch", "create", "import"] as const).map((mode) => (
                    <button
                      key={mode}
                      type="button"
                      className={draft.mode === mode ? "primary" : "secondary"}
                      aria-pressed={draft.mode === mode}
                      disabled={busy}
                      onClick={() => updateDraft({ mode })}
                    >
                      {uiText(
                        mode === "watch"
                          ? "Watch"
                          : mode === "create"
                            ? "Create"
                            : "Import",
                      )}
                    </button>
                  ))}
                </div>
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
                      <Badge>{uiText("Test only")}</Badge>
                    </div>
                    {!view.setup?.ownerSupported && (
                      <p className="subtle">{uiText("Linux required")}</p>
                    )}
                    {commands.length > 0 && (
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
                            {uiText("Open the installed instance URL")}
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
          <details className="account-advanced">
            <summary>{uiText("Advanced")}</summary>
            <section className="panel account-panel">
              <h2>{uiText("Add a public record")}</h2>
              <form
                className="account-form"
                onSubmit={(e) => void addPublic(e)}
              >
                <label>
                  {uiText("Name")}
                  <input
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    maxLength={80}
                    required
                  />
                </label>
                <label>
                  {uiText("Capability mode")}
                  <select
                    aria-label={uiText("Capability mode")}
                    value={mode}
                    onChange={(e) => setMode(e.target.value as typeof mode)}
                  >
                    <option value="watch">{uiText("Watch-only record")}</option>
                    <option value="external">
                      {uiText("External/manual record")}
                    </option>
                  </select>
                </label>
                <label>
                  {uiText("Disposable public key (64 lowercase hex)")}
                  <input
                    value={key}
                    onChange={(e) => setKey(e.target.value)}
                    pattern="[0-9a-f]{64}"
                    required
                  />
                </label>
                <button
                  className="secondary"
                  disabled={busy || view.record.accounts.length >= 8}
                >
                  {uiText("Save public account")}
                </button>
              </form>
              <div className="account-entry">
                <h3>{uiText("Isolated disposable signer")}</h3>
                <p>
                  {ref
                    ? view.fixture.status?.recoveryRequired
                      ? uiText("Recovery hold — new permissions blocked.")
                      : view.fixture.status?.locked
                        ? uiText(
                            "Locked. Owner approval takes place in the terminal.",
                          )
                        : uiText(
                            "Manually unlocked fixture. Real execution is disabled.",
                          )
                    : view.fixture.available
                      ? uiText(
                          "Signer reachable. Complete disposable custody setup in the owner terminal; this UI never creates keys.",
                        )
                      : uiText(
                          "Not configured. Ordinary installation never creates a signer or keys.",
                        )}
                </p>
                <button
                  className="secondary"
                  onClick={() => void addSigner()}
                  disabled={
                    busy ||
                    ref?.version !== 1 ||
                    view.record.accounts.length >= 8 ||
                    managed.length > 0
                  }
                >
                  {uiText("Record disposable signer")}
                </button>
              </div>
            </section>
            <section className="panel account-panel">
              <div className="panel-heading">
                <h2>{uiText("Permission proposals")}</h2>
                <Badge>{uiText("OWNER TERMINAL REQUIRED")}</Badge>
              </div>
              <p>
                {uiText(
                  "A persona or app session cannot approve a mandate. Wallet lifetime caps include earlier spent and held units.",
                )}
              </p>
              <form
                className="account-form proposal-fields"
                onSubmit={(e) => void propose(e)}
              >
                <label>
                  {uiText("Fixture account")}
                  <select
                    aria-label={uiText("Fixture account")}
                    value={selected}
                    onChange={(e) => setSelected(e.target.value)}
                    required
                  >
                    <option value="">
                      {uiText("Select a current fixture reference")}
                    </option>
                    {managed.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  {uiText("Wallet lifetime cap")}
                  <input
                    value={walletBudget}
                    onChange={(e) => setWalletBudget(e.target.value)}
                    pattern="[0-9]+"
                    required
                  />
                </label>
                <label>
                  {uiText("Bot grant cap")}
                  <input
                    value={botBudget}
                    onChange={(e) => setBotBudget(e.target.value)}
                    pattern="[0-9]+"
                    required
                  />
                </label>
                <label>
                  {uiText("Maximum per operation")}
                  <input
                    value={maxOperation}
                    onChange={(e) => setMaxOperation(e.target.value)}
                    pattern="[0-9]+"
                    required
                  />
                </label>
                <label>
                  {uiText("Expiry in minutes")}
                  <input
                    value={minutes}
                    onChange={(e) => setMinutes(e.target.value)}
                    type="number"
                    min="1"
                    max="1440"
                    required
                  />
                </label>
                <label>
                  {uiText("Fixture bot identity")}
                  <input
                    value={botID}
                    onChange={(e) => setBotID(e.target.value)}
                    pattern="[0-9a-f]{32}"
                    required
                  />
                </label>
                <button
                  className="secondary"
                  disabled={
                    busy ||
                    !selected ||
                    !!view.fixture.status?.recoveryRequired ||
                    view.record.proposals.length >= 8
                  }
                >
                  {uiText("Create permission proposal")}
                </button>
              </form>
              {view.record.proposals.map((d) => {
                const p = d.proposal,
                  proof = view.proposals.find((x) => x.id === p.id);
                return (
                  <article
                    className="account-entry"
                    key={p.id}
                    aria-label={`Proposal ${p.id}`}
                  >
                    <div className="panel-heading">
                      <h3>{uiText("Disposable mandate")}</h3>
                      <Badge>
                        {proof?.receipt.state ?? uiText("unverified")}
                      </Badge>
                    </div>
                    <p>
                      {uiText("Wallet cap ")}
                      {p.policy.budget}
                      {uiText(" · Bot cap ")}
                      {p.grant.budget}
                      {uiText(" · Per operation ")}
                      {p.grant.maxOperation}
                      {uiText(" fixture units")}
                    </p>
                    <p>
                      {uiText("Expires")}{" "}
                      {new Date(p.grant.expiresAt * 1000).toLocaleString()}
                      {uiText(" · Destination fixture:treasury")}
                    </p>
                    <p className="account-key">
                      {uiText("Bot ")}
                      {p.grant.botId}
                      {uiText(" · Digest ")}
                      {proof?.digest}
                    </p>
                    <button className="secondary" onClick={() => download(p)}>
                      {uiText("Download public proposal")}
                    </button>
                    <p className="account-note">
                      {uiText(
                        "Save this public file in your owner account. Run the command on the instance host, compare the digest and all limits in the protected terminal, then explicitly approve. Passwords never belong here.",
                      )}
                    </p>
                    <code className="account-key">
                      fased-app custody approve --disposable-fixture
                      --proposal-file /absolute/path/to/fased-fixture-proposal-
                      {p.id}.json
                    </code>
                    <p className="account-note">
                      {uiText(
                        "An approval receipt records owner consent for the fixture. It does not enable real wallets, automatic unlock or live trading.",
                      )}
                    </p>
                  </article>
                );
              })}
            </section>
          </details>
        </>
      )}
    </div>
  );
}
