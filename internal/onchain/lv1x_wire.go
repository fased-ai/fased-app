package onchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"filippo.io/edwards25519"
)

const (
	// Canonical Lean V1 (LV1X) protocol constants matching token/sat freeze/mainnet-v1-rc1.
	LV1XMagic               = "LV1X"
	LV1XVersion             = 1
	LV1XProtocolSchemaHash  = "76d3a86010f7e22a02006fdce70b79b7773dd2cd0f0462bef28548bbcb7d5d72"
	LV1XMinCommitLamports   = uint64(10_000_000)
	LV1XMinStakeAtoms       = uint64(1_000_000_000) // 1 SAT (9 decimals)
	LV1XLifetimeSatAtoms    = uint64(30_000_000_000_000_000)
	LV1XDefaultLPSolMin     = uint64(200_000_000_000)     // 200 SOL Balanced-A
	LV1XDefaultLPSatMin     = uint64(200_000_000_000_000) // 200,000 WENSAT Balanced-A
	LV1XDefaultBoostBudget  = uint64(400_000)             // 400,000 WENSAT boost pool
	LV1XDefaultBoostMult    = uint16(24)                  // 24x bootstrap multiplier
	LV1XBootstrapVestingSec = uint32(2_592_000)           // 30d vest
	LV1XDefaultTGEBps       = uint16(1000)                // 10% TGE
	LV1XRoundIntervalSec    = int64(600)
	LV1XSlotsPerYear        = uint64(52_560)
	LV1XMaxShards           = 32
	LV1XUnshardedShardID    = uint8(255)
	LV1XPageEntries         = 8
	BPFLoaderUpgradeableID  = "BPFLoaderUpgradeab1e11111111111111111111111"
	SolanaTokenProgramID    = Token
	SolanaAssociatedTokenID = AssociatedToken
	LV1XDevnetELFBytes      = 648_416
	LV1XMainnetELFBytes     = 650_592

	// Pinned Devnet & Frozen RC1 Mainnet ELF metadata (Mainnet addresses come from signed manifest).
	LV1XDevnetProgramID        = "WAcbznz9veHLEU4qup2eqvwQcGzLeV7hYvAtfaQTrVY"
	LV1XDevnetProgramDataID    = "6YHn2R8rauQs5JT1ACd6oyrZ5PsJ2SKEP6oZLdvx1S7z"
	LV1XDevnetLedgerID         = "DXYmEs4veeHR5sFXMdnsfmrjL3gEuGhuawcQwxgb5V46"
	LV1XDevnetBTCMint          = "2bBh5wMSYniLm8jpH9JtR4sxw4BCx7Q2q2AkX2FmGsd9"
	LV1XDevnetELFSHA256        = "32056042a15a228fe94ac3fc8e0d315d98ce4657576abd787fe186301581b245"
	LV1XDevnetVendorELFSHA256  = "32056042a15a228fe94ac3fc8e0d315d98ce4657576abd787fe186301581b245"
	LV1XMainnetELFSHA256       = "2d9a48f6ecc4114e11c03c1744b291033f39f11445d3292fa9f8caf12999ffc7"
	LV1XMainnetVendorELFSHA256 = "2d9a48f6ecc4114e11c03c1744b291033f39f11445d3292fa9f8caf12999ffc7"
	LV1XMainnetBTCMint         = "cbbtcf3aa214zXHbiAZQwf4122FBYbraNdFqgw4iMij"

	// Mainnet remains intentionally empty until unlocked via signed manifest + BPF check + owner step-up.
	LV1XMainnetProgramID = ""
)

// Canonical LEAN_IX_TAGS matching wen/vendor/satcoin-mining-v2/lean-v1-pda.mjs.
const (
	LV1XTagInitialize               uint8 = 0
	LV1XTagCommit                   uint8 = 5
	LV1XTagCommitFromSession        uint8 = 6
	LV1XTagRevealBatch              uint8 = 7
	LV1XTagBeginSettlement          uint8 = 9
	LV1XTagFinishSettlement         uint8 = 12
	LV1XTagClaimSolBatch            uint8 = 13
	LV1XTagClaimSat                 uint8 = 14
	LV1XTagClaimBtc                 uint8 = 16
	LV1XTagCloseNonce               uint8 = 18
	LV1XTagStake                    uint8 = 21
	LV1XTagActivateStake            uint8 = 22
	LV1XTagClaimStakingReward       uint8 = 25
	LV1XTagUnstake                  uint8 = 26
	LV1XTagOpenSession              uint8 = 30
	LV1XTagDepositSession           uint8 = 31
	LV1XTagStopSession              uint8 = 32
	LV1XTagCloseSession             uint8 = 33
	LV1XTagCommitToShard            uint8 = 35
	LV1XTagCommitFromSessionToShard uint8 = 36
	LV1XTagRevealPageBatch          uint8 = 37
	LV1XTagMergeShardsBatch         uint8 = 38
	LV1XTagScanPagesBatch           uint8 = 40
	LV1XTagAllocatePagesBatch       uint8 = 41
	LV1XTagClaimShardSolBatch       uint8 = 42
	LV1XTagClaimShardSat            uint8 = 43
	LV1XTagClaimShardBtc            uint8 = 44
	LV1XTagClaimSatBatch            uint8 = 52
	LV1XTagClaimBtcBatch            uint8 = 53
)

// FindProgramAddress derives a canonical Solana off-curve PDA from seeds.
func FindProgramAddress(programID string, seeds ...[]byte) (string, byte, error) {
	prog, err := Key(programID)
	if err != nil {
		return "", 0, err
	}
	if len(seeds) > 16 {
		return "", 0, errors.New("too many PDA seeds")
	}
	for _, s := range seeds {
		if len(s) > 32 {
			return "", 0, errors.New("PDA seed exceeds 32 bytes")
		}
	}
	const pdaMarker = "ProgramDerivedAddress"
	for bump := 255; bump >= 0; bump-- {
		h := sha256.New()
		for _, s := range seeds {
			_, _ = h.Write(s)
		}
		_, _ = h.Write([]byte{byte(bump)})
		_, _ = h.Write(prog)
		_, _ = h.Write([]byte(pdaMarker))
		sum := h.Sum(nil)
		if _, err := new(edwards25519.Point).SetBytes(sum); err != nil {
			return Base58(sum), byte(bump), nil
		}
	}
	return "", 0, errors.New("PDA derivation exhausted bumps")
}

func u64LEBytes(v uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return b[:]
}

func u32LEBytes(v uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return b[:]
}

func i64LEBytes(v int64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(v))
	return b[:]
}

// LV1XRuntimePDA derives findProgramAddress(["lv1-runtime", ledger], program).
func LV1XRuntimePDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-runtime"), l)
	return addr, err
}

// LV1XMintPDA derives findProgramAddress(["lv1-mint", ledger], program).
func LV1XMintPDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-mint"), l)
	return addr, err
}

// LV1XIssuancePDA derives findProgramAddress(["lv1-issuance", ledger], program).
func LV1XIssuancePDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-issuance"), l)
	return addr, err
}

// LV1XRoundPDA derives findProgramAddress(["lv1-round", ledger, u64le(slot)], program).
func LV1XRoundPDA(programID, ledgerID string, slot uint64) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-round"), l, u64LEBytes(slot))
	return addr, err
}

// LV1XPagePDA derives findProgramAddress(["lv1-page", round, u32le(page)], program).
func LV1XPagePDA(programID, roundAddr string, page uint32) (string, error) {
	rk, err := Key(roundAddr)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-page"), rk, u32LEBytes(page))
	return addr, err
}

// LV1XShardPDA derives findProgramAddress(["lv1-shard", round, shardId], program).
func LV1XShardPDA(programID, roundAddr string, shardID uint8) (string, error) {
	if int(shardID) >= LV1XMaxShards {
		return "", errors.New("invalid LV1X shard id")
	}
	rk, err := Key(roundAddr)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-shard"), rk, []byte{shardID})
	return addr, err
}

