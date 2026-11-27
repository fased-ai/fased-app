package wenlv1x

// StrategyPreset defines a named 80-range distribution shape matching WEN's allocation curve.
type StrategyPreset struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Mode        string    `json:"mode"` // neutral | directional | defensive
	Buckets     [5]uint16 `json:"bucketsBps"`
	Curve       []float64 `json:"curve"` // 80 normalized bars summing to 1.0
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
	UserStaked   bool    `json:"userStaked,omitempty"`
	UserWon      bool    `json:"userWon,omitempty"`
	SatReward    float64 `json:"satReward,omitempty"`
	SettledAt    int64   `json:"settledAt"`
}

// SessionRecord tracks an active or closed local/managed LV1X mining session.
type SessionRecord struct {
	ID              string    `json:"id"`
	WalletID        string    `json:"walletId"`
	WalletHandle    string    `json:"walletHandle"`
	Cluster         string    `json:"cluster"`
	StrategyID      string    `json:"strategyId"`
	Mode            string    `json:"mode"`   // manual | semi_auto | auto
	Status          string    `json:"status"` // active | paused | stopped | closed
	PerRoundSOL     float64   `json:"perRoundSol"`
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

// CommitRequest submits or simulates a commit into the active LV1X round.
type CommitRequest struct {
	WalletID      string    `json:"walletId"`
	Cluster       string    `json:"cluster,omitempty"`
	AmountSOL     float64   `json:"amountSol"`
	Selection     *uint8    `json:"selection,omitempty"` // explicit single bucket 0..4
	StrategyID    string    `json:"strategyId,omitempty"`
	Curve         []float64 `json:"curve,omitempty"` // 80-bar curve if customized
	ShardID       uint8     `json:"shardId"`
	Autonomous    bool      `json:"autonomous,omitempty"`
	TaskID        string    `json:"taskId,omitempty"`
	DryRunPreview bool      `json:"dryRunPreview,omitempty"`
}

// ClaimRequest claims settled SOL/SAT rewards for a wallet & round.
type ClaimRequest struct {
	WalletID   string `json:"walletId"`
	RoundSlot  uint64 `json:"roundSlot"`
	ShardID    uint8  `json:"shardId"`
	Page       uint32 `json:"page"`
	Autonomous bool   `json:"autonomous,omitempty"`
	TaskID     string `json:"taskId,omitempty"`
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
	Sessions        []SessionRecord  `json:"sessions"`
}
