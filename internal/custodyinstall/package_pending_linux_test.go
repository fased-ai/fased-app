//go:build linux

package custodyinstall

import (
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestPendingPackageProofBindsOriginalInstallation(t *testing.T) {
	t.Parallel()
	const owner = 1400
	id := strings.Repeat("a", 32)
	release, installer, coreHash := strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)
	receipt := maintenance.CandidateReceipt{Version: 1, Owner: owner, Scope: "fresh", PacketSHA256: release, ReleaseSHA256: release,
		InstallerSHA256: installer, EngineSHA256: coreHash, ProductVersion: "0.1.12", Sequence: 12, Target: update.Target{OS: "linux", Arch: "amd64"}}
	authority := maintenance.Authority{Owner: owner, Profile: "local", ImageSHA256: installer}
	intent := packageProvisionIntent{Version: 1, Owner: owner, Location: "local", Instance: id, Input: PackageProvisionInput{
		Version: receipt.ProductVersion, Source: strings.Repeat("e", 40), ReleaseSHA256: release, ProvenanceSHA256: strings.Repeat("f", 64),
		Sequence: receipt.Sequence, Listen: "127.0.0.1:17331", Core: update.Artifact{File: "fased-app", SHA256: coreHash, Size: 1},
		Signer: update.Artifact{File: "fased-signer", SHA256: strings.Repeat("1", 64), Size: 1},
		Native: update.NativePackage{Family: "dnf", InstallerSHA256: installer, Artifact: update.Artifact{File: "fased-app-linux-amd64.rpm", SHA256: strings.Repeat("2", 64), Size: 1}},
	}}
	if !pendingPackageProof(owner, "local", id, intent, receipt, authority) {
		t.Fatal("the original pending installation was refused")
	}
	paired := intent
	paired.Input.Native = update.NativePackage{}
	paired.Input.Bundle = &update.LinuxBundle{Archive: update.Artifact{File: "fased-app-linux-amd64.tar.gz", Size: 1, SHA256: strings.Repeat("2", 64)}, ManifestSHA256: strings.Repeat("3", 64), InstallerSHA256: installer}
	if !pendingPackageProof(owner, "local", id, paired, receipt, authority) {
		t.Fatal("the original pending bundle installation was refused")
	}
	foreign := paired
	changed := *paired.Input.Bundle
	changed.InstallerSHA256 = strings.Repeat("4", 64)
	foreign.Input.Bundle = &changed
	if pendingPackageProof(owner, "local", id, foreign, receipt, authority) {
		t.Fatal("a pending bundle belonging to another installer was accepted")
	}
	debian := intent
	debian.Input.Native.Family = "apt"
	debian.Input.Native.Artifact.File = "fased-app-linux-amd64.deb"
	if !pendingPackageProof(owner, "local", id, debian, receipt, authority) {
		t.Fatal("the original Debian pending installation was refused")
	}
	for name, mutate := range map[string]func(*packageProvisionIntent, *maintenance.CandidateReceipt, *maintenance.Authority){
		"other instance": func(i *packageProvisionIntent, _ *maintenance.CandidateReceipt, _ *maintenance.Authority) {
			i.Instance = strings.Repeat("3", 32)
		},
		"other owner": func(_ *packageProvisionIntent, r *maintenance.CandidateReceipt, _ *maintenance.Authority) { r.Owner++ },
		"other helper": func(_ *packageProvisionIntent, _ *maintenance.CandidateReceipt, a *maintenance.Authority) {
			a.ImageSHA256 = strings.Repeat("3", 64)
		},
		"other core": func(i *packageProvisionIntent, _ *maintenance.CandidateReceipt, _ *maintenance.Authority) {
			i.Input.Core.SHA256 = strings.Repeat("3", 64)
		},
		"update instead of fresh": func(_ *packageProvisionIntent, r *maintenance.CandidateReceipt, _ *maintenance.Authority) {
			r.Scope = "update"
		},
		"public listener": func(i *packageProvisionIntent, _ *maintenance.CandidateReceipt, _ *maintenance.Authority) {
			i.Input.Listen = "0.0.0.0:17331"
		},
		"wrong package family": func(i *packageProvisionIntent, _ *maintenance.CandidateReceipt, _ *maintenance.Authority) {
			i.Input.Native.Family = "apt"
		},
	} {
		t.Run(name, func(t *testing.T) {
			i, r, a := intent, receipt, authority
			mutate(&i, &r, &a)
			if pendingPackageProof(owner, "local", id, i, r, a) {
				t.Fatal("a different pending installation was accepted")
			}
		})
	}
}
