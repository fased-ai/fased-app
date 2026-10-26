package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type RPCConfigInput struct {
	AccountID string `json:"accountId"`
	Revision  int64  `json:"revision"`
	Endpoint  string `json:"endpoint,omitempty"`
	Network   string `json:"network,omitempty"`
	Remove    bool   `json:"remove,omitempty"`
}

func sameAccountIdentity(a, b Account) bool {
	if a.ID != b.ID || a.Mode != b.Mode || a.Network != b.Network || a.PublicKey != b.PublicKey {
		return false
	}
	if a.Signer == nil || b.Signer == nil {
		return a.Signer == nil && b.Signer == nil
	}
	return *a.Signer == *b.Signer
}

func (s *Store) ConfigureAccountRPC(ctx context.Context, in RPCConfigInput) (AccountsView, error) {
	record, err := s.accountsRecord(ctx)
	if err != nil {
		return AccountsView{}, err
	}
	if record.Revision != in.Revision {
		return AccountsView{}, ErrConflict
	}
	if in.Remove && (in.Endpoint != "" || in.Network != "") {
		return AccountsView{}, errors.New("remove does not accept endpoint settings")
	}
	for i, a := range record.Accounts {
		if a.ID != in.AccountID {
			continue
		}
		if in.Remove {
			record.Accounts[i].RPC = nil
		} else {
			if in.Endpoint == "" || in.Network == "" {
				return AccountsView{}, errors.New("choose an endpoint and network")
			}
			if _, err = s.CheckAccountRPC(ctx, RPCCheckRequest{AccountID: a.ID, AccountRevision: &in.Revision, Endpoint: in.Endpoint, Network: in.Network}); err != nil {
				return AccountsView{}, err
			}
			record.Accounts[i].RPC = &AccountRPC{Version: 1, Endpoint: in.Endpoint, Network: in.Network}
		}
		return s.updateAccounts(ctx, record, true)
	}
	return AccountsView{}, errors.New("wallet unavailable")
}

// This migration changes only the reader floor. Existing account bytes and
// revisions remain unchanged; older readers must not discard persisted RPCs.
func migrateAccountRPC(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase, raw string
	if err = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return err
	}
	if phase == "paused" {
		return errors.New("finish schema 19 recovery before RPC migration")
	}
	if err = tx.QueryRowContext(ctx, "SELECT document FROM accounts WHERE id=1").Scan(&raw); err != nil {
		return err
	}
	var prior struct {
		Version  int                          `json:"version"`
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if json.Unmarshal([]byte(raw), &prior) != nil || prior.Version != 1 {
		return errors.New("preserve invalid account records")
	}
	for _, a := range prior.Accounts {
		for key := range a {
			if strings.EqualFold(key, "rpc") {
				return errors.New("RPC fields present in unprotected schema; preserve records")
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=20"); err != nil {
		return err
	}
	return tx.Commit()
}
