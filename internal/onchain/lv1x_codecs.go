package onchain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

// Exact LV1X framed account spaces & magics matching wen/vendor/satcoin-mining-v2/lean-v1-pda.mjs.
const (
	LV1XSpaceRuntime       = 512
	LV1XSpaceYear          = 128
	LV1XSpaceRound         = 768
	LV1XSpaceRoundShard    = 192
	LV1XSpacePage          = 1792
	LV1XSpaceNonce         = 160
	LV1XSpaceStakingState  = 192
	LV1XSpaceStakePosition = 160
	LV1XSpaceTreasuryState = 128
	LV1XSpaceSessionState  = 256

	LV1XMagicRuntime       = "LV1RUNTM"
	LV1XMagicRound         = "LV1ROUND"
	LV1XMagicRoundShard    = "LV1SHRD!"
	LV1XMagicPage          = "LV1PAGE!"
	LV1XMagicStakingState  = "LV1STAK!"
	LV1XMagicStakePosition = "LV1SPOS!"
	LV1XMagicTreasuryState = "LV1TREAS"
	LV1XMagicSessionState  = "LV1SESS!"
)

type lv1xReader struct {
	b   []byte
	off int
}

func readLV1XFrame(data []byte, magic string, space int) (*lv1xReader, error) {
	if len(data) != space {
		return nil, fmt.Errorf("wrong %s account size %d != %d", magic, len(data), space)
	}
	if string(data[:8]) != magic {
		return nil, fmt.Errorf("wrong account magic: got %q want %q", string(data[:8]), magic)
	}
	bodyLen := int(binary.LittleEndian.Uint32(data[8:12]))
	if bodyLen > space-12 {
		return nil, fmt.Errorf("%s framed length %d exceeds space", magic, bodyLen)
	}
	for i := 12 + bodyLen; i < space; i++ {
		if data[i] != 0 {
			return nil, fmt.Errorf("%s noncanonical zero padding at byte %d", magic, i)
		}
	}
	return &lv1xReader{b: data[12 : 12+bodyLen]}, nil
}

func writeLV1XFrame(magic string, body []byte, space int) ([]byte, error) {
	if len(body)+12 > space {
		return nil, fmt.Errorf("%s frame %d exceeds space %d", magic, len(body)+12, space)
	}
	out := make([]byte, space)
	copy(out[:8], magic)
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(body)))
	copy(out[12:], body)
	return out, nil
}

func (r *lv1xReader) take(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.b) {
		return nil, errors.New("lv1x unexpected EOF")
	}
	s := r.b[r.off : r.off+n]
	r.off += n
	return s, nil
}

func (r *lv1xReader) u8() (uint8, error) {
	s, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return s[0], nil
}

func (r *lv1xReader) bool() (bool, error) {
	v, err := r.u8()
	if err != nil {
		return false, err
	}
	if v > 1 {
		return false, errors.New("lv1x invalid bool")
	}
	return v == 1, nil
}

func (r *lv1xReader) u16() (uint16, error) {
	s, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(s), nil
}

func (r *lv1xReader) u32() (uint32, error) {
	s, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(s), nil
}

func (r *lv1xReader) u64() (uint64, error) {
	s, err := r.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(s), nil
}

func (r *lv1xReader) i64() (int64, error) {
	u, err := r.u64()
	return int64(u), err
}

func (r *lv1xReader) u128() ([16]byte, error) {
	var out [16]byte
	s, err := r.take(16)
	if err != nil {
		return out, err
	}
	copy(out[:], s)
	return out, nil
}

func (r *lv1xReader) key() ([32]byte, error) {
	var out [32]byte
	s, err := r.take(32)
	if err != nil {
		return out, err
	}
	copy(out[:], s)
	return out, nil
}

