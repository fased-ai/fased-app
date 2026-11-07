//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func nativeArgs(m Manifest) []string {
	return append(appArgs(m, "native-client"), "--owner-config", m.Root+"/identity.json")
}
func nativePolicyPath(m Manifest) string {
	return "/etc/sudoers.d/fased-native-" + strconv.Itoa(m.Owner)
}
func nativePolicy(m Manifest) []byte {
	return []byte("# Fased Local native access; no signer or arbitrary command authority.\n#" + strconv.Itoa(m.Owner) + " ALL=(root) NOPASSWD: " + strings.Join(nativeArgs(m), " ") + ", /usr/bin/systemctl start " + Unit(m.Owner, "signer") + ", /usr/bin/systemctl start " + Unit(m.Owner, "app") + "\n")
}

func (s *System) EnsureNativeRunning(ctx context.Context, m Manifest) error {
	if !m.NativeOwnerAccess || m.Owner != os.Getuid() || Verify(m) != nil {
		return errors.New("protected native owner enrollment required")
	}
	if Runtime(m) == nil {
		return nil
	}
	if err := noActiveController(m); err != nil {
		return err
	}
	if _, err := ReadEnrollmentCommit(m); err != nil {
		return err
	}
	// These exact starts are the only service operations in the native grant.
	for _, role := range []string{"signer", "app"} {
		f, err := readRoot("/etc/systemd/system/" + Unit(m.Owner, role))
		if err != nil || !known(f, definitions(m)["fased-"+role+".service"], 0644) {
			return errors.New("protected native unit changed")
		}
		cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "--", "/usr/bin/systemctl", "start", Unit(m.Owner, role))
		if err = cmd.Run(); err != nil {
			return errors.New("native service start is not authorized")
		}
	}
	return nil
}

func installNativePolicy(ctx context.Context, m Manifest, s *System) error {
	if !m.NativeOwnerAccess {
		return nil
	}
	path := nativePolicyPath(m)
	if info, err := os.Lstat(path); err == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || info.Mode().Perm() != 0440 || !info.Mode().IsRegular() {
			return errors.New("foreign native policy is preserved")
		}
		b, err := s.Run(ctx, nil, "/usr/bin/cat", "--", path)
		if err != nil || string(b) != string(nativePolicy(m)) {
			return errors.New("foreign native policy is preserved")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := ownerJournal()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".native-policy-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(nativePolicy(m))
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return err
	}
	// Validate as the ordinary owner before privileged fixed-file publication.
	if _, err = readRoot("/usr/sbin/visudo"); err != nil {
		return err
	}
	if err = exec.CommandContext(ctx, "/usr/sbin/visudo", "-cf", f.Name()).Run(); err != nil {
		return errors.New("native owner policy validation failed")
	}
	pending := path + "-" + m.ID + ".pending"
	if _, err = s.Run(ctx, nil, "/usr/bin/install", "--owner=0", "--group=0", "--mode=0440", "--", f.Name(), pending); err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/ln", "--no-target-directory", "--", pending, path); err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/rm", "--", pending); err != nil {
		return err
	}
	_, err = s.Run(ctx, nil, "/usr/bin/sync", "-f", path)
	return err
}

// Inventory may verify existing OS consent but cannot create/repair it before
// the owner reviews maintenance. In particular, do not call the installer here.
func verifyNativePolicy(ctx context.Context, m Manifest, s *System) error {
	info, err := os.Lstat(nativePolicyPath(m))
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || info.Mode().Perm() != 0440 || !info.Mode().IsRegular() {
		return errors.New("existing exact native policy required")
	}
	b, err := s.Run(ctx, nil, "/usr/bin/cat", "--", nativePolicyPath(m))
	if err != nil {
		return err
	}
	if string(b) != string(nativePolicy(m)) {
		return errors.New("foreign native policy is preserved")
	}
	return nil
}

// NativeChannel is one exact existing app-identity client. The rule installed
// at initial OS consent grants only the OS owner this fixed command, without a
// later sudo/app/signing password. AppUID receives no sudo permission.
func (s *System) NativeChannel(ctx context.Context, m Manifest) (net.Conn, error) {
	if !m.NativeOwnerAccess || !m.Enrollment || m.Location != "local" || m.Owner != os.Getuid() || Verify(m) != nil {
		return nil, errors.New("protected native owner enrollment required")
	}
	if err := loadedUnit(m, "app"); err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(child, "/usr/bin/sudo", append([]string{"-n", "--"}, nativeArgs(m)...)...)
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, err
	}
	c := &nativeConn{in: in, out: out, cancel: cancel, cmd: cmd}
	return c, nil
}

type nativeConn struct {
	in     io.WriteCloser
	out    io.ReadCloser
	cancel context.CancelFunc
	cmd    *exec.Cmd
	once   sync.Once
}

func (c *nativeConn) Read(b []byte) (int, error)  { return c.out.Read(b) }
func (c *nativeConn) Write(b []byte) (int, error) { return c.in.Write(b) }
func (c *nativeConn) Close() error {
	c.once.Do(func() { c.cancel(); c.in.Close(); c.out.Close(); _ = c.cmd.Wait() })
	return nil
}

type nativeAddr struct{}

func (nativeAddr) Network() string         { return "protected-native" }
func (nativeAddr) String() string          { return "fixed-owner-app-client" }
func (c *nativeConn) LocalAddr() net.Addr  { return nativeAddr{} }
func (c *nativeConn) RemoteAddr() net.Addr { return nativeAddr{} }
func (c *nativeConn) SetReadDeadline(t time.Time) error {
	return c.out.(interface{ SetReadDeadline(time.Time) error }).SetReadDeadline(t)
}
func (c *nativeConn) SetWriteDeadline(t time.Time) error {
	return c.in.(interface{ SetWriteDeadline(time.Time) error }).SetWriteDeadline(t)
}
func (c *nativeConn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}
