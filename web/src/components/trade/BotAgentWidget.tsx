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
  compactComposer = false,
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
  compactComposer?: boolean;
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
  const [adding, setAdding] = useState(compactComposer);

  const accountOptions: TradeAccountOption[] =
    accounts.length > 0
      ? accounts
      : [{ id: "paper", label: "Paper", kind: "paper", executionMode: "paper" }];

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
    if (!compactComposer) setAdding(false);
  }

  return (
    <div className="trade-bots-widget">
      {!compactComposer && (
        <div className="trade-desk-head">
          <span className="subtle mono">
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
      )}

      {adding && (
        <form className="wt-composer-form" onSubmit={submit}>
          <div className="wt-seg-bar" role="group" aria-label={t("Kind")}>
            <button
              type="button"
              className={kind === "fasedscript" ? "is-buy" : ""}
              onClick={() => setKind("fasedscript")}
            >
              {t("Strategy")}
            </button>
            <button
              type="button"
              className={kind === "ai_agent" ? "is-buy" : ""}
              onClick={() => setKind("ai_agent")}
            >
              {t("AI")}
            </button>
          </div>

          <div className="wt-chip-group" role="group" aria-label={t("Account")}>
            {accountOptions.map((a) => (
              <button
                key={a.id}
                type="button"
                className={`trade-mini-btn ${accountId === a.id ? "is-selected" : ""}`}
                onClick={() => setAccountId(a.id)}
              >
                {a.id === "paper" ? t("Paper") : a.label}
              </button>
            ))}
          </div>

          <div className="wt-inline-fields">
            <label>
              <span>{t("Name")}</span>
              <input value={name} onChange={(e) => setName(e.target.value)} />
            </label>

            {kind === "fasedscript" ? (
              <div className="wt-chip-group" role="group" aria-label={t("Strategy")}>
                {scripts.map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    className={`trade-mini-btn ${(scriptId || scripts[0]?.id) === s.id ? "is-selected" : ""}`}
                    onClick={() => setScriptId(s.id)}
                  >
                    {s.name}
                  </button>
                ))}
                {scripts.length === 0 && (
                  <span className="subtle">{t("None")}</span>
                )}
              </div>
            ) : (
              <div className="wt-chip-group" role="group" aria-label={t("Agent")}>
                {agentProfiles.map((p) => (
                  <button
                    key={p.id}
                    type="button"
                    className={`trade-mini-btn ${(agentId || agentProfiles[0]?.id) === p.id ? "is-selected" : ""}`}
                    onClick={() => setAgentId(p.id)}
                  >
                    {p.name}
                  </button>
                ))}
                {agentProfiles.length === 0 && (
                  <span className="subtle">{t("None")}</span>
                )}
              </div>
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
          </div>

          <button type="submit" className="wt-btn-rose" disabled={busy}>
            {`${t("Create")} · ${symbol} →`}
          </button>
        </form>
      )}

      {!compactComposer && (
        <div className="wt-hairline-table">
          {bots.map((b) => (
            <div className="wt-hairline-row" key={b.id}>
              <div>
                <strong>{b.name}</strong>
                <small className="subtle mono">
                  {` · ${b.symbol} · ${b.interval} · ${b.status}`}
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
      )}
    </div>
  );
}
