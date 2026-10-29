import { Dialog } from "../components/shared";
import type { ChatPermission, OpenAIModelPolicy } from "../agents";
import { isUnmeteredPolicy } from "../agents";
import { useUI } from "../ui-preferences";

export function ChatPermissionReview({
  permission: p,
  providerName,
  policy,
  busy,
  onAllow,
  onClose,
}: {
  permission: ChatPermission;
  providerName: string;
  policy?: OpenAIModelPolicy;
  busy: boolean;
  onAllow: () => void;
  onClose: () => void;
}) {
  const { t } = useUI();
  const planUsage =
    isUnmeteredPolicy(policy) &&
    p.budgetMicrousd === 0 &&
    p.limits.budgetMicrousd === 0;
  return (
    <Dialog title={t("Allow replies")} onClose={onClose}>
      <div className="setup-body">
        <p>
          {t(
            p.memoryMode === "none"
              ? "This conversation only. No notes, tools or scheduled work."
              : "This conversation and relevant current notes. No tools or scheduled work.",
          )}
        </p>
        <dl className="review-list">
          <div>
            <dt>{t("Model")}</dt>
            <dd>{p.model}</dd>
          </div>
          <div>
            <dt>{t("Provider")}</dt>
            <dd>{providerName}</dd>
          </div>
          {!!policy?.billableOutputTokens && (
            <div>
              <dt>{t("Output reserve")}</dt>
              <dd title={t("Includes reasoning tokens")}>
                {policy.billableOutputTokens.toLocaleString()} {t("tokens")}
              </dd>
            </div>
          )}
          <div>
            <dt>{t("Allowance")}</dt>
            <dd>
              {t("{count} replies · {minutes} minutes", {
                count: p.maxRequests,
                minutes: (p.expiresAt - p.createdAt) / 60,
              })}
            </dd>
          </div>
          {planUsage ? (
            <>
              <div>
                <dt>{t("Usage")}</dt>
                <dd>
                  {t(
                    policy?.model === "gemma4:cloud"
                      ? "Ollama Cloud"
                      : policy?.provider === "local"
                        ? "Local"
                        : policy?.provider === "ollama-cloud"
                          ? "Ollama Cloud"
                          : policy?.provider === "kimi-code"
                            ? "Kimi Code"
                            : "ChatGPT",
                  )}
                </dd>
              </div>
              <div>
                <dt>{t("App limits")}</dt>
                <dd>
                  {p.limits.maxInputTokens} / {p.limits.maxOutputTokens}{" "}
                  {t("tokens")} · {p.limits.deadlineSeconds}
                  {t("s")}
                </dd>
              </div>
            </>
          ) : (
            <div>
              <dt>{t("Maximum cost")}</dt>
              <dd>${(p.budgetMicrousd / 1_000_000).toFixed(2)}</dd>
            </div>
          )}
        </dl>
        <details>
          <summary>{t("Limits")}</summary>
          <pre>{JSON.stringify(p, null, 2)}</pre>
        </details>
      </div>
      <div className="dialog-footer">
        <button className="secondary" onClick={onClose} disabled={busy}>
          {t("Cancel")}
        </button>
        <button className="primary" onClick={onAllow} disabled={busy}>
          {t("Allow and send")}
        </button>
      </div>
    </Dialog>
  );
}
