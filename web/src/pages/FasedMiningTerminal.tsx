import { useMemo, useState, useCallback, useEffect, useRef } from "react";
import {
  StrategyTerminal,
  createMiningReadClient,
  ResearchWorkspace,
  browserStorage,
  type ResearchStorage,
  type Library,
  compileAllocation,
  type ResearchContext,
  type ResearchChatReview,
  type SavedChatSelection,
} from "../vendor/wen-terminal/index.js";
import "../vendor/wen-terminal/style.css";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";

/** Shared private research plus a separate, explicitly labelled fixture walkthrough. */
export function FasedMiningTerminal() {
  const core = useCore();
  const { state, agents, navigate } = useWorkspace();
  const profile = state.selectedProfile;
  const profileName =
    state.profiles.find((p) => p.id === profile)?.name ?? profile;
  const [chatReview, setChatReview] = useState<ResearchChatReview | null>(null);
  const [captureError, setCaptureError] = useState("");
  const [captureAttempted, setCaptureAttempted] = useState(false);
  const chatSummary = useMemo(() => {
    try {
      return JSON.parse(chatReview?.text.split("\n")[1] ?? "null");
    } catch {
      return null;
    }
  }, [chatReview]);
  const captureGeneration = useRef(0);
  const capturePanel = useRef<HTMLElement>(null);
  useEffect(() => {
    captureGeneration.current++;
    setChatReview(null);
    setCaptureError("");
    return () => {
      captureGeneration.current++;
    };
  }, [profile, core.mode, core.instanceId]);
  useEffect(() => {
    setCaptureAttempted(false);
    if (chatReview) {
      capturePanel.current?.scrollIntoView({ block: "start" });
      capturePanel.current?.focus();
    }
  }, [chatReview]);
  const storage = useMemo<ResearchStorage>(
    () =>
      core.mode === "ready"
        ? {
            label: "Private core · " + profile,
            load: () =>
              core.request<Library>(
                `wen/research?profile=${encodeURIComponent(profile)}`,
              ),
            save: (revision, raw) =>
              core.request<Library>("wen/research", "POST", {
                profile,
                revision,
                raw,
                action: "save",
              }),
            importHistory: (revision, raw) =>
              core.request<Library>("wen/research", "POST", {
                profile,
                revision,
                raw,
                action: "history",
              }),
          }
        : browserStorage("fased-prototype:" + profile),
    [core.mode, core.request, profile],
  );
  const financialReads = useMemo(() => core.mode === "ready" ? createMiningReadClient({
    fetcher: async (path, options) => {
      const routes: Record<string,string> = {
        "/__wen/mining-read/context":"context", "/__wen/mining-read":"read",
        "/__wen/mining-read/history":"history", "/__wen/mining-read/receipt":"receipt",
        "/__wen/mining-read/treasury-receipt":"treasury-receipt", "/__wen/mining-read/operating-receipt":"operating-receipt",
      };
      const route = routes[String(path)];
      if (!route) throw Error("Unsupported financial read");
      const result = await core.request<unknown>("wen/financial/" + route, options?.method ?? "GET", options?.body ? JSON.parse(String(options.body)) : undefined, options?.signal ?? undefined);
      return Response.json(result);
    },
  }) : undefined, [core.mode, core.instanceId, core.request, profile]);
  const readResearch = useCallback(
    (lookback: number) =>
      core.request<unknown>(
        `wen/market-data?lookback=${lookback}&profile=${encodeURIComponent(profile)}`,
      ),
    [core.request, profile],
  );
  const discussSaved = async (selection: SavedChatSelection) => {
    const generation = captureGeneration.current;
    const review = await core.request<ResearchChatReview>(
      "wen/research/chat-review",
      "POST",
      {
        profile,
        revision: selection.libraryRevision,
        ...(selection.strategyDigest
          ? { strategyDigest: selection.strategyDigest }
          : {}),
        ...(selection.bundleIndex !== undefined
          ? { bundleIndex: selection.bundleIndex }
          : {}),
      },
    );
    if (generation !== captureGeneration.current) return;
    if (review.profileId !== profile)
      throw Error("Research profile changed; select the source again.");
    setChatReview(structuredClone(review));
    setCaptureError("");
  };
  const [context, setContext] = useState<ResearchContext | null>(null);
  const [stage, setStage] = useState<
    "research" | "chat" | "draft" | "compare" | "session" | "results"
  >("research");
  const open = (snapshot: ResearchContext) => {
    setContext(structuredClone(snapshot));
    setStage("research");
  };
  const allocations = (weights: number[]) => {
    if (!context?.amountLamports) return null;
    try {
      return compileAllocation(context.amountLamports, weights);
    } catch {
      return null;
    }
  };
  const selectedAmounts = context ? allocations(context.weightsBps) : null;
  const wideAmounts = allocations([2000, 2000, 2000, 2000, 2000]);
  return (
    <div className="fased-mining-host">
      <StrategyTerminal
        brand="Fased"
        initialView="terminal"
        readProvider={financialReads}
        onOpenResearch={open}
      />
      <ResearchWorkspace
        key={profile + core.mode}
        storage={storage}
        readResearch={core.mode === "ready" ? readResearch : undefined}
        onDiscussSaved={core.mode === "ready" ? discussSaved : undefined}
        onDiscussResearch={
          core.mode === "ready"
            ? (review) => {
                if (review.profileId !== profile) {
                  setCaptureError(
                    "Research profile changed; select the source again.",
                  );
                  return;
                }
                setChatReview(structuredClone(review));
                setCaptureError("");
              }
            : undefined
        }
      />
      {chatReview &&
        core.mode === "ready" &&
        chatReview.profileId === profile && (
          <section
            className="panel fased-research-review"
            aria-label="Research Chat review"
            ref={capturePanel}
            tabIndex={-1}
          >
            <div className="panel-heading">
              <h2>Research → Chat</h2>
              <span className="badge">FROZEN · NO AI</span>
            </div>
            <div className="fased-workflow-body">
              <dl className="fased-context-grid">
                <div>
                  <dt>Conversation profile</dt>
                  <dd>{profileName}</dd>
                </div>
                {chatSummary?.schema === "wen-saved-chat-context/1" ? (
                  <>
                    <div>
                      <dt>Strategy version</dt>
                      <dd>
                        {chatSummary.strategy?.document.name ??
                          "All recorded strategies"}
                      </dd>
                    </div>
                    {chatSummary.strategy && (
                      <div>
                        <dt>Version SHA-256</dt>
                        <dd>
                          <code>{chatSummary.strategy.digest}</code>
                        </dd>
                      </div>
                    )}
                    <div>
                      <dt>Dataset / evidence</dt>
                      <dd>
                        {chatSummary.history
                          ? `${chatSummary.history.scope.dataset} · ${chatSummary.history.scope.evidence} · unverified`
                          : "Strategy only · no results included"}
                      </dd>
                    </div>
                    {chatSummary.history && (
                      <>
                        <div>
                          <dt>Committed / returned (lamports)</dt>
                          <dd>
                            {chatSummary.history.summary.committed} /{" "}
                            {chatSummary.history.summary.returned}
                          </dd>
                        </div>
                        <div>
                          <dt>Fees / deductions (lamports)</dt>
                          <dd>
                            {chatSummary.history.summary.fees} /{" "}
                            {chatSummary.history.summary.forfeitures}
                          </dd>
                        </div>
                        <div>
                          <dt>Declared period</dt>
                          <dd>
                            {chatSummary.history.scope.from} —{" "}
                            {chatSummary.history.scope.to}
                          </dd>
                        </div>
                        <div>
                          <dt>Mining net (lamports)</dt>
                          <dd>{chatSummary.history.summary.netSOL}</dd>
                        </div>
                        <div>
                          <dt>Coverage / gaps</dt>
                          <dd>
                            {chatSummary.history.scope.coverage} ·{" "}
                            {chatSummary.history.gaps.join(", ") ||
                              "None declared"}
                          </dd>
                        </div>
                      </>
                    )}
                  </>
                ) : (
                  <>
                    <div>
                      <dt>BTC/USD midpoint</dt>
                      <dd>
                        {Number.isFinite(chatSummary?.btcUsdMidpoint)
                          ? `$${chatSummary.btcUsdMidpoint.toFixed(2)}`
                          : "Unavailable"}
                      </dd>
                    </div>
                    <div>
                      <dt>Lookback</dt>
                      <dd>
                        {chatSummary?.lookbackMinutes ?? "Unavailable"} min
                      </dd>
                    </div>
                    <div>
                      <dt>Observed at (UTC)</dt>
                      <dd>{chatSummary?.quoteEventAt ?? "Unavailable"}</dd>
                    </div>
                    <div>
                      <dt>Return / realized volatility</dt>
                      <dd>
                        {chatSummary?.metrics
                          ? `${chatSummary.metrics.returnBps} / ${chatSummary.metrics.realizedVolatilityBps} bps`
                          : "Unavailable"}
                      </dd>
                    </div>
                  </>
                )}
                <div>
                  <dt>Review expires</dt>
                  <dd>
                    {new Date(chatReview.expiresAt * 1000).toLocaleTimeString()}
                  </dd>
                </div>
              </dl>
              <details>
                <summary>Source evidence</summary>
                <pre
                  aria-label="Research Chat snapshot"
                  style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                >
                  {chatReview.text}
                </pre>
              </details>
              {(captureError || (captureAttempted && agents.error)) && (
                <p role="alert">{captureError || agents.error}</p>
              )}
              <div className="agent-inline">
                <button
                  className="primary"
                  disabled={
                    agents.busy || chatReview.expiresAt * 1000 <= Date.now()
                  }
                  onClick={() => {
                    setCaptureAttempted(true);
                    if (chatReview.expiresAt * 1000 <= Date.now()) {
                      setCaptureError(
                        "Research review expired; select the source again.",
                      );
                      return;
                    }
                    const generation = captureGeneration.current;
                    void agents
                      .change({
                        action: "wen-research-chat",
                        profileId: chatReview.profileId,
                        revision: chatReview.revision,
                        expiresAt: chatReview.expiresAt,
                        text: chatReview.text,
                        source: chatReview.source,
                      })
                      .then((ok) => {
                        if (generation !== captureGeneration.current) return;
                        if (ok) {
                          setChatReview(null);
                          navigate("agents/chat");
                          window.scrollTo({ top: 0 });
                        }
                      });
                  }}
                >
                  Save to Chat
                </button>
                <button
                  className="secondary"
                  disabled={agents.busy}
                  onClick={() => {
                    setChatReview(null);
                    setCaptureError("");
                  }}
                >
                  Cancel research review
                </button>
              </div>
            </div>
          </section>
        )}
      {context && (
        <section
          className="panel fased-research-review"
          aria-label="Fixture research workflow"
        >
          <div className="panel-heading">
            <h2>Research review</h2>
            <span className="badge">FIXTURE · LOCAL ONLY</span>
            <button className="secondary" onClick={() => setContext(null)}>
              Close review
            </button>
          </div>
          <nav className="fased-workflow-tabs" aria-label="Research workflow">
            {(
              [
                "research",
                "chat",
                "draft",
                "compare",
                "session",
                "results",
              ] as const
            ).map((item) => (
              <button
                key={item}
                aria-current={stage === item ? "step" : undefined}
                onClick={() => setStage(item)}
              >
                {item}
              </button>
            ))}
          </nav>
          <div className="fased-workflow-body" aria-live="polite">
            {stage === "research" && (
              <>
                <h3>Selected snapshot</h3>
                <dl className="fased-context-grid">
                  <div>
                    <dt>Market</dt>
                    <dd>
                      {context.reference} / {context.asset}
                    </dd>
                  </div>
                  <div>
                    <dt>View</dt>
                    <dd>{context.view}</dd>
                  </div>
                  <div>
                    <dt>Horizon</dt>
                    <dd>{context.horizonHours}h</dd>
                  </div>
                  <div>
                    <dt>Source</dt>
                    <dd>Shared terminal fixture</dd>
                  </div>
                  <div>
                    <dt>Private history included</dt>
                    <dd>None</dd>
                  </div>
                  <div>
                    <dt>Provider disclosure</dt>
                    <dd>None</dd>
                  </div>
                </dl>
                <p>Frozen fixture snapshot · live freshness unavailable.</p>
                <button className="primary" onClick={() => setStage("chat")}>
                  Discuss fixture
                </button>
              </>
            )}
            {stage === "chat" && (
              <>
                <h3>Fixture conversation</h3>
                <div className="fased-fixture-message">
                  <strong>You</strong>
                  <p>
                    Review the {context.preset} allocation over{" "}
                    {context.horizonHours} hours using this selected{" "}
                    {context.reference} fixture.
                  </p>
                </div>
                <div className="fased-fixture-message">
                  <strong>Scripted example · no AI call</strong>
                  <p>
                    The five weights express capital allocation, not
                    probabilities. Compare their concentration with Wide before
                    reviewing a session. Missing market observations prevent an
                    entry recommendation.
                  </p>
                </div>
                <button className="primary" onClick={() => setStage("draft")}>
                  Inspect inert draft
                </button>
              </>
            )}
            {stage === "draft" && (
              <>
                <h3>Inert strategy draft</h3>
                <p>
                  {context.preset} · {context.horizonHours}h ·{" "}
                  {displaySOL(context.amountLamports)} per selected round
                </p>
                <AllocationTable
                  weights={context.weightsBps}
                  amounts={selectedAmounts}
                />
                <p>Unsaved draft · no mandate or task.</p>
                <button className="primary" onClick={() => setStage("compare")}>
                  Compare allocation
                </button>
              </>
            )}
            {stage === "compare" && (
              <>
                <h3>Allocation comparison</h3>
                <table className="fased-allocation-table">
                  <thead>
                    <tr>
                      <th>Range</th>
                      <th>Selected fixture</th>
                      <th>Wide fixture</th>
                    </tr>
                  </thead>
                  <tbody>
                    {context.weightsBps.map((weight, i) => (
                      <tr key={i}>
                        <th>{"ABCDE"[i]}</th>
                        <td>
                          {displayWeight(weight)} ·{" "}
                          {displaySOL(selectedAmounts?.[i])}
                        </td>
                        <td>20% · {displaySOL(wideAmounts?.[i])}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <p>
                  Same market and horizon · allocation only · returns
                  unavailable.
                </p>
                <button className="primary" onClick={() => setStage("session")}>
                  Review fixture session
                </button>
              </>
            )}
            {stage === "session" && (
              <>
                <h3>Session preview</h3>
                <dl className="fased-context-grid">
                  <div>
                    <dt>Execution</dt>
                    <dd>Disabled</dd>
                  </div>
                  <div>
                    <dt>Runner</dt>
                    <dd>Not assigned</dd>
                  </div>
                  <div>
                    <dt>Wallet / mandate</dt>
                    <dd>None</dd>
                  </div>
                  <div>
                    <dt>Accepted commitments</dt>
                    <dd>None</dd>
                  </div>
                </dl>
                <button className="primary" disabled>
                  Start session unavailable
                </button>
                <button
                  className="secondary"
                  onClick={() => setStage("results")}
                >
                  Inspect result boundary
                </button>
              </>
            )}
            {stage === "results" && (
              <>
                <h3>Results</h3>
                <p>
                  No actual sessions or receipts. Conditional amounts are not
                  earned rewards.
                </p>
                <dl className="fased-context-grid">
                  <div>
                    <dt>SOL spent</dt>
                    <dd>Not recorded</dd>
                  </div>
                  <div>
                    <dt>BTC received</dt>
                    <dd>Not recorded</dd>
                  </div>
                  <div>
                    <dt>SAT sold</dt>
                    <dd>Not recorded</dd>
                  </div>
                  <div>
                    <dt>Realized proceeds</dt>
                    <dd>Not recorded</dd>
                  </div>
                </dl>
                <button
                  className="secondary"
                  onClick={() => setStage("research")}
                >
                  Return to snapshot
                </button>
              </>
            )}
          </div>
        </section>
      )}
    </div>
  );
}
function AllocationTable({
  weights,
  amounts,
}: {
  weights: number[];
  amounts: readonly string[] | null;
}) {
  return (
    <table className="fased-allocation-table">
      <thead>
        <tr>
          <th>Range</th>
          <th>Capital weight</th>
          <th>SOL</th>
        </tr>
      </thead>
      <tbody>
        {weights.map((weight, i) => (
          <tr key={i}>
            <th>{"ABCDE"[i]}</th>
            <td>{displayWeight(weight)}</td>
            <td>{displaySOL(amounts?.[i])}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// Presentation only: exact base-unit conversion, with no payout calculation.
function displaySOL(amount: string | null | undefined): string {
  if (amount == null) return "Unavailable";
  const units = BigInt(amount);
  const fractional = (units % 1_000_000_000n)
    .toString()
    .padStart(9, "0")
    .replace(/0+$/, "");
  return `${units / 1_000_000_000n}${fractional ? `.${fractional}` : ""} SOL`;
}
function displayWeight(weight: number): string {
  return `${weight / 100}%`;
}
