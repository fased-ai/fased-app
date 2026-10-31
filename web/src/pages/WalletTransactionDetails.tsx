import { useEffect, useRef, useState } from "react";
import { Dialog } from "../components/shared";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import type { Account } from "../accounts";
import {
  currentWalletSnapshot,
  currentWalletTransaction,
  walletAmount,
  walletSignedAmount,
  type WalletActivity,
  type WalletSnapshot,
  type WalletTransaction,
} from "../wallet-read";

export function WalletTransactionDetails({
  account,
  snapshot,
  activity,
  onClose,
}: {
  account: Account;
  snapshot: WalletSnapshot;
  activity: WalletActivity;
  onClose: () => void;
}) {
  const core = useCore(),
    { t } = useUI();
  const [password, setPassword] = useState(""),
    [busy, setBusy] = useState(false),
    [failed, setFailed] = useState(false),
    [detail, setDetail] = useState<WalletTransaction>();
  const active = useRef<AbortController | undefined>(undefined);
  useEffect(() => () => active.current?.abort(), []);
  async function inspect() {
    if (busy || !password || snapshot.expiresAt * 1000 <= Date.now()) return;
    const secret = password,
      abort = new AbortController();
    active.current = abort;
    setPassword("");
    setBusy(true);
    setFailed(false);
    setDetail(undefined);
    try {
      const v = await core.request<WalletSnapshot>(
        "accounts/read",
        "POST",
        {
          accountId: account.id,
          revision: snapshot.revision,
          ref: snapshot.credentialRef,
          passphrase: secret,
          selection: {
            ...activity,
            binding: snapshot.binding,
            fetchedAt: snapshot.fetchedAt,
            expiresAt: snapshot.expiresAt,
          },
        },
        abort.signal,
      );
      if (abort.signal.aborted) return;
      if (
        !currentWalletSnapshot(
          v,
          account,
          snapshot.revision,
          snapshot.credentialRef,
          snapshot.address,
        ) ||
        v.binding !== snapshot.binding ||
        !currentWalletTransaction(v.transaction, snapshot, activity)
      )
        throw new Error("unavailable");
      setDetail(v.transaction);
    } catch {
      if (!abort.signal.aborted) setFailed(true);
    } finally {
      if (!abort.signal.aborted) {
        setBusy(false);
        setPassword("");
      }
    }
  }
  return (
    <Dialog title={t("Transaction")} onClose={onClose}>
      <div className="wallet-transaction">
        <code className="wallet-signature" title={activity.signature}>
          {activity.signature.slice(0, 12)}…{activity.signature.slice(-8)}
        </code>
        {detail ? (
          <>
            <dl className="wallet-read-source">
              <dt>{t("Status")}</dt>
              <dd>{t(detail.failed ? "Failed" : "Finalized")}</dd>
              <dt>{t("Fee")}</dt>
              <dd>{walletAmount(detail.feeLamports, 9)} SOL</dd>
              <dt>{t("Payer")}</dt>
              <dd>
                <code title={detail.payer}>
                  {detail.payer.slice(0, 8)}…{detail.payer.slice(-6)}
                </code>
              </dd>
            </dl>
            <table className="wallet-token-table">
              <caption>
                {t("Changes")}
                {!detail.tokenChangesComplete && (
                  <span className="muted"> · {t("Partial")}</span>
                )}
              </caption>
              <thead>
                <tr>
                  <th>{t("Token")}</th>
                  <th>{t("Change")}</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>SOL</td>
                  <td>{walletSignedAmount(detail.nativeChange, 9)}</td>
                </tr>
                {detail.tokens.map((row) => (
                  <tr key={row.account}>
                    <td>
                      <code title={`${row.mint} · ${row.account}`}>
                        {row.mint.slice(0, 6)}…{row.mint.slice(-4)}
                      </code>
                    </td>
                    <td>{walletSignedAmount(row.change, row.decimals)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <button className="text-button" onClick={onClose}>
              {t("Close")}
            </button>
          </>
        ) : (
          <form
            className="account-form"
            onSubmit={(e) => {
              e.preventDefault();
              void inspect();
            }}
          >
            <label>
              {t("Vault password")}
              <input
                type="password"
                autoComplete="off"
                maxLength={256}
                autoFocus
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={busy}
              />
            </label>
            {failed && <span role="alert">{t("Unavailable")}</span>}
            <div className="account-actions">
              <button disabled={busy || !password}>
                {t(busy ? "Loading" : "View")}
              </button>
              <button type="button" className="text-button" onClick={onClose}>
                {t("Cancel")}
              </button>
            </div>
          </form>
        )}
      </div>
    </Dialog>
  );
}
