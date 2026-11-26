package core

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type clientAuthTransport func(*http.Request) (*http.Response, error)

func (f clientAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientAuthenticationPreservesSafeHTTPFailure(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const secret = "disposable-credential-in-untrusted-response"
			calls := 0
			client := Client{
				Endpoint: Endpoint{URL: clientAuthOrigin},
				Token:    "retained-session",
				CSRF:     "retained-csrf",
				HTTP: &http.Client{Transport: clientAuthTransport(func(request *http.Request) (*http.Response, error) {
					calls++
					if request.Method != http.MethodPost || request.URL.Path != "/api/v1/auth/login" || request.Header.Get("Origin") != clientAuthOrigin {
						t.Fatal("owner authentication request changed")
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(secret))}, nil
				})},
			}
			err := client.Login(t.Context(), "disposable-owner-password")
			if err == nil || errors.Is(err, ErrUnauthorized) != (status == http.StatusUnauthorized) {
				t.Fatalf("unexpected authentication classification for HTTP %d: %v", status, err)
			}
			if status != http.StatusUnauthorized && (!strings.Contains(err.Error(), http.StatusText(status)) || !strings.Contains(err.Error(), "HTTP "+strconv.Itoa(status))) {
				t.Fatalf("HTTP %d lost its safe status: %v", status, err)
			}
			if strings.Contains(err.Error(), secret) || errors.Is(err, ErrCoreUnavailable) {
				t.Fatal("server body or retryable classification escaped authentication")
			}
			if calls != 1 || client.Token != "retained-session" || client.CSRF != "retained-csrf" {
				t.Fatal("failed authentication retried or replaced session state")
			}
		})
	}
}

const clientAuthOrigin = "http://127.0.0.1:7332"

func TestClientAuthenticationSuccessStillAcceptsSession(t *testing.T) {
	t.Parallel()
	client := Client{
		Endpoint: Endpoint{URL: clientAuthOrigin},
		HTTP: &http.Client{Transport: clientAuthTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/api/v1/auth/claim" {
				t.Fatal("claim route changed")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"csrfToken":"fixture-csrf","sessionToken":"fixture-session"}`))}, nil
		})},
	}
	if err := client.Claim(t.Context(), "disposable-code", "disposable-owner-password"); err != nil {
		t.Fatal(err)
	}
	if client.Token != "fixture-session" || client.CSRF != "fixture-csrf" {
		t.Fatal("successful owner authentication lost its session")
	}
}

func TestRatePeerKeyExtractsRightmostIPFromMultiHopXFF(t *testing.T) {
	t.Parallel()
	h := &HTTPHandler{remoteProxyTrust: true, publicHost: "fased.example.com"}

	req1, _ := http.NewRequest(http.MethodPost, clientAuthOrigin, nil)
	req1.Host = "fased.example.com"
	req1.RemoteAddr = "127.0.0.1:4567"
	req1.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.9")
	if got := h.ratePeerKey(req1); got != "proxy:203.0.113.9" {
		t.Fatalf("expected rightmost comma-separated IP proxy:203.0.113.9, got %q", got)
	}

	req2, _ := http.NewRequest(http.MethodPost, clientAuthOrigin, nil)
	req2.Host = "fased.example.com"
	req2.RemoteAddr = "127.0.0.1:4567"
	req2.Header.Add("X-Forwarded-For", "198.51.100.1")
	req2.Header.Add("X-Forwarded-For", "203.0.113.42, 2001:db8::5")
	if got := h.ratePeerKey(req2); got != "proxy:2001:db8::/64" {
		t.Fatalf("expected rightmost multi-header IP normalized to /64 proxy:2001:db8::/64, got %q", got)
	}
}
