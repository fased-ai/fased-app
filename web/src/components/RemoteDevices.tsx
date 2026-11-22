import { useEffect, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";

type TailscaleDevices = {
  devices: { nodeId: string; name: string; login: string }[];
  current: string;
};
type PairedDevices = {
  devices: {
    id: string;
    label: string;
    transport?: string;
    createdAt: string;
    current?: boolean;
  }[];
  current: string;
};
export function RemoteDevices() {
  const core = useCore();
  const { t } = useUI();
  const [tsDevices, setTsDevices] = useState<TailscaleDevices | null>(null);
  const [pairedDevices, setPairedDevices] = useState<PairedDevices | null>(
    null,
  );
  const [link, setLink] = useState("");
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  const pairedMode = !core.remoteAccess && core.pairingAvailable;
  const load = () => {
    if (pairedMode)
      return core
        .request<PairedDevices>("auth/pair/devices")
        .then(setPairedDevices)
        .catch(() => setError(t("Device list unavailable")));
    return core
      .request<TailscaleDevices>("auth/devices")
      .then(setTsDevices)
      .catch(() => setError(t("Device list unavailable")));
  };
  useEffect(() => {
    if (core.mode === "ready" && (core.remoteAccess || core.pairingAvailable))
      void load();
  }, [core.mode, core.remoteAccess, core.pairingAvailable]);
  if (core.mode !== "ready" || (!core.remoteAccess && !core.pairingAvailable))
    return null;
  async function changeTailscale(action: "invite" | "revoke", nodeId?: string) {
    setError("");
    setCopied(false);
    try {
      const result = await core.request<{ url?: string }>(
        "auth/devices",
        "POST",
        { action, nodeId },
      );
      setLink(result.url ?? "");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : t("Connection unavailable"));
    }
  }
  async function changePaired(action: "issue" | "revoke", deviceId?: string) {
    setError("");
    setCopied(false);
    try {
      const result = await core.request<{ url?: string }>(
        "auth/pair/devices",
        "POST",
        { action, ...(deviceId ? { deviceId } : {}) },
      );
      if (action === "issue") setLink(result.url ?? "");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : t("Connection unavailable"));
    }
  }
  return (
    <section className="panel">
      <div className="panel-heading">
        <h2>{t("Devices")}</h2>
        <button
          className="secondary"
          onClick={() =>
            void (pairedMode
              ? changePaired("issue")
              : changeTailscale("invite"))
          }
        >
          {t("Add device")}
        </button>
      </div>
      <div className="settings-body">
        {pairedMode
          ? pairedDevices?.devices.map((device) => (
              <div className="settings-row" key={device.id}>
                <span>
                  {device.label}
                  {device.transport ? ` · ${device.transport}` : ""}
                </span>
                {device.current || device.id === pairedDevices.current ? (
                  <span>{t("This device")}</span>
                ) : (
                  <button
                    className="secondary danger"
                    onClick={() => void changePaired("revoke", device.id)}
                  >
                    {t("Remove")}
                  </button>
                )}
              </div>
            ))
          : tsDevices?.devices.map((device) => (
              <div className="settings-row" key={device.nodeId}>
                <span>{device.name || device.login}</span>
                {device.nodeId === tsDevices.current ? (
                  <span>{t("This device")}</span>
                ) : (
                  <button
                    className="secondary danger"
                    onClick={() =>
                      void changeTailscale("revoke", device.nodeId)
                    }
                  >
                    {t("Remove")}
                  </button>
                )}
              </div>
            ))}
        {link && (
          <div className="settings-row">
            <label>
              {t("Open on your other device")}
              <input
                readOnly
                value={link}
                onFocus={(e) => e.currentTarget.select()}
                autoComplete="off"
              />
            </label>
            {pairedMode && (
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  void navigator.clipboard?.writeText(link).then(() => {
                    setCopied(true);
                  });
                }}
              >
                {copied ? t("Copied") : t("Copy")}
              </button>
            )}
          </div>
        )}
        {error && <p role="alert">{error}</p>}
      </div>
    </section>
  );
}
