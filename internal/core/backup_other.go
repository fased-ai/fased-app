//go:build !linux

package core

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
)

func (*Store) BackupMetadata(context.Context, update.Preflight) (update.RecoveryIndex, error) {
	return update.RecoveryIndex{}, errors.New("private metadata backup is supported only by the Linux disposable lifecycle fixture")
}

func (*Store) VerifyRecovery(context.Context, update.RecoveryCheck) (update.RecoveryIndex, error) {
	return update.RecoveryIndex{}, errors.New("Linux disposable recovery verification only")
}
