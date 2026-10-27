//go:build linux

package core

import (
	"github.com/fased-ai/fased-app/internal/update"
	"syscall"
	"testing"
)

func TestCompletionReleasesOnlyMatchingFenceAndRetries(t *testing.T) {
	s := fixtureStore(t)
	p := pausedBackup(t, s)
	p.Files[0].Artifact.File = "fased-app"
	p.Files[1].Artifact.File = "fased-signer"
	p = rebindProof(p)
	b, e := update.BackupBinding(p)
	if e != nil {
		t.Fatal(e)
	}
	index := update.RecoveryIndex{Version: 1, Phase: "metadata-backed-up", Binding: b, BindingSHA256: update.RecoveryDigest(b), Snapshot: update.MetadataSnapshot{File: "metadata.sqlite", Size: 1, SHA256: p.ManifestSHA256}}
	binding := update.InstallBinding{Version: 1, Index: index, InstallationID: update.NewProbe(), Contracts: []update.Contract{UpdateContract(), {Component: "signer"}}, PreviousCore: p.ManifestSHA256, PreviousSigner: p.ManifestSHA256, PreviousManifest: p.ManifestSHA256, TargetCore: p.Files[0].Artifact.SHA256, TargetSigner: p.Files[1].Artifact.SHA256}
	i := update.Installation{Version: 1, Binding: binding, Digest: update.InstallDigest(binding), Phase: "completed", InstalledSequenceCommitted: true}
	c := update.CorePauseCommand{Action: "complete", ProbeID: update.NewProbe(), Completed: &i}
	bad := i
	bad.Phase = "committed-held"
	c.Completed = &bad
	if _, e = s.UpdatePause(t.Context(), c); e == nil {
		t.Fatal("incomplete signer released fence")
	}
	c.Completed = &i
	s.pauseFault = func(at string) error {
		if at == "pause:durable" {
			return syscall.ENOSPC
		}
		return nil
	}
	if _, e = s.UpdatePause(t.Context(), c); e == nil {
		t.Fatal("lost reply missed")
	}
	s.pauseFault = nil
	for n := 0; n < 2; n++ {
		got, e := s.UpdatePause(t.Context(), c)
		if e != nil || got.State != "released" {
			t.Fatal(got, e)
		}
	}
	setup, e := s.Setup(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateSetup(t.Context(), setup.Revision, setup.Settings); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdatePause(t.Context(), c); e != nil {
		t.Fatal("current retry after metadata write", e)
	}
	next := pauseCommand(t, s, 2)
	if _, e = s.UpdatePause(t.Context(), next); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdatePause(t.Context(), c); e == nil {
		t.Fatal("old completion released newer fence")
	}
}
