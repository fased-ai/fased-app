//go:build linux

package signer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/onchain"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

func decodeWire(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid local message")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing local message")
	}
	return nil
}

func trustedPath(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("identity paths must be absolute and canonical")
	}
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("identity path is writable by a group/other user or uses a symlink")
		}
		var st unix.Stat_t
		if err = unix.Lstat(p, &st); err != nil {
			return err
		}
		if st.Uid != 0 {
			return errors.New("configuration and its parents must be root-owned")
		}
		if p == "/" {
			break
		}
	}
	return nil
}

func LoadConfig(path string) (Config, error) {
	var c Config
	if err := trustedPath(path); err != nil {
		return c, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return c, errors.New("identity configuration must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxDocument+1))
	if err != nil {
		return c, err
	}
	if err = decode(b, &c); err != nil {
		return c, err
	}
	if err = c.Validate(); err != nil {
		return c, err
	}
	if err = protectedDirectory(c.StateDir, c.SignerUID, 0700); err != nil {
		return c, err
	}
	if err = protectedDirectory(c.SocketDir, c.SignerUID, 0755); err != nil {
		return c, err
	}
	return c, nil
}

func protectedDirectory(path string, uid int, mode uint32) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("signer path must be absolute and canonical")
	}
	if err := trustedPath(filepath.Dir(path)); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || int(st.Uid) != uid || st.Mode&0777 != mode {
		return errors.New("signer directory identity or permissions do not match configuration")
	}
	return nil
}

func peerUID(conn *net.UnixConn) (int, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return -1, err
	}
	var credentials *unix.Ucred
	var inner error
	err = raw.Control(func(fd uintptr) {
		credentials, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return -1, err
	}
	if inner != nil {
		return -1, inner
	}
	return int(credentials.Uid), nil
}

func readWire(conn net.Conn, v any) error {
	r := bufio.NewReader(io.LimitReader(conn, 64*1024+1))
	b, err := r.ReadBytes('\n')
	if err != nil {
		return errors.New("incomplete local message")
	}
	return strictWire(b, v)
}

type Server struct {
	listeners       []*net.UnixListener
	vault           *Vault
	wg              sync.WaitGroup
	mu              sync.Mutex
	connections     map[*net.UnixConn]bool
	closed          bool
	done            chan struct{}
	inspectionClose func()
	closeErr        error
}

func VerifyExecutable() error {
	path, err := os.Executable()
	if err != nil {
		return err
	}
	return trustedPath(path)
}

func CheckOwner(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := VerifyExecutable(); err != nil {
		return err
	}
	if os.Geteuid() != c.OwnerUID || os.Getuid() != c.OwnerUID {
		return errors.New("owner administration denied for this OS identity")
	}
	return nil
}

// CheckApplication is called before opening any managed app state/listener.
func CheckApplication(config, root, data string) error {
	c, err := LoadConfig(config)
	if err != nil {
		return err
	}
	if err = VerifyExecutable(); err != nil {
		return err
	}
	if os.Getuid() != c.AppUID || os.Geteuid() != c.AppUID || config != filepath.Join(root, "identity.json") || data != filepath.Join(root, "app-state") || c.StateDir != filepath.Join(root, "signer-state") || c.SocketDir != filepath.Join(root, "signer-ipc") {
		return errors.New("managed core identity or paths do not match installed custody configuration")
	}
	if _, e := os.Lstat(root + "/fixture-inspection-v1/forward.json"); !os.IsNotExist(e) {
		if _, e = update.VerifyInstalledInspectionGuard(root, "core"); e != nil {
			return e
		}
	}
	return protectedDirectory(data, c.AppUID, 0700)
}

func Serve(ctx context.Context, c Config) (*Server, error) { return serveMode(ctx, c, false) }

