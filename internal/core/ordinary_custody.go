package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
)

func ordinaryNetworkAccount(a Account) bool {
	return a.Version == 4 && a.Mode == "managed" && a.Signer != nil && signer.ValidOrdinaryAccountRef(*a.Signer) && a.Signer.Network == a.Network && a.Signer.PublicKey == a.PublicKey
}
func migrateOrdinaryCustody(ctx context.Context, db *sql.DB, directory string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	var phase string
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 35 {
		return errors.New("unsupported ordinary custody predecessor; preserve original state")
	}
	phase, _, err = readPause(ctx, tx)
	if err != nil {
		return err
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	var raw string
	if err = tx.QueryRowContext(ctx, "SELECT document FROM accounts WHERE id=1").Scan(&raw); err != nil {
		return err
	}
	var accounts AccountsRecord
	if err = json.Unmarshal([]byte(raw), &accounts); err != nil {
		return err
	}
	for _, a := range accounts.Accounts {
		if a.Version > 3 || a.Mode == "managed" || a.Signer != nil && a.Signer.Version > 2 {
			return errors.New("predecessor cannot protect ordinary custody metadata")
		}
	}
	operation, _, err := readApplicationUpdate(ctx, tx)
	if err != nil {
		return err
	}
	switch operation.Phase {
	case "idle", "current", "available":
		if operation.OwnerLease != "" || operation.Receipt != nil {
			return ErrCorePaused
		}
	case "check-failed":
		if operation.Receipt != nil {
			return ErrCorePaused
		}
		if operation.OwnerLease != "" {
			if err = ordinaryCustodySettled(ctx, tx, directory, operation); err != nil {
				return err
			}
		}
	case "completed", "rolled-back":
		if err = ordinaryCustodySettled(ctx, tx, directory, operation); err != nil {
			return err
		}
	default:
		return ErrCorePaused
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=36"); err != nil {
		return err
	}
	return tx.Commit()
}
