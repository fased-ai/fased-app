package wenlv1x

import (
	"context"

	"github.com/fased-ai/fased-app/internal/custody"
)

// CandleProvider supplies external BTC OHLCV candles from Fased's marketdata engine.
type CandleProvider func(ctx context.Context, interval string, limit int) ([]CandleBar, string, error)

// Config initializes the Lean V1 (LV1X) mining engine.
type Config struct {
	DataDir        string
	Custody        custody.Backend
	DevnetRPCURL   string
	CandleProvider CandleProvider
	FixtureMode    bool
	HTTPClient     any // optional *http.Client
	Now            any // optional func() time.Time
}

// StrategyPreset defines a named 80-range distribution shape matching WEN's allocation curve.
type StrategyPreset struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Mode        string    `json:"mode"` // neutral | directional | defensive
	Buckets     [5]uint16 `json:"bucketsBps"`
	Curve       []float64 `json:"curve"` // 80 normalized bars summing to 1.0
}

// SavedStrategy represents a user-saved or imported allocation strategy.
type SavedStrategy struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Mode        string    `json:"mode"`
	Buckets     [5]uint16 `json:"bucketsBps"`
	Curve       []float64 `json:"curve"`
	Source      string    `json:"source"` // custom | import | preset
	CreatedAt   int64     `json:"createdAt"`
	UpdatedAt   int64     `json:"updatedAt"`
}

// RoundBucket summarizes pooled SOL and implied EV for one of the 5 LV1X settlement ranges (A..E).
type RoundBucket struct {
	Index      uint8   `json:"index"` // 0..4
	Code       string  `json:"code"`  // A..E
	Label      string  `json:"label"` // <-20bps, -20..-5bps, -5..+5bps, +5..+20bps, >+20bps
	PoolSOL    float64 `json:"poolSol"`
	CrowdShare float64 `json:"crowdShare"`
	ModelProb  float64 `json:"modelProb"`
	EVMultiple float64 `json:"evMultiple"`
}

// RoundTelemetry captures the current or historical LV1X round window.
type RoundTelemetry struct {
	Cluster        string        `json:"cluster"`
	ProgramID      string        `json:"programId"`
	LedgerID       string        `json:"ledgerId"`
	RoundSlot      uint64        `json:"roundSlot"`
	Phase          string        `json:"phase"` // commit | reveal | settle | settled
	EndsAt         int64         `json:"endsAt"`
	RemainingSec   int64         `json:"remainingSec"`
	OpenPriceUSD   float64       `json:"openPriceUsd"`
	MarkPriceUSD   float64       `json:"markPriceUsd"`
	MoveBps        int           `json:"moveBps"`
	WinningRange   *uint8        `json:"winningRange,omitempty"`
	InnerBps       int           `json:"innerBps"`
	OuterBps       int           `json:"outerBps"`
	TotalPoolSOL   float64       `json:"totalPoolSol"`
	MinerCount     uint32        `json:"minerCount"`
	BTCVaultSat    uint64        `json:"btcVaultSat,omitempty"`
	Buckets        []RoundBucket `json:"buckets"`
	Source         string        `json:"source"` // devnet-rpc | cached | simulation
	BPFVerified    bool          `json:"bpfVerified"`
	DeployedSHA256 string        `json:"deployedSha256"`
}

// StreakEntry records one settled round chip for the 24-round streak strip.
type StreakEntry struct {
	RoundSlot    uint64  `json:"roundSlot"`
	WinningRange uint8   `json:"winningRange"` // 0..4
	Code         string  `json:"code"`         // A..E
	MoveBps      int     `json:"moveBps"`
	PoolSOL      float64 `json:"poolSol"`
	OpenPriceUSD float64 `json:"openPriceUsd,omitempty"`
	ClosePrice   float64 `json:"closePriceUsd,omitempty"`
	InnerBps     int     `json:"innerBps,omitempty"`
	OuterBps     int     `json:"outerBps,omitempty"`
	MinerSat     float64 `json:"minerSat,omitempty"`
	UserStaked   bool    `json:"userStaked,omitempty"`
	UserWon      bool    `json:"userWon,omitempty"`
	SatReward    float64 `json:"satReward,omitempty"`
	SettledAt    int64   `json:"settledAt"`
}

