import { useEffect, useState, type FormEvent } from "react";
import { Badge } from "../components/shared";
import { useUI } from "../ui-preferences";
import {
  fixtureNetwork,
  publicID,
  type AccountsRecord,
  type AccountsView,
  type Proposal,
} from "../accounts";

export function FixtureAccountAdvanced({
  instanceId,
  view,
  busy,
  save,
  setError,
  setNotice,
}: {
  instanceId: string;
  view: AccountsView;
  busy: boolean;
  save: (record: AccountsRecord) => Promise<boolean>;
  setError: (error: string) => void;
  setNotice: (notice: string) => void;
}) {
  const { t: uiText } = useUI();
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [mode, setMode] = useState<"watch" | "external">("watch");
  const [selected, setSelected] = useState("");
  const [walletBudget, setWalletBudget] = useState("100");
  const [botBudget, setBotBudget] = useState("50");
  const [maxOperation, setMaxOperation] = useState("10");
  const [minutes, setMinutes] = useState("15");
  const [botID, setBotID] = useState(publicID);

  useEffect(() => {
    setSelected("");
  }, [instanceId]);

  async function addPublic(e: FormEvent) {
    e.preventDefault();
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
    const ref = view.fixture.status?.account;
    if (ref?.version !== 1) return;
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

  async function propose(e: FormEvent) {
    e.preventDefault();
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

  const ref = view.fixture.status?.account;
  const managed = view.record.accounts.filter(
    (a) =>
      a.mode === "fixture-managed" &&
      a.signer?.version === 1 &&
      view.accountStates[a.id] === "current-fixture",
  );

  return (
    <details className="account-advanced">
      <summary>{uiText("Advanced")}</summary>
      <section className="panel account-panel">
        <h2>{uiText("Add a public record")}</h2>
        <form className="account-form" onSubmit={(e) => void addPublic(e)}>
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
                <Badge>{proof?.receipt.state ?? uiText("unverified")}</Badge>
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
                fased-app custody approve --disposable-fixture --proposal-file
                /absolute/path/to/fased-fixture-proposal-{p.id}.json
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
  );
}