func (r *lv1xReader) optU8() (*uint8, error) {
	tag, err := r.u8()
	if err != nil {
		return nil, err
	}
	if tag == 0 {
		return nil, nil
	}
	if tag != 1 {
		return nil, errors.New("lv1x invalid optU8 tag")
	}
	v, err := r.u8()
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *lv1xReader) optU64() (*uint64, error) {
	tag, err := r.u8()
	if err != nil {
		return nil, err
	}
	if tag == 0 {
		return nil, nil
	}
	if tag != 1 {
		return nil, errors.New("lv1x invalid optU64 tag")
	}
	v, err := r.u64()
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *lv1xReader) remaining() int { return len(r.b) - r.off }

func (r *lv1xReader) done() error {
	if r.off != len(r.b) {
		return fmt.Errorf("lv1x trailing unparsed bytes: %d", len(r.b)-r.off)
	}
	return nil
}

type LV1XIssuanceAccount struct {
	Awarded      uint64
	Pending      uint64
	BoostAwarded uint64
	BoostPending uint64
	BoostRetired bool
	LatestSlot   *uint64
}

type LV1XRuntimeAccount struct {
	Ledger            string
	Epoch             int64
	Admins            [3]string
	Recorders         [3]string
	QuorumThreshold   uint8
	Mint              string
	BTCMint           string
	Phase             uint8
	Paused            bool
	BootstrapWinddown bool
	Issuance          LV1XIssuanceAccount
	SettledSlot       *uint64
	BootstrapGross    uint64
	LPSol             uint64
	LPSat             uint64
	Operating         uint64
	NormalFrom        uint64
	GraduatedAt       int64
	Pool              string
	BTCPool           string
	LPSolMin          uint64
	LPSatMin          uint64
	TGEBps            uint16
	VestingDuration   uint32
	BoostBudgetSat    uint32
	BoostMultiplier   uint8
	HasGovConfig      bool
}

func DecodeLV1XRuntime(data []byte) (LV1XRuntimeAccount, error) {
	r, err := readLV1XFrame(data, LV1XMagicRuntime, LV1XSpaceRuntime)
	if err != nil {
		return LV1XRuntimeAccount{}, err
	}
	out := LV1XRuntimeAccount{
		TGEBps:          LV1XDefaultTGEBps,
		VestingDuration: LV1XBootstrapVestingSec,
		BoostBudgetSat:  60_000,
		BoostMultiplier: 8,
	}
	readKey := func() (string, error) {
		k, e := r.key()
		if e != nil {
			return "", e
		}
		return Base58(k[:]), nil
	}
	if out.Ledger, err = readKey(); err != nil {
		return out, err
	}
	if out.Epoch, err = r.i64(); err != nil {
		return out, err
	}
	for i := range 3 {
		if out.Admins[i], err = readKey(); err != nil {
			return out, err
		}
	}
	for i := range 3 {
		if out.Recorders[i], err = readKey(); err != nil {
			return out, err
		}
	}
	if out.QuorumThreshold, err = r.u8(); err != nil {
		return out, err
	}
	if out.Mint, err = readKey(); err != nil {
		return out, err
	}
	if out.BTCMint, err = readKey(); err != nil {
		return out, err
	}
	if out.Phase, err = r.u8(); err != nil {
		return out, err
	}
	if out.Paused, err = r.bool(); err != nil {
		return out, err
	}
	if out.BootstrapWinddown, err = r.bool(); err != nil {
		return out, err
	}
	if out.Issuance.Awarded, err = r.u64(); err != nil {
		return out, err
	}
	if out.Issuance.Pending, err = r.u64(); err != nil {
		return out, err
	}
	if out.Issuance.BoostAwarded, err = r.u64(); err != nil {
		return out, err
	}
	if out.Issuance.BoostPending, err = r.u64(); err != nil {
		return out, err
	}
	if out.Issuance.BoostRetired, err = r.bool(); err != nil {
		return out, err
	}
	if out.Issuance.LatestSlot, err = r.optU64(); err != nil {
		return out, err
	}
	if out.SettledSlot, err = r.optU64(); err != nil {
		return out, err
	}
	if out.BootstrapGross, err = r.u64(); err != nil {
		return out, err
	}
	if out.LPSol, err = r.u64(); err != nil {
		return out, err
	}
	if out.LPSat, err = r.u64(); err != nil {
		return out, err
	}
	if out.Operating, err = r.u64(); err != nil {
		return out, err
	}
	if out.NormalFrom, err = r.u64(); err != nil {
		return out, err
	}
	if out.GraduatedAt, err = r.i64(); err != nil {
		return out, err
	}
	if out.Pool, err = readKey(); err != nil {
		return out, err
	}
	if out.BTCPool, err = readKey(); err != nil {
		return out, err
	}
	if out.LPSolMin, err = r.u64(); err != nil {
		return out, err
	}
	if out.LPSatMin, err = r.u64(); err != nil {
		return out, err
	}
	if r.remaining() > 0 {
		if out.TGEBps, err = r.u16(); err != nil {
			return out, err
		}
		if out.VestingDuration, err = r.u32(); err != nil {
			return out, err
		}
		if out.BoostBudgetSat, err = r.u32(); err != nil {
			return out, err
		}
		if out.BoostMultiplier, err = r.u8(); err != nil {
			return out, err
		}
		if out.HasGovConfig, err = r.bool(); err != nil {
			return out, err
		}
	}
	return out, r.done()
}