// LV1XShardPagePDA derives findProgramAddress(["lv1-spage", round, shardId, u32le(page)], program).
func LV1XShardPagePDA(programID, roundAddr string, shardID uint8, page uint32) (string, error) {
	if int(shardID) >= LV1XMaxShards {
		return "", errors.New("invalid LV1X shard id")
	}
	rk, err := Key(roundAddr)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-spage"), rk, []byte{shardID}, u32LEBytes(page))
	return addr, err
}

// LV1XNoncePDA derives findProgramAddress(["lv1-nonce", round, owner, u64le(group)], program).
func LV1XNoncePDA(programID, roundAddr, owner string, group uint64) (string, error) {
	rk, err := Key(roundAddr)
	if err != nil {
		return "", err
	}
	ok, err := Key(owner)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-nonce"), rk, ok, u64LEBytes(group))
	return addr, err
}

// LV1XSessionPDA derives findProgramAddress(["lv1-session", ledger, owner, u64le(reference)], program).
func LV1XSessionPDA(programID, ledgerID, owner string, reference uint64) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	ok, err := Key(owner)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-session"), l, ok, u64LEBytes(reference))
	return addr, err
}

// LV1XMinerBTCVaultPDA derives findProgramAddress(["lv1-miner-btc", ledger], program).
func LV1XMinerBTCVaultPDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-miner-btc"), l)
	return addr, err
}

// LV1XStakingStatePDA derives findProgramAddress(["lv1-staking", ledger], program).
func LV1XStakingStatePDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-staking"), l)
	return addr, err
}

// LV1XStakingVaultPDA derives findProgramAddress(["lv1-stake-vault", ledger], program).
func LV1XStakingVaultPDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-stake-vault"), l)
	return addr, err
}

// LV1XStakingBTCVaultPDA derives findProgramAddress(["lv1-stake-btc-vault", ledger], program).
func LV1XStakingBTCVaultPDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-stake-btc-vault"), l)
	return addr, err
}

// LV1XStakePositionPDA derives findProgramAddress(["lv1-stake-pos", ledger, owner], program).
func LV1XStakePositionPDA(programID, ledgerID, owner string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	ok, err := Key(owner)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-stake-pos"), l, ok)
	return addr, err
}

// LV1XTreasuryStatePDA derives findProgramAddress(["lv1-treasury", ledger], program).
func LV1XTreasuryStatePDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-treasury"), l)
	return addr, err
}

// LV1XTreasuryBTCVaultPDA derives findProgramAddress(["lv1-treasury-btc", ledger], program).
func LV1XTreasuryBTCVaultPDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-treasury-btc"), l)
	return addr, err
}

// LV1XYearPDA derives findProgramAddress(["lv1-year", ledger, u64le(year)], program).
func LV1XYearPDA(programID, ledgerID string, year uint64) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-year"), l, u64LEBytes(year))
	return addr, err
}

// LV1XLPSolPDA derives findProgramAddress(["lv1-lp-sol", ledger], program).
func LV1XLPSolPDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-lp-sol"), l)
	return addr, err
}

