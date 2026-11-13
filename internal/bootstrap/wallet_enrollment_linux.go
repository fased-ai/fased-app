//go:build linux

package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Run calls this only after authenticated acquisition and exact-image checks.
// The signer comes from the already verified installer, not a new release asset
// or caller-selected path; source/version must equal its authenticated core.
func (p *Prepared) walletEnrollmentArguments(instance, network string, payload *WalletPayload) ([]string, *os.File, error) {
	if !ValidWalletEnrollment(instance, network) {
		return nil, nil, errors.New("choose the exact existing workspace and wallet network")
	}
	if payload == nil || len(payload.Data) == 0 || len(payload.Data) > 64<<20 || payload.SHA256 != digest(payload.Data) || payload.Version != p.descriptor.ProductVersion || payload.Source != p.descriptor.Source {
		return nil, nil, errors.New("matching protected Wallets payload is not available in this verified installer; existing data is unchanged")
	}
	// Production provenance binds the signer only through the installer image
	// that embeds it. The payload's own digest and source fields are not proof
	// that those bytes came from the independently authenticated installer.
	if p.seed.Version >= 3 {
		if !p.report.ProvenanceVerified || verifyRunningInstaller(p.descriptor.Release.InstallerSHA256) != nil {
			return nil, nil, errors.New("running Wallets installer differs from the authenticated release")
		}
	}
	f, err := sealVerifiedExecutable(bytes.NewReader(payload.Data), int64(len(payload.Data)), payload.SHA256)
	if err != nil {
		return nil, nil, err
	}
	return []string{"custody", "enroll", "--ordinary-custody", "--source", "remote", "--expected-instance", instance, "--network", network, "--signer-binary", "/proc/self/fd/4", "--disposable-fixture", "--prepare-host", "--start-existing"}, f, nil
}

func verifyRunningInstaller(expected string) error {
	if !hashOK(expected) {
		return errors.New("authenticated installer digest required")
	}
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return err
	}
	defer f.Close()
	name, err := os.Readlink("/proc/self/exe")
	if err != nil {
		return err
	}
	if strings.HasPrefix(name, "/memfd:") {
		seals, err := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
		required := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
		if err != nil || seals&required != required {
			return errors.New("running installer memory image must be sealed")
		}
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxArchiveBytes {
		return errors.New("bounded running installer required")
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(f, info.Size()+1), make([]byte, 32*1024))
	if err != nil || n != info.Size() || hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("running installer digest differs from the authenticated release")
	}
	return nil
}
