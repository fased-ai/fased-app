//go:build linux

package update

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func fixtureTransportSupported() error { return nil }
func fixturePeerUID(c *net.UnixConn) (int, error) {
	raw, e := c.SyscallConn()
	if e != nil {
		return -1, e
	}
	var cred *unix.Ucred
	var inner error
	e = raw.Control(func(fd uintptr) { cred, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if e != nil {
		return -1, e
	}
	if inner != nil || cred == nil || cred.Pid <= 0 {
		return -1, errors.New("kernel peer credentials unavailable")
	}
	return int(cred.Uid), nil
}
func fixtureAncestors(path string, app int, test bool) error {
	real, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || real != filepath.Clean(path) {
		return errors.New("canonical fixture directory required")
	}
	for p := path; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() {
			return errors.New("fixture ancestor refused")
		}
		s, ok := st.Sys().(*syscall.Stat_t)
		if !ok || !test && (st.Mode().Perm()&0022 != 0 || int(s.Uid) == app) {
			return errors.New("app-writable fixture ancestor refused")
		}
		if p == "/" {
			break
		}
	}
	return nil
}
func fixtureRoleIdentity(r *FixtureTransportRole) error {
	if r == nil || os.Geteuid() == 0 || os.Getuid() != os.Geteuid() {
		return errors.New("nonroot fixture identity required")
	}
	c := r.cfg
	if c.Role != "owner" && c.Role != "signer" || c.OwnerUID <= 0 || c.SignerUID <= 0 || c.AppUID <= 0 || !r.testOnly && (c.OwnerUID == c.SignerUID || c.OwnerUID == c.AppUID || c.SignerUID == c.AppUID) {
		return errors.New("three distinct nonzero fixture UIDs required")
	}
	expected := c.OwnerUID
	if c.Role == "signer" {
		expected = c.SignerUID
	}
	if os.Getuid() != expected {
		return errors.New("fixture role UID refused")
	}
	if e := fixtureAncestors(c.PrivateDir, c.AppUID, r.testOnly); e != nil {
		return e
	}
	st, e := os.Lstat(c.PrivateDir)
	if e != nil || st.Mode().Perm() != 0700 || !acquisitionParentOwned(st) {
		return errors.New("private fixture role directory refused")
	}
	return nil
}
func fixtureSocketParent(r *FixtureTransportRole) error {
	c := r.cfg
	if filepath.Base(c.Socket) != "owner.sock" {
		return errors.New("fixed fixture socket name required")
	}
	parent := filepath.Dir(c.Socket)
	if e := fixtureAncestors(parent, c.AppUID, r.testOnly); e != nil {
		return e
	}
	st, e := os.Lstat(parent)
	if e != nil || st.Mode().Perm() != 0711 {
		return errors.New("fixture socket directory mode refused")
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(s.Uid) != c.SignerUID {
		return errors.New("fixture socket owner refused")
	}
	return nil
}
func fixtureListen(r *FixtureTransportRole) (*net.UnixListener, error) {
	if e := fixtureSocketParent(r); e != nil {
		return nil, e
	}
	if _, e := os.Lstat(r.cfg.Socket); !os.IsNotExist(e) {
		return nil, errors.New("existing fixture socket refused; explicit cleanup required")
	}
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: r.cfg.Socket, Net: "unix"})
	if e != nil {
		return nil, e
	}
	l.SetUnlinkOnClose(false)
	if e = os.Chmod(r.cfg.Socket, 0666); e != nil {
		_ = l.Close()
		return nil, e
	}
	return l, nil
}
func fixtureDial(ctx context.Context, r *FixtureTransportRole) (*net.UnixConn, error) {
	if e := fixtureSocketParent(r); e != nil {
		return nil, e
	}
	st, e := os.Lstat(r.cfg.Socket)
	if e != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm() != 0666 {
		return nil, errors.New("fixture socket type/mode refused")
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(s.Uid) != r.cfg.SignerUID {
		return nil, errors.New("fixture socket owner refused")
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, e := d.DialContext(ctx, "unix", r.cfg.Socket)
	if e != nil {
		return nil, e
	}
	after, e := os.Lstat(r.cfg.Socket)
	if e != nil || !os.SameFile(st, after) {
		_ = conn.Close()
		return nil, errors.New("fixture socket changed")
	}
	return conn.(*net.UnixConn), nil
}
