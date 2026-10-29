package core

// Go-test binary only. Shipping runtime has no admission/CA or owner override.
import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

func TestWalletBrowserFixtureDriver(t *testing.T) {
	if os.Getenv("FASED_WALLET_BROWSER_FIXTURE") != "1" {
		t.Skip("explicit disposable driver only")
	}
	root, err := filepath.Abs(os.Getenv("FASED_WALLET_FIXTURE_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	base, _ := filepath.Abs("../../.evidence/b5-fixtures")
	if !strings.HasPrefix(root, base+string(os.PathSeparator)) {
		t.Fatal("unregistered fixture root")
	}
	if b, e := os.ReadFile(filepath.Join(root, "disposable.json")); e != nil || string(b) != "unfunded-reconstructible-wallet-fixture\n" {
		t.Fatal("missing declaration")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Serve the already-built exact UI files without making every core unit
	// test depend on a frontend build or embedding its bytes in the test binary.
	server, err := Start(ctx, filepath.Join(root, "core"), "127.0.0.1:0", "", os.DirFS("../../dist"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	s := server.Store
	s.rpcAdmission = true
	s.custodyNetwork = "devnet"
	s.fixture = func(_ context.Context, r signer.Request) (signer.Response, error) {
		if r.Action != "status" {
			t.Errorf("unexpected signer mutation %s", r.Action)
			return signer.Response{}, signer.ErrUnavailable
		}
		b, e := ReadPrivateFile(filepath.Join(root, "public-status.json"))
		if os.IsNotExist(e) {
			return signer.Response{Status: signer.Status{FixtureOnly: true, Locked: true}}, nil
		}
		if e != nil {
			return signer.Response{}, e
		}
		var status signer.Status
		if e = json.Unmarshal(b, &status); e != nil {
			return signer.Response{}, e
		}
		return signer.Response{Status: status}, nil
	}
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			w.WriteHeader(400)
			return
		}
		result := solanaGenesis("devnet")
		if q.Method == "getHealth" {
			result = "ok"
		} else if q.Method != "getGenesisHash" {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": q.ID, "result": result})
	}))
	defer tlsServer.Close()
	rt := newPublicRPCTransport()
	rt.enabled = true
	roots := x509.NewCertPool()
	roots.AddCert(tlsServer.Certificate())
	rt.transport.TLSClientConfig.RootCAs = roots
	rt.transport.TLSClientConfig.ServerName = "example.com"
	rt.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialPublicRPC(ctx, network, address, func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}, func(ctx context.Context, network, pinned string) (net.Conn, error) {
			if pinned != "8.8.8.8:443" {
				return nil, signer.ErrUnavailable
			}
			return (&net.Dialer{}).DialContext(ctx, network, tlsServer.Listener.Addr().String())
		})
	}
	s.rpcCheckFixture = func(ctx context.Context, in RPCCheckRequest) (RPCCheckResult, error) {
		v, err := checkPublicRPC(ctx, in, rt)
		// Correctly label controlled TLS bytes, never public-chain acceptance.
		if err == nil {
			v.FixtureOnly = true
			v.LiveRPC = false
		}
		return v, err
	}
	if err = WritePrivateJSON(filepath.Join(root, "ready.json"), map[string]string{"url": server.URL}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	select {
	case <-ctx.Done():
	case err = <-done:
		if err != nil {
			t.Error(err)
		}
	}
}
