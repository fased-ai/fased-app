//go:build linux

package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
)

// Run calls this only after authenticated acquisition and exact-image checks.
// The signer comes from the already verified installer, not a new release asset
// or caller-selected path; source/version must equal its authenticated core.
func (p *Prepared) walletEnrollmentArguments(instance, network string, payload *WalletPayload) ([]string, error) {
	if !ValidWalletEnrollment(instance, network) {
		return nil, errors.New("choose the exact existing workspace and wallet network")
	}
	if payload == nil || len(payload.Data) == 0 || len(payload.Data) > 64<<20 || payload.SHA256 != digest(payload.Data) || payload.Version != p.descriptor.ProductVersion || payload.Source != p.descriptor.Source {
		return nil, errors.New("matching protected Wallets payload is not available in this verified installer; existing data is unchanged")
	}
	f, err := p.root.OpenFile("wallet-signer", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return nil, errors.New("wallet staging exists; preserve it and restart the verified installer")
	}
	_, err = f.Write(payload.Data)
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		return nil, err
	}
	path := filepath.Join(p.options.Directory, "wallet-signer")
	return []string{"custody", "enroll", "--ordinary-custody", "--source", "remote", "--expected-instance", instance, "--network", network, "--signer-binary", path, "--disposable-fixture", "--prepare-host", "--start-existing"}, nil
}
