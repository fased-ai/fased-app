//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func packageMaintenanceArguments(args []string) (int, string, string, error) {
	if len(args) != 6 || args[0] != "--owner" || args[2] != "--location" || args[4] != "--instance" {
		return 0, "", "", errors.New("fixed package maintenance identity required")
	}
	owner, err := strconv.Atoi(args[1])
	if err != nil || strconv.Itoa(owner) != args[1] {
		return 0, "", "", errors.New("invalid package owner")
	}
	if _, err = update.PackageMaintenanceRoot(owner, args[3], args[5]); err != nil {
		return 0, "", "", err
	}
	if compiledFixtureSourceAllowed != nil && !fixturePackageCandidateAllowed(owner, args[3], args[5]) {
		return 0, "", "", errors.New("the exact authorized disposable package is required")
	}
	return owner, args[3], args[5], nil
}

// A real-host test helper must never act on an existing owner-key instance.
// Production helpers have no compiled fixture selector and use their normal
// authenticated package identity checks.
func requireFixturePackage(owner int, location, instance string, allowMissing bool) error {
	if compiledFixtureSourceAllowed == nil {
		return nil
	}
	root, err := custodyinstall.PackageRoot(owner, location, instance)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(root); errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil
	} else if err != nil {
		return err
	}
	m, err := custodyinstall.LoadPackageInstance(owner, root)
	if err != nil {
		return errors.New("the existing fixture package is unclassified; preserve it")
	}
	return requireControlledFixtureManifest(m)
}

func requireControlledFixtureManifest(m custodyinstall.Manifest) error {
	if m.Provenance != signer.ControlledCustodyProfile {
		return errors.New("the real-host fixture cannot operate on owner-key custody")
	}
	return nil
}

func maintenancePackageRegisterCommand(args []string, out io.Writer) error {
	owner, location, instance, err := packageMaintenanceArguments(args)
	if err != nil {
		return err
	}
	if err = requireFixturePackage(owner, location, instance, true); err != nil {
		return err
	}
	a, err := maintenance.RegisterPackage(owner, location, instance)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, maintenance.ImagePath(a.ImageSHA256))
	return err
}

func maintenancePackageGateCommand(args []string) error {
	if len(args) != 8 || args[6] != "--role" || (args[7] != "app" && args[7] != "signer") {
		return errors.New("fixed package service role required")
	}
	owner, location, instance, err := packageMaintenanceArguments(args[:6])
	if err != nil {
		return err
	}
	if err = requireFixturePackage(owner, location, instance, false); err != nil {
		return err
	}
	return custodyinstall.VerifyPackageStart(owner, location, instance, args[7])
}

func maintenancePackageWorkerCommand(action string, args []string) error {
	if action != "maintenance-package-worker" && action != "maintenance-package-recover" || len(args) < 6 {
		return errors.New("fixed package worker required")
	}
	owner, location, instance, err := packageMaintenanceArguments(args[:6])
	if err != nil {
		return err
	}
	boot := action == "maintenance-package-recover"
	if boot && len(args) != 6 || !boot && (len(args) != 8 || args[6] != "--operation" || !update.IDOK(args[7])) {
		return errors.New("one exact package operation required")
	}
	if err = requireFixturePackage(owner, location, instance, false); err != nil {
		return err
	}
	a, err := maintenance.ValidatePackageProcess(owner, location, instance)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	id := ""
	if boot {
		lock, e := maintenance.WaitPackageLock(ctx, owner, location, instance)
		if e != nil {
			return e
		}
		if _, e = maintenance.ValidatePackageProcess(owner, location, instance); e != nil {
			return errors.Join(e, lock.Close())
		}
		if e = maintenance.RestorePackageServiceExecutables(owner, location, instance); e != nil {
			return errors.Join(e, lock.Close())
		}
		r, readErr := update.ReadPackageMaintenanceRecord(owner, location, instance)
		if readErr == nil && r.Phase == "wallet-initializing" {
			e = custodyinstall.RecoverProtectedPackageWallet(ctx, owner, location, instance)
			return errors.Join(e, lock.Close())
		}
		if readErr == nil && r.Phase == "idle" {
			m, validErr := custodyinstall.ValidateIdlePackageMaintenanceRecord(r, owner, location, instance)
			if validErr == nil {
				var actual custodyinstall.Manifest
				actual, validErr = custodyinstall.LoadPackageInstance(owner, m.Root)
				if validErr == nil && actual != m {
					validErr = errors.New("initial package identity changed")
				}
				if validErr == nil {
					validErr = custodyinstall.VerifyPackageInstance(actual)
				}
				if validErr == nil && actual.Network == "" {
					validErr = custodyinstall.VerifyUnconfiguredPackageSigner(ctx, actual)
				}
				if validErr == nil && actual.Network != "" {
					_, validErr = custodyinstall.ReadEnrollmentCommit(actual)
				}
				if validErr == nil {
					for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
						if _, e := os.Lstat(actual.Root + "/" + name); !errors.Is(e, os.ErrNotExist) {
							validErr = errors.New("an existing package recovery fence must be preserved")
							break
						}
					}
				}
			}
			return errors.Join(validErr, lock.Close())
		}
		if readErr == nil {
			id = r.OperationID
		}
		if e = errors.Join(readErr, lock.Close()); e != nil {
			return e
		}
	} else {
		id = args[7]
		lock, e := maintenance.PackageLock(owner, location, instance)
		if e != nil {
			return e
		}
		if _, e = maintenance.ValidatePackageProcess(owner, location, instance); e == nil {
			e = maintenance.RestorePackageServiceExecutables(owner, location, instance)
		}
		if e == nil {
			var record update.MaintenanceRecord
			record, e = update.ReadPackageMaintenanceRecord(owner, location, instance)
			if e == nil && record.OperationID != id {
				e = maintenancePackageAcquire(ctx, a, location, instance, id)
			}
		}
		if e = errors.Join(e, lock.Close()); e != nil {
			return e
		}
	}
	return custodyinstall.RunProtectedPackageUpdate(ctx, owner, location, instance, id, boot)
}

