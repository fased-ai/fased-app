package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Only test binaries contain endpoint/CA injection. The actual request keeps
// its official URL and Host; only its TCP destination and test certificate name
// are replaced. No production constructor or configuration accepts these values.
func newOpenAIHTTPSFixture(endpoint string, roots *x509.CertPool) (http.RoundTripper, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || roots == nil || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || u.Port() == "" {
		return nil, errors.New("explicit trusted loopback HTTPS fixture required")
	}
	tr := newOpenAITransport()
	tr.enabled = true
	tr.wire.TLSClientConfig.RootCAs = roots.Clone()
	tr.wire.TLSClientConfig.ServerName = u.Hostname()
	tr.wire.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "api.openai.com:443" {
			return nil, errors.New("unexpected origin")
		}
		return (&net.Dialer{}).DialContext(ctx, network, u.Host)
	}
	return testOpenAITransport{tr}, nil
}

type testOpenAITransport struct{ next *openAITransport }

func (t testOpenAITransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fased-test-") {
		return nil, errors.New("test credential required")
	}
	return t.next.RoundTrip(r)
}

func TestOpenAIProductionTransportDisabled(t *testing.T) {
	tr := newOpenAITransport()
	tr.wire.DialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("disabled transport dialed")
		return nil, nil
	}
	req, _ := http.NewRequest("POST", "https://api.openai.com/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("production dispatch enabled")
	}
	if tr.wire.Proxy != nil || tr.wire.TLSClientConfig.MinVersion != tls.VersionTLS12 || !tr.wire.DisableKeepAlives {
		t.Fatal("unsafe transport defaults")
	}
}

func TestOpenAITransportRejectsBeforeDial(t *testing.T) {
	tr := newOpenAITransport()
	tr.enabled = true
	tr.wire.DialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("rejected request dialed")
		return nil, nil
	}
	for _, target := range []string{"http://api.openai.com/v1/responses", "https://elsewhere.invalid/v1/responses", "https://api.openai.com/v1/responses?secret=x", "https://api.openai.com/v1/files", "https://api.openai.com/v1/%72esponses"} {
		r, _ := http.NewRequest("POST", target, nil)
		r.Header.Set("Authorization", "Bearer "+testAPIKey)
		if _, err := tr.RoundTrip(r); err == nil {
			t.Fatal("accepted", target)
		}
	}
	r, _ := http.NewRequest("POST", "https://api.openai.com/v1/responses", nil)
	if _, err := tr.RoundTrip(r); err == nil {
		t.Fatal("missing auth accepted")
	}
	r.Header.Set("Authorization", "Bearer "+testAPIKey)
	r.Host = "elsewhere.invalid"
	if _, err := tr.RoundTrip(r); err == nil {
		t.Fatal("host override accepted")
	}
}

func TestOpenAITransportChatCompletion(t *testing.T) {
	s := fixtureStore(t)
	connection := saveTestConnection(t, s)
	v, permission := chatPermission(t, s, connection.Ref)
	v = chatSend(t, s, v, permission, "Summarize my reminder")
	calls := 0
	rt := httpsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Host != "api.openai.com" || r.Header.Get("Authorization") != "Bearer "+testAPIKey {
			t.Error("origin or credential changed")
		}
		canonical := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = "https", "api.openai.com"
		canonical.URL = &u
		response, err := (fixtureAITransport{}).RoundTrip(canonical)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		_, _ = io.Copy(w, response.Body)
	})
	if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	got, err := s.Agents(t.Context(), v.ProfileID, "")
	if err != nil || len(got.AIRequests) != 1 || got.AIRequests[0].State != "completed" || got.AIRequests[0].Answer == "" || got.AIRequests[0].SpentMicrousd <= 0 {
		t.Fatal("chat completion missing", err)
	}
	if err := s.advanceAgentAI(t.Context(), rt, true); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("replayed completion", calls)
	}
	s.lockAIConnection()
	if s.aiConnectionValid(connection.Ref) {
		t.Fatal("lock did not revoke")
	}
}
