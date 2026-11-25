package onchain

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// ParsedWithdrawal summarizes a strictly validated owner withdrawal transaction message.
type ParsedWithdrawal struct {
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Mint        string `json:"mint,omitempty"`
	Amount      uint64 `json:"amount"`
	Decimals    uint8  `json:"decimals,omitempty"`
}

func validateWithdrawalFeeBudget(cuLimit uint32, cuPriceMicroLamports uint64) (uint32, error) {
	if cuLimit == 0 {
		cuLimit = 50_000
	}
	if cuLimit < 5_000 || cuLimit > 1_400_000 {
		return 0, errors.New("compute unit limit out of bounds")
	}
	if cuPriceMicroLamports > DevnetPriorityFeeCapMicroLamports {
		return 0, errors.New("priority fee exceeds withdrawal cap")
	}
	return cuLimit, nil
}

// BuildSolanaTransferMessage constructs a canonical legacy Solana message containing
// ComputeBudget instructions plus a single System::Transfer instruction.
func BuildSolanaTransferMessage(
	feePayer, destination, recentBlockhash [32]byte,
	lamports uint64,
	cuLimit uint32,
	cuPriceMicroLamports uint64,
) ([]byte, error) {
	var zero [32]byte
	if feePayer == zero || destination == zero || recentBlockhash == zero {
		return nil, errors.New("fee payer, destination, and recent blockhash must be non-zero")
	}
	if feePayer == destination {
		return nil, errors.New("withdrawal destination cannot equal signer wallet")
	}
	if lamports == 0 {
		return nil, errors.New("withdrawal lamports must be non-zero")
	}
	limit, err := validateWithdrawalFeeBudget(cuLimit, cuPriceMicroLamports)
	if err != nil {
		return nil, err
	}

	computeKey, err := Key(Compute)
	if err != nil {
		return nil, err
	}
	sysKey, err := Key(System)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(destination[:], computeKey) || bytes.Equal(destination[:], sysKey) {
		return nil, errors.New("withdrawal destination cannot target a program account")
	}

	cuData := make([]byte, 5)
	cuData[0] = 2
	binary.LittleEndian.PutUint32(cuData[1:], limit)

	priceData := make([]byte, 9)
	priceData[0] = 3
	binary.LittleEndian.PutUint64(priceData[1:], cuPriceMicroLamports)

	xferData := make([]byte, 12)
	binary.LittleEndian.PutUint32(xferData[0:4], 2)
	binary.LittleEndian.PutUint64(xferData[4:12], lamports)

	msg := make([]byte, 0, 256)
	// Header: 1 signer (writable), 0 readonly signed, 2 readonly unsigned (Compute, System)
	msg = append(msg, 1, 0, 2)
	msg = append(msg, short(4)...)
	msg = append(msg, feePayer[:]...)
	msg = append(msg, destination[:]...)
	msg = append(msg, computeKey...)
	msg = append(msg, sysKey...)
	msg = append(msg, recentBlockhash[:]...)

	msg = append(msg, short(3)...)
	// Ix 0: ComputeBudget SetComputeUnitLimit (prog index 2)
	msg = append(msg, 2, 0)
	msg = append(msg, short(len(cuData))...)
	msg = append(msg, cuData...)
	// Ix 1: ComputeBudget SetComputeUnitPrice (prog index 2)
	msg = append(msg, 2, 0)
	msg = append(msg, short(len(priceData))...)
	msg = append(msg, priceData...)
	// Ix 2: System::Transfer from 0 -> 1 (prog index 3)
	msg = append(msg, 3, 2, 0, 1)
	msg = append(msg, short(len(xferData))...)
	msg = append(msg, xferData...)

	if _, err := ValidateWithdrawalMessage(msg, feePayer); err != nil {
		return nil, err
	}
	return msg, nil
}