type LV1XWindowVotes struct {
	VotedMask      uint8
	Medians        [3]uint64
	FinalizedPrice *uint64
}

func decodeLV1XWindowVotes(r *lv1xReader) (LV1XWindowVotes, error) {
	var out LV1XWindowVotes
	mask, err := r.u8()
	if err != nil {
		return out, err
	}
	if mask >= 8 {
		return out, errors.New("invalid voted_mask")
	}
	out.VotedMask = mask
	for i := range 3 {
		if mask&(1<<i) != 0 {
			if out.Medians[i], err = r.u64(); err != nil {
				return out, err
			}
			if _, err = r.key(); err != nil {
				return out, err
			}
		}
	}
	out.FinalizedPrice, err = r.optU64()
	return out, err
}

type LV1XRoundAccount struct {
	Ledger        string
	RentPayer     string
	Slot          uint64
	PreviousSlot  *uint64
	Start         int64
	OriginalPhase uint8
	ResSlot       uint64
	ResCeiling    uint64
	ResBoosted    bool
	ResAward      *uint64
	ActiveStake   uint64
	Count         uint32
	AcceptedStake uint64
	Stage         uint8
	Open          *uint64
	Close         *uint64
	OpenVotes     LV1XWindowVotes
	CloseVotes    LV1XWindowVotes
	Scanned       uint32
	Allocated     uint32
	ClosedPages   uint32
	TotalStake    uint64
	Forfeitures   uint64
	WinnerFees    uint64
	GrossSat      uint64
	MinerSat      uint64
	LPSat         uint64
	LPSol         uint64
	Operating     uint64
	MinerBtcSol   uint64
	StakingSol    uint64
	MinerBtcTotal uint64
	ShardCount    uint8
	TotalPages    uint32
	RangeInnerBps uint16
	RangeOuterBps uint16
}

