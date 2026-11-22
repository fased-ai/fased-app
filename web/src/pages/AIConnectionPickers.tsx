import { previewModel } from "../ui-language-preview";
import { useUI } from "../ui-preferences";
import {
  isUnmeteredPolicy,
  type OpenAIAdmission,
  type OpenAIModelPolicy,
} from "../agents";
import { useAIConnection } from "../ai-connection";

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
        <option value="">{uiText("No AI")}</option>
        {(view?.connections ?? (view?.ref ? [view] : [])).map((account) => (
          <option
            key={account.ref}
            value={account.ref}
            disabled={account.locked || account.planPaused}
          >
            {account.name ||
              view?.catalog?.providers.find((p) => p.id === account.provider)
                ?.name ||
              account.provider ||
              uiText("Unavailable")}{" "}
            ·{" "}
            {uiText(
              account.authMethod === "oauth" || account.authMethod === "sign-in"
                ? "Sign in"
                : account.authMethod === "local"
                  ? "Server"
                  : "API key",
            )}
            {(account.planPaused || account.locked) &&
              ` · ${uiText(account.planPaused ? "Paused" : "Locked")}`}
          </option>
        ))}
        {value &&
          !(view?.connections ?? (view?.ref ? [view] : [])).some(
            (a) => a.ref === value,
          ) && (
            <option value={value} disabled>
              {uiText("Previous connection unavailable")}
            </option>
          )}
      </select>
    </label>
  );
}

export function AIModelPicker({
  value,
  onChange,
  connectionRef,
  allowDemo = true,
  disabled = false,
}: {
  value: string;
  onChange: (v: string) => void;
  connectionRef: string;
  allowDemo?: boolean;
  disabled?: boolean;
}) {
  const { t: uiText } = useUI();
  const { view } = useAIConnection();
  const selected = connectionRef
    ? (view?.connections?.find((a) => a.ref === connectionRef) ??
      (view?.ref === connectionRef ? view : null))
    : view;
  const models =
    selected?.catalog?.version === 1 ? selected.catalog.models : [];
  return (
    <label>
      <span>{uiText("Model")}</span>
      <select
        aria-label={uiText("Model")}
        disabled={disabled}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        {allowDemo &&
          (!connectionRef ||
            selected?.provider === "openai" ||
            !selected?.provider) && <option value="">{uiText("No AI")}</option>}
        {!allowDemo && !value && (
          <option value="" disabled>
            {uiText("Model")}
          </option>
        )}
        {value && !models.some((model) => model.id === value) && (
          <option value={value} disabled>
            {uiText("Unavailable model: ")}
            {value}
          </option>
        )}
        {[true, false].map((recommended) => {
          const group = models.filter((m) => m.recommended === recommended);
          if (!group.length) return null;
          return (
            <optgroup
              key={String(recommended)}
              label={uiText(recommended ? "Recommended" : "All models")}
            >
              {group.map((model) => (
                <option
                  key={model.id}
                  value={model.id}
                  disabled={
                    !connectionRef ||
                    !model.selectable ||
                    model.connectionRef !== connectionRef
                  }
                  title={
                    model.selectable
                      ? previewModel(model.id, uiText)
                      : uiText("Unavailable")
                  }
                >
                  {previewModel(model.name, uiText)}
                </option>
              ))}
            </optgroup>
          );
        })}
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
  if (isUnmeteredPolicy(policy))
    return (
      <details className="ai-admission">
        <summary>{uiText("Usage")}</summary>
        <p>
          {uiText(
            policy.model === "gemma4:cloud"
              ? "Ollama Cloud"
              : policy.provider === "local"
                ? "Local"
                : policy.provider === "ollama-cloud" ||
                    policy.model === "gemma4:cloud"
                  ? "Ollama Cloud"
                  : policy.provider === "kimi-code"
                    ? "Kimi Code"
                    : "ChatGPT",
          )}{" "}
          · {previewModel(policy.model, uiText)}
        </p>
        {admission && (
          <dl>
            <dt>{uiText("Input bound")}</dt>
            <dd>{admission.inputTokens}</dd>
            <dt>{uiText("Admission: ")}</dt>
            <dd>{admission.state}</dd>
          </dl>
        )}
      </details>
    );
  if (policy.wireVersion === "openai-signin-v1")
    return (
      <details className="ai-admission">
        <summary>{uiText("Model")}</summary>
        <p>{previewModel(policy.model, uiText)}</p>
        <p>{uiText("Access")}</p>
        {admission && (
          <dl>
            <dt>{uiText("Input bound")}</dt>
            <dd>{admission.inputTokens}</dd>
            <dt>{uiText("Admission: ")}</dt>
            <dd>{admission.state}</dd>
          </dl>
        )}
      </details>
    );
  const price = (value: number) => `$${(value / 1000000).toFixed(2)}`;
  return (
    <details className="ai-admission">
      <summary>{uiText("Model and tariff admission")}</summary>
      <p>
        {uiText("Pinned snapshot: ")}
        {previewModel(policy.model, uiText)}
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
      {!!policy.billableOutputTokens && (
        <p title={uiText("Includes reasoning tokens")}>
          {uiText("Output reserve")}:{" "}
          {policy.billableOutputTokens.toLocaleString()} {uiText("tokens")}
        </p>
      )}
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
      <p>{uiText("Live execution disabled")}</p>
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
