package core

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestWalletTransactionRejectsLateResults(t *testing.T) {
	for _, kind := range []string{"pause cycle", "credential rotation", "sign out", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			s := fixtureStore(t)
			defer s.Close()
			in := walletReadAccount(t, s)
			state, read := walletReadTLS(t)
			s.walletReadFixture = read
			initial, err := s.ReadWallet(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			in.Selection = &WalletSelection{WalletActivity: initial.Activity[0], Binding: initial.Binding, FetchedAt: initial.FetchedAt, ExpiresAt: initial.ExpiresAt}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var authorized atomic.Bool
			authorized.Store(true)
			state.set("tx-change")
			state.change = func() {
				switch kind {
				case "pause cycle":
					pause := pauseCommand(t, s, 1)
					if _, err := s.UpdatePause(t.Context(), pause); err != nil {
						t.Error(err)
					}
					if _, err := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "release", ProbeID: pause.ProbeID, Aborted: aborted(pause)}); err != nil {
						t.Error(err)
					}
				case "credential rotation":
					if _, err := s.RPCCredential(t.Context(), RPCCredentialCommand{AccountID: in.AccountID, Revision: in.Revision, Ref: in.Ref, Passphrase: in.Passphrase, Action: "save", Key: disposableProviderKey + "-rotated"}); err != nil {
						t.Error(err)
					}
				case "sign out":
					authorized.Store(false)
				case "cancel":
					cancel()
				}
			}
			if got, err := s.readWallet(ctx, in, authorized.Load); err == nil || got.Transaction != nil {
				t.Fatal("late result survived", kind, got, err)
			}
		})
	}
}

