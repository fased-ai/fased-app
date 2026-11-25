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
			want: "execution profile must be off, testnet, or mainnet-capped",
		},
		{
			name: "vault flag requires testnet or mainnet-capped profile",
			args: []string{"serve", "--agent-vault-id", strings.Repeat("a", 32)},
			want: "--agent-vault-id requires --execution-profile=testnet or mainnet-capped",
		},
		{
			name: "rpc flag requires testnet profile",
			args: []string{"serve", "--solana-devnet-rpc", "https://api.devnet.solana.com"},
			want: "--solana-devnet-rpc requires --execution-profile=testnet",
		},
		{
			name: "mainnet-capped requires agent-vault-id",
			args: []string{"serve", "--execution-profile", "mainnet-capped", "--confirm-mainnet-capped", "--custody-config", "/tmp/id.json"},
			want: "--execution-profile=mainnet-capped requires --agent-vault-id",
		},
		{
			name: "mainnet-capped requires confirm-mainnet-capped",
			args: []string{"serve", "--execution-profile", "mainnet-capped", "--agent-vault-id", strings.Repeat("a", 32), "--custody-config", "/tmp/id.json"},
			want: "requires explicit owner confirmation",
		},
		{
			name: "mainnet-capped enforces $250 order ceiling",
			args: []string{"serve", "--execution-profile", "mainnet-capped", "--confirm-mainnet-capped", "--agent-vault-id", strings.Repeat("a", 32), "--mainnet-max-order-micro-usd", "300000000", "--custody-config", "/tmp/id.json"},
			want: "250000000",
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
