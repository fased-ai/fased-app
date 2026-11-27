import { useState } from "react";
import { Check, Copy, ExternalLink } from "../../icons";
import type { LeaderboardResponse, ProtocolStats } from "../../mining-lv1x";

const SPECTRUM = [
  "#E0566F",
  "#D98466",
  "#6F7889",
  "#2FB88C",
  "#33A9B8",
] as const;

type AssetSubTab = "overview" | "miners" | "rounds" | "addresses";
type MinerFilter = "winners" | "active" | "missed";

interface MineAssetsPageProps {
  leaderboard: LeaderboardResponse | null;
  stats: ProtocolStats | null;
  windowChoice: "24h" | "7d" | "30d" | "all";
  onWindowChange: (w: "24h" | "7d" | "30d" | "all") => void;
}

export function MineAssetsPage({
  leaderboard,
  stats,
  windowChoice,
  onWindowChange,
}: MineAssetsPageProps) {
  const [tab, setTab] = useState<AssetSubTab>("overview");
  const [minerFilter, setMinerFilter] = useState<MinerFilter>("active");
  const [copiedKey, setCopiedKey] = useState("");

  const rounds = leaderboard?.rounds ?? [];
  const miners = (leaderboard?.miners ?? []).filter((m) =>
    minerFilter === "active"
      ? true
      : minerFilter === "winners"
        ? m.wins > 0
        : m.wins === 0,
  );

  function copyAddr(label: string, addr: string) {
    if (!addr) return;
    void navigator.clipboard?.writeText(addr);
    setCopiedKey(label);
    window.setTimeout(() => setCopiedKey(""), 1200);
  }

  const maxVol = Math.max(5, ...rounds.map((r) => r.poolSol));
  const chartW = 520;
  const chartH = 148;
  const gateY = chartH - 22 - (5 / maxVol) * (chartH - 36);

  const addrRows: Array<{
    label: string;
    addr: string;
    kind: "account" | "token";
  }> = [
    { label: "LV1X Program ID", addr: stats?.programId ?? "", kind: "account" },
    { label: "Ledger ID", addr: stats?.ledgerId ?? "", kind: "account" },
    {
      label: "Runtime PDA",
      addr: stats?.runtimeAddress ?? "",
      kind: "account",
    },
    { label: "WENSAT Mint", addr: stats?.mintAddress ?? "", kind: "token" },
    { label: "cbBTC Mint", addr: stats?.btcMintAddress ?? "", kind: "token" },
    {
      label: "Staking State PDA",
      addr: stats?.stakingAddress ?? "",
      kind: "account",
    },
    {
      label: "Treasury PDA",
      addr: stats?.treasuryAddress ?? "",
      kind: "account",
    },
  ];

  return (
    <div className="mine-subpage" aria-label="Protocol Assets and Leaderboard">
      <div className="mine-subpage-head">
        <div
          className="wt-tabs-underline"
          role="tablist"
          aria-label="Assets views"
        >
          <button
            type="button"
            role="tab"
            aria-selected={tab === "overview"}
            onClick={() => setTab("overview")}
          >
            Overview
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "miners"}
            onClick={() => setTab("miners")}
          >
            Miners ({leaderboard?.miners?.length ?? 0})
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "rounds"}
            onClick={() => setTab("rounds")}
          >
            Rounds ({rounds.length})
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === "addresses"}
            onClick={() => setTab("addresses")}
          >
            Addresses
          </button>
        </div>

        <div className="wt-seg" role="group" aria-label="Leaderboard timeframe">
          {(["24h", "7d", "30d", "all"] as const).map((w) => (
            <button
              key={w}
              type="button"
              aria-pressed={windowChoice === w}
              onClick={() => onWindowChange(w)}
            >
              {w === "all" ? "All" : w}
            </button>
          ))}
        </div>
      </div>

      {tab === "overview" && (
        <div className="mine-assets-overview">
          <div className="mine-kpi-strip">
            <div className="wt-kpi-box">
              <span>Volume ({windowChoice})</span>
              <strong>{(leaderboard?.volumeSol ?? 0).toFixed(2)} SOL</strong>
            </div>
            <div className="wt-kpi-box">
              <span>Settled Rounds</span>
              <strong>{leaderboard?.roundsCount ?? 0}</strong>
            </div>
            <div className="wt-kpi-box">
              <span>Active Miners</span>
              <strong>{leaderboard?.minersCount ?? 0}</strong>
            </div>
            <div className="wt-kpi-box">
              <span>SAT Awarded</span>
              <strong>{(leaderboard?.satAwarded ?? 0).toFixed(1)} SAT</strong>
            </div>
          </div>

          <div className="wt-wpanel mine-vol-chart-panel">
            <div className="mine-section-head">
              <h2>Per-Round Volume vs. 5 SOL Graduation Gate</h2>
              <span className="mine-muted-note">
                {stats?.phaseName ?? "Bootstrap"}
              </span>
            </div>
            <svg
              className="mine-vol-svg"
              viewBox={`0 0 ${chartW} ${chartH}`}
              role="img"
              aria-label="Volume over settled rounds"
            >
              <line
                x1="8"
                y1={gateY}
                x2={chartW - 8}
                y2={gateY}
                stroke="#D98466"
                strokeWidth="1"
                strokeDasharray="4 4"
              />
              {rounds.slice(0, 24).map((r, i) => {
                const slotW =
                  (chartW - 16) / Math.max(1, Math.min(24, rounds.length));
                const barW = Math.max(4, slotW * 0.68);
                const h = Math.max(4, (r.poolSol / maxVol) * (chartH - 36));
                const x = 8 + i * slotW + (slotW - barW) / 2;
                const y = chartH - 22 - h;
                return (
                  <rect
                    key={r.roundSlot}
                    x={x}
                    y={y}
                    width={barW}
                    height={h}
                    rx="2"
                    fill={SPECTRUM[r.winningRange ?? 2]}
                  >
                    <title>
                      #{r.roundSlot}: {r.poolSol.toFixed(2)} SOL ({r.code})
                    </title>
                  </rect>
                );
              })}
            </svg>
          </div>
        </div>
      )}

      {tab === "miners" && (
        <div className="wt-wpanel mine-subpanel">
          <div className="mine-section-head">
            <div className="wt-seg" role="group" aria-label="Miner filter">
              {(
                [
                  ["active", "Active"],
                  ["winners", "Winners"],
                  ["missed", "Missed"],
                ] as const
              ).map(([k, lbl]) => (
                <button
                  key={k}
                  type="button"
                  aria-pressed={minerFilter === k}
                  onClick={() => setMinerFilter(k)}
                >
                  {lbl}
                </button>
              ))}
            </div>
          </div>

          <div className="mine-table-scroll">
            <table className="mine-table" aria-label="Miners leaderboard">
              <thead>
                <tr>
                  <th>Wallet</th>
                  <th>Rounds</th>
                  <th>Wins</th>
                  <th>Committed</th>
                  <th>Net SOL</th>
                  <th>SAT Earned</th>
                </tr>
              </thead>
              <tbody>
                {miners.length === 0 ? (
                  <tr>
                    <td colSpan={6} className="mine-muted-note">
                      No miners matching {minerFilter} filter yet.
                    </td>
                  </tr>
                ) : (
                  miners.map((m) => (
                    <tr key={m.address}>
                      <td className="f-mono">{m.shortAddress}</td>
                      <td>{m.rounds}</td>
                      <td>{m.wins}</td>
                      <td>{m.committedSol.toFixed(3)} SOL</td>
                      <td>{m.netSol.toFixed(3)} SOL</td>
                      <td>{m.earnedSat.toFixed(2)} SAT</td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {tab === "rounds" && (
        <div className="wt-wpanel mine-subpanel">
          <div className="mine-table-scroll">
            <table className="mine-table" aria-label="Historical rounds">
              <thead>
                <tr>
                  <th>Round</th>
                  <th>Winning</th>
                  <th>Move</th>
                  <th>Pool SOL</th>
                  <th>Crowd Split</th>
                  <th>SAT Awarded</th>
                </tr>
              </thead>
              <tbody>
                {rounds.map((r) => (
                  <tr key={r.roundSlot}>
                    <td>#{r.roundSlot}</td>
                    <td>
                      <span
                        className="mine-range-pill"
                        style={{ color: SPECTRUM[r.winningRange ?? 2] }}
                      >
                        {r.code}
                      </span>
                    </td>
                    <td>
                      {r.moveBps >= 0 ? "+" : ""}
                      {r.moveBps} bps
                    </td>
                    <td>{r.poolSol.toFixed(2)} SOL</td>
                    <td>
                      <div className="mine-crowd-bar">
                        {r.crowdShares.map((s, i) => (
                          <i
                            key={i}
                            style={{
                              width: `${Math.max(2, s * 100)}%`,
                              background: SPECTRUM[i],
                            }}
                          />
                        ))}
                      </div>
                    </td>
                    <td>{r.minerSat.toFixed(1)} SAT</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {tab === "addresses" && (
        <div className="wt-wpanel mine-subpanel">
          <div className="wt-hairline-table">
            {addrRows.map((row) => (
              <div key={row.label} className="wt-hairline-row">
                <span>{row.label}</span>
                <div className="mine-row-actions">
                  <span className="f-mono">{row.addr || "—"}</span>
                  {row.addr && (
                    <>
                      <button
                        type="button"
                        className="icon-btn"
                        aria-label={`Copy ${row.label}`}
                        onClick={() => copyAddr(row.label, row.addr)}
                      >
                        {copiedKey === row.label ? (
                          <Check size={13} aria-hidden="true" />
                        ) : (
                          <Copy size={13} aria-hidden="true" />
                        )}
                      </button>
                      <a
                        className="icon-btn"
                        href={`https://solscan.io/${row.kind}/${encodeURIComponent(row.addr)}?cluster=devnet`}
                        target="_blank"
                        rel="noreferrer"
                        aria-label={`Open ${row.label} on Solscan`}
                      >
                        <ExternalLink size={13} aria-hidden="true" />
                      </a>
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
