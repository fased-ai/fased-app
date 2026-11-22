import { useUI } from "../ui-preferences";
import type { AIEvidenceSelection, AgentEvidenceView } from "../agents";
import type { ResearchSelection } from "./ResearchReadPanel";

export function AgentAIContextFields({
  researchSelection,
  researchMode,
  setResearchMode,
  researchPreparing,
  onClearResearch,
  evidenceSelection,
  evidenceAvailable,
  evidencePreview,
  removeEvidence,
  modelAttempts,
  setModelAttempts,
  toolAttempts,
  setToolAttempts,
  turnSeconds,
  setTurnSeconds,
  planUsage,
  turnBudget,
  setTurnBudget,
}: {
  researchSelection?: ResearchSelection;
  researchMode: string;
  setResearchMode: (value: string) => void;
  researchPreparing: boolean;
  onClearResearch?: () => void;
  evidenceSelection?: AIEvidenceSelection;
  evidenceAvailable: boolean;
  evidencePreview: AgentEvidenceView | null;
  removeEvidence: () => void;
  modelAttempts: number;
  setModelAttempts: (value: number) => void;
  toolAttempts: number;
  setToolAttempts: (value: number) => void;
  turnSeconds: number;
  setTurnSeconds: (value: number) => void;
  planUsage: boolean;
  turnBudget: number;
  setTurnBudget: (value: number) => void;
}) {
  const { t: uiText } = useUI();
  return (
    <>
      {researchSelection && (
        <details className="composer-context">
          <summary>{uiText("Selected research")}</summary>
          <fieldset aria-label={uiText("Selected research for AI")}>
            <legend>{uiText("Selected research")}</legend>
            <label>
              {uiText("Research mode")}
              <select
                value={researchMode}
                disabled={researchPreparing}
                onChange={(e) => setResearchMode(e.target.value)}
              >
                <option value="single">{uiText("Single request")}</option>
                <option value="loop">{uiText("Reviewed continuations")}</option>
              </select>
            </label>
            <pre>{JSON.stringify(researchSelection, null, 2)}</pre>
            <button
              type="button"
              className="secondary"
              disabled={researchPreparing}
              onClick={onClearResearch}
            >
              {uiText("Remove research")}
            </button>
          </fieldset>
        </details>
      )}
      {evidenceSelection && (
        <details className="composer-context">
          <summary>{uiText("Selected evidence")}</summary>
          <fieldset aria-label={uiText("AI evidence selection")}>
            <legend>{uiText("Selected evidence")}</legend>
            <pre>{JSON.stringify(evidenceSelection, null, 2)}</pre>
            {!evidenceAvailable && (
              <p role="status">
                {uiText("Selected evidence unavailable. Inspect again.")}
              </p>
            )}
            {evidencePreview && (
              <details>
                <summary>{uiText("Inspected record")}</summary>
                <pre>{JSON.stringify(evidencePreview, null, 2)}</pre>
              </details>
            )}
            <button
              type="button"
              className="secondary"
              onClick={removeEvidence}
            >
              {uiText("Remove evidence")}
            </button>
          </fieldset>
        </details>
      )}
      {researchSelection && (
        <details className="composer-context">
          <summary>{uiText("Limits")}</summary>
          <fieldset
            className="agent-form-grid"
            aria-label={uiText("Research turn limits")}
          >
            <legend>{uiText("Research turn limits")}</legend>
            <label>
              {uiText("Model attempts")}
              <input
                type="number"
                min={1}
                max={3}
                required
                value={modelAttempts}
                onChange={(e) => setModelAttempts(e.target.valueAsNumber)}
              />
            </label>
            <label>
              {uiText("Read attempts")}
              <input
                type="number"
                min={1}
                max={8}
                required
                value={toolAttempts}
                onChange={(e) => setToolAttempts(e.target.valueAsNumber)}
              />
            </label>
            <label>
              {uiText("Research deadline (seconds)")}
              <input
                type="number"
                min={1}
                max={90}
                required
                value={turnSeconds}
                onChange={(e) => setTurnSeconds(e.target.valueAsNumber)}
              />
            </label>
            {!planUsage && (
              <label>
                {uiText("Turn budget (micro USD)")}
                <input
                  type="number"
                  min={1}
                  max={3000000}
                  required
                  value={turnBudget}
                  onChange={(e) => setTurnBudget(e.target.valueAsNumber)}
                />
              </label>
            )}
          </fieldset>
        </details>
      )}
    </>
  );
}
