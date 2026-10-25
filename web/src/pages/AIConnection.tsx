import { useUI } from "../ui-preferences";
import type { OpenAIModelPolicy, OpenAIAdmission } from "../agents";
import { useCallback, useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { TaskAccessSettings, type TaskAccessAllowance } from "./TaskAccess";
import { Badge } from "../components/shared";

export type Connection = {
  taskAccess?: TaskAccessAllowance;
  accessReview?: TaskAccessAllowance;
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
  const { t: uiText } = useUI();
  const { view, refresh } = useAIConnection();
  return (
    <label>
      <span>{uiText("Provider")}</span>
      <select
        value={value}
        aria-label={uiText("Provider")}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => void refresh()}
      >
        <option value="">{uiText("Demo")}</option>
        {view?.ref && (
          <option value={view.ref} disabled={view.locked}>
            {uiText("OpenAI · ")}
            {view.locked ? uiText("locked") : uiText("ready")}
          </option>
        )}
        {value && value !== view?.ref && (
          <option value={value} disabled>
            {uiText("Previous connection unavailable")}
          </option>
        )}
      </select>
    </label>
  );
}
export function AIConnectionSettings() {
  const { t: uiText } = useUI();
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
    <section className="panel ai-connection" aria-label={uiText("Providers")}>
      <div className="panel-heading">
        <h2>{uiText("Providers")}</h2>
        <Badge>{uiText("Demo")}</Badge>
      </div>
      <div className="agent-record-body">
        <p className="connection-status" role="status">
          {view?.ref
            ? `${uiText(view.locked ? "Locked" : "Unlocked")} · ${uiText("OpenAI")}`
            : uiText("No connection saved")}
        </p>
        {error && <p role="alert">{error}</p>}
        {view?.ref && (
          <TaskAccessSettings
            key={`${view.ref}:${core.instanceId}`}
            connection={view}
            onChanged={() =>
              window.dispatchEvent(new Event("ai-connection-changed"))
            }
          />
        )}
        <details className="connection-options">
          <summary>{uiText("Credentials")}</summary>
          <p>
            {uiText("Disposable fased-test- keys only. No real API calls.")}
          </p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void change("save");
            }}
            autoComplete="off"
          >
            <label>
              {uiText("Test key")}
              <input
                type="password"
                autoComplete="off"
                value={key}
                maxLength={256}
                onChange={(e) => setKey(e.target.value)}
              />
            </label>
            <label>
              {uiText("Vault passphrase")}
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
                {view?.ref ? uiText("Replace") : uiText("Save")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy || !view?.ref || passphrase.length < 12}
                onClick={() => void change("unlock")}
              >
                {uiText("Unlock")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy || !view?.ref}
                onClick={() => void change("lock")}
              >
                {uiText("Lock")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy || !view?.ref || passphrase.length < 12}
                onClick={() => void change("remove")}
              >
                {uiText("Remove")}
              </button>
            </div>
          </form>
          <details className="connection-inspection">
            <summary>{uiText("Recovery")}</summary>
            <p>
              {uiText(
                "Encrypted with your separate vault passphrase. Unlock lasts 15 minutes; lock, sign-out and restart end it. An unlocked key is accessible to the running core.",
              )}
            </p>
            <p>
              {uiText(
                "Metadata backups exclude credentials. Keep the vault and passphrase; owner password recovery cannot recover them. Reconnecting preserves request records and budget holds.",
              )}
            </p>
            <p>
              {uiText("Connection: ")}
              {view?.ref || uiText("None")}
            </p>
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
  const { t: uiText } = useUI();
  const { view } = useAIConnection();
  return (
    <label>
      <span>{uiText("Model")}</span>
      <select
        aria-label={uiText("Model")}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">{uiText("Demo")}</option>
        {value && value !== view?.admissionPolicy?.model && (
          <option value={value} disabled>
            {uiText("Unavailable model: ")}
            {value}
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
            {view.admissionPolicy.model}
            {uiText("")}
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
  const { t: uiText } = useUI();
  if (!policy) return null;
  const price = (value: number) => `$${(value / 1000000).toFixed(2)}`;
  return (
    <details className="ai-admission">
      <summary>{uiText("Model and tariff admission")}</summary>
      <p>
        {uiText("Pinned snapshot: ")}
        {policy.model}
        {uiText(" · Standard tier · Text only · No tools.")}
      </p>
      <p>
        {uiText("USD per million tokens: input ")}
        {price(policy.inputMicrousdPerMillion)}
        {uiText(", cached input ")}
        {price(policy.cachedMicrousdPerMillion)}
        {uiText(", output")} {price(policy.outputMicrousdPerMillion)}
        {uiText(
          ". Reserve the uncached ceiling with upward integer rounding. Accounting is not a provider invoice.",
        )}
      </p>
      <p>
        {uiText("Policy ")}
        {policy.id}
        {uiText(" · Reviewed")}{" "}
        {new Date(policy.reviewedAt * 1000).toLocaleDateString()}
        {uiText(" · Expires")}{" "}
        {new Date(policy.expiresAt * 1000).toLocaleString(
          typeof document === "undefined"
            ? "en"
            : document.documentElement.lang,
        )}
        .
      </p>
      <p>
        {uiText(
          "Owner consent covers model visibility and input-token preflight using the same scoped text, instructions and output schema, then one bounded response. Model visibility does not guarantee billing, quota or response permission. Each run checks again; no cache or fallback.",
        )}
      </p>
      <p>
        {uiText(
          "All three API operations are simulated in this build. Test credentials only; real calls stay disabled. The compiled fixture returns a simulated count of 256 tokens, not a local tokenizer.",
        )}
      </p>
      {admission && (
        <>
          <p>
            {uiText("Admission: ")}
            {admission.state}
            {uiText(" · Counted ")}
            {admission.inputTokens}
            {uiText(" input tokens · Response creation attempted:")}{" "}
            {admission.generationAttempted ? uiText("yes") : uiText("no")}.
          </p>
          <p>
            {uiText("Exact request SHA-256: ")}
            {admission.wireSha256}
          </p>
          <p>
            {uiText("Counted payload SHA-256: ")}
            {admission.countPayloadSha256}
          </p>
        </>
      )}
    </details>
  );
}