// CandleBar represents one BTC/USD chart candle synced to 10m mining round windows.
type CandleBar struct {
	Time         int64   `json:"time"` // unix seconds
	Open         float64 `json:"open"`
	High         float64 `json:"high"`
	Low          float64 `json:"low"`
	Close        float64 `json:"close"`
	Volume       float64 `json:"volume"`
	RoundSlot    uint64  `json:"roundSlot,omitempty"`
	MoveBps      int     `json:"moveBps,omitempty"`
	WinningRange *uint8  `json:"winningRange,omitempty"`
}

// CandleResponse bundles chart candles and feed source metadata.
type CandleResponse struct {
	Interval string      `json:"interval"`
	Symbol   string      `json:"symbol"`
	Source   string      `json:"source"`
	Candles  []CandleBar `json:"candles"`
}

// SessionRecord tracks an active or closed local/managed LV1X mining session.
type SessionRecord struct {
	ID              string    `json:"id"`
	WalletID        string    `json:"walletId"`
	WalletHandle    string    `json:"walletHandle"`
	Cluster         string    `json:"cluster"`
	StrategyID      string    `json:"strategyId"`
	Mode            string    `json:"mode"`         // manual | semi_auto | auto
	Status          string    `json:"status"`       // active | paused | stopped | closed
	ScheduleMode    string    `json:"scheduleMode"` // every | choose | cycle | random
	HorizonLabel    string    `json:"horizonLabel"` // 4h | 12h | 24h | 7d | 30d
	StartMode       string    `json:"startMode"`    // now | next | plus2
	SelectedSlots   []uint64  `json:"selectedSlots,omitempty"`
	RoundsCount     int       `json:"roundsCount"`
	PerRoundSOL     float64   `json:"perRoundSol"`
	DepositSOL      float64   `json:"depositSol"`
	RollingBudget   bool      `json:"rollingBudget"`
	SkipRiskWarning string    `json:"skipRiskWarning,omitempty"`
	TotalCommitted  float64   `json:"totalCommittedSol"`
	TotalClaimedSOL float64   `json:"totalClaimedSol"`
	TotalClaimedSAT float64   `json:"totalClaimedSat"`
	RoundsPlayed    int       `json:"roundsPlayed"`
	AutoClaim       bool      `json:"autoClaim"`
	StopOnReversal  bool      `json:"stopOnReversal"`
	Curve           []float64 `json:"curve"`
	SessionRef      uint64    `json:"sessionRef,omitempty"`
	LastRoundSlot   uint64    `json:"lastRoundSlot,omitempty"`
	LastTxSignature string    `json:"lastTxSignature,omitempty"`
	LastDecision    string    `json:"lastDecision,omitempty"`
	CreatedAt       int64     `json:"createdAt"`
	UpdatedAt       int64     `json:"updatedAt"`
}

// ProtocolStats summarizes global Lean V1 protocol state and lifecycle phase.
type ProtocolStats struct {
	Cluster            string  `json:"cluster"`
	ProgramID          string  `json:"programId"`
	LedgerID           string  `json:"ledgerId"`
	RuntimeAddress     string  `json:"runtimeAddress"`
	MintAddress        string  `json:"mintAddress"`
	BTCMintAddress     string  `json:"btcMintAddress"`
	PoolAddress        string  `json:"poolAddress,omitempty"`
	StakingAddress     string  `json:"stakingAddress"`
	TreasuryAddress    string  `json:"treasuryAddress"`
	Phase              uint8   `json:"phase"`     // 0=bootstrap,1=normal,2=draining,3=winddown
	PhaseName          string  `json:"phaseName"` // Bootstrap | Graduated | Draining | Wind-down
	Paused             bool    `json:"paused"`
	BootstrapWinddown  bool    `json:"bootstrapWinddown"`
	MintedSat          float64 `json:"mintedSat"`
	MaxSupplySat       float64 `json:"maxSupplySat"`
	BTCTreasurySat     uint64  `json:"btcTreasurySat"`
	TotalStakedSat     float64 `json:"totalStakedSat"`
	StakedPct          float64 `json:"stakedPct"`
	Volume24hSOL       float64 `json:"volume24hSol"`
	LPSol              float64 `json:"lpSol"`
	LPSolMin           float64 `json:"lpSolMin"`
	LPSat              float64 `json:"lpSat"`
	LPSatMin           float64 `json:"lpSatMin"`
	BoostMultiplier    uint8   `json:"boostMultiplier"`
	BoostBudgetSat     uint32  `json:"boostBudgetSat"`
	BoostAwardedSat    float64 `json:"boostAwardedSat"`
	TGEBps             uint16  `json:"tgeBps"`
	VestingDurationSec uint32  `json:"vestingDurationSec"`
	GraduatedAt        int64   `json:"graduatedAt"`
	RoundIntervalSec   int64   `json:"roundIntervalSec"`
	Source             string  `json:"source"`
	ObservedAt         int64   `json:"observedAt"`
}

