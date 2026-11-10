//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// This grant is distinct from the Local native window and the app-only
// updater. It does not give the app, signer, or another OS user sudo.
func remoteProofPolicyPath(m Manifest) string {
	return "/etc/sudoers.d/fased-remote-proof-" + strconv.Itoa(m.Owner)
}

func remoteProofPolicy(m Manifest) ([]byte, error) {
	if m.validate() != nil || !m.Enrollment || m.Location != "remote" || m.PublicOrigin == "" {
		return nil, errors.New("verified Remote custody identity required")
	}
	args := append(appArgs(m, "status"), "--private-proof-stdin")
	return []byte("# Fased Remote private verification; no signer or arbitrary command authority.\n#" + strconv.Itoa(m.Owner) + " ALL=(root) NOPASSWD: " + strings.Join(args, " ") + "\n"), nil
}

// Read only the exact root-managed target or its operation-bound staging file.
// A non-owner file, changed policy or interrupted foreign stage is preserved.
func readRemoteProofPolicy(ctx context.Context, s *System, path string) (hostFile, error) {
	var f hostFile
	info, err := os.Lstat(path)
	if err != nil {
		return f, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0440 || info.Size() > 8192 {
		return f, errors.New("foreign Remote private verification policy; preserving it")
	}
	data, err := s.Run(ctx, nil, "/usr/bin/cat", "--", path)
	if err != nil || len(data) > 8192 {
		return f, errors.New("Remote private verification policy cannot be read safely")
	}
	return hostFile{Data: data, UID: 0, Mode: 0440, Regular: true}, nil
}

func installRemoteProofPolicy(ctx context.Context, m Manifest, s *System) error {
	policy, err := remoteProofPolicy(m)
	if err != nil {
		return err
	}
	dir, err := ownerJournal()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".remote-proof-policy-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(policy)
	err = errors.Join(err, tmp.Sync(), tmp.Close())
	if err != nil {
		return err
	}
	if _, err = readRoot("/usr/sbin/visudo"); err != nil {
		return err
	}
	if err = exec.CommandContext(ctx, "/usr/sbin/visudo", "-cf", tmp.Name()).Run(); err != nil {
		return errors.New("Remote private verification policy validation failed")
	}
	read := func(path string) (hostFile, error) {
		return readRemoteProofPolicy(ctx, s, path)
	}
	return publishRemoteProofPolicy(ctx, m, read, s.Run)
}

func publishRemoteProofPolicy(ctx context.Context, m Manifest, read readHost, run runHost) error {
	policy, err := remoteProofPolicy(m)
	if err != nil {
		return err
	}
	return publish(ctx, remoteProofPolicyPath(m), m.ID, policy, 0440, read, run)
}

// PreparePrivateAccessProof is the existing access-configuration ceremony,
// not an implicit side effect of status, verification, or an update.
func (s *System) PreparePrivateAccessProof(ctx context.Context, m Manifest) error {
	if s == nil {
		return errors.New("verified Remote custody owner required")
	}
	return preparePrivateAccessProof(ctx, m, remoteProofSetupOps{
		uid: os.Getuid(), euid: os.Geteuid(), lock: OwnerLock, load: Load,
		verify: Verify, commit: ReadEnrollmentCommit, active: noActiveController,
		install: func(ctx context.Context, m Manifest) error {
			s.Authorization = "Authorize one-time private device verification setup for this Remote workspace. Only its fixed unprivileged app command is permitted."
			return installRemoteProofPolicy(ctx, m, s)
		},
	})
}

type remoteProofSetupOps struct {
	uid, euid int
	lock      func() (io.Closer, error)
	load      func(int) (Manifest, error)
	verify    func(Manifest) error
	commit    func(Manifest) (EnrollmentCommit, error)
	active    func(Manifest) error
	install   func(context.Context, Manifest) error
}

func preparePrivateAccessProof(ctx context.Context, m Manifest, o remoteProofSetupOps) error {
	if m.Owner != o.uid || o.uid <= 0 || o.euid != o.uid {
		return errors.New("verified Remote custody owner required")
	}
	lock, err := o.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := o.load(m.Owner)
	if err != nil || current != m || o.verify(m) != nil {
		return errors.New("Remote custody installation changed; preserving it")
	}
	if _, err := remoteProofPolicy(m); err != nil {
		return err
	}
	if _, err := o.commit(m); err != nil {
		return err
	}
	if err := o.active(m); err != nil {
		return err
	}
	if err := o.install(ctx, m); err != nil {
		return fmt.Errorf("Remote device verification setup requires the installation's OS administrator; existing access and data are preserved: %w", err)
	}
	return nil
}
