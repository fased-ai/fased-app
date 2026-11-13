package privateaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

// BeforeInstall proves a real private browser can reach the enrolled VPS. No
// Fased payload, database, service or wallet exists at this stage. The temporary
// owned endpoint is removed conditionally; foreign Serve policy is never reset.
func (m Manager) BeforeInstall(ctx context.Context, dir string, p Plan, out io.Writer) (result tailnet.Identity, resultErr error) {
	var empty tailnet.Identity
	if _, _, err := routeParts(p); err != nil {
		return empty, errors.New("Remote requires Tailscale before installation")
	}
	if err := m.finishPreflight(ctx, dir, p.Origin); err != nil {
		return empty, err
	}
	cfg, etag, err := m.config(ctx)
	if err != nil {
		return empty, err
	}
	state, err := routeState(cfg, p)
	if err != nil || state != "absent" {
		return empty, errors.New("this private endpoint is already owned; refusing to replace it")
	}
	probeDir, err := os.MkdirTemp("/tmp", "fased-private-")
	if err != nil {
		return empty, err
	}
	created, err := os.Lstat(probeDir)
	if err != nil {
		return empty, err
	}
	defer func() {
		if current, err := os.Lstat(probeDir); err == nil && os.SameFile(created, current) {
			_ = os.Remove(probeDir)
		}
	}()
	listener, err := tailnet.Listen(probeDir)
	if err != nil {
		return empty, err
	}
	defer listener.Close()
	nonce := make([]byte, 32)
	if _, err = rand.Read(nonce); err != nil {
		return empty, err
	}
	proof := base64.RawURLEncoding.EncodeToString(nonce)
	approved := make(chan tailnet.Identity, 1)
	server := &http.Server{Handler: setupProbe(p.Origin, proof, tailnet.Trusted, tailnet.WhoIs, approved), ConnContext: tailnet.ConnContext, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	defer server.Close()
	probe := p
	probe.Target = tailnet.Target(probeDir)
	owned, err := routeChange(cfg, probe, true)
	if err != nil {
		return empty, err
	}
	// Persist ownership before attempting the conditional mutation. A timeout can
	// mean that the daemon committed it; recovery must reconcile, never guess.
	receipt := preflightReceipt{Version: 1, Plan: probe}
	if err = core.WritePrivateJSON(filepath.Join(dir, "private-setup.json"), receipt); err != nil {
		return empty, err
	}
	defer func() {
		// UnixListener.Close unlinks its original socket path. Close it while the
		// private directory still exists, before removing that directory below.
		_ = server.Close()
		_ = listener.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, m.finishPreflight(cleanup, dir, p.Origin))
	}()
	// The route may survive a power cut. Persist its ownership record before
	// issuing the conditional Serve mutation so recovery can identify it.
	directory, err := os.Open(dir)
	if err != nil {
		return empty, err
	}
	if err = errors.Join(directory.Sync(), directory.Close()); err != nil {
		return empty, err
	}
	data, _ := json.Marshal(owned)
	if err = m.SetConfig(ctx, data, etag); err != nil {
		return empty, err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Fprintln(out, "Open this private setup link on your Tailscale-connected computer:")
	fmt.Fprintln(out, p.Origin+"/#"+proof)
	select {
	case id := <-approved:
		return id, nil
	case <-ctx.Done():
		return empty, errors.New("private setup was not confirmed; Fased was not deployed")
	case err := <-done:
		return empty, err
	}
}

func setupProbe(origin, proof string, trusted func(*http.Request) bool, resolve tailnet.Resolver, approved chan<- tailnet.Identity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if !trusted(r) || r.Header.Get("X-Forwarded-Host") != origin[len("https://"):] || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("Tailscale-Funnel-Request") != "" {
			http.Error(w, "Private Tailscale access required", 403)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			scriptHash := sha256.Sum256([]byte("fased-setup-script:" + proof))
			scriptNonce := hex.EncodeToString(scriptHash[:])
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+scriptNonce+"'; style-src 'nonce-"+scriptNonce+"'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
			fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Fased</title><style nonce="%[1]s">:root{color-scheme:dark;--bg:#17151b;--panel:#201e2b;--text:#efedf3;--line:#34313f}@media(prefers-color-scheme:light){:root{color-scheme:light;--bg:#f5f4f7;--panel:#fff;--text:#242130;--line:#e0dce8}}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:var(--bg);color:var(--text);font:16px Inter,system-ui,sans-serif}main{padding:1.75rem;border:1px solid var(--line);border-top:2px solid #f74869;border-radius:8px;background:var(--panel);width:calc(100vw - 3rem);max-width:28rem}h1{font-size:1.4rem;margin:0 0 1rem}p{line-height:1.5;margin:0}</style><main><h1>Fased</h1><p id="status">Connecting…</p></main><script nonce="%[1]s">const code=location.hash.slice(1);history.replaceState(null,'','/');fetch('/approve',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({code})}).then(r=>{if(!r.ok)throw Error();document.getElementById('status').textContent='Connected. Continue in your installation terminal.'}).catch(()=>document.getElementById('status').textContent='Reopen the private setup link.');</script></html>`, scriptNonce)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/approve" || r.Header.Get("Origin") != origin || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "Private setup request required", 403)
			return
		}
		var input struct {
			Code string `json:"code"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || subtle.ConstantTimeCompare([]byte(input.Code), []byte(proof)) != 1 {
			http.Error(w, "Reopen the setup link", 403)
			return
		}
		id, err := resolve(r.Context(), r.Header.Get("X-Forwarded-For"))
		if err != nil {
			http.Error(w, "Approve a personal Tailscale device", 403)
			return
		}
		w.WriteHeader(204)
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		select {
		case approved <- id:
		default:
			http.Error(w, "Setup already confirmed", 409)
		}
	})
}

// A Serve Unix target is accepted only at the dedicated ingress path, never the
// privileged core.sock. Paths remain within an absolute private state directory.
func validTarget(value string) bool {
	if len(value) > 5 && value[:5] == "unix:" {
		path := value[5:]
		return filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Base(path) == "tailscale.sock"
	}
	if len(value) > 7 && value[:7] == "http://" {
		_, err := target(value[7:])
		return err == nil
	}
	return false
}

type preflightReceipt struct {
	Version int  `json:"version"`
	Plan    Plan `json:"plan"`
}

func (m Manager) finishPreflight(ctx context.Context, dir, origin string) error {
	file := filepath.Join(dir, "private-setup.json")
	data, err := core.ReadPrivateFile(file)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var r preflightReceipt
	if json.Unmarshal(data, &r) != nil || r.Version != 1 || r.Plan.Mode != "tailscale" || r.Plan.Origin != origin || !strings.HasPrefix(r.Plan.Target, "unix:") {
		return errors.New("unknown private setup receipt; preserve it for recovery")
	}
	socket := strings.TrimPrefix(r.Plan.Target, "unix:")
	probeDir := filepath.Dir(socket)
	if !validTarget(r.Plan.Target) || filepath.Dir(probeDir) != "/tmp" || !strings.HasPrefix(filepath.Base(probeDir), "fased-private-") {
		return errors.New("foreign private setup path; preserve it")
	}
	probe, err := openPreflightDirectory(probeDir)
	if err != nil {
		return err
	}
	if probe != nil {
		defer probe.Close()
	}
	cfg, etag, err := m.config(ctx)
	if err != nil {
		return err
	}
	state, err := routeState(cfg, r.Plan)
	if err != nil {
		return errors.New("private setup route changed; preserve the receipt and inspect before deployment")
	}
	if state == "owned" {
		next, e := routeChange(cfg, r.Plan, false)
		if e != nil {
			return e
		}
		data, _ := json.Marshal(next)
		if err = m.SetConfig(ctx, data, etag); err != nil {
			return err
		}
		cfg, _, err = m.config(ctx)
		state, e = routeState(cfg, r.Plan)
		if err != nil || e != nil || state != "absent" {
			return errors.New("private setup cleanup not confirmed; no deployment allowed")
		}
	}
	if probe != nil {
		if err = removePreflightDirectory(probe, probeDir); err != nil {
			return err
		}
	}
	return os.Remove(file)
}