// LV1XLPSatPDA derives findProgramAddress(["lv1-lp-sat", ledger], program).
func LV1XLPSatPDA(programID, ledgerID string) (string, error) {
	l, err := Key(ledgerID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(programID, []byte("lv1-lp-sat"), l)
	return addr, err
}

// LV1XAssociatedTokenAddress derives the SPL Associated Token Account PDA for (owner, mint).
func LV1XAssociatedTokenAddress(owner, mint string) (string, error) {
	return TokenAccount(owner, mint)
}

// BPFLoaderProgramDataPDA derives the Upgradeable BPF Loader ProgramData address for programID.
func BPFLoaderProgramDataPDA(programID string) (string, error) {
	pk, err := Key(programID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(BPFLoaderUpgradeableID, pk)
	return addr, err
}

// VerifyBPFLoaderProgramDataPrefix validates BPF Loader Upgradeable account state,
// upgrade authority, zero-padded executable ELF prefix [45 : 45+expectedBytes] SHA-256,
// and trailing zero bytes. When expectedBytes <= 0, hashes the full data[45:] slice.
func VerifyBPFLoaderProgramDataPrefix(
	programID string,
	progAcctOwner, progAcctData, progDataAcctOwner, progDataAcctData []byte,
	expectedBytes int,
	expectedSHA256 string,
	allowedAuthorities []string,
	allowImmutable bool,
) (uint64, string, error) {
	loaderKey, _ := Key(BPFLoaderUpgradeableID)
	expectedDataAddr, err := BPFLoaderProgramDataPDA(programID)
	if err != nil {
		return 0, "", err
	}
	expectedDataKey, _ := Key(expectedDataAddr)
	if !bytes.Equal(progAcctOwner, loaderKey) || !bytes.Equal(progDataAcctOwner, loaderKey) {
		return 0, "", errors.New("BPF loader owner mismatch")
	}
	if len(progAcctData) != 36 || binary.LittleEndian.Uint32(progAcctData[:4]) != 2 || !bytes.Equal(progAcctData[4:36], expectedDataKey) {
		return 0, "", errors.New("BPF loader program account pointer mismatch")
	}
	if len(progDataAcctData) <= 45 || len(progDataAcctData) > 10*1024*1024 || binary.LittleEndian.Uint32(progDataAcctData[:4]) != 3 {
		return 0, "", errors.New("BPF loader programdata account invalid")
	}
	deploymentSlot := binary.LittleEndian.Uint64(progDataAcctData[4:12])
	hasAuth := progDataAcctData[12]
	upgradeAuthority := ""
	switch hasAuth {
	case 0:
		if !allowImmutable {
			return 0, "", errors.New("immutable BPF program forbidden by policy")
		}
	case 1:
		upgradeAuthority = Base58(progDataAcctData[13:45])
		if len(allowedAuthorities) > 0 {
			matched := false
			for _, a := range allowedAuthorities {
				if strings.TrimSpace(a) == upgradeAuthority {
					matched = true
					break
				}
			}
			if !matched {
				return 0, "", fmt.Errorf("BPF upgrade authority %s not in allowlist", upgradeAuthority)
			}
		}
	default:
		return 0, "", fmt.Errorf("invalid BPF upgrade authority option byte %d", hasAuth)
	}
	elfSlice := progDataAcctData[45:]
	if expectedBytes > 0 {
		if len(elfSlice) < expectedBytes {
			return 0, "", fmt.Errorf("BPF ELF truncated: have %d need >= %d bytes", len(elfSlice), expectedBytes)
		}
		for _, tailByte := range elfSlice[expectedBytes:] {
			if tailByte != 0 {
				return 0, "", errors.New("BPF ELF trailing bytes are not zero-padded")
			}
		}
		elfSlice = elfSlice[:expectedBytes]
	}
	sum := sha256.Sum256(elfSlice)
	got := hex.EncodeToString(sum[:])
	want := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if want == "" || got != want {
		return 0, "", fmt.Errorf("BPF ELF SHA-256 mismatch: got %s want %s", got, want)
	}
	return deploymentSlot, upgradeAuthority, nil
}

// VerifyBPFLoaderProgramData validates BPF Loader Upgradeable account state and deployed ELF SHA-256.
func VerifyBPFLoaderProgramData(programID string, progAcctOwner, progAcctData, progDataAcctOwner, progDataAcctData []byte, expectedSHA256 string) error {
	_, _, err := VerifyBPFLoaderProgramDataPrefix(
		programID, progAcctOwner, progAcctData, progDataAcctOwner, progDataAcctData,
		0, expectedSHA256, nil, true,
	)
	return err
}

// LV1XCommitmentHash computes SHA-256("lv1-commit-v1"||program||ledger||slot||owner||amount||nonce||selection||salt).
func LV1XCommitmentHash(programID, ledgerID string, slot uint64, owner string, amount, nonce uint64, selection uint8, salt [32]byte) ([32]byte, error) {
	var zero [32]byte
	if selection > 4 {
		return zero, errors.New("LV1X selection must be 0..4")
	}
	pk, err := Key(programID)
	if err != nil {
		return zero, err
	}
	lk, err := Key(ledgerID)
	if err != nil {
		return zero, err
	}
	ok, err := Key(owner)
	if err != nil {
		return zero, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("lv1-commit-v1"))
	_, _ = h.Write(pk)
	_, _ = h.Write(lk)
	_, _ = h.Write(u64LEBytes(slot))
	_, _ = h.Write(ok)
	_, _ = h.Write(u64LEBytes(amount))
	_, _ = h.Write(u64LEBytes(nonce))
	_, _ = h.Write([]byte{selection})
	_, _ = h.Write(salt[:])
	copy(zero[:], h.Sum(nil))
	return zero, nil
}

// LV1XCommitParams specifies an unsharded or sharded LV1X commit transaction.
type LV1XCommitParams struct {
	ProgramID  string
	LedgerID   string
	Owner      [32]byte
	Blockhash  [32]byte
	Slot       uint64
	ShardID    uint8 // LV1XUnshardedShardID (255) for unsharded, 0..31 for sharded
	EntryIndex uint32
	Amount     uint64
	Nonce      uint64
	Commitment [32]byte
	ComputeCU  uint32
	MicroLam   uint64
}

// LV1XClaimSolParams claims settled SOL winnings from a single round page.
type LV1XClaimSolParams struct {
	ProgramID   string
	LedgerID    string
	FeePayer    [32]byte
	Destination [32]byte
	Blockhash   [32]byte
	Slot        uint64
	ShardID     uint8
	Page        uint32
	EntriesMask uint8
	ComputeCU   uint32
	MicroLam    uint64
}

// LV1XSessionParams creates, stops, or closes an on-chain session PDA.
type LV1XSessionParams struct {
	ProgramID      string
	LedgerID       string
	Owner          [32]byte
	Blockhash      [32]byte
	Reference      uint64
	StrategyRoot   [32]byte
	FirstSlot      uint64
	LastSlot       uint64
	ExpiresAt      int64
	MaxEntry       uint64
	MaxTurnover    uint64
	ReturnToBudget bool
	Deposit        uint64
	Action         string // open | stop | close
	ComputeCU      uint32
	MicroLam       uint64
}

type legacyAccountSpec struct {
	key        [32]byte
	isSigner   bool
	isWritable bool
}

type legacyIxSpec struct {
	program  [32]byte
	accounts []int
	data     []byte
}

func appendComputeBudgetIxs(specs *[]legacyIxSpec, addKey func([32]byte, bool, bool) int, cuLimit uint32, microLamports uint64) {
	if cuLimit == 0 {
		cuLimit = 120_000
	}
	if microLamports < 1_000 {
		microLamports = 1_000
	}
	cbRaw, _ := Key(Compute)
	var cb [32]byte
	copy(cb[:], cbRaw)
	setCU := make([]byte, 5)
	setCU[0] = 2
	binary.LittleEndian.PutUint32(setCU[1:5], cuLimit)
	setPrice := make([]byte, 9)
	setPrice[0] = 3
	binary.LittleEndian.PutUint64(setPrice[1:9], microLamports)
	_ = addKey(cb, false, false)
	*specs = append(*specs,
		legacyIxSpec{program: cb, data: setCU},
		legacyIxSpec{program: cb, data: setPrice},
	)
}

func encodeLegacyMessage(feePayer [32]byte, blockhash [32]byte, keysIn []legacyAccountSpec, ixs []legacyIxSpec) ([]byte, error) {
	// Canonical Solana legacy account ordering:
	// 1. writable signers (feePayer first)
	// 2. readonly signers
	// 3. writable non-signers
	// 4. readonly non-signers
	var wSign, roSign, wNon, roNon []legacyAccountSpec
	for _, k := range keysIn {
		switch {
		case k.isSigner && k.isWritable:
			wSign = append(wSign, k)
		case k.isSigner && !k.isWritable:
			roSign = append(roSign, k)
		case !k.isSigner && k.isWritable:
			wNon = append(wNon, k)
		default:
			roNon = append(roNon, k)
		}
	}
	if len(wSign) == 0 || wSign[0].key != feePayer {
		return nil, errors.New("fee payer must be writable signer #0")
	}
	ordered := make([]legacyAccountSpec, 0, len(keysIn))
	ordered = append(ordered, wSign...)
	ordered = append(ordered, roSign...)
	ordered = append(ordered, wNon...)
	ordered = append(ordered, roNon...)
	if len(ordered) > 48 {
		return nil, errors.New("LV1X legacy message exceeds 48 accounts")
	}
	idxByKey := make(map[[32]byte]byte, len(ordered))
	for i, k := range ordered {
		idxByKey[k.key] = byte(i)
	}
	numReq := byte(len(wSign) + len(roSign))
	numROSign := byte(len(roSign))
	numROUnsigned := byte(len(roNon))

	var msg []byte
	msg = append(msg, numReq, numROSign, numROUnsigned)
	msg = append(msg, short(len(ordered))...)
	for _, k := range ordered {
		msg = append(msg, k.key[:]...)
	}
	msg = append(msg, blockhash[:]...)
	msg = append(msg, short(len(ixs))...)
	for _, ix := range ixs {
		progIdx, ok := idxByKey[ix.program]
		if !ok {
			return nil, errors.New("missing program key in account table")
		}
		msg = append(msg, progIdx)
		msg = append(msg, short(len(ix.accounts))...)
		for _, rawPos := range ix.accounts {
			acctIdx := idxByKey[keysIn[rawPos].key]
			msg = append(msg, acctIdx)
		}
		msg = append(msg, short(len(ix.data))...)
		msg = append(msg, ix.data...)
	}
	if len(msg)+65 > 1232 {
		return nil, errors.New("LV1X message exceeds 1232 bytes")
	}
	return msg, nil
}

func mustKey32(b58 string) ([32]byte, error) {
	var out [32]byte
	raw, err := Key(b58)
	if err != nil {
		return out, err
	}
	copy(out[:], raw)
	return out, nil
}

// lv1xIxPrefix returns a buffer initialized with the canonical 5-byte "LV1X\x01" header + 1-byte tag.
func lv1xIxPrefix(tag uint8, payloadCap int) []byte {
	buf := make([]byte, 0, 6+payloadCap)
	return append(buf, 'L', 'V', '1', 'X', LV1XVersion, tag)
}

// BuildLV1XCommitMessage builds an unsharded (Tag 5) or sharded (Tag 35) LV1X Commit message.
func BuildLV1XCommitMessage(p LV1XCommitParams) ([]byte, error) {
	if p.Amount < LV1XMinCommitLamports {
		return nil, fmt.Errorf("LV1X commit below minimum %d lamports", LV1XMinCommitLamports)
	}
	ownerB58 := Base58(p.Owner[:])
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	group := p.Nonce / 64
	nkAddr, err := LV1XNoncePDA(p.ProgramID, rkAddr, ownerB58, group)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)
	nkPK, _ := mustKey32(nkAddr)
	sysPK, _ := mustKey32(System)

	var specs []legacyAccountSpec
	posMap := map[[32]byte]int{}
	addKey := func(k [32]byte, signer, writable bool) int {
		if idx, ok := posMap[k]; ok {
			if signer {
				specs[idx].isSigner = true
			}
			if writable {
				specs[idx].isWritable = true
			}
			return idx
		}
		posMap[k] = len(specs)
		specs = append(specs, legacyAccountSpec{key: k, isSigner: signer, isWritable: writable})
		return len(specs) - 1
	}

	iOwner := addKey(p.Owner, true, true)
	var ixs []legacyIxSpec
	appendComputeBudgetIxs(&ixs, addKey, p.ComputeCU, p.MicroLam)

	iRT := addKey(rtPK, false, false)
	page := p.EntryIndex / LV1XPageEntries
	if p.ShardID == LV1XUnshardedShardID {
		iRK := addKey(rkPK, false, true)
		pkAddr, err := LV1XPagePDA(p.ProgramID, rkAddr, page)
		if err != nil {
			return nil, err
		}
		pkPK, _ := mustKey32(pkAddr)
		iPK := addKey(pkPK, false, true)
		iNK := addKey(nkPK, false, true)
		iSys := addKey(sysPK, false, false)
		_ = addKey(progPK, false, false)

		data := make([]byte, 5+1+8+8+8+32)
		copy(data[:5], lv1xIxPrefix(LV1XTagCommit, 0))
		data[5] = LV1XTagCommit
		binary.LittleEndian.PutUint64(data[6:14], p.Slot)
		binary.LittleEndian.PutUint64(data[14:22], p.Amount)
		binary.LittleEndian.PutUint64(data[22:30], p.Nonce)
		copy(data[30:62], p.Commitment[:])

		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iRK, iPK, iNK, iOwner, iOwner, iSys},
			data:     data,
		})
	} else {
		skAddr, err := LV1XShardPDA(p.ProgramID, rkAddr, p.ShardID)
		if err != nil {
			return nil, err
		}
		pkAddr, err := LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, page)
		if err != nil {
			return nil, err
		}
		skPK, _ := mustKey32(skAddr)
		pkPK, _ := mustKey32(pkAddr)
		iRK := addKey(rkPK, false, false)
		iSK := addKey(skPK, false, true)
		iPK := addKey(pkPK, false, true)
		iNK := addKey(nkPK, false, true)
		iSys := addKey(sysPK, false, false)
		_ = addKey(progPK, false, false)

		data := make([]byte, 5+1+8+1+8+8+32)
		copy(data[:5], lv1xIxPrefix(LV1XTagCommitToShard, 0))
		data[5] = LV1XTagCommitToShard
		binary.LittleEndian.PutUint64(data[6:14], p.Slot)
		data[14] = p.ShardID
		binary.LittleEndian.PutUint64(data[15:23], p.Amount)
		binary.LittleEndian.PutUint64(data[23:31], p.Nonce)
		copy(data[31:63], p.Commitment[:])

		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iRK, iSK, iPK, iNK, iOwner, iOwner, iSys},
			data:     data,
		})
	}
	return encodeLegacyMessage(p.Owner, p.Blockhash, specs, ixs)
}

