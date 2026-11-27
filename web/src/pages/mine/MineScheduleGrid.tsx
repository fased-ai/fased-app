import { useMemo } from "react";
import { Dialog } from "../../components/shared";
import type { ScheduleMode } from "../../mining-lv1x";

export interface ScheduleDraft {
  horizonLabel: "4h" | "12h" | "24h" | "7d" | "30d";
  scheduleMode: ScheduleMode;
  startMode: "now" | "next" | "plus2";
  selectedOffsets: number[];
  everyN: number;
  cycleOn: number;
  cycleOff: number;
  randomCount: number;
  rollingBudget: boolean;
}

const HORIZON_ROUNDS: Record<ScheduleDraft["horizonLabel"], number> = {
  "4h": 24,
  "12h": 72,
  "24h": 144,
  "7d": 1008,
  "30d": 4320,
};

export function computeScheduledSlots(
  draft: ScheduleDraft,
  currentSlot: number,
): number[] {
  const startOffset =
    draft.startMode === "plus2" ? 2 : draft.startMode === "next" ? 1 : 0;
  const totalWindow = Math.min(144, HORIZON_ROUNDS[draft.horizonLabel]);
  const slots: number[] = [];

  if (draft.scheduleMode === "choose") {
    const picked =
      draft.selectedOffsets.length > 0 ? draft.selectedOffsets : [0];
    return picked.map((o) => currentSlot + startOffset + o);
  }
  if (draft.scheduleMode === "cycle") {
    const on = Math.max(1, draft.cycleOn);
    const off = Math.max(1, draft.cycleOff);
    const period = on + off;
    for (let i = 0; i < totalWindow; i++) {
      if (i % period < on) slots.push(currentSlot + startOffset + i);
    }
    return slots;
  }
  if (draft.scheduleMode === "random") {
    const target = Math.max(1, Math.min(totalWindow, draft.randomCount));
    const step = Math.max(1, Math.floor(totalWindow / target));
    for (let i = 0; i < totalWindow && slots.length < target; i += step) {
      slots.push(currentSlot + startOffset + i);
    }
    return slots;
  }
  const every = Math.max(1, draft.everyN);
  for (let i = 0; i < totalWindow; i += every) {
    slots.push(currentSlot + startOffset + i);
  }
  return slots;
}

interface MineScheduleGridProps {
  draft: ScheduleDraft;
  currentSlot: number;
  perRoundSol: number;
  depositSol: number;
  onChange: (next: ScheduleDraft) => void;
  onClose: () => void;
}

export function MineScheduleGrid({
  draft,
  currentSlot,
  perRoundSol,
  depositSol,
  onChange,
  onClose,
}: MineScheduleGridProps) {
  const startOffset =
    draft.startMode === "plus2" ? 2 : draft.startMode === "next" ? 1 : 0;
  const visibleSlots = Math.min(48, HORIZON_ROUNDS[draft.horizonLabel]);
  const planned = useMemo(
    () => computeScheduledSlots(draft, currentSlot),
    [draft, currentSlot],
  );
  const plannedSet = useMemo(() => new Set(planned), [planned]);
  const totalRequiredSol = planned.length * Math.max(0.01, perRoundSol);
  const skipRisk = depositSol > 0 && depositSol < totalRequiredSol;

  function toggleCell(offset: number) {
    const exists = draft.selectedOffsets.includes(offset);
    const next = exists
      ? draft.selectedOffsets.filter((o) => o !== offset)
      : [...draft.selectedOffsets, offset].sort((a, b) => a - b);
    onChange({
      ...draft,
      scheduleMode: "choose",
      selectedOffsets: next.length > 0 ? next : [offset],
    });
  }

  return (
    <Dialog title="Mining Schedule Grid" onClose={onClose} wide>
      <div className="mine-sched-modal">
        <div className="mine-sched-row">
          <div className="mine-field">
            <label>Horizon</label>
            <div className="wt-seg" role="group" aria-label="Schedule horizon">
              {(["4h", "12h", "24h", "7d", "30d"] as const).map((h) => (
                <button
                  key={h}
                  type="button"
                  aria-pressed={draft.horizonLabel === h}
                  onClick={() => onChange({ ...draft, horizonLabel: h })}
                >
                  {h}
                </button>
              ))}
            </div>
          </div>

          <div className="mine-field">
            <label>Start Slot</label>
            <div className="wt-seg" role="group" aria-label="Schedule start">
              {(
                [
                  ["now", "Now"],
                  ["next", "Next"],
                  ["plus2", "+2"],
                ] as const
              ).map(([k, lbl]) => (
                <button
                  key={k}
                  type="button"
                  aria-pressed={draft.startMode === k}
                  onClick={() => onChange({ ...draft, startMode: k })}
                >
                  {lbl}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="mine-field">
          <label>Cadence Pattern</label>
          <div className="wt-seg" role="group" aria-label="Schedule pattern">
            {(
              [
                ["every", "Every"],
                ["choose", "Choose"],
                ["cycle", "Cycle"],
                ["random", "Random"],
              ] as const
            ).map(([k, lbl]) => (
              <button
                key={k}
                type="button"
                aria-pressed={draft.scheduleMode === k}
                onClick={() => onChange({ ...draft, scheduleMode: k })}
              >
                {lbl}
              </button>
            ))}
          </div>
        </div>

        <div
          className="mine-sched-grid"
          role="group"
          aria-label="10-minute slot calendar"
        >
          {Array.from({ length: visibleSlots }, (_, idx) => {
            const slot = currentSlot + startOffset + idx;
            const active = plannedSet.has(slot);
            return (
              <button
                key={slot}
                type="button"
                className={`mine-sched-cell${active ? " is-active" : ""}`}
                aria-pressed={active}
                onClick={() => toggleCell(idx)}
              >
                #{slot % 1000}
              </button>
            );
          })}
        </div>

        <label className="mine-toggle-row">
          <input
            type="checkbox"
            checked={draft.rollingBudget}
            onChange={(e) =>
              onChange({ ...draft, rollingBudget: e.target.checked })
            }
          />
          <span>Rolling Budget (recycle returned SOL into next rounds)</span>
        </label>

        <div className="mine-sched-summary">
          <span>
            {planned.length} rounds · est. {totalRequiredSol.toFixed(2)} SOL
            total
          </span>
          {skipRisk && (
            <span className="mine-warn-text">
              Deposit {depositSol.toFixed(2)} SOL &lt;{" "}
              {totalRequiredSol.toFixed(2)} SOL — later rounds may skip if prior
              rounds miss.
            </span>
          )}
        </div>

        <div className="mine-modal-actions">
          <button type="button" className="wt-btn-rose-sm" onClick={onClose}>
            Apply Schedule ({planned.length}r)
          </button>
        </div>
      </div>
    </Dialog>
  );
}
