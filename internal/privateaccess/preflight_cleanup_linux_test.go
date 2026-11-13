//go:build linux

package privateaccess

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightCleanupRefusesSubstitutedDirectory(t *testing.T) {
	base := t.TempDir()
	foreign := filepath.Join(base, "foreign")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(foreign, "tailscale.sock")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "fased-private-replaced")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	if f, err := openPreflightDirectory(link); err == nil || f != nil {
		t.Fatal("substituted parent accepted")
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "preserve" {
		t.Fatal("foreign file changed", err)
	}
	if f, err := openPreflightDirectory(filepath.Join(base, "missing")); err != nil || f != nil {
		t.Fatal("missing directory not treated as absent", err)
	}
}

func TestPreflightCleanupRemovesOnlyPinnedSocket(t *testing.T) {
	base := t.TempDir()
	probe := filepath.Join(base, "fased-private-original")
	if err := os.Mkdir(probe, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(probe, "tailscale.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	f, err := openPreflightDirectory(probe)
	if err != nil || f == nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = removePreflightDirectory(f, probe); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(probe); !os.IsNotExist(err) {
		t.Fatal("owned directory not removed", err)
	}
}

func TestClosedPreflightListenerCannotUnlinkReplacement(t *testing.T) {
	base := t.TempDir()
	probe := filepath.Join(base, "fased-private-original")
	foreign := filepath.Join(base, "foreign")
	if err := os.Mkdir(probe, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(probe, "tailscale.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := openPreflightDirectory(probe)
	if err != nil || f == nil {
		t.Fatal(err)
	}
	if err = removePreflightDirectory(f, probe); err != nil {
		t.Fatal(err)
	}
	f.Close()
	marker := filepath.Join(foreign, "tailscale.sock")
	if err = os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(foreign, probe); err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	if b, err := os.ReadFile(marker); err != nil || string(b) != "preserve" {
		t.Fatal("second close changed replacement", err)
	}
}