// BuildLV1XClaimSolBatchMessage builds ClaimSolBatch (Tag 13) or ClaimShardSolBatch (Tag 42).
func BuildLV1XClaimSolBatchMessage(p LV1XClaimSolParams) ([]byte, error) {
	if p.EntriesMask == 0 {
		return nil, errors.New("non-zero 8-bit entries mask required")
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)

	var specs []legacyAccountSpec
	posMap := map[[32]byte]int{}
	addKey := func(k [32]byte, signer, writable bool) int {
		if idx, ok := posMap[k]; ok {
			if signer {
				specs[idx].isSigner = true
			}
			if writable {
				specs[idx].isWritable = true
			}
			return idx
		}
		posMap[k] = len(specs)
		specs = append(specs, legacyAccountSpec{key: k, isSigner: signer, isWritable: writable})
		return len(specs) - 1
	}

	_ = addKey(p.FeePayer, true, true)
	var ixs []legacyIxSpec
	appendComputeBudgetIxs(&ixs, addKey, p.ComputeCU, p.MicroLam)

	iRT := addKey(rtPK, false, false)
	iRK := addKey(rkPK, false, true)
	iDest := addKey(p.Destination, false, true)
	_ = addKey(progPK, false, false)

	if p.ShardID == LV1XUnshardedShardID {
		pkAddr, err := LV1XPagePDA(p.ProgramID, rkAddr, p.Page)
		if err != nil {
			return nil, err
		}
		pkPK, _ := mustKey32(pkAddr)
		iPK := addKey(pkPK, false, true)
		data := lv1xIxPrefix(LV1XTagClaimSolBatch, 8+4+1)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, u32LEBytes(p.Page)...)
		data = append(data, p.EntriesMask)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iRK, iPK, iDest},
			data:     data,
		})
	} else {
		pkAddr, err := LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.Page)
		if err != nil {
			return nil, err
		}
		pkPK, _ := mustKey32(pkAddr)
		iPK := addKey(pkPK, false, true)
		data := lv1xIxPrefix(LV1XTagClaimShardSolBatch, 8+1+4+1)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, p.ShardID)
		data = append(data, u32LEBytes(p.Page)...)
		data = append(data, p.EntriesMask)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iRK, iPK, iDest},
			data:     data,
		})
	}
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, specs, ixs)
}

// LV1XRevealItem describes one entry reveal payload inside a page.
type LV1XRevealItem struct {
	EntryRow  uint8
	Selection uint8
	Salt      [32]byte
}

// LV1XRevealParams builds RevealBatch (Tag 7) or RevealPageBatch (Tag 37).
type LV1XRevealParams struct {
	ProgramID string
	LedgerID  string
	FeePayer  [32]byte
	Blockhash [32]byte
	Slot      uint64
	ShardID   uint8
	Page      uint32
	Items     []LV1XRevealItem
	ComputeCU uint32
	MicroLam  uint64
}

// LV1XClaimTokenParams builds ClaimSat / ClaimShardSat or ClaimBtc / ClaimShardBtc (or Batch variants).
type LV1XClaimTokenParams struct {
	ProgramID   string
	LedgerID    string
	Owner       [32]byte
	Blockhash   [32]byte
	Slot        uint64
	ShardID     uint8
	Page        uint32
	EntryRow    uint8
	EntriesMask uint8
	BTCMint     string // Required when Claiming BTC
	CreateATA   bool
	SolEscape   bool // Escape BTC claim directly to owner SOL lamports when frozen
	ComputeCU   uint32
	MicroLam    uint64
}

// LV1XCloseNonceParams builds CloseNonce (Tag 18) to reclaim owner's nonce PDA rent.
type LV1XCloseNonceParams struct {
	ProgramID string
	LedgerID  string
	FeePayer  [32]byte
	Owner     [32]byte
	Blockhash [32]byte
	Slot      uint64
	Nonce     uint64 // Group is derived as Nonce / 64 unless Group is set explicitly via SetGroup
	Group     uint64
	UseGroup  bool
	ComputeCU uint32
	MicroLam  uint64
}

// LV1XFallbackSettleParams parameterizes Tier 2 permissionless fallback settlement builders.
type LV1XFallbackSettleParams struct {
	ProgramID string
	LedgerID  string
	FeePayer  [32]byte
	Blockhash [32]byte
	Slot      uint64
	ShardID   uint8
	ShardIDs  []uint8
	StartPage uint32
	PageCount uint8
	ComputeCU uint32
	MicroLam  uint64
}

// LV1XStakeParams builds Stake (Tag 21), ActivateStake (Tag 22), ClaimStakingReward (Tag 25), or Unstake (Tag 26).
type LV1XStakeParams struct {
	ProgramID string
	LedgerID  string
	Owner     [32]byte
	Blockhash [32]byte
	Action    string // stake | activate | claim_reward | unstake
	Amount    uint64 // SAT atoms (9 decimals)
	BTCMint   string // Required for claim_reward
	CreateATA bool
	ComputeCU uint32
	MicroLam  uint64
}

type lv1xMsgBuilder struct {
	specs  []legacyAccountSpec
	posMap map[[32]byte]int
	ixs    []legacyIxSpec
}

func newLV1XMsgBuilder(feePayer [32]byte, cu uint32, microLam uint64) (*lv1xMsgBuilder, int) {
	b := &lv1xMsgBuilder{posMap: make(map[[32]byte]int)}
	iPayer := b.addKey(feePayer, true, true)
	appendComputeBudgetIxs(&b.ixs, b.addKey, cu, microLam)
	return b, iPayer
}

