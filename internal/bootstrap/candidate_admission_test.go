package bootstrap

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestAttestedContentIdentityKeepsTransportAndLegacySeparate(t *testing.T) {
	d := Descriptor{Binding: Binding{Version: 4}, Source: strings.Repeat("a", 40), BundleSHA256: strings.Repeat("b", 64), Release: ProductionRelease{BuildRef: "refs/heads/main", RunID: "23", RunAttempt: "1"}}
	staged := ApplicationDescriptorIdentity(d)
	d.GitHub = releaseops.GitHubAsset{RepositoryID: "7", ReleaseID: "11", AssetID: "12"}
	if got := ApplicationDescriptorIdentity(d); got != staged {
		t.Fatal("publication transport changed signed-content identity")
	}
	legacy, _ := json.Marshal(d)
	if !ApplicationDescriptorMatches(d, digest(legacy)) || !ApplicationDescriptorMatches(d, staged) {
		t.Fatal("exact verified legacy/canonical identity not recognized")
	}
	changed := d
	changed.Source = strings.Repeat("c", 40)
	if ApplicationDescriptorMatches(changed, staged) || ApplicationDescriptorMatches(changed, digest(legacy)) {
		t.Fatal("different source accepted as same release")
	}
	changed = d
	changed.Release.RunAttempt = "2"
	if ApplicationDescriptorMatches(changed, staged) {
		t.Fatal("different attempt accepted as same release")
	}
	d.Release.BuildRef, d.Release.TagObject = "", strings.Repeat("d", 40)
	legacy, _ = json.Marshal(d)
	if ApplicationDescriptorIdentity(d) != digest(legacy) {
		t.Fatal("original tag-bound identity changed")
	}
}

