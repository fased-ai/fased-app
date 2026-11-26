package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/custody"
	"github.com/fased-ai/fased-app/internal/httpapi"
)

// ConfigureCustodySocket sets the external isolated signer Unix domain socket so core
// never initializes an in-process signer-vault directory inside s.dir.
func (s *Store) ConfigureCustodySocket(sockPath string) {
	if s == nil {
		return
	}
	s.custodyMu.Lock()
	defer s.custodyMu.Unlock()
	s.custodySocketPath = strings.TrimSpace(sockPath)
}

func (s *Store) custodyBackend() (custody.Backend, error) {
	if s == nil {
		return nil, errors.New("workspace store unavailable")
	}
	s.custodyMu.Lock()
	sockPath := strings.TrimSpace(s.custodySocketPath)
	if sockPath != "" {
		if s.custodyClient != nil {
			c := s.custodyClient
			s.custodyMu.Unlock()
			return c, nil
		}
		client, err := custody.NewSocketClient(sockPath)
		if err != nil {
			s.custodyMu.Unlock()
			return nil, err
		}
		s.custodyClient = client
		s.custodyMu.Unlock()
		return client, nil
	}
	s.custodyMu.Unlock()
	return s.custodyService()
}

func (s *Store) custodyService() (*custody.Service, error) {
	if s == nil || s.dir == "" {
		return nil, errors.New("workspace store unavailable")
	}
	s.custodyMu.Lock()
	defer s.custodyMu.Unlock()
	if s.custodySvc != nil {
		return s.custodySvc, nil
	}
	svc, err := custody.NewService(custody.Config{
		DataDir:      s.dir,
		FixtureMode:  s.providerFixture,
		FastKDF:      s.providerFixture,
		RPCTransport: s.marketDataTransport,
		Now:          s.now,
	})
	if err != nil {
		return nil, err
	}
	if !s.providerFixture {
		if sock, sErr := custody.StartSocketServer(svc); sErr == nil {
			s.custodySock = sock
		}
	}
	s.custodySvc = svc
	return svc, nil
}

func (s *Store) loadedCustodyService() *custody.Service {
	if s == nil {
		return nil
	}
	s.custodyMu.Lock()
	defer s.custodyMu.Unlock()
	return s.custodySvc
}

// hasNativeSigningWallet reports whether any non-watch native signing wallet exists in custody
// without lazily initializing a new signer-vault directory when none exists yet.
func (s *Store) hasNativeSigningWallet(ctx context.Context) bool {
	if s == nil {
		return false
	}
	s.custodyMu.Lock()
	sockPath := strings.TrimSpace(s.custodySocketPath)
	loaded := s.custodySvc != nil || s.custodyClient != nil
	s.custodyMu.Unlock()
	if !loaded && sockPath == "" {
		if s.dir == "" {
			return false
		}
		if _, err := os.Stat(filepath.Join(s.dir, "signer-vault", "custody.sqlite")); err != nil {
			return false
		}
	}
	cs, err := s.custodyBackend()
	if err != nil || cs == nil {
		return false
	}
	wallets, err := cs.ListWallets(ctx)
	if err != nil {
		return false
	}
	for _, w := range wallets {
		if w.Custody == custody.CustodyNativeSigner {
			return true
		}
	}
	return false
}

func writeCustodyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, custody.ErrWalletNotFound), errors.Is(err, custody.ErrReviewNotFound), errors.Is(err, custody.ErrLedgerNotFound):
		writeError(w, 404, err.Error())
	case errors.Is(err, custody.ErrOwnerAuthRequired):
		writeAuthCodeError(w, 403, err.Error(), "step_up_required")
	case errors.Is(err, custody.ErrWrongPassword), errors.Is(err, custody.ErrPolicyDenied), errors.Is(err, custody.ErrRoleDenied), errors.Is(err, custody.ErrTxTampered):
		writeError(w, 403, err.Error())
	case errors.Is(err, custody.ErrPolicyConflict), errors.Is(err, custody.ErrAmbiguousState):
		writeError(w, 409, err.Error())
	default:
		writeError(w, 400, err.Error())
	}
}