func DecodeLV1XRound(data []byte) (LV1XRoundAccount, error) {
	r, err := readLV1XFrame(data, LV1XMagicRound, LV1XSpaceRound)
	if err != nil {
		return LV1XRoundAccount{}, err
	}
	var out LV1XRoundAccount
	readKey := func() (string, error) {
		k, e := r.key()
		if e != nil {
			return "", e
		}
		return Base58(k[:]), nil
	}
	if out.Ledger, err = readKey(); err != nil {
		return out, err
	}
	if out.RentPayer, err = readKey(); err != nil {
		return out, err
	}
	if out.Slot, err = r.u64(); err != nil {
		return out, err
	}
	if out.PreviousSlot, err = r.optU64(); err != nil {
		return out, err
	}
	if out.Start, err = r.i64(); err != nil {
		return out, err
	}
	if out.OriginalPhase, err = r.u8(); err != nil {
		return out, err
	}
	if out.ResSlot, err = r.u64(); err != nil {
		return out, err
	}
	if out.ResCeiling, err = r.u64(); err != nil {
		return out, err
	}
	if out.ResBoosted, err = r.bool(); err != nil {
		return out, err
	}
	if out.ResAward, err = r.optU64(); err != nil {
		return out, err
	}
	if out.ActiveStake, err = r.u64(); err != nil {
		return out, err
	}
	if out.Count, err = r.u32(); err != nil {
		return out, err
	}
	if out.AcceptedStake, err = r.u64(); err != nil {
		return out, err
	}
	if out.Stage, err = r.u8(); err != nil {
		return out, err
	}
	if out.Open, err = r.optU64(); err != nil {
		return out, err
	}
	if out.Close, err = r.optU64(); err != nil {
		return out, err
	}
	if out.OpenVotes, err = decodeLV1XWindowVotes(r); err != nil {
		return out, err
	}
	if out.CloseVotes, err = decodeLV1XWindowVotes(r); err != nil {
		return out, err
	}
	if out.Scanned, err = r.u32(); err != nil {
		return out, err
	}
	if out.Allocated, err = r.u32(); err != nil {
		return out, err
	}
	if out.ClosedPages, err = r.u32(); err != nil {
		return out, err
	}
	// Totals
	if out.TotalStake, err = r.u64(); err != nil {
		return out, err
	}
	if out.Forfeitures, err = r.u64(); err != nil {
		return out, err
	}
	if out.WinnerFees, err = r.u64(); err != nil {
		return out, err
	}
	if _, err = r.u128(); err != nil { // creditWeight
		return out, err
	}
	for range 3 { // maxCreditPpm, exactWinningStake, incorrectStake
		if _, err = r.u64(); err != nil {
			return out, err
		}
	}
	// Budget
	if out.GrossSat, err = r.u64(); err != nil {
		return out, err
	}
	if out.MinerSat, err = r.u64(); err != nil {
		return out, err
	}
	if out.LPSat, err = r.u64(); err != nil {
		return out, err
	}
	if out.LPSol, err = r.u64(); err != nil {
		return out, err
	}
	if out.Operating, err = r.u64(); err != nil {
		return out, err
	}
	if out.MinerBtcSol, err = r.u64(); err != nil {
		return out, err
	}
	if out.StakingSol, err = r.u64(); err != nil {
		return out, err
	}
	for range 3 { // stakingRefund, treasuryBtcSol, treasuryLpSol
		if _, err = r.u64(); err != nil {
			return out, err
		}
	}
	// Apportion
	if _, err = r.u32(); err != nil {
		return out, err
	}
	if _, err = r.u128(); err != nil {
		return out, err
	}
	for range 10 { // sat, forfeitures, deductions, refund, lpSol, lpSat, principalPaid, refundPaid, minerBtcRemaining
		if _, err = r.u64(); err != nil {
			return out, err
		}
	}
	if out.MinerBtcTotal, err = r.u64(); err != nil {
		return out, err
	}
	if _, err = r.u64(); err != nil { // minerBtcUnclaimed
		return out, err
	}
	if _, err = r.u128(); err != nil { // minerBtcAllocatedWeight
		return out, err
	}
	if _, err = r.u64(); err != nil { // minerBtcAllocatedOut
		return out, err
	}
	if r.remaining() > 0 {
		shardCount, err := r.u8()
		if err != nil {
			return out, err
		}
		if shardCount > 0 {
			if int(shardCount) > LV1XMaxShards {
				return out, errors.New("invalid sharded round shardCount")
			}
			out.ShardCount = shardCount
			for range 5 { // merged/active/scanned/allocated/closed masks
				if _, err = r.u32(); err != nil {
					return out, err
				}
			}
			if out.TotalPages, err = r.u32(); err != nil {
				return out, err
			}
		}
		if r.remaining() > 0 {
			if out.RangeInnerBps, err = r.u16(); err != nil {
				return out, err
			}
			if out.RangeOuterBps, err = r.u16(); err != nil {
				return out, err
			}
		}
	}
	return out, r.done()
}

