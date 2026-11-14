//go:build linux

package hostprep

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"golang.org/x/sys/unix"
)

var tailscaleLoginURL = regexp.MustCompile(`^https://login\.tailscale\.com/a/[A-Za-z0-9]+$`)

// ProtectedTailscaleLogin admits only the root-recorded fresh installation.
// It never grants an operator or supplies a profile, preference or auth key.
func ProtectedTailscaleLogin(ctx context.Context, owner int, out io.Writer) error {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil || a.Profile != "remote" {
		return errors.New("registered Remote maintenance owner required")
	}
	if err = requireFreshTailscaleSetup(owner, os.Lstat); err != nil {
		return err
	}
	lock, err := tailscaleMachineLock()
	if err != nil {
		return err
	}
	defer lock.Close()
	u, err := user.LookupId(strconv.Itoa(owner))
	if err != nil {
		return err
	}
	run := func(ctx context.Context, args ...string) ([]byte, error) { return protectedOS(ctx, nil, out, args...) }
	wait := func(ctx context.Context) error {
		t := time.NewTimer(time.Second)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	start := func(ctx context.Context) error { return startFreshTailscale(ctx, a, run) }
	remember := func(url string) error { return recordTailscaleRequest(owner, url) }
	resume := func(url string) error { return sameTailscaleRequest(a, url) }
	return protectedTailscaleLogin(ctx, u.Username, out, run, wait, start, remember, resume)
}

func requireFreshTailscaleSetup(owner int, stat func(string) (os.FileInfo, error)) error {
	if owner <= 0 || stat == nil {
		return errors.New("fresh Remote setup required")
	}
	for _, path := range []string{fmt.Sprintf("/var/lib/fased-app-vps-%d", owner), fmt.Sprintf("/var/lib/fased-app-custody-%d", owner)} {
		if _, err := stat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("existing installation must retain its Tailscale authority")
		}
	}
	return nil
}

func protectedTailscaleLogin(ctx context.Context, username string, out io.Writer, run func(context.Context, ...string) ([]byte, error), wait func(context.Context) error, start func(context.Context) error, remember, resume func(string) error) error {
	if out == nil || run == nil || wait == nil || start == nil || remember == nil || resume == nil || !AllowedTailscalePreparation(username, []string{"status", "--json"}) {
		return errors.New("fixed Tailscale owner required")
	}
	status := func(ctx context.Context) (string, string, error) {
		b, err := run(ctx, "/usr/bin/tailscale", "status", "--json")
		if err != nil && len(b) == 0 {
			return "", "", err
		}
		return parseTailscaleStatus(b)
	}
	state, pending, err := status(ctx)
	if err != nil {
		return err
	}
	if state == "Running" {
		return nil
	}
	deadline, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var originalURL string
	if state == "NeedsLogin" && pending != "" {
		if err = resume(pending); err != nil {
			return err
		}
		originalURL = pending
		fmt.Fprintln(out, "Complete the original Tailscale sign-in:", pending)
	} else {
		if state != "NeedsLogin" {
			return errors.New("existing Tailscale state or sign-in request; preserve it")
		}
		if err = start(deadline); err != nil {
			return err
		}
	}
	for {
		state, pending, err = status(deadline)
		if err != nil {
			return err
		}
		if pending != "" {
			if !tailscaleLoginURL.MatchString(pending) || originalURL != "" && originalURL != pending {
				return errors.New("Tailscale sign-in request changed; preserve it")
			}
			if originalURL == "" {
				if err = remember(pending); err != nil {
					return err
				}
				originalURL = pending
				fmt.Fprintln(out, "Complete Tailscale sign-in:", pending)
			}
		}
		if state == "Running" {
			if originalURL == "" {
				return errors.New("Tailscale enrollment completed without observed browser approval; preserve it")
			}
			return nil
		}
		if state != "NeedsLogin" && state != "Starting" && state != "NoState" {
			return errors.New("Tailscale needs owner attention; existing settings are preserved")
		}
		if err = wait(deadline); err != nil {
			return errors.New("Tailscale sign-in remains pending; preserve the original request")
		}
	}
}

