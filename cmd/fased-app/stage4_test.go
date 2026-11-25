package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestServeStage4FlagsValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "invalid profile",
			args: []string{"serve", "--execution-profile", "mainnet"},
			want: "execution profile must be off or testnet",
		},
		{
			name: "vault flag requires testnet profile",
			args: []string{"serve", "--agent-vault-id", strings.Repeat("a", 32)},
			want: "require --execution-profile=testnet",
		},
		{
			name: "rpc flag requires testnet profile",
			args: []string{"serve", "--solana-devnet-rpc", "https://api.devnet.solana.com"},
			want: "require --execution-profile=testnet",
		},
		{
			name: "testnet profile requires custody config",
			args: []string{"serve", "--execution-profile", "testnet"},
			want: "requires serve with --custody-config",
		},
		{
			name: "testnet profile rejects status subcommand",
			args: []string{"status", "--execution-profile", "testnet", "--custody-config", "/tmp/id.json"},
			want: "requires serve with --custody-config",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			err := run(tc.args, nil, &out, &errOut)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("args=%v: got err=%v, want substring %q", tc.args, err, tc.want)
			}
		})
	}
}
