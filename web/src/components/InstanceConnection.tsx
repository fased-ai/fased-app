import { useUI } from "../ui-preferences";
import "./instance-connection.css";
import { useState, useId, type FormEvent } from "react";
import { instanceOrigin } from "../connection";
import { useCore } from "../core";
import { ArrowRight, Monitor, Server } from "../icons";

export function InstanceSummary() {
  const { t: uiText } = useUI();
  const core = useCore();
  const info = core.connection;
  if (!info) return null;
  return (
    <>
      <dl className="instance-facts">
        <div>
          <dt>{uiText("Connection")}</dt>
          <dd>
            {info.location === "local" ? (
              <Monitor size={14} />
            ) : (
              <Server size={14} />
            )}{" "}
            {info.location === "local" ? uiText("Local") : uiText("Remote")}{" "}
            <span className="dot" />
          </dd>
        </div>
        <div>
          <dt>{uiText("Address")}</dt>
          <dd>
            {location.hostname === "wails" ||
            location.hostname === "wails.localhost"
              ? uiText("Desktop connection")
              : location.origin}
          </dd>
        </div>
      </dl>
      <details className="instance-details">
        <summary>{uiText("Instance details")}</summary>
        <dl className="instance-facts">
          {" "}
          <div>
            <dt>{uiText("Instance")}</dt>
            <dd className="instance-identity">{info.instanceId}</dd>
          </div>
        </dl>
      </details>
    </>
  );
}
export function RemoteConnection({ expanded = false }: { expanded?: boolean }) {
  const { t: uiText } = useUI();
  const inputId = useId();
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [opened, setOpened] = useState(false);
  const [destination, setDestination] = useState("");
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setOpened(false);
    let url: string;
    try {
      url = instanceOrigin(address);
    } catch (e) {
      setError((e as Error).message);
      return;
    }
    setDestination(url);
    if (url === location.origin) {
      setError("You are already using this address.");
      return;
    }
    if (
      location.hostname === "wails" ||
      location.hostname === "wails.localhost"
    ) {
      setBusy(true);
      try {
        const response = await fetch("/api/v1/installation/connect", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ url }),
        });
        const result = await response.json();
        if (!response.ok)
          throw new Error(result.error || "Connection unavailable");
        setOpened(true);
      } catch (e) {
        setError((e as Error).message);
      } finally {
        setBusy(false);
      }
    } else {
      const tab = window.open(url, "_blank", "noopener,noreferrer");
      if (tab) tab.opener = null;
      setOpened(true);
    }
  }
  return (
    <details className="remote-connect" open={expanded || undefined}>
      <summary>{uiText("Connect to Remote")}</summary>
      <form onSubmit={submit}>
        <label htmlFor={inputId}>{uiText("Instance URL")}</label>
        <input
          id={inputId}
          type="url"
          placeholder={uiText("https://your-instance.ts.net")}
          autoComplete="url"
          value={address}
          onChange={(e) => {
            setAddress(e.target.value);
            setError("");
          }}
          required
          maxLength={2048}
        />
        <button className="primary" disabled={busy}>
          {busy ? uiText("Checking…") : uiText("Connect")}
          <ArrowRight size={14} />
        </button>
        {error && <p role="alert">{error}</p>}
        {opened && (
          <p role="status">
            <a href={destination} target="_blank" rel="noopener noreferrer">
              {uiText("Continue to this instance")}
            </a>
          </p>
        )}
      </form>
    </details>
  );
}
