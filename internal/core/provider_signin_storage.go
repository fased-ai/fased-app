package core

import (
	"context"
	"database/sql"
	"errors"
)

// Schema 26 protects zero-money plan authority and nonmonetary usage holds.
// A schema-25 task reader accepts these values, so field validation alone cannot
// prevent an older binary from rewriting them. Records and revisions stay exact.
func migrateSignInPlan(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phase string
	if err = tx.QueryRowContext(ctx, "SELECT phase FROM update_pause WHERE id=1").Scan(&phase); err != nil {
		return err
	}
	if phase == "paused" {
		return errors.New("finish schema 25 recovery before sign-in plan migration")
	}
	if _, _, err = readAgents(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=26"); err != nil {
		return err
	}
	return tx.Commit()
}
