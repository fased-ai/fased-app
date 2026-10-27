import { useEffect, useRef, useState } from "react";
import type { Connection } from "./AIConnection";
import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";

export type TaskAccessAllowance = {
  id: string;
  connectionRef: string;
  profileId: string;
  model: string;
  tasks: { id: string; digest: string }[];
  createdAt: number;
  expiresAt: number;
  maxRequests: number;
  budgetMicrousd: number;
  usedRequests: number;
  allocatedMicrousd: number;
};
export function TaskAccessSettings({
  connection,
  onChanged,
}: {
  connection: Connection;
  onChanged: () => void;
}) {
  const { core, agents } = useWorkspace();
  const { t } = useUI();
  const [ids, setIDs] = useState<string[]>([]);
  const [minutes, setMinutes] = useState(60);
  const [review, setReview] = useState<TaskAccessAllowance>();
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const v = agents.view;
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setBusy(false);
    setError("");
    setIDs([]);
    setReview(undefined);
    setPassphrase("");
    return () => {
      generation.current++;
    };
  }, [v?.profileId, core.instanceId]);
  const tasks =
    v?.tasks.filter(
      (task) =>
        task.ai?.authorized &&
        task.ai.connectionRef === connection.ref &&
        ["scheduled", "paused"].includes(task.state),
    ) ?? [];
  async function change(action: string) {
    if (!v) return;
    const epoch = generation.current;
    setBusy(true);
    setError("");
    const secret = passphrase;
    setPassphrase("");
    try {
      const next = await core.request<Connection>("ai/connection", "POST", {
        action,
        ref: connection.ref,
        ...(action === "task-access-review"
          ? {
              revision: v.revision,
              profileId: v.profileId,
              taskIds: ids,
              authorizationSeconds: minutes * 60,
            }
          : {}),
        ...(action === "task-access-allow"
          ? { allowance: review, consentDigest: review?.id, passphrase: secret }
          : {}),
      });
      if (epoch === generation.current) setReview(next.accessReview);
      onChanged();
    } catch (e) {
      if (epoch !== generation.current) return;
      setReview(undefined);
      setError(
        e instanceof Error ? e.message : t("Credential operation failed"),
      );
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  return (
    <details className="connection-options">
      <summary>{t("Background access")}</summary>
      {error && <p role="alert">{t(error)}</p>}
      {connection.taskAccess && (
        <div className="agent-inline">
          <span>
            {
              v?.profiles.find((p) => p.id === connection.taskAccess?.profileId)
                ?.name
            }{" "}
            ·
            {t("{count} replies left", {
              count:
                connection.taskAccess.maxRequests -
                connection.taskAccess.usedRequests,
            })}{" "}
            ·{" "}
            {new Date(
              connection.taskAccess.expiresAt * 1000,
            ).toLocaleTimeString()}
          </span>
          <button
            className="secondary"
            disabled={busy}
            onClick={() => void change("task-access-revoke")}
          >
            {t("Revoke")}
          </button>
        </div>
      )}
      {!review ? (
        <>
          <label>
            {t("Agent")}
            <select
              value={v?.profileId ?? ""}
              onChange={(e) => void agents.select(e.target.value)}
            >
              {v?.profiles.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <fieldset>
            <legend>{t("Reviewed tasks")}</legend>
            {tasks.length ? (
              tasks.map((task) => (
                <label className="ai-consent" key={task.id}>
                  <input
                    type="checkbox"
                    checked={ids.includes(task.id)}
                    onChange={(e) => {
                      setIDs(
                        e.target.checked
                          ? [...ids, task.id]
                          : ids.filter((id) => id !== task.id),
                      );
                      setReview(undefined);
                    }}
                  />
                  {task.name}
                </label>
              ))
            ) : (
              <p>{t("No reviewed tasks use this connection")}</p>
            )}
          </fieldset>
          <label>
            {t("Credential access")}
            <select
              value={minutes}
              onChange={(e) => setMinutes(Number(e.target.value))}
            >
              <option value={15}>{t("15 minutes")}</option>
              <option value={60}>{t("1 hour")}</option>
              <option value={1440}>{t("24 hours")}</option>
            </select>
          </label>
          <button
            className="secondary"
            disabled={busy || ids.length === 0}
            onClick={() => void change("task-access-review")}
          >
            {t("Review access")}
          </button>
        </>
      ) : (
        <form
          autoComplete="off"
          onSubmit={(e) => {
            e.preventDefault();
            void change("task-access-allow");
          }}
        >
          <p>
            {review.model} ·{" "}
            {t("{count} replies · {minutes} minutes", {
              count: review.maxRequests,
              minutes: Math.floor((review.expiresAt - review.createdAt) / 60),
            })}{" "}
            · ${(review.budgetMicrousd / 1_000_000).toFixed(2)}
          </p>
          <p>
            {t(
              "Selected tasks only. Closing the browser keeps them running. Lock, sign-out, expiry or restart stops credential access.",
            )}
          </p>
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
          <details>
            <summary>{t("Limits")}</summary>
            <pre>{JSON.stringify(review, null, 2)}</pre>
          </details>
          <div className="agent-actions">
            <button
              className="primary"
              disabled={busy || passphrase.length < 12}
            >
              {t("Allow task access")}
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
      )}
    </details>
  );
}