// LeaderboardMiner represents a single ranked miner entry.
type LeaderboardMiner struct {
	Address       string  `json:"address"`
	ShortAddress  string  `json:"shortAddress"`
	Status        string  `json:"status"` // winner | active | missed
	Rounds        int     `json:"rounds"`
	Wins          int     `json:"wins"`
	CommittedSOL  float64 `json:"committedSol"`
	ReturnedSOL   float64 `json:"returnedSol"`
	NetSOL        float64 `json:"netSol"`
	EarnedSAT     float64 `json:"earnedSat"`
	EarnedBTC     float64 `json:"earnedBtc"`
	LastRoundSlot uint64  `json:"lastRoundSlot"`
}

// LeaderboardRound captures one historical protocol round row.
type LeaderboardRound struct {
	RoundSlot    uint64     `json:"roundSlot"`
	Phase        string     `json:"phase"`
	WinningRange *uint8     `json:"winningRange,omitempty"`
	Code         string     `json:"code"`
	MoveBps      int        `json:"moveBps"`
	PoolSOL      float64    `json:"poolSol"`
	MinerCount   uint32     `json:"minerCount"`
	MinerSat     float64    `json:"minerSat"`
	CrowdShares  [5]float64 `json:"crowdShares"`
	SettledAt    int64      `json:"settledAt"`
}

// LeaderboardResponse powers #mine/assets (Overview | Miners | Rounds | Addresses).
type LeaderboardResponse struct {
	Cluster    string             `json:"cluster"`
	Window     string             `json:"window"` // 24h | 7d | 30d | all
	VolumeSOL  float64            `json:"volumeSol"`
	RoundsCnt  int                `json:"roundsCount"`
	MinersCnt  int                `json:"minersCount"`
	SatAwarded float64            `json:"satAwarded"`
	Miners     []LeaderboardMiner `json:"miners"`
	Rounds     []LeaderboardRound `json:"rounds"`
	Source     string             `json:"source"`
	ObservedAt int64              `json:"observedAt"`
}

// ClaimableRoundItem describes unclaimed SOL/SAT/BTC rewards on one round page entry.
type ClaimableRoundItem struct {
	RoundSlot    uint64  `json:"roundSlot"`
	ShardID      uint8   `json:"shardId"`
	Page         uint32  `json:"page"`
	EntryRow     uint8   `json:"entryRow"`
	EntriesMask  uint8   `json:"entriesMask"`
	Selection    uint8   `json:"selection"`
	SelectionCod string  `json:"selectionCode"`
	SolLamports  uint64  `json:"solLamports,string"`
	SatAtoms     uint64  `json:"satAtoms,string"`
	VestedAtoms  uint64  `json:"vestedSatAtoms,string"`
	BtcAtoms     uint64  `json:"btcAtoms,string"`
	SolClaimed   bool    `json:"solClaimed"`
	BtcClaimed   bool    `json:"btcClaimed"`
	StakedSOL    float64 `json:"stakedSol"`
}

// EntryHistoryItem records one commit/claim/stake action in local mining history.
type EntryHistoryItem struct {
	ID          string  `json:"id"`
	WalletID    string  `json:"walletId"`
	Cluster     string  `json:"cluster"`
	RoundSlot   uint64  `json:"roundSlot"`
	Kind        string  `json:"kind"` // commit | reveal | claim_sol | claim_sat | claim_btc | stake | unstake | claim_staking
	Selection   *uint8  `json:"selection,omitempty"`
	StrategyID  string  `json:"strategyId,omitempty"`
	AmountSOL   float64 `json:"amountSol,omitempty"`
	AmountSAT   float64 `json:"amountSat,omitempty"`
	AmountBTC   float64 `json:"amountBtc,omitempty"`
	TxSignature string  `json:"txSignature,omitempty"`
	Status      string  `json:"status"`
	CreatedAt   int64   `json:"createdAt"`
}

