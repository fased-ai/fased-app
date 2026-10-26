package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type AccountRPC struct {
	Version  int    `json:"version"`
	Endpoint string `json:"endpoint"`
	Network  string `json:"network"`
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

// Raise only the reader floor; never rewrite prior account or credential bytes.
func migrateProviderRPC(ctx context.Context, db *sql.DB) error {
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
		return errors.New("finish schema 20 recovery before RPC migration")
	}
	if err = tx.QueryRowContext(ctx, "SELECT document FROM accounts WHERE id=1").Scan(&raw); err != nil {
		return err
	}
	var r AccountsRecord
	if json.Unmarshal([]byte(raw), &r) != nil || r.Version != 1 {
		return errors.New("preserve invalid account records")
	}
	for _, a := range r.Accounts {
		if a.Version != 1 || (a.RPC != nil && a.RPC.Version != 1) {
			return errors.New("provider RPC data present before reader protection")
		}
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=21"); err != nil {
		return err
	}
	return tx.Commit()
}
