package custody

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fased-ai/fased-app/internal/onchain"
)

const (
	CustodyNativeSigner = "native_signer"
	CustodyHardware     = "hardware_standard"
	CustodyWatch        = "watch_only"

	RoleVault   = "vault"
	RoleTrading = "trading"
	RoleAgent   = "agent"
	RoleMining  = "mining"
	RoleWatch   = "watch"

	ApprovalManualReview = "manual_review"
	ApprovalSemiAuto     = "semi_auto"
	ApprovalBoundedAuto  = "bounded_auto"

	ReviewStatusPrepared  = "prepared"
	ReviewStatusExecuted  = "executed"
	ReviewStatusCancelled = "cancelled"
	ReviewStatusExpired   = "expired"

	LedgerReserved  = "reserved"
	LedgerBroadcast = "broadcast"
	LedgerConfirmed = "confirmed"
	LedgerFailed    = "failed"
	LedgerUnknown   = "unknown"
)

var (
	ErrWalletNotFound    = errors.New("wallet not found")
	ErrReviewNotFound    = errors.New("review not found")
	ErrLedgerNotFound    = errors.New("ledger entry not found")
	ErrPolicyDenied      = errors.New("signer policy denied operation")
	ErrRoleDenied        = errors.New("wallet role prohibits operation")
	ErrPolicyConflict    = errors.New("wallet policy changed concurrently; refresh and retry")
	ErrWrongPassword     = errors.New("incorrect recovery passphrase or corrupt backup")
	ErrTxTampered        = errors.New("signed transaction payload does not match prepared review byte-for-byte")
	ErrAmbiguousState    = errors.New("unresolved broadcast state; reconcile existing entry before retrying")
	ErrOwnerAuthRequired = errors.New("signer owner authorization required")
)

// AssetCap defines fail-closed per-transaction and daily raw-unit caps for SOL or one SPL mint.
type AssetCap struct {
	Asset       string `json:"asset"` // "solana:native" or "solana:spl:<mint>"
	Mint        string `json:"mint,omitempty"`
	Symbol      string `json:"symbol,omitempty"`
	Decimals    uint8  `json:"decimals"`
	MaxPerTxRaw uint64 `json:"maxPerTxRaw,string"`
	MaxDailyRaw uint64 `json:"maxDailyRaw,string"`
}

// SkillGrant binds a specific skill to explicit wallet actions, mints, and caps.
type SkillGrant struct {
	SkillID          string   `json:"skillId"`
	Actions          []string `json:"actions"`
	AllowedMints     []string `json:"allowedMints,omitempty"`
	MaxPerTxUSD      float64  `json:"maxPerTxUsd,omitempty"`
	MaxSlippageBps   uint32   `json:"maxSlippageBps,omitempty"`
	ScheduledAllowed bool     `json:"scheduledAllowed"`
}

// WalletPolicy defines fail-closed signer-enforced authority bounds.
// Empty lists or zero caps mean DENY.
type WalletPolicy struct {
	Version           int        `json:"version"`
	PolicyHash        string     `json:"policyHash"`
	Role              string     `json:"role"`
	Frozen            bool       `json:"frozen"`
	FreezeReason      string     `json:"freezeReason,omitempty"`
	ApprovalMode      string     `json:"approvalMode"` // manual_review | semi_auto | bounded_auto
	AutoApproveMaxUSD float64    `json:"autoApproveMaxUsd,omitempty"`
	Operations        []string   `json:"operations"`
	Programs          []string   `json:"programs"`
	Destinations      []string   `json:"destinations"`
	SOLCap            AssetCap   `json:"solCap"`
	TokenCaps         []AssetCap `json:"tokenCaps"`
	MaxSlippageBps    uint32     `json:"maxSlippageBps"`
	DailyLossCapUSD   float64    `json:"dailyLossCapUsd,omitempty"`
	ExpiresAt         int64      `json:"expiresAt,omitempty"`
}

// WalletConnections tracks explicit opt-in bindings between a wallet and Fased agents/modules.
type WalletConnections struct {
	AgentIDs           []string     `json:"agentIds"`
	DefaultAgentWallet bool         `json:"defaultAgentWallet"`
	AllowTradeManual   bool         `json:"allowTradeManual"`
	AllowTradeBots     bool         `json:"allowTradeBots"`
	AllowedVenues      []string     `json:"allowedVenues"`
	SkillGrants        []SkillGrant `json:"skillGrants"`
	SweepToWalletID    string       `json:"sweepToWalletId,omitempty"`
}

