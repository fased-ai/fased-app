package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/fased-ai/fased-app/internal/signer"
)

func newPortableAccountID() (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	return hex.EncodeToString(id), nil
}

// PortableWalletEntry holds public network wallet and canonical RPC metadata
// with zero private credentials, signer authorities, or instance IDs.
type PortableWalletEntry struct {
	Name        string `json:"name"`
	Network     string `json:"network"`
	Address     string `json:"address"`
	RPCEndpoint string `json:"rpcEndpoint,omitempty"`
}

type PortableWalletsBundle struct {
	Version    int                   `json:"version"`
	ExportedAt int64                 `json:"exportedAt"`
	Wallets    []PortableWalletEntry `json:"wallets"`
}

type PortableWalletsImportCommand struct {
	Revision int64                 `json:"revision"`
	Bundle   PortableWalletsBundle `json:"bundle"`
}

type PortableWalletsImportResult struct {
	AccountsView
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

func (s *Store) ExportPortableWallets(ctx context.Context) (PortableWalletsBundle, error) {
	r, err := s.accountsRecord(ctx)
	if err != nil {
		return PortableWalletsBundle{}, err
	}
	bundle := PortableWalletsBundle{
		Version:    1,
		ExportedAt: s.now().Unix(),
		Wallets:    []PortableWalletEntry{},
	}
	for _, a := range r.Accounts {
		if solanaGenesis(a.Network) == "" {
			continue
		}
		address, err := signer.FormatPublicKey(a.PublicKey)
		if err != nil {
			continue
		}
		entry := PortableWalletEntry{
			Name:    a.Name,
			Network: a.Network,
			Address: address,
		}
		if a.RPC != nil && a.RPC.Version == 2 && a.RPC.Network == a.Network && validProviderRPC(a) {
			entry.RPCEndpoint = a.RPC.Endpoint
		}
		bundle.Wallets = append(bundle.Wallets, entry)
	}
	return bundle, nil
}

func (s *Store) ImportPortableWallets(ctx context.Context, cmd PortableWalletsImportCommand) (PortableWalletsImportResult, error) {
	if !s.rpcAdmission {
		return PortableWalletsImportResult{}, errors.New("network account setup is disabled in this build")
	}
	if cmd.Bundle.Version != 1 || len(cmd.Bundle.Wallets) == 0 || len(cmd.Bundle.Wallets) > 8 {
		return PortableWalletsImportResult{}, errors.New("invalid portable wallet bundle")
	}
	r, err := s.accountsRecord(ctx)
	if err != nil {
		return PortableWalletsImportResult{}, err
	}
	if cmd.Revision != r.Revision {
		return PortableWalletsImportResult{}, ErrConflict
	}
	existingWatch := map[string]bool{}
	for _, old := range r.Accounts {
		if old.Mode == "watch" {
			existingWatch[old.Network+":"+old.PublicKey] = true
		}
	}
	imported := 0
	skipped := 0
	for _, entry := range cmd.Bundle.Wallets {
		name := strings.TrimSpace(entry.Name)
		if !validName(name) || solanaGenesis(entry.Network) == "" {
			return PortableWalletsImportResult{}, errors.New("invalid wallet entry")
		}
		pubHex, err := signer.ParsePublicAddress(strings.TrimSpace(entry.Address))
		if err != nil {
			return PortableWalletsImportResult{}, err
		}
		dedupKey := entry.Network + ":" + pubHex
		if existingWatch[dedupKey] {
			skipped++
			continue
		}
		if len(r.Accounts) >= 8 {
			return PortableWalletsImportResult{}, errors.New("wallet limit reached")
		}
		idHex, err := newPortableAccountID()
		if err != nil {
			return PortableWalletsImportResult{}, err
		}
		acct := Account{
			Version:   2,
			ID:        idHex,
			Name:      name,
			Mode:      "watch",
			Network:   entry.Network,
			PublicKey: pubHex,
		}
		if ep := strings.TrimSpace(entry.RPCEndpoint); ep != "" {
			acct.RPC = &AccountRPC{
				Version:  2,
				Endpoint: ep,
				Network:  entry.Network,
			}
			if !validProviderRPC(acct) {
				return PortableWalletsImportResult{}, errors.New("unsupported wallet RPC endpoint")
			}
		}
		r.Accounts = append(r.Accounts, acct)
		existingWatch[dedupKey] = true
		imported++
	}
	if imported == 0 {
		view, err := s.Accounts(ctx)
		if err != nil {
			return PortableWalletsImportResult{}, err
		}
		return PortableWalletsImportResult{AccountsView: view, Imported: 0, Skipped: skipped}, nil
	}
	view, err := s.updateAccounts(ctx, r, true)
	if err != nil {
		return PortableWalletsImportResult{}, err
	}
	return PortableWalletsImportResult{AccountsView: view, Imported: imported, Skipped: skipped}, nil
}