func TestCandidateAdmissionUsesExistingControllerAndRejectsScopeExpansion(t *testing.T) {
	f, options, payloads := attestedFixture(t, "staged")
	published, err := Prepare(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	defer published.Close()
	candidate := filepath.Join(f.parent, "candidate-input")
	if err = os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range payloads {
		if err = os.WriteFile(filepath.Join(candidate, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	paths := localinstall.Paths{Root: filepath.Join(f.parent, "installed"), Data: filepath.Join(f.parent, "state")}
	for _, path := range []string{paths.Root, paths.Data} {
		if err = os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	old := []byte("inert original application")
	if err = os.WriteFile(filepath.Join(paths.Root, "fased-app"), old, 0700); err != nil {
		t.Fatal(err)
	}
	unit := []byte("inert owned unit")
	if err = os.WriteFile(filepath.Join(paths.Root, "fased-app.service"), unit, 0600); err != nil {
		t.Fatal(err)
	}
	m := localinstall.Manifest{Schema: 1, Phase: "ready", Version: "old", Root: paths.Root, Data: paths.Data, Files: map[string]string{"fased-app": digest(old), "fased-app.service": digest(unit)}}
	if err = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), m); err != nil {
		t.Fatal(err)
	}
	a := CandidateAdmission{PacketSHA256: digest(payloads[AttestedPacketFile]), OperationID: strings.Repeat("1", 32), BaselineSHA256: localinstall.ManifestSHA256(m), ExpiresAt: f.now.Add(10 * time.Minute).Unix()}
	o := Options{Seed: options.Seed, SeedSHA256: options.SeedSHA256, Directory: filepath.Join(f.parent, "admitted"), now: options.now}
	p, err := PrepareAttestedCandidate(t.Context(), o, candidate, a)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r, err := p.ApplicationRelease()
	publicRelease, publicErr := published.ApplicationRelease()
	if err != nil || publicErr != nil || *r != *publicRelease {
		t.Fatal("staged/public content identity differs", r, publicRelease, err, publicErr)
	}
	legacy := *r
	raw, _ := json.Marshal(published.descriptor)
	legacy.ManifestSHA256 = digest(raw)
	if err = published.BindApplicationRelease(legacy); err != nil {
		t.Fatal("original pending v2 lease cannot be retained", err)
	}
	if got, e := published.ApplicationRelease(); e != nil || *got != legacy {
		t.Fatal("legacy lease identity changed", got, e)
	}
	legacy.EngineSHA256 = strings.Repeat("f", 64)
	if err = published.BindApplicationRelease(legacy); err == nil {
		t.Fatal("alias expanded executable authority")
	}
	if err = p.Run(t.Context(), InstallOptions{}, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("candidate acquired fresh-install/exec authority")
	}
	snapshot := update.CoreSnapshot{Version: 1, ProbeID: update.NewProbe(), BootID: update.NewProbe(), InstanceID: "disposable-existing", State: update.State{Name: "core-db", Version: 33, Features: []string{"accounts-v1"}, Anchor: strings.Repeat("a", 64)}}
	pause := update.CorePauseRequest{Version: 1, IntentID: a.OperationID, Sequence: r.Sequence, ManifestSHA256: r.ManifestSHA256, Expected: snapshot}
	paused := update.CorePauseReceipt{Version: 1, State: "paused", PauseID: update.NewProbe(), Request: &pause, Core: snapshot}
	if _, err = p.ApplicationReaderBootstrapRequest(a.OperationID, update.NewProbe(), digest(old), paused); err != nil {
		t.Fatal("exact reader operation refused", err)
	}
	pause.IntentID = update.NewProbe()
	if _, err = p.ApplicationReaderBootstrapRequest(pause.IntentID, update.NewProbe(), digest(old), paused); err == nil {
		t.Fatal("candidate expanded reader-pause authority")
	}
	h := localinstall.ApplicationUpdateCallbacks{BeforeStop: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return nil }, Health: func(context.Context, localinstall.Manifest) error { return nil }, RollbackAllowed: func(context.Context, localinstall.Manifest, localinstall.Manifest) error { return nil }, Complete: func(context.Context, localinstall.Manifest, localinstall.ApplicationUpdateStatus) error { return nil }}
	driver, err := p.LocalApplicationController(paths, h)
	if err != nil {
		t.Fatal(err)
	}
	if got, e := driver.Check(t.Context()); e != nil || got == nil || *got != *r {
		t.Fatal("existing controller did not admit exact candidate", got, e)
	}
	for _, operation := range []core.ApplicationUpdateOperation{{ID: strings.Repeat("2", 32), Release: r}, {ID: a.OperationID, Automatic: true, Release: r}} {
		if _, err = driver.Apply(t.Context(), operation); err == nil {
			t.Fatal("candidate expanded operation or Automatic authority")
		}
	}
	got, err := os.ReadFile(filepath.Join(paths.Root, "fased-app"))
	if err != nil || string(got) != string(old) {
		t.Fatal("rejected admission changed installed bytes", err)
	}
	p.candidate.BaselineSHA256 = strings.Repeat("e", 64)
	if _, err = p.LocalApplicationController(paths, h); err == nil {
		t.Fatal("another installation baseline accepted")
	}
	p.candidate.BaselineSHA256 = a.BaselineSHA256
	p.options.now = func() time.Time { return time.Unix(a.ExpiresAt, 0) }
	if _, err = driver.Check(t.Context()); err == nil {
		t.Fatal("expired candidate remained usable")
	}
	if _, err = PrepareAttestedCandidate(t.Context(), o, candidate, a); err == nil {
		t.Fatal("existing output overwritten")
	}
	o.Directory = filepath.Join(f.parent, "bad-packet")
	a.PacketSHA256 = strings.Repeat("e", 64)
	if _, err = PrepareAttestedCandidate(t.Context(), o, candidate, a); err == nil {
		t.Fatal("unreviewed packet accepted")
	}
	if _, err = os.Stat(o.Directory); !os.IsNotExist(err) {
		t.Fatal("failed admission left output", err)
	}

	// A successful staged update and an existing legacy-v2 update must both
	// remain current when the real publication acquires its transport IDs.
	p.options.now = options.now
	target := m
	target.Files = map[string]string{"fased-app": p.engine.SHA256, "fased-app.service": digest(unit)}
	target.Version, target.ApplicationSequence = r.Version, r.Sequence
	code, err := os.ReadFile(filepath.Join(p.report.BundleDirectory, "fased-app"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(paths.Root, "fased-app"), code, 0700); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []string{r.ManifestSHA256, digest(raw)} {
		target.ApplicationReleaseSHA256 = identity
		if err = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), target); err != nil {
			t.Fatal(err)
		}
		publicDriver, e := published.LocalApplicationController(paths, h)
		if e != nil {
			t.Fatal(e)
		}
		before, _ := os.ReadFile(filepath.Join(paths.Root, "install.json"))
		if got, e := publicDriver.Check(t.Context()); e != nil || got != nil {
			t.Fatal("exact canonical/legacy installed identity reoffered", identity, got, e)
		}
		after, _ := os.ReadFile(filepath.Join(paths.Root, "install.json"))
		if string(before) != string(after) {
			t.Fatal("no-op rewrote legacy installation")
		}
	}
	target.ApplicationReleaseSHA256 = r.ManifestSHA256
	if err = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), target); err != nil {
		t.Fatal(err)
	}
	status := localinstall.ApplicationUpdateStatus{Version: 1, ID: a.OperationID, Phase: "completed", Sequence: r.Sequence, ReleaseSHA256: r.ManifestSHA256, EngineSHA256: r.EngineSHA256, PreviousManifestSHA256: a.BaselineSHA256, TargetManifestSHA256: localinstall.ManifestSHA256(target)}
	journalDir := filepath.Join(filepath.Dir(paths.Root), "."+filepath.Base(paths.Root)+"-updates")
	if err = os.MkdirAll(journalDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = core.WritePrivateJSON(filepath.Join(journalDir, "current.json"), map[string]any{"status": status, "previous": m, "target": target}); err != nil {
		t.Fatal(err)
	}
	current, err := p.LocalApplicationController(paths, h)
	if err != nil {
		t.Fatal("same-operation completed proof not recognized", err)
	}
	if got, e := current.Check(t.Context()); e != nil || got != nil {
		t.Fatal("completed staged operation did not report current", got, e)
	}
	status.Phase = "healthy-held"
	if err = p.CheckCandidateInstallation(localinstall.ManifestSHA256(target), target.Root, target.Data, status); err != nil {
		t.Fatal("exact interrupted target cannot reach retained recovery", err)
	}
	status.ReleaseSHA256 = strings.Repeat("f", 64)
	if err = p.CheckCandidateInstallation(localinstall.ManifestSHA256(target), target.Root, target.Data, status); err == nil {
		t.Fatal("different interrupted content admitted")
	}
	status.ReleaseSHA256 = r.ManifestSHA256
	status.Phase = "rolled-back"
	if err = p.CheckCandidateInstallation(a.BaselineSHA256, m.Root, m.Data, status); err == nil {
		t.Fatal("rolled-back candidate reused its consumed operation")
	}
	status.Phase = "healthy-held"
	status.ID = strings.Repeat("3", 32)
	if err = p.CheckCandidateInstallation(localinstall.ManifestSHA256(target), target.Root, target.Data, status); err == nil {
		t.Fatal("another operation's completion expanded admission")
	}
	// Compile the actual maintainer entry against disposable trust and call it
	// after completion with no running core/session. Repetition must remain a
	// read-only no-op, before session preparation or the reader resume path.
	entryFixture := filepath.Join(f.parent, "entry-test.json")
	admission := *p.candidate
	admission.ExpiresAt = a.ExpiresAt
	if err = core.WritePrivateJSON(entryFixture, map[string]any{"seed": options.Seed, "seedSha256": options.SeedSHA256, "directory": candidate, "parent": filepath.Join(f.parent, "entry-acquisition"), "local": paths, "admission": admission}); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "go", "test", "-tags=fased_candidate_acceptance", "./cmd/fased-app", "-run", "^TestCandidateAcceptanceCompletedFixture$", "-count=1")
	command.Dir = filepath.Join("..", "..")
	command.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "FASED_CANDIDATE_ENTRY_FIXTURE="+entryFixture)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("actual completed entry: %v %s", err, out)
	}
}
