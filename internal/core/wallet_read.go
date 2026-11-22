package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/update"
)

// WalletReadResponseLimit bounds the same wallet projection in native and CLI
// clients: balances (up to 1024 token accounts per program), activity and tx detail.
const WalletReadResponseLimit int64 = 2 * 1024 * 1024

type WalletReadCommand struct {
	Selection  *WalletSelection `json:"selection,omitempty"`
	AccountID  string           `json:"accountId"`
	Revision   int64            `json:"revision"`
	Ref        string           `json:"ref"`
	Passphrase string           `json:"passphrase"`
}
type walletReader func(context.Context, Account, string, []byte, func() error, *WalletSelection) (WalletSnapshot, error)

func (s *Store) ReadWallet(ctx context.Context, in WalletReadCommand) (WalletSnapshot, error) {
	return s.readWallet(ctx, in, nil)
}
func (s *Store) readWallet(ctx context.Context, in WalletReadCommand, authorized func() bool) (WalletSnapshot, error) {
	var empty WalletSnapshot
	if !s.walletReadMu.TryLock() {
		return empty, errors.New("Wallet busy")
	}
	defer s.walletReadMu.Unlock()
	if !hexValue(in.AccountID, 16) || in.Revision < 0 || (in.Selection != nil && !validWalletSelection(in.Selection)) || (in.Passphrase != "" && !validPassword(in.Passphrase)) {
		return empty, errors.New("Wallet unavailable")
	}
	if s.walletReadFixture == nil && !s.rpcAdmission {
		return empty, errors.New("Wallet reads disabled")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pauseBinding := ""
	lookup := func() (Account, error) {
		if ctx.Err() != nil || (authorized != nil && !authorized()) || (in.Selection != nil && time.Now().Unix() >= in.Selection.ExpiresAt) {
			return Account{}, errors.New("Wallet unavailable")
		}
		phase, pause, err := readPause(ctx, s.db)
		if err != nil {
			return Account{}, errors.New("Wallet unavailable")
		}
		if phase == "paused" {
			return Account{}, ErrCorePaused
		}
		digest := update.SnapshotDigest("wallet-read-pause", struct {
			Phase string
			Pause pauseRecord
		}{phase, pause})
		if pauseBinding == "" {
			pauseBinding = digest
		} else if pauseBinding != digest {
			return Account{}, ErrConflict
		}
		record, err := s.accountsRecord(ctx)
		if err != nil {
			return Account{}, errors.New("Wallet unavailable")
		}
		if record.Revision != in.Revision {
			return Account{}, ErrConflict
		}
		for _, a := range record.Accounts {
			if a.ID != in.AccountID {
				continue
			}
			if !validProviderRPC(a) {
				return Account{}, errors.New("Configure RPC")
			}
			if managedNetworkAccount(a) || ordinaryNetworkAccount(a) {
				status := s.fixtureView(ctx)
				if !status.Available || status.Status == nil || status.Status.Account == nil || a.Signer == nil || *status.Status.Account != *a.Signer {
					return Account{}, errors.New("Wallet reference changed")
				}
			}
			return a, nil
		}
		return Account{}, errors.New("Wallet unavailable")
	}
	account, err := lookup()
	if err != nil {
		return empty, err
	}
	public, err := hex.DecodeString(account.PublicKey)
	if err != nil || len(public) != 32 || hex.EncodeToString(public) != account.PublicKey {
		return empty, errors.New("Wallet unavailable")
	}
	address := onchain.Base58(public)
	provider := rpcProvider(account.RPC.Endpoint, account.Network)
	if !rpcProviderRequiresKey(provider) {
		if in.Ref != "" || in.Passphrase != "" {
			return empty, errors.New("RPC locked or changed")
		}
		binding := rpcBinding(account)
		if in.Selection != nil && in.Selection.Binding != binding {
			return empty, errors.New("RPC locked or changed")
		}
		valid := func() error {
			current, err := lookup()
			if err != nil {
				return err
			}
			if rpcBinding(current) != binding {
				return ErrConflict
			}
			return nil
		}
		return s.executeWalletRead(ctx, account, in.Revision, address, provider, nil, binding, "", valid, in.Selection)
	}
	vault, err := s.readRPCVault(in.AccountID)
	if err != nil || vault.Version != 2 || vault.Ref != in.Ref || vault.Binding != rpcBinding(account) || (in.Selection != nil && in.Selection.Binding != vault.Binding) {
		return empty, errors.New("RPC locked or changed")
	}
	if vault.OSProtected {
		if in.Passphrase != "" {
			return empty, errors.New("RPC locked")
		}
	} else if !validPassword(in.Passphrase) {
		return empty, errors.New("Wallet unavailable")
	}
	encoded, _ := json.Marshal(vault)
	digest := sha256.Sum256(encoded)
	valid := func() error {
		current, err := lookup()
		if err != nil {
			return err
		}
		if rpcBinding(current) != vault.Binding {
			return ErrConflict
		}
		v, err := s.readRPCVault(in.AccountID)
		if err != nil {
			return errors.New("RPC locked or changed")
		}
		raw, _ := json.Marshal(v)
		if sha256.Sum256(raw) != digest {
			return errors.New("RPC locked or changed")
		}
		return nil
	}
	key, err := s.openRPCVaultKey(ctx, in.AccountID, in.Passphrase, vault)
	if err != nil {
		return empty, errors.New("RPC locked")
	}
	defer clear(key)
	return s.executeWalletRead(ctx, account, in.Revision, address, provider, key, vault.Binding, vault.Ref, valid, in.Selection)
}

func (s *Store) executeWalletRead(ctx context.Context, account Account, revision int64, address, provider string, key []byte, binding, ref string, valid func() error, selection *WalletSelection) (WalletSnapshot, error) {
	read := s.walletReadFixture
	if read == nil {
		read = func(ctx context.Context, a Account, address string, key []byte, valid func() error, selection *WalletSelection) (WalletSnapshot, error) {
			next := newPublicRPCTransport()
			defer next.transport.CloseIdleConnections()
			if !next.enabled {
				return WalletSnapshot{}, errors.New("Wallet reads disabled")
			}
			var rt http.RoundTripper = next
			if rpcProviderRequiresKey(provider) {
				rt = &providerRPCTransport{origin: a.RPC.Endpoint, provider: provider, key: key, valid: valid, next: next}
			}
			return walletRPC(ctx, a.RPC.Endpoint, rt, valid, false, address, a.Network, selection)
		}
	}
	out, err := read(ctx, account, address, key, valid, selection)
	if err != nil {
		return WalletSnapshot{}, err
	}
	if err = valid(); err != nil {
		return WalletSnapshot{}, err
	}
	// Bind exactly what the clients reviewed; snapshots are transient and never
	// rewrite account records, financial journals or encrypted vaults.
	out.AccountID = account.ID
	out.Revision = revision
	out.Binding = binding
	out.CredentialRef = ref
	return out, nil
}