type LV1XPageRow struct {
	Owner             string
	Nonce             uint64
	Amount            uint64
	CommitmentHex     string
	Selection         *uint8
	Principal         uint64
	Sat               uint64
	SatClaimedAmount  uint64
	StakingRefund     uint64
	LPSolContribution uint64
	LPSatContribution uint64
	CreditPpm         uint64
	Exact             bool
	Forfeiture        uint64
	WinnerFee         uint64
	SessionRef        uint64
	SolClaimed        bool
	BtcClaimed        bool
	LpReclaimed       bool
}

type LV1XPageAccount struct {
	Ledger    string
	RentPayer string
	Slot      uint64
	Index     uint32
	Rows      []LV1XPageRow
}

func DecodeLV1XPage(data []byte) (LV1XPageAccount, error) {
	r, err := readLV1XFrame(data, LV1XMagicPage, LV1XSpacePage)
	if err != nil {
		return LV1XPageAccount{}, err
	}
	var out LV1XPageAccount
	lk, err := r.key()
	if err != nil {
		return out, err
	}
	rk, err := r.key()
	if err != nil {
		return out, err
	}
	out.Ledger = Base58(lk[:])
	out.RentPayer = Base58(rk[:])
	if out.Slot, err = r.u64(); err != nil {
		return out, err
	}
	if out.Index, err = r.u32(); err != nil {
		return out, err
	}
	cnt, err := r.u8()
	if err != nil {
		return out, err
	}
	if int(cnt) > LV1XPageEntries {
		return out, errors.New("page row count exceeds 8")
	}
	out.Rows = make([]LV1XPageRow, 0, cnt)
	for range cnt {
		var row LV1XPageRow
		ok, e := r.key()
		if e != nil {
			return out, e
		}
		row.Owner = Base58(ok[:])
		if row.Nonce, err = r.u64(); err != nil {
			return out, err
		}
		if row.Amount, err = r.u64(); err != nil {
			return out, err
		}
		ck, e := r.key()
		if e != nil {
			return out, e
		}
		row.CommitmentHex = fmt.Sprintf("%x", ck[:])
		if row.Selection, err = r.optU8(); err != nil {
			return out, err
		}
		if row.Principal, err = r.u64(); err != nil {
			return out, err
		}
		if row.Sat, err = r.u64(); err != nil {
			return out, err
		}
		if row.SatClaimedAmount, err = r.u64(); err != nil {
			return out, err
		}
		if row.StakingRefund, err = r.u64(); err != nil {
			return out, err
		}
		if row.LPSolContribution, err = r.u64(); err != nil {
			return out, err
		}
		if row.LPSatContribution, err = r.u64(); err != nil {
			return out, err
		}
		if row.CreditPpm, err = r.u64(); err != nil {
			return out, err
		}
		if row.Exact, err = r.bool(); err != nil {
			return out, err
		}
		if row.Forfeiture, err = r.u64(); err != nil {
			return out, err
		}
		if row.WinnerFee, err = r.u64(); err != nil {
			return out, err
		}
		if row.SessionRef, err = r.u64(); err != nil {
			return out, err
		}
		if row.SolClaimed, err = r.bool(); err != nil {
			return out, err
		}
		if row.BtcClaimed, err = r.bool(); err != nil {
			return out, err
		}
		if row.LpReclaimed, err = r.bool(); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, row)
	}
	return out, r.done()
}