func (b *lv1xMsgBuilder) addKey(k [32]byte, signer, writable bool) int {
	if idx, ok := b.posMap[k]; ok {
		if signer {
			b.specs[idx].isSigner = true
		}
		if writable {
			b.specs[idx].isWritable = true
		}
		return idx
	}
	b.posMap[k] = len(b.specs)
	b.specs = append(b.specs, legacyAccountSpec{key: k, isSigner: signer, isWritable: writable})
	return len(b.specs) - 1
}

func (b *lv1xMsgBuilder) appendCreateATAIdempotent(iPayer int, ataPK, ownerPK, mintPK [32]byte) error {
	ataProgPK, err := mustKey32(AssociatedToken)
	if err != nil {
		return err
	}
	sysPK, err := mustKey32(System)
	if err != nil {
		return err
	}
	tokPK, err := mustKey32(Token)
	if err != nil {
		return err
	}
	iATA := b.addKey(ataPK, false, true)
	iOwner := b.addKey(ownerPK, false, false)
	iMint := b.addKey(mintPK, false, false)
	iSys := b.addKey(sysPK, false, false)
	iTok := b.addKey(tokPK, false, false)
	_ = b.addKey(ataProgPK, false, false)
	b.ixs = append(b.ixs, legacyIxSpec{
		program:  ataProgPK,
		accounts: []int{iPayer, iATA, iOwner, iMint, iSys, iTok},
		data:     []byte{1}, // CreateIdempotent
	})
	return nil
}

