import { useEffect, useState, type FormEvent } from "react";
import { useCore } from "../core";
import { Badge, SectionHead } from "../components/shared";
import {
  fixtureNetwork,
  publicID,
  type Account,
  type AccountsRecord,
  type AccountsView,
  type Proposal,
} from "../accounts";
import "../accounts.css";

export function AccountManagement() {
  const core = useCore();
  const [view, setView] = useState<AccountsView | null>(null);
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
    request<AccountsView>("accounts")
      .then((v) => {
        if (active) setView(v);
      })
      .catch((e) => {
        if (active) setError(String(e.message));
      });
    return () => {
      active = false;
    };
  }, [request]);
  async function refresh() {
    setBusy(true);
    setError("");
    try {
      setView(await request<AccountsView>("accounts"));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Accounts unavailable");
    } finally {
      setBusy(false);
    }
  }
  async function save(record: AccountsRecord) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      setView(await request<AccountsView>("accounts", "PUT", record));
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : "Account change failed");
      // Refresh public state for review. Never retry the rejected mutation.
      try {
        setView(await request<AccountsView>("accounts"));
      } catch {
        /* keep explicit failure */
      }
      return false;
    } finally {
      setBusy(false);
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
    if (!view || !ref) return;
    await save({
      ...view.record,
      accounts: [
        ...view.record.accounts,
        {
          version: 1,
          id: publicID(),
          name: "Disposable operating account",
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
    if (!view || !ref) return;
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
        view.accountStates[a.id] === "current-fixture",
    ) ?? [];
  return (
    <div className="wallet-workspace">
      <SectionHead
        title="Wallets"
        action={
          <button
            className="secondary"
            disabled={busy}
            onClick={() => void refresh()}
          >
            Refresh accounts and approvals
          </button>
        }
      />
      {error && (
        <div className="storage-warning" role="alert">
          {error}
        </div>
      )}
      {notice && (
        <div className="info-banner" role="status">
          {notice}
        </div>
      )}
      <div className="info-banner account-safety">
        <div>
          <strong>Disposable public references only</strong>
          <span>
            No balances, on-chain reads or signing. Keys and owner approvals
            stay outside this UI.
          </span>
        </div>
        <Badge>REAL WALLETS DISABLED</Badge>
      </div>
      {!view ? (
        <p role="status">Loading public accounts…</p>
      ) : (
        <>
          <div className="account-columns">
            <section className="panel account-panel">
              <div className="panel-heading">
                <h2>Public account records</h2>
                <Badge>REVISION {view.record.revision}</Badge>
              </div>
              {!view.record.accounts.length && <p>No accounts recorded.</p>}
              {view.record.accounts.map((a) => (
                <article
                  className="account-entry"
                  key={a.id}
                  aria-label={`Account ${a.name}`}
                >
                  <div className="panel-heading">
                    <h3>{a.name}</h3>
                    <Badge>
                      {a.mode === "external" ? "external/manual" : a.mode}
                    </Badge>
                  </div>
                  <p>
                    {(
                      {
                        "public-record-only": "Public reference only",
                        "current-fixture": "Current disposable reference",
                        stale:
                          "Stale reference — refresh before proposing permissions",
                        unverified: "Signer unavailable — reference unverified",
                      } as Record<string, string>
                    )[view.accountStates[a.id]] ?? "Reference unverified"}{" "}
                    · Balance unavailable
                  </p>
                  <code className="account-key">{a.publicKey}</code>
                  {a.signer && (
                    <p className="account-key">
                      Wallet {a.signer.walletId} · Epoch {a.signer.epoch}
                    </p>
                  )}
                  <div className="account-actions">
                    <button
                      className="text-button"
                      disabled={busy}
                      onClick={() => {
                        setEditing(a.id);
                        setNewName(a.name);
                      }}
                    >
                      Rename
                    </button>
                    <button
                      className="text-button"
                      disabled={busy}
                      onClick={() => void remove(a)}
                    >
                      Remove record
                    </button>
                    {a.mode === "fixture-managed" &&
                      view.accountStates[a.id] === "stale" &&
                      ref && (
                        <button
                          className="secondary"
                          disabled={busy}
                          onClick={() => void rebind(a)}
                        >
                          Refresh fixture reference
                        </button>
                      )}
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
                        aria-label="New account name"
                        value={newName}
                        onChange={(e) => setNewName(e.target.value)}
                        maxLength={80}
                        required
                      />
                      <button className="secondary" disabled={busy}>
                        Save account name
                      </button>
                    </form>
                  )}
                </article>
              ))}
              <p className="account-note">
                Removing a record does not delete a key or revoke a signer
                grant. Authority is managed separately by the owner.
              </p>
            </section>
            <section className="panel account-panel">
              <h2>Add a public record</h2>
              <form
                className="account-form"
                onSubmit={(e) => void addPublic(e)}
              >
                <label>
                  Name
                  <input
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    maxLength={80}
                    required
                  />
                </label>
                <label>
                  Capability mode
                  <select
                    aria-label="Capability mode"
                    value={mode}
                    onChange={(e) => setMode(e.target.value as typeof mode)}
                  >
                    <option value="watch">Watch-only record</option>
                    <option value="external">External/manual record</option>
                  </select>
                </label>
                <label>
                  Disposable public key (64 lowercase hex)
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
                  Save public account
                </button>
              </form>
              <div className="account-entry">
                <h3>Isolated disposable signer</h3>
                <p>
                  {ref
                    ? view.fixture.status?.recoveryRequired
                      ? "Recovery hold — new permissions blocked."
                      : view.fixture.status?.locked
                        ? "Locked. Owner approval takes place in the terminal."
                        : "Manually unlocked fixture. Real execution is disabled."
                    : view.fixture.available
                      ? "Signer reachable. Complete disposable custody setup in the owner terminal; this UI never creates keys."
                      : "Not configured. Ordinary installation never creates a signer or keys."}
                </p>
                <button
                  className="secondary"
                  onClick={() => void addSigner()}
                  disabled={
                    busy ||
                    !ref ||
                    view.record.accounts.length >= 8 ||
                    managed.length > 0
                  }
                >
                  Record disposable signer
                </button>
              </div>
            </section>
          </div>
          <section className="panel account-panel">
            <div className="panel-heading">
              <h2>Permission proposals</h2>
              <Badge>OWNER TERMINAL REQUIRED</Badge>
            </div>
            <p>
              A persona or app session cannot approve a mandate. Wallet lifetime
              caps include earlier spent and held units.
            </p>
            <form
              className="account-form proposal-fields"
              onSubmit={(e) => void propose(e)}
            >
              <label>
                Fixture account
                <select
                  aria-label="Fixture account"
                  value={selected}
                  onChange={(e) => setSelected(e.target.value)}
                  required
                >
                  <option value="">Select a current fixture reference</option>
                  {managed.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Wallet lifetime cap
                <input
                  value={walletBudget}
                  onChange={(e) => setWalletBudget(e.target.value)}
                  pattern="[0-9]+"
                  required
                />
              </label>
              <label>
                Bot grant cap
                <input
                  value={botBudget}
                  onChange={(e) => setBotBudget(e.target.value)}
                  pattern="[0-9]+"
                  required
                />
              </label>
              <label>
                Maximum per operation
                <input
                  value={maxOperation}
                  onChange={(e) => setMaxOperation(e.target.value)}
                  pattern="[0-9]+"
                  required
                />
              </label>
              <label>
                Expiry in minutes
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
                Fixture bot identity
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
                Create permission proposal
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
                    <h3>Disposable mandate</h3>
                    <Badge>{proof?.receipt.state ?? "unverified"}</Badge>
                  </div>
                  <p>
                    Wallet cap {p.policy.budget} · Bot cap {p.grant.budget} ·
                    Per operation {p.grant.maxOperation} fixture units
                  </p>
                  <p>
                    Expires{" "}
                    {new Date(p.grant.expiresAt * 1000).toLocaleString()} ·
                    Destination fixture:treasury
                  </p>
                  <p className="account-key">
                    Bot {p.grant.botId} · Digest {proof?.digest}
                  </p>
                  <button className="secondary" onClick={() => download(p)}>
                    Download public proposal
                  </button>
                  <p className="account-note">
                    Save this public file in your owner account. Run the command
                    on the instance host, compare the digest and all limits in
                    the protected terminal, then explicitly approve. Passwords
                    never belong here.
                  </p>
                  <code className="account-key">
                    fased-app custody approve --disposable-fixture
                    --proposal-file /absolute/path/to/fased-fixture-proposal-
                    {p.id}.json
                  </code>
                  <p className="account-note">
                    An approval receipt records owner consent for the fixture.
                    It does not enable real wallets, automatic unlock or live
                    trading.
                  </p>
                </article>
              );
            })}
          </section>
        </>
      )}
    </div>
  );
}
