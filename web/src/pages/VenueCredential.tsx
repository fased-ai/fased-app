import { useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { Disclosure } from "../components/shared";
import { Download, Zap } from "../icons";

type Status = {
  rpcReady: boolean;
  ref: string;
  saved: boolean;
  stale: boolean;
  locked: boolean;
  unlockUntil?: number;
  liveExecution: boolean;
  backup?: { version: number; portable: boolean; purpose: string };
  backupVerified?: boolean;
};
export function VenueCredential({
  accountId,
  revision,
  open,
  onOpenChange,
}: {
  accountId: string;
  revision: number;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const core = useCore(),
    { t } = useUI();
  const [status, setStatus] = useState<Status>();
  const [localOpen, setLocalOpen] = useState(false);
  const [recoveryOpen, setRecoveryOpen] = useState(false);
  const isOpen = open ?? localOpen;
  const setIsOpen = (next: boolean) => {
    if (onOpenChange) onOpenChange(next);
    else setLocalOpen(next);
  };
  const [backup, setBackup] = useState<unknown>();
  const [key, setKey] = useState(""),
    [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const generation = useRef(0);
  useEffect(() => {
    const epoch = ++generation.current;
    setStatus(undefined);
    setBackup(undefined);
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
    setStatus({ ...status, backupVerified: false });
    try {
      const v = await core.request<Status>("accounts/venue-credential", "PUT", {
        accountId,
        revision,
        ref: status.ref,
        action,
        ...(action === "lock" ? {} : { passphrase: password }),
        ...(action === "save" ? { key } : {}),
        ...(action === "unlock" ? { seconds: 60 } : {}),
        ...(["restore", "verify-backup"].includes(action) ? { backup } : {}),
      });
      if (v.liveExecution) throw new Error();
      if (epoch === generation.current) {
        if (action === "backup") {
          if (
            !v.backupVerified ||
            !v.backup?.portable ||
            v.backup.purpose !== "jupiter"
          )
            throw new Error("Unexpected Jupiter backup");
          const url = URL.createObjectURL(
            new Blob([JSON.stringify(v.backup)], { type: "application/json" }),
          );
          try {
            const link = document.createElement("a");
            link.href = url;
            link.download = "fased-jupiter-backup.json";
            link.click();
          } finally {
            setTimeout(() => URL.revokeObjectURL(url), 0);
          }
          setStatus({ ...v, backup: undefined, backupVerified: false });
        } else {
          setStatus(v);
          if (action === "restore") setBackup(undefined);
        }
      }
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
    <Disclosure
      label="Jupiter"
      icon={<Zap size={14} />}
      badge={
        status?.saved ? (
          <span
            className={`wt-disclosure-dot ${status.stale ? "warn" : "ok"}`}
            aria-hidden="true"
          />
        ) : undefined
      }
      open={isOpen}
      onOpenChange={(next) => {
        setIsOpen(next);
        if (next) void refresh();
      }}
      className="wallet-cred-disclosure"
      dataSection="jupiter-api-key"
    >
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
        <Disclosure
          label={t("Recovery")}
          icon={<Download size={14} />}
          open={recoveryOpen}
          onOpenChange={setRecoveryOpen}
          className="wallet-recovery-disclosure"
          dataSection="jupiter-recovery"
        >
          <div className="account-actions">
            <button
              type="button"
              className="secondary"
              disabled={busy || !status?.saved || !password}
              onClick={() => void change("backup")}
            >
              {t("Download portable backup")}
            </button>
          </div>
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
          <div className="account-actions">
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
                !status.rpcReady
              }
              onClick={() => void change("restore")}
            >
              {t("Restore")}
            </button>
          </div>
        </Disclosure>
        {status?.backupVerified && (
          <span role="status">{t("Backup verified")}</span>
        )}
        {error && <p role="alert">{error}</p>}
      </form>
    </Disclosure>
  );
}
