package core

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
)

// RecoverPassword is local OS-owner administration only. No HTTP route exposes
// it. The engine must be stopped, preventing login/reset races across processes.
func RecoverPassword(ctx context.Context, dir, password string) error {
	if !validPassword(password) {
		return errors.New("use a password of at least 12 characters and at most 1024 bytes")
	}
	lock, err := LockInstance(filepath.Join(dir, "instance.lock"))
	if err != nil {
		return errors.New("stop the engine before owner recovery")
	}
	defer lock.Close()
	s, err := OpenStore(dir)
	if err != nil {
		return err
	}
	defer s.Close()
	claimed, err := s.Claimed(ctx)
	if err != nil {
		return err
	}
	if !claimed {
		return errors.New("workspace is not claimed; use its one-use claim flow")
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return err
	}
	hash := passwordHash(password, salt)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE instance SET password_salt=?,password_hash=? WHERE id=1", salt, hash); err != nil {
		return metadataWriteError(err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	return tx.Commit()
}
