//go:build linux

package enrollment

import (
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// CanResumeNative is a read-only reconciliation of the EXISTING controller.
// It never creates a journal, adopts a target, clears a hold, or starts a service.
func CanResumeNative(instance, profile, network, coreHash, signerHash string, native bool) (bool, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return false, err
	}
	j, err := readOwnerJournal(filepath.Join(config, "fased-app-enrollment", "state.json"))
	if err != nil {
		return false, err
	}
	m, err := custodyinstall.Load(os.Getuid())
	if err != nil {
		return false, err
	}
	return nativeResumeMatches(j, m, instance, profile, network, coreHash, signerHash, native, custodyinstall.OwnsEnrollmentIntent(m)), nil
}

func nativeResumeMatches(j journal, m custodyinstall.Manifest, instance, profile, network, coreHash, signerHash string, native, owns bool) bool {
	return owns && j.valid(m.Owner) && j.Source.ExpectedInstanceID == instance && j.Source.Profile == profile && j.Network == network && j.CoreHash == coreHash && j.SignerHash == signerHash && j.NativeOwnerAccess == native && m.Owner == j.Owner && m.Enrollment && m.Location == profile && m.Network == network && m.CoreHash == coreHash && m.SignerHash == signerHash && m.NativeOwnerAccess == native && m.Listen == j.Source.Listen && m.PublicOrigin == j.Source.PublicOrigin && m.Mode == j.Mode && m.Profile == j.Profile && m.Provenance == j.Provenance && (j.Target.ID == "" || j.Target.ID == m.ID)
}
