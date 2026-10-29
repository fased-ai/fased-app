import { ownerHandoff } from "../owner-handoff";
import { useUI } from "../ui-preferences";
import { InstanceSummary, RemoteConnection } from "./InstanceConnection";
import logo from "../assets/fased-logo.svg";
import { useState, type FormEvent, type ReactNode } from "react";
import { ArrowRight, ShieldCheck } from "../icons";
import { useCore } from "../core";
export function CoreGate({ children }: { children: ReactNode }) {
  const { t: uiText } = useUI();
  const core = useCore();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [claimError, setClaimError] = useState("");
  const handoff = ownerHandoff(core.connection);
  const recoveryCommands = handoff?.recovery ?? [];
  const native = ["wails", "wails.localhost"].includes(
    window.location.hostname,
  );
  async function useLocalCode() {
    setClaimError("");
    try {
      const response = await fetch("/api/v1/installation/claim-code", {
        cache: "no-store",
      });
      const result = await response.json();
      if (!response.ok || typeof result.code !== "string")
        throw new Error(result.error || "Local claim code unavailable");
      setCode(result.code);
    } catch (error) {
      setClaimError(
        error instanceof Error ? error.message : "Local claim code unavailable",
      );
    }
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    try {
      await core.authenticate(
        password,
        core.mode === "unclaimed" ? code.trim() : undefined,
      );
      setPassword("");
      setCode("");
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
        ) : (
          <form onSubmit={submit}>
            <div className="settings-body">
              <h1>
                {core.mode === "unclaimed"
                  ? uiText("Claim this workspace")
                  : uiText("Sign in")}
              </h1>
              {core.mode === "unclaimed" && (
                <>
                  <label htmlFor="owner-code">
                    {uiText("One-use claim code")}
                  </label>
                  <input
                    id="owner-code"
                    type="password"
                    autoComplete="off"
                    required
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                  />
                  <details>
                    <summary>{uiText("Get setup code")}</summary>
                    {handoff ? (
                      <p>
                        {uiText("Run ")}
                        <code style={{ overflowWrap: "anywhere" }}>
                          {handoff.claim}
                        </code>
                        {uiText(
                          " on this instance. Codes expire after 15 minutes.",
                        )}
                      </p>
                    ) : (
                      <p>
                        {uiText(
                          "Use the setup command printed by the installer.",
                        )}
                      </p>
                    )}
                  </details>
                  {native && (
                    <button
                      type="button"
                      className="secondary"
                      onClick={() => void useLocalCode()}
                    >
                      {uiText("Use this computer’s setup code")}
                    </button>
                  )}
                  {claimError && <p role="alert">{claimError}</p>}
                </>
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
                  {core.error}
                </p>
              )}
              <button className="primary" disabled={core.saving}>
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