// walletsV2API exposes hardened custody lifecycle, fail-closed policy management, per-wallet
// genesis-pinned RPC, hardware/Wallet-Standard verification, and Argon2id encrypted backup.
// Plaintext private keys and mnemonics NEVER cross HTTP JSON in either direction.
func (h *HTTPHandler) walletsV2API(w http.ResponseWriter, r *http.Request, token string) {
	if !h.allowRate(r, "wallets-v2", 120) {
		writeError(w, 429, ErrRateLimited.Error())
		return
	}
	svc, err := h.store.custodyBackend()
	if err != nil {
		writeError(w, 500, "custody signer unavailable")
		return
	}
	rel := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/wallets"), "/")
	ctx := r.Context()

	switch {
	case rel == "":
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		list, err := svc.ListWallets(ctx)
		if err != nil {
			writeError(w, 500, "wallets unavailable")
			return
		}
		sec, secErr := svc.SecurityStatus(ctx)
		auditCount, auditErr := svc.VerifyAuditChain(ctx)
		ownerPasswordSet, _ := h.store.hasPassword(ctx)
		sockReady := false
		if secErr == nil && sec.SocketPath != "" {
			sockReady, _ = custody.ProbeSocketAt(ctx, sec.SocketPath)
		} else {
			sockReady = custody.ProbeSocket(ctx, h.store.dir)
		}
		writeJSON(w, 200, map[string]any{
			"wallets":            list,
			"count":              len(list),
			"socketReady":        sockReady,
			"signerPresent":      true,
			"security":           sec,
			"auditChainCount":    auditCount,
			"auditChainVerified": auditErr == nil,
			"ownerPasswordSet":   ownerPasswordSet,
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
			Name   string `json:"name"`
			Role   string `json:"role"`
			RPCURL string `json:"rpcUrl"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 16*1024, &in) {
			return
		}
		rec, err := svc.CreateNativeWallet(ctx, in.Name, in.Role, in.RPCURL)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"wallet": rec})

	case rel == "attach":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			Custody   string `json:"custody"` // hardware_standard | watch_only
			Name      string `json:"name"`
			Role      string `json:"role"`
			PublicKey string `json:"publicKey"`
			RPCURL    string `json:"rpcUrl"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 16*1024, &in) {
			return
		}
		rec, err := svc.AttachExternalWallet(ctx, in.Custody, in.Name, in.Role, in.PublicKey, in.RPCURL)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"wallet": rec})

	case rel == "restore":
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		var in struct {
			Archive    json.RawMessage `json:"archive"`
			Passphrase string          `json:"passphrase"`
			Name       string          `json:"name"`
			Role       string          `json:"role"`
			RPCURL     string          `json:"rpcUrl"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 64*1024, &in) {
			return
		}
		authCtx, ok := h.requireStepUpCtx(w, r, token, "restore", "*")
		if !ok {
			return
		}
		rec, err := svc.ImportEncryptedRecovery(authCtx, in.Archive, in.Passphrase, in.Name, in.Role, in.RPCURL)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"wallet": rec})

	case strings.HasSuffix(rel, "/policy"):
		id := strings.TrimSuffix(rel, "/policy")
		switch r.Method {
		case http.MethodGet:
			rec, err := svc.GetWallet(ctx, id)
			if err != nil {
				writeCustodyError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"policy": rec.Policy, "wallet": rec})
		case http.MethodPut:
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in struct {
				Policy             custody.WalletPolicy `json:"policy"`
				ConfirmLoosen      bool                 `json:"confirmLoosen"`
				ExpectedPolicyHash string               `json:"expectedPolicyHash"`
			}
			if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
				return
			}
			cur, gErr := svc.GetWallet(ctx, id)
			if gErr != nil {
				writeCustodyError(w, gErr)
				return
			}
			callCtx := ctx
			if !custody.IsPolicyTighterOrEqual(cur.Policy, in.Policy) {
				var ok bool
				callCtx, ok = h.requireStepUpCtx(w, r, token, "policy", id)
				if !ok {
					return
				}
			}
			rec, err := svc.UpdatePolicy(callCtx, id, in.Policy, in.ConfirmLoosen, in.ExpectedPolicyHash, "owner")
			if err != nil {
				writeCustodyError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"wallet": rec})
		default:
			writeError(w, 405, "method not allowed")
		}

	case strings.HasSuffix(rel, "/freeze"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(rel, "/freeze")
		var in struct {
			Frozen bool   `json:"frozen"`
			Reason string `json:"reason"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 8*1024, &in) {
			return
		}
		callCtx := ctx
		if !in.Frozen {
			var ok bool
			callCtx, ok = h.requireStepUpCtx(w, r, token, "unfreeze", id)
			if !ok {
				return
			}
		}
		rec, err := svc.SetFreeze(callCtx, id, in.Frozen, in.Reason, "owner")
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"wallet": rec})

	case strings.HasSuffix(rel, "/connections"):
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		id := strings.TrimSuffix(rel, "/connections")
		var in custody.WalletConnections
		if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
			return
		}
		authCtx, ok := h.requireStepUpCtx(w, r, token, "connections", id)
		if !ok {
			return
		}
		rec, err := svc.UpdateConnections(authCtx, id, in, "owner")
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"wallet": rec})

	case strings.HasSuffix(rel, "/rpc"):
		if r.Method != http.MethodPut {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		id := strings.TrimSuffix(rel, "/rpc")
		var in struct {
			PrimaryRPCURL  string `json:"primaryRpcUrl"`
			FallbackRPCURL string `json:"fallbackRpcUrl"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 16*1024, &in) {
			return
		}
		authCtx, ok := h.requireStepUpCtx(w, r, token, "rpc", id)
		if !ok {
			return
		}
		rec, err := svc.SetWalletRPC(authCtx, id, in.PrimaryRPCURL, in.FallbackRPCURL, "owner")
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"wallet": rec})

	case strings.HasSuffix(rel, "/balance"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(rel, "/balance")
		bal, err := svc.FetchBalance(ctx, id)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, bal)

	case strings.HasSuffix(rel, "/backup"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		id := strings.TrimSuffix(rel, "/backup")
		var in struct {
			Passphrase string `json:"passphrase"`
		}
		if !httpapi.DecodeStrictJSON(w, r, 8*1024, &in) {
			return
		}
		authCtx, ok := h.requireStepUpCtx(w, r, token, "backup", id)
		if !ok {
			return
		}
		archiveBytes, fp, err := svc.ExportEncryptedRecovery(authCtx, id, in.Passphrase, "owner")
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		sum := sha256.Sum256(archiveBytes)
		_ = hex.EncodeToString(sum[:])
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, map[string]any{
			"archive":     json.RawMessage(archiveBytes),
			"fingerprint": fp,
			"filename":    id + ".fased-recovery.json",
		})

	case strings.HasSuffix(rel, "/activity"):
		if r.Method != http.MethodGet {
			writeError(w, 405, "method not allowed")
			return
		}
		id := strings.TrimSuffix(rel, "/activity")
		reviews, ledger, audit, err := svc.ListActivity(ctx, id)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{
			"reviews": reviews,
			"ledger":  ledger,
			"audit":   audit,
		})

	case strings.HasSuffix(rel, "/reviews"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		id := strings.TrimSuffix(rel, "/reviews")
		var in custody.PrepareReviewRequest
		if !httpapi.DecodeStrictJSON(w, r, 16*1024, &in) {
			return
		}
		in.WalletID = id
		rev, err := svc.PrepareReview(ctx, in)
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"review": rev})

	case strings.Contains(rel, "/reviews/"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		parts := strings.Split(rel, "/")
		if len(parts) != 4 || parts[1] != "reviews" {
			writeError(w, 404, "unknown review route")
			return
		}
		walletID, reviewID, action := parts[0], parts[2], parts[3]
		switch action {
		case "execute":
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			authCtx, ok := h.requireStepUpCtx(w, r, token, "execute", walletID)
			if !ok {
				return
			}
			entry, err := svc.ExecuteNativeReview(authCtx, walletID, reviewID, "owner")
			if err != nil {
				if entry.ID != "" {
					writeJSON(w, 202, map[string]any{"entry": entry, "error": err.Error()})
					return
				}
				writeCustodyError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"entry": entry})
		case "submit-signed":
			if !h.v2Mutation(w, r, token, true) {
				return
			}
			var in struct {
				SignedTxBase64 string `json:"signedTxBase64"`
			}
			if !httpapi.DecodeStrictJSON(w, r, 32*1024, &in) {
				return
			}
			entry, err := svc.SubmitHardwareSignedReview(ctx, walletID, reviewID, in.SignedTxBase64, "hardware-device")
			if err != nil {
				if entry.ID != "" {
					writeJSON(w, 202, map[string]any{"entry": entry, "error": err.Error()})
					return
				}
				writeCustodyError(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"entry": entry})
		default:
			writeError(w, 404, "unknown review action")
		}

	case strings.Contains(rel, "/ledger/") && strings.HasSuffix(rel, "/reconcile"):
		if r.Method != http.MethodPost {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, false) {
			return
		}
		parts := strings.Split(rel, "/")
		if len(parts) != 4 || parts[1] != "ledger" || parts[3] != "reconcile" {
			writeError(w, 404, "unknown ledger route")
			return
		}
		entry, err := svc.ReconcileLedgerEntry(ctx, parts[0], parts[2])
		if err != nil {
			writeCustodyError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"entry": entry})

	case !strings.Contains(rel, "/"):
		if r.Method != http.MethodDelete {
			writeError(w, 405, "method not allowed")
			return
		}
		if !h.v2Mutation(w, r, token, true) {
			return
		}
		authCtx, ok := h.requireStepUpCtx(w, r, token, "delete", rel)
		if !ok {
			return
		}
		deleted, err := svc.DeleteWallet(authCtx, rel)
		if err != nil {
			writeCustodyError(w, err)
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