// ServeControlledSwapFixture preserves installed identity and kernel peer checks.
// Only compiled fixture callers supply this dependency; ordinary Serve is closed.
func ServeControlledSwapFixture(ctx context.Context, c Config, observer SwapObserver) (*Server, error) {
	if c.Version != 2 || observer == nil {
		return nil, ErrUnavailable
	}
	return serveConfigured(ctx, c, false, observer)
}
func serveMode(ctx context.Context, c Config, guarded bool) (*Server, error) {
	return serveConfigured(ctx, c, guarded, nil)
}
func serveConfigured(ctx context.Context, c Config, guarded bool, observer SwapObserver) (*Server, error) {
	return serveCredentialConfigured(ctx, c, guarded, observer, nil)
}

// ServeControlledSwapCredentialsFixture is available only to compiled disposable
// identity acceptance. Ordinary startup never accepts a transport or credentials.
func ServeControlledSwapCredentialsFixture(ctx context.Context, c Config, factory func(onchain.CredentialAccess) (SwapObserver, error)) (*Server, error) {
	if c.Version != 2 || factory == nil {
		return nil, ErrUnavailable
	}
	return serveCredentialConfigured(ctx, c, false, nil, factory)
}
func serveCredentialConfigured(ctx context.Context, c Config, guarded bool, observer SwapObserver, factory func(onchain.CredentialAccess) (SwapObserver, error)) (*Server, error) {
	return serveCustodyConfigured(ctx, c, guarded, observer, factory, false)
}
func serveCustodyConfigured(ctx context.Context, c Config, guarded bool, observer SwapObserver, factory func(onchain.CredentialAccess) (SwapObserver, error), controlledOrdinary bool) (*Server, error) {
	if c.Version == 3 && !controlledOrdinary && !OrdinaryOwnerAdmissionEnabled() || controlledOrdinary && (c.Version != 3 || c.Mode != OrdinaryCustodyMode || observer != nil || factory != nil) {
		return nil, ErrUnavailable
	}
	if _, e := os.Lstat(filepath.Dir(c.StateDir) + "/fixture-inspection-v1/forward.json"); !os.IsNotExist(e) && !guarded {
		return nil, errors.New("guarded fixture startup required before state access")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if err := VerifyExecutable(); err != nil {
		return nil, err
	}
	if os.Geteuid() != c.SignerUID || os.Getuid() != c.SignerUID {
		return nil, errors.New("daemon must run only as its configured unprivileged signer UID")
	}
	if err := protectedDirectory(c.StateDir, c.SignerUID, 0700); err != nil {
		return nil, err
	}
	if err := protectedDirectory(c.SocketDir, c.SignerUID, 0755); err != nil {
		return nil, err
	}
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return nil, err
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return nil, err
	}
	network := c.Network
	if c.Version == 3 {
		namespace := ordinaryOwnerNamespace
		if controlledOrdinary {
			namespace = ordinaryNamespace
		}
		network = namespace + network
	}
	v, err := openVaultNetwork(c.StateDir, c.SignerID, false, network)
	if err != nil {
		return nil, err
	}
	if factory != nil {
		v.receiptRPCFactory = factory
		v.swapObserver = closedReceiptObserver{}
	}
	if observer != nil {
		if err = v.ConfigureSwapObserver(observer); err != nil {
			v.Close()
			return nil, err
		}
	}
	return serveListeners(ctx, c, v)
}

