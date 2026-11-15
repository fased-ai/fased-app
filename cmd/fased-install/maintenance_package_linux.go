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
	return owner, args[3], args[5], nil
}

func maintenancePackageRegisterCommand(args []string, out io.Writer) error {
	owner, location, instance, err := packageMaintenanceArguments(args)
	if err != nil {
		return err
	}
	a, err := maintenance.RegisterPackage(owner, location, instance)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, maintenance.ImagePath(a.ImageSHA256))
	return err
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
	if err != nil || stream != nil {
		return errors.New("one bounded package request required")
	}
	r, err := maintenance.DecodePackageRequest(b)
	if err != nil {
		return err
	}
	lock, err := maintenance.PackageLock(owner, location, instance)
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err = maintenance.ValidatePackageProcess(owner, location, instance); err != nil {
		return err
	}
	// Status is intentionally the only admitted action until the installer,
	// authenticated package writer and recovery worker are connected.
	record, err := update.ReadPackageMaintenanceRecord(owner, location, instance)
	var payload json.RawMessage
	if errors.Is(err, os.ErrNotExist) {
		payload = json.RawMessage("null")
	} else if err != nil {
		return err
	} else {
		if record.Phase == "idle" {
			if _, err = custodyinstall.ValidateInitialPackageMaintenanceRecord(record, owner, location, instance); err != nil {
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
