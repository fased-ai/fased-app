package onchaintest

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/fased-ai/fased-app/internal/onchain"
)

// HTTPS is a disposable, in-process test dependency. It never contacts a public
// provider and exposes no instance configuration or runtime CA/endpoint override.
type HTTPS struct {
	Transport  onchain.ControlledTransport
	Chain      *Chain
	RPCOrigin  string
	RPCKey     string
	JupiterKey string
}
type HTTPSTesting interface {
	Helper()
	Cleanup(func())
	Error(...any)
	Fatal(...any)
}

func NewHTTPS(t HTTPSTesting, origin string) *HTTPS {
	t.Helper()
	switch origin {
	case "https://mainnet.helius-rpc.com", "https://devnet.helius-rpc.com", "https://solana-mainnet.g.alchemy.com", "https://solana-devnet.g.alchemy.com":
	default:
		t.Fatal("controlled HTTPS fixture origin refused")
		return nil
	}
	fixture := &HTTPS{Chain: &Chain{WrongNetwork: strings.Contains(origin, "devnet")}, RPCOrigin: origin, RPCKey: strings.Repeat("r", 32), JupiterKey: "fased-test-onchain"}
	admitted, _ := url.Parse(origin)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := r.Clone(r.Context())
		u := *r.URL
		request.URL = &u
		u.Scheme = "https"
		u.Host = r.Host
		if r.Host == "api.jup.ag" {
			if r.Method != http.MethodGet || r.URL.Path != "/swap/v2/build" || r.Header.Get("x-api-key") != fixture.JupiterKey || r.URL.Query().Get("api-key") != "" {
				t.Error("controlled Jupiter credential isolation failed")
				w.WriteHeader(http.StatusForbidden)
				return
			}
		} else {
			helius := strings.Contains(admitted.Host, "helius")
			good := (helius && u.Path == "/" && u.RawQuery == "api-key="+fixture.RPCKey) || (!helius && u.Path == "/v2/"+fixture.RPCKey && u.RawQuery == "")
			if r.Method != http.MethodPost || r.Host != admitted.Host || !good || r.Header.Get("x-api-key") != "" {
				t.Error("controlled RPC credential isolation failed")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			u.Host = "rpc.fased-fixture.invalid"
			u.Path = ""
			u.RawQuery = ""
		}
		response, err := fixture.Chain.RoundTrip(request)
		if err != nil {
			t.Error("controlled chain refused request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	client := server.Client()
	target, _ := url.Parse(server.URL)
	fixture.Transport = &httpsTransport{target: target, next: client.Transport, rpcHost: admitted.Host}
	t.Cleanup(func() { client.CloseIdleConnections(); server.Close() })
	return fixture
}

// Requests returns only credential-free method names, copied under Chain's lock.
func (f *HTTPS) Requests() []string {
	f.Chain.mu.Lock()
	defer f.Chain.mu.Unlock()
	return append([]string(nil), f.Chain.Calls...)
}

type httpsTransport struct {
	target  *url.URL
	next    http.RoundTripper
	rpcHost string
}

func (*httpsTransport) ControlledFixture() bool { return true }
func (t *httpsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r == nil || r.URL == nil || (r.URL.Host != t.rpcHost && r.URL.Host != "api.jup.ag") || r.URL.Scheme != "https" {
		return nil, errors.New("controlled HTTPS origin refused")
	}
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	u.Scheme = t.target.Scheme
	u.Host = t.target.Host
	return t.next.RoundTrip(request)
}
