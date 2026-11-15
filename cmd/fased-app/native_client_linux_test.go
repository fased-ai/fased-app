//go:build linux

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestPackageOwnerUsesRunningProtectedImage(t *testing.T) {
	self, err := os.Executable()
	if err != nil || packageRunningImageMatches(self) != nil {
		t.Fatal("running image refused", err)
	}
	other, err := os.CreateTemp(t.TempDir(), "other-client")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	if packageRunningImageMatches(other.Name()) == nil {
		t.Fatal("different image accepted")
	}
}

func TestPackageBootstrapHealthBindsVerifiedEndpoint(t *testing.T) {
	want := core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: "verified", Connection: &core.ConnectionInfo{Location: "local", Transport: "loopback", OwnerUID: 1000}}
	for _, tc := range []struct {
		name   string
		change func(*core.Health)
		ok     bool
	}{
		{name: "same endpoint", ok: true},
		{name: "different endpoint", change: func(h *core.Health) { h.InstanceID = "foreign" }},
		{name: "different owner", change: func(h *core.Health) { h.Connection.OwnerUID++ }},
		{name: "remote", change: func(h *core.Health) { h.Connection.Location = "remote" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := want
			connection := *want.Connection
			h.Connection = &connection
			if tc.change != nil {
				tc.change(&h)
			}
			data, err := json.Marshal(h)
			if err != nil {
				t.Fatal(err)
			}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(data))}
			err = verifyPackageBootstrapHealth(response, "verified", 1000)
			if (err == nil) != tc.ok {
				t.Fatalf("health result = %v", err)
			}
			if tc.ok {
				got, _ := io.ReadAll(response.Body)
				if !bytes.Equal(got, data) {
					t.Fatal("validated response was changed")
				}
			}
		})
	}
}

func TestFreshPackageOwnerAllowsOnlyAnonymousHealthBeforeBinding(t *testing.T) {
	if !packageOwnerBootstrapProbe(httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/health", nil)) {
		t.Fatal("fixed health probe refused")
	}
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/v1/auth/native-local", nil),
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/health?other=1", nil),
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/health", strings.NewReader("payload")),
		func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/health", nil)
			r.Header.Set("Authorization", "Bearer session")
			return r
		}(),
		func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/health", nil)
			r.Header.Set("Cookie", "session=x")
			return r
		}(),
	} {
		if packageOwnerBootstrapProbe(req) {
			t.Fatal("an unauthenticated bootstrap request gained owner access", req.URL)
		}
	}
}

func TestNativeAppClientAllowlistCannotReachSignerOrMaintenance(t *testing.T) {
	for _, path := range []string{"/api/v1/signer/sign", "/api/v1/updates/application-owner", "/api/v1/updates/application/owner", "/api/v1/updates/application?override=true", "/api/v1/installation/local-owner", "/api/v1/auth/native-local?override=true"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1"+path, nil)
		if nativeClientRequestAllowed(r) {
			t.Fatal("private operation admitted", path)
		}
	}
	for _, row := range [][2]string{{"GET", "/api/v1/accounts"}, {"POST", "/api/v1/auth/native-local"}, {"GET", "/api/v1/updates/application"}, {"POST", "/api/v1/updates/application"}} {
		if !nativeClientRequestAllowed(httptest.NewRequest(row[0], "http://127.0.0.1"+row[1], nil)) {
			t.Fatal("supported native operation denied", row)
		}
	}
	if nativeClientRequestAllowed(httptest.NewRequest("PUT", "http://127.0.0.1/api/v1/updates/application", nil)) {
		t.Fatal("unsupported update method admitted")
	}
}
