//go:build linux

package tailnet

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestActivatedListenerRequiresExactListeningUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tailscale.sock")
	original, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	f, err := original.File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if l, err := activatedListener(f, path+"-other"); err == nil {
		l.Close()
		t.Fatal("foreign socket was accepted")
	}
	l, err := activatedListener(f, path)
	if err != nil {
		t.Fatalf("matching listener refused: %v", err)
	}
	defer l.Close()
	file, err := os.CreateTemp(t.TempDir(), "not-a-socket")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if l, err = activatedListener(file, path); err == nil {
		l.Close()
		t.Fatal("ordinary file was accepted as a listener")
	}
}

func TestProtectedSocketUnitFollowsServiceLifecycle(t *testing.T) {
	for _, tc := range []struct{ kind, name, service string }{
		{"vps", "fased-private-vps-1000.socket", "fased-vps-1000.service"},
		{"custody", "fased-private-custody-1000.socket", "fased-custody-1000-app.service"},
	} {
		name, b, err := ProtectedSocketUnit(tc.kind, 1000)
		if err != nil || name != tc.name {
			t.Fatalf("socket identity = %q, %v", name, err)
		}
		for _, want := range []string{"PartOf=" + tc.service, "Before=" + tc.service, "Service=" + tc.service, "SocketMode=0600", "DirectoryMode=0711", "FileDescriptorName=fased-private", "RemoveOnStop=yes"} {
			if !strings.Contains(string(b), want+"\n") {
				t.Fatalf("missing protected socket setting %q", want)
			}
		}
		path, dropin, err := ProtectedSocketDropIn(tc.kind, 1000)
		if err != nil || path != "/etc/systemd/system/"+tc.service+".d/40-fased-private-ingress.conf" {
			t.Fatalf("drop-in identity = %q, %v", path, err)
		}
		core := "/var/lib/fased-app-" + tc.kind + "-1000/fased-app"
		for _, want := range []string{"Requires=" + tc.name, "After=" + tc.name, "Sockets=" + tc.name, "ExecStartPre=" + core + " private-ingress-check --owner 1000 --kind " + tc.kind} {
			if !strings.Contains(string(dropin), want+"\n") {
				t.Fatalf("missing protected socket binding %q", want)
			}
		}
	}
}

func TestActivatedListenerRejectsUntrustedActivation(t *testing.T) {
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "2")
	t.Setenv("LISTEN_FDNAMES", "fased-private")
	if l, active, err := ActivatedListener("/var/lib/fased-app-vps-1000/app-state", 1000); err == nil || !active || l != nil {
		t.Fatal("unexpected descriptor count was accepted")
	}
	t.Setenv("LISTEN_FDS", "1")
	t.Setenv("LISTEN_FDNAMES", "foreign")
	if l, active, err := ActivatedListener("/var/lib/fased-app-vps-1000/app-state", 1000); err == nil || !active || l != nil {
		t.Fatal("foreign descriptor name was accepted")
	}
	if rootOwnedSocket(filepath.Join(t.TempDir(), "tailscale.sock")) {
		t.Fatal("untrusted or absent socket was accepted")
	}
}

func TestProtectedSocketRequiresKnownOwnerAndProfile(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		owner int
	}{{"", 1000}, {"other", 1000}, {"vps", 0}, {"custody", -1}} {
		if path, err := ProtectedSocket(tc.kind, tc.owner); err == nil || path != "" {
			t.Fatalf("foreign identity accepted: %+v", tc)
		}
	}
	if path, err := ProtectedSocket("vps", 1000); err != nil || path != "/run/fased-vps-1000/tailscale.sock" {
		t.Fatalf("protected path = %q, %v", path, err)
	}
}
