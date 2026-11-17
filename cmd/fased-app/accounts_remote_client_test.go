package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

type accountsRemoteRoundTrip func(*http.Request) (*http.Response, error)

func (f accountsRemoteRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The command's real account adapters run against a controlled HTTP endpoint.
// Device-bound HTTPS and refusal of foreign instances are covered by core's
// existing Remote client tests and the installed fixture journey.
func TestAccountsConnectedRemoteUsesSameAPI(t *testing.T) {
	var paths []string
	transport := accountsRemoteRoundTrip(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer fixture-device-session" {
			t.Error("connected device authentication was not forwarded")
		}
		body := ""
		switch r.URL.Path {
		case "/api/v1/accounts":
			body = `{"record":{"version":1,"revision":0,"accounts":[]}}`
		case "/api/v1/accounts/read":
			if r.Method != http.MethodPost || r.Header.Get("X-CSRF-Token") != "fixture-csrf" {
				t.Error("account read did not use the authenticated mutation path")
			}
			body = `{"accountId":"fixture","revision":0,"fixtureOnly":true,"liveRPC":false}`
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})
	dir := t.TempDir()
	prior := accountsRemoteClient
	accountsRemoteClient = func(_ context.Context, got string) (*core.Client, error) {
		if got != dir {
			t.Fatal("connected profile directory changed")
		}
		return &core.Client{Endpoint: core.Endpoint{URL: "https://fixture.invalid", InstanceID: "fixture"}, HTTP: &http.Client{Transport: transport}, Token: "fixture-device-session", CSRF: "fixture-csrf"}, nil
	}
	defer func() { accountsRemoteClient = prior }()
	var out bytes.Buffer
	if err := accountsCommand([]string{"--remote", "--data-dir", dir}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(`{"accountId":"fixture","revision":0,"ref":"fixture","passphrase":"disposable"}`), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out.Reset()
	if err = accountsCommand([]string{"--remote", "--data-dir", dir, "--read-stdin"}, f, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "GET /api/v1/accounts" || paths[1] != "POST /api/v1/accounts/read" {
		t.Fatal("unexpected account paths", paths)
	}
	if err = accountsCommand([]string{"--remote", "--data-dir", dir, "--setup-stdin"}, f, &out, io.Discard); err == nil || len(paths) != 2 {
		t.Fatal("Remote account writes must remain outside this scoped client", err)
	}
	if _, err = os.Lstat(filepath.Join(dir, "cli-session.json")); !os.IsNotExist(err) {
		t.Fatal("Remote account command created a local owner session")
	}
}
