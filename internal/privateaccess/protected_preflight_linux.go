//go:build linux

package privateaccess

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

// ProtectedInitialApproval is the device actually observed by the temporary
// private endpoint, bound to a single authenticated installation.
type ProtectedInitialApproval struct {
	Version       int              `json:"version"`
	Owner         int              `json:"owner"`
	Origin        string           `json:"origin"`
	Listen        string           `json:"listen"`
	Sequence      uint64           `json:"sequence"`
	ReleaseSHA256 string           `json:"releaseSha256"`
	CoreSHA256    string           `json:"coreSha256"`
	Device        tailnet.Identity `json:"device"`
}

func validApprovalDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == value
}

func (a ProtectedInitialApproval) Matches(owner int, origin, listen string, sequence uint64, release, coreHash string) bool {
	validCore := a.CoreSHA256 == "" || validApprovalDigest(a.CoreSHA256)
	if coreHash != "" {
		validCore = validCore && validApprovalDigest(coreHash) && a.CoreSHA256 == coreHash
	}
	return a.Version == 1 && a.Owner == owner && owner > 0 && a.Origin == origin && a.Listen == listen && a.Sequence == sequence && sequence > 0 && a.ReleaseSHA256 == release && validApprovalDigest(release) && validCore && a.Device.UserID > 0 && a.Device.NodeID != "" && len(a.Device.NodeID) <= 128 && a.Device.Login != "" && len(a.Device.Login) <= 256 && len(a.Device.Name) <= 256
}

func protectedApprovalPath(owner int) string {
	return filepath.Join(update.MaintenanceOwnerRoot(owner), "remote", "initial-approval.json")
}

