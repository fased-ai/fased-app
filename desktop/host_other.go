//go:build !linux

package main

import (
	"context"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func hostPreparationNotice() string                                                { return "" }
func prepareDesktopHost(context.Context, string, localinstall.Paths) (bool, error) { return false, nil }
