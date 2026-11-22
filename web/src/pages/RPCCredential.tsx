import { useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
type Status = {
  canVerify?: boolean;
  canCheck?: boolean;
  backup?: {
    version?: number;
    portable?: boolean;
    accountId?: string;
    instanceId?: string;
  };
  backupVerified?: boolean;
  ref: string;
  saved: boolean;
  stale: boolean;
  locked: boolean;
  osVaultAvailable?: boolean;
  osProtected?: boolean;
  testOnly: boolean;
  liveRPC: boolean;
  check?: { fixtureOnly: boolean; liveRPC: boolean };
};
export function RPCCredential({
  accountId,
  revision,
  configured,
  keyless = false,
}: {
  accountId: string;
  revision: number;
  configured: boolean;
  keyless?: boolean;
}) {
  const core = useCore(),
    { t } = useUI();
  const [status, setStatus] = useState<Status>();
  const [key, setKey] = useState(""),
    [password, setPassword] = useState(""),
    [backupPassword, setBackupPassword] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [verified, setVerified] = useState(false);
  const [backup, setBackup] = useState<unknown>();
  const generation = useRef(0);
  const needsPassphrase = status?.saved
    ? !status.osProtected
    : !status?.osVaultAvailable;
  const backupSecret = needsPassphrase ? password : backupPassword;
  useEffect(() => {
    const epoch = ++generation.current;
    setStatus(undefined);
    setBackup(undefined);
    setKey("");
    setPassword("");
    setBackupPassword("");
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
  async function change(action: string, options?: { portable?: boolean }) {
    if (
      busy ||
      !status ||
      (action === "verify" && !status.canVerify) ||
      (action === "check" && !status.canCheck)
    )
      return;
    const epoch = ++generation.current;
    const isRecoveryAction = ["backup", "verify-backup", "restore"].includes(
      action,
    );
    const secret = isRecoveryAction
      ? backupSecret
      : needsPassphrase
        ? password
        : "";
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
        ...(action === "backup" && options?.portable ? { portable: true } : {}),
        ...(secret ? { passphrase: secret } : {}),
        ...(action === "save" ? { key } : {}),
        ...(["restore", "verify-backup"].includes(action) ? { backup } : {}),
      });
      if (epoch === generation.current) {
        if (
          action === "check" &&
          (!v.check ||
            typeof v.check.fixtureOnly !== "boolean" ||
            typeof v.check.liveRPC !== "boolean" ||
            v.check.fixtureOnly === v.check.liveRPC)
        )
          throw new Error("Unexpected RPC capability");
        if (action === "backup") {
          const isPortable = !!options?.portable;
          if (
            !v.backupVerified ||
            (isPortable
              ? v.backup?.version !== 3 || v.backup.portable !== true
              : v.backup?.accountId !== accountId ||
                v.backup.instanceId !== core.instanceId)
          )
            throw new Error("Unexpected RPC backup");
          const url = URL.createObjectURL(
            new Blob([JSON.stringify(v.backup)], { type: "application/json" }),
          );
          try {
            const link = document.createElement("a");
            link.href = url;
            link.download = isPortable
              ? "fased-rpc-portable-backup.json"
              : "fased-rpc-backup.json";
            link.click();
          } finally {
            setTimeout(() => URL.revokeObjectURL(url), 0);
          }
          setStatus({ ...v, backup: undefined, backupVerified: false });
        } else setStatus(v);
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
        setBackupPassword("");
        setBusy(false);
      }
    }
  }
  if (keyless && !status?.saved) return null;
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
        {needsPassphrase && (
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
        )}
        <div className="account-actions">
          <button
            disabled={
              busy ||
              !status ||
              !key ||
              (needsPassphrase && !password) ||
              !configured
            }
          >
            {t("Save key")}
          </button>
          {status?.saved && (
            <>
              {status.canVerify && (
                <button
                  type="button"
                  disabled={
                    busy || (needsPassphrase && !password) || status.stale
                  }
                  onClick={() => void change("verify")}
                >
                  {t("Verify key")}
                </button>
              )}
              {status.canCheck && (
                <button
                  type="button"
                  disabled={
                    busy ||
                    (needsPassphrase && !password) ||
                    status.stale ||
                    !configured
                  }
                  onClick={() => void change("check")}
                >
                  {t("Check connection")}
                </button>
              )}
              <button
                type="button"
                disabled={busy || (needsPassphrase && !password)}
                onClick={() => void change("remove")}
              >
                {t("Remove key")}
              </button>
            </>
          )}
        </div>
        <details>
          <summary>{t("Recovery")}</summary>
          {!needsPassphrase && (
            <label>
              {t("Backup password")}
              <input
                aria-label={t("Backup password")}
                type="password"
                autoComplete="off"
                maxLength={256}
                value={backupPassword}
                disabled={busy}
                onChange={(e) => setBackupPassword(e.target.value)}
              />
            </label>
          )}
          <div className="account-actions">
            <button
              type="button"
              disabled={busy || !status?.saved || status.stale || !backupSecret}
              onClick={() => void change("backup")}
            >
              {t("Download backup")}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy || !status?.saved || status.stale || !backupSecret}
              onClick={() => void change("backup", { portable: true })}
            >
              {t("Download portable backup")}
            </button>
          </div>
          <p className="account-note">
            {t("Requires this instance and wallet settings.")}
          </p>
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
                  if (file.size > 4096) throw new Error();
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
            disabled={busy || !backup || !backupSecret || !status}
            onClick={() => void change("verify-backup")}
          >
            {t("Verify backup")}
          </button>
          <button
            type="button"
            disabled={
              busy ||
              !backup ||
              !backupSecret ||
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
        {status?.check && (
          <span role="status">
            {t(status.check.liveRPC ? "Connected" : "Connected (demo)")}
          </span>
        )}
        {verified && <span role="status">{t("Key verified")}</span>}
        {error && <p role="alert">{error}</p>}
      </form>
    </details>
  );
}
