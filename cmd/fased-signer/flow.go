package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

type askPassword func(string, bool) ([]byte, error)
type ownerCall func(context.Context, signer.Request) (signer.Response, error)

func ownerFlow(ctx context.Context, c signer.Config, action, path string, password askPassword, out io.Writer) error {
	return conductFlow(ctx, action, path, password, out, func(ctx context.Context, r signer.Request) (signer.Response, error) {
		return signer.Call(ctx, c, true, r)
	})
}

// All secret input belongs to this root-owned helper running as the owner, never
// the app service. A killed helper leaves at most a 60-second manual unlock.
func conductFlow(ctx context.Context, action, path string, ask askPassword, out io.Writer, call ownerCall) (result error) {
	if path == "" {
		return errors.New("choose a private owner backup file before setup/recovery")
	}
	if err := preflightOwnerFile(path); err != nil {
		return err
	}
	// Preflight input/output without creating, truncating or overwriting a file.
	backup, readErr := readOwnerFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	defer clear(backup)
	if action == "recover" && readErr != nil {
		return readErr
	}
	status, err := call(ctx, signer.Request{Action: "status"})
	if err != nil {
		return err
	}
	if action == "recover" && status.Status.Account != nil && !status.Status.RecoveryRequired {
		return errors.New("recovery requires an empty signer or an interrupted recovery hold; existing wallets are preserved")
	}
	// Always attempt a lock after a stateful operation, using a fresh bounded
	// context even when the interactive parent context has been cancelled.
	defer func() {
		lockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, lockErr := call(lockCtx, signer.Request{Action: "lock"})
		result = errors.Join(result, lockErr)
	}()
	var unlock, backupPassword []byte
	defer func() { clear(unlock); clear(backupPassword) }()
	if action == "recover" {
		backupPassword, err = ask("Encrypted backup password", false)
		if err != nil {
			return err
		}
		if status.Status.Account == nil {
			unlock, err = ask("New fixture unlock password", true)
			if err != nil {
				return err
			}
			if _, err = call(ctx, signer.Request{Action: "restore", Document: backup, Password: backupPassword, NewPassword: unlock}); err != nil {
				return err
			}
		} else {
			unlock, err = ask("Current recovery unlock password", false)
			if err != nil {
				return err
			}
		}
	} else {
		if status.Status.Account == nil && readErr == nil {
			return errors.New("existing backup is preserved; use recovery for an empty signer")
		}
		unlock, err = ask("Fixture unlock password", status.Status.Account == nil)
		if err != nil {
			return err
		}
		if status.Status.Account == nil {
			if _, err = call(ctx, signer.Request{Action: "create", Password: unlock}); err != nil {
				return err
			}
		}
		backupPassword, err = ask("Encrypted backup password", readErr != nil)
		if err != nil {
			return err
		}
	}
	if _, err = call(ctx, signer.Request{Action: "unlock", Password: unlock, UnlockSeconds: 60}); err != nil {
		return err
	}
	if action == "setup" && readErr != nil {
		response, err := call(ctx, signer.Request{Action: "backup", Password: backupPassword})
		if err != nil {
			return err
		}
		defer clear(response.Document)
		if err = writeOwnerFile(path, response.Document); err != nil {
			return err
		}
		backup, err = readOwnerFile(path)
		if err != nil {
			return err
		}
		defer clear(backup)
	}
	if _, err = call(ctx, signer.Request{Action: "verify-backup", Document: backup, Password: backupPassword}); err != nil {
		return err
	}
	locked, err := call(ctx, signer.Request{Action: "lock"})
	if err != nil {
		return err
	}
	if !locked.Status.Locked || locked.Status.WalletSigner || locked.Status.FinancialExecution {
		return errors.New("unsafe owner-flow completion state")
	}
	return json.NewEncoder(out).Encode(struct {
		Status         signer.PublicStatus `json:"status"`
		BackupVerified bool                `json:"backupVerified"`
		BackupFile     string              `json:"backupFile"`
	}{locked.Status.PublicView(), true, path})
}

// The protected owner helper reads the seed. The existing setup flow owns
// backup publication, verification, bounded unlock and lock-on-failure.
func conductImport(ctx context.Context, seedPath, backupPath string, ask askPassword, out io.Writer, call ownerCall) error {
	return conductImportAction(ctx, seedPath, backupPath, ask, out, call, "import-fixture-seed")
}
func conductImportAction(ctx context.Context, seedPath, backupPath string, ask askPassword, out io.Writer, call ownerCall, action string) error {
	if action != "import-fixture-seed" && action != "import-managed-seed" && action != "import-ordinary-seed" {
		return signer.ErrUnavailable
	}
	seed, err := readOwnerFile(seedPath)
	if err != nil {
		return err
	}
	defer clear(seed)
	if len(seed) != 32 {
		return errors.New("expected a 32-byte disposable seed")
	}
	return conductFlow(ctx, "setup", backupPath, ask, out, func(ctx context.Context, r signer.Request) (signer.Response, error) {
		if r.Action == "create" {
			r.Action = action
			r.Document = seed
		}
		response, err := call(ctx, r)
		if r.Action == "status" && err == nil && response.Status.Account != nil {
			return signer.Response{}, errors.New("import requires an empty signer; use setup to finish an interrupted import")
		}
		return response, err
	})
}
