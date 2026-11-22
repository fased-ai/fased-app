//go:build windows

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Native Windows command installation stays with its native package adapter.
func installCommand(string, string, string, bool, io.Writer) error { return nil }

func removeCommand(corePath, _, _ string, _ bool) error {
	base := strings.TrimSpace(os.Getenv("LocalAppData"))
	if base == "" {
		dir, err := os.UserCacheDir()
		if err != nil || strings.TrimSpace(dir) == "" {
			return nil
		}
		base = dir
	}
	shimPath := filepath.Join(base, "Fased App", "bin", "fased.cmd")
	info, err := os.Lstat(shimPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return err
	}
	body, err := os.ReadFile(shimPath)
	if err != nil {
		return err
	}
	absCore, absErr := filepath.Abs(corePath)
	want := []byte(fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", corePath))
	wantAbs := []byte(fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", absCore))
	if bytes.Equal(body, want) || (absErr == nil && bytes.Equal(body, wantAbs)) {
		return os.Remove(shimPath)
	}
	return nil
}

func verifiedInstalledCommand(string, string, string, string, bool) (string, error) {
	return "", errors.New("use the native installed command")
}
