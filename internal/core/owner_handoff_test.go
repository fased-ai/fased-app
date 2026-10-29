package core

import (
	"encoding/json"
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
