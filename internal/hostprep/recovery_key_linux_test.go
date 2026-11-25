//go:build linux

package hostprep

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryKey(t *testing.T) string {
	t.Helper()
	pub, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	name := []byte("ssh-ed25519")
	data := make([]byte, 4+len(name)+4+len(pub))
	binary.BigEndian.PutUint32(data, uint32(len(name)))
	copy(data[4:], name)
	binary.BigEndian.PutUint32(data[4+len(name):], uint32(len(pub)))
	copy(data[8+len(name):], pub)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(data)
}
func TestRecoveryKeyIsOrdinaryExplicitAndPreserved(t *testing.T) {
	home := t.TempDir()
	key := recoveryKey(t)
	if err := prepareRecoveryKey(home, bufio.NewReader(strings.NewReader("n\n")), io.Discard, key); err == nil {
		t.Fatal("declined recovery accepted")
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); !os.IsNotExist(err) {
		t.Fatal("decline changed recovery")
	}
	if err := prepareRecoveryKey(home, bufio.NewReader(strings.NewReader("y\n")), io.Discard, key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".ssh", "authorized_keys")
	before, _ := os.ReadFile(path)
	if err := prepareRecoveryKey(home, nil, io.Discard, recoveryKey(t)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) || string(after) != key+"\n" {
		t.Fatal("existing keys replaced")
	}
	if validRecoveryPublicKey("command=evil "+key) || validRecoveryPublicKey("-----BEGIN OPENSSH PRIVATE KEY-----") || validRecoveryPublicKey(key+"\n"+key) {
		t.Fatal("unsafe key accepted")
	}
}
func TestRecoveryKeyRefusesSymlink(t *testing.T) {
	home := t.TempDir()
	foreign := t.TempDir()
	if err := os.Symlink(foreign, filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}
	if err := prepareRecoveryKey(home, bufio.NewReader(strings.NewReader("y\n")), io.Discard, recoveryKey(t)); err == nil {
		t.Fatal("foreign directory accepted")
	}
	entries, _ := os.ReadDir(foreign)
	if len(entries) != 0 {
		t.Fatal("foreign path changed")
	}
}

func TestRecoveryKeyMultiCandidateSelectsSingleKey(t *testing.T) {
	k1 := recoveryKey(t)
	k2 := recoveryKey(t)
	k3 := recoveryKey(t)
	candidates := k1 + "\n# comment\n" + k2 + "\n" + k1 + "\n"

	for _, tc := range []struct {
		name, input, want string
		wantErr           bool
	}{
		{"default selects first", "\n", k1, false},
		{"explicit index selects second", "2\n", k2, false},
		{"custom pasted key", k3 + "\n", k3, false},
		{"out-of-range index fails closed", "9\n", "", true},
		{"invalid input fails closed", "not-a-key\n", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			err := prepareRecoveryKey(home, bufio.NewReader(strings.NewReader(tc.input)), io.Discard, candidates)
			if tc.wantErr {
				if err == nil {
					t.Fatal("invalid selection accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, rErr := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
			if rErr != nil || string(got) != tc.want+"\n" {
				t.Fatalf("authorized_keys = %q (%v), want %q", string(got), rErr, tc.want+"\n")
			}
		})
	}
}