// WalletRPCStatus projects sanitized network configuration (NEVER raw URLs or API keys).
type WalletRPCStatus struct {
	Configured    bool   `json:"configured"`
	Host          string `json:"host,omitempty"`
	Cluster       string `json:"cluster,omitempty"`
	GenesisHash   string `json:"genesisHash,omitempty"`
	WitnessHost   string `json:"witnessHost,omitempty"`
	WitnessStatus string `json:"witnessStatus,omitempty"` // verified | self-public-no-witness | custom
	FallbackHost  string `json:"fallbackHost,omitempty"`
	LatencyMs     int64  `json:"latencyMs,omitempty"`
	Version       int    `json:"version"`
}

// TokenDailyUsage reports signer-enforced daily spent & reserved raw units for one SPL mint.
type TokenDailyUsage struct {
	SpentRaw    uint64 `json:"spentRaw,string"`
	ReservedRaw uint64 `json:"reservedRaw,string"`
}

// WalletPublic is the ONLY wallet projection ever returned across IPC or HTTP.
type WalletPublic struct {
	ID                    string                     `json:"id"`
	Handle                string                     `json:"handle"`
	Name                  string                     `json:"name"`
	Custody               string                     `json:"custody"`
	Role                  string                     `json:"role"`
	PublicKey             string                     `json:"publicKey"`
	Policy                WalletPolicy               `json:"policy"`
	PolicyAcknowledged    bool                       `json:"policyAcknowledged"`
	Connections           WalletConnections          `json:"connections"`
	RPC                   WalletRPCStatus            `json:"rpc"`
	DailySpentLamports    uint64                     `json:"dailySpentLamports,string"`
	DailyReservedLamports uint64                     `json:"dailyReservedLamports,string"`
	DailyTokenUsage       map[string]TokenDailyUsage `json:"dailyTokenUsage,omitempty"`
	DayUTC                string                     `json:"dayUtc,omitempty"`
	CreatedAt             int64                      `json:"createdAt"`
	UpdatedAt             int64                      `json:"updatedAt"`
}

// WalletReview is an immutable prepared & simulated transaction authorization request.
type WalletReview struct {
	ID                   string   `json:"id"`
	WalletID             string   `json:"walletId"`
	Role                 string   `json:"role"`
	Custody              string   `json:"custody"`
	Operation            string   `json:"operation"`
	Programs             []string `json:"programs"`
	Destination          string   `json:"destination"`
	Mint                 string   `json:"mint,omitempty"`
	Symbol               string   `json:"symbol"`
	AmountRaw            uint64   `json:"amountRaw,string"`
	AmountHuman          string   `json:"amountHuman"`
	EstimatedFeeLamports uint64   `json:"estimatedFeeLamports,string"`
	PolicyVersion        int      `json:"policyVersion"`
	PolicyHash           string   `json:"policyHash"`
	TxDigest             string   `json:"txDigest"`
	UnsignedTxBase64     string   `json:"unsignedTxBase64"`
	UnsignedMsgBase64    string   `json:"unsignedMsgBase64"`
	SimulationOK         bool     `json:"simulationOk"`
	SimulationError      string   `json:"simulationError,omitempty"`
	IsBotOrAuto          bool     `json:"isBotOrAuto,omitempty"`
	AgentID              string   `json:"agentId,omitempty"`
	SkillGrantID         string   `json:"skillGrantId,omitempty"`
	Venue                string   `json:"venue,omitempty"`
	ExpiresAt            int64    `json:"expiresAt"`
	Status               string   `json:"status"`
	CreatedAt            int64    `json:"createdAt"`
}

