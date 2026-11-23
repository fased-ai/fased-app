import { previewModel } from "../ui-language-preview";
import { useEffect, useRef, useState } from "react";
import type { Connection } from "./AIConnection";
import { useWorkspace } from "../workspace";
import { useUI } from "../ui-preferences";
import { Dialog, ToggleSwitch } from "../components/shared";
import { Clock3 } from "../icons";

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
  continueOnSignOut?: boolean;
  continueAfterRestart?: boolean;
};

const ACCESS_DURATIONS = [
  { value: 15, label: "15 minutes" },
  { value: 60, label: "1 hour" },
  { value: 1440, label: "24 hours" },
] as const;

export function TaskAccessSettings({
  connection,
  onChanged,
}: {
  connection: Connection;
  onChanged: () => void;
}) {
  const { core, agents } = useWorkspace();
  const { t } = useUI();
  const [open, setOpen] = useState(false);
  const [ids, setIDs] = useState<string[]>([]);
  const [minutes, setMinutes] = useState(60);
  const [continueOnSignOut, setContinueOnSignOut] = useState(false);
  const [continueAfterRestart, setContinueAfterRestart] = useState(false);
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
    setContinueOnSignOut(false);
    setContinueAfterRestart(false);
    setPassphrase("");
    return () => {
      generation.current++;
    };
  }, [v?.profileId, core.instanceId, connection.ref]);
  useEffect(() => {
    setOpen(false);
  }, [core.instanceId, connection.ref]);
  useEffect(() => {
    if (!connection.persistentTaskAccessAvailable) {
      setContinueAfterRestart(false);
      setReview((r) => (r?.continueAfterRestart ? undefined : r));
    }
  }, [connection.persistentTaskAccessAvailable]);
  const tasks =
    v?.tasks.filter(
      (task) =>
        task.ai?.authorized &&
        task.ai.connectionRef === connection.ref &&
        ["scheduled", "paused"].includes(task.state),
    ) ?? [];
  const needsPassphrase = !connection.osProtected;
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
              continueOnSignOut,
              continueAfterRestart,
            }
          : {}),
        ...(action === "task-access-allow"
          ? {
              allowance: review,
              consentDigest: review?.id,
              ...(needsPassphrase ? { passphrase: secret } : {}),
            }
          : {}),
      });
      if (epoch === generation.current) {
        setReview(next.accessReview);
        if (action === "task-access-allow" || action === "task-access-revoke") {
          setOpen(false);
        }
      }
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
    <>
      <button
        type="button"
        className="provider-action-btn"
        aria-expanded={open}
        onClick={() => setOpen(true)}
      >
        <Clock3 size={14} />
        <span>{t("Background access")}</span>
        {connection.taskAccess && (
          <span className="provider-action-dot" aria-hidden="true" />
        )}
      </button>
      {open && (
        <Dialog title={t("Background access")} onClose={() => setOpen(false)}>
          <div className="dialog-body">
            {error && <p role="alert">{t(error)}</p>}
            {connection.taskAccess && (
              <div className="agent-inline">
                <span className="badge">
                  {t(
                    connection.taskAccess.continueAfterRestart
                      ? "After restart"
                      : connection.taskAccess.continueOnSignOut
                        ? "After sign-out"
                        : "This session",
                  )}
                </span>
                <span>
                  {
                    v?.profiles.find(
                      (p) => p.id === connection.taskAccess?.profileId,
                    )?.name
                  }{" "}
                  ·{" "}
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
                  type="button"
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
                {v && v.profiles.length > 1 && (
                  <div className="agent-field">
                    <span className="agent-field-label">{t("Agent")}</span>
                    <div
                      className="segmented-control"
                      role="group"
                      aria-label={t("Agent")}
                    >
                      {v.profiles.map((p) => (
                        <button
                          key={p.id}
                          type="button"
                          aria-pressed={v.profileId === p.id}
                          onClick={() => void agents.select(p.id)}
                        >
                          {p.name}
                        </button>
                      ))}
                    </div>
                  </div>
                )}
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
                <div className="agent-field">
                  <span className="agent-field-label">
                    {t("Credential access")}
                  </span>
                  <div
                    className="segmented-control"
                    role="group"
                    aria-label={t("Credential access")}
                  >
                    {ACCESS_DURATIONS.map((opt) => (
                      <button
                        key={opt.value}
                        type="button"
                        aria-pressed={minutes === opt.value}
                        onClick={() => setMinutes(opt.value)}
                      >
                        {t(opt.label)}
                      </button>
                    ))}
                  </div>
                </div>
                {!connection.pilotEnrollment &&
                  !connection.pilotAvailable &&
                  !connection.pilot && (
                    <ToggleSwitch
                      checked={continueOnSignOut}
                      onChange={(next) => {
                        setContinueOnSignOut(next);
                        if (!next) setContinueAfterRestart(false);
                        setReview(undefined);
                      }}
                      label={t("Continue after sign-out")}
                    />
                  )}
                {connection.persistentTaskAccessAvailable &&
                  !connection.pilotEnrollment &&
                  !connection.pilotAvailable &&
                  !connection.pilot && (
                    <ToggleSwitch
                      checked={continueAfterRestart}
                      onChange={(next) => {
                        setContinueAfterRestart(next);
                        if (next) setContinueOnSignOut(true);
                        setReview(undefined);
                      }}
                      label={t("Continue after restart")}
                    />
                  )}
                <div className="agent-actions">
                  <button
                    type="button"
                    className="primary"
                    disabled={busy || connection.planPaused || ids.length === 0}
                    onClick={() => void change("task-access-review")}
                  >
                    {t("Review access")}
                  </button>
                </div>
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
                  {previewModel(review.model, t)} ·{" "}
                  {t("{count} replies · {minutes} minutes", {
                    count: review.maxRequests,
                    minutes: Math.floor(
                      (review.expiresAt - review.createdAt) / 60,
                    ),
                  })}{" "}
                  ·{" "}
                  {(connection.authMethod === "sign-in" ||
                    connection.authMethod === "local") &&
                  review.budgetMicrousd === 0
                    ? t("Usage")
                    : `$${(review.budgetMicrousd / 1_000_000).toFixed(2)}`}
                </p>
                <p>
                  {t(
                    review.continueAfterRestart
                      ? "Selected tasks continue after restart until expiry. Lock or revoke stops access."
                      : review.continueOnSignOut
                        ? "Selected tasks continue after sign-out until expiry. Lock, revoke or restart stops access."
                        : "Selected tasks only. Closing the browser keeps them running. Lock, sign-out, expiry or restart stops credential access.",
                  )}
                </p>
                <dl className="review-list">
                  <div>
                    <dt>{t("Tasks")}</dt>
                    <dd>{review.tasks.length}</dd>
                  </div>
                  <div>
                    <dt>{t("Expires")}</dt>
                    <dd>
                      {new Date(review.expiresAt * 1000).toLocaleString()}
                    </dd>
                  </div>
                </dl>
                {needsPassphrase && (
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
                )}
                <div className="agent-actions">
                  <button
                    className="primary"
                    disabled={
                      busy ||
                      connection.planPaused ||
                      (needsPassphrase && passphrase.length < 12)
                    }
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
          </div>
        </Dialog>
      )}
    </>
  );
}