// PortfolioResponse aggregates claimable assets, vesting, balances, history, sessions, and policy.
type PortfolioResponse struct {
	Cluster            string               `json:"cluster"`
	WalletID           string               `json:"walletId"`
	WalletPublicKey    string               `json:"walletPublicKey"`
	SolBalanceSOL      float64              `json:"solBalanceSol"`
	SatBalance         float64              `json:"satBalance"`
	BtcBalance         float64              `json:"btcBalance"`
	ClaimableSOL       float64              `json:"claimableSol"`
	ClaimableSAT       float64              `json:"claimableSat"`
	VestedSAT          float64              `json:"vestedSat"`
	ClaimableBTC       float64              `json:"claimableBtc"`
	VestingProgressPct float64              `json:"vestingProgressPct"`
	VestingEndsAt      int64                `json:"vestingEndsAt"`
	ClaimItems         []ClaimableRoundItem `json:"claimItems"`
	History            []EntryHistoryItem   `json:"history"`
	Sessions           []SessionRecord      `json:"sessions"`
	Source             string               `json:"source"`
	ObservedAt         int64                `json:"observedAt"`
}

// StakingOverviewResponse aggregates protocol LV1X staking + user StakePosition.
type StakingOverviewResponse struct {
	Cluster             string  `json:"cluster"`
	StakingAddress      string  `json:"stakingAddress"`
	PositionAddress     string  `json:"positionAddress,omitempty"`
	Phase               uint8   `json:"phase"`
	Paused              bool    `json:"paused"`
	MinStakeSat         float64 `json:"minStakeSat"`
	TotalActiveSat      float64 `json:"totalActiveSat"`
	TotalPendingSat     float64 `json:"totalPendingSat"`
	TotalBtcDistributed float64 `json:"totalBtcDistributed"`
	StreamRemainingBtc  float64 `json:"streamRemainingBtc"`
	StreamEndTs         int64   `json:"streamEndTs"`
	UserActiveSat       float64 `json:"userActiveSat"`
	UserPendingSat      float64 `json:"userPendingSat"`
	PendingMatureAt     int64   `json:"pendingMatureAt"`
	CanActivate         bool    `json:"canActivate"`
	ClaimableBtc        float64 `json:"claimableBtc"`
	WalletSatBalance    float64 `json:"walletSatBalance"`
	WalletBtcBalance    float64 `json:"walletBtcBalance"`
	SharePct            float64 `json:"sharePct"`
	Source              string  `json:"source"`
	ObservedAt          int64   `json:"observedAt"`
}

// SimulateRequest runs the WEN exact near-miss simulator over settled rounds.
type SimulateRequest struct {
	BucketsBps  [5]uint16 `json:"bucketsBps"`
	Curve       []float64 `json:"curve,omitempty"`
	PerRoundSOL float64   `json:"perRoundSol"`
	Limit       int       `json:"limit,omitempty"`
}

// SimulateOutcome records one simulated round outcome.
type SimulateOutcome struct {
	RoundSlot    uint64  `json:"roundSlot"`
	WinningRange *uint8  `json:"winningRange,omitempty"`
	Kind         string  `json:"kind"` // exact | near | miss | void
	ReturnPct    float64 `json:"returnPct"`
}

// SimulateResponse summarizes near-miss curve simulation performance.
type SimulateResponse struct {
	Rounds         int               `json:"rounds"`
	ExactHitPct    float64           `json:"exactHitPct"`
	AvgCreditPct   float64           `json:"avgCreditPct"`
	RetainedPct    float64           `json:"retainedPct"`
	NetSOL         float64           `json:"netSol"`
	EstSat         float64           `json:"estSat"`
	TotalCommitted float64           `json:"totalCommitted"`
	Outcomes       []SimulateOutcome `json:"outcomes"`
}

