package custody

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/fased-ai/fased-app/internal/onchain"
)

const (
	MiningOpCommit  = "mining.lv1x.commit"
	MiningOpClaim   = "mining.lv1x.claim"
	MiningOpSession = "mining.lv1x.session"
)

// MiningPolicy bounds Lean V1 (LV1X) mining authority on RoleMining wallets.
// Devnet uses the pinned LV1X program/ledger by default; Mainnet stays strictly
// gated behind AllowMainnet && non-empty ProgramID && ExpectedELFSHA256 verification.
type MiningPolicy struct {
	Cluster               string   `json:"cluster"` // devnet | mainnet-beta
	ProgramID             string   `json:"programId"`
	LedgerID              string   `json:"ledgerId"`
	ExpectedELFSHA256     string   `json:"expectedElfSha256"`
	MaxSOLPerRoundLamport uint64   `json:"maxSolPerRoundLamports,string"`
	MaxDailySOLLamports   uint64   `json:"maxDailySolLamports,string"`
	AllowedStrategies     []string `json:"allowedStrategies"`
	AutoClaim             bool     `json:"autoClaim"`
	AllowMainnet          bool     `json:"allowMainnet,omitempty"`
}

// MiningLV1XRequest parameterizes a mining.lv1x.{commit,claim,session} review.
type MiningLV1XRequest struct {
	Cluster        string `json:"cluster,omitempty"`
	ProgramID      string `json:"programId,omitempty"`
	LedgerID       string `json:"ledgerId,omitempty"`
	RoundSlot      uint64 `json:"roundSlot,string"`
	ShardID        uint8  `json:"shardId"`
	Page           uint32 `json:"page"`
	EntryIndex     uint32 `json:"entryIndex"`
	Nonce          uint64 `json:"nonce,string"`
	Selection      uint8  `json:"selection"` // 0..4 (A..E bucket)
	AmountLamports uint64 `json:"amountLamports,string"`
	SaltHex        string `json:"saltHex,omitempty"`
	EntriesMask    uint8  `json:"entriesMask,omitempty"`
	SessionAction  string `json:"sessionAction,omitempty"` // open | stop | close
	SessionRef     uint64 `json:"sessionRef,string,omitempty"`
	MaxEntry       uint64 `json:"maxEntry,string,omitempty"`
	MaxTurnover    uint64 `json:"maxTurnover,string,omitempty"`
	FirstSlot      uint64 `json:"firstSlot,string,omitempty"`
	LastSlot       uint64 `json:"lastSlot,string,omitempty"`
	ExpiresAt      int64  `json:"expiresAt,omitempty"`
	StrategyID     string `json:"strategyId,omitempty"`
}

// isMiningOperation returns true when op is one of the LV1X native mining actions.
func isMiningOperation(op string) bool {
	switch op {
	case MiningOpCommit, MiningOpClaim, MiningOpSession:
		return true
	default:
		return false
	}
}

func NormalizeMiningPolicy(m *MiningPolicy) *MiningPolicy {
	if m == nil {
		return nil
	}
	cp := *m
	cp.Cluster = strings.ToLower(strings.TrimSpace(cp.Cluster))
	if cp.Cluster == "" {
		cp.Cluster = "devnet"
	}
	cp.ProgramID = strings.TrimSpace(cp.ProgramID)
	cp.LedgerID = strings.TrimSpace(cp.LedgerID)
	cp.ExpectedELFSHA256 = strings.ToLower(strings.TrimSpace(cp.ExpectedELFSHA256))
	cp.AllowedStrategies = uniqueSortedTrimmed(cp.AllowedStrategies, true)
	if cp.Cluster == "devnet" {
		if cp.ProgramID == "" {
			cp.ProgramID = onchain.LV1XDevnetProgramID
		}
		if cp.LedgerID == "" {
			cp.LedgerID = onchain.LV1XDevnetLedgerID
		}
		if cp.ExpectedELFSHA256 == "" {
			cp.ExpectedELFSHA256 = onchain.LV1XDevnetELFSHA256
		}
	}
	return &cp
}

