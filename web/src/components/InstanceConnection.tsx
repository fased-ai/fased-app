import { useUI } from "../ui-preferences";
import "./instance-connection.css";
import { useState, useId, type FormEvent } from "react";
import { remoteInstanceOrigin } from "../connection";
import { useCore } from "../core";
import { ArrowRight, Link2, Monitor, Server } from "../icons";
import { Disclosure } from "./shared";

function transportLabel(transport?: string): string {
  switch (transport) {
    case "tailscale":
      return "Tailscale";
    case "cloudflare":
      return "Cloudflare";
    case "domain":
      return "Domain";
    case "ssh":
      return "SSH";
    case "proxy":
      return "HTTPS";
    default:
      return "";
  }
}

export function InstanceSummary({ compact = false }: { compact?: boolean }) {
  const { t: uiText } = useUI();
  const core = useCore();
  const [detailsOpen, setDetailsOpen] = useState(false);
  const info = core.connection;
  if (!info) return null;
  const transportTag =
    info.location === "remote" ? transportLabel(info.remoteTransport) : "";
  const originText =
    location.hostname === "wails" || location.hostname === "wails.localhost"
      ? uiText("Desktop connection")
      : location.origin;
  if (compact) {
    return (
      <div className="instance-pill-line">
        {info.location === "local" ? (
          <Monitor size={14} />
        ) : (
          <Server size={14} />
        )}
        <span>
          {info.location === "local" ? uiText("Local") : uiText("Remote")}
        </span>
        {transportTag && (
          <span className="transport-tag">· {transportTag}</span>
        )}
        <span className="dot" />
        <span className="instance-origin">{originText}</span>
      </div>
    );
  }
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
            <span>
              {info.location === "local" ? uiText("Local") : uiText("Remote")}
            </span>
            {transportTag && (
              <span className="transport-tag"> · {transportTag}</span>
            )}{" "}
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
      <Disclosure
        label={uiText("Instance details")}
        icon={<Server size={14} />}
        open={detailsOpen}
        onOpenChange={setDetailsOpen}
        className="instance-details"
      >
        <dl className="instance-facts">
          <div>
            <dt>{uiText("Instance")}</dt>
            <dd className="instance-identity">{info.instanceId}</dd>
          </div>
        </dl>
      </Disclosure>
    </>
  );
}
export function RemoteConnection({ expanded = false }: { expanded?: boolean }) {
  const { t: uiText } = useUI();
  const inputId = useId();
  const [open, setOpen] = useState(expanded);
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
      url = remoteInstanceOrigin(address);
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
    <Disclosure
      label={uiText("Connect to Remote")}
      icon={<Link2 size={14} />}
      open={open}
      onOpenChange={setOpen}
      className="remote-connect"
    >
      <form onSubmit={submit}>
        <label htmlFor={inputId}>{uiText("Instance URL")}</label>
        <input
          id={inputId}
          type="url"
          placeholder={uiText("https://fased.example.com")}
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
    </Disclosure>
  );
}