// BuildSPLTransferMessage constructs a canonical legacy Solana message containing
// ComputeBudget instructions, optional AssociatedToken::CreateIdempotent, and Token::TransferChecked.
func BuildSPLTransferMessage(
	feePayer, destOwner, mint, recentBlockhash [32]byte,
	amount uint64,
	decimals uint8,
	createDestATA bool,
	cuLimit uint32,
	cuPriceMicroLamports uint64,
) ([]byte, error) {
	var zero [32]byte
	if feePayer == zero || destOwner == zero || mint == zero || recentBlockhash == zero {
		return nil, errors.New("fee payer, destination owner, mint, and blockhash must be non-zero")
	}
	if feePayer == destOwner {
		return nil, errors.New("withdrawal destination owner cannot equal signer wallet")
	}
	if amount == 0 {
		return nil, errors.New("SPL withdrawal amount must be non-zero")
	}
	if decimals > 18 {
		return nil, errors.New("SPL token decimals exceed supported bound")
	}
	limit, err := validateWithdrawalFeeBudget(cuLimit, cuPriceMicroLamports)
	if err != nil {
		return nil, err
	}

	feePayerB58 := Base58(feePayer[:])
	destOwnerB58 := Base58(destOwner[:])
	mintB58 := Base58(mint[:])

	srcATAB58, err := TokenAccount(feePayerB58, mintB58)
	if err != nil {
		return nil, err
	}
	dstATAB58, err := TokenAccount(destOwnerB58, mintB58)
	if err != nil {
		return nil, err
	}
	srcATA, err := Key(srcATAB58)
	if err != nil {
		return nil, err
	}
	dstATA, err := Key(dstATAB58)
	if err != nil {
		return nil, err
	}
	computeKey, err := Key(Compute)
	if err != nil {
		return nil, err
	}
	sysKey, err := Key(System)
	if err != nil {
		return nil, err
	}
	tokenKey, err := Key(Token)
	if err != nil {
		return nil, err
	}
	ataProgKey, err := Key(AssociatedToken)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(destOwner[:], computeKey) || bytes.Equal(destOwner[:], sysKey) || bytes.Equal(destOwner[:], tokenKey) || bytes.Equal(destOwner[:], ataProgKey) {
		return nil, errors.New("SPL withdrawal destination owner cannot target a program account")
	}

	cuData := make([]byte, 5)
	cuData[0] = 2
	binary.LittleEndian.PutUint32(cuData[1:], limit)

	priceData := make([]byte, 9)
	priceData[0] = 3
	binary.LittleEndian.PutUint64(priceData[1:], cuPriceMicroLamports)

	xferData := make([]byte, 10)
	xferData[0] = 12 // Token::TransferChecked
	binary.LittleEndian.PutUint64(xferData[1:9], amount)
	xferData[9] = decimals

	msg := make([]byte, 0, 384)
	if createDestATA {
		// 0: feePayer (W,S), 1: srcATA (W), 2: dstATA (W),
		// 3: destOwner (R), 4: mint (R), 5: Compute (R), 6: System (R), 7: Token (R), 8: AssociatedToken (R)
		msg = append(msg, 1, 0, 6)
		msg = append(msg, short(9)...)
		msg = append(msg, feePayer[:]...)
		msg = append(msg, srcATA...)
		msg = append(msg, dstATA...)
		msg = append(msg, destOwner[:]...)
		msg = append(msg, mint[:]...)
		msg = append(msg, computeKey...)
		msg = append(msg, sysKey...)
		msg = append(msg, tokenKey...)
		msg = append(msg, ataProgKey...)
		msg = append(msg, recentBlockhash[:]...)

		msg = append(msg, short(4)...)
		// ComputeLimit (prog 5)
		msg = append(msg, 5, 0)
		msg = append(msg, short(len(cuData))...)
		msg = append(msg, cuData...)
		// ComputePrice (prog 5)
		msg = append(msg, 5, 0)
		msg = append(msg, short(len(priceData))...)
		msg = append(msg, priceData...)
		// AssociatedToken::CreateIdempotent (prog 8): payer=0, ata=2, owner=3, mint=4, system=6, token=7
		msg = append(msg, 8, 6, 0, 2, 3, 4, 6, 7)
		msg = append(msg, short(1)...)
		msg = append(msg, 1)
		// Token::TransferChecked (prog 7): source=1, mint=4, dest=2, owner=0
		msg = append(msg, 7, 4, 1, 4, 2, 0)
		msg = append(msg, short(len(xferData))...)
		msg = append(msg, xferData...)
	} else {
		// 0: feePayer (W,S), 1: srcATA (W), 2: dstATA (W),
		// 3: mint (R), 4: Compute (R), 5: Token (R)
		msg = append(msg, 1, 0, 3)
		msg = append(msg, short(6)...)
		msg = append(msg, feePayer[:]...)
		msg = append(msg, srcATA...)
		msg = append(msg, dstATA...)
		msg = append(msg, mint[:]...)
		msg = append(msg, computeKey...)
		msg = append(msg, tokenKey...)
		msg = append(msg, recentBlockhash[:]...)

		msg = append(msg, short(3)...)
		// ComputeLimit (prog 4)
		msg = append(msg, 4, 0)
		msg = append(msg, short(len(cuData))...)
		msg = append(msg, cuData...)
		// ComputePrice (prog 4)
		msg = append(msg, 4, 0)
		msg = append(msg, short(len(priceData))...)
		msg = append(msg, priceData...)
		// Token::TransferChecked (prog 5): source=1, mint=3, dest=2, owner=0
		msg = append(msg, 5, 4, 1, 3, 2, 0)
		msg = append(msg, short(len(xferData))...)
		msg = append(msg, xferData...)
	}

	if _, err := ValidateWithdrawalMessage(msg, feePayer); err != nil {
		return nil, err
	}
	return msg, nil
}

