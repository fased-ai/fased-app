//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPairedBundleIsSelectedWithoutNativePackages(t *testing.T) {
	id := bootstrap.PackageMaintenanceIdentity{
		MaintenanceIdentity: bootstrap.MaintenanceIdentity{InstallerSHA256: strings.Repeat("a", 64)},
		Target:              update.Target{OS: "linux", Arch: runtime.GOARCH},
		Archive:             update.Artifact{File: "fased-app-linux-" + runtime.GOARCH + ".tar.gz", Size: 1, SHA256: strings.Repeat("b", 64)},
		BundleSHA256:        strings.Repeat("c", 64),
		Signer:              bundle.File{SHA256: strings.Repeat("d", 64), Bytes: 1},
	}
	native, paired, err := packageFormatsForHost(id)
	if err != nil || native != nil || paired == nil || paired.Archive != id.Archive || paired.InstallerSHA256 != id.InstallerSHA256 {
		t.Fatal("paired bundle was not selected", native, paired, err)
	}
	for _, mutate := range []func(*bootstrap.PackageMaintenanceIdentity){
		func(v *bootstrap.PackageMaintenanceIdentity) { v.Target.Arch = "wrong" },
		func(v *bootstrap.PackageMaintenanceIdentity) { v.Signer.SHA256 = "" },
		func(v *bootstrap.PackageMaintenanceIdentity) { v.Deb.File = "foreign.deb" },
	} {
		bad := id
		mutate(&bad)
		if _, _, err := packageFormatsForHost(bad); err == nil {
			t.Fatal("foreign or incomplete bundle accepted")
		}
	}
}

func TestPackageProvisionAllowsBrowserOnlyWithoutDesktop(t *testing.T) {
	installerHash := strings.Repeat("a", 64)
	a := maintenance.Authority{ImageSHA256: installerHash}
	id := bootstrap.MaintenanceIdentity{Version: "0.1.12", Source: strings.Repeat("b", 40), InstallerSHA256: installerHash}
	desktop, err := acquirePackageProvisionDesktop(context.Background(), &bootstrap.Prepared{}, a, t.TempDir(), id)
	if err != nil || desktop != nil {
		t.Fatal("a browser-only package required an unprovided desktop", desktop, err)
	}
	a.ImageSHA256 = strings.Repeat("c", 64)
	if _, err := acquirePackageProvisionDesktop(context.Background(), &bootstrap.Prepared{}, a, t.TempDir(), id); err == nil {
		t.Fatal("a different installer was treated as the authenticated browser-only helper")
	}
}

func TestPackageCandidateTrustRequiresPinnedAttestation(t *testing.T) {
	oldBinding, oldOptions := compiledFirstInstallBinding, compiledMaintenanceOptions
	t.Cleanup(func() { compiledFirstInstallBinding, compiledMaintenanceOptions = oldBinding, oldOptions })
	compiledMaintenanceOptions = nil
	id := strings.Repeat("a", 32)
	seed, err := json.Marshal(bootstrap.Seed{Binding: bootstrap.Binding{Version: 4, Purpose: bootstrap.AttestedPurpose, Repository: "fased-ai/fased-app", Channel: "stable"}, SigningProfile: bootstrap.GitHubAttestationProfile})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(seed)
	binding := &firstInstallBinding{Seed: seed, SHA256: hex.EncodeToString(sum[:])}
	compiledFirstInstallBinding = binding
	if got, err := packageCandidateTrust(1400, "local", id); err != nil || got != binding {
		t.Fatal("the compiled native package trust was refused", err)
	}
	if _, err := packageCandidateTrust(1400, "local", "../foreign"); err == nil {
		t.Fatal("a foreign package identity selected trust")
	}
	wrong := *binding
	wrong.SHA256 = strings.Repeat("b", 64)
	compiledFirstInstallBinding = &wrong
	if _, err := packageCandidateTrust(1400, "local", id); err == nil {
		t.Fatal("a substituted compiled seed was accepted")
	}
}

