package core

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionHealthUsesRuntimeOrigin(t *testing.T) {
	root, err := filepath.Abs("../../.evidence/connection-fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler, err := NewHTTPHandler(store, "http://127.0.0.1:7331", "https://node.owner.ts.net", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ origin, location, transport string }{
		{"http://127.0.0.1:7331", "local", "loopback"},
		{"https://node.owner.ts.net", "remote", "private-https"},
	} {
		t.Run(tc.transport, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("GET", tc.origin+"/api/v1/health", nil))
			var body struct {
				InstanceID string `json:"instanceId"`
				Connection struct {
					Location  string `json:"location"`
					Transport string `json:"transport"`
				} `json:"connection"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || body.InstanceID != store.instanceID || body.Connection.Location != tc.location || body.Connection.Transport != tc.transport {
				t.Fatalf("runtime connection absent or incorrect: %s", w.Body.String())
			}
		})
	}
}
