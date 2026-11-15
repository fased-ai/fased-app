//go:build linux

package custodyinstall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
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
	if m.Layout == PackageLayout {
		if m.Owner != os.Getuid() || os.Geteuid() != os.Getuid() || VerifyPackageOwnerTarget(m) != nil {
			return errors.New("protected Local package owner required")
		}
		// Package services are started by their installed systemd units; the
		// owner channel never grants direct systemctl or sudo tool access.
		return nil
	}
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
	if s.protectedOwner > 0 {
		return installProtectedSudoPolicy(ctx, nativePolicyPath(m), nativePolicy(m))
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
	policy := nativePolicy(m)
	args, err := BoundPolicyWriteArgs(m.Owner, pending, policy)
	if err != nil {
		return err
	}
	if _, err = s.Run(ctx, policy, args...); err != nil {
		return err
	}
	info, err := os.Lstat(pending)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0440 {
		return errors.New("native policy staging identity changed; preserving it")
	}
	bound, err := s.Run(ctx, nil, "/usr/bin/cat", "--", pending)
	if err != nil || string(bound) != string(policy) {
		return errors.New("native policy staging content changed; preserving it")
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

// Only fixed policy bytes generated from the verified manifest reach here.
func installProtectedSudoPolicy(ctx context.Context, path string, policy []byte) error {
	if os.Getuid() != 0 || os.Geteuid() != 0 || trustedUtility("/usr/sbin/visudo") != nil {
		return errors.New("protected policy publication required")
	}
	c := exec.CommandContext(ctx, "/usr/sbin/visudo", "-c", "-f", "-")
	c.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	c.Stdin = bytes.NewReader(policy)
	if err := c.Run(); err != nil {
		return errors.New("fixed enrollment policy is invalid")
	}
	return maintenance.PublishProtected(path, policy, 0440)
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

// VerifyPackageOwnerTarget binds owner access to the running instance and its
// existing protected update direction. It never selects a legacy owner root.
func VerifyPackageOwnerTarget(m Manifest) error {
	if m.validatePackage() != nil || m.Location != "local" || !m.Enrollment {
		return errors.New("exact protected Local package required")
	}
	r, err := update.ReadPackageMaintenanceRecord(m.Owner, m.Location, m.ID)
	if err != nil {
		return err
	}
	selected, err := SelectProtectedPackageStart(r, m.Owner, m.Location, m.ID)
	if err != nil || selected != m || VerifyPackageInstance(m) != nil {
		return errors.New("package owner target differs from its protected recovery state")
	}
	return verifyPackageStartFences(m, r, readRoot)
}

// PackageOwnerChannel is reached only through the instance's fixed root
// helper and its already-held instance lock. The app itself runs as AppUID.
func PackageOwnerChannel(ctx context.Context, owner int, location, instance string, lock *os.File, in io.Reader, out io.Writer) error {
	if in == nil || out == nil || lock == nil || location != "local" {
		return errors.New("one protected Local owner request required")
	}
	if _, err := maintenance.ValidatePackageProcess(owner, location, instance); err != nil {
		return err
	}
	root, err := PackageRoot(owner, location, instance)
	if err != nil {
		return err
	}
	m, err := LoadPackageInstance(owner, root)
	if err != nil || VerifyPackageOwnerTarget(m) != nil || loadedUnit(m, "app") != nil {
		return errors.New("original protected Local package required")
	}
	request, err := http.ReadRequest(bufio.NewReader(io.LimitReader(in, 64*1024+1)))
	if err != nil || request.ContentLength < 0 || request.ContentLength > 16*1024 || len(request.TransferEncoding) != 0 {
		return errors.New("bounded Local owner request required")
	}
	defer request.Body.Close()
	body, err := io.ReadAll(io.LimitReader(request.Body, 16*1024+1))
	if err != nil || len(body) > 16*1024 || int64(len(body)) != request.ContentLength {
		return errors.New("bounded Local owner request required")
	}
	defer clear(body)
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Close = true
	var wire bytes.Buffer
	if err = request.Write(&wire); err != nil || wire.Len() > 64*1024 {
		return errors.New("bounded Local owner request required")
	}
	defer clear(wire.Bytes())
	commitPath := m.Root + "/enrollment-ready.json"
	if _, err = os.Lstat(commitPath); errors.Is(err, os.ErrNotExist) {
		r, readErr := update.ReadPackageMaintenanceRecord(owner, location, instance)
		if readErr != nil || r.Phase != "idle" || m.Network != "" || VerifyUnconfiguredPackageSigner(ctx, m) != nil {
			return errors.New("only the original empty Local installation can establish its owner")
		}
		var response bytes.Buffer
		probe := []byte("GET /api/v1/health HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
		if err = packageOwnerExchange(ctx, m, probe, &response, 64*1024); err != nil {
			return err
		}
		res, parseErr := http.ReadResponse(bufio.NewReader(&response), nil)
		if parseErr != nil || res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			return errors.New("running Local package health is unavailable")
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, 32*1024+1))
		closeErr := res.Body.Close()
		var health core.Health
		if readErr != nil || closeErr != nil || len(data) > 32*1024 || json.Unmarshal(data, &health) != nil {
			return errors.New("running Local package health is invalid")
		}
		commit, commitErr := packageOwnerCommit(m, health)
		if commitErr != nil {
			return commitErr
		}
		if err = maintenance.PublishProtected(commitPath, mustJSON(commit), 0644); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err = ReadEnrollmentCommit(m); err != nil {
		return err
	}
	// The app's Apply handler can start the package worker before the HTTP
	// response returns. Release the instance lock first so that worker does not
	// fail on its own owner's request. The app-UID client revalidates the
	// protected manifest, journal, fence and endpoint after this release.
	return packageOwnerAfterUnlock(lock, func() error {
		return packageOwnerExchange(ctx, m, wire.Bytes(), out, 8<<20)
	})
}

func packageOwnerAfterUnlock(lock *os.File, request func() error) error {
	if lock == nil || request == nil {
		return errors.New("original package lock required")
	}
	if err := lock.Close(); err != nil {
		return err
	}
	return request()
}

func packageOwnerExchange(ctx context.Context, m Manifest, request []byte, out io.Writer, limit int64) error {
	child, err := startMaintenanceChild(ctx, m, "app", m.Root+"/fased-app", "native-client", "--data-dir", m.Root+"/app-state", "--owner-config", m.Root+"/identity.json")
	if err != nil {
		return err
	}
	_, writeErr := child.in.Write(request)
	writeErr = errors.Join(writeErr, child.in.Close())
	n, readErr := io.Copy(out, io.LimitReader(child.out, limit+1))
	if n > limit {
		readErr = errors.Join(readErr, errors.New("Local owner response exceeds its bound"))
	}
	if writeErr != nil || readErr != nil {
		// The bounded reader may have stopped before the child finished writing.
		// End only that app-UID transport process before waiting for it.
		_ = child.cmd.Process.Kill()
	}
	return errors.Join(writeErr, readErr, child.close())
}

// NativeChannel is one exact existing app-identity client. The rule installed
// at initial OS consent grants only the OS owner this fixed command, without a
// later sudo/app/signing password. AppUID receives no sudo permission.
func (s *System) NativeChannel(ctx context.Context, m Manifest) (net.Conn, error) {
	if !m.Enrollment || m.Location != "local" || m.Owner != os.Getuid() || os.Geteuid() != os.Getuid() {
		return nil, errors.New("protected native owner enrollment required")
	}
	if m.Layout == PackageLayout {
		if VerifyPackageOwnerTarget(m) != nil {
			return nil, errors.New("protected Local package owner required")
		}
	} else if !m.NativeOwnerAccess || Verify(m) != nil {
		return nil, errors.New("protected native owner enrollment required")
	}
	if err := loadedUnit(m, "app"); err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	var cmd *exec.Cmd
	var frame []byte
	var err error
	if m.Layout == PackageLayout {
		cmd, frame, err = maintenance.PackageOwnerStreamCommand(child, m.Location, m.ID, io.Discard)
		if err != nil {
			cancel()
			return nil, err
		}
	} else {
		cmd = exec.CommandContext(child, "/usr/bin/sudo", append([]string{"-n", "--"}, nativeArgs(m)...)...)
	}
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
	if len(frame) != 0 {
		if _, err = in.Write(frame); err != nil {
			cancel()
			in.Close()
			out.Close()
			_ = cmd.Wait()
			return nil, err
		}
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
