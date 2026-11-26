package custody

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SocketServer exposes ONLY typed custody operations over <dataDir>/signer-vault/app.sock
// (or an explicit runtime socket path) guarded by OS SO_PEERCRED UID verification.
// Raw private keys are NEVER exposed across IPC.
type SocketServer struct {
	svc            *Service
	sockPath       string
	allowedPeerUID int
	isOwnerSocket  bool
	listener       net.Listener
	httpSrv        *http.Server
	mu             sync.Mutex
	closed         bool
}

// StartSocketServer starts the isolated Unix socket custody server inside signer-vault/ for the current UID.
func StartSocketServer(svc *Service) (*SocketServer, error) {
	return StartSocketServerAt(svc, "", os.Getuid(), 0o600)
}

// StartSocketServerAt starts the isolated app-facing Unix socket custody server at sockPath, enforcing
// OS peer UID verification (SO_PEERCRED on Linux), socket file permissions, and in-signer owner proofs
// for privileged operations.
func StartSocketServerAt(svc *Service, sockPath string, allowedPeerUID int, mode os.FileMode) (*SocketServer, error) {
	return startSocketServer(svc, sockPath, allowedPeerUID, mode, false)
}

// StartOwnerSocketServerAt starts a dedicated owner-only (0600) Unix socket for local/admin CLI operations
// including raw keypair import/export without direct SQLite file access.
func StartOwnerSocketServerAt(svc *Service, sockPath string, allowedPeerUID int, mode os.FileMode) (*SocketServer, error) {
	if mode == 0 {
		mode = 0o600
	}
	return startSocketServer(svc, sockPath, allowedPeerUID, mode, true)
}

func startSocketServer(svc *Service, sockPath string, allowedPeerUID int, mode os.FileMode, isOwnerSocket bool) (*SocketServer, error) {
	if svc == nil {
		return nil, errors.New("custody service required")
	}
	targetSock := strings.TrimSpace(sockPath)
	if targetSock == "" {
		name := "app.sock"
		if isOwnerSocket {
			name = "owner.sock"
		}
		targetSock = filepath.Join(svc.dir, name)
	}
	if mode == 0 {
		mode = 0o600
	}
	parent := filepath.Dir(targetSock)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return nil, err
	}
	_ = os.Remove(targetSock)
	rawLn, err := net.Listen("unix", targetSock)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(targetSock, mode)
	ln := newPeerCredListener(rawLn, allowedPeerUID)

	ss := &SocketServer{
		svc:            svc,
		sockPath:       targetSock,
		allowedPeerUID: allowedPeerUID,
		isOwnerSocket:  isOwnerSocket,
		listener:       ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", ss.handleStatus)
	mux.HandleFunc("/v1/audit/verify", ss.handleVerifyAudit)
	mux.HandleFunc("/v1/owner/status", ss.handleOwnerStatus)
	mux.HandleFunc("/v1/owner/configure", ss.handleOwnerConfigure)
	mux.HandleFunc("/v1/owner/authorize", ss.handleOwnerAuthorize)
	if isOwnerSocket {
		mux.HandleFunc("/v1/owner/import-raw", ss.handleOwnerImportRaw)
		mux.HandleFunc("/v1/owner/export-raw", ss.handleOwnerExportRaw)
	}
	mux.HandleFunc("/v1/wallets", ss.handleWallets)
	mux.HandleFunc("/v1/wallets/create", ss.handleCreateWallet)
	mux.HandleFunc("/v1/wallets/attach", ss.handleAttachWallet)
	mux.HandleFunc("/v1/wallets/resolve", ss.handleResolveWallet)
	mux.HandleFunc("/v1/wallets/restore", ss.handleRestoreWallet)
	mux.HandleFunc("/v1/wallets/", ss.handleWalletSubroute)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	ss.httpSrv = srv
	go func() {
		_ = srv.Serve(ln)
	}()
	return ss, nil
}

func (s *SocketServer) SocketPath() string {
	if s == nil {
		return ""
	}
	return s.sockPath
}

