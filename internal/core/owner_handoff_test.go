package core

import (
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/signer"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInstalledOwnerHandoffIsMetadataNotAuthority(t *testing.T) {
	store, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, err := NewHTTPHandler(store, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: store, HTTP: &http.Server{Handler: h}}
	for _, c := range []struct {
		location string
		owner    int
	}{{"local", 0}, {"remote", 0}, {"remote", 1000}} {
		if err = server.ConfigureConnection(c.location, c.owner); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/health", nil)
		request.RemoteAddr = "127.0.0.1:12345"
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		var health Health
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &health) != nil || health.Connection == nil {
			t.Fatal(response.Code, response.Body.String())
		}
		if health.Connection.Location != c.location || health.Connection.OwnerUID != c.owner {
			t.Fatal(health.Connection, c)
		}
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(response.Body.Bytes(), &raw)
		var connection map[string]json.RawMessage
		_ = json.Unmarshal(raw["connection"], &connection)
		if _, present := connection["ownerUid"]; present != (c.owner > 0) {
			t.Fatal("ordinary instances acquired a dedicated owner identity", connection)
		}
		for _, name := range []string{"walletSigner", "financialExecution", "aiConnections", "updater"} {
			if health.Capabilities[name] {
				t.Fatal("metadata enabled a held capability", name)
			}
		}
		request = httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/setup", nil)
		request.RemoteAddr = "127.0.0.1:12345"
		response = httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatal("owner metadata bypassed authentication", response.Code)
		}
	}
	for _, c := range []struct {
		location string
		owners   []int
	}{{"local", []int{1000}}, {"remote", []int{-1}}, {"unknown", nil}, {"remote", []int{1000, 1001}}} {
		beforeLocation, beforeOwner := h.instanceLocation, h.instanceOwnerUID
		if server.ConfigureConnection(c.location, c.owners...) == nil {
			t.Fatal("invalid ownership accepted", c)
		}
		if beforeLocation != h.instanceLocation || beforeOwner != h.instanceOwnerUID {
			t.Fatal("failed setup partially changed the descriptor")
		}
	}
	if err = server.ConfigureConnection("local"); err != nil || h.instanceOwnerUID != 0 {
		t.Fatal("legacy Local configuration failed", err)
	}
}

func TestEnrollmentMetadataUsesVerifiedInstallerClass(t *testing.T) {
	store, err := OpenStore(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h, err := NewHTTPHandler(store, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: store, HTTP: &http.Server{Handler: h}}
	for _, source := range []string{"", "local", "remote"} {
		if err = server.ConfigureInstallationSource(source); err != nil {
			t.Fatal(err)
		}
		// A per-user installation can be physically Remote. Its actual installer
		// remains Local; the UI must not derive --source from a preference/location.
		if err = server.ConfigureConnection("remote"); err != nil {
			t.Fatal(err)
		}
		v := store.walletSetup(FixtureView{})
		if v.Source != source || v.InstanceID != store.instanceID || v.State != "not-installed" {
			t.Fatal(v)
		}
	}
	before := store.installationSource.Load()
	if server.ConfigureInstallationSource("foreign") == nil || store.installationSource.Load() != before {
		t.Fatal("foreign installer changed source")
	}
	// Only the verified managed v2 startup supplies an owner UID in Local mode.
	store.fixture = func(context.Context, signer.Request) (signer.Response, error) { return signer.Response{}, nil }
	store.custodyNetwork = "devnet"
	if err = server.ConfigureConnection("local", 1000); err != nil || h.instanceOwnerUID != 1000 || h.instanceOwnerKind != "custody" {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1:7331/api/v1/health", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	var health Health
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &health) != nil || health.Connection == nil || health.Connection.OwnerKind != "custody" || health.Connection.OwnerUID != 1000 || health.Connection.Location != "local" {
		t.Fatal(response.Code, response.Body.String())
	}
}
