import { useState } from "react";
import { useUI } from "../../ui-preferences";
import { Pause, Play, Plus, Trash2 } from "../../icons";
import type { LabScript } from "../../quantlab-desk";
import type { Interval } from "../../market-data";
import type { TradeAccountOption, TradeBot } from "../../trade-engine";

export function BotAgentWidget({
  venue,
  symbol,
  interval,
  scripts,
  bots,
  accounts = [],
  agentProfiles = [],
  busy = false,
  onCreateBot,
  onBotAction,
  onDeleteBot,
}: {
  venue: string;
  symbol: string;
  interval: Interval;
  scripts: LabScript[];
  bots: TradeBot[];
  accounts?: TradeAccountOption[];
  agentProfiles?: { id: string; name: string }[];
  busy?: boolean;
  onCreateBot?: (req: {
    name: string;
    kind: "fasedscript" | "ai_agent";
    accountId: string;
    venue: string;
    symbol: string;
    interval: string;
    scriptId?: string;
    agentId?: string;
    maxNotionalUsd: number;
  }) => void;
  onBotAction?: (id: string, action: "start" | "pause" | "stop") => void;
  onDeleteBot?: (id: string) => void;
}) {
  const { t } = useUI();
  const [kind, setKind] = useState<"fasedscript" | "ai_agent">("fasedscript");
  const [name, setName] = useState(`${symbol} Bot`);
  const [accountId, setAccountId] = useState("paper");
  const [scriptId, setScriptId] = useState(scripts[0]?.id ?? "");
  const [agentId, setAgentId] = useState(agentProfiles[0]?.id ?? "");
  const [maxUsd, setMaxUsd] = useState("250");
  const [adding, setAdding] = useState(false);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!onCreateBot) return;
    onCreateBot({
      name: name.trim() || `${symbol} ${kind}`,
      kind,
      accountId,
      venue,
      symbol,
      interval,
      scriptId: kind === "fasedscript" ? scriptId || scripts[0]?.id : undefined,
      agentId:
        kind === "ai_agent" ? agentId || agentProfiles[0]?.id : undefined,
      maxNotionalUsd: Number(maxUsd) > 0 ? Number(maxUsd) : 250,
    });
    setAdding(false);
  }

  return (
    <div className="trade-bots-widget">
      <div className="trade-desk-head">
        <span className="subtle">
          {venue} · {symbol} · {interval}
        </span>
        <button
          type="button"
          className="trade-mini-btn"
          onClick={() => setAdding((v) => !v)}
        >
          <Plus size={13} aria-hidden="true" />
          <span>{t("Add")}</span>
        </button>
      </div>

      {adding && (
        <form className="trade-ticket-grid" onSubmit={submit}>
          <div className="trade-ticket-pair">
            <label>
              <span>{t("Kind")}</span>
              <select
                value={kind}
                onChange={(e) =>
                  setKind(e.target.value as "fasedscript" | "ai_agent")
                }
              >
                <option value="fasedscript">{t("Strategy")}</option>
                <option value="ai_agent">{t("AI")}</option>
              </select>
            </label>
            <label>
              <span>{t("Account")}</span>
              <select
                value={accountId}
                onChange={(e) => setAccountId(e.target.value)}
              >
                <option value="paper">{t("Paper")}</option>
                {accounts
                  .filter((a) => a.id !== "paper")
                  .map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.label}
                    </option>
                  ))}
              </select>
            </label>
          </div>
          <label>
            <span>{t("Name")}</span>
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          {kind === "fasedscript" ? (
            <label>
              <span>{t("Strategy")}</span>
              <select
                value={scriptId}
                onChange={(e) => setScriptId(e.target.value)}
              >
                {scripts.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </label>
          ) : (
            <label>
              <span>{t("Agent")}</span>
              <select
                value={agentId}
                onChange={(e) => setAgentId(e.target.value)}
              >
                {agentProfiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          <label>
            <span>{t("Max order")}</span>
            <input
              type="number"
              min="1"
              value={maxUsd}
              onChange={(e) => setMaxUsd(e.target.value)}
            />
          </label>
          <button type="submit" className="primary" disabled={busy}>
            {t("Create")}
          </button>
        </form>
      )}

      <div className="trade-bottom-rows">
        {bots.map((b) => (
          <div className="trade-bottom-row" key={b.id}>
            <div>
              <strong>{b.name}</strong>
              <small className="subtle">
                {` · ${b.symbol} (${b.interval}) · ${b.status}`}
                {b.lastSignal ? ` · ${b.lastSignal}` : ""}
              </small>
            </div>
            <div className="trade-desk-actions">
              {b.status !== "running" ? (
                <button
                  type="button"
                  className="icon-button"
                  title={t("Start")}
                  disabled={busy}
                  onClick={() => onBotAction?.(b.id, "start")}
                >
                  <Play size={13} />
                </button>
              ) : (
                <button
                  type="button"
                  className="icon-button"
                  title={t("Pause")}
                  disabled={busy}
                  onClick={() => onBotAction?.(b.id, "pause")}
                >
                  <Pause size={13} />
                </button>
              )}
              <button
                type="button"
                className="icon-button"
                title={t("Remove")}
                disabled={busy}
                onClick={() => onDeleteBot?.(b.id)}
              >
                <Trash2 size={13} />
              </button>
            </div>
          </div>
        ))}
        {bots.length === 0 && !adding && <p className="subtle">{t("None")}</p>}
      </div>
    </div>
  );
}
