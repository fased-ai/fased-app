//go:build linux

package update

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPackageMaintenanceRecordBindsBothDeployments(t *testing.T) {
	owner, location, id := 1800, "local", strings.Repeat("a", 32)
	root, err := PackageInstanceRoot(owner, location, id)
	if err != nil {
		t.Fatal(err)
	}
	encode := func(owner int, location, id, root string) json.RawMessage {
		b, err := json.Marshal(map[string]any{"version": 3, "layout": PackageInstanceLayout, "enrollment": true, "owner": owner, "location": location, "id": id, "root": root, "coreHash": strings.Repeat("b", 64), "signerHash": strings.Repeat("c", 64), "app": map[string]any{"uid": 1801}, "signer": map[string]any{"uid": 1802}})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	base := encode(owner, location, id, root)
	r := MaintenanceRecord{Owner: owner, Base: base, Target: base}
	if !packageRecordMatchesInstance(r, owner, location, id) {
		t.Fatal("exact package journal refused")
	}
	updated := r
	updated.Target = bytes.Replace(base, []byte(strings.Repeat("b", 64)), []byte(strings.Repeat("d", 64)), 1)
	updated.Target = bytes.Replace(updated.Target, []byte(strings.Repeat("c", 64)), []byte(strings.Repeat("e", 64)), 1)
	if !packageRecordMatchesInstance(updated, owner, location, id) {
		t.Fatal("same instance with updated executable hashes refused")
	}
	expected, err := PackageManifestIdentityFromJSON(base)
	if err != nil || !PackageMaintenanceRecordMatchesIdentity(r, expected) {
		t.Fatal("exact app/signer journal identity refused", err)
	}
	foreign := expected
	foreign.App.UID++
	if PackageMaintenanceRecordMatchesIdentity(r, foreign) {
		t.Fatal("another app identity accepted")
	}
	missingTarget := r
	missingTarget.Target = nil
	if PackageMaintenanceRecordMatchesIdentity(missingTarget, expected) {
		t.Fatal("a missing recovery target was accepted")
	}
	for _, change := range []func(*MaintenanceRecord){
		func(v *MaintenanceRecord) { v.Owner++ },
		func(v *MaintenanceRecord) { v.Base = encode(owner, "remote", id, root) },
		func(v *MaintenanceRecord) { v.Target = encode(owner, location, strings.Repeat("b", 32), root) },
		func(v *MaintenanceRecord) { v.Target = encode(owner, location, id, root+"/../other") },
		func(v *MaintenanceRecord) {
			v.Target = bytes.Replace(base, []byte(`"uid":1801`), []byte(`"uid":1901`), 1)
		},
		func(v *MaintenanceRecord) {
			v.Target = bytes.Replace(base, []byte(`"uid":1802`), []byte(`"uid":1902`), 1)
		},
		func(v *MaintenanceRecord) {
			v.Target = bytes.Replace(base, []byte(`"layout":"linux-package-v1"`), []byte(`"layout":"linux-package-v1","network":"devnet"`), 1)
		},
		func(v *MaintenanceRecord) {
			v.Target = bytes.Replace(base, []byte(`"uid":1801`), []byte(`"uid":1801,"gid":999`), 1)
		},
		func(v *MaintenanceRecord) {
			v.Target = bytes.Replace(base, []byte(`"layout":"linux-package-v1"`), []byte(`"layout":"linux-package-v1","listen":"0.0.0.0:7332"`), 1)
		},
		func(v *MaintenanceRecord) {
			v.Target = bytes.Replace(base, []byte(`"layout":"linux-package-v1"`), []byte(`"layout":"linux-package-v1","CoreHash":"ignored"`), 1)
		},
		func(v *MaintenanceRecord) {
			v.Target = bytes.Replace(base, []byte(`"owner":1800`), []byte(`"owner":1800,"Owner":1800`), 1)
		},
	} {
		bad := r
		change(&bad)
		if packageRecordMatchesInstance(bad, owner, location, id) {
			t.Fatal("foreign package journal accepted")
		}
	}
	if _, err := PackageMaintenanceDirectory(owner, location, id, "../operation"); err == nil {
		t.Fatal("foreign operation path accepted")
	}
}
