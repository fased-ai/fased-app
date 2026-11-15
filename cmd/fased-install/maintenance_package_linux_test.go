//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

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