func maintenancePackageRequestCommand(args []string, in io.Reader, out io.Writer) error {
	owner, location, instance, err := packageMaintenanceArguments(args)
	if err != nil {
		return err
	}
	if _, err = maintenance.ValidatePackageProcess(owner, location, instance); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, stream, err := readMaintenanceFrame(ctx, in)
	if err != nil {
		return errors.New("one bounded package request required")
	}
	r, err := maintenance.DecodePackageRequest(b)
	if err != nil {
		return err
	}
	streamAction := r.Action == "candidate-stage" || r.Action == "owner-channel"
	if streamAction != (stream != nil) {
		return errors.New("package operation requires its fixed request framing")
	}
	lock, err := maintenance.PackageLock(owner, location, instance)
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err = maintenance.ValidatePackageProcess(owner, location, instance); err != nil {
		return err
	}
	allowMissing := r.Action == "provision" || r.Action == "candidate-stage" && r.Candidate.Scope == "fresh"
	if err = requireFixturePackage(owner, location, instance, allowMissing); err != nil {
		return err
	}
	workCtx, stop := context.WithTimeout(context.Background(), 10*time.Minute)
	defer stop()
	if stream != nil {
		if closer, ok := in.(io.Closer); ok {
			stopClose := context.AfterFunc(workCtx, func() { _ = closer.Close() })
			defer stopClose()
		}
	}
	if r.Action == "owner-channel" {
		ownerCtx, cancelOwner := context.WithTimeout(workCtx, 20*time.Second)
		defer cancelOwner()
		if closer, ok := in.(io.Closer); ok {
			stopClose := context.AfterFunc(ownerCtx, func() { _ = closer.Close() })
			defer stopClose()
		}
		if closer, ok := out.(io.Closer); ok {
			stopClose := context.AfterFunc(ownerCtx, func() { _ = closer.Close() })
			defer stopClose()
		}
		return custodyinstall.PackageOwnerChannel(ownerCtx, owner, location, instance, lock, stream, out)
	}
	if r.Action == "candidate-stage" {
		receipt, err := maintenancePackageStageCandidate(workCtx, owner, location, instance, *r.Candidate, stream)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(maintenance.Response{Version: 1, Action: r.Action, Complete: true, Payload: payload})
	}
	if r.Action == "initialize" {
		if err := maintenancePackageInitialize(workCtx, owner, location, instance); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(maintenance.Response{Version: 1, Action: r.Action, Complete: true})
	}
	if r.Action == "provision" {
		if err := maintenancePackageProvision(workCtx, owner, location, instance, r.Listen, lock); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(maintenance.Response{Version: 1, Action: r.Action, Complete: true})
	}
	if r.Action == "wallet-initialize" {
		a, err := maintenance.ValidatePackageProcess(owner, location, instance)
		if err != nil {
			return err
		}
		if err = preparePackageWalletAfterUpdate(workCtx, a, location, instance, *r.Wallet); err != nil {
			return err
		}
		if err := custodyinstall.ConfigureProtectedPackageWallet(workCtx, owner, location, instance, *r.Wallet, lock); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(maintenance.Response{Version: 1, Action: r.Action, Complete: true})
	}
	record, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	var payload json.RawMessage
	if errors.Is(err, os.ErrNotExist) {
		payload = json.RawMessage("null")
	} else if err != nil {
		return err
	} else {
		if record.Phase == "idle" {
			if _, err = custodyinstall.ValidateIdlePackageMaintenanceRecord(record, owner, location, instance); err != nil {
				return err
			}
		} else if record.Phase == "wallet-initializing" {
			if _, _, err = custodyinstall.ValidatePackageWalletInitialization(record, owner, location, instance); err != nil {
				return err
			}
		} else if !record.RecoveryEligible() {
			return errors.New("unrecognized package recovery state; preserve it")
		}
		// Return only status, not the potentially large recovery journal.
		payload, err = json.Marshal(struct {
			OperationID string `json:"operationId"`
			Phase       string `json:"phase"`
			Start       string `json:"start"`
		}{record.OperationID, record.Phase, record.Start})
		if err != nil {
			return err
		}
	}
	return json.NewEncoder(out).Encode(maintenance.Response{Version: 1, Action: r.Action, Complete: true, Payload: payload})
}