// CommitRequest submits or simulates a commit into the active LV1X round.
type CommitRequest struct {
	WalletID      string    `json:"walletId"`
	Cluster       string    `json:"cluster,omitempty"`
	AmountSOL     float64   `json:"amountSol"`
	Selection     *uint8    `json:"selection,omitempty"` // explicit single bucket 0..4
	StrategyID    string    `json:"strategyId,omitempty"`
	Curve         []float64 `json:"curve,omitempty"` // 80-bar curve if customized
	ShardID       uint8     `json:"shardId"`
	Nonce         uint64    `json:"nonce,omitempty"`
	SaltHex       string    `json:"saltHex,omitempty"`
	Autonomous    bool      `json:"autonomous,omitempty"`
	TaskID        string    `json:"taskId,omitempty"`
	DryRunPreview bool      `json:"dryRunPreview,omitempty"`
	ReviewID      string    `json:"reviewId,omitempty"`
}

// RevealRequest submits an LV1X RevealBatch / RevealPageBatch tx.
type RevealRequest struct {
	WalletID      string `json:"walletId"`
	Cluster       string `json:"cluster,omitempty"`
	RoundSlot     uint64 `json:"roundSlot"`
	ShardID       uint8  `json:"shardId"`
	Page          uint32 `json:"page"`
	EntryRow      uint8  `json:"entryRow"`
	Selection     uint8  `json:"selection"`
	SaltHex       string `json:"saltHex"`
	Autonomous    bool   `json:"autonomous,omitempty"`
	TaskID        string `json:"taskId,omitempty"`
	DryRunPreview bool   `json:"dryRunPreview,omitempty"`
	ReviewID      string `json:"reviewId,omitempty"`
}

// ClaimRequest claims settled SOL, SAT, or cbBTC rewards for a wallet & round.
type ClaimRequest struct {
	WalletID      string `json:"walletId"`
	Cluster       string `json:"cluster,omitempty"`
	Asset         string `json:"asset,omitempty"` // sol | sat | btc (default sol)
	RoundSlot     uint64 `json:"roundSlot"`
	ShardID       uint8  `json:"shardId"`
	Page          uint32 `json:"page"`
	EntryRow      uint8  `json:"entryRow"`
	CreateATA     bool   `json:"createAta,omitempty"`
	Autonomous    bool   `json:"autonomous,omitempty"`
	TaskID        string `json:"taskId,omitempty"`
	DryRunPreview bool   `json:"dryRunPreview,omitempty"`
	ReviewID      string `json:"reviewId,omitempty"`
}

// ClaimAllRequest claims across SOL/SAT/BTC for the wallet's claimable rounds.
type ClaimAllRequest struct {
	WalletID string   `json:"walletId"`
	Cluster  string   `json:"cluster,omitempty"`
	Assets   []string `json:"assets,omitempty"` // default ["sol","sat","btc"]
}

// StakeActionRequest parameterizes Stake, ActivateStake, Unstake, or ClaimStakingReward.
type StakeActionRequest struct {
	WalletID      string  `json:"walletId"`
	Cluster       string  `json:"cluster,omitempty"`
	AmountSAT     float64 `json:"amountSat,omitempty"`
	CreateATA     bool    `json:"createAta,omitempty"`
	DryRunPreview bool    `json:"dryRunPreview,omitempty"`
	ReviewID      string  `json:"reviewId,omitempty"`
}

// CommitPreview estimates bucket weights, expected EV, and policy compliance before signing.
type CommitPreview struct {
	Cluster        string    `json:"cluster"`
	RoundSlot      uint64    `json:"roundSlot"`
	AmountSOL      float64   `json:"amountSol"`
	AmountLamports uint64    `json:"amountLamports,string"`
	DominantBucket uint8     `json:"dominantBucket"`
	DominantCode   string    `json:"dominantCode"`
	BucketWeights  [5]uint16 `json:"bucketWeightsBps"`
	ExpectedEVMult float64   `json:"expectedEvMultiple"`
	MainnetBlocked bool      `json:"mainnetBlocked"`
	MainnetReason  string    `json:"mainnetReason,omitempty"`
}

// MiningStatus bundles active cluster posture, round telemetry, streak, presets, and sessions.
type MiningStatus struct {
	Cluster         string           `json:"cluster"`
	MainnetEnabled  bool             `json:"mainnetEnabled"`
	MainnetGateNote string           `json:"mainnetGateNote"`
	Round           RoundTelemetry   `json:"round"`
	Streak          []StreakEntry    `json:"streak"`
	Presets         []StrategyPreset `json:"presets"`
	SavedStrategies []SavedStrategy  `json:"savedStrategies,omitempty"`
	Sessions        []SessionRecord  `json:"sessions"`
}
