package privateaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestVerifyUsesDeviceBoundProofForSelfAddress(t *testing.T) {
	p := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "http://127.0.0.1:7331"}
	cfg, _ := json.Marshal(expected(p))
	for _, tc := range []struct {
		name                           string
		peer                           tailnet.Identity
		bound, initial, expected, want bool
	}{
		{"approved", tailnet.Identity{UserID: 7, NodeID: "owner"}, true, false, false, true},
		{"initial", tailnet.Identity{UserID: 7, NodeID: "owner"}, false, true, false, true},
		{"wrong initial", tailnet.Identity{UserID: 7, NodeID: "other"}, false, true, false, false},
		{"unbound", tailnet.Identity{UserID: 7, NodeID: "owner"}, false, false, false, false},
		{"different ssh device", tailnet.Identity{UserID: 7, NodeID: "other"}, true, false, true, false},
		{"serving node", tailnet.Identity{UserID: 7, NodeID: "server"}, true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			var token string
			m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { return cfg, "etag", nil },
				Run: func(context.Context, ...string) ([]byte, error) {
					return []byte(`{"BackendState":"Running","Self":{"Online":true,"ID":"server","DNSName":"node.owner.ts.net."}}`), nil
				},
				Client:   &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, syscall.ECONNREFUSED })},
				ProofOut: &out, Proof: func(_ context.Context, r core.PrivateAccessProofRequest) (core.PrivateAccessProof, error) {
					if r.Action == "begin" {
						token = r.Token
						return core.PrivateAccessProof{State: "pending", InstanceID: "instance"}, nil
					}
					if r.Action != "wait" || r.Token != token {
						t.Fatal("challenge changed")
					}
					return core.PrivateAccessProof{State: "verified", InstanceID: "instance", UserID: tc.peer.UserID, NodeID: tc.peer.NodeID, OwnerBound: tc.bound}, nil
				},
			}
			if tc.initial {
				m.InitialPeer = func() (tailnet.Identity, bool, error) { return tailnet.Identity{UserID: 7, NodeID: "owner"}, true, nil }
			}
			if tc.expected {
				m.ExpectedPeer = &tailnet.Identity{UserID: 7, NodeID: "owner"}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			err := m.Verify(ctx, p, "instance")
			if (err == nil) != tc.want {
				t.Fatalf("verification = %v", err)
			}
			if !strings.Contains(out.String(), p.Origin+"/.fased-private-access#"+token) {
				t.Fatal("private link missing")
			}
		})
	}
}

func TestDeviceProofRefusesServeChangeBeforeCompletion(t *testing.T) {
	p := Plan{Mode: "tailscale", Origin: "https://node.owner.ts.net:8443", Target: "http://127.0.0.1:7331"}
	cfg := expected(p)
	var out bytes.Buffer
	m := Manager{GetConfig: func(context.Context) ([]byte, string, error) { b, _ := json.Marshal(cfg); return b, "etag", nil },
		Run: func(context.Context, ...string) ([]byte, error) {
			return []byte(`{"BackendState":"Running","Self":{"Online":true,"ID":"server","DNSName":"node.owner.ts.net."}}`), nil
		}, ProofOut: &out,
		Proof: func(_ context.Context, r core.PrivateAccessProofRequest) (core.PrivateAccessProof, error) {
			if r.Action == "begin" {
				return core.PrivateAccessProof{State: "pending", InstanceID: "instance"}, nil
			}
			cfg["AllowFunnel"] = map[string]any{"node.owner.ts.net:8443": true}
			return core.PrivateAccessProof{State: "verified", InstanceID: "instance", UserID: 7, NodeID: "owner", OwnerBound: true}, nil
		},
	}
	if err := m.Verify(t.Context(), p, "instance"); err == nil {
		t.Fatal("route change after device proof accepted")
	}
}
