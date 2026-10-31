import { useCallback, useEffect, useRef, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import {
  applicationUpdateLabels,
  readApplicationUpdates,
  type ApplicationUpdateView,
} from "../application-updates";
import type { UpdatePolicy } from "../update-preferences";
import "../updates.css";
export function ApplicationUpdatesPanel() {
  const { t } = useUI();
  const core = useCore();
  const request = core.request;
  const [view, setView] = useState<ApplicationUpdateView | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [review, setReview] = useState<ApplicationUpdateView | null>(null);
  const epoch = useRef(0);
  const refresh = useCallback(async () => {
    const n = ++epoch.current;
    try {
      const v = readApplicationUpdates(await request("updates/application"));
      if (n === epoch.current) {
        setView(v);
        setError("");
      }
    } catch {
      if (n === epoch.current) {
        setView(null);
        setError("Update status unavailable. Refresh and try again.");
      }
    }
  }, [request]);
  useEffect(() => {
    if (core.mode === "ready") void refresh();
    else setView(null);
    return () => {
      epoch.current++;
    };
  }, [core.mode, refresh]);
  const phase = view?.operation.phase;
  const running = ["check-queued", "checking", "queued", "applying"].includes(
    phase ?? "",
  );
  useEffect(() => {
    if (!running) return;
    const timer = setInterval(() => void refresh(), 1000);
    return () => clearInterval(timer);
  }, [running, refresh]);
  async function action(name: string, reviewed = view) {
    if (!reviewed) return;
    setBusy(true);
    setError("");
    try {
      const value = await request("updates/application", "POST", {
        action: name,
        revision: reviewed.revision,
        policyRevision: reviewed.policyRevision,
        ...(name === "apply"
          ? { manifestSha256: reviewed.operation.release?.manifestSha256 }
          : {}),
      });
      setView(readApplicationUpdates(value));
      setReview(null);
    } catch (e) {
      const message =
        e instanceof Error
          ? e.message
          : "Could not update. Refresh before retrying.";
      await refresh();
      setError(message);
    } finally {
      setBusy(false);
    }
  }
  async function policy(next: UpdatePolicy) {
    if (!view) return;
    const previous = view;
    setView({ ...view, policy: next });
    setBusy(true);
    setError("");
    try {
      await request("updates/preferences", "PUT", {
        revision: view.policyRevision,
        action: "policy",
        policy: next,
      });
      await refresh();
    } catch (e) {
      const message =
        e instanceof Error ? e.message : "Could not save update settings.";
      setView(previous);
      await refresh();
      setError(message);
    } finally {
      setBusy(false);
    }
  }
  const release = view?.operation.release;
  return (
    <section className="panel" aria-labelledby="application-updates-title">
      <div className="panel-heading">
        <h2 id="application-updates-title">{t("Updates")}</h2>
        <button
          className="ghost"
          disabled={busy || core.mode !== "ready"}
          onClick={() => void refresh()}
        >
          {t("Refresh")}
        </button>
      </div>
      <div className="settings-body">
        {core.mode !== "ready" ? (
          <p>{t("Connect and sign in to view updates.")}</p>
        ) : (
          <>
            {error && <p role="alert">{t(error)}</p>}
            {view && (
              <>
                <div className="row spread">
                  <span>{t("Installed")}</span>
                  <span>{view.installedVersion}</span>
                </div>
                <p role="status">
                  {t(
                    view.controllerAvailable
                      ? applicationUpdateLabels[view.operation.phase]
                      : "Release channel unavailable",
                  )}
                </p>
                {!view.controllerAvailable && (
                  <p className="subtle">
                    {t(
                      "Updates stay off until the trusted release channel is ready.",
                    )}
                  </p>
                )}
                {view.operation.failure && (
                  <p role="alert">{t(view.operation.failure)}</p>
                )}
                {release && (
                  <div className="row spread">
                    <span>{t("Available")}</span>
                    <span>{release.version}</span>
                  </div>
                )}
                <div className="actions">
                  <button
                    className="secondary"
                    disabled={
                      busy ||
                      running ||
                      !view.controllerAvailable ||
                      phase === "recovery"
                    }
                    onClick={() => void action("check")}
                  >
                    {t("Check")}
                  </button>
                  {phase === "available" && (
                    <button
                      className="primary"
                      disabled={busy}
                      onClick={() => setReview(view)}
                    >
                      {t("Update")}
                    </button>
                  )}
                  {phase === "recovery" && (
                    <button
                      className="secondary"
                      disabled={busy || !view.controllerAvailable}
                      onClick={() => void action("recover")}
                    >
                      {t("Check recovery")}
                    </button>
                  )}
                </div>
                {review && release && (
                  <section className="panel" aria-label={t("Review update")}>
                    <h3>{t("Review update")}</h3>
                    <p>
                      {t("Version")}: {review.operation.release?.version}
                    </p>
                    <p>
                      {t(
                        "Your data stays in this instance. Active work may delay the update.",
                      )}
                    </p>
                    <div className="actions">
                      <button
                        className="secondary"
                        disabled={busy}
                        onClick={() => setReview(null)}
                      >
                        {t("Cancel")}
                      </button>
                      <button
                        className="primary"
                        disabled={busy}
                        onClick={() => void action("apply", review)}
                      >
                        {t("Update now")}
                      </button>
                    </div>
                  </section>
                )}
                <label>
                  {t("Mode")}
                  <select
                    aria-label={t("Update mode")}
                    value={view.policy.mode ?? "manual"}
                    disabled={busy || running}
                    onChange={(e) =>
                      void policy({
                        ...view.policy,
                        mode: e.target.value as "manual" | "automatic",
                      })
                    }
                  >
                    <option value="manual">{t("Manual")}</option>
                    <option value="automatic">{t("Automatic")}</option>
                  </select>
                </label>
                {!view.activationEnabled && (
                  <p className="subtle">
                    {t("Automatic installation is not active.")}
                  </p>
                )}
                <label className="checkbox-row">
                  <input
                    type="checkbox"
                    checked={view.policy.notify}
                    disabled={busy || running}
                    onChange={(e) =>
                      void policy({ ...view.policy, notify: e.target.checked })
                    }
                  />
                  {t("Show in inbox")}
                </label>
                <details>
                  <summary>{t("Schedule and version")}</summary>
                  <label className="checkbox-row">
                    <input
                      type="checkbox"
                      disabled={busy || running}
                      checked={!!view.policy.window}
                      onChange={(e) =>
                        void policy({
                          ...view.policy,
                          window: e.target.checked
                            ? {
                                timeZone:
                                  Intl.DateTimeFormat().resolvedOptions()
                                    .timeZone || "UTC",
                                days: [0, 1, 2, 3, 4, 5, 6],
                                start: "02:00",
                                end: "04:00",
                              }
                            : undefined,
                        })
                      }
                    />
                    {t("Maintenance window")}
                  </label>
                  {view.policy.window && (
                    <div className="actions" aria-label={t("Days")}>
                      {["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"].map(
                        (day, index) => (
                          <button
                            key={day}
                            className="secondary"
                            aria-pressed={view.policy.window!.days.includes(
                              index,
                            )}
                            disabled={busy}
                            onClick={() => {
                              const window = view.policy.window!;
                              const days = window.days.includes(index)
                                ? window.days.filter((d) => d !== index)
                                : [...window.days, index].sort();
                              if (!days.length) {
                                setError("Choose at least one day.");
                                return;
                              }
                              setView({
                                ...view,
                                policy: {
                                  ...view.policy,
                                  window: { ...window, days },
                                },
                              });
                            }}
                          >
                            {t(day)}
                          </button>
                        ),
                      )}
                    </div>
                  )}
                  {view.policy.window && (
                    <div className="row">
                      <label>
                        {t("Timezone")}
                        <input
                          value={view.policy.window.timeZone}
                          disabled={busy}
                          onChange={(e) =>
                            setView({
                              ...view,
                              policy: {
                                ...view.policy,
                                window: {
                                  ...view.policy.window!,
                                  timeZone: e.target.value,
                                },
                              },
                            })
                          }
                        />
                      </label>
                      <label>
                        {t("From")}
                        <input
                          type="time"
                          value={view.policy.window.start}
                          disabled={busy}
                          onChange={(e) =>
                            setView({
                              ...view,
                              policy: {
                                ...view.policy,
                                window: {
                                  ...view.policy.window!,
                                  start: e.target.value,
                                },
                              },
                            })
                          }
                        />
                      </label>
                      <label>
                        {t("To")}
                        <input
                          type="time"
                          value={view.policy.window.end}
                          disabled={busy}
                          onChange={(e) =>
                            setView({
                              ...view,
                              policy: {
                                ...view.policy,
                                window: {
                                  ...view.policy.window!,
                                  end: e.target.value,
                                },
                              },
                            })
                          }
                        />
                      </label>
                      <button
                        className="secondary"
                        disabled={busy}
                        onClick={() => void policy(view.policy)}
                      >
                        {t("Save")}
                      </button>
                    </div>
                  )}
                  {view.policy.pin ? (
                    <div className="row">
                      <span>{t("Version pinned")}</span>
                      <button
                        className="ghost"
                        disabled={busy}
                        onClick={() =>
                          void policy({ ...view.policy, pin: undefined })
                        }
                      >
                        {t("Unpin")}
                      </button>
                    </div>
                  ) : (
                    release && (
                      <button
                        className="ghost"
                        disabled={busy}
                        onClick={() =>
                          void policy({
                            ...view.policy,
                            pin: {
                              sequence: release.sequence,
                              manifestSha256: release.manifestSha256,
                            },
                          })
                        }
                      >
                        {t("Pin release")}
                      </button>
                    )
                  )}
                </details>
              </>
            )}
          </>
        )}
      </div>
    </section>
  );
}
