//go:build !linux

package signer

func (*Vault) BackupHistory([]byte) ([]byte, error)                { return nil, ErrUnavailable }
func (*Vault) verifyHistoryBackup([]byte, []byte) error            { return ErrUnavailable }
func (*Vault) RestoreHistory([]byte, []byte, []byte, string) error { return ErrUnavailable }
func (*storage) ensureRecoveredHistory(envelope) error             { return ErrUnavailable }

func (*Vault) verifyHistoryBackupWithSeed([]byte, []byte, []byte) error { return ErrUnavailable }
