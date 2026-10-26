import { useState, useRef, useEffect } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { Dialog } from "../components/shared";
import { fixtureNetwork } from "../accounts";
export function RPCCheck({ onClose }: { onClose: () => void }) {
  const { t } = useUI();
  const core = useCore();
  const [endpoint, setEndpoint] = useState("https://rpc.fased.invalid/solana");
  const [result, setResult] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    setBusy(false);
    setResult(false);
    setError("");
    return () => {
      generation.current++;
    };
  }, [core.instanceId]);
  return (
    <Dialog title="RPC" onClose={onClose}>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          if (busy) return;
          const epoch = ++generation.current;
          setBusy(true);
          setResult(false);
          setError("");
          try {
            const v = await core.request<{
              fixtureOnly: boolean;
              liveRPC: boolean;
            }>("accounts/rpc-check", "POST", {
              endpoint,
              network: fixtureNetwork,
            });
            if (epoch === generation.current) {
              if (!v.fixtureOnly || v.liveRPC)
                throw new Error("Unexpected RPC capability");
              setResult(true);
            }
          } catch (e) {
            if (epoch === generation.current)
              setError(e instanceof Error ? e.message : "RPC check failed");
          } finally {
            if (epoch === generation.current) setBusy(false);
          }
        }}
      >
        <label>
          {t("RPC URL")}
          <input
            aria-label={t("RPC URL")}
            value={endpoint}
            maxLength={512}
            disabled={busy}
            onChange={(e) => {
              setEndpoint(e.target.value);
              setResult(false);
              setError("");
            }}
          />
        </label>
        <label>
          {t("Network")}
          <input readOnly value={t("Demo")} />
        </label>
        <button className="primary" disabled={busy}>
          {t("Check RPC")}
        </button>
        {result && <p role="status">{t("RPC verified (demo)")}</p>}
        {error && <p role="alert">{error}</p>}
      </form>
    </Dialog>
  );
}
