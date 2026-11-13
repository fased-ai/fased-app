//go:build linux

package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestProtectedInitialApprovalBindsBrowserAndOriginalRelease(t *testing.T) {
	const origin, listen = "https://node.owner.ts.net:8443", "127.0.0.1:7332"
	release, coreHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	device := tailnet.Identity{UserID: 42, NodeID: "node-1", Login: "owner@example.com"}
	m := Manager{Run: func(context.Context, ...string) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Self":{"DNSName":"node.owner.ts.net.","Online":true}}`), nil
	}}
	var saved ProtectedInitialApproval
	var confirmed, writes int
	load := func() (ProtectedInitialApproval, error) {
		if writes == 0 {
			return saved, os.ErrNotExist
		}
		return saved, nil
	}
	save := func(a ProtectedInitialApproval) error { writes++; saved = a; return nil }
	before := func(_ context.Context, p Plan) (tailnet.Identity, error) {
		confirmed++
		if p.Origin != origin || p.Target != "http://"+listen {
			t.Fatal("preflight target changed", p)
		}
		return device, nil
	}
	got, err := confirmProtectedRemoteInitial(t.Context(), m, 1300, origin, listen, 8, release, coreHash, load, save, before)
	if err != nil || got != device || writes != 1 || confirmed != 1 || !saved.Matches(1300, origin, listen, 8, release, coreHash) {
		t.Fatal("browser approval was not bound to the exact install", got, err)
	}
	got, err = confirmProtectedRemoteInitial(t.Context(), m, 1300, origin, listen, 8, release, coreHash, load, save, before)
	if err != nil || got != device || writes != 1 || confirmed != 1 {
		t.Fatal("retry repeated approval", err)
	}
	if _, err = confirmProtectedRemoteInitial(t.Context(), m, 1300, origin, listen, 9, release, coreHash, load, save, before); err == nil || writes != 1 || confirmed != 1 {
		t.Fatal("different release replaced existing approval", err)
	}
	if _, err = confirmProtectedRemoteInitial(t.Context(), m, 1300, "https://foreign.owner.ts.net", listen, 8, release, coreHash, load, save, before); err == nil {
		t.Fatal("different Tailscale device accepted")
	}
	failed := func(context.Context, Plan) (tailnet.Identity, error) {
		return tailnet.Identity{}, errors.New("browser not confirmed")
	}
	writes, confirmed = 0, 0
	if _, err = confirmProtectedRemoteInitial(t.Context(), m, 1300, origin, listen, 8, release, coreHash, load, save, failed); err == nil || writes != 0 {
		t.Fatal("unconfirmed browser saved as approval", err)
	}
}

func TestProtectedPendingOriginRejectsConflicts(t *testing.T) {
	want := "https://node.owner.ts.net:8443"
	receipt := preflightReceipt{Version: 1, Plan: Plan{Mode: "tailscale", Origin: want, Target: "unix:/tmp/fased-private-123/tailscale.sock"}}
	b, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, prior := range []string{"", want} {
		got, err := protectedPendingOrigin(prior, b)
		if err != nil || got != want {
			t.Fatal("original protected port was lost", got, err)
		}
	}
	if _, err := protectedPendingOrigin("https://node.owner.ts.net", b); err == nil {
		t.Fatal("conflicting protected approvals admitted")
	}
	receipt.Plan.Target = "unix:/var/lib/foreign/tailscale.sock"
	b, _ = json.Marshal(receipt)
	if _, err := protectedPendingOrigin("", b); err == nil {
		t.Fatal("foreign socket admitted")
	}
}

func TestProtectedApprovalBindsVerifiedCoreOnce(t *testing.T) {
	release, coreHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	a := ProtectedInitialApproval{Version: 1, Owner: 1300, Origin: "https://node.owner.ts.net", Listen: "127.0.0.1:7332", Sequence: 8, ReleaseSHA256: release, Device: tailnet.Identity{UserID: 42, NodeID: "device", Login: "owner@example.com"}}
	if !a.Matches(1300, a.Origin, a.Listen, 8, release, "") || a.Matches(1300, a.Origin, a.Listen, 8, release, coreHash) {
		t.Fatal("unverified core treated as bound")
	}
	bound, changed, err := bindProtectedInitialCore(a, 1300, a.Origin, a.Listen, 8, release, coreHash)
	if err != nil || !changed || !bound.Matches(1300, a.Origin, a.Listen, 8, release, coreHash) {
		t.Fatal("verified core not bound", err)
	}
	if _, changed, err = bindProtectedInitialCore(bound, 1300, a.Origin, a.Listen, 8, release, coreHash); err != nil || changed {
		t.Fatal("same binding retry changed state", err)
	}
	if _, _, err = bindProtectedInitialCore(bound, 1300, a.Origin, a.Listen, 8, release, strings.Repeat("c", 64)); err == nil {
		t.Fatal("different core replaced approval")
	}
}
