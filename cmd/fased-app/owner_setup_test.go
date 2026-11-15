package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type ownerSetupRoundTripper func(*http.Request) (*http.Response, error)

func (f ownerSetupRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProtectedLocalSetupLinkUsesPrivateOwnerLaunch(t *testing.T) {
	old := ownerSetupClient
	t.Cleanup(func() { ownerSetupClient = old })
	const origin = "http://127.0.0.1:7331"
	instance, code := strings.Repeat("i", 43), strings.Repeat("a", 43)
	owner, connectionInstance, location := os.Getuid(), instance, "local"
	var calls []string
	ownerSetupClient = func(context.Context, string) (*core.Client, bool, error) {
		transport := ownerSetupRoundTripper(func(r *http.Request) (*http.Response, error) {
			calls = append(calls, r.Method+" "+r.URL.Path)
			var body []byte
			switch r.URL.Path {
			case "/api/v1/health":
				body, _ = json.Marshal(core.Health{InstanceID: connectionInstance, Connection: &core.ConnectionInfo{Location: location, Transport: "loopback", OwnerUID: owner}})
			case "/api/v1/auth/native-local":
				if r.Method != http.MethodPost || r.Header.Get("Origin") != origin {
					t.Fatal("unprotected Local launch request")
				}
				body, _ = json.Marshal(map[string]string{"code": code})
			default:
				t.Fatalf("unexpected owner route %s", r.URL.Path)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
		return &core.Client{Endpoint: core.Endpoint{URL: origin, InstanceID: instance}, HTTP: &http.Client{Transport: transport}}, true, nil
	}
	dir := filepath.Join(t.TempDir(), "uncreated-owner-state")
	var output bytes.Buffer
	if err := run([]string{"setup-link", "--data-dir", dir}, nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.TrimSpace(output.String()))
	if err != nil || link.Query().Get("instance") != instance || link.Fragment != "local="+code || strings.Join(calls, ",") != "GET /api/v1/health,POST /api/v1/auth/native-local" {
		t.Fatal("protected browser handoff differs", link, calls, err)
	}
	for _, change := range []func(){
		func() { owner++ },
		func() { connectionInstance = strings.Repeat("x", 43) },
		func() { location = "remote" },
	} {
		owner, connectionInstance, location = os.Getuid(), instance, "local"
		change()
		calls = nil
		if err = run([]string{"setup-link", "--data-dir", dir}, nil, &output, io.Discard); err == nil || len(calls) != 1 {
			t.Fatal("foreign owner or instance received a launch proof", err, calls)
		}
	}
	owner, connectionInstance, location = os.Getuid(), instance, "local"
	calls = nil
	if err = run([]string{"setup-link", "--data-dir", dir, "--public-origin", "https://other.example"}, nil, &output, io.Discard); err == nil || len(calls) != 1 {
		t.Fatal("foreign browser origin received a launch proof", err, calls)
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("protected launch changed owner state", err)
	}
}

func TestOwnerSetupLinkPreservesClaimedWorkspace(t *testing.T) {
	dir, err := os.MkdirTemp("", "fsl-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	server, err := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.ConfigureConnection("remote"); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	}()
	var output bytes.Buffer
	if err = ownerSetupLink(t.Context(), dir, "", &output); err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(strings.TrimSpace(output.String()))
	if err != nil || link.Query().Get("instance") == "" || !strings.HasPrefix(link.Fragment, "setup=") {
		t.Fatal("missing bound handoff", err)
	}
	var before, after core.ClaimCode
	data, _ := core.ReadPrivateFile(filepath.Join(dir, "claim-code.json"))
	_ = json.Unmarshal(data, &before)
	if err = ownerSetupLink(t.Context(), dir, "http://public.invalid", &output); err == nil {
		t.Fatal("unsafe origin accepted")
	}
	data, _ = core.ReadPrivateFile(filepath.Join(dir, "claim-code.json"))
	_ = json.Unmarshal(data, &after)
	if before != after {
		t.Fatal("invalid destination rotated proof")
	}
	session, err := server.Store.Claim(t.Context(), strings.TrimPrefix(link.Fragment, "setup="), "disposable-setup-test-password")
	if err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = ownerSetupLink(t.Context(), dir, "", &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "#setup=") || !server.Store.Session(t.Context(), session) {
		t.Fatal("reopen reset an existing owner")
	}
}
