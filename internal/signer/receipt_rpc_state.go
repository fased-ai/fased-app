package signer

import (
	"encoding/json"
	"errors"
	"time"
)

type ReceiptRPCStatus struct {
	Ref         string    `json:"ref"`
	Origin      string    `json:"origin"`
	Network     string    `json:"network"`
	Saved       bool      `json:"saved"`
	Locked      bool      `json:"locked"`
	UnlockUntil time.Time `json:"unlockUntil,omitempty"`
}

const receiptRPCCredentialFeature = "managed-receipt-rpc-v1"

// ReceiptRPCBinding is public read-access identity, never a signing grant.
type ReceiptRPCBinding struct {
	Version int    `json:"version"`
	Ref     string `json:"ref"`
	Origin  string `json:"origin"`
}

type receiptRPCEnvelope struct {
	Version    int        `json:"version"`
	Purpose    string     `json:"purpose"`
	Ref        string     `json:"ref"`
	Account    AccountRef `json:"account"`
	Origin     string     `json:"origin"`
	Salt       []byte     `json:"salt"`
	Nonce      []byte     `json:"nonce"`
	Ciphertext []byte     `json:"ciphertext"`
}

func (e receiptRPCEnvelope) aad() []byte {
	e.Ciphertext = nil
	b, _ := json.Marshal(e)
	return append([]byte("fased-signer-receipt-rpc-v1:"), b...)
}
func receiptRPCOrigin(origin, network string) bool {
	prefix := "mainnet"
	if network == "devnet" {
		prefix = "devnet"
	} else if network != "mainnet-beta" {
		return false
	}
	return origin == "https://"+prefix+".helius-rpc.com" || origin == "https://solana-"+prefix+".g.alchemy.com"
}
func validReceiptRPCEnvelope(e receiptRPCEnvelope) bool {
	return e.Version == 1 && e.Purpose == "receipt-rpc" && validID(e.Ref) && managedRefValid(e.Account) && receiptRPCOrigin(e.Origin, e.Account.Network) && len(e.Salt) == 16 && len(e.Nonce) == 12 && len(e.Ciphertext) >= 40 && len(e.Ciphertext) <= 272
}
func (l *ledger) applyReceiptRPC(r journalRecord) error {
	expected := journalRecord{Version: r.Version, Sequence: r.Sequence, Previous: r.Previous, Hash: r.Hash, At: r.At, Kind: r.Kind, ReceiptRPC: r.ReceiptRPC}
	if recordHash(expected) != recordHash(r) || r.ReceiptRPC == nil || !validReceiptRPCEnvelope(*r.ReceiptRPC) || !sameWallet(l.ref, r.ReceiptRPC.Account) || r.ReceiptRPC.Account.SignerID != l.ref.SignerID {
		return errors.New("invalid receipt RPC credential history")
	}
	l.receiptRPC = clone(r.ReceiptRPC)
	return nil
}