// BuildLV1XRevealMessage builds RevealBatch (Tag 7) or RevealPageBatch (Tag 37).
func BuildLV1XRevealMessage(p LV1XRevealParams) ([]byte, error) {
	if len(p.Items) == 0 || len(p.Items) > LV1XPageEntries {
		return nil, errors.New("reveal batch requires 1..8 items")
	}
	for i, it := range p.Items {
		if int(it.EntryRow) >= LV1XPageEntries {
			return nil, errors.New("reveal entry row must be 0..7")
		}
		if it.Selection > 4 {
			return nil, errors.New("reveal selection must be 0..4")
		}
		if i > 0 && it.EntryRow <= p.Items[i-1].EntryRow {
			return nil, errors.New("reveal entry rows must be strictly ascending")
		}
	}
	if p.ShardID != LV1XUnshardedShardID && int(p.ShardID) >= LV1XMaxShards {
		return nil, errors.New("invalid LV1X shard id")
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	var pkAddr string
	if p.ShardID == LV1XUnshardedShardID {
		pkAddr, err = LV1XPagePDA(p.ProgramID, rkAddr, p.Page)
	} else {
		pkAddr, err = LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.Page)
	}
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)
	pkPK, _ := mustKey32(pkAddr)

	b, _ := newLV1XMsgBuilder(p.FeePayer, p.ComputeCU, p.MicroLam)
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, false)
	iPK := b.addKey(pkPK, false, true)
	_ = b.addKey(progPK, false, false)

	var data []byte
	if p.ShardID == LV1XUnshardedShardID && p.Page == 0 {
		data = lv1xIxPrefix(LV1XTagRevealBatch, 8+1+len(p.Items)*34)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, byte(len(p.Items)))
	} else {
		data = lv1xIxPrefix(LV1XTagRevealPageBatch, 8+1+4+1+len(p.Items)*34)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, p.ShardID)
		data = append(data, u32LEBytes(p.Page)...)
		data = append(data, byte(len(p.Items)))
	}
	for _, it := range p.Items {
		data = append(data, it.EntryRow, it.Selection)
		data = append(data, it.Salt[:]...)
	}
	b.ixs = append(b.ixs, legacyIxSpec{
		program:  progPK,
		accounts: []int{iRT, iRK, iPK},
		data:     data,
	})
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XClaimSatMessage builds ClaimSat (Tag 14) or ClaimShardSat (Tag 43) with optional ATA creation.
func BuildLV1XClaimSatMessage(p LV1XClaimTokenParams) ([]byte, error) {
	if int(p.EntryRow) >= LV1XPageEntries {
		return nil, errors.New("entry row must be 0..7")
	}
	if p.ShardID != LV1XUnshardedShardID && int(p.ShardID) >= LV1XMaxShards {
		return nil, errors.New("invalid LV1X shard id")
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	mintAddr, err := LV1XMintPDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	issAddr, err := LV1XIssuancePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	ownerB58 := Base58(p.Owner[:])
	ataAddr, err := LV1XAssociatedTokenAddress(ownerB58, mintAddr)
	if err != nil {
		return nil, err
	}
	var pkAddr string
	if p.ShardID == LV1XUnshardedShardID {
		pkAddr, err = LV1XPagePDA(p.ProgramID, rkAddr, p.Page)
	} else {
		pkAddr, err = LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.Page)
	}
	if err != nil {
		return nil, err
	}

	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)
	pkPK, _ := mustKey32(pkAddr)
	mintPK, _ := mustKey32(mintAddr)
	issPK, _ := mustKey32(issAddr)
	ataPK, _ := mustKey32(ataAddr)
	tokPK, _ := mustKey32(Token)

	b, iPayer := newLV1XMsgBuilder(p.Owner, p.ComputeCU, p.MicroLam)
	if p.CreateATA {
		if err := b.appendCreateATAIdempotent(iPayer, ataPK, p.Owner, mintPK); err != nil {
			return nil, err
		}
	}
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, false)
	iPK := b.addKey(pkPK, false, true)
	iMint := b.addKey(mintPK, false, true)
	iIss := b.addKey(issPK, false, false)
	iATA := b.addKey(ataPK, false, true)
	iTok := b.addKey(tokPK, false, false)
	_ = b.addKey(progPK, false, false)

	var data []byte
	if p.ShardID == LV1XUnshardedShardID {
		data = lv1xIxPrefix(LV1XTagClaimSat, 8+4+1)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, u32LEBytes(p.Page)...)
		data = append(data, p.EntryRow)
	} else {
		data = lv1xIxPrefix(LV1XTagClaimShardSat, 8+1+4+1)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, p.ShardID)
		data = append(data, u32LEBytes(p.Page)...)
		data = append(data, p.EntryRow)
	}
	b.ixs = append(b.ixs, legacyIxSpec{
		program:  progPK,
		accounts: []int{iRT, iRK, iPK, iMint, iIss, iATA, iTok},
		data:     data,
	})
	return encodeLegacyMessage(p.Owner, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XClaimBtcMessage builds ClaimBtc (Tag 16) or ClaimShardBtc (Tag 44).
func BuildLV1XClaimBtcMessage(p LV1XClaimTokenParams) ([]byte, error) {
	if int(p.EntryRow) >= LV1XPageEntries {
		return nil, errors.New("entry row must be 0..7")
	}
	if p.ShardID != LV1XUnshardedShardID && int(p.ShardID) >= LV1XMaxShards {
		return nil, errors.New("invalid LV1X shard id")
	}
	btcMint := strings.TrimSpace(p.BTCMint)
	if btcMint == "" {
		return nil, errors.New("btcMint required")
	}
	btcMintPK, err := mustKey32(btcMint)
	if err != nil {
		return nil, err
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	vaultAddr, err := LV1XMinerBTCVaultPDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	ownerB58 := Base58(p.Owner[:])
	ataAddr, err := LV1XAssociatedTokenAddress(ownerB58, btcMint)
	if err != nil {
		return nil, err
	}
	var pkAddr string
	if p.ShardID == LV1XUnshardedShardID {
		pkAddr, err = LV1XPagePDA(p.ProgramID, rkAddr, p.Page)
	} else {
		pkAddr, err = LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.Page)
	}
	if err != nil {
		return nil, err
	}

	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)
	pkPK, _ := mustKey32(pkAddr)
	vaultPK, _ := mustKey32(vaultAddr)
	ataPK, _ := mustKey32(ataAddr)
	tokPK, _ := mustKey32(Token)

	b, iPayer := newLV1XMsgBuilder(p.Owner, p.ComputeCU, p.MicroLam)
	if p.CreateATA {
		if err := b.appendCreateATAIdempotent(iPayer, ataPK, p.Owner, btcMintPK); err != nil {
			return nil, err
		}
	}
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, true)
	iPK := b.addKey(pkPK, false, true)
	iVault := b.addKey(vaultPK, false, true)
	iATA := b.addKey(ataPK, false, true)
	iTok := b.addKey(tokPK, false, false)
	accounts := []int{iRT, iRK, iPK, iVault, iATA, iTok}
	if p.SolEscape {
		iEscape := b.addKey(p.Owner, false, true)
		accounts = append(accounts, iEscape)
	}
	_ = b.addKey(progPK, false, false)

	var data []byte
	if p.ShardID == LV1XUnshardedShardID {
		data = lv1xIxPrefix(LV1XTagClaimBtc, 8+4+1)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, u32LEBytes(p.Page)...)
		data = append(data, p.EntryRow)
	} else {
		data = lv1xIxPrefix(LV1XTagClaimShardBtc, 8+1+4+1)
		data = append(data, u64LEBytes(p.Slot)...)
		data = append(data, p.ShardID)
		data = append(data, u32LEBytes(p.Page)...)
		data = append(data, p.EntryRow)
	}
	b.ixs = append(b.ixs, legacyIxSpec{
		program:  progPK,
		accounts: accounts,
		data:     data,
	})
	return encodeLegacyMessage(p.Owner, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XClaimSatBatchMessage builds ClaimSatBatch (Tag 52) across <= 8 page rows.
func BuildLV1XClaimSatBatchMessage(p LV1XClaimTokenParams) ([]byte, error) {
	if p.EntriesMask == 0 {
		return nil, errors.New("non-zero 8-bit entries mask required")
	}
	if p.ShardID != LV1XUnshardedShardID && int(p.ShardID) >= LV1XMaxShards {
		return nil, errors.New("invalid LV1X shard id")
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	mintAddr, err := LV1XMintPDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	issAddr, err := LV1XIssuancePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	ownerB58 := Base58(p.Owner[:])
	ataAddr, err := LV1XAssociatedTokenAddress(ownerB58, mintAddr)
	if err != nil {
		return nil, err
	}
	var pkAddr string
	if p.ShardID == LV1XUnshardedShardID {
		pkAddr, err = LV1XPagePDA(p.ProgramID, rkAddr, p.Page)
	} else {
		pkAddr, err = LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.Page)
	}
	if err != nil {
		return nil, err
	}

	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)
	pkPK, _ := mustKey32(pkAddr)
	mintPK, _ := mustKey32(mintAddr)
	issPK, _ := mustKey32(issAddr)
	ataPK, _ := mustKey32(ataAddr)
	tokPK, _ := mustKey32(Token)

	b, iPayer := newLV1XMsgBuilder(p.Owner, p.ComputeCU, p.MicroLam)
	if p.CreateATA {
		if err := b.appendCreateATAIdempotent(iPayer, ataPK, p.Owner, mintPK); err != nil {
			return nil, err
		}
	}
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, false)
	iPK := b.addKey(pkPK, false, true)
	iMint := b.addKey(mintPK, false, true)
	iIss := b.addKey(issPK, false, false)
	iATA := b.addKey(ataPK, false, true)
	iTok := b.addKey(tokPK, false, false)
	_ = b.addKey(progPK, false, false)

	data := lv1xIxPrefix(LV1XTagClaimSatBatch, 8+1+4+1)
	data = append(data, u64LEBytes(p.Slot)...)
	data = append(data, p.ShardID)
	data = append(data, u32LEBytes(p.Page)...)
	data = append(data, p.EntriesMask)
	b.ixs = append(b.ixs, legacyIxSpec{
		program:  progPK,
		accounts: []int{iRT, iRK, iPK, iMint, iIss, iATA, iTok},
		data:     data,
	})
	return encodeLegacyMessage(p.Owner, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XClaimBtcBatchMessage builds ClaimBtcBatch (Tag 53), routing to owner SOL when SolEscape is true.
func BuildLV1XClaimBtcBatchMessage(p LV1XClaimTokenParams) ([]byte, error) {
	if p.EntriesMask == 0 {
		return nil, errors.New("non-zero 8-bit entries mask required")
	}
	if p.ShardID != LV1XUnshardedShardID && int(p.ShardID) >= LV1XMaxShards {
		return nil, errors.New("invalid LV1X shard id")
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	vaultAddr, err := LV1XMinerBTCVaultPDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	var pkAddr string
	if p.ShardID == LV1XUnshardedShardID {
		pkAddr, err = LV1XPagePDA(p.ProgramID, rkAddr, p.Page)
	} else {
		pkAddr, err = LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.Page)
	}
	if err != nil {
		return nil, err
	}

	b, iPayer := newLV1XMsgBuilder(p.Owner, p.ComputeCU, p.MicroLam)
	var destPK [32]byte
	if p.SolEscape {
		destPK = p.Owner
	} else {
		btcMint := strings.TrimSpace(p.BTCMint)
		if btcMint == "" {
			return nil, errors.New("btcMint required unless solEscape is set")
		}
		btcMintPK, mErr := mustKey32(btcMint)
		if mErr != nil {
			return nil, mErr
		}
		ataAddr, aErr := LV1XAssociatedTokenAddress(Base58(p.Owner[:]), btcMint)
		if aErr != nil {
			return nil, aErr
		}
		destPK, _ = mustKey32(ataAddr)
		if p.CreateATA {
			if err := b.appendCreateATAIdempotent(iPayer, destPK, p.Owner, btcMintPK); err != nil {
				return nil, err
			}
		}
	}

	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)
	pkPK, _ := mustKey32(pkAddr)
	vaultPK, _ := mustKey32(vaultAddr)
	tokPK, _ := mustKey32(Token)

	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, true)
	iPK := b.addKey(pkPK, false, true)
	iVault := b.addKey(vaultPK, false, true)
	iDest := b.addKey(destPK, false, true)
	iTok := b.addKey(tokPK, false, false)
	_ = b.addKey(progPK, false, false)

	data := lv1xIxPrefix(LV1XTagClaimBtcBatch, 8+1+4+1)
	data = append(data, u64LEBytes(p.Slot)...)
	data = append(data, p.ShardID)
	data = append(data, u32LEBytes(p.Page)...)
	data = append(data, p.EntriesMask)
	b.ixs = append(b.ixs, legacyIxSpec{
		program:  progPK,
		accounts: []int{iRT, iRK, iPK, iVault, iDest, iTok},
		data:     data,
	})
	return encodeLegacyMessage(p.Owner, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XCloseNonceMessage builds CloseNonce (Tag 18), refunding nonce PDA rent to FeePayer.
func BuildLV1XCloseNonceMessage(p LV1XCloseNonceParams) ([]byte, error) {
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	group := p.Nonce / 64
	if p.UseGroup {
		group = p.Group
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	ownerB58 := Base58(p.Owner[:])
	nkAddr, err := LV1XNoncePDA(p.ProgramID, rkAddr, ownerB58, group)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)
	nkPK, _ := mustKey32(nkAddr)

	b, iPayer := newLV1XMsgBuilder(p.FeePayer, p.ComputeCU, p.MicroLam)
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, false)
	iNK := b.addKey(nkPK, false, true)
	_ = b.addKey(progPK, false, false)

	data := lv1xIxPrefix(LV1XTagCloseNonce, 8+32+8)
	data = append(data, u64LEBytes(p.Slot)...)
	data = append(data, p.Owner[:]...)
	data = append(data, u64LEBytes(group)...)
	b.ixs = append(b.ixs, legacyIxSpec{
		program:  progPK,
		accounts: []int{iRT, iRK, iNK, iPayer},
		data:     data,
	})
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XMergeShardsBatchMessage builds MergeShardsBatch (Tag 38).
func BuildLV1XMergeShardsBatchMessage(p LV1XFallbackSettleParams) ([]byte, error) {
	if len(p.ShardIDs) == 0 || len(p.ShardIDs) > 16 {
		return nil, errors.New("MergeShardsBatch requires 1..16 unique shard IDs")
	}
	seen := make(map[uint8]bool, len(p.ShardIDs))
	for _, sid := range p.ShardIDs {
		if int(sid) >= LV1XMaxShards || seen[sid] {
			return nil, fmt.Errorf("invalid or duplicate shardId %d", sid)
		}
		seen[sid] = true
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)

	b, _ := newLV1XMsgBuilder(p.FeePayer, p.ComputeCU, p.MicroLam)
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, true)
	accts := []int{iRT, iRK}
	data := lv1xIxPrefix(LV1XTagMergeShardsBatch, 8+1+len(p.ShardIDs))
	data = append(data, u64LEBytes(p.Slot)...)
	data = append(data, uint8(len(p.ShardIDs)))
	for _, sid := range p.ShardIDs {
		shAddr, sErr := LV1XShardPDA(p.ProgramID, rkAddr, sid)
		if sErr != nil {
			return nil, sErr
		}
		shPK, _ := mustKey32(shAddr)
		accts = append(accts, b.addKey(shPK, false, true))
		data = append(data, sid)
	}
	_ = b.addKey(progPK, false, false)
	b.ixs = append(b.ixs, legacyIxSpec{program: progPK, accounts: accts, data: data})
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XBeginSettlementMessage builds BeginSettlement (Tag 9).
func BuildLV1XBeginSettlementMessage(p LV1XFallbackSettleParams) ([]byte, error) {
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)

	b, _ := newLV1XMsgBuilder(p.FeePayer, p.ComputeCU, p.MicroLam)
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, true)
	_ = b.addKey(progPK, false, false)
	data := lv1xIxPrefix(LV1XTagBeginSettlement, 8)
	data = append(data, u64LEBytes(p.Slot)...)
	b.ixs = append(b.ixs, legacyIxSpec{program: progPK, accounts: []int{iRT, iRK}, data: data})
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, b.specs, b.ixs)
}

