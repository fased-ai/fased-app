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
  const { agents } = useWorkspace();
  const [revision, setRevision] = useState<number>();
  const [text, setText] = useState(sourceText);
  const [expiry, setExpiry] = useState("");
  if (revision === undefined)
    return (
      <button
        className="text-button"
        disabled={agents.busy}
        onClick={() => {
          setText(sourceText);
          setExpiry("");
          setRevision(view.revision);
        }}
      >
        Remember this
      </button>
    );
  const stale = revision !== view.revision;
  return (
    <section aria-label="Chat memory review">
      <h4>Review a profile memory</h4>
      <p>
        This saves your edited note for future disclosed context. It does not
        verify the source, train a model or grant permissions.
      </p>
      <details open>
        <summary>
          Retained {sourceKind === "message" ? "user message" : "manual answer"}
        </summary>
        <p>{sourceText}</p>
        <small>
          Profile {view.profileId} · Conversation {sessionId} · Source{" "}
          {sourceId}. The server records its identity and content hash.
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
              if (ok) setRevision(undefined);
            });
        }}
      >
        <label>
          Reviewed memory note
          <textarea
            aria-label="Reviewed memory note"
            required
            maxLength={4000}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </label>
        <label>
          Memory expiry (optional)
          <input
            type="datetime-local"
            value={expiry}
            onChange={(e) => setExpiry(e.target.value)}
          />
        </label>
        {stale && (
          <p role="status">
            Workspace changed. Cancel and select the source again before saving.
          </p>
        )}
        <div className="agent-inline">
          <button className="primary" disabled={agents.busy || stale}>
            Save reviewed memory
          </button>
          <button
            type="button"
            className="secondary"
            disabled={agents.busy}
            onClick={() => setRevision(undefined)}
          >
            Cancel memory
          </button>
        </div>
      </form>
    </section>
  );
}
