//go:build linux

package custodyinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrationManifestFixture(id string) Manifest {
	return Manifest{
		Layout:       PackageLayout,
		Version:      3,
		Mode:         "ordinary-custody:v1",
		Profile:      "owner-authorized:v1",
		Provenance:   "owner-input:v1",
		Enrollment:   true,
		Location:     "remote",
		PublicOrigin: "https://controlled.owner.ts.net",
		ID:           id,
		Owner:        1000,
		Root:         "/var/lib/fased-app-package/1000/remote/" + id,
		Listen:       "127.0.0.1:17490",
		CoreHash:     strings.Repeat("c", 64),
		SignerHash:   strings.Repeat("d", 64),
		App:          Identity{Name: packageAccountName(1000, "remote", id, "app"), UID: 1005, GID: 1005},
		Signer:       Identity{Name: packageAccountName(1000, "remote", id, "signer"), UID: 1006, GID: 1006},
	}
}

func TestPlanPackageUnitMigration(t *testing.T) {
	t.Parallel()
	id := strings.Repeat("a", 32)
	m := migrationManifestFixture(id)
	oldDefs := legacyPackageDefinitions(m)
	newDefs := definitions(m)
	appStored := filepath.Join(m.Root, "fased-app.service")
	signerStored := filepath.Join(m.Root, "fased-signer.service")
	appSystemd := filepath.Join("/etc/systemd/system", instanceUnit(m, "app"))
	signerSystemd := filepath.Join("/etc/systemd/system", instanceUnit(m, "signer"))

	t.Run("all legacy produces 4 migration steps", func(t *testing.T) {
		files := map[string]hostFile{
			appStored:     {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-app.service"]},
			signerStored:  {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-signer.service"]},
			appSystemd:    {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-app.service"]},
			signerSystemd: {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-signer.service"]},
		}
		steps, err := planPackageUnitMigration(m, func(p string) (hostFile, error) {
			f, ok := files[p]
			if !ok {
				return hostFile{}, os.ErrNotExist
			}
			return f, nil
		})
		if err != nil || len(steps) != 4 {
			t.Fatalf("expected 4 steps, got len=%d err=%v", len(steps), err)
		}
	})

	t.Run("partial resume skips already hardened units", func(t *testing.T) {
		files := map[string]hostFile{
			appStored:     {UID: 0, Regular: true, Mode: 0644, Data: newDefs["fased-app.service"]},
			signerStored:  {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-signer.service"]},
			appSystemd:    {UID: 0, Regular: true, Mode: 0644, Data: newDefs["fased-app.service"]},
			signerSystemd: {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-signer.service"]},
		}
		steps, err := planPackageUnitMigration(m, func(p string) (hostFile, error) {
			return files[p], nil
		})
		if err != nil || len(steps) != 2 || steps[0].Path != signerStored || steps[1].Path != signerSystemd {
			t.Fatalf("unexpected partial steps: %+v (%v)", steps, err)
		}
	})

	t.Run("all hardened is a clean no-op", func(t *testing.T) {
		files := map[string]hostFile{
			appStored:     {UID: 0, Regular: true, Mode: 0644, Data: newDefs["fased-app.service"]},
			signerStored:  {UID: 0, Regular: true, Mode: 0644, Data: newDefs["fased-signer.service"]},
			appSystemd:    {UID: 0, Regular: true, Mode: 0644, Data: newDefs["fased-app.service"]},
			signerSystemd: {UID: 0, Regular: true, Mode: 0644, Data: newDefs["fased-signer.service"]},
		}
		steps, err := planPackageUnitMigration(m, func(p string) (hostFile, error) {
			return files[p], nil
		})
		if err != nil || len(steps) != 0 {
			t.Fatalf("expected 0 steps, got len=%d err=%v", len(steps), err)
		}
	})

	t.Run("tampered unit rejected", func(t *testing.T) {
		files := map[string]hostFile{
			appStored:     {UID: 0, Regular: true, Mode: 0644, Data: []byte("[Service]\nExecStart=/bin/sh\n")},
			signerStored:  {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-signer.service"]},
			appSystemd:    {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-app.service"]},
			signerSystemd: {UID: 0, Regular: true, Mode: 0644, Data: oldDefs["fased-signer.service"]},
		}
		if _, err := planPackageUnitMigration(m, func(p string) (hostFile, error) {
			return files[p], nil
		}); err == nil {
			t.Fatal("tampered unit accepted for migration")
		}
	})
}