func buildLV1XPagesBatchSettlementMessage(tag uint8, p LV1XFallbackSettleParams) ([]byte, error) {
	if p.PageCount < 1 || p.PageCount > 8 {
		return nil, errors.New("pageCount must be 1..8")
	}
	if p.ShardID != LV1XUnshardedShardID && int(p.ShardID) >= LV1XMaxShards {
		return nil, errors.New("invalid LV1X shard id")
	}
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	rkPK, _ := mustKey32(rkAddr)

	b, _ := newLV1XMsgBuilder(p.FeePayer, p.ComputeCU, p.MicroLam)
	iRT := b.addKey(rtPK, false, false)
	iRK := b.addKey(rkPK, false, true)
	accts := []int{iRT, iRK}
	if p.ShardID != LV1XUnshardedShardID {
		shAddr, sErr := LV1XShardPDA(p.ProgramID, rkAddr, p.ShardID)
		if sErr != nil {
			return nil, sErr
		}
		shPK, _ := mustKey32(shAddr)
		accts = append(accts, b.addKey(shPK, false, true))
	}
	for i := uint32(0); i < uint32(p.PageCount); i++ {
		var pgAddr string
		if p.ShardID == LV1XUnshardedShardID {
			pgAddr, err = LV1XPagePDA(p.ProgramID, rkAddr, p.StartPage+i)
		} else {
			pgAddr, err = LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.StartPage+i)
		}
		if err != nil {
			return nil, err
		}
		pgPK, _ := mustKey32(pgAddr)
		accts = append(accts, b.addKey(pgPK, false, true))
	}
	_ = b.addKey(progPK, false, false)
	data := lv1xIxPrefix(tag, 8+1+4+1)
	data = append(data, u64LEBytes(p.Slot)...)
	data = append(data, p.ShardID)
	data = append(data, u32LEBytes(p.StartPage)...)
	data = append(data, p.PageCount)
	b.ixs = append(b.ixs, legacyIxSpec{program: progPK, accounts: accts, data: data})
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XScanPagesBatchMessage builds ScanPagesBatch (Tag 40).
func BuildLV1XScanPagesBatchMessage(p LV1XFallbackSettleParams) ([]byte, error) {
	return buildLV1XPagesBatchSettlementMessage(LV1XTagScanPagesBatch, p)
}

// BuildLV1XAllocatePagesBatchMessage builds AllocatePagesBatch (Tag 41).
func BuildLV1XAllocatePagesBatchMessage(p LV1XFallbackSettleParams) ([]byte, error) {
	return buildLV1XPagesBatchSettlementMessage(LV1XTagAllocatePagesBatch, p)
}

