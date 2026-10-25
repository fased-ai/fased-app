import { useUI } from "../ui-preferences";
import { useEffect, useRef, useState } from "react";
import type { AgentAIRequest, ResearchAIReview } from "../agents";
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
  const { t: uiText } = useUI();
  const core = useCore();
  const [result, setResult] = useState<ResearchAIReview>();
  const [error, setError] = useState("");
  const [consent, setConsent] = useState(false);
  const [busy, setBusy] = useState(false);
  const version = (() => {
    try {
      return JSON.parse(request.context).version === "manual-ai-research-v2"
        ? 2
        : 1;
    } catch {
      return 1;
    }
  })();
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
      const r = await core.request<ResearchAIReview>(
        "wen/research/ai-inspect",
        "POST",
        {
          schema: `wen-research-ai-selection/${version}`,
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
        schema: `wen-research-ai-start/${version}`,
        ...(version === 2 ? { step: request.researchStep } : {}),
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
  async function changeTurn(next: boolean) {
    if (!result || busy || result.revision !== revision) return;
    const generation = ++epoch.current;
    setBusy(true);
    setConsent(false);
    setError("");
    try {
      const body = next
        ? {
            schema: "wen-research-ai-next/1",
            profile: request.profileId,
            id: request.researchTurnId,
            revision,
            fromRequestId: request.id,
            fromFrameSHA256: result.turn.models.at(-1)?.frameSHA256,
          }
        : {
            schema: `wen-research-ai-selection/${version}`,
            profile: request.profileId,
            id: request.researchTurnId,
            revision,
          };
      await core.request(
        `wen/research/${next ? "ai-prepare-next" : "ai-cancel"}`,
        "POST",
        body,
      );
      await onChanged();
    } catch (e) {
      if (generation === epoch.current) setError((e as Error).message);
      // A bounded refusal can hold the turn; fetch the authoritative record.
      await onChanged();
    } finally {
      if (generation === epoch.current) {
        setBusy(false);
        setResult(undefined);
      }
    }
  }
  return (
    <section aria-label={uiText("Research turn")}>
      <button
        className="secondary"
        disabled={busy}
        onClick={() => void inspect()}
      >
        {uiText("Inspect research turn")}
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
          {uiText("Cancel turn inspection")}
        </button>
      )}
      {error && <p role="alert">{error}</p>}
      {result && (
        <div role="region" aria-label={uiText("Research turn record")}>
          <p role="status">
            {result.turn.state} · {result.sourceState}
          </p>
          <pre>{JSON.stringify(result, null, 2)}</pre>
          {version === 2 && result.turn.state === "awaiting_review" && (
            <button
              className="primary"
              disabled={
                busy ||
                result.sourceState !== "unchanged" ||
                result.revision !== revision
              }
              onClick={() => void changeTurn(true)}
            >
              {uiText("Prepare next research request")}
            </button>
          )}
          {version === 2 &&
            [
              "prepared",
              "queued",
              "running",
              "reads_queued",
              "reads_running",
              "awaiting_review",
              "held",
            ].includes(result.turn.state) && (
              <button
                className="secondary"
                disabled={busy || result.revision !== revision}
                onClick={() => void changeTurn(false)}
              >
                {uiText("Cancel research turn")}
              </button>
            )}
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
                  {version === 2
                    ? uiText(
                        "I reviewed this wire and its allowed local reads and consent to one fixture request",
                      )
                    : uiText(
                        "I reviewed this research wire and consent to one fixture request",
                      )}
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
                  {uiText("Run research fixture")}
                </button>
              </>
            )}
        </div>
      )}
    </section>
  );
}