func TestPackageMaintenanceEntryRejectsOtherArgumentsAndUnprivilegedCalls(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, args := range [][]string{
		{"--owner", "1400", "--location", "local", "--instance", "../" + id},
		{"--owner", "01400", "--location", "local", "--instance", id},
		{"--owner", "1400", "--location", "remote", "--instance", strings.ToUpper(id)},
		{"--owner", "1400", "--location", "local", "--instance", id, "--path", "/tmp"},
	} {
		if _, _, _, err := packageMaintenanceArguments(args); err == nil {
			t.Fatalf("foreign package identity accepted: %v", args)
		}
		for _, command := range []string{"maintenance-peer", "maintenance-app"} {
			if err := run(append([]string{command}, args...), strings.NewReader(""), io.Discard, io.Discard); err == nil {
				t.Fatalf("%s accepted a foreign package identity: %v", command, args)
			}
		}
	}
	if os.Geteuid() == 0 {
		t.Skip("ordinary-owner refusal requires an unprivileged test runner")
	}
	args := []string{"--owner", "1400", "--location", "local", "--instance", id}
	transition := append(append([]string{"maintenance-package-transition"}, args...), "--previous-helper", strings.Repeat("b", 64))
	if err := run(transition, strings.NewReader(""), io.Discard, io.Discard); err == nil || err.Error() != "administrator package helper transition required" {
		t.Fatalf("unprivileged helper transition returned %v", err)
	}
	if err := run(append([]string{"maintenance-package-register"}, args...), strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("unprivileged registration was accepted")
	}
	if err := run(append([]string{"maintenance-package-request"}, args...), strings.NewReader(`{"version":1,"action":"status"}`), io.Discard, io.Discard); err == nil {
		t.Fatal("unprivileged request was accepted")
	}
	if err := run(append(append([]string{"maintenance-package-gate"}, args...), "--role", "root"), strings.NewReader(""), io.Discard, io.Discard); err == nil {
		t.Fatal("invalid service gate role was accepted")
	}
	for _, command := range [][]string{
		append([]string{"maintenance-package-recover"}, args...),
		append(append([]string{"maintenance-package-worker"}, args...), "--operation", id),
	} {
		if err := run(command, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatal("an ordinary process entered the protected package worker", command[0])
		}
	}
}

func TestPackageWorkerRequiresTheOriginalManualOperationAndCandidate(t *testing.T) {
	instance, operation := strings.Repeat("1", 32), strings.Repeat("2", 32)
	manifest := custodyinstall.Manifest{Owner: 1400, ID: instance, Location: "local"}
	installed := update.MaintenancePackage{Sequence: 11, ReleaseSHA256: strings.Repeat("a", 64)}
	release := core.ApplicationRelease{EngineSHA256: strings.Repeat("b", 64), Version: "0.1.12", Sequence: 12, ManifestSHA256: strings.Repeat("c", 64), Target: update.Target{OS: "linux", Arch: "amd64"}}
	policy := core.UpdatePolicy{Mode: "manual"}
	view := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Policy: policy, Operation: core.ApplicationUpdateOperation{ID: operation, Phase: "applying", Release: &release, PolicySHA256: update.SnapshotDigest("application-update-policy", policy)}}}
	candidate := maintenance.CandidateReceipt{Version: 1, Owner: manifest.Owner, Scope: "update", PacketSHA256: release.ManifestSHA256, InstallerSHA256: strings.Repeat("d", 64), EngineSHA256: release.EngineSHA256, ProductVersion: release.Version, Sequence: release.Sequence, ReleaseSHA256: release.ManifestSHA256, Target: release.Target, BaseSequence: installed.Sequence, BaseReleaseSHA256: installed.ReleaseSHA256, BaseInstallation: instance, BaseManifestSHA: update.SnapshotDigest("candidate-base-manifest:v1", manifest), ExpiresAt: 200}
	if _, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, nil, time.Unix(100, 0)); err != nil {
		t.Fatal("the original manual update was refused", err)
	}
	for name, change := range map[string]func(*core.ApplicationOwnerView, *maintenance.CandidateReceipt){
		"automatic": func(v *core.ApplicationOwnerView, _ *maintenance.CandidateReceipt) { v.View.Operation.Automatic = true },
		"other operation": func(v *core.ApplicationOwnerView, _ *maintenance.CandidateReceipt) {
			v.View.Operation.ID = strings.Repeat("3", 32)
		},
		"other predecessor": func(_ *core.ApplicationOwnerView, c *maintenance.CandidateReceipt) {
			c.BaseInstallation = strings.Repeat("4", 32)
		},
		"other core": func(_ *core.ApplicationOwnerView, c *maintenance.CandidateReceipt) {
			c.EngineSHA256 = strings.Repeat("e", 64)
		},
		"other release": func(_ *core.ApplicationOwnerView, c *maintenance.CandidateReceipt) {
			c.ReleaseSHA256 = strings.Repeat("f", 64)
		},
		"expired admission": func(_ *core.ApplicationOwnerView, c *maintenance.CandidateReceipt) { c.ExpiresAt = 100 },
	} {
		t.Run(name, func(t *testing.T) {
			v, c := view, candidate
			change(&v, &c)
			if _, err := approvedPackageCandidate(v, operation, installed, manifest, c, nil, time.Unix(100, 0)); err == nil {
				t.Fatal("a different operation or candidate was admitted")
			}
		})
	}
}

