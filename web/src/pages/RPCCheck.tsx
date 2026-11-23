import { Copy, TerminalSquare } from "../icons";
import { VenueCredential } from "./VenueCredential";
import { RPCCredential } from "./RPCCredential";
import { useState, useRef, useEffect } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { Dialog, Disclosure } from "../components/shared";
import {
  fixtureNetwork,
  rpcEndpoint,
  rpcPresets,
  rpcRequiresKey,
  type Account,
  type AccountsView,
} from "../accounts";
export function RPCCheck({
  account,
  revision,
  onClose,
  onSaved,
}: {
  account: Account;
  revision: number;
  onClose: () => void;
  onSaved: (view: AccountsView) => void;
}) {
  const { t } = useUI();
  const core = useCore();
  const presets = rpcPresets(account.network);
  const [endpoint, setEndpoint] = useState(() => rpcEndpoint(account));
  const [result, setResult] = useState<"" | "live" | "test">("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [activeSection, setActiveSection] = useState<
    "rpc" | "jupiter" | "receipt" | null
  >(null);
  const generation = useRef(0);
  useEffect(() => {
    setActiveSection(null);
  }, [core.instanceId, account.id]);
  useEffect(() => {
    generation.current++;
    setBusy(false);
    setResult("");
    setError("");
    return () => {
      generation.current++;
    };
  }, [core.instanceId, revision, account.id]);
  async function save(remove = false) {
    if (busy) return;
    const epoch = ++generation.current;
    setBusy(true);
    setError("");
    setResult("");
    try {
      const view = await core.request<AccountsView>("accounts/rpc", "PUT", {
        accountId: account.id,
        revision,
        ...(remove ? { remove: true } : { endpoint, network: account.network }),
      });
      if (epoch === generation.current) onSaved(view);
    } catch (e) {
      if (epoch === generation.current)
        setError(e instanceof Error ? e.message : "RPC settings unavailable");
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  return (
    <Dialog title={`RPC · ${account.name}`} onClose={onClose}>
      <div className="wallet-rpc">
        <form
          className="account-form"
          onSubmit={async (e) => {
            e.preventDefault();
            if (busy) return;
            const epoch = ++generation.current;
            setBusy(true);
            setResult("");
            setError("");
            try {
              const v = await core.request<{
                fixtureOnly: boolean;
                liveRPC: boolean;
                account?: Account;
                accountRevision?: number;
              }>("accounts/rpc-check", "POST", {
                endpoint,
                network: account.network,
                accountId: account.id,
                accountRevision: revision,
              });
              if (epoch === generation.current) {
                if (
                  typeof v.fixtureOnly !== "boolean" ||
                  typeof v.liveRPC !== "boolean" ||
                  v.fixtureOnly === v.liveRPC ||
                  v.account?.id !== account.id ||
                  v.account.network !== account.network ||
                  v.accountRevision !== revision
                )
                  throw new Error("Unexpected RPC capability");
                setResult(v.liveRPC ? "live" : "test");
              }
            } catch (e) {
              if (epoch === generation.current)
                setError(e instanceof Error ? e.message : "RPC check failed");
            } finally {
              if (epoch === generation.current) setBusy(false);
            }
          }}
        >
          {presets.length > 0 && (
            <div className="account-actions" role="group" aria-label="RPC">
              {presets.map((preset) => (
                <button
                  key={preset.id}
                  type="button"
                  className="secondary"
                  aria-pressed={endpoint === preset.endpoint}
                  disabled={busy}
                  onClick={() => {
                    setEndpoint(preset.endpoint);
                    setResult("");
                    setError("");
                  }}
                >
                  {preset.label}
                </button>
              ))}
            </div>
          )}
          <label>
            RPC
            <input
              aria-label={t("RPC URL")}
              value={endpoint}
              maxLength={512}
              disabled={busy}
              onChange={(e) => {
                setEndpoint(e.target.value);
                setResult("");
                setError("");
              }}
            />
          </label>
          <label>
            {t("Network")}
            <input
              readOnly
              value={
                account.network === fixtureNetwork ? t("Demo") : account.network
              }
            />
          </label>
          <div className="account-actions">
            <button className="primary" disabled={busy || !endpoint.trim()}>
              {t("Check")}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy || !endpoint.trim()}
              onClick={() => void save()}
            >
              {t("Save")}
            </button>
            {account.rpc && (
              <button
                type="button"
                className="text-button"
                disabled={busy}
                onClick={() => void save(true)}
              >
                {t("Remove")}
              </button>
            )}
          </div>
          {result && (
            <p role="status">
              {t(result === "live" ? "Verified" : "Verified (test)")}
            </p>
          )}
          {error && <p role="alert">{error}</p>}
        </form>
        <RPCCredential
          accountId={account.id}
          revision={revision}
          configured={!!account.rpc}
          keyless={
            !rpcRequiresKey(
              endpoint || account.rpc?.endpoint || "",
              account.network,
            )
          }
          open={activeSection === "rpc"}
          onOpenChange={(next) => setActiveSection(next ? "rpc" : null)}
        />
        {account.version === 3 && (
          <VenueCredential
            accountId={account.id}
            revision={revision}
            open={activeSection === "jupiter"}
            onOpenChange={(next) => setActiveSection(next ? "jupiter" : null)}
          />
        )}
        {account.version === 3 &&
          [
            "https://mainnet.helius-rpc.com",
            "https://devnet.helius-rpc.com",
            "https://solana-mainnet.g.alchemy.com",
            "https://solana-devnet.g.alchemy.com",
          ].includes(account.rpc?.endpoint ?? "") && (
            <Disclosure
              label={t("Receipt reader")}
              icon={<TerminalSquare size={14} />}
              open={activeSection === "receipt"}
              onOpenChange={(next) => setActiveSection(next ? "receipt" : null)}
              className="wallet-cred-disclosure"
              dataSection="receipt-reader"
            >
              <div className="account-actions">
                <code>
                  fased-app custody receipt-rpc-save --rpc-origin{" "}
                  {account.rpc!.endpoint} --disposable-fixture
                </code>
                <button
                  type="button"
                  className="icon-btn"
                  aria-label={t("Copy command")}
                  onClick={() =>
                    void navigator.clipboard.writeText(
                      `fased-app custody receipt-rpc-save --rpc-origin ${account.rpc!.endpoint} --disposable-fixture`,
                    )
                  }
                >
                  <Copy size={14} />
                </button>
              </div>
            </Disclosure>
          )}
      </div>
    </Dialog>
  );
}
