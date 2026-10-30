// Package onchain contains the narrow Solana swap contract shared by core and
// signer. It owns no keys, agent permissions or automatic retry policy.
package onchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
)

const (
	Version         = "jupiter-build-swap-v1"
	SOL             = "So11111111111111111111111111111111111111112"
	USDC            = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
	Jupiter         = "JUP6LkbZbjS1jKKwapdHNy74zcZ3tLUZoi5QNyVTaV4"
	Token           = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	AssociatedToken = "ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL"
	System          = "11111111111111111111111111111111"
	Compute         = "ComputeBudget111111111111111111111111111111"
	EventAuthority  = "D8cy77BBepLMngZx6ZukaTff5hCt1HrWyKk3Hnd9oitf"
	MaxDocument     = 32 * 1024
)

type Meta struct {
	Pubkey     string `json:"pubkey"`
	IsSigner   bool   `json:"isSigner"`
	IsWritable bool   `json:"isWritable"`
}
type Instruction struct {
	ProgramID string `json:"programId"`
	Accounts  []Meta `json:"accounts"`
	Data      string `json:"data"`
}
type Request struct {
	AccountID      string `json:"accountId"`
	InputMint      string `json:"inputMint"`
	OutputMint     string `json:"outputMint"`
	Amount         string `json:"amount"`
	SlippageBPS    uint16 `json:"slippageBps"`
	MaxFeeLamports uint64 `json:"maxFeeLamports,string"`
}
type Quote struct {
	Version           string        `json:"version"`
	ID                string        `json:"id"`
	ProfileID         string        `json:"profileId"`
	InstanceID        string        `json:"instanceId"`
	AccountID         string        `json:"accountId"`
	Network           string        `json:"network"`
	Owner             string        `json:"owner"`
	RPCSHA256         string        `json:"rpcSha256"`
	InputMint         string        `json:"inputMint"`
	OutputMint        string        `json:"outputMint"`
	Amount            string        `json:"amount"`
	Output            string        `json:"output"`
	MinimumOutput     string        `json:"minimumOutput"`
	SlippageBPS       uint16        `json:"slippageBps"`
	MaxFeeLamports    uint64        `json:"maxFeeLamports,string"`
	FeeLamports       uint64        `json:"feeLamports,string"`
	RentLamports      uint64        `json:"rentLamports,string"`
	Blockhash         string        `json:"blockhash"`
	LastValidHeight   uint64        `json:"lastValidHeight,string"`
	ObservedSlot      uint64        `json:"observedSlot,string"`
	CreatedAt         int64         `json:"createdAt"`
	ExpiresAt         int64         `json:"expiresAt"`
	Instructions      []Instruction `json:"instructions"`
	TransactionSHA256 string        `json:"transactionSha256"`
	Digest            string        `json:"digest"`
}
type Simulation struct {
	Slot  uint64 `json:"slot,string"`
	Units uint64 `json:"units,string"`
	Fee   uint64 `json:"fee,string"`
}
type Observation struct {
	State  string `json:"state"`
	Slot   uint64 `json:"slot,string"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
}

func Hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func QuoteDigest(q Quote) string { q.Digest = ""; return Hash(q) }
func Uint(s string) (uint64, error) {
	n, e := strconv.ParseUint(s, 10, 64)
	if e != nil || n == 0 || strconv.FormatUint(n, 10) != s || n > 1_000_000_000_000_000 {
		return 0, errors.New("bounded canonical amount required")
	}
	return n, nil
}
func Minimum(out string, bps uint16) (string, error) {
	n, e := Uint(out)
	if e != nil || bps > 100 {
		return "", errors.New("slippage exceeds one percent")
	}
	v := new(big.Int).SetUint64(n)
	v.Mul(v, big.NewInt(int64(10000-bps)))
	v.Add(v, big.NewInt(9999))
	v.Div(v, big.NewInt(10000))
	return v.String(), nil
}
func ValidRequest(r Request) bool {
	_, e := Uint(r.Amount)
	return e == nil && r.AccountID != "" && len(r.AccountID) <= 128 && ((r.InputMint == SOL && r.OutputMint == USDC) || (r.InputMint == USDC && r.OutputMint == SOL)) && r.SlippageBPS <= 100 && r.MaxFeeLamports >= 5000 && r.MaxFeeLamports <= 1_000_000
}
func ValidateQuote(q Quote, now int64) error {
	if q.Version != Version || len(q.ID) != 32 || len(q.ProfileID) == 0 || len(q.ProfileID) > 80 || len(q.InstanceID) < 32 || len(q.InstanceID) > 80 || !ValidRequest(Request{q.AccountID, q.InputMint, q.OutputMint, q.Amount, q.SlippageBPS, q.MaxFeeLamports}) || (q.Network != "mainnet-beta" && q.Network != "devnet") || len(q.RPCSHA256) != 64 || q.CreatedAt <= 0 || q.ExpiresAt <= q.CreatedAt || q.ExpiresAt-q.CreatedAt > 60 || (now > 0 && (now < q.CreatedAt || now >= q.ExpiresAt)) || q.LastValidHeight == 0 || q.FeeLamports < 5000 || q.FeeLamports > q.MaxFeeLamports || q.RentLamports > 6_000_000 || len(q.TransactionSHA256) != 64 || q.Digest != QuoteDigest(q) {
		return errors.New("swap review changed or expired")
	}
	min, e := Minimum(q.Output, q.SlippageBPS)
	if e != nil || q.MinimumOutput != min {
		return errors.New("minimum output differs from quote")
	}
	if _, e = Key(q.Owner); e != nil {
		return e
	}
	if _, e = Key(q.Blockhash); e != nil {
		return e
	}
	message, e := Message(q)
	if e != nil {
		return e
	}
	h := sha256.Sum256(message)
	if hex.EncodeToString(h[:]) != q.TransactionSHA256 {
		return errors.New("swap transaction changed")
	}
	return nil
}
