package custodyinstall

import (
	"strings"
	"testing"
)

func TestPackageInstanceIdentityIsSeparateFromLegacyAndOtherProfiles(t *testing.T) {
	for _, location := range []string{"local", "remote"} {
		m := Manifest{Layout: PackageLayout, Version: 3, Mode: "ordinary-custody:v1", Profile: "owner-authorized:v1", Provenance: "owner-input:v1", Enrollment: true, Location: location, Owner: 1000, ID: strings.Repeat("a", 32), Listen: "127.0.0.1:17331", CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64)}
		if location == "remote" {
			m.PublicOrigin = "https://owner.example.ts.net"
		}
		m.Root, _ = PackageRoot(m.Owner, m.Location, m.ID)
		m.App = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "app"), UID: 2001, GID: 2001}
		m.Signer = Identity{Name: packageAccountName(m.Owner, m.Location, m.ID, "signer"), UID: 2002, GID: 2002}
		if err := m.validatePackage(); err != nil {
			t.Fatalf("valid %s instance: %v", location, err)
		}
		if m.Root == Root(m.Owner) || m.validate() == nil {
			t.Fatal("package instance was admitted to the legacy controller")
		}
		for _, unit := range definitions(m) {
			if strings.Contains(string(unit), "fixture") || strings.Contains(string(unit), "disposable") {
				t.Fatal("package instance exposes a fixture label")
			}
		}
		signerUnit := string(definitions(m)["fased-signer.service"])
		if strings.Contains(signerUnit, "Restart=on-failure") || strings.Contains(signerUnit, "WantedBy=multi-user.target") {
			t.Fatal("an unconfigured signer must not be enabled or restarted")
		}
		for _, change := range []func(*Manifest){
			func(v *Manifest) { v.Owner++ },
			func(v *Manifest) { v.ID = strings.Repeat("d", 32) },
			func(v *Manifest) { v.Root = Root(v.Owner) },
			func(v *Manifest) { v.Root += "/../foreign" },
			func(v *Manifest) { v.App.Name = accountName(v.Owner, "app") },
			func(v *Manifest) { v.Signer = v.App },
			func(v *Manifest) { v.Provenance = "controlled-disposable:v1" },
			func(v *Manifest) { v.Network = "unknown" },
			func(v *Manifest) { v.NativeOwnerAccess = true },
			func(v *Manifest) { v.Layout = "" },
		} {
			bad := m
			change(&bad)
			if bad.validatePackage() == nil {
				t.Fatal("foreign or incomplete instance identity accepted")
			}
		}
	}
}

func TestPackageRootRejectsForeignIdentity(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, row := range []struct {
		owner    int
		location string
		id       string
	}{{0, "local", id}, {1000, "../local", id}, {1000, "local", "../" + id}, {1000, "local", strings.ToUpper(id)}} {
		if _, err := PackageRoot(row.owner, row.location, row.id); err == nil {
			t.Fatal("invalid owner, profile or instance identity accepted")
		}
	}
}

func TestPackageServiceIdentityIsBoundToInstance(t *testing.T) {
	id := strings.Repeat("a", 32)
	local := Manifest{Layout: PackageLayout, Owner: 1000, Location: "local", ID: id}
	remote := local
	remote.Location = "remote"
	other := local
	other.ID = strings.Repeat("b", 32)
	for _, role := range []string{"app", "signer"} {
		name := instanceUnit(local, role)
		if name == Unit(local.Owner, role) || name == instanceUnit(remote, role) || name == instanceUnit(other, role) {
			t.Fatalf("%s service is not isolated by instance", role)
		}
		account := packageAccountName(local.Owner, local.Location, local.ID, role)
		if len(account) > 32 || account == packageAccountName(remote.Owner, remote.Location, remote.ID, role) || account == packageAccountName(other.Owner, other.Location, other.ID, role) {
			t.Fatalf("%s account is not isolated by instance", role)
		}
	}
}