// ReadProtectedInitialApproval never accepts an operator-writable witness.
func ReadProtectedInitialApproval(owner int) (ProtectedInitialApproval, error) {
	var approval ProtectedInitialApproval
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 {
		return approval, errors.New("protected Remote approval required")
	}
	f, err := update.OpenProtected(protectedApprovalPath(owner), 16<<10, 0600)
	if err != nil {
		return approval, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil || update.StrictJSON(b, &approval) != nil || !approval.Matches(owner, approval.Origin, approval.Listen, approval.Sequence, approval.ReleaseSHA256, approval.CoreSHA256) {
		return ProtectedInitialApproval{}, errors.New("invalid protected Remote approval; preserve it")
	}
	return approval, nil
}

// ProtectedPendingOrigin exposes only the helper-owned port needed to retry
// the same interrupted preflight. It grants no Serve mutation or claim.
func ProtectedPendingOrigin(owner int) (string, error) {
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 {
		return "", errors.New("protected Remote approval required")
	}
	origin := ""
	approval, err := ReadProtectedInitialApproval(owner)
	if err == nil {
		origin = approval.Origin
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	path := filepath.Join(filepath.Dir(protectedApprovalPath(owner)), "private-setup.json")
	f, err := update.OpenProtected(path, 16<<10, 0600)
	if errors.Is(err, os.ErrNotExist) {
		return origin, nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil {
		return "", err
	}
	return protectedPendingOrigin(origin, b, filepath.Dir(path))
}

func protectedPendingOrigin(origin string, b []byte, dir string) (string, error) {
	var receipt preflightReceipt
	if update.StrictJSON(b, &receipt) != nil || receipt.Version != 1 || receipt.Plan.Mode != "tailscale" || !preflightSocketTarget(receipt.Plan.Target, dir) {
		return "", errors.New("invalid protected private setup; preserve it")
	}
	if _, _, err := tailnet.Origin(receipt.Plan.Origin); err != nil || origin != "" && origin != receipt.Plan.Origin {
		return "", errors.New("protected private setup origin differs; preserve it")
	}
	return receipt.Plan.Origin, nil
}

// ConfirmProtectedRemoteInitial runs the existing nonce/browser preflight in
// the fixed helper itself. The helper observes the daemon peer and retains the
// result under its root-owned journal; it never trusts an owner-written peer.
func ConfirmProtectedRemoteInitial(ctx context.Context, owner int, origin, listen string, sequence uint64, release, coreHash string, out io.Writer) (tailnet.Identity, error) {
	var empty tailnet.Identity
	if os.Getuid() != 0 || os.Geteuid() != 0 || owner <= 0 || sequence == 0 || !validApprovalDigest(release) || coreHash != "" && !validApprovalDigest(coreHash) {
		return empty, errors.New("protected Remote installation identity required")
	}
	dir := filepath.Dir(protectedApprovalPath(owner))
	if err := core.EnsurePrivateDirectory(dir); err != nil {
		return empty, err
	}
	m := Manager{GetConfig: systemConfig, SetConfig: systemSetConfig, Run: func(ctx context.Context, args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, "/usr/bin/tailscale", args...)
		c.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
		return c.Output()
	}}
	return confirmProtectedRemoteInitial(ctx, m, owner, origin, listen, sequence, release, coreHash,
		func() (ProtectedInitialApproval, error) { return ReadProtectedInitialApproval(owner) },
		func(a ProtectedInitialApproval) error {
			if err := core.WritePrivateJSON(protectedApprovalPath(owner), a); err != nil {
				return err
			}
			f, err := os.Open(dir)
			if err != nil {
				return err
			}
			return errors.Join(f.Sync(), f.Close())
		},
		func(ctx context.Context, p Plan) (tailnet.Identity, error) {
			return m.beforeInstall(ctx, dir, p, out, dir)
		})
}

func confirmProtectedRemoteInitial(ctx context.Context, m Manager, owner int, origin, listen string, sequence uint64, release, coreHash string, load func() (ProtectedInitialApproval, error), save func(ProtectedInitialApproval) error, before func(context.Context, Plan) (tailnet.Identity, error)) (tailnet.Identity, error) {
	var empty tailnet.Identity
	if owner <= 0 || sequence == 0 || !validApprovalDigest(release) || coreHash != "" && !validApprovalDigest(coreHash) {
		return empty, errors.New("protected Remote installation identity required")
	}
	plan, err := m.Plan(ctx, "tailscale", listen)
	if err != nil {
		return empty, err
	}
	actual, _, err := tailnet.Origin(origin)
	expected, _, expectedErr := tailnet.Origin(plan.Origin)
	if err != nil || expectedErr != nil || actual != expected {
		return empty, errors.New("Remote installation requires this enrolled device's private HTTPS")
	}
	if approval, err := load(); err == nil {
		if !approval.Matches(owner, origin, listen, sequence, release, coreHash) {
			return empty, errors.New("protected Remote approval differs; preserve it")
		}
		return approval.Device, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	plan.Origin = origin
	device, err := before(ctx, plan)
	if err != nil {
		return empty, err
	}
	approval := ProtectedInitialApproval{Version: 1, Owner: owner, Origin: origin, Listen: listen, Sequence: sequence, ReleaseSHA256: release, CoreSHA256: coreHash, Device: device}
	if !approval.Matches(owner, origin, listen, sequence, release, coreHash) {
		return empty, errors.New("private browser identity is invalid")
	}
	if err = save(approval); err != nil {
		return empty, err
	}
	return device, nil
}

// BindProtectedInitialCore completes the same release-bound approval only
// after the helper independently verifies the downloaded bundle.
func BindProtectedInitialCore(owner int, origin, listen string, sequence uint64, release, coreHash string) error {
	a, err := ReadProtectedInitialApproval(owner)
	if err != nil {
		return err
	}
	bound, changed, err := bindProtectedInitialCore(a, owner, origin, listen, sequence, release, coreHash)
	if err != nil || !changed {
		return err
	}
	if err = core.WritePrivateJSON(protectedApprovalPath(owner), bound); err != nil {
		return err
	}
	f, err := os.Open(filepath.Dir(protectedApprovalPath(owner)))
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func bindProtectedInitialCore(a ProtectedInitialApproval, owner int, origin, listen string, sequence uint64, release, coreHash string) (ProtectedInitialApproval, bool, error) {
	if !validApprovalDigest(coreHash) {
		return a, false, errors.New("verified Remote application required")
	}
	if !a.Matches(owner, origin, listen, sequence, release, "") || a.CoreSHA256 != "" && a.CoreSHA256 != coreHash {
		return a, false, errors.New("protected Remote approval differs; preserve it")
	}
	if a.CoreSHA256 == coreHash {
		return a, false, nil
	}
	a.CoreSHA256 = coreHash
	return a, true, nil
}