type LV1XStakingStateAccount struct {
	Ledger              string
	TotalActiveStake    uint64
	TotalPendingStake   uint64
	AccBtcPerSatE18     *big.Int
	PendingSolForBtc    uint64
	TotalBtcDistributed uint64
	StreamRemainingBtc  uint64
	StreamLastTs        int64
	StreamEndTs         int64
}

func u128LEToBigInt(raw [16]byte) *big.Int {
	lo := binary.LittleEndian.Uint64(raw[:8])
	hi := binary.LittleEndian.Uint64(raw[8:16])
	out := new(big.Int).SetUint64(hi)
	out.Lsh(out, 64)
	out.Or(out, new(big.Int).SetUint64(lo))
	return out
}

func DecodeLV1XStakingState(data []byte) (LV1XStakingStateAccount, error) {
	r, err := readLV1XFrame(data, LV1XMagicStakingState, LV1XSpaceStakingState)
	if err != nil {
		return LV1XStakingStateAccount{}, err
	}
	var out LV1XStakingStateAccount
	lk, err := r.key()
	if err != nil {
		return out, err
	}
	out.Ledger = Base58(lk[:])
	if out.TotalActiveStake, err = r.u64(); err != nil {
		return out, err
	}
	if out.TotalPendingStake, err = r.u64(); err != nil {
		return out, err
	}
	acc, err := r.u128()
	if err != nil {
		return out, err
	}
	out.AccBtcPerSatE18 = u128LEToBigInt(acc)
	if out.PendingSolForBtc, err = r.u64(); err != nil {
		return out, err
	}
	if out.TotalBtcDistributed, err = r.u64(); err != nil {
		return out, err
	}
	if r.remaining() > 0 {
		if out.StreamRemainingBtc, err = r.u64(); err != nil {
			return out, err
		}
		if out.StreamLastTs, err = r.i64(); err != nil {
			return out, err
		}
		if out.StreamEndTs, err = r.i64(); err != nil {
			return out, err
		}
	}
	return out, r.done()
}

type LV1XStakePositionAccount struct {
	Ledger          string
	Owner           string
	ActiveStake     uint64
	PendingStake    uint64
	PendingMatureAt int64
	RewardDebtBtc   *big.Int
	UnclaimedBtc    uint64
}

func DecodeLV1XStakePosition(data []byte) (LV1XStakePositionAccount, error) {
	r, err := readLV1XFrame(data, LV1XMagicStakePosition, LV1XSpaceStakePosition)
	if err != nil {
		return LV1XStakePositionAccount{}, err
	}
	var out LV1XStakePositionAccount
	lk, err := r.key()
	if err != nil {
		return out, err
	}
	ok, err := r.key()
	if err != nil {
		return out, err
	}
	out.Ledger = Base58(lk[:])
	out.Owner = Base58(ok[:])
	if out.ActiveStake, err = r.u64(); err != nil {
		return out, err
	}
	if out.PendingStake, err = r.u64(); err != nil {
		return out, err
	}
	if out.PendingMatureAt, err = r.i64(); err != nil {
		return out, err
	}
	debt, err := r.u128()
	if err != nil {
		return out, err
	}
	out.RewardDebtBtc = u128LEToBigInt(debt)
	if out.UnclaimedBtc, err = r.u64(); err != nil {
		return out, err
	}
	return out, r.done()
}

type LV1XTreasuryStateAccount struct {
	Ledger         string
	BtcSolPending  uint64
	LpSolPending   uint64
	TotalBtcBought uint64
	TotalLpBurned  uint64
}

func DecodeLV1XTreasuryState(data []byte) (LV1XTreasuryStateAccount, error) {
	r, err := readLV1XFrame(data, LV1XMagicTreasuryState, LV1XSpaceTreasuryState)
	if err != nil {
		return LV1XTreasuryStateAccount{}, err
	}
	var out LV1XTreasuryStateAccount
	lk, err := r.key()
	if err != nil {
		return out, err
	}
	out.Ledger = Base58(lk[:])
	if out.BtcSolPending, err = r.u64(); err != nil {
		return out, err
	}
	if out.LpSolPending, err = r.u64(); err != nil {
		return out, err
	}
	if out.TotalBtcBought, err = r.u64(); err != nil {
		return out, err
	}
	if out.TotalLpBurned, err = r.u64(); err != nil {
		return out, err
	}
	return out, r.done()
}

