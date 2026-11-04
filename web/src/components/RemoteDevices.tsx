import { useEffect, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";

type Devices = {
  devices: { nodeId: string; name: string; login: string }[];
  current: string;
};
export function RemoteDevices() {
  const core = useCore();
  const { t } = useUI();
  const [devices, setDevices] = useState<Devices | null>(null);
  const [link, setLink] = useState("");
  const [error, setError] = useState("");
  const load = () =>
    core
      .request<Devices>("auth/devices")
      .then(setDevices)
      .catch(() => setError(t("Device list unavailable")));
  useEffect(() => {
    if (core.mode === "ready" && core.remoteAccess) void load();
  }, [core.mode, core.remoteAccess]);
  if (!core.remoteAccess || core.mode !== "ready") return null;
  async function change(action: "invite" | "revoke", nodeId?: string) {
    setError("");
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
  return (
    <section className="panel">
      <div className="panel-heading">
        <h2>{t("Devices")}</h2>
        <button className="secondary" onClick={() => void change("invite")}>
          {t("Add device")}
        </button>
      </div>
      <div className="settings-body">
        {devices?.devices.map((device) => (
          <div className="settings-row" key={device.nodeId}>
            <span>{device.name || device.login}</span>
            {device.nodeId === devices.current ? (
              <span>{t("This device")}</span>
            ) : (
              <button
                className="secondary danger"
                onClick={() => void change("revoke", device.nodeId)}
              >
                {t("Remove")}
              </button>
            )}
          </div>
        ))}
        {link && (
          <label>
            {t("Open on your other device")}
            <input
              readOnly
              value={link}
              onFocus={(e) => e.currentTarget.select()}
              autoComplete="off"
            />
          </label>
        )}
        {error && <p role="alert">{error}</p>}
      </div>
    </section>
  );
}
