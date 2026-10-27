package core

import (
	"context"
	"database/sql"
	"errors"

	"github.com/fased-ai/fased-app/internal/update"
)

// UpdateSnapshot reads one SQLite transaction. It emits no credentials or
// sessions. Sessions/claim codes are ephemeral and not financial authority.
func (s *Store) UpdateSnapshot(ctx context.Context, probe string) (update.CoreSnapshot, error) {
	if !update.IDOK(probe) {
		return update.CoreSnapshot{}, errors.New("invalid inventory challenge")
	}
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return update.CoreSnapshot{}, e
	}
	defer tx.Rollback()
	result, e := s.snapshotTx(ctx, tx, probe)
	if e != nil {
		return update.CoreSnapshot{}, e
	}
	if e = tx.Commit(); e != nil {
		return update.CoreSnapshot{}, e
	}
	return result, nil
}

type snapshotReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Store) snapshotTx(ctx context.Context, tx snapshotReader, probe string) (update.CoreSnapshot, error) {
	var e error
	var v struct {
		History                         string
		Schema                          int
		Instance, Setup, Accounts       string
		SetupRevision, AccountsRevision int64
		PasswordSalt, PasswordHash      []byte
		Agents                          string
		AgentsRevision                  int64
		Research                        string
		ResearchRevision                int64
	}
	if e = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v.Schema); e != nil {
		return update.CoreSnapshot{}, e
	}
	if v.Schema != StateSchema {
		return update.CoreSnapshot{}, errors.New("unsupported core snapshot schema")
	}
	if e = tx.QueryRowContext(ctx, "SELECT instance_id,setup,revision,password_salt,password_hash FROM instance WHERE id=1").Scan(&v.Instance, &v.Setup, &v.SetupRevision, &v.PasswordSalt, &v.PasswordHash); e != nil {
		return update.CoreSnapshot{}, e
	}
	if e = tx.QueryRowContext(ctx, "SELECT document,revision FROM accounts WHERE id=1").Scan(&v.Accounts, &v.AccountsRevision); e != nil {
		return update.CoreSnapshot{}, e
	}
	if e = tx.QueryRowContext(ctx, "SELECT document,revision FROM agent_workspace WHERE id=1").Scan(&v.Agents, &v.AgentsRevision); e != nil {
		return update.CoreSnapshot{}, e
	}
	if e = tx.QueryRowContext(ctx, "SELECT document,revision FROM wen_research WHERE id=1").Scan(&v.Research, &v.ResearchRevision); e != nil {
		return update.CoreSnapshot{}, e
	}
	v.History, e = historySnapshot(ctx, tx)
	if e != nil {
		return update.CoreSnapshot{}, e
	}
	contract := UpdateContract()
	state := update.State{Name: "core-db", Version: v.Schema, Features: contract.State[0].Features, Anchor: update.SnapshotDigest("core-db", v)}
	return update.CoreSnapshot{Version: 1, ProbeID: probe, BootID: s.bootID, InstanceID: v.Instance, State: state}, nil
}
