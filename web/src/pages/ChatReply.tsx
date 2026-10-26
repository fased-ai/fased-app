import { useUI } from "../ui-preferences";
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
  const { t: uiText } = useUI();
  const { agents } = useWorkspace();
  return (
    <article className="ai-request" key={q.id}>
      <div className="chat-prompt">
        <small>{uiText("You")}</small>
        <h4>{q.prompt}</h4>
      </div>
      {q.state === "prepared" && (
        <h5 className="reply-review-title">{uiText("Review reply")}</h5>
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
            fixtureAvailable={
              view.aiExecutionAvailable ?? view.aiFixtureAvailable
            }
            onChanged={() => agents.refresh()}
          />
        )}
      <div
        className="response-status"
        hidden={!!q.chatPermissionId && q.state === "completed"}
      >
        <strong>{uiText(q.state)}</strong>
        <small>
          {uiText("Demo · ")}
          {money(q.spentMicrousd)}
        </small>
      </div>
      {q.state === "prepared" && (
        <p className="review-limits">
          {uiText("Up to ")}
          {q.limits.maxInputTokens}
          {uiText(" input / ")}
          {q.limits.maxOutputTokens} {uiText("output tokens · ")}
          {q.limits.deadlineSeconds}
          {uiText("s ·")} {money(q.limits.budgetMicrousd)}
          {uiText(" fixture budget")}
        </p>
      )}
      <details>
        <summary>{uiText("Details")}</summary>
        <AIAdmissionReview policy={q.policy} admission={q.admission} />
        <p>
          {uiText("Model: ")}
          {q.model}
          {uiText(" · Held ")}
          {money(q.heldMicrousd)}
          {uiText(" · Spent")} {money(q.spentMicrousd)}
          {uiText(" (fixture only)")}
        </p>
        <p>
          {uiText("Limits: ")}
          {q.limits.maxInputTokens}
          {uiText(" input / ")}
          {q.limits.maxOutputTokens} {uiText("output tokens · ")}
          {q.limits.deadlineSeconds}
          {uiText("s · Budget")} {money(q.limits.budgetMicrousd)}
          {uiText(" · No tools")}
        </p>

        <p>
          {uiText("Connection: ")}
          {q.connectionRef || uiText("no credential")}
        </p>
        {q.news && (
          <p>
            {uiText("News snapshot: ")}
            {q.news.fetchId}
            {uiText(" · source ")}
            {q.news.url}
            {uiText(" · freshness")} {q.news.maxAgeSeconds}
            {uiText("s · content hash ")}
            {q.news.bodySha256}
          </p>
        )}
        <pre>{q.disclosure}</pre>
        <p>
          {uiText(
            "Also included: fixed instructions treating this context as untrusted data, and the review-only output schema. No other profiles, expired notes, credentials or wallet records.",
          )}
        </p>
        <p className="subtle">
          {uiText("Disclosure SHA-256: ")}
          {q.digest}
        </p>
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
            {uiText("I reviewed this context and these fixture limits")}
          </label>
          <button
            className="primary"
            disabled={
              agents.busy ||
              consent !== q.digest ||
              !(view.aiExecutionAvailable ?? view.aiFixtureAvailable)
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
            {uiText("Run development fixture")}
          </button>
          {!(view.aiExecutionAvailable ?? view.aiFixtureAvailable) && (
            <p className="subtle">
              {uiText(
                "Execution is disabled in this core. Only an explicitly started local development fixture can run.",
              )}
            </p>
          )}
        </>
      )}
      {!q.chatPermissionId &&
        ["prepared", "queued", "running"].includes(q.state) && (
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
            {uiText("Stop")}
          </button>
        )}
      {(q.answer || q.partialAnswer) && (
        <div className="chat-response">
          <small>
            {view.profiles.find((p) => p.id === q.profileId)?.name ||
              uiText("Agent")}
          </small>
          <p className="ai-answer">{q.answer || q.partialAnswer}</p>
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
          {uiText("Use as draft")}
        </button>
      )}
      {q.cards.map((card) => (
        <section
          className="ai-action-card"
          key={card.id}
          aria-label={card.title}
        >
          <small>
            {uiText(card.kind === "reminder-draft" ? "Task" : "Proposal")} ·{" "}
            {uiText(
              card.review === "kept"
                ? "Saved"
                : card.review === "dismissed"
                  ? "Dismissed"
                  : "Draft",
            )}
            {card.kind !== "reminder-draft" && uiText(" · REVIEW ONLY")}
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
                {uiText("Review WEN revision")}
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
                {uiText("Keep draft")}
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
                {uiText("Dismiss draft")}
              </button>
            </div>
          )}
        </section>
      ))}
    </article>
  );
}
