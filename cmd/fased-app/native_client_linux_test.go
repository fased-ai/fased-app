//go:build linux

package main

import (
	"net/http/httptest"
	"testing"
)

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
