import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Pencil, Pin, Plus, Search, Trash2 } from "../icons";
import { useCore } from "../core";
import { useUI } from "../ui-preferences";
import {
  createMemoryV2,
  deleteMemoryV2,
  searchMemoriesV2,
  updateMemoryV2,
  type AgentV2Memory,
} from "../agents-v2";

export function AgentRulesMemoryPanel({ profileId }: { profileId: string }) {
  const { t } = useUI();
  const core = useCore();

  const [items, setItems] = useState<AgentV2Memory[]>([]);
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const [editorOpen, setEditorOpen] = useState(false);
  const [editingId, setEditingId] = useState("");
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const [pinned, setPinned] = useState(true);

  const loadRules = useCallback(
    async (q = query) => {
      if (core.mode !== "ready") return;
      try {
        const res = await searchMemoriesV2(core, {
          profileId,
          q: q.trim() || undefined,
          limit: 50,
          excludeDrafts: true,
        });
        setItems(res.memories ?? []);
        setError("");
      } catch (e) {
        setError(e instanceof Error ? e.message : "Memory unavailable");
      }
    },
    [core, profileId, query],
  );

  useEffect(() => {
    setQuery("");
    setEditorOpen(false);
    setEditingId("");
    setTitle("");
    setContent("");
    setPinned(true);
    void loadRules("");
  }, [core.mode, core.instanceId, profileId]);

  function openNew() {
    setEditingId("");
    setTitle("");
    setContent("");
    setPinned(true);
    setError("");
    setEditorOpen(true);
  }

  function openEdit(item: AgentV2Memory) {
    setEditingId(item.id);
    setTitle(item.title || "");
    setContent(item.content);
    setPinned(item.pinned);
    setError("");
    setEditorOpen(true);
  }

  function closeEditor() {
    setEditorOpen(false);
    setEditingId("");
    setTitle("");
    setContent("");
    setPinned(true);
  }

  async function handleSave(e: FormEvent) {
    e.preventDefault();
    const trimmedContent = content.trim();
    if (!trimmedContent || busy || core.mode !== "ready") return;
    setBusy(true);
    setError("");
    try {
      if (editingId) {
        await updateMemoryV2(core, editingId, {
          title: title.trim() || undefined,
          content: trimmedContent,
          pinned,
        });
      } else {
        await createMemoryV2(core, {
          profileId,
          scope: "workspace",
          title: title.trim() || undefined,
          content: trimmedContent,
          pinned,
        });
      }
      closeEditor();
      await loadRules(query);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not save rule");
    } finally {
      setBusy(false);
    }
  }

  async function togglePin(item: AgentV2Memory) {
    if (busy || core.mode !== "ready" || item.id.startsWith("legacy-")) return;
    setBusy(true);
    setError("");
    try {
      await updateMemoryV2(core, item.id, { pinned: !item.pinned });
      await loadRules(query);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not update rule");
    } finally {
      setBusy(false);
    }
  }

  async function handleDelete(item: AgentV2Memory) {
    if (busy || core.mode !== "ready" || item.id.startsWith("legacy-")) return;
    setBusy(true);
    setError("");
    try {
      await deleteMemoryV2(core, item.id);
      if (editingId === item.id) closeEditor();
      await loadRules(query);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not delete rule");
    } finally {
      setBusy(false);
    }
  }

  const showSearch = items.length > 3 || query.trim() !== "";

  return (
    <section className="panel" aria-label={t("Rules & Memory")}>
      <div className="panel-heading">
        <h2>{t("Rules & Memory")}</h2>
        <button
          type="button"
          className="secondary"
          disabled={busy}
          onClick={openNew}
        >
          <Plus size={16} aria-hidden="true" />
          {t("Add rule")}
        </button>
      </div>
      <div className="agent-record-body">
        {error && <p role="alert">{error}</p>}

        {editorOpen && (
          <form
            className="rules-memory-form"
            aria-label={t(editingId ? "Edit" : "Add rule")}
            onSubmit={(e) => void handleSave(e)}
          >
            <label>
              {t("Title")}
              <input
                type="text"
                maxLength={80}
                value={title}
                disabled={busy}
                onChange={(e) => setTitle(e.target.value)}
              />
            </label>
            <label>
              {t("Rule or fact")}
              <textarea
                aria-label={t("Rule or fact")}
                required
                maxLength={16000}
                value={content}
                disabled={busy}
                onChange={(e) => setContent(e.target.value)}
              />
            </label>
            <label className="rules-memory-pin-toggle">
              <input
                type="checkbox"
                checked={pinned}
                disabled={busy}
                onChange={(e) => setPinned(e.target.checked)}
              />
              <span>{t("Always active")}</span>
            </label>
            <div className="agent-inline">
              <button type="submit" className="primary" disabled={busy}>
                {t("Save")}
              </button>
              <button
                type="button"
                className="secondary"
                disabled={busy}
                onClick={closeEditor}
              >
                {t("Cancel")}
              </button>
            </div>
          </form>
        )}

        {showSearch && (
          <form
            className="agent-inline rules-memory-search"
            onSubmit={(e) => {
              e.preventDefault();
              void loadRules(query);
            }}
          >
            <label>
              <span className="sr-only">{t("Search")}</span>
              <input
                type="search"
                aria-label={t("Search")}
                placeholder={t("Search")}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <button type="submit" className="secondary" disabled={busy}>
              <Search size={14} aria-hidden="true" />
              {t("Search")}
            </button>
          </form>
        )}

        {items.length === 0 ? (
          <p className="empty">{t("No rules saved")}</p>
        ) : (
          <div className="rules-memory-list">
            {items.map((item) => {
              const isLegacy = item.id.startsWith("legacy-");
              const label = item.title || item.scope;
              return (
                <article className="rules-memory-row" key={item.id}>
                  <div>
                    <div className="rules-memory-head">
                      <strong>{label}</strong>
                      {item.pinned && (
                        <span className="v2-pill">{t("Pinned")}</span>
                      )}
                    </div>
                    <p>{item.content}</p>
                  </div>
                  {!isLegacy && (
                    <div className="rules-memory-actions">
                      <button
                        type="button"
                        className={`icon-button ${item.pinned ? "is-pinned" : ""}`}
                        aria-label={`${t(item.pinned ? "Unpin" : "Pin")} ${label}`}
                        title={t(item.pinned ? "Unpin" : "Pin")}
                        disabled={busy}
                        onClick={() => void togglePin(item)}
                      >
                        <Pin size={14} aria-hidden="true" />
                      </button>
                      <button
                        type="button"
                        className="icon-button"
                        aria-label={`${t("Edit")} ${label}`}
                        title={t("Edit")}
                        disabled={busy}
                        onClick={() => openEdit(item)}
                      >
                        <Pencil size={14} aria-hidden="true" />
                      </button>
                      <button
                        type="button"
                        className="icon-button"
                        aria-label={`${t("Delete")} ${label}`}
                        title={t("Delete")}
                        disabled={busy}
                        onClick={() => void handleDelete(item)}
                      >
                        <Trash2 size={14} aria-hidden="true" />
                      </button>
                    </div>
                  )}
                </article>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}
