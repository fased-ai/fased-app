import { useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
type Status = {
  backupVerified?: boolean;
  ref: string;
  saved: boolean;
  stale: boolean;
  locked: boolean;
  testOnly: boolean;
  liveRPC: boolean;
  check?: { fixtureOnly: boolean; liveRPC: boolean };
};
export function RPCCredential({
  accountId,
  revision,
  configured,
}: {
  accountId: string;
  revision: number;
  configured: boolean;
}) {
  const core = useCore(),
    { t } = useUI();
  const [status, setStatus] = useState<Status>();
  const [key, setKey] = useState(""),
    [password, setPassword] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [verified, setVerified] = useState(false);
  const [backup, setBackup] = useState<unknown>();
  const generation = useRef(0);
  useEffect(() => {
    const epoch = ++generation.current;
    setStatus(undefined);
    setBackup(undefined);
    setKey("");
    setPassword("");
    setError("");
    setVerified(false);
    core
      .request<Status>("accounts/rpc-credential", "PUT", {
        accountId,
        revision,
        action: "status",
        ref: "",
      })
      .then((v) => {
        if (epoch === generation.current) setStatus(v);
      })
      .catch(() => {
        if (epoch === generation.current) setError(t("Unavailable"));
      });
    return () => {
      generation.current++;
    };
  }, [core.instanceId, accountId, revision]);
  async function change(action: string) {
    if (busy || !status) return;
    const epoch = ++generation.current;
    setBusy(true);
    setError("");
    setVerified(false);
    setStatus({ ...status, check: undefined, backupVerified: false });
    try {
      const v = await core.request<Status>("accounts/rpc-credential", "PUT", {
        accountId,
        revision,
        ref: status.ref,
        action,
        passphrase: password,
        ...(action === "save" ? { key } : {}),
        ...(["restore", "verify-backup"].includes(action) ? { backup } : {}),
      });
      if (epoch === generation.current) {
        if (action === "check" && (!v.check?.fixtureOnly || v.check.liveRPC))
          throw new Error("Unexpected RPC capability");
        setStatus(v);
        setVerified(action === "verify");
        if (action === "restore") setBackup(undefined);
      }
    } catch {
      if (epoch === generation.current)
        setError(t("Credential operation failed"));
    } finally {
      if (epoch === generation.current) {
        setKey("");
        setPassword("");
        setBusy(false);
      }
    }
  }
  return (
    <details name={`wallet-connection-${accountId}`}>
      <summary>{t("API key")}</summary>
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
            aria-label={t("RPC API key")}
            type="password"
            autoComplete="off"
            maxLength={256}
            value={key}
            disabled={busy}
            onChange={(e) => setKey(e.target.value)}
          />
        </label>
        <label>
          {t("Vault password")}
          <input
            aria-label={t("Vault password")}
            type="password"
            autoComplete="off"
            maxLength={256}
            value={password}
            disabled={busy}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <div className="account-actions">
          <button
            disabled={busy || !status || !key || !password || !configured}
          >
            {t("Save key")}
          </button>
          {status?.saved && (
            <>
              <button
                type="button"
                disabled={busy || !password || status.stale}
                onClick={() => void change("verify")}
              >
                {t("Verify key")}
              </button>
              <button
                type="button"
                disabled={busy || !password || status.stale || !configured}
                onClick={() => void change("check")}
              >
                {t("Check connection")}
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
        <details>
          <summary>{t("Recovery")}</summary>
          <label>
            {t("Backup file")}
            <input
              type="file"
              accept="application/json,.json"
              aria-label={t("Backup file")}
              disabled={busy}
              onChange={async (e) => {
                const file = e.target.files?.[0];
                const epoch = generation.current;
                setBackup(undefined);
                setError("");
                if (!file) return;
                try {
                  if (file.size > 2048) throw new Error();
                  const parsed: unknown = JSON.parse(await file.text());
                  if (epoch === generation.current) setBackup(parsed);
                } catch {
                  if (epoch === generation.current)
                    setError(t("Invalid backup"));
                }
              }}
            />
          </label>
          <button
            type="button"
            disabled={busy || !backup || !password || !status}
            onClick={() => void change("verify-backup")}
          >
            {t("Verify backup")}
          </button>
          <button
            type="button"
            disabled={
              busy ||
              !backup ||
              !password ||
              !status ||
              status.saved ||
              !configured
            }
            onClick={() => void change("restore")}
          >
            {t("Restore")}
          </button>
        </details>
        {status?.backupVerified && (
          <span role="status">{t("Backup verified")}</span>
        )}
        {status?.saved && (
          <span>{t(status.stale ? "Reconnect" : "Saved")}</span>
        )}
        {status?.check && <span role="status">{t("Connected (demo)")}</span>}
        {verified && <span role="status">{t("Key verified")}</span>}
        {error && <p role="alert">{error}</p>}
      </form>
    </details>
  );
}
