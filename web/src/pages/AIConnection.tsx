import { useEffect, useState } from "react";
import { useCore } from "../core";

type Connection = {
  ref: string;
  model: string;
  locked: boolean;
  unlockExpires: number;
  testOnly: true;
  liveCallsEnabled: false;
};
function useConnection() {
  const core = useCore();
  const [view, setView] = useState<Connection | null>(null);
  const [error, setError] = useState("");
  const refresh = () => {
    void core
      .request<Connection>("ai/connection")
      .then(setView)
      .catch(() => {
        setView(null);
        setError("Connection unavailable. Preserve the vault before recovery.");
      });
  };
  useEffect(() => {
    refresh();
    window.addEventListener("ai-connection-changed", refresh);
    return () => window.removeEventListener("ai-connection-changed", refresh);
  }, []);
  return { view, setView, error, setError, refresh, core };
}
export function ConnectionPicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (ref: string) => void;
}) {
  const { view, refresh } = useConnection();
  return (
    <label>
      AI connection
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={refresh}
      >
        <option value="">In-process fixture (no credential)</option>
        {view?.ref && (
          <option value={view.ref} disabled={view.locked}>
            OpenAI test connection · {view.locked ? "locked" : view.model}
          </option>
        )}
        {value && value !== view?.ref && (
          <option value={value} disabled>
            Previous connection unavailable
          </option>
        )}
      </select>
    </label>
  );
}
export function AIConnectionSettings() {
  const { view, setView, error, setError, core } = useConnection();
  const [key, setKey] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  async function change(action: string) {
    setBusy(true);
    setError("");
    const secret = passphrase,
      credential = key;
    setKey("");
    setPassphrase("");
    try {
      const next = await core.request<Connection>("ai/connection", "POST", {
        action,
        ref: view?.ref ?? "",
        ...(action !== "lock" ? { passphrase: secret } : {}),
        ...(action === "save" ? { key: credential } : {}),
      });
      setView(next);
      window.dispatchEvent(new Event("ai-connection-changed"));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Credential operation failed");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="panel" aria-label="OpenAI connection">
      <div className="panel-heading">
        <h2>OpenAI connection</h2>
        <span>TEST KEYS ONLY</span>
      </div>
      <div className="agent-record-body">
        <p>
          Live AI calls are disabled. Use a disposable fased-test- key to test
          the connection. No OpenAI account is contacted and fixture prices are
          examples.
        </p>
        <p>
          The core encrypts the key with a separate vault passphrase. Unlock
          lasts 15 minutes and ends on lock, logout or restart. Keep this
          passphrase safe: password recovery cannot recover it. A compromised
          running core can access an unlocked key.
        </p>
        <p role="status">
          {view?.ref
            ? `${view.locked ? "Locked" : "Unlocked"} · ${view.model} · ${view.ref}`
            : "No connection saved"}
        </p>
        {error && <p role="alert">{error}</p>}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void change("save");
          }}
          autoComplete="off"
        >
          <label>
            Disposable API key
            <input
              type="password"
              autoComplete="off"
              value={key}
              maxLength={256}
              onChange={(e) => setKey(e.target.value)}
            />
          </label>
          <label>
            Vault passphrase
            <input
              type="password"
              autoComplete="off"
              value={passphrase}
              maxLength={1024}
              onChange={(e) => setPassphrase(e.target.value)}
            />
          </label>
          <div className="agent-actions">
            <button
              className="primary"
              disabled={busy || !view || !key || passphrase.length < 12}
            >
              {view?.ref ? "Replace test connection" : "Save test connection"}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy || !view?.ref || passphrase.length < 12}
              onClick={() => void change("unlock")}
            >
              Unlock connection
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy || !view?.ref}
              onClick={() => void change("lock")}
            >
              Lock connection
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy || !view?.ref || passphrase.length < 12}
              onClick={() => void change("remove")}
            >
              Remove connection
            </button>
          </div>
        </form>
        <p>
          Credential files are excluded from metadata backups. Lost
          vault/passphrase: revoke the test key and reconnect; existing request
          records and budget holds remain.
        </p>
      </div>
    </section>
  );
}
