import type { OpenAIModelPolicy, OpenAIAdmission } from "../agents";
import { useEffect, useState } from "react";
import { useCore } from "../core";

type Connection = {
  admissionPolicy: OpenAIModelPolicy;
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

export function AIModelPicker({
  value,
  onChange,
  connectionRef,
}: {
  value: string;
  onChange: (v: string) => void;
  connectionRef: string;
}) {
  const { view } = useConnection();
  return (
    <label>
      AI model
      <select
        aria-label="AI model"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">Fixture model · invented tariff</option>
        {value && value !== view?.admissionPolicy?.model && (
          <option value={value} disabled>
            Unavailable model: {value}
          </option>
        )}
        {view?.admissionPolicy && (
          <option
            value={view.admissionPolicy.model}
            disabled={
              !connectionRef || view.locked || view.ref !== connectionRef
            }
          >
            {view.admissionPolicy.model} · admission fixture only
          </option>
        )}
      </select>
    </label>
  );
}
export function AIAdmissionReview({
  policy,
  admission,
}: {
  policy?: OpenAIModelPolicy;
  admission?: OpenAIAdmission;
}) {
  if (!policy) return null;
  const price = (value: number) => `$${(value / 1000000).toFixed(2)}`;
  return (
    <details className="ai-admission">
      <summary>Model and tariff admission</summary>
      <p>
        Pinned snapshot: {policy.model} · Standard tier · Text only · No tools.
      </p>
      <p>
        USD per million tokens: input {price(policy.inputMicrousdPerMillion)},
        cached input {price(policy.cachedMicrousdPerMillion)}, output{" "}
        {price(policy.outputMicrousdPerMillion)}. Reserve the uncached ceiling
        with upward integer rounding. Accounting is not a provider invoice.
      </p>
      <p>
        Policy {policy.id} · Reviewed{" "}
        {new Date(policy.reviewedAt * 1000).toLocaleDateString()} · Expires{" "}
        {new Date(policy.expiresAt * 1000).toLocaleString()}.
      </p>
      <p>
        Owner consent covers model visibility and input-token preflight using
        the same scoped text, instructions and output schema, then one bounded
        response. Model visibility does not guarantee billing, quota or response
        permission. Each run checks again; no cache or fallback.
      </p>
      <p>
        All three API operations are simulated in this build. Test credentials
        only; real calls stay disabled. The compiled fixture returns a simulated
        count of 256 tokens, not a local tokenizer.
      </p>
      {admission && (
        <>
          <p>
            Admission: {admission.state} · Counted {admission.inputTokens} input
            tokens · Response creation attempted:{" "}
            {admission.generationAttempted ? "yes" : "no"}.
          </p>
          <p>Exact request SHA-256: {admission.wireSha256}</p>
          <p>Counted payload SHA-256: {admission.countPayloadSha256}</p>
        </>
      )}
    </details>
  );
}
