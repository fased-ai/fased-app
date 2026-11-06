package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func retainedChannelDescriptor(t *testing.T) bootstrap.Descriptor {
	t.Helper()
	b, err := os.ReadFile("testdata/consecutive-update-descriptor.json")
	if err != nil {
		t.Fatal(err)
	}
	var d bootstrap.Descriptor
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestChannelConsecutiveUpdateLegacyOwnerContract(t *testing.T) {
	d := retainedChannelDescriptor(t)
	b, _ := json.Marshal(d)
	h := sha256.Sum256(b)
	legacy := hex.EncodeToString(h[:])
	if legacy != "6c61856f6426958817de4d3080f3d35f006417f7cf99a8c6ab7375969cddb99e" {
		t.Fatal("retained descriptor changed", legacy)
	}
	r := core.ApplicationRelease{EngineSHA256: "0421bb53c43533198848390299c87d0440cacc975067107a668be62e7c6737e1", Version: d.ProductVersion, Sequence: d.Sequence, ManifestSHA256: bootstrap.ApplicationDescriptorIdentity(d), Target: d.Target}
	if r.ManifestSHA256 != "ef805fa651456e42b200b23f5f0309cfe209956462c3e301de8183a0aa0f48fb" || r.ManifestSHA256 == legacy {
		t.Fatal("retained canonical identity changed", r)
	}
	// Exact comparison performed by the unchanged 0.1.6 owner after its first
	// upgrade to 0.1.9. Its executable and authority are deliberately retained.
	oldOwner := r
	oldOwner.ManifestSHA256 = legacy
	approved, err := channelMaintenanceRelease(d, r)
	if err != nil || approved != oldOwner {
		t.Fatalf("next upgrade refused by retained owner: approved=%+v owner=%+v err=%v", approved, oldOwner, err)
	}
	for _, mutate := range []func(*core.ApplicationRelease){
		func(r *core.ApplicationRelease) { r.ManifestSHA256 = strings.Repeat("0", 64) },
		func(r *core.ApplicationRelease) { r.Sequence++ },
		func(r *core.ApplicationRelease) { r.Version = "different" },
		func(r *core.ApplicationRelease) { r.Target.Arch = "arm64" },
	} {
		changed := r
		mutate(&changed)
		if _, err := channelMaintenanceRelease(d, changed); err == nil {
			t.Fatal("different approved release accepted", changed)
		}
	}
}

func TestChannelConsecutiveUpdatesKeepIdentityProofAcrossRestartAndPublication(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	d := retainedChannelDescriptor(t)
	// Model the accepted first upgrade, then the exact retained successor
	// descriptor. Both checks use the unchanged 0.1.6 owner wire contract.
	first := d
	first.ProductVersion, first.Sequence = "0.1.9", 1000010
	installed, sequence := strings.Repeat("a", 64), uint64(1000007)
	for _, next := range []bootstrap.Descriptor{first, d} {
		canonical := bootstrap.ApplicationDescriptorIdentity(next)
		if current, err := channelReleaseCurrentWithProof(parent, next, canonical, sequence, installed); err != nil || current {
			t.Fatal("next genuine update refused", current, err)
		}
		approved, err := channelMaintenanceRelease(next, core.ApplicationRelease{Version: next.ProductVersion, Sequence: next.Sequence, Target: next.Target, ManifestSHA256: canonical})
		if err != nil || approved.ManifestSHA256 != channelDescriptorDigest(next) {
			t.Fatal("retained owner cannot verify exact descriptor", err)
		}
		if err = channelRememberDescriptor(parent, next); err != nil {
			t.Fatal(err)
		}
		// Simulated immutable installation floor; no cache value can advance it.
		installed, sequence = approved.ManifestSHA256, approved.Sequence
	}
	// A fresh caller has only persisted descriptor evidence and the installed
	// floor. Publication changes delivery IDs, not authenticated content.
	published := d
	published.GitHub.ReleaseID, published.GitHub.AssetID = "9001", "9002"
	canonical := bootstrap.ApplicationDescriptorIdentity(published)
	if current, err := channelReleaseCurrentWithProof(parent, published, canonical, sequence, installed); err != nil || !current {
		t.Fatal("restart/publication offered duplicate execution", current, err)
	}
	for _, mutate := range []func(*bootstrap.Descriptor){
		func(v *bootstrap.Descriptor) { v.Source = strings.Repeat("f", 40) },
		func(v *bootstrap.Descriptor) { v.BundleSHA256 = strings.Repeat("e", 64) },
		func(v *bootstrap.Descriptor) { v.Release.RunAttempt = "2" },
		func(v *bootstrap.Descriptor) { v.Target = update.Target{OS: "linux", Arch: "arm64"} },
	} {
		changed := published
		mutate(&changed)
		if current, err := channelReleaseCurrentWithProof(parent, changed, bootstrap.ApplicationDescriptorIdentity(changed), sequence, installed); err == nil || current {
			t.Fatal("cache accepted different content at consumed sequence", current, err)
		}
	}
	// A higher installed sequence remains no-op without fetching old code.
	if current, err := channelReleaseCurrentWithProof(parent, published, canonical, sequence+1, installed); err != nil || !current {
		t.Fatal("installed floor was lowered", current, err)
	}
	proofPath := filepath.Join(parent, "descriptors", installed+".json")
	original, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{[]byte("{}"), append(original, ' '), []byte(strings.Repeat("x", 65537))} {
		if err = os.WriteFile(proofPath, payload, 0600); err != nil {
			t.Fatal(err)
		}
		if current, err := channelReleaseCurrentWithProof(parent, published, canonical, sequence, installed); err == nil || current {
			t.Fatal("corrupt proof accepted", current, err)
		}
	}
	if err = os.Remove(proofPath); err != nil {
		t.Fatal(err)
	}
	if current, err := channelReleaseCurrentWithProof(parent, published, canonical, sequence, installed); err == nil || current {
		t.Fatal("absent proof accepted", current, err)
	}
	foreign := filepath.Join(parent, "foreign.json")
	if err = os.WriteFile(foreign, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(foreign, proofPath); err != nil {
		t.Fatal(err)
	}
	if current, err := channelReleaseCurrentWithProof(parent, published, canonical, sequence, installed); err == nil || current {
		t.Fatal("symlink proof accepted", current, err)
	}
}

func TestChannelSourceUsesValidatedIDUnderOwnerControl(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	id := strings.Repeat("a", 32)
	called := ""
	factory := func(directory string) (bootstrap.Options, error) {
		called = directory
		return bootstrap.Options{Directory: directory}, nil
	}
	source := channelSource(factory, parent)
	if _, e := source(context.Background(), core.ApplicationRelease{}, id); e == nil {
		t.Fatal("missing trust accepted")
	}
	if !filepath.IsAbs(called) || !strings.HasPrefix(called, parent+string(filepath.Separator)+"acquisition-"+id+"-") {
		t.Fatal(called)
	}
	called = ""
	if _, e := source(context.Background(), core.ApplicationRelease{}, "/foreign/path"); e == nil || called != "" {
		t.Fatal("untrusted path reached acquisition", e)
	}
	if _, e := channelOptions(nil, parent); e == nil {
		t.Fatal("ordinary channel activated")
	}
}

func TestChannelReleaseCurrentNeverOffersDowngradeOrReplacement(t *testing.T) {
	d := bootstrap.Descriptor{Sequence: 1000008}
	identity := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name            string
		installed       uint64
		identity        string
		current, refuse bool
	}{
		{"installed candidate ahead of public", 1000009, identity, true, false},
		{"same release", 1000008, identity, true, false},
		{"same sequence different content", 1000008, strings.Repeat("b", 64), false, true},
		{"genuinely newer public release", 1000007, identity, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, err := channelReleaseCurrent(d, identity, tc.installed, tc.identity)
			if current != tc.current || (err != nil) != tc.refuse {
				t.Fatalf("current=%v err=%v", current, err)
			}
		})
	}
}