func ValidateMiningPolicyForUpdate(m *MiningPolicy) error {
	if m == nil {
		return nil
	}
	norm := NormalizeMiningPolicy(m)
	switch norm.Cluster {
	case "devnet":
		if norm.ProgramID != onchain.LV1XDevnetProgramID {
			return fmt.Errorf("%w: devnet LV1X program must match pinned %s", ErrPolicyDenied, onchain.LV1XDevnetProgramID)
		}
		if norm.LedgerID != onchain.LV1XDevnetLedgerID {
			return fmt.Errorf("%w: devnet LV1X ledger must match pinned %s", ErrPolicyDenied, onchain.LV1XDevnetLedgerID)
		}
	case "mainnet-beta":
		if onchain.LV1XMainnetProgramID == "" || !norm.AllowMainnet {
			return fmt.Errorf("%w: mainnet LV1X mining is disabled until official Mainnet deployment pins are published", ErrPolicyDenied)
		}
		if norm.ProgramID != onchain.LV1XMainnetProgramID || norm.ExpectedELFSHA256 == "" {
			return fmt.Errorf("%w: mainnet LV1X requires pinned program ID and expected ELF SHA-256", ErrPolicyDenied)
		}
	default:
		return fmt.Errorf("%w: unsupported LV1X mining cluster %q", ErrPolicyDenied, norm.Cluster)
	}
	if _, _, err := ValidateSolanaPublicAddress(norm.ProgramID); err != nil {
		return fmt.Errorf("invalid LV1X program ID: %w", err)
	}
	if _, _, err := ValidateSolanaPublicAddress(norm.LedgerID); err != nil {
		return fmt.Errorf("invalid LV1X ledger ID: %w", err)
	}
	if len(norm.ExpectedELFSHA256) != 64 {
		return errors.New("LV1X expectedElfSha256 must be 64-char hex")
	}
	if _, err := hex.DecodeString(norm.ExpectedELFSHA256); err != nil {
		return errors.New("LV1X expectedElfSha256 must be valid lowercase hex")
	}
	if norm.MaxDailySOLLamports > 0 && norm.MaxSOLPerRoundLamport > norm.MaxDailySOLLamports {
		return errors.New("maxSolPerRoundLamports cannot exceed maxDailySolLamports")
	}
	return nil
}

func IsMiningPolicyTighterOrEqual(prev, next *MiningPolicy) bool {
	if prev == nil && next == nil {
		return true
	}
	if prev == nil && next != nil {
		// Adding a MiningPolicy expands capability surface -> require explicit owner confirm.
		return false
	}
	if prev != nil && next == nil {
		return true
	}
	p := NormalizeMiningPolicy(prev)
	n := NormalizeMiningPolicy(next)
	if p.Cluster != n.Cluster || p.ProgramID != n.ProgramID || p.LedgerID != n.LedgerID || p.ExpectedELFSHA256 != n.ExpectedELFSHA256 {
		return false
	}
	if !p.AllowMainnet && n.AllowMainnet {
		return false
	}
	if !p.AutoClaim && n.AutoClaim {
		return false
	}
	if n.MaxSOLPerRoundLamport > p.MaxSOLPerRoundLamport || n.MaxDailySOLLamports > p.MaxDailySOLLamports {
		return false
	}
	if !isSubset(n.AllowedStrategies, p.AllowedStrategies) {
		return false
	}
	return true
}

