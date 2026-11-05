//go:build linux

package providersecrets

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
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

func serviceACLFixture(uid uint32) []byte {
	b := make([]byte, 44)
	binary.LittleEndian.PutUint32(b, 2)
	for i, e := range [][3]uint32{{1, 4, ^uint32(0)}, {2, 4, uid}, {4, 0, ^uint32(0)}, {16, 4, ^uint32(0)}, {32, 0, ^uint32(0)}} {
		o := 4 + i*8
		binary.LittleEndian.PutUint16(b[o:], uint16(e[0]))
		binary.LittleEndian.PutUint16(b[o+2:], uint16(e[1]))
		binary.LittleEndian.PutUint32(b[o+4:], e[2])
	}
	return b
}

func TestNativeSystemdNamedUserACL(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("ordinary credential reader required")
	}
	f, err := os.CreateTemp(t.TempDir(), "disposable-credential")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.Write(bytes.Repeat([]byte{17}, 32)); err != nil {
		t.Fatal(err)
	}
	if err = f.Chmod(0400); err != nil {
		t.Fatal(err)
	}
	private, err := readServiceCredential(f)
	clear(private)
	if err != nil {
		t.Fatal("private owner mode refused", err)
	}
	if err = unix.Fsetxattr(int(f.Fd()), "system.posix_acl_access", serviceACLFixture(uint32(os.Geteuid())), 0); err != nil {
		t.Fatal("local ACL fixture unavailable", err)
	}
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0440 {
		t.Fatal("ACL mask not represented in stat mode", err)
	}
	key, err := readServiceCredential(f)
	defer clear(key)
	if err != nil || len(key) != 32 {
		t.Fatal("systemd named-user credential ACL refused", err)
	}
	if err = unix.Fremovexattr(int(f.Fd()), "system.posix_acl_access"); err != nil {
		t.Fatal(err)
	}
	if err = f.Chmod(0440); err != nil {
		t.Fatal(err)
	}
	if key, err = readServiceCredential(f); err == nil {
		clear(key)
		t.Fatal("generic group access accepted")
	}
}

func TestNativeSystemdACLRejectsExtraAuthority(t *testing.T) {
	uid := uint32(os.Geteuid())
	valid := serviceACLFixture(uid)
	for i := range valid {
		changed := bytes.Clone(valid)
		changed[i] ^= 1
		if exactServiceACL(changed, uid) {
			t.Fatal("changed ACL admitted", i)
		}
	}
	if exactServiceACL(valid[:43], uid) || exactServiceACL(append(bytes.Clone(valid), make([]byte, 8)...), uid) {
		t.Fatal("malformed or extra ACL entry accepted")
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
		if info, statErr := os.Stat(filepath.Join(os.Getenv("CREDENTIALS_DIRECTORY"), CredentialName)); statErr == nil {
			if owner, ok := info.Sys().(*syscall.Stat_t); ok {
				t.Logf("runtime metadata only: mode=%#o uid=%d size=%d", info.Mode().Perm(), owner.Uid, info.Size())
			}
		} else {
			t.Log("runtime metadata unavailable", statErr)
		}
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
