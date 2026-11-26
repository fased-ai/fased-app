package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/httpapi"
	"github.com/fased-ai/fased-app/internal/walletvault"
)

func (s *Store) walletVaultService() (*walletvault.Service, error) {
	if s == nil || s.dir == "" {
		return nil, errors.New("workspace store unavailable")
	}
	s.walletVaultMu.Lock()
	defer s.walletVaultMu.Unlock()
	if svc, ok := s.walletVaultPtr.(*walletvault.Service); ok && svc != nil {
		return svc, nil
	}
	svc, err := walletvault.NewService(s.dir, s.now)
	if err != nil {
		return nil, err
	}
	if s.providerFixture {
		svc.SetFastKDFForTests()
	}
	s.walletVaultPtr = svc
	return svc, nil
}

type solanaRPCStatus struct {
	MainnetConfigured bool   `json:"mainnetConfigured"`
	MainnetHost       string `json:"mainnetHost,omitempty"`
	DevnetConfigured  bool   `json:"devnetConfigured"`
	DevnetHost        string `json:"devnetHost,omitempty"`
}

func (s *Store) solanaRPCStatusView() solanaRPCStatus {
	var out solanaRPCStatus
	if _, host, ok := s.resolveSolanaRPC("mainnet-beta"); ok {
		out.MainnetConfigured = true
		out.MainnetHost = host
	}
	if _, host, ok := s.resolveSolanaRPC("devnet"); ok {
		out.DevnetConfigured = true
		out.DevnetHost = host
	}
	return out
}

func (h *HTTPHandler) walletsV2API(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "wallets-v2", 120) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.walletVaultService()
	if err != nil {
		writeError(w, 500, "wallet vault unavailable")
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/wallets"), "/")
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	switch {
	case rel == "":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		list, err := svc.ListWallets()
		if err != nil {
			writeError(w, 500, "wallets unavailable")
			return
		}
		writeJSON(w, 200, map[string]any{
			"wallets": list,
			"count":   len(list),
			"rpc":     h.store.solanaRPCStatusView(),
		})

	case rel == "create":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			Name       string `json:"name"`
			Role       string `json:"role"`
			Words      int    `json:"words"`
			Passphrase string `json:"passphrase"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 16*1024, &in) {
			return
		}
		rec, mnemonic, err := svc.CreateSolanaWallet(in.Name, in.Role, in.Words, in.Passphrase)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{
			"wallet":    rec,
			"mnemonic":  mnemonic,
			"publicKey": rec.PublicKey,
		})

	case rel == "import":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in walletvault.ImportInput
		if !httpapi.DecodeStrictJSON(w, r, 16*1024, &in) {
			return
		}
		rec, err := svc.ImportWallet(in)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"wallet": rec,
		})

	case strings.HasSuffix(rel, "/export"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		id := strings.TrimSuffix(rel, "/export")
		var in struct {
			Passphrase string `json:"passphrase"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 8*1024, &in) {
			return
		}
		exp, err := svc.ExportSecret(id, in.Passphrase)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeError(w, 404, "wallet not found")
				return
			}
			if errors.Is(err, walletvault.ErrWrongPassphrase) {
				writeError(w, 403, err.Error())
				return
			}
			writeError(w, 400, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, exp)

	case strings.HasSuffix(rel, "/balance"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(rel, "/balance")
		walletRec, found, err := svc.GetWallet(id)
		if err != nil {
			writeError(w, 500, "wallet lookup failed")
			return
		}
		if !found {
			writeError(w, 404, "wallet not found")
			return
		}
		var in struct {
			Cluster string `json:"cluster"`
		}
		if r.ContentLength != 0 && strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			if !httpapi.DecodeStrictJSON(w, r, 8*1024, &in) {
				return
			}
		}
		cluster := strings.TrimSpace(in.Cluster)
		if cluster == "" {
			cluster = "mainnet-beta"
		}
		rawRPC, hostOnly, ok := h.store.resolveSolanaRPC(cluster)
		if !ok {
			writeJSON(w, 200, map[string]any{
				"rpcRequired": true,
				"cluster":     cluster,
				"publicKey":   walletRec.PublicKey,
			})
			return
		}
		if h.store.providerFixture && h.store.marketDataTransport == nil {
			writeJSON(w, 200, map[string]any{
				"rpcRequired": false,
				"cluster":     cluster,
				"rpcHost":     hostOnly,
				"publicKey":   walletRec.PublicKey,
				"lamports":    uint64(1500000000),
				"sol":         1.5,
				"latencyMs":   3,
			})
			return
		}
		start := time.Now()
		body := `{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["` + walletRec.PublicKey + `"]}`
		transport := http.DefaultTransport
		if h.store.marketDataTransport != nil {
			transport = h.store.marketDataTransport
		}
		client := &http.Client{
			Transport: transport,
			Timeout:   8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("redirects blocked")
			},
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, rawRPC, strings.NewReader(body))
		if reqErr != nil {
			writeError(w, 400, "invalid configured RPC endpoint")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := client.Do(req)
		if doErr != nil {
			writeError(w, 502, "RPC request failed")
			return
		}
		rawResp, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		_ = resp.Body.Close()
		var rpcOut struct {
			Result *struct {
				Value uint64 `json:"value"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rawResp, &rpcOut); err != nil || rpcOut.Result == nil {
			errMsg := "unexpected RPC response"
			if rpcOut.Error != nil && rpcOut.Error.Message != "" {
				errMsg = rpcOut.Error.Message
			}
			writeError(w, 502, errMsg)
			return
		}
		lamports := rpcOut.Result.Value
		writeJSON(w, 200, map[string]any{
			"rpcRequired": false,
			"cluster":     cluster,
			"rpcHost":     hostOnly,
			"publicKey":   walletRec.PublicKey,
			"lamports":    lamports,
			"sol":         float64(lamports) / 1e9,
			"latencyMs":   time.Since(start).Milliseconds(),
		})

	case !strings.Contains(rel, "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		deleted, err := svc.DeleteWallet(rel)
		if err != nil {
			writeError(w, 500, "delete failed")
			return
		}
		if !deleted {
			writeError(w, 404, "wallet not found")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true, "id": rel})

	default:
		writeError(w, 404, "unknown wallets operation")
	}
}
