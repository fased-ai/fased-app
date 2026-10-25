import type { AgentAIRequest, AgentView } from "../agents";
import { useWorkspace } from "../workspace";
import { ResearchTurnReview } from "./ResearchTurnReview";
import { ChatMemoryReview } from "./ChatMemoryReview";
import { ReminderDraftReview } from "./ReminderDraftReview";
import { AIAdmissionReview } from "./AIConnection";

const money = (n: number) => `$${(n / 1_000_000).toFixed(6)}`;
export function ChatReply({
  q,
  requests,
  view,
  draftBusy,
  consent,
  setConsent,
  useDraft,
  onReviewStrategy,
}: {
  q: AgentAIRequest;
  requests: AgentAIRequest[];
  view: AgentView;
  draftBusy: boolean;
  consent: string;
  setConsent: (value: string) => void;
  useDraft: (id: string) => Promise<void>;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
}) {
  const { agents } = useWorkspace();
  return (
    <article className="ai-request" key={q.id}>
      <div className="chat-prompt">
        <small>You</small>
        <h4>{q.prompt}</h4>
      </div>
      {q.state === "prepared" && (
        <h5 className="reply-review-title">Review reply</h5>
      )}
      {q.researchTurnId &&
        !requests.some(
          (other) =>
            other.researchTurnId === q.researchTurnId &&
            (other.researchStep ?? 0) > (q.researchStep ?? 0),
        ) && (
          <ResearchTurnReview
            request={q}
            revision={view.revision}
            fixtureAvailable={view.aiFixtureAvailable}
            onChanged={() => agents.refresh()}
          />
        )}
      <div className="response-status">
        <strong>{q.state}</strong>
        <small>Fixture · {money(q.spentMicrousd)}</small>
      </div>
      {q.state === "prepared" && (
        <p className="review-limits">
          Up to {q.limits.maxInputTokens} input / {q.limits.maxOutputTokens}{" "}
          output tokens · {q.limits.deadlineSeconds}s ·{" "}
          {money(q.limits.budgetMicrousd)} fixture budget
        </p>
      )}
      <details>
        <summary>Exact context disclosure</summary>
        <AIAdmissionReview policy={q.policy} admission={q.admission} />
        <p>
          Model: {q.model} · Held {money(q.heldMicrousd)} · Spent{" "}
          {money(q.spentMicrousd)} (fixture only)
        </p>
        <p>
          Limits: {q.limits.maxInputTokens} input / {q.limits.maxOutputTokens}{" "}
          output tokens · {q.limits.deadlineSeconds}s · Budget{" "}
          {money(q.limits.budgetMicrousd)} · No tools
        </p>

        <p>Connection: {q.connectionRef || "no credential"}</p>
        {q.news && (
          <p>
            News snapshot: {q.news.fetchId} · source {q.news.url} · freshness{" "}
            {q.news.maxAgeSeconds}s · content hash {q.news.bodySha256}
          </p>
        )}
        <pre>{q.disclosure}</pre>
        <p>
          Also included: fixed instructions treating this context as untrusted
          data, and the review-only output schema. No other profiles, expired
          notes, credentials or wallet records.
        </p>
        <p className="subtle">Disclosure SHA-256: {q.digest}</p>
      </details>
      {q.failure && <p role="status">{q.failure}</p>}
      {q.state === "prepared" && !q.researchTurnId && (
        <>
          <label className="ai-consent">
            <input
              type="checkbox"
              checked={consent === q.digest}
              onChange={(e) => setConsent(e.target.checked ? q.digest : "")}
            />
            I reviewed this context and these fixture limits
          </label>
          <button
            className="primary"
            disabled={
              agents.busy || consent !== q.digest || !view.aiFixtureAvailable
            }
            onClick={() => {
              void agents
                .change({
                  action: "ai-start",
                  id: q.id,
                  consentDigest: q.digest,
                })
                .then((ok) => {
                  if (ok) setConsent("");
                });
            }}
          >
            Run development fixture
          </button>
          {!view.aiFixtureAvailable && (
            <p className="subtle">
              Execution is disabled in this core. Only an explicitly started
              local development fixture can run.
            </p>
          )}
        </>
      )}
      {["prepared", "queued", "running"].includes(q.state) && (
        <button
          className="secondary"
          disabled={agents.busy}
          onClick={() =>
            void agents.change({
              action: "ai-cancel",
              id: q.id,
            })
          }
        >
          Stop
        </button>
      )}
      {q.answer && (
        <div className="chat-response">
          <small>
            {view.profiles.find((p) => p.id === q.profileId)?.name || "Agent"}
          </small>
          <p className="ai-answer">{q.answer}</p>
        </div>
      )}
      {q.state === "completed" && q.answer && (
        <ChatMemoryReview
          key={`${view.profileId}:${q.id}`}
          view={view}
          sourceId={q.id}
          sourceKind="answer"
          sourceText={q.answer}
          sessionId={q.sessionId}
        />
      )}
      {!q.researchTurnId && (
        <button
          type="button"
          className="secondary"
          disabled={agents.busy || draftBusy}
          onClick={() => void useDraft(q.id)}
        >
          Use as draft
        </button>
      )}
      {q.cards.map((card) => (
        <section
          className="ai-action-card"
          key={card.id}
          aria-label={card.title}
        >
          <small>
            {card.kind} · {card.review} · REVIEW ONLY
          </small>
          <h4>{card.title}</h4>
          <p>{card.text}</p>

          {q.state === "completed" &&
            card.kind === "paper-strategy-draft" &&
            card.review !== "dismissed" &&
            onReviewStrategy && (
              <button
                className="secondary"
                disabled={agents.busy}
                onClick={() => onReviewStrategy(q.id, card.id)}
              >
                Review WEN revision
              </button>
            )}
          {card.kind === "reminder-draft" && card.review !== "dismissed" && (
            <ReminderDraftReview
              key={`${view.profileId}:${card.id}`}
              view={view}
              card={card}
            />
          )}
          {card.review === "unreviewed" && (
            <div className="agent-inline">
              <button
                className="secondary"
                disabled={agents.busy}
                onClick={() =>
                  void agents.change({
                    action: "ai-card-keep",
                    id: card.id,
                  })
                }
              >
                Keep draft
              </button>
              <button
                className="text-button"
                disabled={agents.busy}
                onClick={() =>
                  void agents.change({
                    action: "ai-card-dismiss",
                    id: card.id,
                  })
                }
              >
                Dismiss draft
              </button>
            </div>
          )}
        </section>
      ))}
    </article>
  );
}
