package signer

import (
	"bytes"
	"encoding/hex"
)

// The existing encrypted portable format is shared by offline inspection and
// protected owner recovery. It is historical evidence, never permission to spend.
const MaxRecoveryBackup = 18 << 20
const historyRecoveryFeature = "historical-recovery-hold-v1"
const portableMagic = "FASED-ENCRYPTED-HISTORY-FIXTURE:v1\n"
const portablePurpose = "fased-app-portable-history-fixture:v1"

func IsHistoryBackup(b []byte) bool { return bytes.HasPrefix(b, []byte(portableMagic)) }

func validHistoryHold(e envelope) bool {
	if e.RecoveredHistorySHA256 == "" {
		return true
	}
	h, err := hex.DecodeString(e.RecoveredHistorySHA256)
	return err == nil && len(h) == 32 && hex.EncodeToString(h) == e.RecoveredHistorySHA256 && e.Purpose == "vault" && e.RecoveryRequired
}
