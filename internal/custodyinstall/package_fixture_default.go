//go:build !linux || !custody_acceptance_fixture || !fased_realhost_fixture

package custodyinstall

import "github.com/fased-ai/fased-app/internal/signer"

func controlledPackageFixture(Manifest) bool { return false }

func packageProvisionProvenance(int, string, string) (string, error) {
	return signer.OwnerKeyProvenance, nil
}
