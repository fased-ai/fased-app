//go:build !linux

package signer

import "github.com/fased-ai/fased-app/internal/update"

func (*storage) stageWrite(string, int, int64, []byte) error { return ErrUnavailable }
func (*storage) stageFiles(update.Intent, bool) ([]update.StageFile, error) {
	return nil, ErrUnavailable
}
