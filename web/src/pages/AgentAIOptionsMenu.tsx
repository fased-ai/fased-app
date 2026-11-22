import type { MutableRefObject, RefObject } from "react";
import { SlidersHorizontal } from "../icons";
import { useUI } from "../ui-preferences";
import { useWorkspace } from "../workspace";
import type {
  AgentView,
  ChatPermission,
  AIMemorySelection,
  AIEvidenceSelection,
  AgentEvidenceView,
} from "../agents";
import type { Connection } from "../ai-connection";
import { AgentEvidenceInspector } from "./AgentEvidence";
import { NewsSourcePicker } from "./NewsInputs";
import type { ResearchSelection } from "./ResearchReadPanel";

export function AgentAIOptionsMenu({
  optionsMenu,
  optionsOpen,
  setOptionsOpen,
  refreshAccess,
  closeOptions,
  view,
  sessionId,
  researchSelection,
  generation,
  setDraftBusy,
  setDraftError,
  setDraftSource,
  setEvidenceSelection,
  setEvidencePreview,
  setConsent,
  questionRef,
  reviewEach,
  setReviewEach,
  currentPermission,
  onSaveComposer,
  hasComposer,
  prompt,
  newsSourceId,
  setNewsSourceId,
  newsAge,
  setNewsAge,
  memoryMode,
  setMemoryMode,
  legacyDraft,
  memoryIds,
  toggleMemory,
  input,
  setInput,
  output,
  setOutput,
  deadline,
  setDeadline,
  planUsage,
  budget,
  setBudget,
  selectedAccess,
  model,
}: {
  optionsMenu: RefObject<HTMLDetailsElement | null>;
  optionsOpen: boolean;
  setOptionsOpen: (open: boolean) => void;
  refreshAccess: () => Promise<unknown>;
  closeOptions: () => void;
  view: AgentView;
  sessionId: string;
  researchSelection?: ResearchSelection;
  generation: MutableRefObject<number>;
  setDraftBusy: (busy: boolean) => void;
  setDraftError: (err: string) => void;
  setDraftSource: (src: string) => void;
  setEvidenceSelection: (sel?: AIEvidenceSelection) => void;
  setEvidencePreview: (preview: AgentEvidenceView | null) => void;
  setConsent: (consent: string) => void;
  questionRef: RefObject<HTMLTextAreaElement | null>;
  reviewEach: boolean;
  setReviewEach: (checked: boolean) => void;
  currentPermission?: ChatPermission;
  hasComposer: boolean;
  onSaveComposer?: () => Promise<void>;
  prompt: string;
  newsSourceId: string;
  setNewsSourceId: (value: string) => void;
  newsAge: number;
  setNewsAge: (value: number) => void;
  memoryMode: AIMemorySelection["mode"] | "legacy";
  setMemoryMode: (mode: AIMemorySelection["mode"] | "legacy") => void;
  legacyDraft: boolean;
  memoryIds: string[];
  toggleMemory: (id: string, checked: boolean) => void;
  input: number;
  setInput: (value: number) => void;
  output: number;
  setOutput: (value: number) => void;
  deadline: number;
  setDeadline: (value: number) => void;
  planUsage: boolean;
  budget: number;
  setBudget: (value: number) => void;
  selectedAccess?: Connection | null;
  model: string;
}) {
  const { t: uiText } = useUI();
  const tr = uiText;
  const { agents } = useWorkspace();

  return (
    <details
      className="message-options"
      ref={optionsMenu}
      open={optionsOpen}
      onToggle={(e) => {
        setOptionsOpen(e.currentTarget.open);
        if (e.currentTarget.open) void refreshAccess();
      }}
    >
      <summary aria-label={tr("Options")} title={tr("Options")}>
        <SlidersHorizontal size={16} />
      </summary>
      <div className="message-options-panel">
        <details className="chat-evidence">
          <summary>{uiText("Sources")}</summary>
          {!researchSelection && (
            <AgentEvidenceInspector
              view={view}
              sessionId={sessionId}
              onUse={(e) => {
                generation.current++;
                setDraftBusy(false);
                setDraftError("");
                setDraftSource("");
                setEvidenceSelection({
                  kind: e.kind,
                  id: e.id,
                  ...(e.itemId ? { itemId: e.itemId } : {}),
                  sha256: e.sha256,
                });
                setEvidencePreview(e);
                setConsent("");
                closeOptions();
                questionRef.current?.focus();
              }}
            />
          )}
        </details>
        <label className="ai-consent">
          <input
            type="checkbox"
            checked={reviewEach}
            onChange={(e) => setReviewEach(e.target.checked)}
          />
          {tr("Review each reply")}
        </label>
        {currentPermission && view.chatReadiness?.[sessionId] === "ready" && (
          <div className="agent-inline">
            <span>
              {tr("{count} replies left", {
                count:
                  currentPermission.maxRequests -
                  currentPermission.usedRequests,
              })}
            </span>
            <button
              type="button"
              className="text-button"
              disabled={agents.busy}
              onClick={() =>
                void agents.change({
                  action: "chat-revoke",
                  id: sessionId,
                  consentDigest: currentPermission.id,
                })
              }
            >
              {tr("Revoke")}
            </button>
          </div>
        )}
        {hasComposer && onSaveComposer && (
          <button
            type="button"
            className="text-button"
            disabled={agents.busy || !prompt.trim()}
            onClick={async () => {
              closeOptions();
              await onSaveComposer();
            }}
          >
            {uiText("Save message")}
          </button>
        )}
        {!researchSelection && (
          <NewsSourcePicker
            view={view}
            value={newsSourceId}
            onChange={setNewsSourceId}
            age={newsAge}
            onAge={setNewsAge}
          />
        )}
        <label>
          {uiText("Memory")}
          <select
            value={memoryMode}
            onChange={(e) =>
              setMemoryMode(
                e.target.value as AIMemorySelection["mode"] | "legacy",
              )
            }
          >
            {legacyDraft && (
              <option value="legacy">{uiText("Legacy active notes")}</option>
            )}
            <option value="relevant">
              {tr("Relevant notes and recent chat")}
            </option>
            <option value="all">{uiText("All active notes")}</option>
            <option value="none">{uiText("None")}</option>
            <option value="selected">{uiText("Selected notes")}</option>
          </select>
        </label>
        {memoryMode === "selected" && (
          <fieldset aria-label={uiText("AI memory selection")}>
            <legend>{uiText("Selected notes")}</legend>
            {view.memories.map((m) => (
              <label key={m.id}>
                <input
                  type="checkbox"
                  checked={memoryIds.includes(m.id)}
                  disabled={m.expired && !memoryIds.includes(m.id)}
                  onChange={(e) => toggleMemory(m.id, e.target.checked)}
                />
                {m.text}
                {m.expired ? uiText(" (expired)") : ""}
              </label>
            ))}
            {memoryIds
              .filter((id) => !view.memories.some((m) => m.id === id))
              .map((id) => (
                <label key={id}>
                  <input
                    type="checkbox"
                    checked
                    onChange={() => toggleMemory(id, false)}
                  />
                  {uiText("Unavailable note: ")}
                  {id}
                </label>
              ))}
          </fieldset>
        )}
        <div className="agent-form-grid">
          <label>
            {uiText("Maximum input tokens")}
            <input
              type="number"
              min={1000}
              max={32000}
              required
              value={input}
              onChange={(e) => setInput(e.target.valueAsNumber)}
            />
          </label>
          <label>
            {uiText("Maximum output tokens")}
            <input
              type="number"
              min={128}
              max={1024}
              required
              value={output}
              onChange={(e) => setOutput(e.target.valueAsNumber)}
            />
          </label>
          <label>
            {uiText("Request deadline (seconds)")}
            <input
              type="number"
              min={1}
              max={120}
              required
              value={deadline}
              onChange={(e) => setDeadline(e.target.valueAsNumber)}
            />
          </label>
          {!planUsage && (
            <label>
              {uiText("Spending limit ($)")}
              <input
                type="number"
                min={0.000001}
                step="0.000001"
                max={
                  (selectedAccess?.catalog?.models.find((m) => m.id === model)
                    ?.policy?.maxRequestBudgetMicrousd || 1000000) / 1000000
                }
                required
                value={budget / 1_000_000}
                onChange={(e) =>
                  setBudget(Math.round(e.target.valueAsNumber * 1_000_000))
                }
              />
            </label>
          )}
        </div>
      </div>
    </details>
  );
}
