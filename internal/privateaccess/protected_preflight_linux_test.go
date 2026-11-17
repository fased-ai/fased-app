//go:build linux

package privateaccess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

func TestPackageRemoteApprovalDoesNotBecomeOwnerWideAuthority(t *testing.T) {
	release, coreHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	a := ProtectedInitialApproval{Version: 1, Owner: 1300, Instance: strings.Repeat("c", 32), Origin: "https://node.owner.ts.net:8443", Listen: "127.0.0.1:7332", Sequence: 8, ReleaseSHA256: release, CoreSHA256: coreHash, Device: tailnet.Identity{UserID: 42, NodeID: "device", Login: "owner@example.com"}}
	if a.Matches(a.Owner, a.Origin, a.Listen, a.Sequence, release, coreHash) {
		t.Fatal("a package-scoped approval was accepted as owner-wide authority")
	}
	if !a.MatchesPackage(a.Owner, a.Instance, a.Origin, a.Listen, a.Sequence, release, coreHash) || a.MatchesPackage(a.Owner, strings.Repeat("d", 32), a.Origin, a.Listen, a.Sequence, release, coreHash) {
		t.Fatal("package approval was not restricted to its original instance")
	}
	manager := Manager{Run: func(context.Context, ...string) ([]byte, error) {
		return []byte(`{"BackendState":"Running","Self":{"DNSName":"node.owner.ts.net.","Online":true}}`), nil
	}}
	var saved ProtectedInitialApproval
	writes, confirmations := 0, 0
	load := func() (ProtectedInitialApproval, error) {
		if writes == 0 {
			return saved, os.ErrNotExist
		}
		return saved, nil
	}
	save := func(got ProtectedInitialApproval) error { writes++; saved = got; return nil }
	before := func(context.Context, Plan) (tailnet.Identity, error) { confirmations++; return a.Device, nil }
	got, err := confirmProtectedRemoteInitial(t.Context(), manager, a.Owner, a.Origin, a.Listen, a.Sequence, release, coreHash, load, save, before, a.Instance)
	if err != nil || got != a.Device || saved.Instance != a.Instance || writes != 1 || confirmations != 1 {
		t.Fatal("device approval was not recorded for this package", err)
	}
	if _, err = confirmProtectedRemoteInitial(t.Context(), manager, a.Owner, a.Origin, a.Listen, a.Sequence, release, coreHash, load, save, before, strings.Repeat("d", 32)); err == nil || writes != 1 || confirmations != 1 {
		t.Fatal("another package reused the original device approval", err)
	}
}

func TestPackageRemotePreflightSocketFitsLinux(t *testing.T) {
	instance := strings.Repeat("a", 32)
	dir, err := protectedPackageApprovalDir(4294967294, instance)
	if err != nil {
		t.Fatal(err)
	}
	// MkdirTemp appends at most ten decimal digits. Linux includes a NUL in
	// its 108-byte sockaddr_un path, so the full pathname must fit in 107.
	socket := filepath.Join(dir, "fased-private-4294967295", "tailscale.sock")
	if len([]byte(socket)) > 107 {
		t.Fatalf("protected Remote preflight socket is too long for Linux (%d bytes): %s", len([]byte(socket)), socket)
	}
	other, err := protectedPackageApprovalDir(4294967294, strings.Repeat("b", 32))
	if err != nil || other == dir || !preflightSocketTarget("unix:"+socket, dir) || preflightSocketTarget("unix:"+socket, other) {
		t.Fatal("different package instances share their preflight state")
	}
	if _, err = protectedPackageApprovalDir(0, instance); err == nil {
		t.Fatal("invalid package owner admitted")
	}
}

func TestProtectedPendingOriginRejectsConflicts(t *testing.T) {
	want := "https://node.owner.ts.net:8443"
	dir := "/var/lib/fased-maintenance/owners/1300/remote"
	receipt := preflightReceipt{Version: 1, Plan: Plan{Mode: "tailscale", Origin: want, Target: "unix:/tmp/fased-private-123/tailscale.sock"}}
	b, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, prior := range []string{"", want} {
		got, err := protectedPendingOrigin(prior, b, dir)
		if err != nil || got != want {
			t.Fatal("original protected port was lost", got, err)
		}
	}
	if _, err := protectedPendingOrigin("https://node.owner.ts.net", b, dir); err == nil {
		t.Fatal("conflicting protected approvals admitted")
	}
	receipt.Plan.Target = "unix:" + filepath.Join(dir, "fased-private-123", "tailscale.sock")
	b, _ = json.Marshal(receipt)
	if got, err := protectedPendingOrigin("", b, dir); err != nil || got != want {
		t.Fatal("protected socket retry lost its port", got, err)
	}
	receipt.Plan.Target = "unix:/var/lib/foreign/tailscale.sock"
	b, _ = json.Marshal(receipt)
	if _, err := protectedPendingOrigin("", b, dir); err == nil {
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
