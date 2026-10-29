//go:build linux

package custodyinstall

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func enrollmentInstance(s string) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}

func ReadEnrollmentCommit(m Manifest) (EnrollmentCommit, error) {
	var c EnrollmentCommit
	if m.validate() != nil || !m.Enrollment {
		return c, errors.New("exact enrolled installation required")
	}
	f, err := readRoot(m.Root + "/enrollment-ready.json")
	if err != nil {
		return c, errors.New("enrollment is not committed; managed startup is disabled")
	}
	if f.Mode != 0644 || strict(f.Data, &c) != nil || c.Version != 1 || c.ManifestSHA256 != sum(mustJSON(EnrollmentIdentity(m))) || !enrollmentInstance(c.InstanceID) || !validHex(c.Digest, 32) {
		return EnrollmentCommit{}, errors.New("enrollment commit changed; preserve all state")
	}
	return c, nil
}

func (s *System) enrollmentCommand(ctx context.Context, m Manifest, uid, gid int, action, dir, instance, digest string) (*exec.Cmd, error) {
	if m.Owner != os.Getuid() || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() || uid <= 0 || gid <= 0 || !m.Enrollment || Verify(m) != nil || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || strings.ContainsAny(dir, "\r\n\x00") {
		return nil, errors.New("verified ordinary-owner enrollment helper required")
	}
	if action == "export" {
		if uid == m.Signer.UID || !enrollmentInstance(instance) || digest != "" {
			return nil, errors.New("invalid source export identity")
		}
	} else {
		stage := filepath.Base(dir)
		if uid != m.App.UID || gid != m.App.GID || filepath.Dir(dir) != m.Root || dir != m.Root+"/app-state" && !(strings.HasPrefix(stage, "enrollment-") && strings.HasSuffix(stage, ".stage") && validHex(strings.TrimSuffix(strings.TrimPrefix(stage, "enrollment-"), ".stage"), 16)) {
			return nil, errors.New("image writes require the isolated app staging identity")
		}
		if action == "empty" {
			if instance != "" || digest != "" {
				return nil, errors.New("empty probe has no image authority")
			}
		} else if (action != "import" && action != "verify") || !enrollmentInstance(instance) || !validHex(digest, 32) {
			return nil, errors.New("invalid staged enrollment image command")
		}
	}
	if _, err := s.Run(ctx, nil, "/usr/bin/setpriv", "--version"); err != nil {
		return nil, err
	}
	args := []string{"-n", "--", "/usr/bin/setpriv", "--reuid", strconv.Itoa(uid), "--regid", strconv.Itoa(gid), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", m.Root + "/fased-app", "enrollment-image", action, "--data-dir", dir, "--disposable-fixture"}
	if instance != "" {
		args = append(args, "--expected-instance", instance)
	}
	if digest != "" {
		args = append(args, "--digest", digest)
	}
	c := exec.CommandContext(ctx, "/usr/bin/sudo", args...)
	c.Stderr = s.Out
	c.WaitDelay = 2 * time.Second
	return c, nil
}

// EnrollmentExport keeps stdin open so the existing source instance lock cannot
// be released before the controller has published the root-owned source fence.
func (s *System) EnrollmentExport(ctx context.Context, m Manifest, uid, gid int, dir, instance string) (*EnrollmentProcess, error) {
	child, cancel := context.WithCancel(ctx)
	c, err := s.enrollmentCommand(child, m, uid, gid, "export", dir, instance, "")
	if err != nil {
		cancel()
		return nil, err
	}
	in, err := c.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	out, err := c.StdoutPipe()
	if err != nil {
		in.Close()
		cancel()
		return nil, err
	}
	if err = c.Start(); err != nil {
		in.Close()
		out.Close()
		cancel()
		return nil, err
	}
	return &EnrollmentProcess{Input: in, Output: out, cancel: cancel, wait: c.Wait}, nil
}

func (s *System) EnrollmentImage(ctx context.Context, m Manifest, action, dir, instance, digest string, in io.Reader, out io.Writer) error {
	c, err := s.enrollmentCommand(ctx, m, m.App.UID, m.App.GID, action, dir, instance, digest)
	if err != nil {
		return err
	}
	c.Stdin, c.Stdout = in, out
	return c.Run()
}

// Signer state is classified without opening or passing key bytes through the
// app helper. This probe is only used before first enrollment activation.
func (s *System) EnrollmentSignerEmpty(ctx context.Context, m Manifest) error {
	if m.Owner != os.Getuid() || !m.Enrollment || Verify(m) != nil {
		return errors.New("verified enrollment target required")
	}
	b, err := s.Run(ctx, nil, "/usr/bin/find", m.Root+"/signer-state", "-mindepth", "1", "-maxdepth", "1", "-printf", "x", "-quit")
	if err != nil || len(b) != 0 {
		return errors.New("target signer state is not empty; preserve keys and recovery state")
	}
	return nil
}

// Activation is invoked only after the controller durably records the source
// fence and complete target publication. A durable commit also gates Control.
func ActivateEnrollment(ctx context.Context, m Manifest, instance, digest string, running, autostart bool, s *System) error {
	if m.Owner != os.Getuid() || !m.Enrollment || !enrollmentInstance(instance) || !validHex(digest, 32) || Verify(m) != nil {
		return errors.New("verified committed enrollment target required")
	}
	c := EnrollmentCommit{Version: 1, ManifestSHA256: sum(mustJSON(EnrollmentIdentity(m))), InstanceID: instance, Digest: digest}
	if err := publish(ctx, m.Root+"/enrollment-ready.json", m.ID, mustJSON(c), 0644, readRoot, s.Run); err != nil {
		return err
	}
	for _, role := range []string{"signer", "app"} {
		if err := publish(ctx, "/etc/systemd/system/"+Unit(m.Owner, role), m.ID, definitions(m)["fased-"+role+".service"], 0644, readRoot, s.Run); err != nil {
			return err
		}
	}
	if _, err := s.Run(ctx, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, role := range []string{"signer", "app"} {
		if err := loadedUnit(m, role); err != nil {
			return err
		}
	}
	if autostart {
		if _, err := s.Run(ctx, nil, "/usr/bin/systemctl", "enable", "--", Unit(m.Owner, "signer"), Unit(m.Owner, "app")); err != nil {
			return err
		}
	}
	if running {
		for _, role := range []string{"signer", "app"} {
			if _, err := s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", Unit(m.Owner, role)); err != nil {
				return err
			}
		}
		if err := waitReady(ctx, m, s); err != nil {
			return err
		}
		b, err := s.Run(ctx, nil, appArgs(m, "status")...)
		var h core.Health
		if err != nil || json.Unmarshal(b, &h) != nil || h.InstanceID != instance || h.Connection == nil || h.Connection.Location != m.Location || h.Connection.OwnerUID != m.Owner {
			return errors.New("enrolled runtime is not the original instance/profile")
		}
	}
	return publish(ctx, m.Root+"/ready.json", m.ID, readyRecord(m), 0644, readRoot, s.Run)
}
