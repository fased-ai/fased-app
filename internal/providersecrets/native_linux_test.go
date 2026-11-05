//go:build linux

package providersecrets

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

type testDesktopSecret struct {
	Session           dbus.ObjectPath
	Parameters, Value []byte
	ContentType       string
}
type testDesktopKeyring struct {
	mu                sync.Mutex
	key               []byte
	locked, duplicate bool
}

func (*testDesktopKeyring) OpenSession(string, dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	return dbus.MakeVariant(""), "/org/freedesktop/secrets/session/test", nil
}
func (k *testDesktopKeyring) SearchItems(map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	item := dbus.ObjectPath("/org/freedesktop/secrets/item/test")
	if k.key == nil {
		return nil, nil, nil
	}
	if k.locked {
		return nil, []dbus.ObjectPath{item}, nil
	}
	if k.duplicate {
		return []dbus.ObjectPath{item, item}, nil, nil
	}
	return []dbus.ObjectPath{item}, nil, nil
}
func (*testDesktopKeyring) ReadAlias(string) (dbus.ObjectPath, *dbus.Error) {
	return "/org/freedesktop/secrets/collection/test", nil
}
func (k *testDesktopKeyring) Get(string, string) (dbus.Variant, *dbus.Error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return dbus.MakeVariant(k.locked), nil
}
func (k *testDesktopKeyring) CreateItem(_ map[string]dbus.Variant, s testDesktopSecret, _ bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.key = bytes.Clone(s.Value)
	return "/org/freedesktop/secrets/item/test", "/", nil
}
func (k *testDesktopKeyring) GetSecret(session dbus.ObjectPath) (testDesktopSecret, *dbus.Error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return testDesktopSecret{session, []byte{}, bytes.Clone(k.key), "application/octet-stream"}, nil
}
func (*testDesktopKeyring) Close() *dbus.Error { return nil }

func TestNativeDesktopKeyringWithoutHostCredentials(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("native credential access forbids root")
	}
	tool, e := exec.LookPath("dbus-daemon")
	if e != nil {
		t.Skip("isolated DBus test dependency unavailable")
	}
	cmd := exec.Command(tool, "--session", "--nofork", "--address=unix:path="+filepath.Join(t.TempDir(), "bus"), "--print-address=1")
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	output, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	address, e := bufio.NewReader(output).ReadString('\n')
	if e != nil {
		t.Fatal(e, diagnostic.String())
	}
	address = strings.TrimSpace(address)
	conn, e := dbus.Connect(address)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if reply, e := conn.RequestName("org.freedesktop.secrets", dbus.NameFlagDoNotQueue); e != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatal("isolated keyring", e)
	}
	k := &testDesktopKeyring{}
	for _, entry := range []struct {
		path  dbus.ObjectPath
		iface string
	}{
		{"/org/freedesktop/secrets", "org.freedesktop.Secret.Service"},
		{"/org/freedesktop/secrets/collection/test", "org.freedesktop.Secret.Collection"},
		{"/org/freedesktop/secrets/collection/test", "org.freedesktop.DBus.Properties"},
		{"/org/freedesktop/secrets/item/test", "org.freedesktop.Secret.Item"},
		{"/org/freedesktop/secrets/session/test", "org.freedesktop.Secret.Session"},
	} {
		if e = conn.Export(k, entry.path, entry.iface); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("CREDENTIALS_DIRECTORY", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	native := Native{}
	if _, e = native.Key(context.Background(), "disposable-instance", false); e != ErrMissing {
		t.Fatal("read created a key", e)
	}
	first, e := native.Key(context.Background(), "disposable-instance", true)
	if e != nil || len(first) != 32 {
		t.Fatal("OS key creation", e)
	}
	second, e := native.Key(context.Background(), "disposable-instance", false)
	if e != nil || !bytes.Equal(first, second) {
		t.Fatal("OS key was replaced", e)
	}
	clear(first)
	clear(second)
	k.mu.Lock()
	k.locked = true
	k.mu.Unlock()
	if _, e = native.Key(context.Background(), "disposable-instance", true); e != ErrUnavailable {
		t.Fatal("locked keyring was bypassed", e)
	}
	k.mu.Lock()
	k.locked = false
	k.duplicate = true
	k.mu.Unlock()
	if _, e = native.Key(context.Background(), "disposable-instance", false); e != ErrUnavailable {
		t.Fatal("ambiguous OS identity accepted", e)
	}
}

func TestNativeServiceCredentialRefusesUnsafePaths(t *testing.T) {
	for _, path := range []string{"relative", t.TempDir(), "/run/credentials/../foreign"} {
		t.Run(path, func(t *testing.T) {
			t.Setenv("CREDENTIALS_DIRECTORY", path)
			if _, e := (Native{}).Key(context.Background(), "test", true); e != ErrUnavailable {
				t.Fatal("unsafe credential path accepted", e)
			}
		})
	}
}

// The existing Ubuntu CI job invokes this test as an ordinary-user systemd
// service with a disposable encrypted credential. Never run project code as root.
func TestNativeSystemdRuntimeCredential(t *testing.T) {
	expected := os.Getenv("FASED_NATIVE_EXPECTED_SHA256")
	if expected == "" {
		t.Skip("requires the controlled Ubuntu service fixture")
	}
	if os.Geteuid() == 0 || len(expected) != 64 {
		t.Fatal("ordinary controlled service required")
	}
	key, e := (Native{}).Key(context.Background(), "disposable-native-service", false)
	if e != nil {
		t.Fatal(e)
	}
	defer clear(key)
	h := sha256.Sum256(key)
	if hex.EncodeToString(h[:]) != expected {
		t.Fatal("systemd credential identity changed")
	}
	nonce, ciphertext, e := Seal(key, []byte("controlled synthetic credential"), []byte("fixed-scope"))
	if e != nil {
		t.Fatal(e)
	}
	plain, e := Open(key, nonce, ciphertext, []byte("fixed-scope"))
	if e != nil || string(plain) != "controlled synthetic credential" {
		t.Fatal("native encryption round trip", e)
	}
	clear(plain)
}
