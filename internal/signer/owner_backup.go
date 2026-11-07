package signer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/fased-ai/fased-app/internal/update"
)

// OwnerBackup is the existing native-client file container. Document contains
// only the encrypted archive; its independently retained fingerprint binds that
// archive, not the JSON container or the destination instance.
type OwnerBackup struct {
	Version     int    `json:"version"`
	InstanceID  string `json:"instanceId"`
	Network     string `json:"network"`
	Fingerprint string `json:"fingerprint"`
	Document    []byte `json:"document"`
}

// RecoveryDocument accepts the existing native container and raw CLI archive.
// It does not authenticate history or authorize recovery: the signer does that.
// A network supplied by the native client must also match container metadata.
func RecoveryDocument(data []byte, fingerprint, network string) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxOwnerInput {
		return nil, errors.New("invalid recovery file size")
	}
	var fields map[string]json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) && update.StrictJSONBounded(data, &fields, MaxOwnerInput) != nil {
		return nil, errors.New("invalid recovery file")
	}
	if fields["document"] != nil || fields["instanceId"] != nil || fields["fingerprint"] != nil {
		var b OwnerBackup
		if update.StrictJSONBounded(data, &b, MaxOwnerInput) != nil {
			return nil, errors.New("invalid owner backup")
		}
		id, err := base64.RawURLEncoding.DecodeString(b.InstanceID)
		if err != nil || len(id) != 32 || base64.RawURLEncoding.EncodeToString(id) != b.InstanceID || b.Version != 1 || !managedNetwork(b.Network) || network != "" && network != b.Network || len(b.Document) == 0 || len(b.Document) > MaxRecoveryBackup || fingerprint == "" || fingerprint != b.Fingerprint || fingerprint != BackupFingerprint(b.Document) {
			return nil, errors.New("choose the encrypted backup and its independent checksum")
		}
		return bytes.Clone(b.Document), nil
	}
	if len(data) > MaxRecoveryBackup || (fingerprint != "" && fingerprint != BackupFingerprint(data)) || IsHistoryBackup(data) && fingerprint == "" {
		return nil, errors.New("choose the encrypted backup and its independent checksum")
	}
	return bytes.Clone(data), nil
}
