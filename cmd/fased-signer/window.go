//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

// This is an owner-helper input adapter, not a network/API owner endpoint. The
// caller must pass CheckOwner before any input is read. Public request-stdin and
// the app Unix socket retain their original, secret-free contracts.
func ownerWindow(ctx context.Context, c signer.Config, expectedInstance string, input io.Reader, out io.Writer) error {
	if err := signer.CheckOwner(c); err != nil {
		return err
	}
	var request signer.OwnerInput
	defer request.Clear()
	if err := readOwnerWindowInput(c, expectedInstance, input, &request); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	root := filepath.Join(home, ".local", "state", "fased", "wallet-owner", c.SignerID)
	return conductWindow(ctx, c, root, request, out, func(ctx context.Context, r signer.Request) (signer.Response, error) {
		return signer.Call(ctx, c, true, r)
	})
}

func readOwnerWindowInput(c signer.Config, expectedInstance string, input io.Reader, request *signer.OwnerInput) error {
	b, err := io.ReadAll(io.LimitReader(input, signer.MaxOwnerInput+1))
	defer clear(b)
	if err != nil || len(b) > signer.MaxOwnerInput {
		return errors.New("invalid protected wallet input")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(request) != nil || d.Decode(new(any)) != io.EOF || request.Validate() != nil || request.SignerID != c.SignerID || request.Intent.Network != c.Network || request.Intent.InstanceID != expectedInstance {
		return errors.New("protected wallet input does not match this signer")
	}
	return nil
}

func conductWindow(ctx context.Context, c signer.Config, root string, request signer.OwnerInput, out io.Writer, call ownerCall) (result error) {
	if request.Validate() != nil || request.SignerID != c.SignerID || request.Intent.Network != c.Network {
		return errors.New("protected wallet identity mismatch")
	}
	// Validate every existing ancestor before creating a private child. Never
	// repair a foreign directory or follow a link supplied by another identity.
	if err := createOwnerDirectory(root); err != nil {
		return err
	}
	if err := preflightOwnerFile(filepath.Join(root, "operation.json")); err != nil {
		return err
	}
	intent := struct {
		Intent signer.OwnerIntent `json:"intent"`
		Signer string             `json:"signerId"`
	}{request.Intent, c.SignerID}
	binding, _ := json.Marshal(intent)
	path := filepath.Join(root, "operation.json")
	existing, err := readOwnerFile(path)
	if err == nil {
		if !bytes.Equal(existing, binding) {
			return errors.New("resume the existing wallet operation; a second key is refused")
		}
	} else if errors.Is(err, os.ErrNotExist) && request.Phase == "prepare" {
		if err = writeOwnerFile(path, binding); err != nil {
			return err
		}
	} else {
		return errors.New("wallet operation is unavailable")
	}
	// Failure/cancellation must lock independently of the cancelled parent.
	defer func() {
		lock, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := call(lock, signer.Request{Action: "lock"})
		result = errors.Join(result, err)
	}()
	backupPath := filepath.Join(root, "backup.json")
	if request.Phase == "prepare" {
		if request.Intent.Action == "recover" {
			prior, err := readPrivateBoundedFile(backupPath, signer.MaxRecoveryBackup)
			if err == nil && !bytes.Equal(prior, request.Backup) {
				return errors.New("existing recovery backup is preserved")
			}
			if errors.Is(err, os.ErrNotExist) {
				err = writeOwnerFile(backupPath, request.Backup)
			}
			if err != nil {
				return err
			}
		}
		ask := func(label string, _ bool) ([]byte, error) {
			if strings.Contains(label, "backup") {
				return bytes.Clone(request.BackupPassword), nil
			}
			return bytes.Clone(request.Unlock), nil
		}
		wrapped := call
		if request.Intent.Action == "import" {
			key := ed25519.NewKeyFromSeed(request.Seed)
			pub := hex.EncodeToString(key.Public().(ed25519.PublicKey))
			clear(key)
			wrapped = func(ctx context.Context, r signer.Request) (signer.Response, error) {
				if r.Action == "create" {
					r.Action = "import-managed-seed"
					if c.Version == 3 {
						r.Action = "import-ordinary-seed"
					}
					r.Document = bytes.Clone(request.Seed)
					defer clear(r.Document)
				}
				response, err := call(ctx, r)
				if r.Action == "status" && err == nil && response.Status.Account != nil && response.Status.Account.PublicKey != pub {
					return signer.Response{}, errors.New("existing wallet differs from the imported key")
				}
				return response, err
			}
		}
		action := "setup"
		if request.Intent.Action == "recover" {
			action = "recover"
		}
		// The conductor's output includes an owner path; it stays private here.
		if err := conductFlow(ctx, action, backupPath, ask, io.Discard, wrapped, request.Fingerprint); err != nil {
			return err
		}
	} else {
		backup, err := readPrivateBoundedFile(backupPath, signer.MaxRecoveryBackup)
		if err != nil || !bytes.Equal(backup, request.Backup) {
			return errors.New("local backup acknowledgement differs from the original")
		}
		if _, err = call(ctx, signer.Request{Action: "unlock", Password: bytes.Clone(request.Unlock), UnlockSeconds: 60}); err != nil {
			return err
		}
		if _, err = call(ctx, signer.Request{Action: "verify-backup", Document: bytes.Clone(request.Backup), Password: bytes.Clone(request.BackupPassword)}); err != nil {
			return err
		}
	}
	backup, err := readPrivateBoundedFile(backupPath, signer.MaxRecoveryBackup)
	if err != nil {
		return err
	}
	locked, err := call(ctx, signer.Request{Action: "lock"})
	if err != nil || !locked.Status.Locked || locked.Status.Account == nil || locked.Status.WalletSigner || locked.Status.FinancialExecution {
		return errors.New("wallet operation did not finish locked")
	}
	return json.NewEncoder(out).Encode(signer.OwnerResult{Intent: request.Intent, SignerID: c.SignerID, Status: locked.Status.PublicView(), BackupVerified: request.Phase == "verify", Backup: backup, Fingerprint: signer.BackupFingerprint(backup)})
}

func createOwnerDirectory(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err = createOwnerDirectory(filepath.Dir(path)); err != nil {
			return err
		}
		if err = os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	// Intermediate OS-owner ancestors can be readable but never writable by
	// another identity. The final directory is checked private by the caller.
	fd, err := ownerDirectory(filepath.Join(path, ".owner-preflight"), false)
	if err != nil {
		return err
	}
	return os.NewFile(uintptr(fd), path).Close()
}
