package custody

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const socketName = "app.sock"

// SocketServer exposes ONLY typed custody operations over <dataDir>/signer-vault/app.sock (0o600).
// Notice raw private-key export has NO HTTP or socket route here—keeping raw key material strictly
// isolated inside the signer boundary.
type SocketServer struct {
	svc      *Service
	listener net.Listener
	httpSrv  *http.Server
}

// StartSocketServer starts the isolated Unix socket custody server inside signer-vault/.
func StartSocketServer(svc *Service) (*SocketServer, error) {
	if svc == nil {
		return nil, errors.New("custody service required")
	}
	sockPath := filepath.Join(svc.Dir(), socketName)
	_ = os.Remove(sockPath)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(sockPath, 0o600)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.ListWallets(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"wallets": len(list),
			"socket":  socketName,
		})
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return &SocketServer{svc: svc, listener: ln, httpSrv: srv}, nil
}

// SocketPath returns the Unix socket path.
func (s *SocketServer) SocketPath() string {
	if s == nil || s.svc == nil {
		return ""
	}
	return filepath.Join(s.svc.Dir(), socketName)
}

// Close shuts down the Unix socket cleanly.
func (s *SocketServer) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.httpSrv != nil {
		_ = s.httpSrv.Shutdown(ctx)
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	return nil
}

// ProbeSocket checks whether app.sock is responsive on local/VPS runtime.
func ProbeSocket(ctx context.Context, dataDir string) bool {
	sockPath := filepath.Join(strings.TrimSpace(dataDir), vaultDirName, socketName)
	if _, err := os.Stat(sockPath); err != nil {
		return false
	}
	client := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(dCtx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(dCtx, "unix", sockPath)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://signer.local/v1/status", nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