type LV1XSessionStateAccount struct {
	Ledger         string
	Owner          string
	Reference      uint64
	StrategyRoot   string
	FirstSlot      uint64
	LastSlot       uint64
	ExpiresAt      int64
	MaxEntry       uint64
	MaxTurnover    uint64
	Available      uint64
	UsedTurnover   uint64
	Unsettled      uint64
	ReturnToBudget bool
	Stopped        bool
}

func DecodeLV1XSessionState(data []byte) (LV1XSessionStateAccount, error) {
	r, err := readLV1XFrame(data, LV1XMagicSessionState, LV1XSpaceSessionState)
	if err != nil {
		return LV1XSessionStateAccount{}, err
	}
	var out LV1XSessionStateAccount
	lk, err := r.key()
	if err != nil {
		return out, err
	}
	ok, err := r.key()
	if err != nil {
		return out, err
	}
	out.Ledger = Base58(lk[:])
	out.Owner = Base58(ok[:])
	if out.Reference, err = r.u64(); err != nil {
		return out, err
	}
	rk, err := r.key()
	if err != nil {
		return out, err
	}
	out.StrategyRoot = fmt.Sprintf("%x", rk[:])
	if out.FirstSlot, err = r.u64(); err != nil {
		return out, err
	}
	if out.LastSlot, err = r.u64(); err != nil {
		return out, err
	}
	if out.ExpiresAt, err = r.i64(); err != nil {
		return out, err
	}
	if out.MaxEntry, err = r.u64(); err != nil {
		return out, err
	}
	if out.MaxTurnover, err = r.u64(); err != nil {
		return out, err
	}
	if out.Available, err = r.u64(); err != nil {
		return out, err
	}
	if out.UsedTurnover, err = r.u64(); err != nil {
		return out, err
	}
	if out.Unsettled, err = r.u64(); err != nil {
		return out, err
	}
	if out.ReturnToBudget, err = r.bool(); err != nil {
		return out, err
	}
	if out.Stopped, err = r.bool(); err != nil {
		return out, err
	}
	return out, r.done()
}

// LV1XVestedBootstrapSat computes vested SAT matching leanVestedBootstrapSat in lean-v1-pda.mjs.
func LV1XVestedBootstrapSat(totalSat uint64, phase uint8, graduatedAt, nowSec int64, vestingDuration uint32, tgeBps uint16) uint64 {
	if totalSat == 0 {
		return 0
	}
	if phase == 3 {
		return totalSat
	}
	if phase == 1 {
		if nowSec < graduatedAt {
			return 0
		}
		dur := int64(vestingDuration)
		if dur <= 0 {
			dur = 1
		}
		capped := uint64(tgeBps)
		if capped > 10_000 {
			capped = 10_000
		}
		totalBig := new(big.Int).SetUint64(totalSat)
		tge := new(big.Int).Mul(totalBig, new(big.Int).SetUint64(capped))
		tge.Div(tge, big.NewInt(10_000))
		elapsed := nowSec - graduatedAt
		if elapsed >= dur {
			return totalSat
		}
		linearPool := new(big.Int).Sub(totalBig, tge)
		vestedLinear := new(big.Int).Mul(linearPool, big.NewInt(elapsed))
		vestedLinear.Div(vestedLinear, big.NewInt(dur))
		vested := new(big.Int).Add(tge, vestedLinear)
		if vested.Cmp(totalBig) > 0 {
			return totalSat
		}
		return vested.Uint64()
	}
	return 0
}

// Unused import safeguard if needed later.
var _ = bytes.Equal
