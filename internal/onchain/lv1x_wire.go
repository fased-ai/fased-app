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
	// Canonical Lean V1 (LV1X) protocol constants matching wen/vendor/satcoin-mining-v2/lean-v1-pda.mjs.
	LV1XMagic              = "LV1X"
	LV1XVersion            = 1
	LV1XMinCommitLamports  = uint64(10_000_000)
	LV1XRoundIntervalSec   = int64(600)
	LV1XMaxShards          = 32
	LV1XUnshardedShardID   = uint8(255)
	LV1XPageEntries        = 8
	BPFLoaderUpgradeableID = "BPFLoaderUpgradeab1e11111111111111111111111"

	// Pinned Devnet Lean V1 deployment (wen-lean-v1-devnet-manifest/1).
	LV1XDevnetProgramID        = "WAcbznz9veHLEU4qup2eqvwQcGzLeV7hYvAtfaQTrVY"
	LV1XDevnetProgramDataID    = "6YHn2R8rauQs5JT1ACd6oyrZ5PsJ2SKEP6oZLdvx1S7z"
	LV1XDevnetLedgerID         = "DXYmEs4veeHR5sFXMdnsfmrjL3gEuGhuawcQwxgb5V46"
	LV1XDevnetBTCMint          = "2bBh5wMSYniLm8jpH9JtR4sxw4BCx7Q2q2AkX2FmGsd9"
	LV1XDevnetELFSHA256        = "635a46e23c0524ee9189b4c9eb69b7e3ca8bb2b29f1ae522128bf4381d7c8d1f"
	LV1XDevnetVendorELFSHA256  = "c438d5cbf43a053697af2ce7270719a4be22f6a47b53e651457789046ecc9069"
	LV1XMainnetVendorELFSHA256 = "9227e38f35a795b1f4b151251757a358cd80197a17a72db0a5f0061f1fa5389f"

	// Mainnet remains intentionally empty until official Mainnet LV1X program IDs are published.
	LV1XMainnetProgramID = ""
)

