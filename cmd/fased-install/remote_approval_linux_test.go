//go:build linux

package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestFreshRemoteApprovalPreservesExistingOwner(t *testing.T) {
	missing := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	if err := requireFreshRemoteApproval(1100, "/home/owner", os.ErrNotExist, missing); err != nil {
		t.Fatal("fresh setup refused", err)
	}
	for _, existing := range []string{"fased-app-vps-1100", "intent.json", "owner-handoff.json"} {
		stat := func(path string) (os.FileInfo, error) {
			if strings.HasSuffix(path, existing) {
				return nil, nil
			}
			return nil, os.ErrNotExist
		}
		if err := requireFreshRemoteApproval(1100, "/home/owner", os.ErrNotExist, stat); err == nil {
			t.Fatalf("existing %s could replace its claimant", existing)
		}
	}
	if err := requireFreshRemoteApproval(1100, "/home/owner", nil, func(string) (os.FileInfo, error) { t.Fatal("recorded approval was reprobed"); return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if err := requireFreshRemoteApproval(1100, "/home/owner", errors.New("damaged approval"), missing); err == nil {
		t.Fatal("damaged approval ignored")
	}
}
