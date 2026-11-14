package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

func TestInitialRemoteOwnerHandoffPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	want := remoteSetupOwner{Version: 1, Origin: "https://node.owner.ts.net", Listen: "127.0.0.1:7332", EngineSHA256: "verified", Owner: tailnet.Identity{UserID: 42, NodeID: "device", Login: "owner@example.com"}}
	if err := saveInitialRemoteOwner(dir, want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "owner-handoff.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveInitialRemoteOwner(dir, want); err != nil {
		t.Fatal("identical retry refused", err)
	}
	other := want
	other.Owner.NodeID = "different-device"
	if err := saveInitialRemoteOwner(dir, other); err == nil {
		t.Fatal("different owner replaced the original")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("original handoff changed", err)
	}
}

func TestRemoteInstallerRetryRequiresSameInstalledInputs(t *testing.T) {
	m := vpsinstall.Manifest{Listen: "127.0.0.1:7332", PublicOrigin: "https://node.owner.ts.net", ApplicationSequence: 8, ApplicationReleaseSHA256: "release", CoreSHA256: "core"}
	if !sameRemoteInstallerInput(m, m.Listen, m.PublicOrigin, 8, "release", "core") {
		t.Fatal("original install refused")
	}
	for _, changed := range []vpsinstall.Manifest{
		{Listen: "127.0.0.1:7333", PublicOrigin: m.PublicOrigin, ApplicationSequence: 8, ApplicationReleaseSHA256: "release", CoreSHA256: "core"},
		{Listen: m.Listen, PublicOrigin: "https://other.owner.ts.net", ApplicationSequence: 8, ApplicationReleaseSHA256: "release", CoreSHA256: "core"},
		{Listen: m.Listen, PublicOrigin: m.PublicOrigin, ApplicationSequence: 9, ApplicationReleaseSHA256: "release", CoreSHA256: "core"},
		{Listen: m.Listen, PublicOrigin: m.PublicOrigin, ApplicationSequence: 8, ApplicationReleaseSHA256: "other", CoreSHA256: "core"},
		{Listen: m.Listen, PublicOrigin: m.PublicOrigin, ApplicationSequence: 8, ApplicationReleaseSHA256: "release", CoreSHA256: "other"},
	} {
		if sameRemoteInstallerInput(changed, m.Listen, m.PublicOrigin, 8, "release", "core") {
			t.Fatal("different installation admitted")
		}
	}
}
