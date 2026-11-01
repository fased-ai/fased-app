package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func finalizerFixture(t *testing.T, profiles ...string) (*firstFixture, Options, ReleaseFinalizerInput) {
	t.Helper()
	root, err := filepath.Abs("../../.evidence/ubuntu-release-finalizer/test-state")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	variant := ""
	if len(profiles) == 1 {
		variant = profiles[0]
	}
	f, o, _ := productionFixture(t, variant)
	if !strings.HasPrefix(f.parent, root+string(os.PathSeparator)) {
		t.Fatal("fixture escaped declared scratch root")
	}
	archive := filepath.Join(f.parent, "candidate.tar.gz")
	if err = os.WriteFile(archive, f.archive, 0600); err != nil {
		t.Fatal(err)
	}
	proof := f.files["/targets/provenance/"+f.descriptor.Release.Provenance.SHA256+".sigstore.json"]
	input := ReleaseFinalizerInput{Seed: o.Seed, SeedSHA256: o.SeedSHA256, ArchiveFile: archive, WorkDirectory: f.parent, Provenance: proof, Plan: ReleasePlan{Version: 1, Descriptor: f.descriptor, PreviousSequence: f.descriptor.Sequence - 1, Versions: ReleaseVersions{Targets: 1, Snapshot: 1, Timestamp: 1}, Expires: ReleaseExpiries{Targets: f.targets.Signed.Expires, Snapshot: f.snapshot.Signed.Expires, Timestamp: f.timestamp.Signed.Expires}}}
	return f, o, input
}
func approveRelease(t *testing.T, f *firstFixture, finalizer *ReleaseFinalizer) ReleaseApprovals {
	t.Helper()
	a := ReleaseApprovals{}
	for _, role := range []string{"targets", "snapshot", "timestamp"} {
		request, err := finalizer.Request(role, a)
		if err != nil {
			t.Fatal(err)
		}
		if request.PayloadSHA256 != digest(request.Payload) || request.Role != role || request.PlanSHA256 == "" {
			t.Fatal("unbound request")
		}
		count := 1
		if role == "targets" {
			count = f.targetSignatures
		}
		var signatures []metadata.Signature
		for _, signer := range f.keys[role][:count] {
			pub, err := signer.PublicKey()
			if err != nil {
				t.Fatal(err)
			}
			key, err := metadata.KeyFromPublicKey(pub)
			if err != nil {
				t.Fatal(err)
			}
			id, err := key.ID()
			if err != nil {
				t.Fatal(err)
			}
			signature, err := signer.SignMessage(bytes.NewReader(request.Payload))
			if err != nil {
				t.Fatal(err)
			}
			signatures = append(signatures, metadata.Signature{KeyID: id, Signature: signature})
		}
		switch role {
		case "targets":
			a.Targets = signatures
		case "snapshot":
			a.Snapshot = signatures
		case "timestamp":
			a.Timestamp = signatures
		}
	}
	return a
}
func TestReleaseFinalizerRoundTrip(t *testing.T) {
	f, o, input := finalizerFixture(t)
	finalizer, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	approvals := approveRelease(t, f, finalizer)
	out, err := finalizer.Finalize(approvals)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Report.MetadataVerified || out.Report.Published || out.Report.Activation {
		t.Fatal("invalid output authority")
	}
	f.files = map[string][]byte{}
	for name, b := range out.Files {
		f.files["/"+name] = b
	}
	o.Directory = filepath.Join(f.parent, "from-finalizer")
	p, err := Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	release, err := p.ApplicationRelease()
	if err != nil || release.Sequence != f.descriptor.Sequence {
		t.Fatal(release, err)
	}
	if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("fixture executable ran")
	}
}

