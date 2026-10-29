//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestEnrollmentCustodyProfileAndLegacyBytes(t *testing.T) {
	m := fixtureManifest()
	before := mustJSON(m)
	if bytes.Contains(before, []byte(`"enrollment"`)) || bytes.Contains(before, []byte(`"location"`)) || bytes.Contains(before, []byte(`"publicOrigin"`)) {
		t.Fatal("legacy manifest bytes gained default fields")
	}
	for _, profile := range []string{"local", "remote"} {
		m := fixtureManifest()
		m.Version = 2
		m.Network = "devnet"
		m.Mode = modeFor(m.Network)
		m.Enrollment = true
		m.Location = profile
		if profile == "remote" {
			m.PublicOrigin = "https://controlled.owner.ts.net"
		}
		if err := m.validate(); err != nil {
			t.Fatal(err)
		}
		j := installerIntent(m)
		if err := j.validate(m.Owner); err != nil || !j.Enrollment || j.Location != profile || j.PublicOrigin != m.PublicOrigin {
			t.Fatal("intent lost profile", err)
		}
		unit := string(definitions(m)["fased-app.service"])
		if !strings.Contains(unit, "--instance-location "+profile) || m.PublicOrigin != "" && !strings.Contains(unit, "--public-origin "+m.PublicOrigin) {
			t.Fatal("unit lost profile/origin")
		}
		m.PublicOrigin = "https://controlled.owner.ts.net/extra"
		if m.validate() == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(before, &fields); err != nil {
		t.Fatal(err)
	}
}

func TestEnrollmentRecordRejectsUnpinnedPaths(t *testing.T) {
	s := System{Out: io.Discard}
	for _, path := range []string{"/tmp/enrollment.json", "/proc/1/fd/5/source/enrollment.json", "/proc/1000/fd/5/../source/enrollment.json"} {
		if err := s.EnrollmentRecord(context.Background(), path, strings.Repeat("a", 32), []byte(`{}`)); err == nil {
			t.Fatal("unpinned root record accepted")
		}
	}
}

func TestEnrollmentIdentitySurvivesOnlyVerifiedCodePairChanges(t *testing.T) {
	original := fixtureManifest()
	original.Version, original.Mode, original.Network, original.Enrollment, original.Location = 2, "disposable-custody:v2", "devnet", true, "local"
	updated := original
	updated.CoreHash, updated.SignerHash = strings.Repeat("a", 64), strings.Repeat("b", 64)
	if EnrollmentIdentity(original) != EnrollmentIdentity(updated) {
		t.Fatal("verified code replacement strands enrollment")
	}
	for _, change := range []func(*Manifest){
		func(m *Manifest) { m.Owner++ }, func(m *Manifest) { m.ID = "foreign" }, func(m *Manifest) { m.Root += "-foreign" },
		func(m *Manifest) { m.App.UID++ }, func(m *Manifest) { m.App.GID++ }, func(m *Manifest) { m.App.Name += "x" },
		func(m *Manifest) { m.Signer.UID++ }, func(m *Manifest) { m.Signer.GID++ }, func(m *Manifest) { m.Signer.Name += "x" },
		func(m *Manifest) { m.Listen = "127.0.0.1:19090" }, func(m *Manifest) { m.PublicOrigin = "https://foreign.ts.net" },
		func(m *Manifest) { m.Location = "remote" }, func(m *Manifest) { m.Network = "mainnet-beta" }, func(m *Manifest) { m.Mode = "other" },
		func(m *Manifest) { m.Version++ }, func(m *Manifest) { m.Enrollment = false },
	} {
		changed := updated
		change(&changed)
		if EnrollmentIdentity(original) == EnrollmentIdentity(changed) {
			t.Fatal("foreign custody topology adopted")
		}
	}
}
