import { InstanceSummary, RemoteConnection } from "./InstanceConnection";
import logo from "../assets/fased-logo.svg";
import { useState, type FormEvent, type ReactNode } from "react";
import { ArrowRight, LockKeyhole, ShieldCheck } from "../icons";
import { useCore } from "../core";
export function CoreGate({ children }: { children: ReactNode }) {
  const core = useCore();
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [claimError, setClaimError] = useState("");
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
            <h1>Opening your workspace</h1>
            <p>Connecting to Fased…</p>
          </div>
        ) : core.mode === "error" ? (
          <div className="settings-body">
            <h1>{native ? "Connect to Fased" : "Connection unavailable"}</h1>
            {!native && <p role="alert">{core.error}</p>}
            <button className="primary" onClick={() => void core.retry()}>
              Retry connection
            </button>
          </div>
        ) : (
          <form onSubmit={submit}>
            <div className="settings-body">
              <span className="feature-icon lime">
                <LockKeyhole size={23} />
              </span>
              <h1>
                {core.mode === "unclaimed" ? "Claim this workspace" : "Sign in"}
              </h1>
              {core.mode === "unclaimed" && (
                <>
                  <label htmlFor="owner-code">One-use claim code</label>
                  <input
                    id="owner-code"
                    type="password"
                    autoComplete="off"
                    required
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                  />
                  <details>
                    <summary>Get setup code</summary>
                    <p>
                      Run <code>fased-app claim-code</code> on this instance.
                      Codes expire after 15 minutes.
                    </p>
                  </details>
                  {native && (
                    <button
                      type="button"
                      className="secondary"
                      onClick={() => void useLocalCode()}
                    >
                      Use this computer’s setup code
                    </button>
                  )}
                  {claimError && <p role="alert">{claimError}</p>}
                </>
              )}
              <label htmlFor="owner-password">
                {core.mode === "unclaimed"
                  ? "Create owner password"
                  : "Owner password"}
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
              {core.mode === "unclaimed" && (
                <p className="subtle">
                  At least 12 characters. This protects app access; it is not a
                  wallet unlock credential.
                </p>
              )}
              {core.error && (
                <p className="core-error" role="alert">
                  {core.error}
                </p>
              )}
              <button className="primary" disabled={core.saving}>
                {core.saving
                  ? "Checking…"
                  : core.mode === "unclaimed"
                    ? "Claim workspace"
                    : "Sign in"}
                <ArrowRight size={15} />
              </button>
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
