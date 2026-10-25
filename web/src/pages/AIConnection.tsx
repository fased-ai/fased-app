import type { OpenAIModelPolicy, OpenAIAdmission } from "../agents";
import { useCallback, useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { Badge } from "../components/shared";

export type Connection = {
  admissionPolicy: OpenAIModelPolicy;
  ref: string;
  model: string;
  locked: boolean;
  unlockExpires: number;
  testOnly: true;
  liveCallsEnabled: false;
};
export function useAIConnection() {
  const core = useCore();
  const [view, setView] = useState<Connection | null>(null);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const refresh = useCallback(async () => {
    const epoch = ++generation.current;
    try {
      const next = await core.request<Connection>("ai/connection");
      if (epoch === generation.current) {
        setView(next);
        setError("");
      }
      return next;
    } catch {
      if (epoch === generation.current) {
        setView(null);
        setError("Connection unavailable. Try again; preserve the vault.");
      }
      return null;
    }
  }, [core.request]);
  useEffect(() => {
    setView(null);
    setError("");
    if (core.mode !== "ready") return;
    const load = () => {
      void refresh();
    };
    load();
    window.addEventListener("ai-connection-changed", load);
    window.addEventListener("focus", load);
    const timer = window.setInterval(load, 15000);
    return () => {
      generation.current++;
      clearInterval(timer);
      window.removeEventListener("ai-connection-changed", load);
      window.removeEventListener("focus", load);
    };
  }, [refresh, core.mode, core.instanceId]);
  return { view, setView, error, setError, refresh, core };
}
export function ConnectionPicker({
  value,
  onChange,
}: {
  value: string;
  onChange: (ref: string) => void;
}) {
  const { view, refresh } = useAIConnection();
  return (
    <label>
      <span>AI connection</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => void refresh()}
      >
        <option value="">Local fixture</option>
        {view?.ref && (
          <option value={view.ref} disabled={view.locked}>
            OpenAI fixture · {view.locked ? "locked" : "ready"}
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
  const { view, setView, error, setError, core } = useAIConnection();
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
    <section className="panel ai-connection" aria-label="OpenAI connection">
      <div className="panel-heading">
        <h2>OpenAI connection</h2>
        <Badge>FIXTURE</Badge>
      </div>
      <div className="agent-record-body">
        <p className="connection-status" role="status">
          {view?.ref
            ? `${view.locked ? "Locked" : "Unlocked"} · OpenAI fixture`
            : "No connection saved"}
        </p>
        {error && <p role="alert">{error}</p>}
        <details className="connection-options">
          <summary>Connection settings</summary>
          <p>Disposable fased-test- keys only. No real API calls.</p>
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
          <details className="connection-inspection">
            <summary>Vault and recovery</summary>
            <p>
              Encrypted with your separate vault passphrase. Unlock lasts 15
              minutes; lock, sign-out and restart end it. An unlocked key is
              accessible to the running core.
            </p>
            <p>
              Metadata backups exclude credentials. Keep the vault and
              passphrase; owner password recovery cannot recover them.
              Reconnecting preserves request records and budget holds.
            </p>
            <p>Connection: {view?.ref || "None"}</p>
          </details>
        </details>
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
  const { view } = useAIConnection();
  return (
    <label>
      <span>AI model</span>
      <select
        aria-label="AI model"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">Fixture model</option>
        {value && value !== view?.admissionPolicy?.model && (
          <option value={value} disabled>
            Unavailable model: {value}
          </option>
        )}
        {view?.admissionPolicy && (
          <option
            value={view.admissionPolicy.model}
            disabled={
              !connectionRef ||
              view.locked ||
              view.ref !== connectionRef ||
              Date.now() / 1000 >= view.admissionPolicy.expiresAt
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
