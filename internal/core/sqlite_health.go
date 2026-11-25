package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SQLiteHealthReport summarizes bounded read-only diagnostics for a single
// SQLite state database without exposing any stored records or credentials.
type SQLiteHealthReport struct {
	Name       string `json:"name"`
	Exists     bool   `json:"exists"`
	Bytes      int64  `json:"bytes"`
	WALBytes   int64  `json:"walBytes"`
	QuickCheck string `json:"quickCheck"`
	Healthy    bool   `json:"healthy"`
}

// InspectSQLiteHealth inspects a SQLite database file and runs PRAGMA quick_check(1).
func InspectSQLiteHealth(ctx context.Context, dbPath string) SQLiteHealthReport {
	report := SQLiteHealthReport{Name: filepath.Base(dbPath)}
	info, err := os.Lstat(dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.QuickCheck = "missing"
		} else {
			report.QuickCheck = "stat-error"
		}
		return report
	}
	if !info.Mode().IsRegular() {
		report.QuickCheck = "non-regular"
		return report
	}
	report.Exists = true
	report.Bytes = info.Size()
	if walInfo, walErr := os.Lstat(dbPath + "-wal"); walErr == nil && walInfo.Mode().IsRegular() {
		report.WALBytes = walInfo.Size()
	}
	db, err := sql.Open("sqlite", sqliteFileURI(dbPath, "mode=ro&_pragma=busy_timeout(3000)"))
	if err != nil {
		report.QuickCheck = "open-error"
		return report
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := sqliteQuickCheck(ctx, db, report.Name); err != nil {
		report.QuickCheck = sanitizeDiagValue(err.Error(), 120)
		return report
	}
	report.QuickCheck = "ok"
	report.Healthy = true
	return report
}

// SnapshotSQLite creates a consistent standalone snapshot of srcPath at dstPath
// using SQLite VACUUM INTO (safe while Fased Core holds WAL readers/writers).
func SnapshotSQLite(ctx context.Context, srcPath, dstPath string) error {
	srcAbs, err := filepath.Abs(srcPath)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dstPath)
	if err != nil {
		return err
	}
	if strings.ContainsAny(dstAbs, "\x00\r\n") {
		return errors.New("invalid snapshot destination path")
	}
	info, err := os.Lstat(srcAbs)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("sqlite source must be a regular file")
	}
	if err := os.Remove(dstAbs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err := sql.Open("sqlite", sqliteFileURI(srcAbs, "_pragma=busy_timeout(5000)"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", dstAbs); err != nil {
		_ = os.Remove(dstAbs)
		return fmt.Errorf("snapshot %s failed: %w", filepath.Base(srcAbs), err)
	}
	if err := os.Chmod(dstAbs, 0600); err != nil {
		_ = os.Remove(dstAbs)
		return err
	}
	return nil
}

// sqliteQuickCheck runs SQLite's bounded integrity check (PRAGMA quick_check(1))
// at store startup so corrupted state surfaces explicitly before serving.
func sqliteQuickCheck(ctx context.Context, db *sql.DB, name string) error {
	if db == nil {
		return errors.New("sqlite database is nil")
	}
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&result); err != nil {
		return fmt.Errorf("%s integrity check failed: %w", name, err)
	}
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		return fmt.Errorf("%s integrity check failed: %s", name, sanitizeDiagValue(result, 120))
	}
	return nil
}

// OptionalStateSQLitePaths returns the clean relative paths of optional Stage 1-3
// engine SQLite databases stored beneath the Core data directory (excluding secrets/keys).
func OptionalStateSQLitePaths() []string {
	return []string{
		"marketdata/meta.sqlite",
		"integrations/decision.sqlite",
		"integrations/mcpclient.sqlite",
		"worldintel/intel.sqlite",
		"automation/automation.sqlite",
		"quantlab/lab.sqlite",
		"desk/desk.sqlite",
		"hyperliquid/orders.sqlite",
		"cex/cex.sqlite",
		"equities/equities.sqlite",
		"hub/hub.sqlite",
		"swarm/swarm.sqlite",
	}
}

func checkpointSQLitePathPassive(ctx context.Context, dbPath string) error {
	info, err := os.Lstat(dbPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	db, err := sql.Open("sqlite", sqliteFileURI(dbPath, "_pragma=busy_timeout(3000)"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var busy, logPages, checkpointed int
	return db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logPages, &checkpointed)
}

// CheckpointWAL performs a non-blocking passive WAL checkpoint on core.sqlite,
// agents.sqlite (when initialized), and any initialized Stage 1-3 SQLite stores
// to keep WAL sidecars bounded over long runs.
func (s *Store) CheckpointWAL(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var firstErr error
	if s.db != nil {
		var busy, logPages, checkpointed int
		if err := s.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logPages, &checkpointed); err != nil {
			firstErr = err
		}
	}
	s.agentV2Mu.Lock()
	v2 := s.agentV2
	s.agentV2Mu.Unlock()
	if v2 != nil && v2.db != nil {
		var busy, logPages, checkpointed int
		if err := v2.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &logPages, &checkpointed); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.dir != "" {
		for _, rel := range OptionalStateSQLitePaths() {
			if err := checkpointSQLitePathPassive(ctx, filepath.Join(s.dir, filepath.FromSlash(rel))); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
