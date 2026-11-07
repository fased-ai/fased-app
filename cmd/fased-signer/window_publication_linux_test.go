//go:build linux

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOwnerPublicationProcessInterruptionLeavesNoPartialFinal(t *testing.T) {
	if path := os.Getenv("FASED_OWNER_PUBLICATION_TEST_PATH"); path != "" {
		_ = publishOwnerFile(path, []byte("disposable encrypted backup"), func() error { os.Exit(86); return nil })
		os.Exit(87)
	}
	path := filepath.Join(ownerFixture(t), "backup.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnerPublicationProcessInterruptionLeavesNoPartialFinal$")
	cmd.Env = append(os.Environ(), "FASED_OWNER_PUBLICATION_TEST_PATH="+path)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 {
		t.Fatal("interruption probe failed", err)
	}
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial final survived process interruption", err)
	}
	doc := []byte("disposable encrypted backup")
	if err = writeOwnerFile(path, doc); err != nil {
		t.Fatal("retry after interrupted publication", err)
	}
	if err = writeOwnerFile(path, []byte("conflicting backup")); err == nil {
		t.Fatal("existing backup overwritten")
	}
	got, err := readOwnerFile(path)
	if err != nil || !bytes.Equal(got, doc) {
		t.Fatal("original final changed", err)
	}
}
