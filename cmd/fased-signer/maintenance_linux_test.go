//go:build linux

package main

import (
	"io"
	"strings"
	"testing"
)

func TestPackageMaintenanceRejectsIncompleteInstance(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, args := range [][]string{
		{"custody-maintenance", "--owner", "1800", "--location", "local", "--operation", id},
		{"custody-maintenance", "--owner", "1800", "--instance", id, "--operation", id},
		{"custody-maintenance", "--owner", "1800", "--location", "../local", "--instance", id, "--operation", id},
	} {
		if err := signerMaintenanceCommand(args, nil, io.Discard); err == nil {
			t.Fatal("incomplete or foreign instance accepted")
		}
	}
}