func allowMiningAutonomousExecution(w WalletPublic, rev WalletReview, actor string) bool {
	if w.Role != RoleMining || w.Custody != CustodyNativeSigner || w.Policy.Frozen {
		return false
	}
	if !isMiningOperation(rev.Operation) || !rev.IsBotOrAuto {
		return false
	}
	if rev.ConnectionsHash == "" || rev.ConnectionsHash != ComputeConnectionsHash(w.Connections) {
		return false
	}
	normM := NormalizeMiningPolicy(w.Policy.Mining)
	if normM == nil {
		return false
	}
	if normM.Cluster == "mainnet-beta" && (onchain.LV1XMainnetProgramID == "" || !normM.AllowMainnet) {
		return false
	}
	actorClean := strings.TrimSpace(actor)
	if actorClean != "mining-engine" && !strings.HasPrefix(actorClean, "task:") {
		return false
	}
	// Semi-Auto permits unattended claim transactions when AutoClaim is enabled;
	// BoundedAuto allows bounded commits and claims within SOL + USD ceilings.
	if w.Policy.ApprovalMode == ApprovalManualReview {
		return false
	}
	if w.Policy.ApprovalMode == ApprovalSemiAuto {
		return rev.Operation == MiningOpClaim && normM.AutoClaim
	}
	if w.Policy.ApprovalMode != ApprovalBoundedAuto {
		return false
	}
	if rev.Operation == MiningOpClaim {
		return normM.AutoClaim
	}
	if w.Policy.AutoApproveMaxUSD <= 0 || rev.EstimatedUSD <= 0 || rev.EstimatedUSD > w.Policy.AutoApproveMaxUSD {
		return false
	}
	if normM.MaxSOLPerRoundLamport > 0 && rev.AmountRaw > normM.MaxSOLPerRoundLamport {
		return false
	}
	return true
}