func serveListeners(ctx context.Context, c Config, v *Vault) (*Server, error) {
	s := &Server{vault: v, connections: map[*net.UnixConn]bool{}, done: make(chan struct{})}
	for _, owner := range []bool{true, false} {
		path := socketPath(c, owner)
		if info, err := os.Lstat(path); err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				s.Close()
				return nil, errors.New("refusing to replace foreign socket path")
			}
			// State lock is already held. Only this UID can mutate the socket directory.
			if err = os.Remove(path); err != nil {
				s.Close()
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			s.Close()
			return nil, err
		}
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			s.Close()
			return nil, err
		}
		s.listeners = append(s.listeners, listener)
		// Connect permission alone conveys no role. Kernel SO_PEERCRED is checked
		// before any challenge, request or secret; each socket has a separate cap.
		if err = os.Chmod(path, 0666); err != nil {
			s.Close()
			return nil, err
		}
		uid := c.AppUID
		if owner {
			uid = c.OwnerUID
		}
		s.wg.Add(1)
		go s.accept(listener, c, uid, owner)
	}
	if _, e := os.Lstat(filepath.Dir(c.StateDir) + "/fixture-inspection-v1/forward.json"); e == nil {
		role, e := update.OpenInstalledInspectionFixture(filepath.Dir(c.StateDir), "signer")
		if e != nil {
			s.Close()
			return nil, e
		}
		channel, e := update.NewInstalledInspectionServer(ctx, role, func(p string) (update.SignerSnapshot, error) {
			v, e := s.vault.UpdateInventory(p)
			if e != nil {
				return update.SignerSnapshot{}, e
			}
			return *v, nil
		})
		if e != nil {
			role.Close()
			s.Close()
			return nil, e
		}
		s.inspectionClose = channel.Close
	}
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}

func socketPath(c Config, owner bool) string {
	name := "app.sock"
	if owner {
		name = "owner.sock"
	}
	return filepath.Join(c.SocketDir, name)
}

func (s *Server) accept(listener *net.UnixListener, c Config, uid int, owner bool) {
	defer s.wg.Done()
	slots := make(chan struct{}, 8)
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		peer, err := peerUID(conn)
		if err != nil || peer != uid {
			conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			<-slots
			return
		}
		s.connections[conn] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer func() {
				conn.Close()
				s.mu.Lock()
				delete(s.connections, conn)
				s.mu.Unlock()
				<-slots
				s.wg.Done()
			}()
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			nonce := randomID()
			if json.NewEncoder(conn).Encode(greeting{Version: c.Version, SignerID: c.SignerID, Challenge: nonce}) != nil {
				return
			}
			var request wireRequest
			if readWire(conn, &request) != nil {
				return
			}
			defer wipe(request.Request.Password)
			defer wipe(request.Request.NewPassword)
			defer wipe(request.Request.Document)
			if request.Version != c.Version || request.SignerID != c.SignerID || request.Challenge != nonce {
				return
			}
			response := execute(s.vault, request.Request, owner)
			defer wipe(response.Document)
			_ = json.NewEncoder(conn).Encode(response)
		}()
	}
}

func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.done
		return s.closeErr
	}
	s.closed = true
	for _, l := range s.listeners {
		l.Close()
	}
	for c := range s.connections {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	if s.inspectionClose != nil {
		s.inspectionClose()
	}
	s.closeErr = s.vault.Close()
	close(s.done)
	return s.closeErr
}

func call(ctx context.Context, c Config, owner bool, request Request) (Response, error) {
	var response Response
	if err := c.Validate(); err != nil {
		return response, err
	}
	if err := VerifyExecutable(); err != nil {
		return response, err
	}
	expected := c.AppUID
	if owner {
		expected = c.OwnerUID
	}
	if os.Geteuid() != expected || os.Getuid() != expected {
		return response, errors.New("this OS identity cannot use the requested signer channel")
	}
	dialer := net.Dialer{}
	raw, err := dialer.DialContext(ctx, "unix", socketPath(c, owner))
	if err != nil {
		return response, err
	}
	defer raw.Close()
	conn := raw.(*net.UnixConn)
	uid, err := peerUID(conn)
	if err != nil || uid != c.SignerUID {
		return response, errors.New("local peer is not the configured signer")
	}
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	var hello greeting
	if err = readWire(conn, &hello); err != nil {
		return response, err
	}
	if hello.Version != c.Version || hello.SignerID != c.SignerID || !validID(hello.Challenge) {
		return response, errors.New("signer identity/challenge mismatch")
	}
	if err = json.NewEncoder(conn).Encode(wireRequest{Version: c.Version, SignerID: c.SignerID, Challenge: hello.Challenge, Request: request}); err != nil {
		return response, err
	}
	if err = readWire(conn, &response); err != nil {
		return response, err
	}
	if response.Error != "" {
		return response, errors.New(response.Error)
	}
	return response, nil
}
