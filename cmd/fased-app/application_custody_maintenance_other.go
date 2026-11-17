//go:build !linux

package main

import (
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
)

func bindCompiledCustodyMaintenance(*core.Store, int, string) error {
	return errors.New("custody maintenance requires Linux")
}
