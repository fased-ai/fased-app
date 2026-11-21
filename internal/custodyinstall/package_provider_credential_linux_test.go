//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageProviderCredentialPrepareRetainsOriginalEnvelope(t *testing.T) {
	t.Parallel()
	stage := t.TempDir()
	path := filepath.Join(stage, packageTaskCredential)
	var encryptions int
	envelope := bytes.Repeat([]byte{'x'}, 96)
	run := func(_ context.Context, secret []byte, args ...string) ([]byte, error) {
		encryptions++
		if !providerCredentialEncryption(secret, args) || bytes.Equal(secret, make([]byte, 32)) {
			t.Fatal("expected a generated key and the fixed systemd encryption command")
		}
		return envelope, nil
	}
	digest := func(got string, mode os.FileMode, limit int64) (string, int64, error) {
		if got != path || mode != 0600 || limit != packageTaskCredentialLimit {
			t.Fatal("credential must use the fixed protected location and bound")
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", 0, err
		}
		if info.Mode() != mode {
			return "", 0, errors.New("untrusted credential")
		}
		return "", info.Size(), nil
	}
	publish := func(got string, data []byte, mode os.FileMode) error {
		if got != path || !bytes.Equal(data, envelope) || mode != 0600 {
			t.Fatal("only host-encrypted bytes may be published")
		}
		return os.WriteFile(path, data, mode)
	}
	for range 2 {
		if err := preparePackageTaskCredential(t.Context(), stage, run, digest, publish); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := preparePackageTaskCredential(t.Context(), stage, run, digest, publish); err == nil || encryptions != 1 {
		t.Fatal("an interrupted or foreign credential must not be regenerated", err, encryptions)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	invalid := func(context.Context, []byte, ...string) ([]byte, error) { return []byte("invalid"), nil }
	if err := preparePackageTaskCredential(t.Context(), stage, invalid, digest, publish); err == nil {
		t.Fatal("an invalid OS response must not create a credential file")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("encryption failure left a partial credential", err)
	}
}

func TestPackageProviderCredentialDropInIsInstanceAndRoleBound(t *testing.T) {
	t.Parallel()
	m := Manifest{Layout: PackageLayout, Owner: 1000, Location: "local", ID: strings.Repeat("a", 32), App: Identity{UID: 1201, GID: 1201}}
	var err error
	m.Root, err = PackageRoot(m.Owner, m.Location, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := packageTaskCredentialDropIn(m)
	want := "[Service]\nLoadCredentialEncrypted=fased-provider-task-key:" + m.Root + "/fased-provider-task-key.cred\n"
	f := hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte(want)}
	read := func(got string) (hostFile, error) {
		if got != path {
			return hostFile{}, os.ErrNotExist
		}
		return f, nil
	}
	fields := map[string]string{
		"FragmentPath": "/etc/systemd/system/" + instanceUnit(m, "app"), "DropInPaths": path,
		"User": "1201", "Group": "1201", "NoNewPrivileges": "yes", "ProtectSystem": "strict", "NeedDaemonReload": "no",
		"LimitCORE": "0", "ExecStart": "{ path=" + m.Root + "/fased-app ; argv[]=... ; }", "Requires": packageRecoveryUnit(m), "After": packageRecoveryUnit(m),
	}
	if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, nil); err != nil {
		t.Fatal("exact provider credential must be accepted by ordinary app startup", err)
	}
	if allowedPackageServiceDropIns(m, "signer", path, read) || allowedPackageDropIns(path, read) || allowedPackageServiceDropIns(m, "app", path+" "+path, read) {
		t.Fatal("credential must be restricted to the app unit")
	}
	other := m
	other.ID = strings.Repeat("b", 32)
	other.Root, err = PackageRoot(other.Owner, other.Location, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{packageTaskCredentialUnit(other), append([]byte(want), []byte("ExecStartPre=/tmp/foreign\n")...)} {
		f.Data = body
		if err := validateLoadedUnitFields(m, "app", m.Root+"/fased-app", fields, read, nil); err == nil {
			t.Fatal("unit accepted a changed credential drop-in")
		}
	}
	if allowedPackageServiceDropIns(m, "app", packageTaskCredentialDropIn(other), read) {
		t.Fatal("unit accepted another instance's credential file")
	}
}
