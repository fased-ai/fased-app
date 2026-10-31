// Package walletfixture is imported by compiled acceptance tests only.
// It has one fixed origin, synthetic public observations and no external dial.
package walletfixture

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/fased-ai/fased-app/internal/onchain"
)

const Endpoint = "https://devnet.helius-rpc.com"
const Genesis = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
const Lamports = "9007199254740993"

var Signature = onchain.Base58(bytes.Repeat([]byte{6}, 64))
var Mint = onchain.Base58(bytes.Repeat([]byte{3}, 32))
var TokenAccount = onchain.Base58(bytes.Repeat([]byte{4}, 32))

func New() (*http.Transport, func()) {
	var mu sync.Mutex
	address := ""
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || r.URL.Query().Get("api-key") == "" {
			http.Error(w, "invalid fixture request", 400)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		var result any
		if in.Method != "getGenesisHash" && in.Method != "getTransaction" {
			var key string
			if len(in.Params) == 0 || json.Unmarshal(in.Params[0], &key) != nil {
				http.Error(w, "invalid fixture key", 400)
				return
			}
			if _, err := onchain.Key(key); err != nil {
				http.Error(w, "invalid fixture key", 400)
				return
			}
			if in.Method == "getBalance" {
				address = key
			} else if address != key {
				http.Error(w, "mixed fixture wallet", 400)
				return
			}
		}
		switch in.Method {
		case "getGenesisHash":
			result = Genesis
		case "getBalance":
			result = map[string]any{"context": map[string]any{"slot": 100}, "value": uint64(9007199254740993)}
		case "getTokenAccountsByOwner":
			var filter struct {
				Program string `json:"programId"`
			}
			if len(in.Params) < 2 || json.Unmarshal(in.Params[1], &filter) != nil {
				http.Error(w, "invalid filter", 400)
				return
			}
			entries := []any{}
			if filter.Program == onchain.Token {
				entries = append(entries, map[string]any{"pubkey": TokenAccount, "account": map[string]any{"owner": onchain.Token, "executable": false, "data": map[string]any{"parsed": map[string]any{"type": "account", "info": map[string]any{"owner": address, "mint": Mint, "state": "initialized", "tokenAmount": map[string]any{"amount": "2000000", "decimals": 6}}}}}})
			}
			result = map[string]any{"context": map[string]any{"slot": 100}, "value": entries}
		case "getSignaturesForAddress":
			result = []any{map[string]any{"signature": Signature, "slot": 99, "err": nil, "blockTime": nil, "confirmationStatus": "finalized"}}
		case "getTransaction":
			var sig string
			if address == "" || len(in.Params) == 0 || json.Unmarshal(in.Params[0], &sig) != nil || sig != Signature {
				http.Error(w, "invalid fixture signature", 400)
				return
			}
			token := func(amount string) any {
				return map[string]any{"accountIndex": 1, "mint": Mint, "owner": address, "programId": onchain.Token, "uiTokenAmount": map[string]any{"amount": amount, "decimals": 6}}
			}
			result = map[string]any{"slot": 99, "blockTime": nil, "version": "legacy", "transaction": map[string]any{"signatures": []string{Signature}, "message": map[string]any{"accountKeys": []string{address, TokenAccount}, "header": map[string]any{"numRequiredSignatures": 1, "numReadonlySignedAccounts": 0, "numReadonlyUnsignedAccounts": 0}}}, "meta": map[string]any{"err": nil, "fee": 5000, "preBalances": []uint64{9007199254740993, 2039280}, "postBalances": []uint64{9007199254735993, 2039280}, "preTokenBalances": []any{token("1000000")}, "postTokenBalances": []any{token("2000000")}}}
		default:
			http.Error(w, "unknown fixture method", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": in.ID, "result": result})
	}))
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	next := server.Client().Transport.(*http.Transport).Clone()
	next.TLSClientConfig = next.TLSClientConfig.Clone()
	next.TLSClientConfig.RootCAs = roots
	next.TLSClientConfig.ServerName = "example.com"
	next.Proxy = nil
	next.DialContext = func(ctx context.Context, network, destination string) (net.Conn, error) {
		if destination != "devnet.helius-rpc.com:443" {
			return nil, errors.New("external fixture dial refused")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return next, func() { next.CloseIdleConnections(); server.Close() }
}
