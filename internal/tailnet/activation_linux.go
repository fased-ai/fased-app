//go:build linux

package tailnet

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// ProtectedSocket is outside the app-writable state tree. The socket is
// created by systemd and passed to the unprivileged service as an open FD.
func ProtectedSocket(kind string, owner int) (string, error) {
	if instance, ok := strings.CutPrefix(kind, "package-"); ok {
		if _, err := update.PackageInstanceRoot(owner, "remote", instance); err != nil {
			return "", errors.New("protected private ingress identity required")
		}
		return fmt.Sprintf("/run/fased-package-%d-%s/tailscale.sock", owner, instance), nil
	}
	if (kind != "vps" && kind != "custody") || owner <= 0 {
		return "", errors.New("protected private ingress identity required")
	}
	return fmt.Sprintf("/run/fased-%s-%d/tailscale.sock", kind, owner), nil
}

func ProtectedSocketReady(kind string, owner int) bool {
	path, err := ProtectedSocket(kind, owner)
	return err == nil && rootOwnedSocket(path)
}

// ProtectedDataSocketReady binds the protected socket to the exact managed
// data directory and owner; a similarly named directory cannot opt in.
func ProtectedDataSocketReady(data string, owner int) bool {
	kind, _, err := protectedDataKind(data, owner)
	return err == nil && kind != "" && ProtectedSocketReady(kind, owner)
}

// ProtectedPackageData identifies only the exact new Remote package layout.
// It selects no credential or route and never adopts an older installation.
func ProtectedPackageData(data string, owner int) bool {
	_, packageLayout, err := protectedDataKind(data, owner)
	return err == nil && packageLayout
}

func protectedDataKind(data string, owner int) (string, bool, error) {
	if strings.HasPrefix(data, "/var/lib/fased-app-package/") {
		instance := filepath.Base(filepath.Dir(data))
		root, err := update.PackageInstanceRoot(owner, "remote", instance)
		if err != nil || data != filepath.Join(root, "app-state") {
			return "", true, errors.New("exact Remote package ingress identity required")
		}
		return "package-" + instance, true, nil
	}
	if owner > 0 {
		for _, kind := range []string{"vps", "custody"} {
			if data == fmt.Sprintf("/var/lib/fased-app-%s-%d/app-state", kind, owner) {
				return kind, false, nil
			}
		}
	}
	return "", false, nil
}

func protectedSocketService(kind string, owner int) (string, error) {
	if _, err := ProtectedSocket(kind, owner); err != nil {
		return "", err
	}
	if instance, ok := strings.CutPrefix(kind, "package-"); ok {
		return update.PackageInstanceUnit(owner, "remote", instance, "app")
	}
	service := fmt.Sprintf("fased-vps-%d.service", owner)
	if kind == "custody" {
		service = fmt.Sprintf("fased-custody-%d-app.service", owner)
	}
	return service, nil
}

// ProtectedSocketUnit shares the service's lifecycle: stopping Fased also
// stops the listener, so a private request cannot restart it during an update.
func ProtectedSocketUnit(kind string, owner int) (string, []byte, error) {
	path, err := ProtectedSocket(kind, owner)
	if err != nil {
		return "", nil, err
	}
	service, err := protectedSocketService(kind, owner)
	if err != nil {
		return "", nil, err
	}
	name := fmt.Sprintf("fased-private-%s-%d.socket", kind, owner)
	body := fmt.Sprintf("[Unit]\nDescription=Fased private ingress\nPartOf=%s\nBefore=%s\nStopWhenUnneeded=yes\n\n[Socket]\nListenStream=%s\nSocketUser=root\nSocketGroup=root\nSocketMode=0600\nDirectoryMode=0711\nFileDescriptorName=fased-private\nService=%s\nAccept=no\nRemoveOnStop=yes\n", service, service, path, service)
	return name, []byte(body), nil
}

// ProtectedSocketDropIn keeps the original installed service definition
// intact while systemd starts and passes the matching root-owned listener.
func ProtectedSocketDropIn(kind string, owner int) (string, []byte, error) {
	if strings.HasPrefix(kind, "package-") {
		return "", nil, errors.New("package services bind their protected socket directly")
	}
	name, _, err := ProtectedSocketUnit(kind, owner)
	if err != nil {
		return "", nil, err
	}
	service, err := protectedSocketService(kind, owner)
	if err != nil {
		return "", nil, err
	}
	path := "/etc/systemd/system/" + service + ".d/40-fased-private-ingress.conf"
	core := fmt.Sprintf("/var/lib/fased-app-%s-%d/fased-app", kind, owner)
	body := fmt.Sprintf("[Unit]\nRequires=%s\nAfter=%s\n\n[Service]\nSockets=%s\nExecStartPre=%s private-ingress-check --owner %d --kind %s\n", name, name, name, core, owner, kind)
	return path, []byte(body), nil
}

// ActivatedListener accepts exactly the one socket passed by systemd to this
// process. The path and every parent must remain root-owned and non-writable.
// A false result means no socket activation was requested by the service.
func ActivatedListener(data string, owner int) (net.Listener, bool, error) {
	kind, packageLayout, err := protectedDataKind(data, owner)
	if err != nil {
		return nil, true, err
	}
	pid, count := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS")
	if pid == "" && count == "" && os.Getenv("LISTEN_FDNAMES") == "" {
		if packageLayout {
			return nil, true, errors.New("protected private listener was not passed by the service manager")
		}
		return nil, false, nil
	}
	if pid != strconv.Itoa(os.Getpid()) || count != "1" || os.Getenv("LISTEN_FDNAMES") != "fased-private" {
		return nil, true, errors.New("invalid protected ingress activation")
	}
	path, err := ProtectedSocket(kind, owner)
	if err != nil || !rootOwnedSocket(path) {
		return nil, true, errors.New("protected ingress socket identity differs")
	}
	f := os.NewFile(3, "fased-private")
	if f == nil {
		return nil, true, errors.New("protected ingress descriptor is missing")
	}
	listener, err := activatedListener(f, path)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		if listener != nil {
			_ = listener.Close()
		}
		return nil, true, errors.Join(err, closeErr)
	}
	// Do not accidentally pass socket activation authority to spawned tools.
	for _, name := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = os.Unsetenv(name)
	}
	return listener, true, nil
}

func activatedListener(f *os.File, path string) (net.Listener, error) {
	fd := int(f.Fd())
	typ, typeErr := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	accepting, acceptErr := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ACCEPTCONN)
	addr, addrErr := unix.Getsockname(fd)
	u, ok := addr.(*unix.SockaddrUnix)
	if typeErr != nil || acceptErr != nil || addrErr != nil || typ != unix.SOCK_STREAM || accepting != 1 || !ok || u.Name != path {
		return nil, errors.New("protected ingress descriptor differs")
	}
	return net.FileListener(f)
}

func rootOwnedSocket(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.IndexByte(path, 0) >= 0 {
		return false
	}
	for name := path; ; name = filepath.Dir(name) {
		i, err := os.Lstat(name)
		if err != nil {
			return false
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
			return false
		}
		if name == path {
			if i.Mode()&os.ModeSocket == 0 || i.Mode().Perm() != 0600 {
				return false
			}
		} else if !i.IsDir() {
			return false
		}
		if name == "/" {
			return true
		}
	}
}