// Canonical LEAN_IX_TAGS matching wen/vendor/satcoin-mining-v2/lean-v1-pda.mjs.
const (
	LV1XTagInitialize               uint8 = 0
	LV1XTagCommit                   uint8 = 5
	LV1XTagCommitFromSession        uint8 = 6
	LV1XTagClaimSolBatch            uint8 = 13
	LV1XTagClaimSat                 uint8 = 14
	LV1XTagClaimBtc                 uint8 = 16
	LV1XTagOpenSession              uint8 = 30
	LV1XTagDepositSession           uint8 = 31
	LV1XTagStopSession              uint8 = 32
	LV1XTagCloseSession             uint8 = 33
	LV1XTagCommitToShard            uint8 = 35
	LV1XTagCommitFromSessionToShard uint8 = 36
	LV1XTagClaimShardSolBatch       uint8 = 42
	LV1XTagClaimShardSat            uint8 = 43
	LV1XTagClaimShardBtc            uint8 = 44
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

// BPFLoaderProgramDataPDA derives the Upgradeable BPF Loader ProgramData address for programID.
func BPFLoaderProgramDataPDA(programID string) (string, error) {
	pk, err := Key(programID)
	if err != nil {
		return "", err
	}
	addr, _, err := FindProgramAddress(BPFLoaderUpgradeableID, pk)
	return addr, err
}

// VerifyBPFLoaderProgramData validates BPF Loader Upgradeable account state and deployed ELF SHA-256
// matching wen/vendor/satcoin-mining-v2/program.mjs (data[45:] holds the ELF bytes).
func VerifyBPFLoaderProgramData(programID string, progAcctOwner, progAcctData, progDataAcctOwner, progDataAcctData []byte, expectedSHA256 string) error {
	loaderKey, _ := Key(BPFLoaderUpgradeableID)
	expectedDataAddr, err := BPFLoaderProgramDataPDA(programID)
	if err != nil {
		return err
	}
	expectedDataKey, _ := Key(expectedDataAddr)
	if !bytes.Equal(progAcctOwner, loaderKey) || !bytes.Equal(progDataAcctOwner, loaderKey) {
		return errors.New("BPF loader owner mismatch")
	}
	if len(progAcctData) != 36 || binary.LittleEndian.Uint32(progAcctData[:4]) != 2 || !bytes.Equal(progAcctData[4:36], expectedDataKey) {
		return errors.New("BPF loader program account pointer mismatch")
	}
	if len(progDataAcctData) <= 45 || len(progDataAcctData) > 10*1024*1024 || binary.LittleEndian.Uint32(progDataAcctData[:4]) != 3 {
		return errors.New("BPF loader programdata account invalid")
	}
	sum := sha256.Sum256(progDataAcctData[45:])
	got := hex.EncodeToString(sum[:])
	want := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if want == "" || got != want {
		return fmt.Errorf("BPF ELF SHA-256 mismatch: got %s want %s", got, want)
	}
	return nil
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

		data := make([]byte, 1+8+8+8+32)
		data[0] = LV1XTagCommit
		binary.LittleEndian.PutUint64(data[1:9], p.Slot)
		binary.LittleEndian.PutUint64(data[9:17], p.Amount)
		binary.LittleEndian.PutUint64(data[17:25], p.Nonce)
		copy(data[25:57], p.Commitment[:])

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

		data := make([]byte, 1+8+1+8+8+32)
		data[0] = LV1XTagCommitToShard
		binary.LittleEndian.PutUint64(data[1:9], p.Slot)
		data[9] = p.ShardID
		binary.LittleEndian.PutUint64(data[10:18], p.Amount)
		binary.LittleEndian.PutUint64(data[18:26], p.Nonce)
		copy(data[26:58], p.Commitment[:])

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
		data := make([]byte, 1+8+4+1)
		data[0] = LV1XTagClaimSolBatch
		binary.LittleEndian.PutUint64(data[1:9], p.Slot)
		binary.LittleEndian.PutUint32(data[9:13], p.Page)
		data[13] = p.EntriesMask
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iRK, iPK, iDest},
			data:     data,
		})
	} else {
		skAddr, err := LV1XShardPDA(p.ProgramID, rkAddr, p.ShardID)
		if err != nil {
			return nil, err
		}
		pkAddr, err := LV1XShardPagePDA(p.ProgramID, rkAddr, p.ShardID, p.Page)
		if err != nil {
			return nil, err
		}
		skPK, _ := mustKey32(skAddr)
		pkPK, _ := mustKey32(pkAddr)
		iSK := addKey(skPK, false, true)
		iPK := addKey(pkPK, false, true)
		data := make([]byte, 1+8+1+4+1)
		data[0] = LV1XTagClaimShardSolBatch
		binary.LittleEndian.PutUint64(data[1:9], p.Slot)
		data[9] = p.ShardID
		binary.LittleEndian.PutUint32(data[10:14], p.Page)
		data[14] = p.EntriesMask
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iRK, iSK, iPK, iDest},
			data:     data,
		})
	}
	return encodeLegacyMessage(p.FeePayer, p.Blockhash, specs, ixs)
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
		data := make([]byte, 1+8+32+8+8+8+8+8+1+8)
		off := 0
		data[off] = LV1XTagOpenSession
		off++
		binary.LittleEndian.PutUint64(data[off:off+8], p.Reference)
		off += 8
		copy(data[off:off+32], p.StrategyRoot[:])
		off += 32
		binary.LittleEndian.PutUint64(data[off:off+8], p.FirstSlot)
		off += 8
		binary.LittleEndian.PutUint64(data[off:off+8], p.LastSlot)
		off += 8
		binary.LittleEndian.PutUint64(data[off:off+8], uint64(p.ExpiresAt))
		off += 8
		binary.LittleEndian.PutUint64(data[off:off+8], p.MaxEntry)
		off += 8
		binary.LittleEndian.PutUint64(data[off:off+8], p.MaxTurnover)
		off += 8
		if p.ReturnToBudget {
			data[off] = 1
		}
		off++
		binary.LittleEndian.PutUint64(data[off:off+8], p.Deposit)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSK, iOwner, iSys},
			data:     data,
		})
	case "stop":
		data := make([]byte, 9)
		data[0] = LV1XTagStopSession
		binary.LittleEndian.PutUint64(data[1:9], p.Reference)
		ixs = append(ixs, legacyIxSpec{
			program:  progPK,
			accounts: []int{iRT, iSK, iOwner},
			data:     data,
		})
	case "close":
		data := make([]byte, 9)
		data[0] = LV1XTagCloseSession
		binary.LittleEndian.PutUint64(data[1:9], p.Reference)
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
