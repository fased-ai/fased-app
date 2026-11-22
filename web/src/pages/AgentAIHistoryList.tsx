import type { MutableRefObject, RefObject } from "react";
import { useUI } from "../ui-preferences";
import type { AgentView } from "../agents";
import { ChatReply } from "./ChatReply";
import { NewsWatchMessage } from "./NewsWatchMessage";
import { ChatMemoryReview } from "./ChatMemoryReview";
import { reminderRun, ReminderRunMessage } from "./ReminderRunMessage";

export function AgentAIHistoryList({
  view,
  sessionId,
  requests,
  messagesRef,
  followReplies,
  draftBusy,
  consent,
  setConsent,
  useDraft,
  onReviewStrategy,
}: {
  view: AgentView;
  sessionId: string;
  requests: AgentView["aiRequests"];
  messagesRef: RefObject<HTMLDivElement | null>;
  followReplies: MutableRefObject<boolean>;
  draftBusy: boolean;
  consent: string;
  setConsent: (value: string) => void;
  useDraft: (id: string) => Promise<void>;
  onReviewStrategy?: (requestId: string, cardId: string) => void;
}) {
  const { t: uiText } = useUI();
  return (
    <div
      className="local-messages"
      aria-live="polite"
      ref={messagesRef}
      onScroll={(e) => {
        const el = e.currentTarget;
        followReplies.current =
          el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      }}
    >
      {[
        ...view.messages
          .filter((m) => m.sessionId === sessionId)
          .map((message) => ({
            id: message.id,
            at: message.createdAt,
            message,
            request: undefined,
          })),
        ...requests.map((request) => ({
          id: request.id,
          at: request.createdAt,
          message: undefined,
          request,
        })),
      ]
        .sort((a, b) => a.at - b.at || Number(!a.message) - Number(!b.message))
        .map((entry) =>
          entry.message ? (
            <article
              className={`chat-message ${entry.message.role === "system" ? "chat-system" : ""}`}
              key={entry.id}
            >
              {entry.message.role === "system" ? (
                <details>
                  <summary>{uiText("Saved locally")}</summary>
                  <NewsWatchMessage text={entry.message.text} />
                </details>
              ) : (
                <>
                  <small>
                    {entry.message.role === "user"
                      ? uiText("You")
                      : uiText("Record")}{" "}
                    · {new Date(entry.at * 1000).toLocaleString()}
                  </small>
                  {reminderRun(entry.message, view.profileId) ? (
                    <ReminderRunMessage
                      result={reminderRun(entry.message, view.profileId)!}
                      source={entry.message.text}
                    />
                  ) : (
                    <NewsWatchMessage text={entry.message.text} />
                  )}
                </>
              )}
              {entry.message.role === "user" && (
                <ChatMemoryReview
                  view={view}
                  sourceKind="message"
                  sourceId={entry.message.id}
                  sourceText={entry.message.text}
                  sessionId={sessionId}
                />
              )}
            </article>
          ) : (
            <ChatReply
              key={entry.id}
              q={entry.request!}
              requests={requests}
              view={view}
              draftBusy={draftBusy}
              consent={consent}
              setConsent={setConsent}
              useDraft={useDraft}
              onReviewStrategy={onReviewStrategy}
            />
          ),
        )}
    </div>
  );
}
