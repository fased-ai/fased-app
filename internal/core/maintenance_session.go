package core

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// IssueMaintenanceSession is for the verified dropped-AppUID helper only. It
// opens an existing session table without running OpenStore or any migration:
// an admitted new helper may be talking to an older, still-running reader.
// It changes only an ephemeral session, never owner bytes, schema or holds.
func IssueMaintenanceSession(ctx context.Context, dir, expected string) (string, error) {
	if os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return "", errors.New("ordinary data-owner helper required")
	}
	infoDir, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !infoDir.IsDir() {
		return "", errors.New("existing data-owner directory required")
	}
	if err = protectPath(dir, infoDir); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "core.sqlite")
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("existing core database required")
	}
	if err = protectPath(path, info); err != nil {
		return "", err
	}
	uri := url.URL{Scheme: "file", Path: path}
	query := uri.Query()
	query.Set("mode", "rw")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL;"); err != nil {
		return "", err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return "", err
	}
	if !oneOfSchema(version, 33, 35, 36, 37, 38) {
		return "", errors.New("unsupported predecessor session format")
	}
	var instance string
	var claimed bool
	if err = tx.QueryRowContext(ctx, "SELECT instance_id,password_hash IS NOT NULL FROM instance WHERE id=1").Scan(&instance, &claimed); err != nil {
		return "", err
	}
	if instance != expected || !claimed {
		return "", ErrUnauthorized
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return "", errors.New("core database identity changed")
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,expires) VALUES(?,?)", tokenHash(token), time.Now().Add(SessionLifetime).Unix()); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}
func oneOfSchema(value int, accepted ...int) bool {
	for _, candidate := range accepted {
		if value == candidate {
			return true
		}
	}
	return false
}
