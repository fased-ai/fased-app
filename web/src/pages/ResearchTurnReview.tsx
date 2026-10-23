import { useEffect, useRef, useState } from "react";
import type { AgentAIRequest } from "../agents";
import { useCore } from "../core";

export function ResearchTurnReview({
  request,
  revision,
  fixtureAvailable,
  onChanged,
}: {
  request: AgentAIRequest;
  revision: number;
  fixtureAvailable: boolean;
  onChanged: () => Promise<void>;
}) {
  const core = useCore();
  const [result, setResult] = useState<{
    sourceState: string;
    dispatchEnabled: boolean;
    revision: number;
    request: AgentAIRequest;
  }>();
  const [error, setError] = useState("");
  const [consent, setConsent] = useState(false);
  const [busy, setBusy] = useState(false);
  const epoch = useRef(0);
  const active = useRef<AbortController | null>(null);
  useEffect(() => {
    epoch.current++;
    active.current?.abort();
    setResult(undefined);
    setConsent(false);
    setError("");
    setBusy(false);
    return () => {
      epoch.current++;
      active.current?.abort();
    };
  }, [request.profileId, request.id, revision]);
  async function inspect() {
    const generation = ++epoch.current;
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    setBusy(true);
    setError("");
    try {
      const r = await core.request<{
        sourceState: string;
        dispatchEnabled: boolean;
        revision: number;
        request: AgentAIRequest;
      }>(
        "wen/research/ai-inspect",
        "POST",
        {
          schema: "wen-research-ai-selection/1",
          profile: request.profileId,
          id: request.researchTurnId,
          revision,
        },
        controller.signal,
      );
      if (
        r.request.id !== request.id ||
        r.request.profileId !== request.profileId ||
        r.request.digest !== request.digest ||
        r.dispatchEnabled
      )
        throw Error("Research review identity is unavailable");
      if (generation === epoch.current && !controller.signal.aborted)
        setResult(r);
    } catch (e) {
      if (generation === epoch.current && !controller.signal.aborted)
        setError((e as Error).message);
    } finally {
      if (generation === epoch.current) setBusy(false);
    }
  }
  async function startFixture() {
    if (
      !consent ||
      result?.sourceState !== "unchanged" ||
      result.revision !== revision ||
      result.request.state !== "prepared"
    )
      return;
    const generation = ++epoch.current;
    setBusy(true);
    setError("");
    setConsent(false);
    try {
      await core.request("wen/research/ai-start", "POST", {
        schema: "wen-research-ai-start/1",
        profile: request.profileId,
        id: request.researchTurnId,
        revision,
        requestId: request.id,
        consentDigest: request.digest,
      });
      await onChanged();
    } catch (e) {
      if (generation === epoch.current) setError((e as Error).message);
    } finally {
      if (generation === epoch.current) {
        setBusy(false);
        setResult(undefined);
      }
    }
  }
  return (
    <section aria-label="Research turn">
      <button
        className="secondary"
        disabled={busy}
        onClick={() => void inspect()}
      >
        Inspect research turn
      </button>
      {busy && (
        <button
          className="secondary"
          onClick={() => {
            epoch.current++;
            active.current?.abort();
            setBusy(false);
          }}
        >
          Cancel turn inspection
        </button>
      )}
      {error && <p role="alert">{error}</p>}
      {result && (
        <div role="region" aria-label="Research turn record">
          <p role="status">{result.sourceState}</p>
          <pre>{JSON.stringify(result, null, 2)}</pre>
          {request.state === "prepared" &&
            result.request.state === "prepared" && (
              <>
                <label className="ai-consent">
                  <input
                    type="checkbox"
                    checked={consent}
                    disabled={
                      busy ||
                      !fixtureAvailable ||
                      result.sourceState !== "unchanged" ||
                      result.revision !== revision
                    }
                    onChange={(e) => setConsent(e.target.checked)}
                  />
                  I reviewed this research wire and consent to one fixture
                  request
                </label>
                <button
                  className="primary"
                  disabled={
                    busy ||
                    !fixtureAvailable ||
                    !consent ||
                    result.sourceState !== "unchanged" ||
                    result.revision !== revision
                  }
                  onClick={() => void startFixture()}
                >
                  Run research fixture
                </button>
              </>
            )}
        </div>
      )}
    </section>
  );
}