func startFreshTailscale(ctx context.Context, a maintenance.Authority, run func(context.Context, ...string) ([]byte, error)) error {
	g, err := readFreshTailscaleGrant(a, false)
	if err != nil {
		return err
	}
	if err = noTailscaleOverrides(os.Lstat); err != nil {
		return err
	}
	get := func(args ...string) ([]byte, error) { return run(ctx, args...) }
	versionJSON, err := get("/usr/bin/tailscale", "version", "--json")
	if err != nil {
		return err
	}
	version, err := supportedTailscaleVersion(versionJSON)
	if err != nil || version != g.ClientVersion {
		return errors.New("Tailscale version changed; preserve it")
	}
	prefs, err := get("/usr/bin/tailscale", "debug", "prefs")
	if err != nil {
		return err
	}
	profiles, err := get("/usr/bin/tailscale", "switch", "--list", "--json")
	if err != nil {
		return err
	}
	digest, err := freshTailscalePrefs(prefs, profiles)
	if err != nil || digest != g.PrefsSHA256 {
		return errors.New("Tailscale preferences or profiles changed; preserve them")
	}
	client, err := tailscaleExecutableHash("/usr/bin/tailscale")
	if err != nil || client != g.ClientSHA256 {
		return errors.New("Tailscale client changed; preserve it")
	}
	daemon, err := tailscaleExecutableHash("/usr/sbin/tailscaled")
	if err != nil || daemon != g.DaemonSHA256 {
		return errors.New("Tailscale daemon changed; preserve it")
	}
	pid, identity, err := freshTailscaleDaemon(ctx, run)
	if err != nil || pid != g.DaemonPID || identity != g.DaemonIdentitySHA256 {
		return errors.New("Tailscale daemon identity changed; preserve it")
	}
	defaults, err := tailscaleDefaultHash()
	if err != nil || defaults != g.DefaultSHA256 {
		return errors.New("Tailscale service settings changed; preserve them")
	}
	status, err := get("/usr/bin/tailscale", "status", "--json")
	if err != nil && len(status) == 0 {
		return err
	}
	state, pending, err := parseTailscaleStatus(status)
	if err != nil || state != "NeedsLogin" || pending != "" {
		return errors.New("Tailscale enrollment state changed; preserve it")
	}
	if err = consumeFreshTailscaleGrant(a.Owner); err != nil {
		return err
	}
	// The empty Start options do not replace preferences. Both requests use
	// the fixed local daemon socket; caller-selected endpoints are impossible.
	if err = freshTailscalePOST(ctx, g, run, "/localapi/v0/start", "{}"); err != nil {
		return err
	}
	return freshTailscalePOST(ctx, g, run, "/localapi/v0/login-interactive", "")
}

func freshTailscalePOST(ctx context.Context, g freshTailscaleGrant, run func(context.Context, ...string) ([]byte, error), path, body string) error {
	if path != "/localapi/v0/start" && path != "/localapi/v0/login-interactive" || path == "/localapi/v0/start" && body != "{}" || path == "/localapi/v0/login-interactive" && body != "" {
		return errors.New("invalid fixed Tailscale operation")
	}
	t := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", "/run/tailscale/tailscaled.sock")
		if err != nil {
			return nil, err
		}
		u, ok := conn.(*net.UnixConn)
		if !ok {
			conn.Close()
			return nil, errors.New("Tailscale socket identity unavailable")
		}
		raw, err := u.SyscallConn()
		var cred *unix.Ucred
		var peerErr error
		if err == nil {
			err = raw.Control(func(fd uintptr) { cred, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
		}
		if err != nil || peerErr != nil || !sameTailscalePeer(cred, g.DaemonPID) {
			conn.Close()
			return nil, errors.New("Tailscale socket daemon changed; preserve its state")
		}
		pid, identity, err := freshTailscaleDaemon(ctx, run)
		if err != nil || pid != g.DaemonPID || identity != g.DaemonIdentitySHA256 {
			conn.Close()
			return nil, errors.New("Tailscale daemon identity changed; preserve its state")
		}
		return conn, nil
	}}
	defer t.CloseIdleConnections()
	c := &http.Client{Transport: t, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("local daemon redirected") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://local-tailscaled.sock"+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("Tailscale enrollment response unavailable; preserve its state")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return errors.New("Tailscale enrollment was refused; preserve its state")
	}
	return nil
}

func sameTailscalePeer(cred *unix.Ucred, expectedPID int) bool {
	return cred != nil && cred.Uid == 0 && expectedPID > 1 && int(cred.Pid) == expectedPID
}
