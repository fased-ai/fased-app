//go:build linux

package vpsinstall

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/privateaccess"
	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestProtectedSetupLinkUsesOnlyConfirmedDevice(t *testing.T) {
	device := tailnet.Identity{UserID: 42, NodeID: "device", Login: "owner@example.com"}
	m := Manifest{PublicOrigin: "https://node.owner.ts.net", Listen: "127.0.0.1:7332", ApplicationSequence: 8, ApplicationReleaseSHA256: strings.Repeat("a", 64), CoreSHA256: strings.Repeat("b", 64)}
	a := privateaccess.ProtectedInitialApproval{Version: 1, Owner: 1300, Origin: m.PublicOrigin, Listen: m.Listen, Sequence: m.ApplicationSequence, ReleaseSHA256: m.ApplicationReleaseSHA256, CoreSHA256: m.CoreSHA256, Device: device}
	for _, supplied := range []*tailnet.Identity{nil, &device} {
		got, err := protectedSetupPeer(1300, m, supplied, a, nil)
		if err != nil || got == nil || *got != device {
			t.Fatal("confirmed device refused", err)
		}
	}
	other := device
	other.NodeID = "different-device"
	if _, err := protectedSetupPeer(1300, m, &other, a, nil); err == nil {
		t.Fatal("different device admitted")
	}
	if got, err := protectedSetupPeer(1300, m, &device, a, os.ErrNotExist); err != nil || got != nil {
		t.Fatal("legacy recovery forwarded an unverified owner", err)
	}
	if _, err := protectedSetupPeer(1300, m, nil, a, errors.New("damaged approval")); err == nil {
		t.Fatal("damaged approval ignored")
	}
	m.CoreSHA256 = strings.Repeat("c", 64)
	if _, err := protectedSetupPeer(1300, m, &device, a, nil); err == nil {
		t.Fatal("changed package admitted")
	}
	got, err := protectedSetupPeer(1300, m, nil, a, nil)
	if err != nil || got != nil {
		t.Fatal("already-claimed status cannot use prior approval after update", err)
	}
}
