//go:build linux

package vpsinstall

import (
	"context"
	"errors"
	"io"
	"os/exec"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

// ApplicationReaderInventory reuses the original reader's existing protected
// inventory/session route before target staging. No DB or cookie bytes leave it.
func ApplicationReaderInventory(ctx context.Context, m Manifest, id string, s *custodyinstall.System) (update.CoreSnapshot, error) {
	var snapshot update.CoreSnapshot
	if s == nil || !update.IDOK(id) {
		return snapshot, errors.New("ordinary owner and exact source probe required")
	}
	o := liveOps(s)
	return applicationReaderInventory(ctx, m, id, o, func(ctx context.Context, args []string) ([]byte, error) {
		return runApplicationReaderSource(ctx, s, args, nil)
	})
}

func applicationReaderInventory(ctx context.Context, m Manifest, id string, o hostOps, helper func(context.Context, []string) ([]byte, error)) (update.CoreSnapshot, error) {
	var snapshot update.CoreSnapshot
	if !update.IDOK(id) || helper == nil {
		return snapshot, errors.New("one protected source inventory required")
	}
	if e := verify(ctx, o, m); e != nil {
		return snapshot, e
	}
	if e := loaded(ctx, o, m); e != nil {
		return snapshot, e
	}
	for _, tool := range []string{"/usr/bin/sudo", "/usr/bin/setpriv", "/usr/bin/env"} {
		if e := trustedTool(o, tool); e != nil {
			return snapshot, e
		}
	}
	args := applicationReaderSourceArgs(m, "update-inventory", id)
	b, e := helper(ctx, args)
	if e != nil {
		return snapshot, e
	}
	if update.StrictJSONBounded(b, &snapshot, 16384) != nil || snapshot.Version != 1 || snapshot.ProbeID != id || snapshot.InstanceID == "" {
		return update.CoreSnapshot{}, errors.New("invalid protected predecessor inventory")
	}
	return snapshot, nil
}

// ApplicationReaderSignIn passes the owner's original terminal directly to the
// already-installed unprivileged login command. It never reads the password or
// copies a session into the administrator process and cannot run unattended.
func ApplicationReaderSignIn(ctx context.Context, m Manifest, s *custodyinstall.System) error {
	if s == nil || s.NonInteractive {
		return errors.New("explicit owner terminal sign-in required")
	}
	o := liveOps(s)
	if e := verify(ctx, o, m); e != nil {
		return e
	}
	if e := loaded(ctx, o, m); e != nil {
		return e
	}
	for _, tool := range []string{"/usr/bin/sudo", "/usr/bin/setpriv", "/usr/bin/env"} {
		if e := trustedTool(o, tool); e != nil {
			return e
		}
	}
	_, e := runApplicationReaderSource(ctx, s, applicationReaderSourceArgs(m, "login", ""), s.In)
	return e
}

func applicationReaderSourceArgs(m Manifest, action, id string) []string {
	base := applicationOwnerHelperArgs(m, m.Core)
	args := append(append([]string{}, base[:len(base)-4]...), m.Core, action, "--data-dir", m.Data)
	if action == "update-inventory" {
		args = append(args, "--probe-id", id)
	}
	return args
}

func runApplicationReaderSource(ctx context.Context, s *custodyinstall.System, args []string, input io.Reader) ([]byte, error) {
	if _, e := s.Run(ctx, nil, "/usr/bin/setpriv", "--version"); e != nil {
		return nil, e
	}
	c := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{"-n", "--"}, args...)...)
	c.Dir = "/"
	c.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	c.Stdin = input
	c.Stderr = s.Out
	var output applicationOwnerResponse
	c.Stdout = &output
	if e := c.Run(); e != nil {
		return nil, e
	}
	return output.Bytes(), nil
}
