package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWalletReadClientPreservesBoundedProjection(t *testing.T) {
	key, amount := strings.Repeat("1", 44), "18446744073709551615"
	want := WalletSnapshot{Address: key, Network: "devnet", Lamports: amount, Transaction: &WalletTransaction{Payer: key}}
	for i := 0; i < 128; i++ {
		want.Tokens = append(want.Tokens, WalletTokenBalance{Program: key, Mint: key, Amount: amount, Decimals: 255, Slot: amount})
	}
	for i := 0; i < 20; i++ {
		want.Activity = append(want.Activity, WalletActivity{Signature: strings.Repeat("1", 88), Slot: amount})
	}
	for i := 0; i < 256; i++ {
		want.Transaction.Tokens = append(want.Transaction.Tokens, WalletTokenChange{Account: key, Program: key, Mint: key, Decimals: 255, Before: amount, After: amount, Change: "-" + amount})
	}
	raw, err := json.Marshal(want)
	if err != nil || len(raw) <= 32*1024 || len(raw) >= 256*1024 {
		t.Fatal("regression must exercise the bounded large wallet projection", len(raw), err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	client := Client{Endpoint: Endpoint{URL: server.URL}, HTTP: server.Client()}
	var got WalletSnapshot
	if err = client.Do(t.Context(), http.MethodPost, "/api/v1/accounts/read", nil, &got); err != nil {
		t.Fatal("installed CLI rejects a supported native wallet response", err)
	}
	if len(got.Tokens) != 128 || len(got.Activity) != 20 || got.Transaction == nil || len(got.Transaction.Tokens) != 256 || got.Lamports != amount {
		t.Fatal("wallet projection was truncated")
	}
	for _, route := range []string{"/api/v1/accounts", "/api/v1/accounts/read?override=true", "/api/v1/accounts/read/other"} {
		if err = client.Do(t.Context(), http.MethodPost, route, nil, &got); err == nil {
			t.Fatal("large response allowance escaped the exact wallet route", route)
		}
	}
}

func TestWalletReadClientRefusesOversizedProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"value": strings.Repeat("x", 256*1024)})
	}))
	defer server.Close()
	client := Client{Endpoint: Endpoint{URL: server.URL}, HTTP: server.Client()}
	var got map[string]string
	if err := client.Do(t.Context(), http.MethodPost, "/api/v1/accounts/read", nil, &got); err == nil {
		t.Fatal("oversized wallet response accepted")
	}
}
