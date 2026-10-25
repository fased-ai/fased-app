import { Dialog } from "../components/shared";
import { useUI } from "../ui-preferences";
import { useState } from "react";
import type { AgentView, ChatMemorySource } from "../agents";
import { useWorkspace } from "../workspace";

export function ChatMemoryReview({
  view,
  sourceId,
  sourceKind,
  sourceText,
  sessionId,
}: {
  view: AgentView;
  sourceId: string;
  sourceKind: ChatMemorySource;
  sourceText: string;
  sessionId: string;
}) {
  const { t: uiText } = useUI();
  const { agents, navigate } = useWorkspace();
  const [revision, setRevision] = useState<number>();
  const [saved, setSaved] = useState(false);
  const [text, setText] = useState(sourceText);
  const [expiry, setExpiry] = useState("");
  if (revision === undefined)
    return (
      <>
        {saved && (
          <div className="agent-inline">
            <p role="status">{uiText("Memory saved.")}</p>
            <button
              type="button"
              className="secondary"
              onClick={() => navigate("agents/memory")}
            >
              {uiText("Open Memory")}
            </button>
          </div>
        )}
        <button
          className="text-button"
          disabled={agents.busy}
          onClick={() => {
            setSaved(false);
            setText(sourceText);
            setExpiry("");
            setRevision(view.revision);
          }}
        >
          {uiText("Remember this")}
        </button>
      </>
    );
  const stale = revision !== view.revision;
  return (
    <Dialog title={uiText("Memory")} onClose={() => setRevision(undefined)}>
      <section aria-label={uiText("Chat memory review")}>
        <details>
          <summary>
            {uiText("Retained ")}
            {sourceKind === "message"
              ? uiText("user message")
              : uiText("manual answer")}
          </summary>
          <p>{sourceText}</p>
          <small>
            {uiText("Profile ")}
            {view.profileId}
            {uiText(" · Conversation ")}
            {sessionId}
            {uiText(" · Source")} {sourceId}
            {uiText(". The server records its identity and content hash.")}
          </small>
        </details>
        <form
          className="agent-form"
          onSubmit={(e) => {
            e.preventDefault();
            if (stale) return;
            void agents
              .change({
                action: "memory-chat-save",
                revision,
                id: sourceId,
                source: sourceKind,
                text,
                expiresAt: expiry
                  ? Math.floor(new Date(expiry).getTime() / 1000)
                  : 0,
              })
              .then((ok) => {
                if (ok) {
                  setSaved(true);
                  setRevision(undefined);
                }
              });
          }}
        >
          <label>
            {uiText("Fact")}
            <textarea
              aria-label={uiText("Fact")}
              required
              maxLength={4000}
              value={text}
              onChange={(e) => setText(e.target.value)}
            />
          </label>
          <label>
            {uiText("Memory expiry (optional)")}
            <input
              type="datetime-local"
              value={expiry}
              onChange={(e) => setExpiry(e.target.value)}
            />
          </label>
          {stale && (
            <p role="status">
              {uiText(
                "Workspace changed. Cancel and select the source again before saving.",
              )}
            </p>
          )}
          <div className="agent-inline">
            <button className="primary" disabled={agents.busy || stale}>
              {uiText("Save memory")}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={agents.busy}
              onClick={() => setRevision(undefined)}
            >
              {uiText("Cancel memory")}
            </button>
          </div>
        </form>
      </section>
    </Dialog>
  );
}
