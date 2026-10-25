import { useUI } from "../ui-preferences";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useCore } from "../core";
import {
  readUpdatePreferences,
  type UpdatePolicy,
  type UpdatePreferencesRequest,
  type UpdatePreferencesView,
} from "../update-preferences";
import { Badge } from "./shared";
import "../updates.css";

const weekdays = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];
export function UpdatePreferencesPanel() {
  const { t: uiText } = useUI();
  const core = useCore();
  const [view, setView] = useState<UpdatePreferencesView | null>(null);
  const [policy, setPolicy] = useState<UpdatePolicy>({ notify: true });
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [release, setRelease] = useState("");
  const [sequence, setSequence] = useState("");
  const [manifest, setManifest] = useState("");
  const [os, setOS] = useState("linux");
  const [arch, setArch] = useState("amd64");
  const generation = useRef(0);
  const request = core.request;
  const refresh = useCallback(async () => {
    const epoch = ++generation.current;
    setBusy(true);
    setError("");
    setNotice("");
    setView(null);
    try {
      const current = readUpdatePreferences(
        await request("updates/preferences"),
      );
      if (epoch === generation.current) {
        setView(current);
        setPolicy(structuredClone(current.policy));
      }
    } catch {
      if (epoch === generation.current)
        setError(
          "Update preferences unavailable. Reconnect and refresh; no cached records are shown as current.",
        );
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }, [request]);
  useEffect(() => {
    if (core.mode === "ready") void refresh();
    else setView(null);
    return () => {
      generation.current++;
    };
  }, [core.mode, refresh]);
  async function change(input: UpdatePreferencesRequest) {
    const epoch = ++generation.current;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const current = readUpdatePreferences(
        await request("updates/preferences", "PUT", input),
      );
      if (epoch !== generation.current) return false;
      setView(current);
      setPolicy(structuredClone(current.policy));
      setNotice(
        "Saved app metadata only. No update, signature verification or wallet action occurred.",
      );
      await core.retry();
      return true;
    } catch (e) {
      if (epoch === generation.current) {
        setView(null);
        setError(
          (e instanceof Error ? e.message : "Could not save update metadata") +
            ". Refresh and review before retrying. Nothing will be retried automatically.",
        );
      }
      return false;
    } finally {
      if (epoch === generation.current) setBusy(false);
    }
  }
  async function propose(e: FormEvent) {
    e.preventDefault();
    if (!view) return;
    if (
      await change({
        revision: view.revision,
        action: "propose",
        candidate: {
          release,
          sequence,
          manifestSha256: manifest,
          target: { os, arch },
        },
      })
    ) {
      setRelease("");
      setSequence("");
      setManifest("");
    }
  }
  const dirty =
    !!view && JSON.stringify(policy) !== JSON.stringify(view.policy);
  return (
    <section
      className="panel update-preferences"
      aria-labelledby="update-preferences-title"
    >
      <div className="panel-heading">
        <h2 id="update-preferences-title">
          {uiText("Update preferences & proposals")}
        </h2>
        <Badge>{uiText("REVIEW ONLY")}</Badge>
      </div>
      <div className="settings-body">
        <p>
          {uiText(
            "Save notice, pin and window preferences here. Automatic updates remain disabled. These records cannot install, resume, unlock or authorize anything.",
          )}
        </p>
        {core.mode !== "ready" ? (
          <p className="subtle">
            {uiText(
              "Connect and sign in to a Fased instance. Prototype mode does not save update policy.",
            )}
          </p>
        ) : (
          <>
            <button
              className="secondary"
              disabled={busy}
              onClick={() => void refresh()}
            >
              {busy ? uiText("Working…") : uiText("Refresh update preferences")}
            </button>
            {error && <p role="alert">{error}</p>}
            {notice && <p role="status">{notice}</p>}
            {view && (
              <>
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    void change({
                      revision: view.revision,
                      action: "policy",
                      policy,
                    });
                  }}
                >
                  <fieldset disabled={busy} className="update-policy-fields">
                    <legend>{uiText("Notice and review preferences")}</legend>
                    <label>
                      <input
                        type="checkbox"
                        checked={policy.notify}
                        onChange={(e) =>
                          setPolicy({ ...policy, notify: e.target.checked })
                        }
                      />
                      {uiText("Show notices for pending proposals")}
                    </label>
                    <p className="subtle">
                      {uiText(
                        "Notices come only from saved manual proposals. No release feed, background checks, desktop push or scheduler is active.",
                      )}
                    </p>
                    <label>
                      <input
                        type="checkbox"
                        checked={!!policy.pin}
                        onChange={(e) =>
                          setPolicy({
                            ...policy,
                            pin: e.target.checked
                              ? { sequence: "", manifestSha256: "" }
                              : undefined,
                          })
                        }
                      />
                      {uiText("Pin a release identity for review")}
                    </label>
                    {policy.pin && (
                      <div className="update-form-grid">
                        <label>
                          {uiText("Pinned sequence")}
                          <input
                            required
                            inputMode="numeric"
                            value={policy.pin.sequence}
                            onChange={(e) =>
                              setPolicy({
                                ...policy,
                                pin: {
                                  ...policy.pin!,
                                  sequence: e.target.value,
                                },
                              })
                            }
                          />
                        </label>
                        <label>
                          {uiText("Pinned manifest SHA-256")}
                          <input
                            required
                            minLength={64}
                            maxLength={64}
                            value={policy.pin.manifestSha256}
                            onChange={(e) =>
                              setPolicy({
                                ...policy,
                                pin: {
                                  ...policy.pin!,
                                  manifestSha256: e.target.value,
                                },
                              })
                            }
                          />
                        </label>
                      </div>
                    )}
                    <p className="subtle">
                      {uiText(
                        "A pin compares claimed sequence and digest. It does not verify a release or constrain the protected owner's separate review.",
                      )}
                    </p>
                    <label>
                      <input
                        type="checkbox"
                        checked={!!policy.window}
                        onChange={(e) =>
                          setPolicy({
                            ...policy,
                            window: e.target.checked
                              ? {
                                  timeZone: "UTC",
                                  days: [1, 2, 3, 4, 5],
                                  start: "02:00",
                                  end: "04:00",
                                }
                              : undefined,
                          })
                        }
                      />
                      {uiText("Set a preferred maintenance window")}
                    </label>
                    {policy.window && (
                      <>
                        <div className="update-form-grid">
                          <label>
                            {uiText("IANA timezone")}
                            <input
                              required
                              value={policy.window.timeZone}
                              onChange={(e) =>
                                setPolicy({
                                  ...policy,
                                  window: {
                                    ...policy.window!,
                                    timeZone: e.target.value,
                                  },
                                })
                              }
                            />
                          </label>
                          <label>
                            {uiText("Window start")}
                            <input
                              required
                              type="time"
                              value={policy.window.start}
                              onChange={(e) =>
                                setPolicy({
                                  ...policy,
                                  window: {
                                    ...policy.window!,
                                    start: e.target.value,
                                  },
                                })
                              }
                            />
                          </label>
                          <label>
                            {uiText("Window end")}
                            <input
                              required
                              type="time"
                              value={policy.window.end}
                              onChange={(e) =>
                                setPolicy({
                                  ...policy,
                                  window: {
                                    ...policy.window!,
                                    end: e.target.value,
                                  },
                                })
                              }
                            />
                          </label>
                        </div>
                        <div
                          className="update-weekdays"
                          role="group"
                          aria-label={uiText("Maintenance start days")}
                        >
                          {weekdays.map((day, n) => (
                            <label key={day}>
                              <input
                                type="checkbox"
                                checked={policy.window!.days.includes(n)}
                                onChange={(e) =>
                                  setPolicy({
                                    ...policy,
                                    window: {
                                      ...policy.window!,
                                      days: (e.target.checked
                                        ? [...policy.window!.days, n]
                                        : policy.window!.days.filter(
                                            (d) => d !== n,
                                          )
                                      ).sort((a, b) => a - b),
                                    },
                                  })
                                }
                              />
                              {day}
                            </label>
                          ))}
                        </div>
                        <p className="subtle">
                          {uiText(
                            "15 minutes to 12 hours, on the selected start days; crossing midnight is supported. Repeated DST minutes both count; skipped minutes never occur. This is guidance, not a trigger.",
                          )}
                        </p>
                      </>
                    )}
                    <p>
                      {uiText("Window at last check: ")}
                      <strong>{view.window}</strong> ·{" "}
                      {new Date(view.evaluatedAt).toLocaleString()}
                      {uiText(". Refresh to evaluate again.")}
                    </p>
                    <button className="primary" type="submit">
                      {uiText("Save update preferences")}
                    </button>
                  </fieldset>
                </form>
                <h3>
                  {uiText("Update notices (")}
                  {view.notifications.length})
                </h3>
                {view.notifications.length === 0 ? (
                  <p className="subtle">{uiText("No pending notices.")}</p>
                ) : (
                  <ul>
                    {view.notifications.map((id) => (
                      <li key={id}>
                        {
                          view.proposals.find((p) => p.digest === id)!.candidate
                            .release
                        }{" "}
                        {uiText("— manual proposal awaiting review")}
                      </li>
                    ))}
                  </ul>
                )}
                <h3>{uiText("Manual update proposals")}</h3>
                <p className="subtle">
                  {uiText(
                    "Public metadata only. Claimed releases and digests are unverified. The protected owner flow must independently verify signatures, artifacts, compatibility and current state.",
                  )}
                </p>
                {dirty && (
                  <p role="status">
                    {uiText(
                      "Save preferences or refresh to discard changes before managing proposals.",
                    )}
                  </p>
                )}
                <form onSubmit={(e) => void propose(e)}>
                  <fieldset
                    disabled={busy || dirty}
                    className="update-policy-fields"
                  >
                    <legend>{uiText("Add a review-only proposal")}</legend>
                    <div className="update-form-grid">
                      <label>
                        {uiText("Release label")}
                        <input
                          required
                          maxLength={80}
                          value={release}
                          onChange={(e) => setRelease(e.target.value)}
                        />
                      </label>
                      <label>
                        {uiText("Claimed sequence")}
                        <input
                          required
                          inputMode="numeric"
                          value={sequence}
                          onChange={(e) => setSequence(e.target.value)}
                        />
                      </label>
                      <label>
                        {uiText("Claimed manifest SHA-256")}
                        <input
                          required
                          minLength={64}
                          maxLength={64}
                          value={manifest}
                          onChange={(e) => setManifest(e.target.value)}
                        />
                      </label>
                      <label>
                        {uiText("Proposal OS")}
                        <select
                          value={os}
                          onChange={(e) => setOS(e.target.value)}
                        >
                          <option value="linux">{uiText("Linux")}</option>
                          <option value="darwin">{uiText("macOS")}</option>
                          <option value="windows">{uiText("Windows")}</option>
                        </select>
                      </label>
                      <label>
                        {uiText("Proposal architecture")}
                        <select
                          value={arch}
                          onChange={(e) => setArch(e.target.value)}
                        >
                          <option value="amd64">{uiText("x86-64")}</option>
                          <option value="arm64">{uiText("ARM64")}</option>
                        </select>
                      </label>
                    </div>
                    <button className="secondary" type="submit">
                      {uiText("Add review-only proposal")}
                    </button>
                  </fieldset>
                </form>
                {view.proposals.map((p) => (
                  <article
                    className="update-proposal"
                    key={p.digest}
                    aria-label={`Update proposal ${p.candidate.release}`}
                  >
                    <h4>
                      {p.candidate.release}{" "}
                      <Badge>{uiText("UNVERIFIED")}</Badge>
                    </h4>
                    <p>
                      {p.candidate.target.os}/{p.candidate.target.arch}
                      {uiText(" · Sequence ")}
                      {p.candidate.sequence} · {p.state} ·{" "}
                      {p.pinMatch
                        ? uiText("Matches current pin preference")
                        : uiText("Does not match current pin preference")}
                    </p>
                    <p>
                      {uiText("Claimed manifest:")}{" "}
                      <code>{p.candidate.manifestSha256}</code>
                    </p>
                    <p>
                      {uiText("Review digest: ")}
                      <code>{p.digest}</code>
                    </p>
                    <p className="subtle">
                      {uiText(
                        "Marking reviewed acknowledges this exact metadata only. It cannot approve an update.",
                      )}
                    </p>
                    <div className="update-actions">
                      {p.state === "pending" && (
                        <button
                          className="secondary"
                          disabled={busy || dirty}
                          onClick={() =>
                            void change({
                              revision: view.revision,
                              action: "review",
                              digest: p.digest,
                            })
                          }
                        >
                          {uiText("Mark reviewed")}
                        </button>
                      )}
                      {p.state !== "dismissed" && (
                        <button
                          className="secondary"
                          disabled={busy || dirty}
                          onClick={() =>
                            void change({
                              revision: view.revision,
                              action: "dismiss",
                              digest: p.digest,
                            })
                          }
                        >
                          {uiText("Dismiss proposal")}
                        </button>
                      )}
                      {p.state === "dismissed" && (
                        <button
                          className="secondary"
                          disabled={busy || dirty}
                          onClick={() =>
                            void change({
                              revision: view.revision,
                              action: "remove",
                              digest: p.digest,
                            })
                          }
                        >
                          {uiText("Remove dismissed proposal")}
                        </button>
                      )}
                      <button
                        className="secondary"
                        disabled={busy || dirty}
                        onClick={() =>
                          void change({
                            revision: view.revision,
                            action: "policy",
                            policy: {
                              ...view.policy,
                              pin: {
                                sequence: p.candidate.sequence,
                                manifestSha256: p.candidate.manifestSha256,
                              },
                            },
                          })
                        }
                      >
                        {uiText("Pin this proposal")}
                      </button>
                    </div>
                  </article>
                ))}
                <p className="subtle">
                  {uiText(
                    "Up to eight proposals. Dismiss and remove old entries explicitly. Removal forgets its notice history.",
                  )}
                </p>
              </>
            )}
          </>
        )}
      </div>
    </section>
  );
}
