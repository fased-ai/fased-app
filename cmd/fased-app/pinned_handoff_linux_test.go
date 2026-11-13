//go:build linux

package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

func TestPinnedInstallationSessionPrecedesOwnerEntry(t *testing.T) {
	m := custodyinstall.Manifest{Version: 3, Owner: 1400, Root: "/var/lib/fased-app-custody-1400", Enrollment: true, Location: "local", NativeOwnerAccess: true}
	runner := filepath.Join(m.Root, "maintenance", "runner")
	for _, tc := range []struct {
		name        string
		fenced      bool
		fenceErr    error
		published   bool
		ready       bool
		runtimeErr  error
		wantPrepare bool
		wantError   bool
	}{
		{"live predecessor", false, nil, false, true, nil, true, false},
		{"published live predecessor", false, nil, true, true, nil, true, false},
		{"published stopped after marker removal", false, nil, true, false, nil, false, false},
		{"stopped preparation", true, nil, false, false, nil, false, false},
		{"transaction recovery", true, nil, true, false, nil, false, false},
		{"foreign fence", false, errors.New("foreign fence"), true, false, nil, false, true},
		{"foreign service", false, nil, true, false, errors.New("foreign service"), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checked, prepared := false, false
			err := preparePinnedInstallationSession(m, runner, tc.published, func() (bool, error) { checked = true; return tc.fenced, tc.fenceErr }, func() (bool, error) { return tc.ready, tc.runtimeErr }, func() error {
				if !checked {
					t.Fatal("session prepared before recovery inspection")
				}
				prepared = true
				return nil
			})
			if !checked || prepared != tc.wantPrepare || (err != nil) != tc.wantError {
				t.Fatalf("checked=%t prepared=%t err=%v", checked, prepared, err)
			}
		})
	}
	failure := errors.New("session unavailable")
	if err := preparePinnedInstallationSession(m, runner, true, func() (bool, error) { return false, nil }, func() (bool, error) { return true, nil }, func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("session failure was not preserved: %v", err)
	}
	if err := preparePinnedInstallationSession(m, filepath.Join(m.Root, "fased-app"), true, nil, nil, nil); err != nil {
		t.Fatalf("legacy runner should not prepare a pinned session: %v", err)
	}
	if err := preparePinnedInstallationSession(m, runner, true, nil, nil, nil); err == nil {
		t.Fatal("pinned runner accepted missing session checks")
	}
}
