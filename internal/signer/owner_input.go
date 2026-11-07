package signer

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// OwnerIntent is public. It is not an authorization, a path, or an RPC request.
// Only the independently authenticated OS-owner helper may consume OwnerInput.
type OwnerIntent struct {
	Version     int    `json:"version"`
	InstanceID  string `json:"instanceId"`
	OperationID string `json:"operationId"`
	Action      string `json:"action"`
	Network     string `json:"network"`
}

func (i OwnerIntent) Validate() error {
	id, err := base64.RawURLEncoding.DecodeString(i.InstanceID)
	if i.Version != 1 || err != nil || len(id) != 32 || base64.RawURLEncoding.EncodeToString(id) != i.InstanceID || !validID(i.OperationID) || !managedNetwork(i.Network) || (i.Action != "setup" && i.Action != "import" && i.Action != "recover") {
		return errors.New("invalid wallet owner operation")
	}
	return nil
}

// OwnerInput travels only between the isolated native owner window and the
// immutable owner helper over a private pipe/verified SSH channel. It must never
// be sent to the core, stored as a request, or written to diagnostics.
type OwnerInput struct {
	Intent         OwnerIntent `json:"intent"`
	SignerID       string      `json:"signerId"`
	Phase          string      `json:"phase"`
	Unlock         []byte      `json:"unlock"`
	BackupPassword []byte      `json:"backupPassword"`
	Seed           []byte      `json:"seed,omitempty"`
	Backup         []byte      `json:"backup,omitempty"`
	Fingerprint    string      `json:"fingerprint,omitempty"`
}

const MaxOwnerInput = 2*MaxRecoveryBackup + 64*1024

func (i *OwnerInput) Clear() {
	clear(i.Unlock)
	clear(i.BackupPassword)
	clear(i.Seed)
	clear(i.Backup)
}

func (i OwnerInput) Validate() error {
	if i.Intent.Validate() != nil || !validID(i.SignerID) || len(i.Unlock) < 16 || len(i.Unlock) > 1024 || len(i.BackupPassword) < 16 || len(i.BackupPassword) > 1024 {
		return errors.New("wallet passwords must be between 16 and 1024 bytes")
	}
	if i.Phase != "prepare" && i.Phase != "verify" {
		return errors.New("invalid wallet owner phase")
	}
	if i.Intent.Action == "import" {
		if len(i.Seed) != 32 {
			return errors.New("import requires one 32-byte seed")
		}
	} else if len(i.Seed) != 0 {
		return errors.New("seed is accepted only for import")
	}
	if i.Intent.Action == "recover" || i.Phase == "verify" {
		if len(i.Backup) == 0 || len(i.Backup) > MaxRecoveryBackup || i.Fingerprint != BackupFingerprint(i.Backup) {
			return errors.New("recovery requires the encrypted backup and its independent fingerprint")
		}
	} else if len(i.Backup) != 0 || i.Fingerprint != "" {
		return errors.New("recovery input requires recover")
	}
	return nil
}

func BackupFingerprint(document []byte) string {
	h := sha256.Sum256(document)
	return hex.EncodeToString(h[:])
}

type OwnerResult struct {
	Intent         OwnerIntent  `json:"intent"`
	SignerID       string       `json:"signerId"`
	Status         PublicStatus `json:"status"`
	BackupVerified bool         `json:"backupVerified"`
	Backup         []byte       `json:"backup"`
	Fingerprint    string       `json:"fingerprint"`
}
