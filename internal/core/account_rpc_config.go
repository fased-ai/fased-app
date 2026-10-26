package core

import (
	"context"
	"errors"
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