func TestPackageCandidateResumesOnlyTheOriginallyBoundOperationAfterExpiry(t *testing.T) {
	instance, operation := strings.Repeat("1", 32), strings.Repeat("2", 32)
	manifest := custodyinstall.Manifest{Owner: 1400, ID: instance, Location: "local"}
	installed := update.MaintenancePackage{Sequence: 11, ReleaseSHA256: strings.Repeat("a", 64)}
	release := core.ApplicationRelease{EngineSHA256: strings.Repeat("b", 64), Version: "0.1.12", Sequence: 12, ManifestSHA256: strings.Repeat("c", 64), Target: update.Target{OS: "linux", Arch: "amd64"}}
	policy := core.UpdatePolicy{Mode: "manual"}
	view := core.ApplicationOwnerView{View: core.ApplicationUpdateView{Policy: policy, Operation: core.ApplicationUpdateOperation{ID: operation, Phase: "applying", Release: &release, PolicySHA256: update.SnapshotDigest("application-update-policy", policy)}}}
	candidate := maintenance.CandidateReceipt{Version: 1, Owner: manifest.Owner, Scope: "update", PacketSHA256: release.ManifestSHA256, InstallerSHA256: strings.Repeat("d", 64), EngineSHA256: release.EngineSHA256, ProductVersion: release.Version, Sequence: release.Sequence, ReleaseSHA256: release.ManifestSHA256, Target: release.Target, BaseSequence: installed.Sequence, BaseReleaseSHA256: installed.ReleaseSHA256, BaseInstallation: instance, BaseManifestSHA: update.SnapshotDigest("candidate-base-manifest:v1", manifest), ExpiresAt: 200}
	request, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, nil, time.Unix(199, 0))
	if err != nil {
		t.Fatalf("the original update was not admitted: %v", err)
	}
	bound := packageCandidateOperation(candidate, request)
	if _, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, &bound, time.Unix(201, 0)); err != nil {
		t.Fatalf("the originally bound update could not resume after expiry: %v", err)
	}
	if _, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, nil, time.Unix(201, 0)); err == nil {
		t.Fatal("an expired candidate authorized a new operation")
	}
	// A new release check after the old admission expired can authorize a
	// subsequent manual Apply of the same, independently verified candidate.
	fresh := view
	fresh.View.Operation.ID = strings.Repeat("9", 32)
	fresh.View.Operation.CheckedAt = 201
	var verified bootstrap.PackageMaintenanceIdentity
	verified.Version, verified.InstallerSHA256 = candidate.ProductVersion, candidate.InstallerSHA256
	verified.Core.SHA256, verified.Sequence = candidate.EngineSHA256, candidate.Sequence
	verified.ReleaseSHA256, verified.Target = candidate.ReleaseSHA256, candidate.Target
	admitted, err := readmitCheckedPackageCandidate(fresh, candidate, nil, &verified, time.Unix(202, 0))
	if err != nil || candidate.ExpiresAt != 200 {
		t.Fatal("the original candidate receipt was changed or could not be rechecked", err)
	}
	freshRequest, err := approvedPackageCandidate(fresh, fresh.View.Operation.ID, installed, manifest, admitted, nil, time.Unix(202, 0))
	if err != nil {
		t.Fatalf("a fresh manual check could not retry the retained candidate: %v", err)
	}
	automatic := fresh
	automatic.View.Operation.Automatic = true
	if _, err := approvedPackageCandidate(automatic, automatic.View.Operation.ID, installed, manifest, admitted, nil, time.Unix(202, 0)); err == nil {
		t.Fatal("a rechecked candidate authorized an automatic update")
	}
	freshBound := packageCandidateOperation(candidate, freshRequest)
	if _, err := approvedPackageCandidate(fresh, fresh.View.Operation.ID, installed, manifest, candidate, &freshBound, time.Unix(2000, 0)); err != nil {
		t.Fatal("the original rechecked operation could not recover after expiry", err)
	}
	for name, check := range map[string]struct {
		view     core.ApplicationOwnerView
		verified *bootstrap.PackageMaintenanceIdentity
		now      time.Time
	}{
		"missing proof": {fresh, nil, time.Unix(202, 0)},
		"old check":     {view, &verified, time.Unix(202, 0)},
		"future check":  {fresh, &verified, time.Unix(200, 0)},
		"expired check": {fresh, &verified, time.Unix(1101, 0)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readmitCheckedPackageCandidate(check.view, candidate, nil, check.verified, check.now); err == nil {
				t.Fatal("an unverified or stale check readmitted the expired candidate")
			}
		})
	}
	foreign := verified
	foreign.Core.SHA256 = strings.Repeat("8", 64)
	if _, err := readmitCheckedPackageCandidate(fresh, candidate, nil, &foreign, time.Unix(202, 0)); err == nil {
		t.Fatal("a different verified release readmitted the candidate")
	}
	for name, change := range map[string]func(*maintenance.CandidateOperation){
		"another operation": func(b *maintenance.CandidateOperation) { b.OperationID = strings.Repeat("3", 32) },
		"another request":   func(b *maintenance.CandidateOperation) { b.RequestSHA256 = strings.Repeat("4", 64) },
		"another candidate": func(b *maintenance.CandidateOperation) { b.PacketSHA256 = strings.Repeat("5", 64) },
		"another owner":     func(b *maintenance.CandidateOperation) { b.Owner++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := bound
			change(&changed)
			if _, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, &changed, time.Unix(201, 0)); err == nil {
				t.Fatal("a different binding reused the retained candidate")
			}
		})
	}
}

