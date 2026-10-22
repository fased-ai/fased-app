// Package wenpm1 implements only WEN's pinned disposable PM1 v1 wire profile.
// It has no RPC, signer, wallet, or application execution integration.
package wenpm1

const SchemaHash = "cd27e05b6e8f6106ffca75e5bd967caa185ad8a137ffcc972c2c8ebbc48c8ec1"

type AnnualPair struct {
	Year   uint64
	Amount uint64
}
type Annual []AnnualPair

type Binding struct {
	Program Key `json:"program"`
	Ledger  Key `json:"ledger"`
	Network Key `json:"network"`
	Economy Key `json:"economy"`
	Terms   Key `json:"terms"`
}

type Vote struct {
	Price      uint64 `json:"price,string"`
	SourceTime int64  `json:"source_time,string"`
	Sequence   uint64 `json:"sequence,string"`
}

type Entry struct {
	Owner      Key    `json:"owner"`
	Nonce      uint64 `json:"nonce,string"`
	Amount     uint64 `json:"amount,string"`
	Commitment Key    `json:"commitment"`
	Selection  *uint8 `json:"selection"`
	Principal  uint64 `json:"principal,string"`
	SAT        uint64 `json:"sat,string"`
	MinerInput uint64 `json:"miner_input,string"`
	SOLClaimed bool   `json:"sol_claimed"`
	SATClaimed bool   `json:"sat_claimed"`
	CreditPPM  uint64 `json:"credit_ppm,string"`
	Exact      bool   `json:"exact"`
	Forfeiture uint64 `json:"forfeiture,string"`
	WinnerFee  uint64 `json:"winner_fee,string"`
}

type Round struct {
	Slot               uint64       `json:"slot,string"`
	Start              int64        `json:"start,string"`
	Hold               uint64       `json:"hold,string"`
	Entries            []Entry      `json:"entries" wiremax:"1024"`
	Observations       [14][3]*Vote `json:"observations"`
	Settled            bool         `json:"settled"`
	Outcome            *uint8       `json:"outcome"`
	Allocations        [6]uint64    `json:"allocations"`
	CarryInput         uint64       `json:"carry_input,string"`
	TotalStake         uint64       `json:"total_stake,string"`
	Forfeitures        uint64       `json:"forfeitures,string"`
	WinnerFees         uint64       `json:"winner_fees,string"`
	CreditWeight       Uint128      `json:"credit_weight"`
	MaxCreditPPM       uint64       `json:"max_credit_ppm,string"`
	ReleasedMinerInput uint64       `json:"released_miner_input,string"`
	ExactWinningStake  uint64       `json:"exact_winning_stake,string"`
	IncorrectStake     uint64       `json:"incorrect_stake,string"`
}

type Ledger struct {
	Binding           Binding `json:"binding"`
	Epoch             int64   `json:"epoch,string"`
	Recorders         [3]Key  `json:"recorders"`
	Mint              Key     `json:"mint"`
	EligibleStake     uint64  `json:"eligible_stake,string"`
	LifetimeCommitted uint64  `json:"lifetime_committed,string"`
	Annual            Annual  `json:"annual" wiremax:"8192"`
	Rounds            []Round `json:"rounds" wiremax:"1024"`
}

type Receipt struct {
	Binding    Binding `json:"binding"`
	Slot       uint64  `json:"slot,string"`
	Entry      uint32  `json:"entry"`
	Owner      Key     `json:"owner"`
	Nonce      uint64  `json:"nonce,string"`
	Amount     uint64  `json:"amount,string"`
	Selection  *uint8  `json:"selection"`
	Settled    bool    `json:"settled"`
	Outcome    *uint8  `json:"outcome"`
	Principal  uint64  `json:"principal,string"`
	SAT        uint64  `json:"sat,string"`
	MinerInput uint64  `json:"miner_input,string"`
	SOLClaimed bool    `json:"sol_claimed"`
	SATClaimed bool    `json:"sat_claimed"`
	CreditPPM  uint64  `json:"credit_ppm,string"`
	Exact      bool    `json:"exact"`
	Forfeiture uint64  `json:"forfeiture,string"`
	WinnerFee  uint64  `json:"winner_fee,string"`
}

type Open struct {
	Slot uint64 `json:"slot,string"`
}

type Commit struct {
	Slot       uint64 `json:"slot,string"`
	Amount     uint64 `json:"amount,string"`
	Nonce      uint64 `json:"nonce,string"`
	Commitment Key    `json:"commitment"`
}

type Reveal struct {
	Slot      uint64 `json:"slot,string"`
	Entry     uint32 `json:"entry"`
	Selection uint8  `json:"selection"`
	Salt      Key    `json:"salt"`
}

type Observe struct {
	Slot   uint64 `json:"slot,string"`
	Sample uint8  `json:"sample"`
	Vote   Vote   `json:"vote"`
}

type Settle struct {
	Slot uint64 `json:"slot,string"`
}

type ClaimSol struct {
	Slot  uint64 `json:"slot,string"`
	Entry uint32 `json:"entry"`
}

type ClaimSat struct {
	Slot  uint64 `json:"slot,string"`
	Entry uint32 `json:"entry"`
}

// Request is sealed to the seven fixture-only instruction models.
type Request interface{ pm1Operation() byte }

func (Open) pm1Operation() byte     { return 1 }
func (Commit) pm1Operation() byte   { return 2 }
func (Reveal) pm1Operation() byte   { return 3 }
func (Observe) pm1Operation() byte  { return 4 }
func (Settle) pm1Operation() byte   { return 5 }
func (ClaimSol) pm1Operation() byte { return 6 }
func (ClaimSat) pm1Operation() byte { return 7 }