func (s *SocketServer) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	var err error
	if s.httpSrv != nil {
		err = s.httpSrv.Shutdown(ctx)
	}
	if s.sockPath != "" {
		_ = os.Remove(s.sockPath)
	}
	return err
}

// ProbeSocket verifies socket responsiveness using typed status IPC in dataDir/signer-vault/app.sock.
func ProbeSocket(ctx context.Context, dataDir string) bool {
	ok, _ := ProbeSocketAt(ctx, filepath.Join(dataDir, vaultDirName, "app.sock"))
	return ok
}

// ProbeSocketAt verifies socket responsiveness at an explicit Unix domain socket path.
func ProbeSocketAt(ctx context.Context, sockPath string) (bool, int) {
	tr := &http.Transport{
		DialContext: func(dCtx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(dCtx, "unix", sockPath)
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://fased-signer/v1/status", nil)
	if err != nil {
		return false, 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()
	var body struct {
		OK      bool `json:"ok"`
		Wallets int  `json:"wallets"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode == 200 && body.OK, body.Wallets
}

func socketErrCodeAndStatus(err error) (string, int) {
	switch {
	case errors.Is(err, ErrWalletNotFound):
		return "wallet_not_found", http.StatusNotFound
	case errors.Is(err, ErrReviewNotFound):
		return "review_not_found", http.StatusNotFound
	case errors.Is(err, ErrLedgerNotFound):
		return "ledger_not_found", http.StatusNotFound
	case errors.Is(err, ErrPolicyConflict):
		return "policy_conflict", http.StatusConflict
	case errors.Is(err, ErrAmbiguousState):
		return "ambiguous_state", http.StatusConflict
	case errors.Is(err, ErrWrongPassword):
		return "wrong_password", http.StatusForbidden
	case errors.Is(err, ErrOwnerAuthRequired):
		return "owner_auth_required", http.StatusForbidden
	case errors.Is(err, ErrTxTampered):
		return "tx_tampered", http.StatusForbidden
	case errors.Is(err, ErrRoleDenied):
		return "role_denied", http.StatusForbidden
	case errors.Is(err, ErrPolicyDenied):
		return "policy_denied", http.StatusForbidden
	default:
		return "bad_request", http.StatusBadRequest
	}
}

func writeSockJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSockErr(w http.ResponseWriter, err error) {
	code, status := socketErrCodeAndStatus(err)
	writeSockJSON(w, status, map[string]string{
		"error": err.Error(),
		"code":  code,
	})
}

func decodeSockBody(r *http.Request, dst any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func (s *SocketServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	list, err := s.svc.ListWallets(r.Context())
	if err != nil {
		writeSockErr(w, err)
		return
	}
	sec, _ := s.svc.SecurityStatus(r.Context())
	sec.ProcessIsolated = true
	sec.SocketPath = s.sockPath
	sec.AllowedPeerUID = s.allowedPeerUID
	auditCount, auditErr := s.svc.VerifyAuditChain(r.Context())
	writeSockJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"wallets":            len(list),
		"security":           sec,
		"auditChainCount":    auditCount,
		"auditChainVerified": auditErr == nil,
	})
}

func (s *SocketServer) requireOwnerProof(w http.ResponseWriter, r *http.Request, action, walletID string) bool {
	if s.isOwnerSocket {
		return true
	}
	enrolled, err := s.svc.HasOwnerAuth(r.Context())
	if err != nil {
		writeSockErr(w, err)
		return false
	}
	if !enrolled && !s.svc.cfg.RequireOwnerAuth {
		return true
	}
	proof := strings.TrimSpace(r.Header.Get(ownerProofHeader))
	if err := s.svc.ConsumeOwnerProof(action, walletID, proof); err != nil {
		writeSockErr(w, err)
		return false
	}
	return true
}

func (s *SocketServer) handleOwnerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	has, err := s.svc.HasOwnerAuth(r.Context())
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, map[string]bool{
		"hasOwnerAuth": has,
		"enforced":     has || s.svc.cfg.RequireOwnerAuth,
	})
}

func (s *SocketServer) handleOwnerConfigure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeSockBody(r, &req); err != nil {
		writeSockErr(w, err)
		return
	}
	if err := s.svc.ConfigureOwnerAuth(r.Context(), req.Password); err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, map[string]bool{"ok": true, "hasOwnerAuth": true})
}

func (s *SocketServer) handleOwnerAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Password string `json:"password"`
		Action   string `json:"action"`
		WalletID string `json:"walletId"`
	}
	if err := decodeSockBody(r, &req); err != nil {
		writeSockErr(w, err)
		return
	}
	proof, exp, err := s.svc.AuthorizeOwnerAction(r.Context(), req.Password, req.Action, req.WalletID)
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, map[string]any{
		"proof":     proof,
		"expiresAt": exp.UTC().Unix(),
	})
}

func (s *SocketServer) handleOwnerImportRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		FilePath      string `json:"filePath"`
		Name          string `json:"name"`
		Role          string `json:"role"`
		PrimaryRPCURL string `json:"primaryRpcUrl"`
	}
	if err := decodeSockBody(r, &req); err != nil {
		writeSockErr(w, err)
		return
	}
	wallet, err := s.svc.ImportSolanaKeypairFile(r.Context(), req.FilePath, req.Name, req.Role, req.PrimaryRPCURL)
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, wallet)
}

func (s *SocketServer) handleOwnerExportRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		WalletID                    string `json:"walletId"`
		OutputPath                  string `json:"outputPath"`
		AcknowledgeCustodyReduction bool   `json:"acknowledgeCustodyReduction"`
	}
	if err := decodeSockBody(r, &req); err != nil {
		writeSockErr(w, err)
		return
	}
	if err := s.svc.ExportRawKeypairFile(r.Context(), req.WalletID, req.OutputPath, req.AcknowledgeCustodyReduction); err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, map[string]any{"output": req.OutputPath, "custodyReduced": true})
}

func (s *SocketServer) handleVerifyAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	count, err := s.svc.VerifyAuditChain(r.Context())
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, map[string]any{"count": count, "verified": true})
}

func (s *SocketServer) handleWallets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	list, err := s.svc.ListWallets(r.Context())
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, list)
}

func (s *SocketServer) handleCreateWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name          string `json:"name"`
		Role          string `json:"role"`
		PrimaryRPCURL string `json:"primaryRpcUrl"`
	}
	if err := decodeSockBody(r, &req); err != nil {
		writeSockErr(w, err)
		return
	}
	wallet, err := s.svc.CreateNativeWallet(r.Context(), req.Name, req.Role, req.PrimaryRPCURL)
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, map[string]any{"wallet": wallet})
}

func (s *SocketServer) handleAttachWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Custody       string `json:"custody"`
		Name          string `json:"name"`
		Role          string `json:"role"`
		PublicKey     string `json:"publicKey"`
		PrimaryRPCURL string `json:"primaryRpcUrl"`
	}
	if err := decodeSockBody(r, &req); err != nil {
		writeSockErr(w, err)
		return
	}
	wallet, err := s.svc.AttachExternalWallet(r.Context(), req.Custody, req.Name, req.Role, req.PublicKey, req.PrimaryRPCURL)
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, wallet)
}

func (s *SocketServer) handleResolveWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var sel WalletSelector
	if err := decodeSockBody(r, &sel); err != nil {
		writeSockErr(w, err)
		return
	}
	wallet, err := s.svc.ResolveWallet(r.Context(), sel)
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, wallet)
}

func (s *SocketServer) handleRestoreWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.requireOwnerProof(w, r, "restore", "*") {
		return
	}
	var req struct {
		Archive       json.RawMessage `json:"archive"`
		Passphrase    string          `json:"passphrase"`
		Name          string          `json:"name"`
		Role          string          `json:"role"`
		PrimaryRPCURL string          `json:"primaryRpcUrl"`
	}
	if err := decodeSockBody(r, &req); err != nil {
		writeSockErr(w, err)
		return
	}
	wallet, err := s.svc.ImportEncryptedRecovery(r.Context(), req.Archive, req.Passphrase, req.Name, req.Role, req.PrimaryRPCURL)
	if err != nil {
		writeSockErr(w, err)
		return
	}
	writeSockJSON(w, http.StatusOK, wallet)
}

func (s *SocketServer) handleWalletSubroute(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/v1/wallets/")
	parts := strings.Split(rel, "/")
	if len(parts) == 0 || parts[0] == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	walletID, err := url.PathUnescape(parts[0])
	if err != nil {
		writeSockErr(w, err)
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			wp, err := s.svc.GetWallet(r.Context(), walletID)
			if err != nil {
				writeSockErr(w, err)
				return
			}
			writeSockJSON(w, http.StatusOK, wp)
		case http.MethodDelete:
			if !s.requireOwnerProof(w, r, "delete", walletID) {
				return
			}
			deleted, err := s.svc.DeleteWallet(r.Context(), walletID)
			if err != nil {
				writeSockErr(w, err)
				return
			}
			writeSockJSON(w, http.StatusOK, map[string]bool{"deleted": deleted})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	switch parts[1] {
	case "policy":
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Policy             WalletPolicy `json:"policy"`
			ConfirmLoosen      bool         `json:"confirmLoosen"`
			PolicyAcknowledged bool         `json:"policyAcknowledged"`
			ExpectedPolicyHash string       `json:"expectedPolicyHash"`
			Actor              string       `json:"actor"`
		}
		if err := decodeSockBody(r, &req); err != nil {
			writeSockErr(w, err)
			return
		}
		loosen := req.ConfirmLoosen || req.PolicyAcknowledged
		cur, gErr := s.svc.GetWallet(r.Context(), walletID)
		if gErr != nil {
			writeSockErr(w, gErr)
			return
		}
		if !IsPolicyTighterOrEqual(cur.Policy, req.Policy) {
			if !s.requireOwnerProof(w, r, "policy", walletID) {
				return
			}
		}
		wp, err := s.svc.UpdatePolicy(r.Context(), walletID, req.Policy, loosen, req.ExpectedPolicyHash, req.Actor)
		if err != nil {
			writeSockErr(w, err)
			return
		}
		writeSockJSON(w, http.StatusOK, wp)
	case "freeze":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Frozen bool   `json:"frozen"`
			Reason string `json:"reason"`
			Actor  string `json:"actor"`
		}
		if err := decodeSockBody(r, &req); err != nil {
			writeSockErr(w, err)
			return
		}
		if !req.Frozen {
			if !s.requireOwnerProof(w, r, "unfreeze", walletID) {
				return
			}
		}
		wp, err := s.svc.SetFreeze(r.Context(), walletID, req.Frozen, req.Reason, req.Actor)
		if err != nil {
			writeSockErr(w, err)
			return
		}
		writeSockJSON(w, http.StatusOK, wp)
	case "connections":
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Connections WalletConnections `json:"connections"`
			Actor       string            `json:"actor"`
		}
		if err := decodeSockBody(r, &req); err != nil {
			writeSockErr(w, err)
			return
		}
		if !s.requireOwnerProof(w, r, "connections", walletID) {
			return
		}
		wp, err := s.svc.UpdateConnections(r.Context(), walletID, req.Connections, req.Actor)
		if err != nil {
			writeSockErr(w, err)
			return
		}
		writeSockJSON(w, http.StatusOK, wp)
	case "rpc":
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			PrimaryURL  string `json:"primaryUrl"`
			FallbackURL string `json:"fallbackUrl"`
			Actor       string `json:"actor"`
		}
		if err := decodeSockBody(r, &req); err != nil {
			writeSockErr(w, err)
			return
		}
		if !s.requireOwnerProof(w, r, "rpc", walletID) {
			return
		}
		wp, err := s.svc.SetWalletRPC(r.Context(), walletID, req.PrimaryURL, req.FallbackURL, req.Actor)
		if err != nil {
			writeSockErr(w, err)
			return
		}
		writeSockJSON(w, http.StatusOK, wp)
	case "balance":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		bal, err := s.svc.FetchBalance(r.Context(), walletID)
		if err != nil {
			writeSockErr(w, err)
			return
		}
		writeSockJSON(w, http.StatusOK, bal)
	case "activity":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		revs, led, aud, err := s.svc.ListActivity(r.Context(), walletID)
		if err != nil {
			writeSockErr(w, err)
			return
		}
		writeSockJSON(w, http.StatusOK, map[string]any{"reviews": revs, "ledger": led, "audit": aud})
	case "backup":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Passphrase string `json:"passphrase"`
			Actor      string `json:"actor"`
		}
		if err := decodeSockBody(r, &req); err != nil {
			writeSockErr(w, err)
			return
		}
		if !s.requireOwnerProof(w, r, "backup", walletID) {
			return
		}
		archive, fp, err := s.svc.ExportEncryptedRecovery(r.Context(), walletID, req.Passphrase, req.Actor)
		if err != nil {
			writeSockErr(w, err)
			return
		}
		writeSockJSON(w, http.StatusOK, map[string]any{"archive": json.RawMessage(archive), "fingerprint": fp})
	case "reviews":
		if len(parts) == 3 && parts[2] == "prepare" && r.Method == http.MethodPost {
			var req PrepareReviewRequest
			if err := decodeSockBody(r, &req); err != nil {
				writeSockErr(w, err)
				return
			}
			req.WalletID = walletID
			rev, err := s.svc.PrepareReview(r.Context(), req)
			if err != nil {
				writeSockErr(w, err)
				return
			}
			writeSockJSON(w, http.StatusOK, rev)
			return
		}
		if len(parts) == 4 && r.Method == http.MethodPost {
			revID, err := url.PathUnescape(parts[2])
			if err != nil {
				writeSockErr(w, err)
				return
			}
			switch parts[3] {
			case "execute":
				var req struct {
					Actor string `json:"actor"`
				}
				_ = decodeSockBody(r, &req)
				// Manual reviews, vault wallets, and owner/manual-trade executions MUST carry
				// an in-signer owner proof; only genuinely bounded-auto bot executions proceed on policy alone.
				needOwnerProof := true
				if wPub, wErr := s.svc.GetWallet(r.Context(), walletID); wErr == nil {
					actorClean := strings.TrimSpace(req.Actor)
					isAutoActor := actorClean == "trade-bot" || strings.HasPrefix(actorClean, "agent:")
					if isAutoActor && wPub.Role != RoleVault && wPub.Role != RoleMining && wPub.Policy.ApprovalMode == ApprovalBoundedAuto && wPub.Connections.AllowTradeBots {
						needOwnerProof = false
					}
				}
				if needOwnerProof {
					if !s.requireOwnerProof(w, r, "execute", walletID) {
						return
					}
				}
				entry, err := s.svc.ExecuteNativeReview(r.Context(), walletID, revID, req.Actor)
				if err != nil {
					writeSockErr(w, err)
					return
				}
				writeSockJSON(w, http.StatusOK, entry)
				return
			case "submit-signed":
				var req struct {
					SignedTxBase64 string `json:"signedTxBase64"`
					Actor          string `json:"actor"`
				}
				if err := decodeSockBody(r, &req); err != nil {
					writeSockErr(w, err)
					return
				}
				entry, err := s.svc.SubmitHardwareSignedReview(r.Context(), walletID, revID, req.SignedTxBase64, req.Actor)
				if err != nil {
					writeSockErr(w, err)
					return
				}
				writeSockJSON(w, http.StatusOK, entry)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case "ledger":
		if len(parts) == 4 && parts[3] == "reconcile" && r.Method == http.MethodPost {
			entryID, err := url.PathUnescape(parts[2])
			if err != nil {
				writeSockErr(w, err)
				return
			}
			entry, err := s.svc.ReconcileLedgerEntry(r.Context(), walletID, entryID)
			if err != nil {
				writeSockErr(w, err)
				return
			}
			writeSockJSON(w, http.StatusOK, entry)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}
