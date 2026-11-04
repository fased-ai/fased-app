//go:build linux

package main

import (
	"bytes"
	"testing"
)

func TestRemoteEntryUpgradeHasNoLegacyPasswordHandoff(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if err := signInForEntryUpgrade(t.Context(), "remote", bytes.NewReader(nil), &out, &diagnostic); err != nil || out.Len() != 0 || diagnostic.Len() != 0 {
		t.Fatal("Remote bootstrap tried legacy sign-in", err, out.String(), diagnostic.String())
	}
}
