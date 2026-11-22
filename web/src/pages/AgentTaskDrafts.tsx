import { useEffect, useState } from "react";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import {
  parseTaskDraftMemory,
  type AgentV2Memory,
  type ParsedTaskDraft,
} from "../agents-v2";
import { useWorkspace } from "../workspace";
import { formatInterval } from "./TaskComposer";

export function AgentTaskDrafts({
  profileId,
  refreshKey,
  onSchedule,
}: {
  profileId: string;
  refreshKey?: number;
  onSchedule: (draft: ParsedTaskDraft) => void;
}) {
  const { t: uiText } = useUI();
  const core = useCore();
  const { launchChatPrompt } = useWorkspace();
  const [drafts, setDrafts] = useState<ParsedTaskDraft[]>([]);
  const [busyId, setBusyId] = useState("");

  useEffect(() => {
    let active = true;
    if (core.mode !== "ready") {
      setDrafts([]);
      return;
    }
    const url = `agents/v2/memories?profileId=${encodeURIComponent(profileId)}&scope=task_draft&limit=20`;
    void core
      .request<{ memories?: AgentV2Memory[] }>(url)
      .then((res) => {
        if (!active) return;
        const parsed = (res.memories ?? [])
          .map(parseTaskDraftMemory)
          .filter((d): d is ParsedTaskDraft => d !== null);
        setDrafts(parsed);
      })
      .catch(() => {
        if (active) setDrafts([]);
      });
    return () => {
      active = false;
    };
  }, [core, core.mode, core.instanceId, profileId, refreshKey]);

  if (drafts.length === 0) return null;

  async function dismissDraft(id: string) {
    setBusyId(id);
    try {
      await core.request(
        `agents/v2/memories/${encodeURIComponent(id)}`,
        "DELETE",
      );
      setDrafts((cur) => cur.filter((d) => d.id !== id));
    } finally {
      setBusyId("");
    }
  }

  return (
    <section className="panel" aria-label={uiText("Agent task drafts")}>
      <div className="panel-heading">
        <h2>{uiText("Agent task drafts")}</h2>
        <span>{drafts.length}</span>
      </div>
      <div className="agent-record-body">
        {drafts.map((d) => (
          <article className="memory-card" key={d.id} aria-label={d.name}>
            <header className="task-result-heading">
              <strong>{d.name}</strong>
              <small>{formatInterval(d.intervalSeconds)}</small>
            </header>
            <p className="task-result-text">{d.description}</p>
            <div className="row-actions">
              <button
                type="button"
                className="primary"
                disabled={busyId === d.id}
                onClick={() => onSchedule(d)}
              >
                {uiText("Schedule")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busyId === d.id}
                onClick={() =>
                  launchChatPrompt({
                    profileId,
                    title: d.name,
                    prompt: d.description,
                  })
                }
              >
                {uiText("Run in Chat")}
              </button>
              <button
                type="button"
                className="text-button"
                disabled={busyId === d.id}
                onClick={() => void dismissDraft(d.id)}
              >
                {uiText("Dismiss")}
              </button>
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}