// LedgerEntry records every durable pre-sign reservation and broadcast state.
type LedgerEntry struct {
	ID          string `json:"id"`
	ReviewID    string `json:"reviewId"`
	WalletID    string `json:"walletId"`
	Operation   string `json:"operation"`
	Destination string `json:"destination"`
	Asset       string `json:"asset"`
	AmountRaw   uint64 `json:"amountRaw,string"`
	State       string `json:"state"`
	TxSignature string `json:"txSignature,omitempty"`
	PolicyHash  string `json:"policyHash"`
	Error       string `json:"error,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// AuditEntry records tamper-evident hash-chained custody and policy lifecycle changes.
type AuditEntry struct {
	ID         string `json:"id"`
	WalletID   string `json:"walletId"`
	Action     string `json:"action"`
	Actor      string `json:"actor"`
	Summary    string `json:"summary"`
	PolicyHash string `json:"policyHash,omitempty"`
	Timestamp  int64  `json:"timestamp"`
	PrevHash   string `json:"prevHash,omitempty"`
	EntryHash  string `json:"entryHash,omitempty"`
}

// SecurityStatus reports the signer process isolation & master-key protection posture.
type SecurityStatus struct {
	ProcessIsolated   bool   `json:"processIsolated"`
	KEKProtection     string `json:"kekProtection"` // systemd-creds | os-keychain | file-dev-only
	OwnerAuthEnforced bool   `json:"ownerAuthEnforced"`
	SocketPath        string `json:"socketPath,omitempty"`
	AllowedPeerUID    int    `json:"allowedPeerUid,omitempty"`
}

// WalletSelector resolves a wallet deterministically according to the selection contract.
type WalletSelector struct {
	WalletHandle string `json:"walletHandle,omitempty"`
	WalletID     string `json:"walletId,omitempty"`
	SkillGrantID string `json:"skillGrantId,omitempty"`
	AgentID      string `json:"agentId,omitempty"`
	IsAutonomous bool   `json:"isAutonomous,omitempty"`
	Venue        string `json:"venue,omitempty"`
}

// PrepareReviewRequest builds and validates a transaction against wallet policy.
type PrepareReviewRequest struct {
	WalletID      string         `json:"walletId"`
	Operation     string         `json:"operation"`   // solana.nativeTransfer | solana.splTransferChecked | jupiter.swap | trade.order
	Destination   string         `json:"destination"` // recipient Base58 or @wallet:<id>
	Mint          string         `json:"mint,omitempty"`
	OutputMint    string         `json:"outputMint,omitempty"`
	Decimals      uint8          `json:"decimals,omitempty"`
	SlippageBps   uint32         `json:"slippageBps,omitempty"`
	CreateDestATA bool           `json:"createDestAta,omitempty"`
	AmountHuman   string         `json:"amountHuman,omitempty"`
	AmountRaw     uint64         `json:"amountRaw,string,omitempty"`
	IsBotOrAuto   bool           `json:"isBotOrAuto,omitempty"`
	AgentID       string         `json:"agentId,omitempty"`
	SkillGrantID  string         `json:"skillGrantId,omitempty"`
	Venue         string         `json:"venue,omitempty"`
	JupiterQuote  *onchain.Quote `json:"jupiterQuote,omitempty"`
}

// TokenBalance holds a single SPL token account balance queried via signer RPC.
type TokenBalance struct {
	Mint        string `json:"mint"`
	AmountRaw   uint64 `json:"amountRaw,string"`
	Decimals    uint8  `json:"decimals"`
	AmountHuman string `json:"amountHuman"`
}

// BalanceResult holds signer-queried SOL and SPL token balance metrics.
type BalanceResult struct {
	WalletID  string         `json:"walletId"`
	PublicKey string         `json:"publicKey"`
	Cluster   string         `json:"cluster"`
	RPCHost   string         `json:"rpcHost"`
	Lamports  uint64         `json:"lamports,string"`
	SOL       string         `json:"sol"`
	Tokens    []TokenBalance `json:"tokens,omitempty"`
	LatencyMs int64          `json:"latencyMs"`
}

// RecoveryPackage is an Argon2id + AES-256-GCM encrypted backup safe for Local & VPS portability.
type RecoveryPackage struct {
	Version    int    `json:"version"`
	Format     string `json:"format"`
	WalletID   string `json:"walletId"`
	Role       string `json:"role"`
	PublicKey  string `json:"publicKey"`
	KDF        string `json:"kdf"`
	KDFTime    uint32 `json:"kdfTime"`
	KDFMemory  uint32 `json:"kdfMemory"`
	KDFThreads uint8  `json:"kdfThreads"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// ValidRole returns true for supported permanent wallet roles.
func ValidRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleVault, RoleTrading, RoleAgent, RoleMining, RoleWatch:
		return true
	default:
		return false
	}
}