func TestReleaseFinalizerRejectsChangedInputs(t *testing.T) {
	f, _, input := finalizerFixture(t)
	variants := map[string]func(*ReleaseFinalizerInput){
		"wrong-plan-version": func(i *ReleaseFinalizerInput) { i.Plan.Version = 2 },
		"seed-pin":           func(i *ReleaseFinalizerInput) { i.SeedSHA256 = strings.Repeat("0", 64) },
		"binding":            func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.Channel = "development" },
		"sequence-floor":     func(i *ReleaseFinalizerInput) { i.Plan.PreviousSequence = i.Plan.Descriptor.Sequence },
		"minimum-sequence":   func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.Sequence = 1; i.Plan.PreviousSequence = 0 },
		"metadata-replay":    func(i *ReleaseFinalizerInput) { i.Plan.PreviousVersions.Targets = 1 },
		"negative-floor":     func(i *ReleaseFinalizerInput) { i.Plan.PreviousVersions.Snapshot = -1 },
		"version-overflow":   func(i *ReleaseFinalizerInput) { i.Plan.Versions.Timestamp = 1 << 31 },
		"expired":            func(i *ReleaseFinalizerInput) { i.Plan.Expires.Timestamp = time.Now().Add(-time.Hour) },
		"excess-validity": func(i *ReleaseFinalizerInput) {
			i.Plan.Expires.Targets = time.Now().Add(31 * 24 * time.Hour).Truncate(time.Second)
		},
		"child-outlives-parent": func(i *ReleaseFinalizerInput) { i.Plan.Expires.Snapshot = i.Plan.Expires.Targets.Add(time.Hour) },
		"wrong-product-version": func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.ProductVersion = "1.2.4" },
		"wrong-source":          func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.Source = strings.Repeat("f", 40) },
		"asset-name":            func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.GitHub.Name = "other.tar.gz" },
		"asset-size":            func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.GitHub.Size++ },
		"asset-repository":      func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.GitHub.RepositoryID = "99" },
		"asset-release":         func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.GitHub.ReleaseID = "latest" },
		"asset-origin":          func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.GitHub.CDNPath = "/outside/path" },
		"proof":                 func(i *ReleaseFinalizerInput) { i.Provenance = append(append([]byte(nil), i.Provenance...), ' ') },
		"manifest":              func(i *ReleaseFinalizerInput) { i.Plan.Descriptor.BundleSHA256 = strings.Repeat("f", 64) },
	}
	for name, change := range variants {
		t.Run(name, func(t *testing.T) {
			changed := input
			change(&changed)
			if _, err := NewReleaseFinalizer(changed); err == nil {
				t.Fatal("changed input admitted")
			}
		})
	}
	t.Run("engineering-profile", func(t *testing.T) {
		bad, _, _ := productionFixture(t, "engineering-bundle")
		i := input
		i.Seed = jsonBytes(t, bad.seed)
		i.SeedSHA256 = digest(i.Seed)
		i.Plan.Descriptor = bad.descriptor
		i.Provenance = bad.files["/targets/provenance/"+bad.descriptor.Release.Provenance.SHA256+".sigstore.json"]
		i.ArchiveFile = filepath.Join(f.parent, "engineering.tar.gz")
		if err := os.WriteFile(i.ArchiveFile, bad.archive, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewReleaseFinalizer(i); err == nil {
			t.Fatal("engineering archive signed for production")
		}
	})
}
func TestReleaseFinalizerApprovalBoundaries(t *testing.T) {
	f, _, input := finalizerFixture(t)
	finalizer, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	a := approveRelease(t, f, finalizer)
	if _, err = finalizer.Request("root", a); err == nil {
		t.Fatal("root mutation offered")
	}
	if _, err = finalizer.Request("snapshot", ReleaseApprovals{}); err == nil {
		t.Fatal("snapshot before targets quorum")
	}
	if _, err = finalizer.Request("timestamp", ReleaseApprovals{Targets: a.Targets}); err == nil {
		t.Fatal("timestamp before snapshot")
	}
	variants := map[string]func(*ReleaseApprovals){
		"targets-subquorum": func(x *ReleaseApprovals) { x.Targets = x.Targets[:1] },
		"duplicate-key":     func(x *ReleaseApprovals) { x.Targets = []metadata.Signature{x.Targets[0], x.Targets[0]} },
		"wrong-role":        func(x *ReleaseApprovals) { x.Snapshot = x.Timestamp },
		"missing-timestamp": func(x *ReleaseApprovals) { x.Timestamp = nil },
		"bad-signature": func(x *ReleaseApprovals) {
			x.Targets = append([]metadata.Signature(nil), x.Targets...)
			x.Targets[0].Signature = make([]byte, 64)
		},
		"unknown-key": func(x *ReleaseApprovals) {
			x.Targets = append([]metadata.Signature(nil), x.Targets...)
			x.Targets[0].KeyID = strings.Repeat("0", 64)
		},
	}
	for name, change := range variants {
		t.Run(name, func(t *testing.T) {
			v := a
			change(&v)
			if _, err := finalizer.Finalize(v); err == nil {
				t.Fatal("invalid approvals emitted metadata")
			}
		})
	}
	changed := input
	changed.Plan.Descriptor.GitHub.AssetID = "18"
	other, err := NewReleaseFinalizer(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Finalize(a); err == nil {
		t.Fatal("asset locator changed after approval")
	}
	changed = input
	changed.Plan.Expires.Timestamp = changed.Plan.Expires.Timestamp.Add(-time.Minute)
	other, err = NewReleaseFinalizer(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Finalize(a); err == nil {
		t.Fatal("timestamp body changed after approval")
	}
	reordered := a
	reordered.Targets = []metadata.Signature{a.Targets[1], a.Targets[0]}
	first, err := finalizer.Finalize(a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := finalizer.Finalize(reordered)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range first.Files {
		if !bytes.Equal(b, second.Files[name]) {
			t.Fatal("signature arrival order changed output", name)
		}
	}
	if err = os.WriteFile(input.ArchiveFile, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = finalizer.Request("targets", a); err == nil {
		t.Fatal("archive changed before sign request")
	}
	if _, err = finalizer.Finalize(a); err == nil {
		t.Fatal("archive changed before finalization")
	}
}

func TestReleaseFinalizerManualUpdateAdmission(t *testing.T) {
	t.Run("original", func(t *testing.T) { releaseFinalizerManualAdmission(t, "") })
	t.Run("single-owner", func(t *testing.T) { releaseFinalizerManualAdmission(t, SingleOwnerSigningProfile) })
}
func releaseFinalizerManualAdmission(t *testing.T, profile string) {
	f, o, input := finalizerFixture(t, profile)
	finalizer, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	out, err := finalizer.Finalize(approveRelease(t, f, finalizer))
	if err != nil {
		t.Fatal(err)
	}
	f.files = map[string][]byte{}
	for name, b := range out.Files {
		f.files["/"+name] = b
	}
	o.Directory = filepath.Join(f.parent, "update-admission")
	p, err := Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	paths := localinstall.Paths{Root: filepath.Join(f.parent, "installed"), Data: filepath.Join(f.parent, "state")}
	for _, dir := range []string{paths.Root, paths.Data} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	old := []byte("inert original")
	unit := []byte("inert unit")
	if err = os.WriteFile(filepath.Join(paths.Root, "fased-app"), old, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(paths.Root, "fased-app.service"), unit, 0600); err != nil {
		t.Fatal(err)
	}
	manifest := localinstall.Manifest{Schema: 1, Phase: "ready", Version: "old", Root: paths.Root, Data: paths.Data, Files: map[string]string{"fased-app": digest(old), "fased-app.service": digest(unit)}}
	if err = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), manifest); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	h := localinstall.ApplicationUpdateCallbacks{BeforeStop: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { callbacks++; return nil }, Health: func(context.Context, localinstall.Manifest) error { callbacks++; return nil }, RollbackAllowed: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { callbacks++; return nil }, Complete: func(context.Context, localinstall.Manifest, localinstall.ApplicationUpdateStatus) error {
		callbacks++
		return nil
	}}
	driver, err := p.LocalApplicationController(paths, h)
	if err != nil {
		t.Fatal(err)
	}
	release, err := driver.Check(t.Context())
	if err != nil || release == nil || release.Sequence != input.Plan.Descriptor.Sequence || release.ManifestSHA256 != out.Report.DescriptorSHA256 {
		t.Fatal(release, err)
	}
	if callbacks != 0 {
		t.Fatal("admission invoked service callbacks")
	}
	engine, _ := bundle.Names()
	if err = os.WriteFile(filepath.Join(p.report.BundleDirectory, engine), []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = driver.Check(t.Context()); err == nil {
		t.Fatal("changed candidate admitted for update")
	}
	if got, err := os.ReadFile(filepath.Join(paths.Root, "fased-app")); err != nil || !bytes.Equal(got, old) {
		t.Fatal("admission changed installed fixture")
	}
	// No Apply here: that API controls actual user systemd. Unchanged commit/no-op
	// and interrupted-completion behavior is covered by localinstall's inert seam.
}
func TestReleaseFinalizerCLI(t *testing.T) {
	f, _, input := finalizerFixture(t)
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(f.parent, "fased-release-candidate")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, "./cmd/fased-release-candidate")
	build.Dir = repo
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOFLAGS=-mod=readonly")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("CLI build: %v %s", err, output)
	}
	write := func(name string, b []byte) string {
		t.Helper()
		p := filepath.Join(f.parent, name)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("seed.json", input.Seed)
	write("proof.json", input.Provenance)
	candidate := write("candidate.json", jsonBytes(t, map[string]any{"version": 1, "seedFile": "seed.json", "seedSha256": input.SeedSHA256, "archiveFile": "candidate.tar.gz", "provenanceFile": "proof.json", "plan": input.Plan}))
	approvals := ReleaseApprovals{}
	approvalFile := write("approvals.json", jsonBytes(t, approvals))
	invoke := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(t.Context(), binary, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return out, fmt.Errorf("%w: %s", err, stderr.String())
		}
		return out, nil
	}
	for _, role := range []string{"targets", "snapshot", "timestamp"} {
		b, err := invoke("release-request", "--candidate-file", candidate, "--approvals-file", approvalFile, "--work-dir", f.parent, "--role", role)
		if err != nil {
			t.Fatal(err)
		}
		var request ReleaseSigningRequest
		if json.Unmarshal(b, &request) != nil || request.Role != role || request.PayloadSHA256 != digest(request.Payload) {
			t.Fatal("invalid CLI request")
		}
		count := 1
		if role == "targets" {
			count = 2
		}
		var signatures []metadata.Signature
		for _, signer := range f.keys[role][:count] {
			pub, err := signer.PublicKey()
			if err != nil {
				t.Fatal(err)
			}
			key, err := metadata.KeyFromPublicKey(pub)
			if err != nil {
				t.Fatal(err)
			}
			id, err := key.ID()
			if err != nil {
				t.Fatal(err)
			}
			sig, err := signer.SignMessage(bytes.NewReader(request.Payload))
			if err != nil {
				t.Fatal(err)
			}
			signatures = append(signatures, metadata.Signature{KeyID: id, Signature: sig})
		}
		switch role {
		case "targets":
			approvals.Targets = signatures
		case "snapshot":
			approvals.Snapshot = signatures
		case "timestamp":
			approvals.Timestamp = signatures
		}
		write("approvals.json", jsonBytes(t, approvals))
	}
	output := filepath.Join(f.parent, "metadata")
	args := []string{"release-finalize", "--candidate-file", candidate, "--approvals-file", approvalFile, "--work-dir", f.parent, "--output", output}
	result, err := invoke(args...)
	if err != nil {
		t.Fatal(err)
	}
	var report ReleaseFinalizationReport
	if json.Unmarshal(result, &report) != nil || !report.MetadataVerified || report.Published || report.Activation {
		t.Fatal("invalid completion authority")
	}
	finalizer, err := NewReleaseFinalizer(input)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := finalizer.Finalize(approvals)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range expected.Files {
		got, err := os.ReadFile(filepath.Join(output, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("CLI output mismatch", name, err)
		}
	}
	original, err := os.ReadFile(filepath.Join(output, "finalization.json"))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := invoke(args...); err == nil || len(b) != 0 {
		t.Fatal("existing output overwritten")
	}
	got, err := os.ReadFile(filepath.Join(output, "finalization.json"))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("existing output changed")
	}
	for _, flag := range []string{"--private-key", "--tls-ca-file", "--clock"} {
		if b, err := invoke(append(args, flag, "forbidden")...); err == nil || len(b) != 0 {
			t.Fatal("unapproved override", flag)
		}
	}
	approvals.Timestamp = nil
	write("approvals.json", jsonBytes(t, approvals))
	args[len(args)-1] = filepath.Join(f.parent, "invalid-output")
	if b, err := invoke(args...); err == nil || len(b) != 0 {
		t.Fatal("subquorum emitted output")
	}
	if _, err = os.Stat(args[len(args)-1]); !os.IsNotExist(err) {
		t.Fatal("unverified output directory created")
	}
	if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("application fixture ran")
	}
}
