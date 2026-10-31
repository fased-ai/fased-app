import { useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { Dialog } from "../components/shared";
import type { Account } from "../accounts";
import {
  currentWalletSnapshot,
  walletAmount,
  type WalletSnapshot,
} from "../wallet-read";

export function WalletRead({
  account,
  revision,
  address,
  blocked,
  settingsOpen,
}: {
  account: Account;
  revision: number;
  address: string;
  blocked: boolean;
  settingsOpen: boolean;
}) {
  const core = useCore(),
    { t } = useUI();
  const [snapshot, setSnapshot] = useState<WalletSnapshot>();
  const [ref, setRef] = useState(""),
    [password, setPassword] = useState("");
  const [open, setOpen] = useState(false),
    [busy, setBusy] = useState(false);
  const [state, setState] = useState("Unavailable");
  const generation = useRef(0),
    active = useRef<AbortController | undefined>(undefined);
  const reset = () => {
    generation.current++;
    active.current?.abort();
    setSnapshot(undefined);
    setPassword("");
    setRef("");
    setOpen(false);
    setBusy(false);
    setState("Unavailable");
  };
  useEffect(() => {
    reset();
    return () => {
      generation.current++;
      active.current?.abort();
    };
  }, [core.instanceId, account, revision, blocked, settingsOpen]);
  useEffect(() => {
    if (!snapshot) return;
    const timer = window.setTimeout(
      () => setState("Stale"),
      Math.max(0, snapshot.expiresAt * 1000 - Date.now()),
    );
    return () => clearTimeout(timer);
  }, [snapshot]);
  async function prepare() {
    if (busy || blocked || account.rpc?.version !== 2) return;
    const epoch = ++generation.current;
    active.current?.abort();
    const abort = new AbortController();
    active.current = abort;
    setSnapshot(undefined);
    setBusy(true);
    setState("Loading");
    try {
      const v = await core.request<{
        saved: boolean;
        stale: boolean;
        ref: string;
      }>(
        "accounts/rpc-credential",
        "PUT",
        { accountId: account.id, revision, action: "status", ref: "" },
        abort.signal,
      );
      if (epoch !== generation.current) return;
      if (!v.saved || v.stale || !v.ref) {
        setState(v.stale ? "Stale" : "Locked");
        return;
      }
      setRef(v.ref);
      setPassword("");
      setOpen(true);
      setState("Locked");
    } catch {
      if (epoch === generation.current) setState("Unavailable");
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  async function read() {
    if (busy || !password || !ref) return;
    const epoch = ++generation.current,
      secret = password;
    const abort = new AbortController();
    active.current = abort;
    setPassword("");
    setSnapshot(undefined);
    setBusy(true);
    setState("Loading");
    try {
      const v = await core.request<WalletSnapshot>(
        "accounts/read",
        "POST",
        { accountId: account.id, revision, ref, passphrase: secret },
        abort.signal,
      );
      if (epoch !== generation.current) return;
      if (!currentWalletSnapshot(v, account, revision, ref, address))
        throw new Error("stale snapshot");
      setSnapshot(v);
      setState("");
      setOpen(false);
    } catch {
      if (epoch === generation.current) setState("Unavailable");
    } finally {
      if (epoch === generation.current) {
        setBusy(false);
        setPassword("");
      }
    }
  }
  const current = state === "" && snapshot;
  return (
    <section className="wallet-read" aria-label={t("Balance")}>
      <div className="wallet-read-heading">
        <span>{t("Balance")}</span>
        <strong
          title={
            current ? walletAmount(current.lamports, 9) + " SOL" : undefined
          }
        >
          {current
            ? walletAmount(current.lamports, 9) + " SOL"
            : t(state || "Unavailable")}
        </strong>
        <button
          className="text-button"
          disabled={busy || blocked || account.rpc?.version !== 2}
          onClick={() => void prepare()}
        >
          {t("Refresh")}
        </button>
      </div>
      {current && (
        <>
          {current.tokens.length > 0 && (
            <table className="wallet-token-table">
              <thead>
                <tr>
                  <th>{t("Token")}</th>
                  <th>{t("Balance")}</th>
                </tr>
              </thead>
              <tbody>
                {current.tokens.map((v) => (
                  <tr key={v.program + v.mint}>
                    <td>
                      <code title={v.mint}>
                        {v.mint.slice(0, 6)}…{v.mint.slice(-4)}
                      </code>
                    </td>
                    <td title={walletAmount(v.amount, v.decimals)}>
                      {walletAmount(v.amount, v.decimals)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <details>
            <summary>{t("Activity")}</summary>
            {current.activity.length === 0 ? (
              <p>{t("No recent activity")}</p>
            ) : (
              <ul className="wallet-activity">
                {current.activity.map((v) => (
                  <li key={v.signature}>
                    <code title={v.signature}>
                      {v.signature.slice(0, 8)}…{v.signature.slice(-6)}
                    </code>
                    <span>{t(v.failed ? "Failed" : "Finalized")}</span>
                    <time>
                      {v.blockTime === null
                        ? "—"
                        : new Date(v.blockTime * 1000).toLocaleString()}
                    </time>
                  </li>
                ))}
              </ul>
            )}
          </details>
          <details>
            <summary>{t("Source")}</summary>
            <dl className="wallet-read-source">
              <dt>RPC</dt>
              <dd>{current.endpoint}</dd>
              <dt>{t("Slot")}</dt>
              <dd>{current.slot}</dd>
              <dt>{t("Updated")}</dt>
              <dd>{new Date(current.fetchedAt * 1000).toLocaleString()}</dd>
            </dl>
          </details>
        </>
      )}
      {open && (
        <Dialog title={t("Unlock RPC")} onClose={reset}>
          <form
            className="account-form"
            onSubmit={(e) => {
              e.preventDefault();
              void read();
            }}
          >
            <label>
              {t("Vault password")}
              <input
                type="password"
                autoComplete="off"
                maxLength={256}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={busy}
                autoFocus
              />
            </label>
            {state === "Unavailable" && (
              <span role="alert">{t("Unavailable")}</span>
            )}
            <div className="account-actions">
              <button disabled={busy || !password}>
                {t(busy ? "Loading" : "Refresh")}
              </button>
              <button type="button" className="text-button" onClick={reset}>
                {t("Cancel")}
              </button>
            </div>
          </form>
        </Dialog>
      )}
    </section>
  );
}
