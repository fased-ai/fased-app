import { ownerHandoff } from "../owner-handoff";
import { useUI } from "../ui-preferences";
import { InstanceSummary, RemoteConnection } from "./InstanceConnection";
import logo from "../assets/fased-logo.svg";
import { captureOwnerSetup } from "../owner-setup";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { ArrowRight, ShieldCheck } from "../icons";
import { useCore } from "../core";
// Module capture survives React StrictMode's repeated render/effect setup.
let ownerSetup = captureOwnerSetup(window.location.href, (url) =>
  history.replaceState(history.state, "", url),
);
let nativeProof: { instanceId: string; result: Promise<string> } | undefined;
let localProof: { code: string; result: Promise<void> } | undefined;
let nativeLocalAttempted = false;
let remoteAttempt: { instance: string; result: Promise<void> } | undefined;
export function CoreGate({ children }: { children: ReactNode }) {
  const { t: uiText } = useUI();
  const core = useCore();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState(ownerSetup?.code ?? "");
  const [claimError, setClaimError] = useState("");
  const [passwordSignIn, setPasswordSignIn] = useState(false);
  const handoff = ownerHandoff(core.connection);
  const recoveryCommands = handoff?.recovery ?? [];
  const native = ["wails", "wails.localhost"].includes(
    window.location.hostname,
  );
  useEffect(() => {
    if (!core.remoteAccess || !core.connection) return;
    if (core.mode === "ready") {
      remoteAttempt = {
        instance: core.connection.instanceId,
        result: Promise.resolve(),
      };
      ownerSetup = null;
      setCode("");
      return;
    }
    if (!["locked", "unclaimed"].includes(core.mode)) return;
    try {
      if (!code && sessionStorage.getItem("fased.remote.signed-out") === "1")
        return;
    } catch {
      /* OS-device authority still requires explicit Connect after this window's logout. */
    }
    if (!core.remoteEnrolled && !code) return;
    const instance = core.connection.instanceId;
    if (ownerSetup && ownerSetup.instanceId !== instance) {
      setCode("");
      setClaimError("Reopen Fased to connect.");
      return;
    }
    if (!remoteAttempt || remoteAttempt.instance !== instance) {
      remoteAttempt = {
        instance,
        result: core.authenticate("", code || undefined, false, true),
      };
    }
    let active = true;
    remoteAttempt.result
      .then(() => {
        ownerSetup = null;
        if (active) setCode("");
      })
      .catch(() => {
        if (active) setClaimError("Use an approved device or a setup link.");
      });
    return () => {
      active = false;
    };
  }, [
    core.remoteAccess,
    core.remoteEnrolled,
    core.connection,
    core.mode,
    code,
  ]);
  useEffect(() => {
    if (!native || !core.localAccess) return;
    if (core.mode === "ready") {
      nativeLocalAttempted = true;
      return;
    }
    if (!nativeLocalAttempted && ["locked", "unclaimed"].includes(core.mode)) {
      nativeLocalAttempted = true;
      void core
        .authenticate("", undefined, true)
        .catch(() => setClaimError("Reopen Fased to connect."));
    }
  }, [native, core.localAccess, core.mode]);
  useEffect(() => {
    if (ownerSetup?.mode === "local" && core.mode === "ready") {
      ownerSetup = null;
      setCode("");
      return;
    }
    if (
      ownerSetup?.mode !== "local" ||
      !core.connection ||
      !["locked", "unclaimed"].includes(core.mode)
    )
      return;
    if (ownerSetup.instanceId !== core.connection.instanceId) {
      ownerSetup = null;
      setCode("");
      setClaimError("Reopen Fased to connect.");
      return;
    }
    const proof = ownerSetup.code;
    if (!localProof || localProof.code !== proof) {
      localProof = { code: proof, result: core.authenticate("", proof, true) };
    }
    let active = true;
    localProof.result
      .then(() => {
        if (ownerSetup?.code === proof) ownerSetup = null;
        if (active) setCode("");
      })
      .catch(() => {
        if (ownerSetup?.code === proof) ownerSetup = null;
        if (active) {
          setCode("");
          setClaimError("Reopen Fased to connect.");
        }
      });
    return () => {
      active = false;
    };
  }, [core.mode, core.connection, code]);
  useEffect(() => {
    const receive = () => {
      const next = captureOwnerSetup(window.location.href, (url) =>
        history.replaceState(history.state, "", url),
      );
      if (!next) return;
      if (core.connection && next.instanceId !== core.connection.instanceId) {
        setClaimError("Reopen Fased to finish setup.");
        setCode("");
        return;
      }
      ownerSetup = next;
      setCode(next.code);
      setClaimError("");
    };
    window.addEventListener("hashchange", receive);
    return () => window.removeEventListener("hashchange", receive);
  }, [core.connection]);
  useEffect(() => {
    if (core.localAccess || core.mode !== "unclaimed" || !core.connection)
      return;
    const id = core.connection.instanceId;
    if (ownerSetup && ownerSetup.instanceId !== id) {
      ownerSetup = null;
      setCode("");
      return;
    }
    if (code || !native) return;
    if (!nativeProof || nativeProof.instanceId !== id) {
      nativeProof = {
        instanceId: id,
        result: (async () => {
          const response = await fetch("/api/v1/installation/claim-code", {
            cache: "no-store",
          });
          const result = await response.json();
          if (!response.ok || !/^[A-Za-z0-9_-]{43}$/.test(result.code ?? ""))
            throw Error("Reopen Fased to finish setup.");
          return result.code as string;
        })(),
      };
    }
    let active = true;
    nativeProof.result
      .then((value) => {
        if (active) setCode(value);
      })
      .catch(() => {
        if (active) setClaimError("Reopen Fased to finish setup.");
      });
    return () => {
      active = false;
    };
  }, [core.mode, core.connection, native, code, core.localAccess]);
  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      await core.authenticate(
        password,
        core.mode === "unclaimed" ? code.trim() : undefined,
      );
      if (core.remoteAccess)
        await core.authenticate("", undefined, false, true);
      setPassword("");
      setCode("");
      ownerSetup = null;
      nativeProof = undefined;
    } catch {
      /* error is rendered by the core controller */
    }
  }
  if (core.mode === "ready" || core.mode === "prototype") return children;
  return (
    <main className="core-gate">
      <section className="core-auth panel">
        <div className="core-auth-brand">
          <img src={logo} alt="" width="28" height="28" />
          <strong>Fased</strong>
        </div>
        <InstanceSummary />
        {core.mode === "loading" ? (
          <div className="empty-state" role="status">
            <ShieldCheck size={28} />
            <h1>{uiText("Opening your workspace")}</h1>
            <p>{uiText("Connecting to Fased…")}</p>
          </div>
        ) : core.mode === "error" ? (
          <div className="settings-body">
            <h1>
              {native
                ? uiText("Connect to Fased")
                : uiText("Connection unavailable")}
            </h1>
            {!native && <p role="alert">{core.error}</p>}
            <button className="primary" onClick={() => void core.retry()}>
              {uiText("Retry connection")}
            </button>
          </div>
        ) : ownerSetup?.mode === "local" ? (
          <div className="empty-state" role="status">
            {uiText("Opening your workspace")}
          </div>
        ) : core.localAccess && !passwordSignIn && !code ? (
          <div className="settings-body">
            <h1>{uiText("Open Fased")}</h1>
            {native && (
              <button
                className="primary"
                disabled={core.saving}
                onClick={() =>
                  void core
                    .authenticate("", undefined, true)
                    .catch(() => setClaimError("Reopen Fased to connect."))
                }
              >
                {uiText("Open Fased")}
              </button>
            )}
            <p role={claimError ? "alert" : "status"}>
              {uiText(claimError || "Open Fased on this computer.")}
            </p>
            {core.passwordAvailable && (
              <button onClick={() => setPasswordSignIn(true)}>
                {uiText("Use password")}
              </button>
            )}
          </div>
        ) : core.remoteAccess &&
          (core.mode === "unclaimed" || core.remoteEnrolled) ? (
          <div className="settings-body">
            <h1>{uiText("Open Fased")}</h1>
            <button
              className="button primary"
              disabled={core.saving}
              onClick={() => {
                remoteAttempt = undefined;
                void core
                  .authenticate("", code || undefined, false, true)
                  .catch(() =>
                    setClaimError("Use an approved device or a setup link."),
                  );
              }}
            >
              {uiText("Connect")}
            </button>
            {claimError && <p role="alert">{uiText(claimError)}</p>}
          </div>
        ) : (
          <form onSubmit={submit}>
            <div className="settings-body">
              <h1>
                {core.mode === "unclaimed"
                  ? uiText("Claim this workspace")
                  : uiText("Sign in")}
              </h1>
              {core.mode === "unclaimed" && !code && (
                <p role="status">
                  {uiText(
                    claimError || "Open the setup link from your installation.",
                  )}
                </p>
              )}
              <label htmlFor="owner-password">
                {core.mode === "unclaimed"
                  ? uiText("Create owner password")
                  : uiText("Owner password")}
              </label>
              <input
                id="owner-password"
                type="password"
                autoFocus={core.mode === "locked"}
                autoComplete={
                  core.mode === "unclaimed"
                    ? "new-password"
                    : "current-password"
                }
                required
                minLength={core.mode === "unclaimed" ? 12 : undefined}
                maxLength={1024}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              {core.error && (
                <p className="core-error" role="alert">
                  {core.mode === "unclaimed"
                    ? uiText("Reopen Fased to finish setup.")
                    : core.error}
                </p>
              )}
              <button
                className="primary"
                disabled={core.saving || (core.mode === "unclaimed" && !code)}
              >
                {core.saving
                  ? uiText("Checking…")
                  : core.mode === "unclaimed"
                    ? uiText("Claim workspace")
                    : uiText("Sign in")}
                <ArrowRight size={15} />
              </button>
              {core.mode === "locked" && (
                <details>
                  <summary>{uiText("Recovery")}</summary>
                  {handoff ? (
                    <>
                      <p>{uiText("Run on this instance")}</p>
                      <pre
                        style={{
                          whiteSpace: "pre-wrap",
                          overflowWrap: "anywhere",
                        }}
                      >
                        <code>{recoveryCommands.join("\n")}</code>
                      </pre>
                    </>
                  ) : (
                    <p>
                      {uiText("Use this installation’s recovery commands.")}
                    </p>
                  )}
                </details>
              )}
            </div>
          </form>
        )}
        {core.mode !== "loading" && (
          <RemoteConnection expanded={native && core.mode === "error"} />
        )}
      </section>
    </main>
  );
}
