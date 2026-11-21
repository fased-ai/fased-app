//go:build linux

package vpsinstall

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

func TestProtectedIngressProbeBindsTheRunningInstance(t *testing.T) {
	t.Parallel()
	_, _, m, _ := credentialFixture(t)
	m.PublicOrigin = "https://fixture.example.ts.net"
	const instance = "fixture-instance"
	for _, tc := range []struct {
		name, id string
		status   int
		want     bool
	}{
		{"same instance", instance, http.StatusOK, true},
		{"different instance", "foreign-instance", http.StatusOK, false},
		{"unavailable", instance, http.StatusServiceUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dial := func(context.Context) (net.Conn, error) {
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					request, err := http.ReadRequest(bufio.NewReader(server))
					if err != nil {
						t.Error(err)
						return
					}
					if request.URL.Path != "/api/v1/health" || request.Header.Get("X-Forwarded-Host") != "fixture.example.ts.net" || request.Header.Get("X-Forwarded-Proto") != "https" || request.Header.Get("X-Forwarded-For") != "100.64.0.1" {
						t.Error("incorrect private health probe")
					}
					body, _ := json.Marshal(core.Health{Status: "ok", APIVersion: core.APIVersion, InstanceID: tc.id, Connection: &core.ConnectionInfo{Location: "remote", Transport: "private-https", OwnerUID: m.OwnerUID}})
					_, _ = fmt.Fprintf(server, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nContent-Type: application/json\r\nConnection: close\r\n\r\n", tc.status, http.StatusText(tc.status), len(body))
					_, _ = server.Write(body)
				}()
				return client, nil
			}
			err := privateaccess.ProbeProtectedIngress(t.Context(), m.PublicOrigin, m.OwnerUID, instance, dial)
			if (err == nil) != tc.want {
				t.Fatal("unexpected probe result", err)
			}
		})
	}
}
