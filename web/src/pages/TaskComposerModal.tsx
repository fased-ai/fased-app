import { useState, type FormEvent } from "react";
import { Dialog } from "../components/shared";
import { Plus, Trash2 } from "../icons";
import type {
  ApprovalPolicy,
  EdgeCondition,
  ExecMode,
  GoalDomain,
  ScheduleKind,
  SwarmChildSpec,
  SwarmRequest,
  TaskLibraries,
  UniversalTask,
} from "../tasks-v2";
import { useUI } from "../ui-preferences";
import type { WalletV2 } from "../wallets-v2";

export type ComposerMode = "single" | "swarm";

export function TaskComposerModal({
  initialMode = "single",
  libraries,
  internalWallets = [],
  onClose,
  onCreateTask,
  onCreateSwarm,
}: {
  initialMode?: ComposerMode;
  libraries: TaskLibraries | null;
  internalWallets?: WalletV2[];
  onClose: () => void;
  onCreateTask: (input: Partial<UniversalTask>) => Promise<void>;
  onCreateSwarm: (input: SwarmRequest) => Promise<void>;
}) {
  const { t } = useUI();
  const [composerMode, setComposerMode] = useState<ComposerMode>(initialMode);
  const [name, setName] = useState("");
  const [goalDomain, setGoalDomain] = useState<GoalDomain>("mining");
  const [goalObjective, setGoalObjective] = useState("");
  const [scheduleKind, setScheduleKind] = useState<ScheduleKind>("interval");
  const [intervalSec, setIntervalSec] = useState("60");
  const [cronExpr, setCronExpr] = useState("0 */4 * * *");
  const [eventFilter, setEventFilter] = useState("news:catalyst");
  const [execMode, setExecMode] = useState<ExecMode>("hybrid");
  const [approvalPolicy, setApprovalPolicy] =
    useState<ApprovalPolicy>("semi_auto");
  const [personaId, setPersonaId] = useState("miner");
  const [skillIds, setSkillIds] = useState<string[]>([]);
  const [strategyId, setStrategyId] = useState("wide");
  const [walletHandle, setWalletHandle] = useState("");
  const [venue, setVenue] = useState("");
  const [symbol, setSymbol] = useState("");
  const [maxRuns, setMaxRuns] = useState("");
  const [maxSpendUsd, setMaxSpendUsd] = useState("");
  const [stopOnReversal, setStopOnReversal] = useState(true);
  const [activateNow, setActivateNow] = useState(false);

  const [children, setChildren] = useState<SwarmChildSpec[]>([
    {
      name: "Risk Breaker",
      goalDomain: "monitoring",
      execMode: "standalone",
      personaId: "risk_officer",
      condition: "on_signal",
      objective: "Pause downstream execution if drawdown threshold breaches",
    },
  ]);

  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const availableSkills = (libraries?.skills ?? []).filter(
    (sk) => sk.goalDomain === goalDomain || sk.goalDomain === "monitoring",
  );

  function toggleSkill(id: string) {
    setSkillIds((prev) =>
      prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id],
    );
  }

  function addChild() {
    if (children.length >= 6) return;
    setChildren((prev) => [
      ...prev,
      {
        name: `Stage ${prev.length + 1}`,
        goalDomain: "notifications",
        execMode: "standalone",
        condition: "on_signal",
        objective: "",
      },
    ]);
  }

  function updateChild(index: number, patch: Partial<SwarmChildSpec>) {
    setChildren((prev) =>
      prev.map((item, i) => (i === index ? { ...item, ...patch } : item)),
    );
  }

  function removeChild(index: number) {
    setChildren((prev) => prev.filter((_, i) => i !== index));
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    const trimmedName = name.trim();
    if (!trimmedName) {
      setError(t("Task name is required."));
      return;
    }
    const scheduleSpec =
      scheduleKind === "interval" || scheduleKind === "continuous"
        ? { intervalSec: Math.max(5, Number(intervalSec) || 60) }
        : scheduleKind === "cron"
          ? { cronExpr: cronExpr.trim() || "0 */4 * * *" }
          : scheduleKind === "event"
            ? { eventFilter: eventFilter.trim() || "news:catalyst" }
            : {};

    setSubmitting(true);
    try {
      const executionBinding = {
        ...(walletHandle.trim() ? { walletHandle: walletHandle.trim() } : {}),
        ...(venue.trim() ? { venue: venue.trim() } : {}),
        ...(symbol.trim() ? { symbol: symbol.trim().toUpperCase() } : {}),
      };
      const constraints = {
        ...(Number(maxRuns) > 0 ? { maxRuns: Number(maxRuns) } : {}),
        ...(Number(maxSpendUsd) > 0
          ? { maxSpendUsd: Number(maxSpendUsd) }
          : {}),
        stopOnReversal,
      };
      if (composerMode === "swarm") {
        await onCreateSwarm({
          name: trimmedName,
          goalDomain,
          goalObjective: goalObjective.trim(),
          scheduleKind,
          scheduleSpec,
          approvalPolicy,
          executionBinding,
          constraints,
          createdVia: "ui",
          children: children.filter((c) => c.name.trim()),
        });
      } else {
        await onCreateTask({
          name: trimmedName,
          goalDomain,
          goalObjective: goalObjective.trim(),
          scheduleKind,
          scheduleSpec,
          execMode,
          approvalPolicy,
          executionBinding,
          attachments: {
            ...(personaId ? { personaId } : {}),
            ...(skillIds.length ? { skillIds } : {}),
            ...(strategyId.trim() ? { strategyId: strategyId.trim() } : {}),
          },
          constraints,
          status: activateNow ? "active" : "draft",
          createdVia: "ui",
        });
      }
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("Failed to save task"));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog
      title={t("New Task")}
      onClose={onClose}
      wide
      className="wt-task-modal"
    >
      <form className="wt-task-form" onSubmit={(e) => void handleSubmit(e)}>
        <div className="f-seg" role="group" aria-label={t("Task kind")}>
          <button
            type="button"
            aria-pressed={composerMode === "single"}
            onClick={() => setComposerMode("single")}
          >
            {t("Single Task")}
          </button>
          <button
            type="button"
            aria-pressed={composerMode === "swarm"}
            onClick={() => setComposerMode("swarm")}
          >
            {t("Swarm DAG")}
          </button>
        </div>

        <div className="wt-form-grid">
          <label className="wt-field wt-span-2">
            <span>{t("Task name")}</span>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={
                composerMode === "swarm"
                  ? "Macro News → Miner Guard"
                  : "WEN EV Contrarian Loop"
              }
              required
            />
          </label>

          <label className="wt-field">
            <span>{t("Goal")}</span>
            <select
              value={goalDomain}
              onChange={(e) => setGoalDomain(e.target.value as GoalDomain)}
            >
              <option value="mining">Mining</option>
              <option value="trading">Trading</option>
              <option value="news">News</option>
              <option value="monitoring">Monitoring</option>
              <option value="portfolio">Portfolio</option>
              <option value="research">Research</option>
              <option value="notifications">Notifications</option>
              <option value="custom">Custom</option>
            </select>
          </label>

          <label className="wt-field">
            <span>{t("Mode")}</span>
            <select
              value={execMode}
              onChange={(e) => setExecMode(e.target.value as ExecMode)}
              disabled={composerMode === "swarm"}
            >
              <option value="standalone">Standalone (No AI)</option>
              <option value="hybrid">Hybrid (Rules + AI)</option>
              <option value="ai">AI Agent</option>
            </select>
          </label>

          <label className="wt-field wt-span-2">
            <span>{t("Objective")}</span>
            <input
              type="text"
              value={goalObjective}
              onChange={(e) => setGoalObjective(e.target.value)}
              placeholder="Stop immediately on trend reversal or daily loss breach"
            />
          </label>

          <label className="wt-field">
            <span>{t("Schedule")}</span>
            <select
              value={scheduleKind}
              onChange={(e) => setScheduleKind(e.target.value as ScheduleKind)}
            >
              <option value="interval">Interval</option>
              <option value="continuous">Continuous</option>
              <option value="cron">Cron</option>
              <option value="event">Event Trigger</option>
              <option value="once">Run Once</option>
            </select>
          </label>

          {scheduleKind === "cron" ? (
            <label className="wt-field">
              <span>{t("Cron expression")}</span>
              <input
                type="text"
                value={cronExpr}
                onChange={(e) => setCronExpr(e.target.value)}
              />
            </label>
          ) : scheduleKind === "event" ? (
            <label className="wt-field">
              <span>{t("Event filter")}</span>
              <input
                type="text"
                value={eventFilter}
                onChange={(e) => setEventFilter(e.target.value)}
              />
            </label>
          ) : (
            <label className="wt-field">
              <span>{t("Interval (sec)")}</span>
              <input
                type="number"
                min={5}
                value={intervalSec}
                onChange={(e) => setIntervalSec(e.target.value)}
                disabled={scheduleKind === "once"}
              />
            </label>
          )}

          <label className="wt-field">
            <span>{t("Approval")}</span>
            <select
              value={approvalPolicy}
              onChange={(e) =>
                setApprovalPolicy(e.target.value as ApprovalPolicy)
              }
            >
              <option value="manual">Manual</option>
              <option value="semi_auto">Semi-Auto</option>
              <option value="auto">Auto</option>
            </select>
          </label>

          <label className="wt-field">
            <span>{t("Persona")}</span>
            <select
              value={personaId}
              onChange={(e) => setPersonaId(e.target.value)}
            >
              <option value="">None</option>
              {(libraries?.personas ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>

          <label className="wt-field">
            <span>{t("Wallet handle")}</span>
            {(() => {
              const eligible = internalWallets.filter((w) =>
                goalDomain === "mining"
                  ? w.role === "mining"
                  : w.role === "trading" || w.role === "agent",
              );
              if (eligible.length > 0) {
                return (
                  <select
                    value={walletHandle}
                    onChange={(e) => setWalletHandle(e.target.value)}
                  >
                    <option value="">Default internal wallet</option>
                    {eligible.map((w) => (
                      <option key={w.id} value={w.handle}>
                        {w.name} ({w.role} · Internal)
                      </option>
                    ))}
                  </select>
                );
              }
              return (
                <input
                  type="text"
                  value={walletHandle}
                  onChange={(e) => setWalletHandle(e.target.value)}
                  placeholder="@wallet:wlt_..."
                />
              );
            })()}
          </label>

          <label className="wt-field">
            <span>{t("Venue / Strategy")}</span>
            <div className="wt-dual-inputs">
              <input
                type="text"
                value={venue}
                onChange={(e) => setVenue(e.target.value)}
                placeholder="jupiter / wen"
              />
              <input
                type="text"
                value={strategyId}
                onChange={(e) => setStrategyId(e.target.value)}
                placeholder="wide / ev"
              />
            </div>
          </label>

          <label className="wt-field">
            <span>{t("Symbol")}</span>
            <input
              type="text"
              value={symbol}
              onChange={(e) => setSymbol(e.target.value)}
              placeholder="BTC / SOL"
            />
          </label>

          <label className="wt-field">
            <span>{t("Max runs / USD cap")}</span>
            <div className="wt-dual-inputs">
              <input
                type="number"
                min={0}
                value={maxRuns}
                onChange={(e) => setMaxRuns(e.target.value)}
                placeholder="0 (∞)"
              />
              <input
                type="number"
                min={0}
                value={maxSpendUsd}
                onChange={(e) => setMaxSpendUsd(e.target.value)}
                placeholder="USD cap"
              />
            </div>
          </label>
        </div>

        {availableSkills.length > 0 && composerMode === "single" && (
          <div className="wt-modal-skills">
            <span className="wt-field-label">{t("Skills")}</span>
            <div className="wt-skill-chips">
              {availableSkills.map((sk) => {
                const selected = skillIds.includes(sk.id);
                return (
                  <button
                    key={sk.id}
                    type="button"
                    aria-pressed={selected}
                    className={`wt-filter-chip ${selected ? "is-selected" : ""}`}
                    onClick={() => toggleSkill(sk.id)}
                  >
                    {sk.name}
                  </button>
                );
              })}
            </div>
          </div>
        )}

        {composerMode === "swarm" && (
          <div className="wt-swarm-children">
            <div className="wt-swarm-head">
              <span className="wt-field-label">
                {t("Downstream DAG nodes")} ({children.length})
              </span>
              <button
                type="button"
                className="wt-filter-chip"
                onClick={addChild}
              >
                <Plus size={12} aria-hidden="true" />
                {t("Add child")}
              </button>
            </div>
            {children.map((child, idx) => (
              <div key={idx} className="wt-swarm-child-row">
                <input
                  type="text"
                  value={child.name}
                  onChange={(e) => updateChild(idx, { name: e.target.value })}
                  placeholder="Child task name"
                />
                <select
                  value={child.goalDomain ?? "monitoring"}
                  onChange={(e) =>
                    updateChild(idx, {
                      goalDomain: e.target.value as GoalDomain,
                    })
                  }
                >
                  <option value="monitoring">Monitoring</option>
                  <option value="mining">Mining</option>
                  <option value="trading">Trading</option>
                  <option value="notifications">Notifications</option>
                  <option value="research">Research</option>
                </select>
                <select
                  value={child.condition ?? "on_signal"}
                  onChange={(e) =>
                    updateChild(idx, {
                      condition: e.target.value as EdgeCondition,
                    })
                  }
                >
                  <option value="on_signal">on_signal</option>
                  <option value="on_success">on_success</option>
                  <option value="on_threshold">on_threshold</option>
                  <option value="on_failure">on_failure</option>
                  <option value="always">always</option>
                </select>
                <button
                  type="button"
                  className="f-icon-btn"
                  aria-label={t("Remove")}
                  onClick={() => removeChild(idx)}
                >
                  <Trash2 size={13} aria-hidden="true" />
                </button>
              </div>
            ))}
          </div>
        )}

        <div className="wt-modal-checks">
          <label className="wt-check-inline">
            <input
              type="checkbox"
              checked={stopOnReversal}
              onChange={(e) => setStopOnReversal(e.target.checked)}
            />
            <span>{t("Stop on trend reversal")}</span>
          </label>
          {composerMode === "single" && (
            <label className="wt-check-inline">
              <input
                type="checkbox"
                checked={activateNow}
                onChange={(e) => setActivateNow(e.target.checked)}
              />
              <span>{t("Activate immediately")}</span>
            </label>
          )}
        </div>

        {error && (
          <p className="wt-form-error" role="alert">
            {error}
          </p>
        )}

        <div className="wt-modal-footer">
          <button type="button" className="wt-btn-soft" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button
            type="submit"
            className="wt-btn-rose-sm"
            disabled={submitting}
          >
            {submitting
              ? t("Saving…")
              : composerMode === "swarm"
                ? t("Create Swarm DAG")
                : activateNow
                  ? t("Create & Activate")
                  : t("Save Draft")}
          </button>
        </div>
      </form>
    </Dialog>
  );
}