func TestWalletTransactionHTTPSSelectionAndPreservation(t *testing.T) {
	s := fixtureStore(t)
	defer s.Close()
	in := walletReadAccount(t, s)
	state, read := walletReadTLS(t)
	s.walletReadFixture = read
	initial, err := s.ReadWallet(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	selected := &WalletSelection{WalletActivity: initial.Activity[0], Binding: initial.Binding, FetchedAt: initial.FetchedAt, ExpiresAt: initial.ExpiresAt}
	in.Selection = selected
	vault, _ := s.readRPCVault(in.AccountID)
	originalVault, _ := json.Marshal(vault)
	accounts, _ := s.accountsRecord(t.Context())
	originalAccounts, _ := json.Marshal(accounts)
	got, err := s.ReadWallet(t.Context(), in)
	if err != nil || got.Transaction == nil || got.Transaction.NativeChange != "-100" {
		t.Fatal(got, err)
	}
	for _, kind := range []string{"expired", "binding", "slot", "signature", "status", "future"} {
		t.Run(kind, func(t *testing.T) {
			bad := *selected
			switch kind {
			case "expired":
				bad.FetchedAt -= 60
				bad.ExpiresAt -= 60
			case "binding":
				bad.Binding = strings.Repeat("b", 64)
			case "slot":
				bad.Slot = "98"
			case "signature":
				bad.Signature = onchain.Base58(bytes.Repeat([]byte{8}, 64))
			case "status":
				bad.Failed = true
			case "future":
				bad.FetchedAt += 120
				bad.ExpiresAt += 120
			}
			in.Selection = &bad
			got, err := s.ReadWallet(t.Context(), in)
			if err == nil || got.Version != 0 {
				t.Fatal("unbound selection accepted", got, err)
			}
		})
	}
	in.Selection = selected
	state.set("tx-unavailable")
	if got, err := s.ReadWallet(t.Context(), in); err == nil || got.Transaction != nil {
		t.Fatal("pruned transaction accepted")
	}
	state.set("")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.ReadWallet(ctx, in); err == nil {
		t.Fatal("cancelled inspection")
	}
	current, _ := s.readRPCVault(in.AccountID)
	raw, _ := json.Marshal(current)
	if !bytes.Equal(raw, originalVault) {
		t.Fatal("vault changed")
	}
	currentAccounts, _ := s.accountsRecord(t.Context())
	raw, _ = json.Marshal(currentAccounts)
	if !bytes.Equal(raw, originalAccounts) {
		t.Fatal("records changed")
	}
	state.set("tx-change")
	state.change = func() {
		v, _ := s.accountsRecord(t.Context())
		v.Accounts[0].Name = "Changed during inspection"
		if _, err := s.UpdateAccounts(t.Context(), v); err != nil {
			t.Error(err)
		}
	}
	if got, err := s.ReadWallet(t.Context(), in); err == nil || got.Transaction != nil {
		t.Fatal("late transaction survived revision change")
	}
}

func walletTransactionFixture() map[string]any {
	key := func(n byte) string { return onchain.Base58(bytes.Repeat([]byte{n}, 32)) }
	token := func(index int, program, amount string) map[string]any {
		return map[string]any{"accountIndex": index, "mint": key(3), "owner": key(2), "programId": program, "uiTokenAmount": map[string]any{"amount": amount, "decimals": 6}}
	}
	return map[string]any{
		"slot": 99, "blockTime": nil, "version": "legacy",
		"transaction": map[string]any{"signatures": []any{onchain.Base58(bytes.Repeat([]byte{6}, 64))}, "message": map[string]any{"accountKeys": []any{key(7), key(2), key(4), key(5)}, "header": map[string]any{"numRequiredSignatures": 1, "numReadonlySignedAccounts": 0, "numReadonlyUnsignedAccounts": 0}}},
		"meta":        map[string]any{"err": nil, "fee": 5000, "preBalances": []any{100000, uint64(9007199254740993), 2039280, 2039280}, "postBalances": []any{95000, uint64(9007199254740893), 2039280, 2039280}, "preTokenBalances": []any{token(2, onchain.Token, "9007199254740993"), token(3, token2022Program, "20")}, "postTokenBalances": []any{token(2, onchain.Token, "9007199254741000"), token(3, token2022Program, "15")}},
	}
}

func walletSelectionFixture() *WalletSelection {
	now := time.Now().Unix()
	return &WalletSelection{WalletActivity: WalletActivity{Signature: onchain.Base58(bytes.Repeat([]byte{6}, 64)), Slot: "99"}, FetchedAt: now, ExpiresAt: now + 60, Binding: strings.Repeat("a", 64)}
}

func TestWalletTransactionParse(t *testing.T) {
	address := onchain.Base58(bytes.Repeat([]byte{2}, 32))
	parse := func(v map[string]any, selected *WalletSelection) (WalletTransaction, error) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return parseWalletTransaction(b, address, selected)
	}
	got, err := parse(walletTransactionFixture(), walletSelectionFixture())
	if err != nil || got.FeeLamports != "5000" || got.Payer == address || got.NativeChange != "-100" || !got.TokenChangesComplete || len(got.Tokens) != 2 || got.Tokens[0].Change != "7" || got.Tokens[1].Change != "-5" {
		t.Fatalf("exact observation: %+v %v", got, err)
	}
	t.Run("failed still reports observed fee and changes", func(t *testing.T) {
		v := walletTransactionFixture()
		v["meta"].(map[string]any)["err"] = map[string]any{"InstructionError": []any{0, "Custom"}}
		s := walletSelectionFixture()
		s.Failed = true
		got, err := parse(v, s)
		if err != nil || !got.Failed || got.FeeLamports != "5000" {
			t.Fatal(got, err)
		}
	})
	t.Run("missing and unpaired token metadata never implies zero", func(t *testing.T) {
		v := walletTransactionFixture()
		delete(v["meta"].(map[string]any), "preTokenBalances")
		got, err := parse(v, walletSelectionFixture())
		if err != nil || got.TokenChangesComplete || len(got.Tokens) != 0 {
			t.Fatal(got, err)
		}
		v = walletTransactionFixture()
		v["meta"].(map[string]any)["postTokenBalances"] = []any{}
		got, err = parse(v, walletSelectionFixture())
		if err != nil || got.TokenChangesComplete || len(got.Tokens) != 0 {
			t.Fatal(got, err)
		}
	})
	t.Run("v0 loaded addresses", func(t *testing.T) {
		v := walletTransactionFixture()
		v["version"] = 0
		m := v["transaction"].(map[string]any)["message"].(map[string]any)
		keys := m["accountKeys"].([]any)
		m["accountKeys"] = keys[:2]
		m["addressTableLookups"] = []any{map[string]any{"accountKey": onchain.Base58(bytes.Repeat([]byte{9}, 32)), "writableIndexes": []any{0, 1}, "readonlyIndexes": []any{}}}
		v["meta"].(map[string]any)["loadedAddresses"] = map[string]any{"writable": keys[2:], "readonly": []any{}}
		got, err := parse(v, walletSelectionFixture())
		if err != nil || len(got.Tokens) != 2 {
			t.Fatal(got, err)
		}
	})
	for _, name := range []string{"slot", "signature", "status", "fee", "meta", "native", "owner", "duplicate", "amount", "decimals", "version", "loaded"} {
		t.Run("refuse "+name, func(t *testing.T) {
			v := walletTransactionFixture()
			meta := v["meta"].(map[string]any)
			msg := v["transaction"].(map[string]any)["message"].(map[string]any)
			switch name {
			case "slot":
				v["slot"] = 100
			case "signature":
				v["transaction"].(map[string]any)["signatures"] = []any{onchain.Base58(bytes.Repeat([]byte{8}, 64))}
			case "status":
				meta["err"] = map[string]any{"error": "untrusted"}
			case "fee":
				meta["fee"] = -1
			case "meta":
				v["meta"] = nil
			case "native":
				meta["preBalances"] = []any{0}
			case "owner":
				msg["accountKeys"] = []any{onchain.Base58(bytes.Repeat([]byte{10}, 32))}
			case "duplicate":
				keys := msg["accountKeys"].([]any)
				keys[0] = keys[1]
			case "amount":
				meta["preTokenBalances"].([]any)[0].(map[string]any)["uiTokenAmount"].(map[string]any)["amount"] = "1e9"
			case "decimals":
				meta["postTokenBalances"].([]any)[0].(map[string]any)["uiTokenAmount"].(map[string]any)["decimals"] = 7
			case "version":
				v["version"] = 1
			case "loaded":
				v["version"] = 0
			}
			if _, err := parse(v, walletSelectionFixture()); err == nil {
				t.Fatal("accepted inconsistent transaction")
			}
		})
	}
}
