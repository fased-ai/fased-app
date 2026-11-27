import { useState } from "react";
import type { StakingOverviewResponse } from "../../mining-lv1x";

interface MineStakePageProps {
  staking: StakingOverviewResponse | null;
  selectedWalletId: string;
  mainnetLocked: boolean;
  busy: boolean;
  onStake: (amountSat: number) => void;
  onUnstake: (amountSat: number) => void;
  onActivate: () => void;
  onClaimBtc: () => void;
}

export function MineStakePage({
  staking,
  selectedWalletId,
  mainnetLocked,
  busy,
  onStake,
  onUnstake,
  onActivate,
  onClaimBtc,
}: MineStakePageProps) {
  const [mode, setMode] = useState<"stake" | "unstake">("stake");
  const [amountStr, setAmountStr] = useState("10");

  const parsedSat = Math.max(0, Number(amountStr) || 0);
  const userActive = staking?.userActiveSat ?? 0;
  const userPending = staking?.userPendingSat ?? 0;
  const poolActive = staking?.totalActiveSat ?? 0;
  const walletSat = staking?.walletSatBalance ?? 0;
  const minStake = staking?.minStakeSat ?? 10;

  const projectedShare =
    mode === "stake"
      ? poolActive + parsedSat > 0
        ? (((userActive + parsedSat) / (poolActive + parsedSat)) * 100).toFixed(
            2,
          )
        : "0.00"
      : poolActive - parsedSat > 0
        ? (
            (Math.max(0, userActive - parsedSat) / (poolActive - parsedSat)) *
            100
          ).toFixed(2)
        : "0.00";

  const sourceMax = mode === "stake" ? walletSat : userActive + userPending;

  return (
    <div className="mine-subpage" aria-label="WENSAT Staking and cbBTC Yield">
      <div className="mine-kpi-strip">
        <div className="wt-kpi-box">
          <span>Active Staked Pool</span>
          <strong>
            {poolActive.toLocaleString("en-US", { maximumFractionDigits: 1 })}{" "}
            SAT
          </strong>
        </div>
        <div className="wt-kpi-box">
          <span>Pending Maturity</span>
          <strong>
            {(staking?.totalPendingSat ?? 0).toLocaleString("en-US", {
              maximumFractionDigits: 1,
            })}{" "}
            SAT
          </strong>
        </div>
        <div className="wt-kpi-box">
          <span>Total BTC Distributed</span>
          <strong>{(staking?.totalBtcDistributed ?? 0).toFixed(6)} BTC</strong>
        </div>
        <div className="wt-kpi-box">
          <span>BTC Stream Remaining</span>
          <strong>{(staking?.streamRemainingBtc ?? 0).toFixed(6)} BTC</strong>
        </div>
      </div>

      <div className="mine-stake-grid">
        <div className="wt-wpanel mine-subpanel">
          <div className="mine-section-head">
            <h2>Your Stake Position</h2>
            <span className="mine-muted-note">
              Share {(staking?.sharePct ?? 0).toFixed(2)}%
            </span>
          </div>

          <div className="wt-hairline-table">
            <div className="wt-hairline-row">
              <span>Active Staked WENSAT</span>
              <strong>{userActive.toFixed(2)} SAT</strong>
            </div>
            <div className="wt-hairline-row">
              <span>Pending Warmup Stake</span>
              <strong>{userPending.toFixed(2)} SAT</strong>
            </div>
            <div className="wt-hairline-row">
              <span>Wallet WENSAT Balance</span>
              <span>{walletSat.toFixed(2)} SAT</span>
            </div>
            <div className="wt-hairline-row">
              <span>Claimable cbBTC Rewards</span>
              <strong>{(staking?.claimableBtc ?? 0).toFixed(6)} cbBTC</strong>
            </div>
          </div>

          <div className="mine-secondary-actions">
            <button
              type="button"
              className="wt-btn-soft"
              disabled={
                busy ||
                mainnetLocked ||
                !selectedWalletId ||
                !staking?.canActivate
              }
              onClick={onActivate}
            >
              Activate Mature Stake
            </button>
            <button
              type="button"
              className="wt-btn-rose-sm"
              disabled={
                busy ||
                mainnetLocked ||
                !selectedWalletId ||
                (staking?.claimableBtc ?? 0) <= 0
              }
              onClick={onClaimBtc}
            >
              Claim cbBTC Yield
            </button>
          </div>
        </div>

        <div className="wt-wpanel mine-composer">
          <div className="wt-seg" role="group" aria-label="Staking mode">
            <button
              type="button"
              aria-pressed={mode === "stake"}
              onClick={() => setMode("stake")}
            >
              Stake
            </button>
            <button
              type="button"
              aria-pressed={mode === "unstake"}
              onClick={() => setMode("unstake")}
            >
              Unstake
            </button>
          </div>

          <div className="mine-field">
            <label htmlFor="mine-stake-amount">
              Amount WENSAT (min {minStake} SAT to stake)
            </label>
            <input
              id="mine-stake-amount"
              className="mine-amount-input"
              inputMode="decimal"
              value={amountStr}
              onChange={(e) => setAmountStr(e.target.value)}
            />
            <div className="mine-quick-chips">
              {[25, 50, 75, 100].map((pct) => (
                <button
                  key={pct}
                  type="button"
                  className="mine-chip"
                  disabled={sourceMax <= 0}
                  onClick={() =>
                    setAmountStr(((sourceMax * pct) / 100).toFixed(2))
                  }
                >
                  {pct === 100 ? "Max" : `${pct}%`}
                </button>
              ))}
            </div>
          </div>

          <div className="mine-kv-row">
            <span>Projected Pool Share</span>
            <strong>{projectedShare}%</strong>
          </div>

          <button
            type="button"
            className="wt-btn-rose"
            disabled={
              busy ||
              mainnetLocked ||
              !selectedWalletId ||
              parsedSat <= 0 ||
              (mode === "stake" && parsedSat < minStake)
            }
            onClick={() =>
              mode === "stake" ? onStake(parsedSat) : onUnstake(parsedSat)
            }
          >
            {mode === "stake" ? "Stake WENSAT" : "Unstake WENSAT"}
          </button>
        </div>
      </div>
    </div>
  );
}