// BuildLV1XFinishSettlementMessage builds FinishSettlement (Tag 12).
func BuildLV1XFinishSettlementMessage(p LV1XFallbackSettleParams) ([]byte, error) {
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	year := p.Slot / LV1XSlotsPerYear
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	ykAddr, err := LV1XYearPDA(p.ProgramID, p.LedgerID, year)
	if err != nil {
		return nil, err
	}
	rkAddr, err := LV1XRoundPDA(p.ProgramID, p.LedgerID, p.Slot)
	if err != nil {
		return nil, err
	}
	lpSolAddr, _ := LV1XLPSolPDA(p.ProgramID, p.LedgerID)
	lpSatAddr, _ := LV1XLPSatPDA(p.ProgramID, p.LedgerID)
	mintAddr, _ := LV1XMintPDA(p.ProgramID, p.LedgerID)
	issAddr, _ := LV1XIssuancePDA(p.ProgramID, p.LedgerID)
	stAddr, _ := LV1XStakingStatePDA(p.ProgramID, p.LedgerID)
	trAddr, _ := LV1XTreasuryStatePDA(p.ProgramID, p.LedgerID)

	rtPK, _ := mustKey32(rtAddr)
	ykPK, _ := mustKey32(ykAddr)
	rkPK, _ := mustKey32(rkAddr)
	lpSolPK, _ := mustKey32(lpSolAddr)
	lpSatPK, _ := mustKey32(lpSatAddr)
	mintPK, _ := mustKey32(mintAddr)
	issPK, _ := mustKey32(issAddr)
	stPK, _ := mustKey32(stAddr)
	trPK, _ := mustKey32(trAddr)
	tokPK, _ := mustKey32(Token)

	b, _ := newLV1XMsgBuilder(p.FeePayer, p.ComputeCU, p.MicroLam)
	accts := []int{
		b.addKey(rtPK, false, true),
		b.addKey(ykPK, false, true),
		b.addKey(rkPK, false, true),
		b.addKey(lpSolPK, false, true),
		b.addKey(lpSatPK, false, true),
		b.addKey(mintPK, false, true),
		b.addKey(issPK, false, false),
		b.addKey(stPK, false, true),
		b.addKey(trPK, false, true),
		b.addKey(tokPK, false, false),
	}
	_ = b.addKey(progPK, false, false)
	data := lv1xIxPrefix(LV1XTagFinishSettlement, 8)
	data = append(data, u64LEBytes(p.Slot)...)
	b.ixs = append(b.ixs, legacyIxSpec{program: progPK, accounts: accts, data: data})
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XStakeMessage builds Stake (21), ActivateStake (22), ClaimStakingReward (25), or Unstake (26).
func BuildLV1XStakeMessage(p LV1XStakeParams) ([]byte, error) {
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	ownerB58 := Base58(p.Owner[:])
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	stAddr, err := LV1XStakingStatePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	posAddr, err := LV1XStakePositionPDA(p.ProgramID, p.LedgerID, ownerB58)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	stPK, _ := mustKey32(stAddr)
	posPK, _ := mustKey32(posAddr)
	sysPK, _ := mustKey32(System)
	tokPK, _ := mustKey32(Token)

	b, iOwner := newLV1XMsgBuilder(p.Owner, p.ComputeCU, p.MicroLam)
	iRT := b.addKey(rtPK, false, false)
	iSt := b.addKey(stPK, false, true)
	iPos := b.addKey(posPK, false, true)
	_ = b.addKey(progPK, false, false)

	act := strings.ToLower(strings.TrimSpace(p.Action))
	switch act {
	case "stake":
		if p.Amount < LV1XMinStakeAtoms {
			return nil, fmt.Errorf("stake below minimum %d SAT atoms", LV1XMinStakeAtoms)
		}
		vaultAddr, err := LV1XStakingVaultPDA(p.ProgramID, p.LedgerID)
		if err != nil {
			return nil, err
		}
		mintAddr, err := LV1XMintPDA(p.ProgramID, p.LedgerID)
		if err != nil {
			return nil, err
		}
		ataAddr, err := LV1XAssociatedTokenAddress(ownerB58, mintAddr)
		if err != nil {
			return nil, err
		}
		vaultPK, _ := mustKey32(vaultAddr)
		ataPK, _ := mustKey32(ataAddr)
		iVault := b.addKey(vaultPK, false, true)
		iATA := b.addKey(ataPK, false, true)
		iSys := b.addKey(sysPK, false, false)
		iTok := b.addKey(tokPK, false, false)
		data := lv1xIxPrefix(LV1XTagStake, 8)
		data = append(data, u64LEBytes(p.Amount)...)
		b.ixs = append(b.ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSt, iPos, iVault, iATA, iOwner, iSys, iTok},
			data:     data,
		})

	case "activate":
		b.ixs = append(b.ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSt, iPos},
			data:     lv1xIxPrefix(LV1XTagActivateStake, 0),
		})

	case "claim_reward":
		btcMint := strings.TrimSpace(p.BTCMint)
		if btcMint == "" {
			return nil, errors.New("btcMint required for claim_reward")
		}
		btcMintPK, err := mustKey32(btcMint)
		if err != nil {
			return nil, err
		}
		btcVaultAddr, err := LV1XStakingBTCVaultPDA(p.ProgramID, p.LedgerID)
		if err != nil {
			return nil, err
		}
		ataAddr, err := LV1XAssociatedTokenAddress(ownerB58, btcMint)
		if err != nil {
			return nil, err
		}
		btcVaultPK, _ := mustKey32(btcVaultAddr)
		ataPK, _ := mustKey32(ataAddr)
		if p.CreateATA {
			if err := b.appendCreateATAIdempotent(iOwner, ataPK, p.Owner, btcMintPK); err != nil {
				return nil, err
			}
		}
		iBtcVault := b.addKey(btcVaultPK, false, true)
		iATA := b.addKey(ataPK, false, true)
		iTok := b.addKey(tokPK, false, false)
		b.ixs = append(b.ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSt, iPos, iBtcVault, iATA, iOwner, iTok},
			data:     lv1xIxPrefix(LV1XTagClaimStakingReward, 0),
		})

	case "unstake":
		if p.Amount == 0 {
			return nil, errors.New("unstake amount must be non-zero")
		}
		vaultAddr, err := LV1XStakingVaultPDA(p.ProgramID, p.LedgerID)
		if err != nil {
			return nil, err
		}
		mintAddr, err := LV1XMintPDA(p.ProgramID, p.LedgerID)
		if err != nil {
			return nil, err
		}
		mintPK, _ := mustKey32(mintAddr)
		ataAddr, err := LV1XAssociatedTokenAddress(ownerB58, mintAddr)
		if err != nil {
			return nil, err
		}
		vaultPK, _ := mustKey32(vaultAddr)
		ataPK, _ := mustKey32(ataAddr)
		if p.CreateATA {
			if err := b.appendCreateATAIdempotent(iOwner, ataPK, p.Owner, mintPK); err != nil {
				return nil, err
			}
		}
		iVault := b.addKey(vaultPK, false, true)
		iATA := b.addKey(ataPK, false, true)
		iTok := b.addKey(tokPK, false, false)
		data := lv1xIxPrefix(LV1XTagUnstake, 8)
		data = append(data, u64LEBytes(p.Amount)...)
		b.ixs = append(b.ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSt, iPos, iVault, iATA, iOwner, iTok},
			data:     data,
		})

	default:
		return nil, fmt.Errorf("unsupported LV1X stake action %q", p.Action)
	}
	return encodeLegacyMessage(p.Owner, p.Blockhash, b.specs, b.ixs)
}

// BuildLV1XSessionMessage builds OpenSession (Tag 30), StopSession (Tag 32), or CloseSession (Tag 33).
func BuildLV1XSessionMessage(p LV1XSessionParams) ([]byte, error) {
	progPK, err := mustKey32(p.ProgramID)
	if err != nil {
		return nil, err
	}
	ownerB58 := Base58(p.Owner[:])
	rtAddr, err := LV1XRuntimePDA(p.ProgramID, p.LedgerID)
	if err != nil {
		return nil, err
	}
	skAddr, err := LV1XSessionPDA(p.ProgramID, p.LedgerID, ownerB58, p.Reference)
	if err != nil {
		return nil, err
	}
	rtPK, _ := mustKey32(rtAddr)
	skPK, _ := mustKey32(skAddr)
	sysPK, _ := mustKey32(System)

	var specs []legacyAccountSpec
	posMap := map[[32]byte]int{}
	addKey := func(k [32]byte, signer, writable bool) int {
		if idx, ok := posMap[k]; ok {
			if signer {
				specs[idx].isSigner = true
			}
			if writable {
				specs[idx].isWritable = true
			}
			return idx
		}
		posMap[k] = len(specs)
		specs = append(specs, legacyAccountSpec{key: k, isSigner: signer, isWritable: writable})
		return len(specs) - 1
	}

	iOwner := addKey(p.Owner, true, true)
	var ixs []legacyIxSpec
	appendComputeBudgetIxs(&ixs, addKey, p.ComputeCU, p.MicroLam)

	iRT := addKey(rtPK, false, false)
	iSK := addKey(skPK, false, true)
	_ = addKey(progPK, false, false)

	switch strings.ToLower(strings.TrimSpace(p.Action)) {
	case "open":
		iSys := addKey(sysPK, false, false)
		data := lv1xIxPrefix(LV1XTagOpenSession, 8+32+8+8+8+8+8+1+8)
		data = append(data, u64LEBytes(p.Reference)...)
		data = append(data, p.StrategyRoot[:]...)
		data = append(data, u64LEBytes(p.FirstSlot)...)
		data = append(data, u64LEBytes(p.LastSlot)...)
		data = append(data, i64LEBytes(p.ExpiresAt)...)
		data = append(data, u64LEBytes(p.MaxEntry)...)
		data = append(data, u64LEBytes(p.MaxTurnover)...)
		if p.ReturnToBudget {
			data = append(data, 1)
		} else {
			data = append(data, 0)
		}
		data = append(data, u64LEBytes(p.Deposit)...)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSK, iOwner, iSys},
			data:     data,
		})
	case "deposit":
		if p.Deposit == 0 {
			return nil, errors.New("deposit amount must be positive")
		}
		iSys := addKey(sysPK, false, false)
		data := lv1xIxPrefix(LV1XTagDepositSession, 8+8)
		data = append(data, u64LEBytes(p.Reference)...)
		data = append(data, u64LEBytes(p.Deposit)...)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSK, iOwner, iSys},
			data:     data,
		})
	case "stop":
		data := lv1xIxPrefix(LV1XTagStopSession, 8)
		data = append(data, u64LEBytes(p.Reference)...)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSK, iOwner},
			data:     data,
		})
	case "close":
		data := lv1xIxPrefix(LV1XTagCloseSession, 8)
		data = append(data, u64LEBytes(p.Reference)...)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSK, iOwner},
			data:     data,
		})
	default:
		return nil, fmt.Errorf("unsupported LV1X session action %q", p.Action)
	}
	return encodeLegacyMessage(p.Owner, p.Blockhash, specs, ixs)
}
