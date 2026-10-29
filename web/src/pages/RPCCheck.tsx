import { RPCCredential } from "./RPCCredential";
import { useState, useRef, useEffect } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { Dialog } from "../components/shared";
import {
  fixtureNetwork,
  rpcEndpoint,
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
  const [endpoint, setEndpoint] = useState(() => rpcEndpoint(account));
  const [result, setResult] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setBusy(false);
    setResult(false);
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
    setResult(false);
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
            setResult(false);
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
                  !v.fixtureOnly ||
                  v.liveRPC ||
                  v.account?.id !== account.id ||
                  v.account.network !== account.network ||
                  v.accountRevision !== revision
                )
                  throw new Error("Unexpected RPC capability");
                setResult(true);
              }
            } catch (e) {
              if (epoch === generation.current)
                setError(e instanceof Error ? e.message : "RPC check failed");
            } finally {
              if (epoch === generation.current) setBusy(false);
            }
          }}
        >
          <label>
            RPC
            <input
              aria-label={t("RPC URL")}
              value={endpoint}
              maxLength={512}
              disabled={busy}
              onChange={(e) => {
                setEndpoint(e.target.value);
                setResult(false);
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
          {result && <p role="status">{t("Verified (test)")}</p>}
          {error && <p role="alert">{error}</p>}
        </form>
        <RPCCredential
          accountId={account.id}
          revision={revision}
          configured={!!account.rpc}
        />
      </div>
    </Dialog>
  );
}
