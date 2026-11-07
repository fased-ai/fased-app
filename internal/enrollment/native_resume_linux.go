//go:build linux

package enrollment

import (
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/signer"
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
	if j.CoreRecovery != nil && j.Phase == "suspended" {
		return false, nil
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

// Eligibility is read-only and grants no setup or key authority. The existing
// approval terminal revalidates private emptiness and OS state before recovery.
func CanRecoverNative(instance, profile, network, coreHash, signerHash string, native bool) (bool, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return false, err
	}
	j, err := readOwnerJournal(filepath.Join(config, "fased-app-enrollment", "state.json"))
	if err != nil {
		return false, err
	}
	if !nativeRecoveryMatches(j, instance, profile, network, coreHash, signerHash, native) {
		return false, nil
	}
	if n, err := realNode(j.Source.Data); err != nil || n != j.Node {
		return false, nil
	}
	base := j.Target
	if j.CoreRecovery != nil {
		base = j.CoreRecovery.Previous
	}
	return custodyinstall.CanRecoverEnrollmentCore(base, j.ID, coreHash), nil
}

func nativeRecoveryMatches(j journal, instance, profile, network, coreHash, signerHash string, native bool) bool {
	return j.valid(os.Getuid()) && j.Target.ID != "" && j.Phase == "suspended" && j.Digest == "" && j.Source.ExpectedInstanceID == instance && profile == "remote" && j.Source.Profile == profile && j.Network == network && !native && !j.NativeOwnerAccess && j.Mode == signer.OrdinaryCustodyMode && hexID(coreHash, 32) && signerHash == j.SignerHash && (j.CoreRecovery == nil && coreHash != j.CoreHash || j.CoreRecovery != nil && j.CoreRecovery.NextCoreHash == coreHash)
}