func TestPackageCandidateOperationRefusesUnprotectedFile(t *testing.T) {
	dir := t.TempDir()
	operation := strings.Repeat("2", 32)
	bound := maintenance.CandidateOperation{Version: 1, Owner: os.Getuid(), Scope: "update", PacketSHA256: strings.Repeat("a", 64), OperationID: operation, RequestSHA256: strings.Repeat("b", 64)}
	body, err := json.Marshal(bound)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "candidate-operation.json"), body, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = readPackageCandidateOperation(dir, os.Getuid(), operation); err == nil {
		t.Fatal("an owner-writable candidate binding was accepted")
	}
}

func TestPackageWorkerAcquiresTheNextApprovedRelease(t *testing.T) {
	old := maintenance.CandidateReceipt{Scope: "update", ReleaseSHA256: strings.Repeat("a", 64)}
	expected := core.ApplicationRelease{ManifestSHA256: strings.Repeat("b", 64)}
	if !packageNeedsAcquisition(old, expected) {
		t.Fatal("an earlier completed release prevented acquisition of the next release")
	}
	old.ReleaseSHA256 = expected.ManifestSHA256
	if packageNeedsAcquisition(old, expected) {
		t.Fatal("the exact retained release was acquired a second time")
	}
	old.Scope = "fresh"
	if !packageNeedsAcquisition(old, expected) {
		t.Fatal("the fresh-install candidate was mistaken for an update admission")
	}
}

func TestPackageApplicationPeerOnlyAcceptsItsOriginalEmptySignerOperation(t *testing.T) {
	const owner = 1400
	instance, operation := strings.Repeat("1", 32), strings.Repeat("2", 32)
	root, err := update.PackageInstanceRoot(owner, "local", instance)
	if err != nil {
		t.Fatal(err)
	}
	m := custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance, Enrollment: true, Location: "local", ID: instance, Owner: owner, Root: root, Listen: "127.0.0.1:7331", CoreHash: strings.Repeat("a", 64), SignerHash: strings.Repeat("b", 64), App: custodyinstall.Identity{UID: 2001}, Signer: custodyinstall.Identity{UID: 2002}}
	base, _ := json.Marshal(m)
	previous := update.MaintenancePackage{Version: "0.1.11", Source: strings.Repeat("c", 40), Sequence: 11, ReleaseSHA256: strings.Repeat("d", 64), ProvenanceSHA256: strings.Repeat("e", 64), Components: []update.Component{
		{Version: "0.1.11", Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-app", Size: 1, SHA256: m.CoreHash}},
		{Version: "0.1.11", Contract: signer.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-signer", Size: 1, SHA256: m.SignerHash}},
	}}
	previous.NativePackage = &update.NativePackage{Family: "dnf", Artifact: update.Artifact{File: "fased-app-linux-amd64.rpm", Size: 1, SHA256: strings.Repeat("f", 64)}, InstallerSHA256: strings.Repeat("9", 64)}
	target := previous
	target.Sequence++
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: operation, Phase: "prepared", Start: "base", Base: base, Target: base, PreviousPackage: &previous, Package: &target, OwnerLease: operation}
	cfg := signer.Config{OwnerUID: owner, AppUID: m.App.UID, SignerUID: m.Signer.UID, SignerID: instance}
	q := maintenance.AppMaintenanceRequest{Action: "application-claim", OperationID: operation}
	read := func() (update.MaintenanceRecord, error) { return r, nil }
	if err = validatePackageApplicationPeerRequest("local", instance, cfg, q, read); err != nil {
		t.Fatal("the original empty-signer operation was refused", err)
	}
	for _, bad := range []maintenance.AppMaintenanceRequest{
		{Action: "application-claim", OperationID: strings.Repeat("3", 32)},
		{Action: "application-finish", OperationID: operation},
		{Action: "application-claim", OperationID: operation, ProbeID: strings.Repeat("4", 32)},
	} {
		if err = validatePackageApplicationPeerRequest("local", instance, cfg, bad, read); err == nil {
			t.Fatal("foreign or premature application action was accepted", bad.Action)
		}
	}
	cfg.Network = "mainnet-beta"
	if err = validatePackageApplicationPeerRequest("local", instance, cfg, q, read); err == nil {
		t.Fatal("a key-bearing signer entered empty-signer recovery")
	}
}

