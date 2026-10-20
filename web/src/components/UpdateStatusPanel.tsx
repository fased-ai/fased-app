import { useCallback, useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { readUpdateStatus, updateLabels, type UpdateStatus } from "../updates";
import { Badge } from "./shared";
import "../updates.css";

export function UpdateStatusPanel() {
  const { mode, request } = useCore();
  const [view, setView] = useState<UpdateStatus | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++generation.current;
    setView(null);
    setError("");
    setBusy(true);
    try {
      const result = readUpdateStatus(await request<unknown>("updates/status"));
      if (current === generation.current) setView(result);
    } catch {
      if (current === generation.current)
        setError(
          "Update status unavailable. Check the existing core connection and sign-in, then refresh. No previous observation is shown as current.",
        );
    } finally {
      if (current === generation.current) setBusy(false);
    }
  }, [request]);
  useEffect(() => {
    if (mode === "ready") void refresh();
    else setView(null);
    return () => {
      generation.current++;
    };
  }, [mode, refresh]);
  const flag = (v: boolean | null, yes: string, no: string) =>
    v === null ? "Unknown" : v ? yes : no;
  return (
    <section
      className="panel update-status"
      aria-labelledby="update-status-title"
    >
      <div className="panel-heading">
        <h2 id="update-status-title">Updates & recovery</h2>
        <Badge>READ ONLY</Badge>
      </div>
      <div className="settings-body">
        <p>
          Real wallets and automatic updates are disabled. Owner update actions
          require the protected terminal.
        </p>
        {mode !== "ready" ? (
          <p className="subtle">
            Connect and sign in to a private core to see actual status.
            Prototype preferences cannot report installation or signer state.
          </p>
        ) : (
          <>
            <button
              className="secondary"
              disabled={busy}
              onClick={() => void refresh()}
            >
              {busy ? "Reading update status…" : "Refresh update status"}
            </button>
            {error && <p role="alert">{error}</p>}
            {view && (
              <div aria-live="polite">
                <h3>{updateLabels[view.state]}</h3>
                <p className="subtle">
                  Last observed:{" "}
                  <time dateTime={view.observedAt}>
                    {new Date(view.observedAt).toLocaleString()}
                  </time>
                  . Refresh to check again.
                </p>
                <dl className="update-observations">
                  <dt>Core metadata fence</dt>
                  <dd>{view.core.phase}</dd>
                  {view.core.intentId && (
                    <>
                      <dt>Core intent reference</dt>
                      <dd>
                        <code>{view.core.intentId}</code>
                        <small>
                          App-owned metadata; verify in the owner terminal.
                        </small>
                      </dd>
                    </>
                  )}
                  <dt>Signer connection</dt>
                  <dd>{view.signer.state}</dd>
                  <dt>Wallet lock</dt>
                  <dd>
                    {flag(
                      view.signer.locked,
                      "Locked",
                      "Unlocked (disposable fixture)",
                    )}
                  </dd>
                  <dt>Signer update / storage hold</dt>
                  <dd>
                    {flag(view.signer.updateHeld, "Reported", "Not reported")}
                  </dd>
                  <dt>Signer recovery hold</dt>
                  <dd>
                    {flag(
                      view.signer.recoveryRequired,
                      "Reported",
                      "Not reported",
                    )}
                  </dd>
                </dl>
                <h3>Recovery guidance</h3>
                <ul className="update-guidance">
                  {view.guidance.map((g) => (
                    <li key={g.code}>
                      <p>{g.message}</p>
                      {g.command && (
                        <pre aria-label="Owner inventory command">
                          <code>{g.command}</code>
                        </pre>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}
