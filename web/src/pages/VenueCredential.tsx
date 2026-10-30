import { useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";

type Status = {
  rpcReady: boolean;
  ref: string;
  saved: boolean;
  stale: boolean;
  locked: boolean;
  unlockUntil?: number;
  liveExecution: boolean;
};
export function VenueCredential({
  accountId,
  revision,
}: {
  accountId: string;
  revision: number;
}) {
  const core = useCore(),
    { t } = useUI();
  const [status, setStatus] = useState<Status>();
  const [key, setKey] = useState(""),
    [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const generation = useRef(0);
  useEffect(() => {
    const epoch = ++generation.current;
    setStatus(undefined);
    setKey("");
    setPassword("");
    setError("");
    setBusy(false);
    core
      .request<Status>("accounts/venue-credential", "PUT", {
        accountId,
        revision,
        action: "status",
        ref: "",
      })
      .then((v) => {
        if (epoch === generation.current) setStatus(v);
      })
      .catch(() => {
        if (epoch === generation.current) setError(t("Save an RPC key first"));
      });
    return () => {
      generation.current++;
    };
  }, [core.instanceId, accountId, revision]);
  useEffect(() => {
    if (!status?.unlockUntil || status.locked) return;
    const timer = setTimeout(
      () =>
        setStatus((v) =>
          v ? { ...v, locked: true, unlockUntil: undefined } : v,
        ),
      Math.max(0, status.unlockUntil * 1000 - Date.now()),
    );
    return () => clearTimeout(timer);
  }, [status?.unlockUntil, status?.locked]);
  async function change(action: string) {
    if (!status || busy) return;
    const epoch = ++generation.current;
    setBusy(true);
    setError("");
    try {
      const v = await core.request<Status>("accounts/venue-credential", "PUT", {
        accountId,
        revision,
        ref: status.ref,
        action,
        ...(action === "lock" ? {} : { passphrase: password }),
        ...(action === "save" ? { key } : {}),
        ...(action === "unlock" ? { seconds: 60 } : {}),
      });
      if (v.liveExecution) throw new Error();
      if (epoch === generation.current) setStatus(v);
    } catch {
      if (epoch === generation.current) {
        setError(t("Credential operation failed"));
        setStatus((v) =>
          v ? { ...v, locked: true, unlockUntil: undefined } : v,
        );
      }
    } finally {
      if (epoch === generation.current) {
        setKey("");
        setPassword("");
        setBusy(false);
      }
    }
  }
  async function refresh() {
    const epoch = ++generation.current;
    setBusy(false);
    setKey("");
    setPassword("");
    try {
      const v = await core.request<Status>("accounts/venue-credential", "PUT", {
        accountId,
        revision,
        action: "status",
        ref: "",
      });
      if (epoch === generation.current) {
        setStatus(v);
        setError("");
      }
    } catch {
      if (epoch === generation.current) setError(t("Save an RPC key first"));
    }
  }
  return (
    <details
      name={`wallet-connection-${accountId}`}
      onToggle={(e) => {
        if (e.currentTarget.open) void refresh();
      }}
    >
      <summary>Jupiter</summary>
      <form
        className="account-form"
        onSubmit={(e) => {
          e.preventDefault();
          void change("save");
        }}
      >
        <label>
          {t("API key")}
          <input
            aria-label={t("Jupiter API key")}
            type="password"
            autoComplete="off"
            value={key}
            maxLength={512}
            disabled={busy}
            onChange={(e) => setKey(e.target.value)}
          />
        </label>
        <label>
          {t("Vault password")}
          <input
            aria-label={t("Connection password")}
            type="password"
            autoComplete="off"
            value={password}
            maxLength={256}
            disabled={busy}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <div className="account-actions">
          <button disabled={busy || !status?.rpcReady || !key || !password}>
            {t("Save key")}
          </button>
          {status?.saved && (
            <>
              <button
                type="button"
                disabled={busy || !password || status.stale}
                onClick={() => void change("unlock")}
              >
                {t("Unlock keys")}
              </button>
              <button
                type="button"
                disabled={busy || status.locked}
                onClick={() => void change("lock")}
              >
                {t("Lock")}
              </button>
              <button
                type="button"
                disabled={busy || !password}
                onClick={() => void change("remove")}
              >
                {t("Remove key")}
              </button>
            </>
          )}
        </div>
        {status?.saved && (
          <span role="status">
            {t(
              status.stale
                ? "Reconnect"
                : status.locked
                  ? "Locked"
                  : "Unlocked",
            )}
          </span>
        )}
        {error && <p role="alert">{error}</p>}
      </form>
    </details>
  );
}
