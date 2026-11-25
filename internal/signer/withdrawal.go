package signer

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/fased-ai/fased-app/internal/onchain"
)

// SolanaWithdrawIntent describes an owner-only SOL or SPL withdrawal request whose
// transaction message is constructed and signed strictly inside fased-signer.
type SolanaWithdrawIntent struct {
	Network                  string `json:"network"`
	GenesisHash              string `json:"genesisHash"`
	Destination              string `json:"destination"`
	Mint                     string `json:"mint,omitempty"`
	Amount                   uint64 `json:"amount"`
	Decimals                 uint8  `json:"decimals,omitempty"`
	CreateDestATA            bool   `json:"createDestAta,omitempty"`
	RecentBlockhash          string `json:"recentBlockhash"`
	ComputeUnits             uint32 `json:"computeUnits,omitempty"`
	PriorityFeeMicroLamports uint64 `json:"priorityFeeMicroLamports,omitempty"`
	ExpiresAt                int64  `json:"expiresAt"`
}

// SignSolanaWithdrawal constructs and signs an owner-authorized SOL or SPL Token
// withdrawal message using the vault's Ed25519 private key without exposing seeds.
func (v *Vault) SignSolanaWithdrawal(intent SolanaWithdrawIntent) (*SolanaDevnetSignedTx, error) {
	if v == nil {
		return nil, ErrUnavailable
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.ledger == nil {
		return nil, ErrUnavailable
	}
	if !v.unlocked() {
		return nil, ErrLocked
	}
	var expectedGenesis string
	switch intent.Network {
	case "devnet":
		expectedGenesis = onchain.SolanaDevnetGenesisHash
	case "mainnet-beta":
		expectedGenesis = onchain.SolanaMainnetGenesisHash
	default:
		return nil, errors.New("solana withdrawal network must be devnet or mainnet-beta")
	}
	if v.network != "" && v.network != intent.Network {
		return nil, errors.New("solana withdrawal network mismatches signer vault network")
	}
	if intent.GenesisHash != expectedGenesis {
		return nil, errors.New("solana withdrawal genesis hash mismatch")
	}
	if intent.ExpiresAt <= v.now().Unix() {
		return nil, errors.New("solana withdrawal intent expired")
	}

	destRaw, err := onchain.Key(strings.TrimSpace(intent.Destination))
	if err != nil {
		return nil, errors.New("invalid withdrawal destination address")
	}
	bhRaw, err := onchain.Key(strings.TrimSpace(intent.RecentBlockhash))
	if err != nil {
		return nil, errors.New("invalid recent blockhash")
	}
	var destKey, recentBH [32]byte
	copy(destKey[:], destRaw)
	copy(recentBH[:], bhRaw)

	priv := ed25519.NewKeyFromSeed(v.seed)
	defer wipe(priv)
	var feePayer [32]byte
	copy(feePayer[:], priv[ed25519.SeedSize:])

	var msgBytes []byte
	mintStr := strings.TrimSpace(intent.Mint)
	if mintStr == "" {
		msgBytes, err = onchain.BuildSolanaTransferMessage(
			feePayer,
			destKey,
			recentBH,
			intent.Amount,
			intent.ComputeUnits,
			intent.PriorityFeeMicroLamports,
		)
	} else {
		mintRaw, mErr := onchain.Key(mintStr)
		if mErr != nil {
			return nil, errors.New("invalid SPL token mint address")
		}
		var mintKey [32]byte
		copy(mintKey[:], mintRaw)
		msgBytes, err = onchain.BuildSPLTransferMessage(
			feePayer,
			destKey,
			mintKey,
			recentBH,
			intent.Amount,
			intent.Decimals,
			intent.CreateDestATA,
			intent.ComputeUnits,
			intent.PriorityFeeMicroLamports,
		)
	}
	if err != nil {
		return nil, err
	}
	if _, err := onchain.ValidateWithdrawalMessage(msgBytes, feePayer); err != nil {
		return nil, err
	}

	rawSig := ed25519.Sign(priv, msgBytes)
	var sigArr [64]byte
	copy(sigArr[:], rawSig)
	wipe(rawSig)
	signedTx, sigB58 := onchain.AssembleSignedLegacyTransaction(sigArr, msgBytes)
	msgSum := sha256.Sum256(msgBytes)
	return &SolanaDevnetSignedTx{
		Signature:       sigB58,
		SignedTxBase64:  base64.StdEncoding.EncodeToString(signedTx),
		MessageSHA256:   hex.EncodeToString(msgSum[:]),
		PublicKeyBase58: base58PublicKey(feePayer[:]),
		GenesisHash:     expectedGenesis,
	}, nil
}
