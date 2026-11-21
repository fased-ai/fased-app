//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestProtectedEnrollmentIngressPreservesForeignFilesAndRetries(t *testing.T) {
	t.Parallel()
	m := fixtureManifest()
	m.Location, m.PublicOrigin = "remote", "https://fixture.example.ts.net"
	files := map[string]hostFile{}
	active := false
	o := protectedIngressOps{
		probe:  func(context.Context, Manifest) error { return nil },
		active: func(context.Context, Manifest) (bool, error) { return active, nil },
		read: func(path string) (hostFile, error) {
			f, ok := files[path]
			if !ok {
				return f, os.ErrNotExist
			}
			return f, nil
		},
		ensure: func(string) error { return nil },
		publish: func(path string, data []byte, mode os.FileMode) error {
			if f, ok := files[path]; ok && !known(f, data, mode) {
				return os.ErrExist
			}
			files[path] = hostFile{UID: 0, Mode: mode, Regular: true, Data: bytes.Clone(data)}
			return nil
		},
	}
	drop, body, _ := tailnet.ProtectedSocketDropIn("custody", m.Owner)
	unit, socket, _ := tailnet.ProtectedSocketUnit("custody", m.Owner)
	for range 2 {
		if err := prepareProtectedEnrollmentIngress(t.Context(), m, o); err != nil {
			t.Fatal(err)
		}
	}
	if !known(files[drop], body, 0644) || !known(files["/etc/systemd/system/"+unit], socket, 0644) {
		t.Fatal("socket unit was not retained")
	}
	delete(files, drop)
	active = true
	if err := prepareProtectedEnrollmentIngress(t.Context(), m, o); err == nil {
		t.Fatal("active service was modified")
	}
	active = false
	foreign := []byte("foreign override")
	files[drop] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: foreign}
	if err := prepareProtectedEnrollmentIngress(t.Context(), m, o); err == nil || !bytes.Equal(files[drop].Data, foreign) {
		t.Fatal("foreign override was not preserved")
	}
}