func TestFreshPackageCandidateRetirementRequiresOriginalIdlePair(t *testing.T) {
	const owner = 1400
	id := strings.Repeat("1", 32)
	root, err := update.PackageInstanceRoot(owner, "local", id)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%d/local/%s", owner, id)))
	name := "fs-" + hex.EncodeToString(h[:])[:24]
	m := custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance, Enrollment: true, Location: "local", ID: id, Owner: owner, Root: root, Listen: "127.0.0.1:7332", CoreHash: strings.Repeat("a", 64), SignerHash: strings.Repeat("b", 64), App: custodyinstall.Identity{Name: name + "-a", UID: 2001, GID: 2001}, Signer: custodyinstall.Identity{Name: name + "-s", UID: 2002, GID: 2002}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := &update.MaintenancePackage{Version: "0.1.11", Source: strings.Repeat("c", 40), Sequence: 11, ReleaseSHA256: strings.Repeat("d", 64), ProvenanceSHA256: strings.Repeat("e", 64), Components: []update.Component{
		{Version: "0.1.11", Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-app", Size: 1, SHA256: m.CoreHash}},
		{Version: "0.1.11", Contract: signer.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-signer", Size: 1, SHA256: m.SignerHash}},
	}}
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: id, Phase: "idle", Start: "base", Base: raw, Target: raw, Package: p}
	a := maintenance.Authority{Version: 1, Owner: owner, Profile: "local", ImageSHA256: strings.Repeat("f", 64)}
	receipt := maintenance.CandidateReceipt{Version: 1, Owner: owner, Scope: "fresh", PacketSHA256: p.ReleaseSHA256, InstallerSHA256: a.ImageSHA256, EngineSHA256: m.CoreHash, ProductVersion: p.Version, Sequence: p.Sequence, ReleaseSHA256: p.ReleaseSHA256, Target: update.Target{OS: "linux", Arch: "amd64"}}
	if err = freshPackageCandidateMatches(a, "local", id, receipt, r); err != nil {
		t.Fatal("matching initial installation refused", err)
	}
	// The same archived initial pair remains evidence after helper rotation,
	// even though the transition journal records a later completed package.
	p.Bundle = &update.LinuxBundle{
		Archive:        update.Artifact{File: "fased-app-linux-amd64.tar.gz", Size: 1, SHA256: strings.Repeat("a", 64)},
		ManifestSHA256: strings.Repeat("b", 64), InstallerSHA256: a.ImageSHA256,
	}
	active := a
	active.ImageSHA256 = strings.Repeat("9", 64)
	lineage := update.PackageHelperTransition{Version: 1, Owner: owner, Instance: id,
		PreviousHelper: a.ImageSHA256, NextHelper: active.ImageSHA256, JournalSHA256: strings.Repeat("8", 64)}
	verified := false
	verify := func(got maintenance.Authority) error {
		if got != a {
			t.Fatalf("unexpected predecessor helper: %+v", got)
		}
		verified = true
		return nil
	}
	read := func(uid int, location, instance string) (update.PackageHelperTransition, error) {
		if uid != owner || location != a.Profile || instance != id {
			t.Fatalf("foreign archive lookup: %d %s %s", uid, location, instance)
		}
		return lineage, nil
	}
	if err = archivedPackageCandidateMatches(active, "local", id, receipt, r, read, verify); err != nil || !verified {
		t.Fatal("valid predecessor archive refused", err)
	}
	if err = freshPackageCandidateMatches(active, "local", id, receipt, r); err == nil {
		t.Fatal("historical permission leaked into live candidate retirement")
	}
	lineage.NextHelper = strings.Repeat("7", 64)
	verified = false
	if err = archivedPackageCandidateMatches(active, "local", id, receipt, r, read, verify); err == nil || verified {
		t.Fatal("an unrelated helper lineage authorized archive validation")
	}

	// Chained transitions (H0 -> H1 -> H2) preserve archived initial H0 evidence
	// via Ancestors while live candidate retirement under active H2 still rejects H0.
	h1 := strings.Repeat("8", 64)
	chainedLineage := update.PackageHelperTransition{
		Version: 1, Owner: owner, Instance: id,
		PreviousHelper: h1, NextHelper: active.ImageSHA256,
		Ancestors:     []string{a.ImageSHA256},
		JournalSHA256: strings.Repeat("6", 64),
	}
	verified = false
	readChained := func(uid int, location, instance string) (update.PackageHelperTransition, error) {
		return chainedLineage, nil
	}
	if err = archivedPackageCandidateMatches(active, "local", id, receipt, r, readChained, verify); err != nil || !verified {
		t.Fatal("valid chained ancestor archive refused", err)
	}
	receiptUnknown := receipt
	receiptUnknown.InstallerSHA256 = strings.Repeat("5", 64)
	verified = false
	if err = archivedPackageCandidateMatches(active, "local", id, receiptUnknown, r, readChained, verify); err == nil || verified {
		t.Fatal("unrecorded helper unexpectedly matched chained archive lineage")
	}

	configured := m
	configured.Network = "devnet"
	configuredRaw, err := json.Marshal(configured)
	if err != nil {
		t.Fatal(err)
	}
	configuredRecord := r
	configuredRecord.Base, configuredRecord.Target = configuredRaw, configuredRaw
	if err = freshPackageCandidateMatches(a, "local", id, receipt, configuredRecord); err != nil {
		t.Fatal("wallet setup must not strand the original public candidate", err)
	}
	for name, change := range map[string]func(*maintenance.Authority, *maintenance.CandidateReceipt, *update.MaintenanceRecord){
		"different helper": func(a *maintenance.Authority, _ *maintenance.CandidateReceipt, _ *update.MaintenanceRecord) {
			a.ImageSHA256 = strings.Repeat("9", 64)
		},
		"different release": func(_ *maintenance.Authority, c *maintenance.CandidateReceipt, _ *update.MaintenanceRecord) {
			c.ReleaseSHA256 = strings.Repeat("8", 64)
		},
		"different pair": func(_ *maintenance.Authority, c *maintenance.CandidateReceipt, _ *update.MaintenanceRecord) {
			c.EngineSHA256 = strings.Repeat("7", 64)
		},
		"unfinished operation": func(_ *maintenance.Authority, _ *maintenance.CandidateReceipt, r *update.MaintenanceRecord) {
			r.Phase = "prepared"
		},
		"retained hold": func(_ *maintenance.Authority, _ *maintenance.CandidateReceipt, r *update.MaintenanceRecord) {
			r.Installation = &update.Installation{}
		},
	} {
		t.Run(name, func(t *testing.T) {
			badA, badReceipt, badRecord := a, receipt, r
			change(&badA, &badReceipt, &badRecord)
			if freshPackageCandidateMatches(badA, "local", id, badReceipt, badRecord) == nil {
				t.Fatal("foreign or unfinished operation accepted")
			}
		})
	}
}

func TestCompletedPackageCandidateRetirementRequiresOriginalCompletion(t *testing.T) {
	const owner = 1400
	id, operation := strings.Repeat("1", 32), strings.Repeat("2", 32)
	root, err := update.PackageInstanceRoot(owner, "local", id)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%d/local/%s", owner, id)))
	name := "fs-" + hex.EncodeToString(h[:])[:24]
	base := custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance, Enrollment: true, Location: "local", ID: id, Owner: owner, Root: root, Listen: "127.0.0.1:7332", CoreHash: strings.Repeat("a", 64), SignerHash: strings.Repeat("b", 64), App: custodyinstall.Identity{Name: name + "-a", UID: 2001, GID: 2001}, Signer: custodyinstall.Identity{Name: name + "-s", UID: 2002, GID: 2002}}
	target := base
	target.CoreHash, target.SignerHash = strings.Repeat("c", 64), strings.Repeat("d", 64)
	previous := update.MaintenancePackage{Version: "0.1.11", Source: strings.Repeat("e", 40), Sequence: 11, ReleaseSHA256: strings.Repeat("f", 64), ProvenanceSHA256: strings.Repeat("a", 64), NativePackage: &update.NativePackage{Family: "dnf", Artifact: update.Artifact{File: "fased-app-linux-amd64.rpm", Size: 1, SHA256: strings.Repeat("b", 64)}, InstallerSHA256: strings.Repeat("c", 64)}, Components: []update.Component{
		{Version: "0.1.11", Contract: core.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-app", Size: 1, SHA256: base.CoreHash}},
		{Version: "0.1.11", Contract: signer.OrdinaryUpdateContract(), Artifact: update.Artifact{File: "fased-signer", Size: 1, SHA256: base.SignerHash}},
	}}
	p := previous
	p.Sequence, p.ReleaseSHA256 = 12, strings.Repeat("4", 64)
	p.Components = append([]update.Component(nil), previous.Components...)
	p.Components[0].Artifact.SHA256, p.Components[1].Artifact.SHA256 = target.CoreHash, target.SignerHash
	snapshot := update.CoreSnapshot{Version: 1, BootID: strings.Repeat("5", 32), ProbeID: strings.Repeat("6", 32), InstanceID: "fixture-instance", State: update.State{Name: "core-db", Version: core.StateSchema, Anchor: strings.Repeat("7", 64)}}
	request := &update.CorePauseRequest{Version: 1, IntentID: operation, Sequence: p.Sequence, ManifestSHA256: p.ReleaseSHA256, Expected: snapshot}
	checkpoint := &update.ApplicationCheckpoint{Version: 1, OperationID: operation, ManifestSHA256: p.ReleaseSHA256, PreviousEngineSHA256: base.CoreHash, Pause: update.CorePauseReceipt{Version: 1, State: "paused", PauseID: strings.Repeat("8", 32), Request: request, Core: snapshot}, Snapshot: update.MetadataSnapshot{Size: 64, SHA256: strings.Repeat("9", 64)}}
	checkpoint.Snapshot.File = "application-" + checkpoint.Snapshot.SHA256 + ".sqlite"
	baseJSON, _ := json.Marshal(base)
	targetJSON, _ := json.Marshal(target)
	r := update.MaintenanceRecord{Version: 1, Owner: owner, OperationID: operation, Phase: "completed", Start: "target", Base: baseJSON, Target: targetJSON, PreviousPackage: &previous, Package: &p, OwnerLease: operation, Application: checkpoint}
	a := maintenance.Authority{Owner: owner, Profile: "local", ImageSHA256: strings.Repeat("c", 64)}
	receipt := maintenance.CandidateReceipt{Version: 1, Owner: owner, Scope: "update", PacketSHA256: p.ReleaseSHA256, InstallerSHA256: p.NativePackage.InstallerSHA256, EngineSHA256: target.CoreHash, ProductVersion: p.Version, Sequence: p.Sequence, ReleaseSHA256: p.ReleaseSHA256, Target: update.Target{OS: "linux", Arch: "amd64"}, BaseSequence: previous.Sequence, BaseReleaseSHA256: previous.ReleaseSHA256, BaseInstallation: id, BaseManifestSHA: update.SnapshotDigest("candidate-base-manifest:v1", base), ExpiresAt: 1}
	if err = completedPackageCandidateMatches(a, "local", id, receipt, r); err != nil {
		t.Fatal("the original completed candidate was refused", err)
	}
	if err = matchCompletedPackageCandidate(a, "local", id, receipt, r, custodyinstall.SelectArchivedPackageCompletion); err != nil {
		t.Fatal("completed app-only archive was refused", err)
	}
	held := r
	held.Phase, held.Start = "held", ""
	if _, err = custodyinstall.SelectArchivedPackageCompletion(held, a.Owner, "local", id); err == nil {
		t.Fatal("held journal was accepted as completed archive")
	}

	for name, change := range map[string]func(*maintenance.CandidateReceipt, *update.MaintenanceRecord){
		"unfinished": func(_ *maintenance.CandidateReceipt, r *update.MaintenanceRecord) { r.Phase, r.Start = "held", "" },
		"wrong predecessor": func(c *maintenance.CandidateReceipt, _ *update.MaintenanceRecord) {
			c.BaseReleaseSHA256 = strings.Repeat("d", 64)
		},
		"different installer": func(c *maintenance.CandidateReceipt, _ *update.MaintenanceRecord) {
			c.InstallerSHA256 = strings.Repeat("e", 64)
		},
		"missing checkpoint": func(_ *maintenance.CandidateReceipt, r *update.MaintenanceRecord) { r.Application = nil },
		"another operation": func(_ *maintenance.CandidateReceipt, r *update.MaintenanceRecord) {
			r.OperationID = strings.Repeat("f", 32)
		},
	} {
		t.Run(name, func(t *testing.T) {
			badReceipt, badRecord := receipt, r
			change(&badReceipt, &badRecord)
			if completedPackageCandidateMatches(a, "local", id, badReceipt, badRecord) == nil {
				t.Fatal("foreign or unfinished candidate was retired")
			}
		})
	}
}

func TestPackageCandidateRequiresAnOriginalReservation(t *testing.T) {
	parent := t.TempDir()
	directory, reservation := filepath.Join(parent, "candidate"), filepath.Join(parent, "reservation.json")
	if err := requirePackageCandidateReservation(directory, reservation); err != nil {
		t.Fatal("new candidate was refused", err)
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	unique := filepath.Join(directory, "preserve")
	if err := os.WriteFile(unique, []byte("unclassified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requirePackageCandidateReservation(directory, reservation); err == nil {
		t.Fatal("a new reservation would classify an existing directory")
	}
	if contents, err := os.ReadFile(unique); err != nil || string(contents) != "unclassified" {
		t.Fatal("unclassified directory changed", err)
	}
	if err := os.WriteFile(reservation, []byte("present"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requirePackageCandidateReservation(directory, reservation); err != nil {
		t.Fatal("reserved retry was refused before protected reservation validation", err)
	}
}

func TestPackageWorkerGeneralAutomaticRequiresAllowAutomaticAndPin(t *testing.T) {
	const owner = 1400
	instance, operation := strings.Repeat("1", 32), strings.Repeat("2", 32)
	target := update.Target{OS: "linux", Arch: runtime.GOARCH}
	installed := update.MaintenancePackage{Version: "0.1.24", Sequence: 1000025, ReleaseSHA256: strings.Repeat("a", 64)}
	release := core.ApplicationRelease{
		EngineSHA256:   strings.Repeat("b", 64),
		Version:        "0.1.25",
		Sequence:       1000026,
		ManifestSHA256: strings.Repeat("c", 64),
		Target:         target,
	}
	policy := core.UpdatePolicy{
		Mode: "automatic",
		Pin:  &core.UpdatePin{Sequence: "1000026", ManifestSHA256: release.ManifestSHA256},
	}
	for _, location := range []string{"local", "remote"} {
		t.Run(location, func(t *testing.T) {
			manifest := custodyinstall.Manifest{Layout: custodyinstall.PackageLayout, Owner: owner, ID: instance, Location: location}
			view := core.ApplicationOwnerView{
				View: core.ApplicationUpdateView{
					Policy: policy,
					Operation: core.ApplicationUpdateOperation{
						ID:           operation,
						Phase:        "applying",
						Release:      &release,
						PolicySHA256: update.SnapshotDigest("application-update-policy", policy),
						Automatic:    true,
					},
				},
			}
			candidate := maintenance.CandidateReceipt{
				Version:           1,
				Owner:             owner,
				Scope:             "update",
				AllowAutomatic:    false,
				PacketSHA256:      release.ManifestSHA256,
				InstallerSHA256:   strings.Repeat("d", 64),
				EngineSHA256:      release.EngineSHA256,
				ProductVersion:    release.Version,
				Sequence:          release.Sequence,
				ReleaseSHA256:     release.ManifestSHA256,
				Target:            target,
				BaseSequence:      installed.Sequence,
				BaseReleaseSHA256: installed.ReleaseSHA256,
				BaseInstallation:  instance,
				BaseManifestSHA:   update.SnapshotDigest("candidate-base-manifest:v1", manifest),
				ExpiresAt:         200,
			}
			if _, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, nil, time.Unix(100, 0)); err == nil {
				t.Fatal("manual-only candidate was admitted for general Automatic")
			}
			candidate.AllowAutomatic = true
			if q, err := approvedPackageCandidate(view, operation, installed, manifest, candidate, nil, time.Unix(100, 0)); err != nil || q.OperationID != operation {
				t.Fatalf("allowAutomatic candidate was refused: %v", err)
			}
			wrongPin := view
			wrongPin.View.Policy.Pin = &core.UpdatePin{Sequence: "1000099", ManifestSHA256: release.ManifestSHA256}
			wrongPin.View.Operation.PolicySHA256 = update.SnapshotDigest("application-update-policy", wrongPin.View.Policy)
			if _, err := approvedPackageCandidate(wrongPin, operation, installed, manifest, candidate, nil, time.Unix(100, 0)); err == nil {
				t.Fatal("mismatched version pin was admitted")
			}
			wallet := manifest
			wallet.Network = "devnet"
			candidate.BaseManifestSHA = update.SnapshotDigest("candidate-base-manifest:v1", wallet)
			if _, err := approvedPackageCandidate(view, operation, installed, wallet, candidate, nil, time.Unix(100, 0)); err == nil {
				t.Fatal("wallet-configured package admitted Automatic")
			}
		})
	}
}
