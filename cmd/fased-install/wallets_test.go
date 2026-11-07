package main

import (
	"io"
	"strings"
	"testing"
)

func TestWalletsEntryRejectsIncompleteConsentBeforeAcquisition(t *testing.T) {
	for _, args := range [][]string{
		{"--install", "--profile", "remote", "--entry-action", "wallets"},
		{"--install", "--profile", "local", "--entry-action", "wallets", "--wallet-instance", strings.Repeat("A", 43), "--wallet-network", "devnet"},
		{"--install", "--profile", "remote", "--entry-action", "wallets", "--wallet-instance", strings.Repeat("A", 43), "--wallet-network", "other"},
		{"--install", "--profile", "remote", "--wallet-network", "devnet"},
	} {
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err == nil || !strings.Contains(strings.ToLower(err.Error()), "wallet") {
			t.Fatal("incomplete wallet consent reached acquisition", err)
		}
	}
}
