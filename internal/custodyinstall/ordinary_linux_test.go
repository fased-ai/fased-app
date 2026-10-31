//go:build linux

package custodyinstall

import (
	"bytes"
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
	"strings"
	"testing"
)

func TestOrdinaryInstalledContextAndLegacyRefusal(t *testing.T) {
	legacy := fixtureManifest()
	before := mustJSON(legacy)
	if bytes.Contains(before, []byte("profile")) || bytes.Contains(before, []byte("provenance")) {
		t.Fatal("legacy bytes changed")
	}
	m := legacy
	m.Version = 3
	m.Mode = signer.OrdinaryCustodyMode
	m.Network = "devnet"
	m.Profile = signer.OwnerCustodyProfile
	m.Provenance = signer.ControlledCustodyProfile
	m.Enrollment = true
	m.Location = "local"
	if err := m.validate(); err != nil {
		t.Fatal(err)
	}
	if cfg := identityConfig(m); cfg.Validate() != nil || cfg.Profile != m.Profile || cfg.Provenance != m.Provenance {
		t.Fatal("identity context not bound")
	}
	j := installerIntent(m)
	if j.validate(m.Owner) != nil || j.Mode != m.Mode || j.Profile != m.Profile || j.Provenance != m.Provenance {
		t.Fatal("completion lost the ordinary context")
	}
	identity := EnrollmentIdentity(m)
	other := m
	other.Provenance = signer.OwnerKeyProvenance
	if identity == EnrollmentIdentity(other) {
		t.Fatal("controlled provenance promoted by enrollment")
	}
	if (InstallTransaction{Version: 1, Base: m}).valid() {
		t.Fatal("legacy maintenance adopted ordinary custody")
	}
	o := Options{Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance, StageOnly: true, Disposable: true, PrepareHost: true}
	if _, err := Install(t.Context(), o, nil); !errors.Is(err, signer.ErrUnavailable) {
		t.Fatal("normal admission crossed the host boundary", err)
	}
	if !bytes.Equal(before, mustJSON(legacy)) {
		t.Fatal("old manifest mutated")
	}
}
func TestOrdinaryInstalledReadyRejectsForeignClass(t *testing.T) {
	m := fixtureManifest()
	m.Version = 3
	m.Mode = signer.OrdinaryCustodyMode
	m.Network = "devnet"
	m.Profile = signer.OwnerCustodyProfile
	m.Provenance = signer.ControlledCustodyProfile
	s := signer.Status{FixtureOnly: true, AdmissionHeld: true, Custody: &signer.CustodyAdmission{Version: 1, Class: m.Mode, Profile: m.Profile, Provenance: m.Provenance}}
	if !signerStatusReady(m, s) {
		t.Fatal("bound empty ordinary signer unavailable")
	}
	s.Custody.Profile = signer.ControlledCustodyProfile
	if signerStatusReady(m, s) {
		t.Fatal("wrong policy reported ready")
	}
	s.Custody.Profile = m.Profile
	ref := signer.AccountRef{Version: 3, SignerID: m.ID, WalletID: strings.Repeat("b", 32), Epoch: strings.Repeat("c", 32), PublicKey: strings.Repeat("a", 64), Network: m.Network}
	s.Account = &ref
	s.Custody.Origin = "generated"
	s.RecoveryRequired = true
	if !signerStatusReady(m, s) {
		t.Fatal("bound key held unavailable")
	}
	ref.Version = 2
	if signerStatusReady(m, s) {
		t.Fatal("v2 key promoted")
	}
	ref.Version = 3
	s.RecoveryRequired = false
	if signerStatusReady(m, s) {
		t.Fatal("key recovery hold erased")
	}
}