// ValidCustody returns true for supported custody backends.
func ValidCustody(c string) bool {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case CustodyNativeSigner, CustodyHardware, CustodyWatch:
		return true
	default:
		return false
	}
}

// DefaultBaselinePolicy returns a strict fail-closed read-only policy for a newly created wallet.
func DefaultBaselinePolicy(role string) WalletPolicy {
	cleanRole := strings.ToLower(strings.TrimSpace(role))
	if !ValidRole(cleanRole) {
		cleanRole = RoleTrading
	}
	p := WalletPolicy{
		Version:      1,
		Role:         cleanRole,
		Frozen:       false,
		ApprovalMode: ApprovalManualReview,
		Operations:   []string{},
		Programs:     []string{},
		Destinations: []string{},
		SOLCap: AssetCap{
			Asset:    "solana:native",
			Symbol:   "SOL",
			Decimals: 9,
		},
		TokenCaps:      []AssetCap{},
		MaxSlippageBps: 50,
	}
	p.PolicyHash = ComputePolicyHash(p)
	return p
}

// DefaultConnections returns zero-authority default connections (all OFF by default).
func DefaultConnections() WalletConnections {
	return WalletConnections{
		AgentIDs:      []string{},
		AllowedVenues: []string{},
		SkillGrants:   []SkillGrant{},
	}
}