func (s *Service) checkMiningRoundCapLocked(ctx context.Context, w WalletPublic, cluster string, roundSlot uint64, amountLamports uint64) error {
	normM := NormalizeMiningPolicy(w.Policy.Mining)
	if normM == nil {
		return fmt.Errorf("%w: mining policy required", ErrPolicyDenied)
	}
	if normM.MaxSOLPerRoundLamport == 0 || amountLamports > normM.MaxSOLPerRoundLamport {
		return fmt.Errorf("%w: mining round amount exceeds maxSolPerRoundLamports", ErrPolicyDenied)
	}
	var spent, reserved uint64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT spent_lamports, reserved_lamports FROM mining_round_usage WHERE wallet_id = ? AND cluster = ? AND round_slot = ?`,
		w.ID, cluster, int64(roundSlot),
	).Scan(&spent, &reserved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if spent > math.MaxUint64-reserved || spent+reserved > math.MaxUint64-amountLamports || spent+reserved+amountLamports > normM.MaxSOLPerRoundLamport {
		return fmt.Errorf("%w: round %d mining cap exhausted", ErrPolicyDenied, roundSlot)
	}
	return nil
}

func (s *Service) reserveMiningRoundTxLocked(ctx context.Context, tx *sql.Tx, walletID, cluster string, roundSlot uint64, amountLamports, roundCap uint64) error {
	if roundCap == 0 {
		return fmt.Errorf("%w: mining round cap must be positive", ErrPolicyDenied)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT OR IGNORE INTO mining_round_usage(wallet_id, cluster, round_slot, spent_lamports, reserved_lamports) VALUES (?, ?, ?, 0, 0)`,
		walletID, cluster, int64(roundSlot),
	); err != nil {
		return err
	}
	var spent, reserved uint64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT spent_lamports, reserved_lamports FROM mining_round_usage WHERE wallet_id = ? AND cluster = ? AND round_slot = ?`,
		walletID, cluster, int64(roundSlot),
	).Scan(&spent, &reserved); err != nil {
		return err
	}
	if spent > math.MaxUint64-reserved || spent+reserved > math.MaxUint64-amountLamports || spent+reserved+amountLamports > roundCap {
		return fmt.Errorf("%w: round %d mining cap exhausted during reservation", ErrPolicyDenied, roundSlot)
	}
	_, err := tx.ExecContext(
		ctx,
		`UPDATE mining_round_usage SET reserved_lamports = reserved_lamports + ? WHERE wallet_id = ? AND cluster = ? AND round_slot = ?`,
		amountLamports, walletID, cluster, int64(roundSlot),
	)
	return err
}

func (s *Service) settleMiningRoundTxLocked(ctx context.Context, tx *sql.Tx, walletID, cluster string, roundSlot uint64, amountLamports uint64, confirmed bool) error {
	if confirmed {
		_, err := tx.ExecContext(
			ctx,
			`UPDATE mining_round_usage SET reserved_lamports = MAX(0, reserved_lamports - ?), spent_lamports = spent_lamports + ? WHERE wallet_id = ? AND cluster = ? AND round_slot = ?`,
			amountLamports, amountLamports, walletID, cluster, int64(roundSlot),
		)
		return err
	}
	_, err := tx.ExecContext(
		ctx,
		`UPDATE mining_round_usage SET reserved_lamports = MAX(0, reserved_lamports - ?) WHERE wallet_id = ? AND cluster = ? AND round_slot = ?`,
		amountLamports, walletID, cluster, int64(roundSlot),
	)
	return err
}

type preparedMiningDetails struct {
	Cluster        string
	ProgramID      string
	LedgerID       string
	RoundSlot      uint64
	ShardID        uint8
	Page           uint32
	EntryIndex     uint32
	Nonce          uint64
	Selection      uint8
	Salt           [32]byte
	AmountLamports uint64
	EntriesMask    uint8
	SessionAction  string
	SessionRef     uint64
	FirstSlot      uint64
	LastSlot       uint64
	ExpiresAt      int64
	MaxEntry       uint64
	MaxTurnover    uint64
	EstFeeLamports uint64
	EstimatedUSD   float64
	DestAddr       string
	Symbol         string
	HumanAmount    string
}

func (s *Service) validateMiningPrepareLocked(ctx context.Context, w WalletPublic, op string, req PrepareReviewRequest) (preparedMiningDetails, error) {
	var out preparedMiningDetails
	if w.Role != RoleMining {
		return out, fmt.Errorf("%w: operation %q requires a mining role wallet", ErrRoleDenied, op)
	}
	if w.Policy.Mining == nil {
		return out, fmt.Errorf("%w: wallet %s lacks MiningPolicy configuration", ErrPolicyDenied, w.Handle)
	}
	mPol := NormalizeMiningPolicy(w.Policy.Mining)
	if err := ValidateMiningPolicyForUpdate(mPol); err != nil {
		return out, err
	}
	if req.MiningLV1X == nil {
		return out, fmt.Errorf("%w: miningLv1x request parameters required", ErrPolicyDenied)
	}
	m := *req.MiningLV1X

	cluster := strings.ToLower(strings.TrimSpace(firstStr(m.Cluster, mPol.Cluster)))
	if cluster != mPol.Cluster {
		return out, fmt.Errorf("%w: cluster %q does not match pinned wallet mining cluster %q", ErrPolicyDenied, cluster, mPol.Cluster)
	}
	if cluster == "mainnet-beta" && (onchain.LV1XMainnetProgramID == "" || !mPol.AllowMainnet) {
		return out, fmt.Errorf("%w: Mainnet LV1X mining is gated off", ErrPolicyDenied)
	}
	progID := firstStr(strings.TrimSpace(m.ProgramID), mPol.ProgramID)
	ledgerID := firstStr(strings.TrimSpace(m.LedgerID), mPol.LedgerID)
	if progID != mPol.ProgramID || ledgerID != mPol.LedgerID {
		return out, fmt.Errorf("%w: LV1X program/ledger mismatch policy pin", ErrPolicyDenied)
	}
	if !slices.Contains(w.Policy.Programs, progID) {
		return out, fmt.Errorf("%w: LV1X program %s must be allowed in wallet policy", ErrPolicyDenied, progID)
	}
	if strat := strings.ToLower(strings.TrimSpace(m.StrategyID)); strat != "" && len(mPol.AllowedStrategies) > 0 {
		if !slices.Contains(mPol.AllowedStrategies, strat) {
			return out, fmt.Errorf("%w: strategy %q is not in AllowedStrategies", ErrPolicyDenied, strat)
		}
	}

	out.Cluster = cluster
	out.ProgramID = progID
	out.LedgerID = ledgerID
	out.RoundSlot = m.RoundSlot
	out.ShardID = m.ShardID
	if out.ShardID == 0 && m.EntryIndex == 0 && op == MiningOpCommit {
		// Permit explicit 0..31 shardId or 255 unsharded; leave as supplied.
	}
	if out.ShardID != onchain.LV1XUnshardedShardID && int(out.ShardID) >= onchain.LV1XMaxShards {
		return out, fmt.Errorf("%w: invalid shard id %d", ErrPolicyDenied, out.ShardID)
	}
	out.Page = m.Page
	out.EntryIndex = m.EntryIndex
	out.Nonce = m.Nonce
	out.Selection = m.Selection
	out.EntriesMask = m.EntriesMask
	out.EstFeeLamports = 10_000

	switch op {
	case MiningOpCommit:
		if out.Selection > 4 {
			return out, fmt.Errorf("%w: LV1X selection bucket must be 0..4", ErrPolicyDenied)
		}
		amt := m.AmountLamports
		if amt == 0 {
			amt = req.AmountRaw
		}
		if amt < onchain.LV1XMinCommitLamports {
			return out, fmt.Errorf("%w: commit %d lamports below minimum %d", ErrPolicyDenied, amt, onchain.LV1XMinCommitLamports)
		}
		if w.Policy.SOLCap.MaxPerTxRaw == 0 || amt+out.EstFeeLamports > w.Policy.SOLCap.MaxPerTxRaw {
			return out, fmt.Errorf("%w: commit+fee exceeds per-tx SOL cap", ErrPolicyDenied)
		}
		dailyMax := w.Policy.SOLCap.MaxDailyRaw
		if mPol.MaxDailySOLLamports > 0 && (dailyMax == 0 || mPol.MaxDailySOLLamports < dailyMax) {
			dailyMax = mPol.MaxDailySOLLamports
		}
		if dailyMax == 0 || w.DailySpentLamports+w.DailyReservedLamports+amt+out.EstFeeLamports > dailyMax {
			return out, fmt.Errorf("%w: commit+fee exceeds remaining daily mining SOL cap", ErrPolicyDenied)
		}
		if err := s.checkMiningRoundCapLocked(ctx, w, cluster, m.RoundSlot, amt); err != nil {
			return out, err
		}
		if strings.TrimSpace(m.SaltHex) != "" {
			rawSalt, err := hex.DecodeString(strings.TrimSpace(m.SaltHex))
			if err != nil || len(rawSalt) != 32 {
				return out, errors.New("saltHex must be 64-char hex")
			}
			copy(out.Salt[:], rawSalt)
		} else {
			if _, err := rand.Read(out.Salt[:]); err != nil {
				return out, err
			}
		}
		out.AmountLamports = amt
		out.EstimatedUSD = (float64(amt) / 1e9) * 250.0
		out.Symbol = "SOL"
		out.HumanAmount = FormatLamportsToSOL(amt)
		rkAddr, _ := onchain.LV1XRoundPDA(progID, ledgerID, m.RoundSlot)
		out.DestAddr = rkAddr

	case MiningOpClaim:
		if out.EntriesMask == 0 {
			out.EntriesMask = 0x01
		}
		out.AmountLamports = 0
		out.EstimatedUSD = (float64(out.EstFeeLamports) / 1e9) * 250.0
		out.Symbol = "SOL"
		out.HumanAmount = FormatLamportsToSOL(out.EstFeeLamports)
		out.DestAddr = w.PublicKey

	case MiningOpSession:
		act := strings.ToLower(strings.TrimSpace(m.SessionAction))
		if act != "open" && act != "stop" && act != "close" {
			return out, fmt.Errorf("%w: invalid LV1X sessionAction %q", ErrPolicyDenied, m.SessionAction)
		}
		out.SessionAction = act
		out.SessionRef = m.SessionRef
		out.FirstSlot = m.FirstSlot
		out.LastSlot = m.LastSlot
		out.ExpiresAt = m.ExpiresAt
		out.MaxEntry = m.MaxEntry
		out.MaxTurnover = m.MaxTurnover
		if act == "open" {
			amt := m.AmountLamports
			if amt < onchain.LV1XMinCommitLamports {
				return out, fmt.Errorf("%w: session deposit below minimum %d", ErrPolicyDenied, onchain.LV1XMinCommitLamports)
			}
			if w.Policy.SOLCap.MaxPerTxRaw == 0 || amt+out.EstFeeLamports > w.Policy.SOLCap.MaxPerTxRaw ||
				w.DailySpentLamports+w.DailyReservedLamports+amt+out.EstFeeLamports > w.Policy.SOLCap.MaxDailyRaw {
				return out, fmt.Errorf("%w: session deposit exceeds SOL caps", ErrPolicyDenied)
			}
			out.AmountLamports = amt
			out.EstimatedUSD = (float64(amt) / 1e9) * 250.0
			out.HumanAmount = FormatLamportsToSOL(amt)
		} else {
			out.AmountLamports = 0
			out.EstimatedUSD = (float64(out.EstFeeLamports) / 1e9) * 250.0
			out.HumanAmount = FormatLamportsToSOL(out.EstFeeLamports)
		}
		out.Symbol = "SOL"
		sessAddr, _ := onchain.LV1XSessionPDA(progID, ledgerID, w.PublicKey, m.SessionRef)
		out.DestAddr = sessAddr
	}
	return out, nil
}

func buildMiningReviewMessage(feePayer [32]byte, bh [32]byte, priorityFee uint64, op string, md preparedMiningDetails) ([]byte, error) {
	switch op {
	case MiningOpCommit:
		ownerB58 := Base58Encode(feePayer[:])
		commit, err := onchain.LV1XCommitmentHash(md.ProgramID, md.LedgerID, md.RoundSlot, ownerB58, md.AmountLamports, md.Nonce, md.Selection, md.Salt)
		if err != nil {
			return nil, err
		}
		return onchain.BuildLV1XCommitMessage(onchain.LV1XCommitParams{
			ProgramID:  md.ProgramID,
			LedgerID:   md.LedgerID,
			Owner:      feePayer,
			Blockhash:  bh,
			Slot:       md.RoundSlot,
			ShardID:    md.ShardID,
			EntryIndex: md.EntryIndex,
			Amount:     md.AmountLamports,
			Nonce:      md.Nonce,
			Commitment: commit,
			MicroLam:   priorityFee,
		})
	case MiningOpClaim:
		return onchain.BuildLV1XClaimSolBatchMessage(onchain.LV1XClaimSolParams{
			ProgramID:   md.ProgramID,
			LedgerID:    md.LedgerID,
			FeePayer:    feePayer,
			Destination: feePayer,
			Blockhash:   bh,
			Slot:        md.RoundSlot,
			ShardID:     md.ShardID,
			Page:        md.Page,
			EntriesMask: md.EntriesMask,
			MicroLam:    priorityFee,
		})
	case MiningOpSession:
		var root [32]byte
		return onchain.BuildLV1XSessionMessage(onchain.LV1XSessionParams{
			ProgramID:      md.ProgramID,
			LedgerID:       md.LedgerID,
			Owner:          feePayer,
			Blockhash:      bh,
			Reference:      md.SessionRef,
			StrategyRoot:   root,
			FirstSlot:      md.FirstSlot,
			LastSlot:       md.LastSlot,
			ExpiresAt:      md.ExpiresAt,
			MaxEntry:       md.MaxEntry,
			MaxTurnover:    md.MaxTurnover,
			ReturnToBudget: true,
			Deposit:        md.AmountLamports,
			Action:         md.SessionAction,
			MicroLam:       priorityFee,
		})
	default:
		return nil, errors.New("unsupported mining operation")
	}
}
