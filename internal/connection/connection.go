// Package connection verifies an explicitly chosen Fased browser destination.
// It has no installation, credential, signer or service-manager authority.
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var identity = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func Origin(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 2048 || strings.ContainsAny(value, "\\\r\n\t\x00") {
		return "", errors.New("enter an HTTPS instance address, or HTTP loopback for an SSH tunnel")
	}
	u, e := url.Parse(value)
	if e != nil || u.Host == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("use an instance origin without credentials, path, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if u.Scheme != "https" && !(u.Scheme == "http" && (host == "localhost" || ip != nil && ip.IsLoopback())) {
		return "", errors.New("Remote requires HTTPS; HTTP is allowed only for a loopback SSH tunnel")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid instance port")
	}
	if u.Port() != "" {
		if n, e := strconv.Atoi(u.Port()); e != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid instance port")
		}
	}
	u.Host = strings.ToLower(u.Host)
	return strings.TrimSuffix(u.String(), "/"), nil
}

type Result struct {
	URL                 string `json:"url"`
	InstanceID          string `json:"instanceId"`
	Location            string `json:"location"`
	Transport           string `json:"transport"`
	InstallationCreated bool   `json:"installationCreated"`
}

func Probe(ctx context.Context, value, expected string) (Result, error) {
	client, close := instanceClient()
	defer close()
	return probe(ctx, value, expected, client)
}
func instanceClient() (*http.Client, func()) {
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second, DisableCompression: true}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("instance redirects are refused") }}
	return client, transport.CloseIdleConnections
}

// A claimed instance needs only its normal sign-in URL. Do not execute an
// installed setup-proof helper merely to reopen it; older releases lack it.
func ClaimedOwnerURL(ctx context.Context, value, expected string) (string, bool, error) {
	client, close := instanceClient()
	defer close()
	return claimedOwnerURL(ctx, value, expected, client)
}
func claimedOwnerURL(ctx context.Context, value, expected string, client *http.Client) (string, bool, error) {
	if expected == "" {
		return "", false, errors.New("expected owner instance required")
	}
	instance, err := probe(ctx, value, expected, client)
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instance.URL+"/api/v1/auth/state", nil)
	if err != nil {
		return "", false, err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", false, errors.New("cannot verify owner sign-in state")
	}
	defer response.Body.Close()
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != 200 || err != nil || kind != "application/json" || response.Header.Get("Content-Encoding") != "" {
		return "", false, errors.New("invalid owner sign-in state")
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	var state struct {
		Claimed *bool `json:"claimed"`
	}
	if err != nil || len(b) > 4096 || json.Unmarshal(b, &state) != nil || state.Claimed == nil {
		return "", false, errors.New("invalid owner sign-in state")
	}
	if !*state.Claimed {
		return "", false, nil
	}
	address, err := OwnerSetupURL(instance.URL, instance.InstanceID, "")
	return address, true, err
}
func probe(ctx context.Context, value, expected string, client *http.Client) (Result, error) {
	origin, e := Origin(value)
	if e != nil {
		return Result{}, e
	}
	if expected != "" && !identity.MatchString(expected) {
		return Result{}, errors.New("invalid expected instance identity")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", origin+"/api/v1/health", nil)
	if e != nil {
		return Result{}, e
	}
	req.Header.Set("Accept", "application/json")
	response, e := client.Do(req)
	if e != nil {
		return Result{}, errors.New("cannot verify this Fased instance; check its address, private access and TLS")
	}
	defer response.Body.Close()
	h, e := ReadHealth(response, expected)
	if e != nil {
		return Result{}, e
	}
	return Result{URL: origin, InstanceID: h.InstanceID, Location: h.Connection.Location, Transport: h.Connection.Transport}, nil
}

// ReadHealth applies the same instance readiness contract to ordinary Connect
// and installation's private HTTPS verification. The caller closes the body.
func ReadHealth(response *http.Response, expected string) (core.Health, error) {
	if expected != "" && !identity.MatchString(expected) {
		return core.Health{}, errors.New("invalid expected instance identity")
	}
	if response == nil || response.Body == nil {
		return core.Health{}, errors.New("invalid instance response")
	}
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != 200 || err != nil || kind != "application/json" || response.Header.Get("Content-Encoding") != "" {
		return core.Health{}, errors.New("destination is not a compatible Fased instance")
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, 16*1024+1))
	if e != nil || len(b) > 16*1024 {
		return core.Health{}, errors.New("invalid instance response")
	}
	var h core.Health
	if json.Unmarshal(b, &h) != nil || h.Status != "ok" || h.APIVersion != core.APIVersion || !identity.MatchString(h.InstanceID) || h.Connection == nil || (h.Connection.Location != "local" && h.Connection.Location != "remote") || (h.Connection.Transport != "loopback" && h.Connection.Transport != "private-https") {
		return core.Health{}, errors.New("destination is not a compatible Fased instance")
	}
	if expected != "" && h.InstanceID != expected {
		return core.Health{}, errors.New("instance identity changed; no connection opened")
	}
	return h, nil
}