// ComputePolicyHash computes a canonical SHA-256 digest of a normalized WalletPolicy.
func ComputePolicyHash(p WalletPolicy) string {
	norm := NormalizePolicy(p)
	norm.PolicyHash = ""
	raw, _ := json.Marshal(norm)
	sum := sha256.Sum256(append([]byte("fased-custody-policy:v1\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

// NormalizePolicy canonicalizes slices and enforces strict role invariants.
func NormalizePolicy(p WalletPolicy) WalletPolicy {
	out := p
	out.Role = strings.ToLower(strings.TrimSpace(out.Role))
	if out.Version < 1 {
		out.Version = 1
	}
	if out.ApprovalMode == "" {
		out.ApprovalMode = ApprovalManualReview
	}
	// Hard invariant: Vault and Watch wallets NEVER run autonomous/semi-auto execution.
	if out.Role == RoleVault || out.Role == RoleWatch {
		out.ApprovalMode = ApprovalManualReview
		out.AutoApproveMaxUSD = 0
	}
	if out.Operations == nil {
		out.Operations = []string{}
	} else {
		out.Operations = uniqueSortedTrimmed(out.Operations, false)
	}
	if out.Programs == nil {
		out.Programs = []string{}
	} else {
		out.Programs = uniqueSortedTrimmed(out.Programs, false)
	}
	if out.Destinations == nil {
		out.Destinations = []string{}
	} else {
		out.Destinations = uniqueSortedTrimmed(out.Destinations, false)
	}
	if out.SOLCap.Asset == "" {
		out.SOLCap.Asset = "solana:native"
		out.SOLCap.Symbol = "SOL"
		out.SOLCap.Decimals = 9
	}
	if out.SOLCap.MaxDailyRaw > 0 && out.SOLCap.MaxPerTxRaw > out.SOLCap.MaxDailyRaw {
		out.SOLCap.MaxPerTxRaw = out.SOLCap.MaxDailyRaw
	}
	if out.TokenCaps == nil {
		out.TokenCaps = []AssetCap{}
	} else {
		normCaps := make([]AssetCap, len(out.TokenCaps))
		for i, tc := range out.TokenCaps {
			tc.Mint = strings.TrimSpace(tc.Mint)
			tc.Symbol = strings.TrimSpace(tc.Symbol)
			normCaps[i] = tc
		}
		out.TokenCaps = normCaps
	}
	out.FreezeReason = strings.TrimSpace(out.FreezeReason)
	return out
}

// IsPolicyTighterOrEqual returns true when next never expands authority relative to prev.
func IsPolicyTighterOrEqual(prevRaw, nextRaw WalletPolicy) bool {
	prev := NormalizePolicy(prevRaw)
	next := NormalizePolicy(nextRaw)
	if prev.Role != next.Role {
		return false
	}
	if prev.Frozen && !next.Frozen {
		return false
	}
	if approvalRank(next.ApprovalMode) > approvalRank(prev.ApprovalMode) {
		return false
	}
	if next.AutoApproveMaxUSD > prev.AutoApproveMaxUSD {
		return false
	}
	if !isSubset(next.Operations, prev.Operations) {
		return false
	}
	if !isSubset(next.Programs, prev.Programs) {
		return false
	}
	if !isSubset(next.Destinations, prev.Destinations) {
		return false
	}
	if next.SOLCap.MaxPerTxRaw > prev.SOLCap.MaxPerTxRaw || next.SOLCap.MaxDailyRaw > prev.SOLCap.MaxDailyRaw {
		return false
	}
	prevTokens := map[string]AssetCap{}
	for _, tc := range prev.TokenCaps {
		prevTokens[strings.TrimSpace(tc.Mint)] = tc
	}
	for _, tc := range next.TokenCaps {
		ptc, ok := prevTokens[strings.TrimSpace(tc.Mint)]
		if !ok {
			return false
		}
		if tc.Decimals != ptc.Decimals {
			return false
		}
		if tc.MaxPerTxRaw > ptc.MaxPerTxRaw || tc.MaxDailyRaw > ptc.MaxDailyRaw {
			return false
		}
	}
	if next.MaxSlippageBps > prev.MaxSlippageBps {
		return false
	}
	if next.DailyLossCapUSD > prev.DailyLossCapUSD {
		return false
	}
	if prev.ExpiresAt > 0 && (next.ExpiresAt == 0 || next.ExpiresAt > prev.ExpiresAt) {
		return false
	}
	return true
}

// ValidateConnectionsForRole enforces strict isolation between Vault/Watch and autonomous modules.
func ValidateConnectionsForRole(role string, conn WalletConnections) (WalletConnections, error) {
	out := conn
	out.AgentIDs = uniqueSortedTrimmed(out.AgentIDs, false)
	out.AllowedVenues = uniqueSortedTrimmed(out.AllowedVenues, true)
	if out.SkillGrants == nil {
		out.SkillGrants = []SkillGrant{}
	}
	out.SweepToWalletID = strings.TrimSpace(out.SweepToWalletID)
	switch role {
	case RoleWatch:
		if len(out.AgentIDs) > 0 || out.DefaultAgentWallet || out.AllowTradeManual || out.AllowTradeBots || len(out.AllowedVenues) > 0 || len(out.SkillGrants) > 0 {
			return WalletConnections{}, fmt.Errorf("%w: watch-only wallet cannot be connected to agents, bots, or trading", ErrRoleDenied)
		}
	case RoleVault:
		if len(out.AgentIDs) > 0 || out.DefaultAgentWallet || out.AllowTradeBots || len(out.SkillGrants) > 0 {
			return WalletConnections{}, fmt.Errorf("%w: vault wallet cannot be assigned to autonomous agents, bots, or skills", ErrRoleDenied)
		}
	case RoleMining:
		if len(out.AgentIDs) > 0 || out.DefaultAgentWallet || out.AllowTradeManual || out.AllowTradeBots {
			return WalletConnections{}, fmt.Errorf("%w: mining wallet cannot enter generic agent or trading routing", ErrRoleDenied)
		}
	}
	return out, nil
}

func approvalRank(mode string) int {
	switch mode {
	case ApprovalManualReview:
		return 0
	case ApprovalSemiAuto:
		return 1
	case ApprovalBoundedAuto:
		return 2
	default:
		return 0
	}
}

func isSubset(sub, sup []string) bool {
	set := make(map[string]bool, len(sup))
	for _, s := range sup {
		set[s] = true
	}
	for _, s := range sub {
		if !set[s] {
			return false
		}
	}
	return true
}

func uniqueSortedTrimmed(src []string, lower bool) []string {
	seen := make(map[string]bool, len(src))
	out := make([]string, 0, len(src))
	for _, item := range src {
		v := strings.TrimSpace(item)
		if lower {
			v = strings.ToLower(v)
		}
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
