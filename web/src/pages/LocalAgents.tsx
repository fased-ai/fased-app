import { useState } from "react";
import { Plus } from "../icons";
import { SectionHead } from "../components/shared";
import { AgentChatV2 } from "../components/AgentChatV2";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import { AgentMemorySection } from "./AgentMemorySection";
import { AgentProfileSettings } from "./AgentProfileSettings";
import { AgentTasks } from "./AgentTasks";
import { LegacyAgentChat } from "./LegacyAgentChat";
import { NewsInputs } from "./NewsInputs";
import type { AgentView } from "../agents";
import "../agents.css";

const sectionTitles: Record<string, string> = {
  chat: "Chat",
  review: "Chat",
  tasks: "Tasks",
  news: "News",
  memory: "Memory",
  settings: "Agents",
  bots: "Strategies",
};

export function LocalAgents() {
  const { t: uiText } = useUI();
  const core = useCore();
  const { agents, setProfileOpen, tab, setAgentSettingsDrafts } =
    useWorkspace();
  const [taskPanel, setTaskPanel] = useState("automations");

  return (
    <>
      <SectionHead
        title={uiText(sectionTitles[tab] || "Agents")}
        action={
          <div className="agent-header-actions">
            {agents.view && (
              <label className="profile-select">
                <span className="sr-only">{uiText("Agent")}</span>
                <select
                  aria-label={uiText("Agent")}
                  disabled={agents.busy}
                  value={agents.view.profileId}
                  onChange={(e) => void agents.select(e.target.value)}
                >
                  {agents.view.profiles.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <button
              className="icon-button"
              aria-label={uiText("New agent")}
              data-tooltip={uiText("New agent")}
              onClick={() => setProfileOpen(true)}
            >
              <Plus size={17} aria-hidden="true" />
            </button>
          </div>
        }
      />
      {agents.error && (
        <p className="storage-warning" role="alert">
          {agents.error}{" "}
          <button
            className="text-button"
            onClick={() => {
              if (tab === "settings" && agents.view) {
                const key = `${core.instanceId}:${agents.view.profileId}`;
                setAgentSettingsDrafts((drafts) => {
                  if (!drafts.has(key)) return drafts;
                  const next = new Map(drafts);
                  next.delete(key);
                  return next;
                });
              }
              void agents.refresh().catch(() => {});
            }}
          >
            {uiText("Refresh records")}
          </button>
        </p>
      )}
      {!agents.view ? (
        <p role="status">{uiText("Loading local agent records…")}</p>
      ) : (
        <LocalAgentEditor
          key={agents.view.profileId}
          view={agents.view}
          taskPanel={taskPanel}
          setTaskPanel={setTaskPanel}
        />
      )}
    </>
  );
}

function LocalAgentEditor({
  view: v,
  taskPanel,
  setTaskPanel,
}: {
  view: AgentView;
  taskPanel: string;
  setTaskPanel: (value: string) => void;
}) {
  const { tab, navigate } = useWorkspace();
  const [legacySessionId, setLegacySessionId] = useState("");
  const [legacySearch, setLegacySearch] = useState("");
  const [useLegacyChat, setUseLegacyChat] = useState(false);

  return (
    <div className="local-agents">
      {tab === "news" && (
        <NewsInputs
          view={v}
          onDiscuss={() => {
            setLegacySessionId("");
            setLegacySearch("");
            navigate("agents/review");
          }}
        />
      )}
      {tab === "chat" &&
        (useLegacyChat ? (
          <LegacyAgentChat
            view={v}
            session={legacySessionId}
            setSession={setLegacySessionId}
            search={legacySearch}
            setSearch={setLegacySearch}
            onBackToThreads={() => setUseLegacyChat(false)}
          />
        ) : (
          <AgentChatV2
            view={v}
            onOpenLegacySession={(sessionId) => {
              setLegacySessionId(sessionId);
              setUseLegacyChat(true);
            }}
          />
        ))}
      {tab === "review" && (
        <LegacyAgentChat
          view={v}
          session={legacySessionId}
          setSession={setLegacySessionId}
          search={legacySearch}
          setSearch={setLegacySearch}
          onBackToThreads={() => {
            setUseLegacyChat(false);
            navigate("agents/chat");
          }}
        />
      )}
      {tab === "settings" && <AgentProfileSettings view={v} />}
      {tab === "memory" && (
        <AgentMemorySection
          view={v}
          onOpenChatSession={(sessionId) => {
            setLegacySessionId(sessionId);
            setLegacySearch("");
            navigate("agents/review");
          }}
        />
      )}
      {tab === "tasks" && (
        <AgentTasks
          view={v}
          panel={taskPanel}
          setPanel={setTaskPanel}
          onDiscuss={() => {
            setLegacySessionId("");
            setLegacySearch("");
            navigate("agents/review");
          }}
        />
      )}
    </div>
  );
}
