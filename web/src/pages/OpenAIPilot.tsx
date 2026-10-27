import { useEffect, useRef, useState } from "react";
import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";
import type { Connection } from "./AIConnection";
import type { AILimits } from "../agents";

export type PilotAllowance = {
  id: string;
  instanceId: string;
  bootId: string;
  connectionRef: string;
  connectionEpoch: number;
  accessEpoch: number;
  profileId: string;
  model: string;
  policyId: string;
  revision: number;
  createdAt: number;
  expiresAt: number;
  maxRequests: number;
  budgetMicrousd: number;
  laneBudgetMicrousd: number;
  limits: AILimits;
  usedRequests: number;
  allocatedMicrousd: number;
};

// The same strict command goes through the existing authenticated UI/CLI API.
// Read/revoke never forward a password or an edited allowance.
export function pilotCommand(
  action: "pilot-review" | "pilot-allow" | "pilot-revoke",
  ref: string,
  profileId: string,
  revision: number,
  review?: PilotAllowance,
  passphrase?: string,
) {
  const base = { action, ref };
  if (action === "pilot-review") return { ...base, profileId, revision };
  if (action === "pilot-revoke") return base;
  if (!review || review.connectionRef !== ref || review.profileId !== profileId)
    throw new Error("Review required");
  return { ...base, pilot: review, consentDigest: review.id, passphrase };
}

export function OpenAIPilotSettings({
  connection,
}: {
  connection: Connection;
}) {
  const { core, agents } = useWorkspace();
  const { t } = useUI();
  const [review, setReview] = useState<PilotAllowance>();
  const [passphrase, setPassphrase] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  const v = agents.view;
  useEffect(() => {
    generation.current++;
    setReview(undefined);
    setPassphrase("");
    setError("");
    setBusy(false);
    return () => {
      generation.current++;
    };
  }, [connection.ref, core.instanceId, v?.profileId, v?.revision]);
  if (!connection.pilotAvailable) return null;
  const allowance = review ?? connection.pilot;
  async function change(
    action: "pilot-review" | "pilot-allow" | "pilot-revoke",
  ) {
    if (!v) return;
    const epoch = generation.current;
    const secret = passphrase;
    setPassphrase("");
    setBusy(true);
    setError("");
    try {
      const next = await core.request<Connection>(
        "ai/connection",
        "POST",
        pilotCommand(
          action,
          connection.ref,
          v.profileId,
          v.revision,
          review,
          secret,
        ),
      );
      if (epoch !== generation.current) return;
      setReview(next.pilotReview);
      window.dispatchEvent(new Event("ai-connection-changed"));
      await agents.refresh();
    } catch (e) {
      if (epoch === generation.current)
        setError(e instanceof Error ? e.message : t("Unavailable"));
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  return (
    <details className="connection-options">
      <summary>{t("Pilot")}</summary>
      {error && <p role="alert">{error}</p>}
      {allowance && (
        <dl className="pilot-facts">
          <dt>{t("Model")}</dt>
          <dd>{allowance.model}</dd>
          <dt>{t("Limits")}</dt>
          <dd>
            {t("{count} attempts · {minutes} minutes", {
              count: allowance.maxRequests,
              minutes: (allowance.expiresAt - allowance.createdAt) / 60,
            })}{" "}
            · ${(allowance.budgetMicrousd / 1000000).toFixed(2)}
          </dd>
          <dt>{t("Used")}</dt>
          <dd>
            {allowance.usedRequests} · $
            {(allowance.allocatedMicrousd / 1000000).toFixed(4)}
          </dd>
          <dt>{t("Chat / task")}</dt>
          <dd>
            3 / 1 · ${(allowance.laneBudgetMicrousd / 1000000).toFixed(2)} / $
            {(allowance.laneBudgetMicrousd / 1000000).toFixed(2)}
          </dd>
          <dt>{t("Per request")}</dt>
          <dd>
            {allowance.limits.maxInputTokens} /{" "}
            {allowance.limits.maxOutputTokens} ·{" "}
            {allowance.limits.deadlineSeconds}s · $
            {(allowance.limits.budgetMicrousd / 1000000).toFixed(2)}
          </dd>
          <dt>{t("Expires")}</dt>
          <dd>{new Date(allowance.expiresAt * 1000).toLocaleTimeString()}</dd>
        </dl>
      )}
      {connection.pilot && (
        <p role="status">
          {t(connection.pilotState === "active" ? "Active" : "Stopped")}
        </p>
      )}
      {review ? (
        <form
          autoComplete="off"
          onSubmit={(e) => {
            e.preventDefault();
            void change("pilot-allow");
          }}
        >
          <label>
            {t("Vault passphrase")}
            <input
              type="password"
              autoComplete="off"
              value={passphrase}
              onChange={(e) => setPassphrase(e.target.value)}
              maxLength={1024}
            />
          </label>
          <div className="pilot-actions">
            <button
              className="primary"
              disabled={busy || passphrase.length < 12}
            >
              {t("Allow pilot")}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => {
                setReview(undefined);
                setPassphrase("");
              }}
            >
              {t("Cancel")}
            </button>
          </div>
        </form>
      ) : !connection.pilot ? (
        <button
          type="button"
          className="secondary"
          disabled={busy || !v || connection.locked}
          onClick={() => void change("pilot-review")}
        >
          {t("Review")}
        </button>
      ) : null}
      {connection.pilot && (
        <button
          type="button"
          className="secondary"
          disabled={busy || connection.pilotState !== "active"}
          onClick={() => void change("pilot-revoke")}
        >
          {t("Revoke")}
        </button>
      )}
    </details>
  );
}
