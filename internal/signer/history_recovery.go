package signer

import (
	"bytes"
	"encoding/hex"
)

// The existing encrypted portable format is shared by offline inspection and
// protected owner recovery. It is historical evidence, never permission to spend.
const MaxRecoveryBackup = 18 << 20
const historyRecoveryFeature = "historical-recovery-hold-v1"
const ownerHistoryRecoveryFeature = "owner-history-recovery-v1"
const portableMagic = "FASED-ENCRYPTED-HISTORY-FIXTURE:v1\n"
const portablePurpose = "fased-app-portable-history-fixture:v1"
const ownerPortableMagic = "FASED-ENCRYPTED-HISTORY:v1\n"
const ownerPortablePurpose = "fased-app-portable-history:v1"

func IsHistoryBackup(b []byte) bool {
	return bytes.HasPrefix(b, []byte(portableMagic)) || bytes.HasPrefix(b, []byte(ownerPortableMagic))
}

func portableProfile(fixtureOnly bool) (magic, purpose string) {
	if fixtureOnly {
		return portableMagic, portablePurpose
	}
	return ownerPortableMagic, ownerPortablePurpose
}

func validHistoryHold(e envelope) bool {
	if e.RecoveredHistorySHA256 == "" {
		return true
	}
	h, err := hex.DecodeString(e.RecoveredHistorySHA256)
	return err == nil && len(h) == 32 && hex.EncodeToString(h) == e.RecoveredHistorySHA256 && e.Purpose == "vault" && e.RecoveryRequired
}
