import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Plus, Search, Trash2 } from "../icons";
import { Dialog } from "../components/shared";
import { useUI } from "../ui-preferences";
import { useCore } from "../core";
import { useWorkspace } from "../workspace";
import { MemoryRecall, memoryEvidenceStatuses } from "./MemoryRecall";
import { TaskHistory } from "./TaskHistory";
import type {
  AgentMemory,
  AgentMemoryRestoreDraft,
  AgentView,
} from "../agents";
import {
  deleteMemoryV2,
  searchMemoriesV2,
  type AgentV2Memory,
} from "../agents-v2";

const formatDate = (at: number) =>
  at
    ? new Date(at * 1000).toLocaleString(
        typeof document === "undefined" ? "en" : document.documentElement.lang,
      )
    : "No expiry";

export function AgentMemorySection({
  view: v,
  onOpenChatSession,
}: {
  view: AgentView;
  onOpenChatSession: (sessionId: string) => void;
}) {
  const { t: uiText } = useUI();
  const t = uiText;
  const core = useCore();
  const { agents } = useWorkspace();

  const [memoryOpen, setMemoryOpen] = useState(false);
  const [memoryID, setMemoryID] = useState("");
  const [memory, setMemory] = useState("");
  const [source, setSource] = useState("");
  const [memoryRestore, setMemoryRestore] =
    useState<AgentMemoryRestoreDraft | null>(null);
  const memoryRevision = useRef(v.revision);
  const memoryExpiryOriginal = useRef(0);

  const [archiveOpen, setArchiveOpen] = useState(false);
  const [archiveKind, setArchiveKind] = useState<"conversations" | "memories">(
    "memories",
  );
  const [archiveSearch, setArchiveSearch] = useState("");
  const [archiveSource, setArchiveSource] = useState("");

  const [ftsQuery, setFtsQuery] = useState("");
  const [v2Memories, setV2Memories] = useState<AgentV2Memory[]>([]);
  const [v2Error, setV2Error] = useState("");

  const loadV2Memories = useCallback(
    async (q = ftsQuery) => {
      if (core.mode !== "ready") return;
      try {
        const res = await searchMemoriesV2(core, {
          profileId: v.profileId,
          q,
          limit: 30,
        });
        setV2Memories(res.memories ?? []);
        setV2Error("");
      } catch (e) {
        setV2Error(e instanceof Error ? e.message : "Memory search failed");
      }
    },
    [core, ftsQuery, v.profileId],
  );

  useEffect(() => {
    void loadV2Memories("");
  }, [core.mode, core.instanceId, v.profileId]);

  const v2OwnedMemories = v2Memories.filter(
    (item) => !item.id.startsWith("legacy-"),
  );

  function resetMemory() {
    setMemoryOpen(false);
    setMemoryID("");
    setMemory("");
    setSource("");
    setMemoryRestore(null);
    memoryExpiryOriginal.current = 0;
  }

  function editMemory(
    m: Pick<AgentMemory, "id" | "text" | "source" | "expiresAt">,
    revision: number,
    restoration: AgentMemoryRestoreDraft | null = null,
  ) {
    setMemoryOpen(true);
    memoryRevision.current = revision;
    setMemoryID(m.id);
    setMemory(m.text);
    setSource(m.source);
    setMemoryRestore(restoration);
    memoryExpiryOriginal.current = m.expiresAt;
  }

  function submit(fn: () => Promise<void>) {
    return (e: FormEvent) => {
      e.preventDefault();
      void fn();
    };
  }

  return (
    <>
      {archiveOpen && (
        <Dialog
          title={t(
            archiveKind === "memories"
              ? "Memory history"
              : "Archived conversations",
          )}
          wide
          onClose={() => setArchiveOpen(false)}
        >
          <TaskHistory
            key={v.profileId}
            view={v}
            kind={archiveKind}
            initialSearch={archiveSearch}
            initialSource={archiveSource}
            onMemorySelected={(m) => {
              setArchiveOpen(false);
              editMemory(
                {
                  ...m,
                  id: v.memories.some((note) => note.id === m.id) ? m.id : "",
                },
                v.revision,
              );
            }}
          />
        </Dialog>
      )}
      <section className="panel">
        <div className="panel-heading">
          <h2>{uiText("Memory")}</h2>
          <button
            type="button"
            className="icon-button"
            aria-label={t("Remember")}
            title={t("Remember")}
            onClick={() => {
              resetMemory();
              setMemoryOpen(true);
            }}
          >
            <Plus size={17} />
          </button>
        </div>
        <div className="agent-record-body memory-workspace">
          {v2OwnedMemories.length > 0 || ftsQuery.trim() !== "" ? (
            <section
              className="memory-library"
              aria-label={uiText("Workspace index")}
            >
              <form
                className="agent-inline"
                onSubmit={(e) => {
                  e.preventDefault();
                  void loadV2Memories(ftsQuery);
                }}
              >
                <label>
                  <span className="sr-only">
                    {uiText("Search saved drafts")}
                  </span>
                  <input
                    type="search"
                    aria-label={uiText("Search saved drafts")}
                    placeholder={uiText("Search memories and drafts")}
                    value={ftsQuery}
                    onChange={(e) => setFtsQuery(e.target.value)}
                  />
                </label>
                <button type="submit" className="secondary">
                  <Search size={14} />
                  {uiText("Search")}
                </button>
              </form>
              {v2Error && <p role="alert">{v2Error}</p>}
              {v2OwnedMemories.map((item) => (
                <article className="memory-card" key={item.id}>
                  <div>
                    <strong>{item.title || item.scope}</strong>{" "}
                    <span className="v2-pill">{item.scope}</span>
                    {item.pinned && (
                      <span className="v2-pill">{uiText("Pinned")}</span>
                    )}
                    <p>{item.content}</p>
                  </div>
                  <div className="agent-inline">
                    <button
                      type="button"
                      className="icon-button"
                      aria-label={`${t("Delete")} ${item.title || item.id}`}
                      onClick={() =>
                        void deleteMemoryV2(core, item.id).then(() =>
                          loadV2Memories(ftsQuery),
                        )
                      }
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                </article>
              ))}
            </section>
          ) : null}

          <div className="memory-library">
            <MemoryRecall
              view={v}
              onOlder={(m) => {
                setArchiveKind("memories");
                setArchiveSource("");
                setArchiveSearch(m.id);
                setArchiveOpen(true);
              }}
              onOpen={(src) => {
                if (v.sessions.some((s) => s.id === src.sessionId)) {
                  onOpenChatSession(src.sessionId!);
                } else {
                  setArchiveKind("conversations");
                  setArchiveSource(src.sessionId!);
                  setArchiveSearch("");
                  setArchiveOpen(true);
                }
              }}
              onCorrect={(m) => editMemory(m, v.revision)}
              onRestore={(draft) =>
                editMemory(
                  { id: draft.memoryId, ...draft.selected },
                  draft.revision,
                  draft,
                )
              }
            />
          </div>

          {memoryOpen && (
            <Dialog
              title={
                memoryRestore
                  ? t("Restore a memory")
                  : memoryID
                    ? t("Edit")
                    : t("Remember")
              }
              onClose={resetMemory}
            >
              <form
                aria-label={uiText("Memory editor")}
                className="memory-editor"
                onSubmit={submit(async () => {
                  if (
                    await agents.change({
                      action: "memory-put",
                      revision: memoryID ? memoryRevision.current : undefined,
                      id: memoryID || undefined,
                      text: memory,
                      source: source.trim() || "Owner note",
                      expiresAt: memoryID ? memoryExpiryOriginal.current : 0,
                    })
                  ) {
                    resetMemory();
                    void loadV2Memories(ftsQuery);
                  }
                })}
              >
                {agents.error && <p role="alert">{agents.error}</p>}
                {memoryRestore && (
                  <section aria-label={uiText("Memory restoration review")}>
                    <article aria-label={uiText("Current memory revision")}>
                      <h4>{uiText("Current")}</h4>
                      <p>{memoryRestore.current.text}</p>
                      <small>
                        {memoryRestore.current.source}
                        {uiText(" · Updated")}{" "}
                        {formatDate(memoryRestore.current.at)}
                        {uiText(" · Expiry")}{" "}
                        {uiText(formatDate(memoryRestore.current.expiresAt))}
                      </small>
                    </article>
                    <article aria-label={uiText("Selected memory revision")}>
                      <h4>
                        {uiText("Selected revision ")}
                        {memoryRestore.historyIndex + 1}
                      </h4>
                      <p>{memoryRestore.selected.text}</p>
                      <small>
                        {memoryRestore.selected.source}
                        {uiText(" · Updated")}{" "}
                        {formatDate(memoryRestore.selected.at)}
                        {uiText(" · Expiry")}{" "}
                        {uiText(formatDate(memoryRestore.selected.expiresAt))}
                      </small>
                    </article>
                    {memoryRestore.selected.expiresAt > 0 &&
                      memoryRestore.selected.expiresAt <= Date.now() / 1000 && (
                        <p role="status">{uiText("Expired revision")}</p>
                      )}
                    <section aria-label={uiText("Restoration source evidence")}>
                      <h4>{uiText("Recorded source")}</h4>
                      <p>
                        {uiText(
                          memoryEvidenceStatuses[memoryRestore.evidence.status],
                        )}
                      </p>
                      {memoryRestore.evidence.text && (
                        <blockquote>{memoryRestore.evidence.text}</blockquote>
                      )}
                    </section>
                    {memoryRestore.revision !== v.revision && (
                      <p role="status">
                        {uiText(
                          "Workspace changed. Review the restoration again.",
                        )}
                      </p>
                    )}
                  </section>
                )}
                <label>
                  {t("Note")}
                  <textarea
                    aria-label={t("Note")}
                    required
                    maxLength={4000}
                    value={memory}
                    onChange={(e) => setMemory(e.target.value)}
                  />
                </label>
                <details className="task-inspection">
                  <summary>{t("Details")}</summary>
                  <label>
                    {uiText("Evidence or source")}
                    <input
                      maxLength={300}
                      value={source}
                      onChange={(e) => setSource(e.target.value)}
                      placeholder={uiText(
                        "e.g. Owner preference, reviewed today",
                      )}
                    />
                  </label>
                </details>
                <div className="agent-inline">
                  <button
                    className="primary"
                    disabled={
                      agents.busy ||
                      !!(memoryRestore && memoryRestore.revision !== v.revision)
                    }
                  >
                    {memoryRestore
                      ? t("Save restored memory")
                      : memoryID
                        ? t("Save correction")
                        : t("Save memory")}
                  </button>
                  <button
                    type="button"
                    className="secondary"
                    onClick={resetMemory}
                  >
                    {memoryRestore
                      ? uiText("Cancel restoration")
                      : memoryID
                        ? uiText("Cancel correction")
                        : uiText("Cancel memory edit")}
                  </button>
                </div>
              </form>
            </Dialog>
          )}
        </div>
      </section>
    </>
  );
}