// ValidateWithdrawalMessage strictly verifies a legacy withdrawal message contains only
// ComputeBudget, AssociatedToken CreateIdempotent, and a single SOL or SPL TransferChecked.
func ValidateWithdrawalMessage(messageBytes []byte, expectedFeePayer [32]byte) (*ParsedWithdrawal, error) {
	if len(messageBytes) < 3+1+64+32+1 || len(messageBytes)+65 > 1232 {
		return nil, errors.New("withdrawal message length out of bounds")
	}
	if (messageBytes[0] & 0x80) != 0 {
		return nil, errors.New("withdrawal message must be legacy transaction format")
	}
	numReq := int(messageBytes[0])
	numROSigned := int(messageBytes[1])
	numROUnsigned := int(messageBytes[2])
	if numReq != 1 || numROSigned != 0 {
		return nil, errors.New("withdrawal message must require exactly one writable fee-payer signer")
	}
	pos := 3
	accountCount, n, err := decodeShort(messageBytes[pos:])
	if err != nil {
		return nil, err
	}
	pos += n
	if accountCount < 2 || accountCount > 16 || numROUnsigned > accountCount-numReq {
		return nil, errors.New("withdrawal account header out of bounds")
	}
	if pos+accountCount*32+32+1 > len(messageBytes) {
		return nil, errors.New("truncated withdrawal accounts or blockhash")
	}
	accounts := make([][32]byte, accountCount)
	for i := 0; i < accountCount; i++ {
		copy(accounts[i][:], messageBytes[pos:pos+32])
		pos += 32
	}
	if accounts[0] != expectedFeePayer {
		return nil, errors.New("withdrawal message fee payer mismatch")
	}
	var zeroHash [32]byte
	if bytes.Equal(messageBytes[pos:pos+32], zeroHash[:]) {
		return nil, errors.New("non-zero recent blockhash required")
	}
	pos += 32

	ixCount, n, err := decodeShort(messageBytes[pos:])
	if err != nil {
		return nil, err
	}
	pos += n
	if ixCount < 1 || ixCount > 6 {
		return nil, errors.New("withdrawal instruction count out of bounds")
	}

	var parsed *ParsedWithdrawal
	for i := 0; i < ixCount; i++ {
		if pos >= len(messageBytes) {
			return nil, errors.New("truncated withdrawal instruction")
		}
		progIdx := int(messageBytes[pos])
		pos++
		if progIdx < numReq || progIdx >= accountCount {
			return nil, errors.New("withdrawal instruction program index invalid")
		}
		progID := Base58(accounts[progIdx][:])
		acctIdxCount, n, err := decodeShort(messageBytes[pos:])
		if err != nil {
			return nil, err
		}
		pos += n
		if acctIdxCount > 12 || pos+acctIdxCount > len(messageBytes) {
			return nil, errors.New("truncated withdrawal instruction accounts")
		}
		acctIdxs := make([]int, acctIdxCount)
		for j := 0; j < acctIdxCount; j++ {
			idx := int(messageBytes[pos+j])
			if idx >= accountCount {
				return nil, errors.New("withdrawal instruction account index out of range")
			}
			acctIdxs[j] = idx
		}
		pos += acctIdxCount
		dataLen, n, err := decodeShort(messageBytes[pos:])
		if err != nil {
			return nil, err
		}
		pos += n
		if dataLen > 64 || pos+dataLen > len(messageBytes) {
			return nil, errors.New("truncated withdrawal instruction data")
		}
		data := messageBytes[pos : pos+dataLen]
		pos += dataLen

		switch progID {
		case Compute:
			if len(acctIdxs) != 0 || (len(data) != 5 && len(data) != 9) {
				return nil, errors.New("unsupported ComputeBudget instruction in withdrawal")
			}
		case AssociatedToken:
			if len(data) != 1 || data[0] != 1 || len(acctIdxs) != 6 || acctIdxs[0] != 0 {
				return nil, errors.New("only AssociatedToken CreateIdempotent allowed in withdrawal")
			}
		case System:
			if len(data) != 12 || binary.LittleEndian.Uint32(data[0:4]) != 2 || len(acctIdxs) != 2 || acctIdxs[0] != 0 || acctIdxs[1] == 0 {
				return nil, errors.New("only System::Transfer allowed in withdrawal")
			}
			lamports := binary.LittleEndian.Uint64(data[4:12])
			if lamports == 0 {
				return nil, errors.New("withdrawal lamports must be non-zero")
			}
			if parsed != nil {
				return nil, errors.New("multiple transfer instructions denied in withdrawal")
			}
			parsed = &ParsedWithdrawal{
				Kind:        "sol",
				Destination: Base58(accounts[acctIdxs[1]][:]),
				Amount:      lamports,
			}
		case Token:
			if len(data) != 10 || data[0] != 12 || len(acctIdxs) != 4 || acctIdxs[3] != 0 {
				return nil, errors.New("only SPL Token TransferChecked allowed in withdrawal")
			}
			amount := binary.LittleEndian.Uint64(data[1:9])
			decimals := data[9]
			if amount == 0 || decimals > 18 {
				return nil, errors.New("invalid SPL withdrawal amount or decimals")
			}
			if parsed != nil {
				return nil, errors.New("multiple transfer instructions denied in withdrawal")
			}
			parsed = &ParsedWithdrawal{
				Kind:        "spl",
				Destination: Base58(accounts[acctIdxs[2]][:]),
				Mint:        Base58(accounts[acctIdxs[1]][:]),
				Amount:      amount,
				Decimals:    decimals,
			}
		default:
			return nil, errors.New("program not permitted in withdrawal message")
		}
	}
	if pos != len(messageBytes) {
		return nil, errors.New("unexpected trailing bytes in withdrawal message")
	}
	if parsed == nil {
		return nil, errors.New("withdrawal message missing transfer instruction")
	}
	return parsed, nil
}
