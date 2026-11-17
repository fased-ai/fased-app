//go:build !linux

package main

import (
	"errors"
	"io"
)

func validateReusableSetupCommand([]string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("atomic custody workers require Linux")
}

func validateStageRecoveryCommand([]string, io.Writer) error {
	return errors.New("atomic custody workers require Linux")
}

func validatePreparationStartCommand([]string, io.Writer) error {
	return errors.New("atomic custody workers require Linux")
}

func validatePreparationFenceCommand([]string, io.Writer) error {
	return errors.New("atomic custody workers require Linux")
}

func preparationHoldCommand([]string, io.Writer, io.Writer) error {
	return errors.New("atomic custody workers require Linux")
}

func validateAtomicWorkerCommand([]string, io.Writer, io.Writer) error {
	return errors.New("atomic custody workers require Linux")
}

func validateAtomicProvisionCommand([]string, io.Writer, io.Writer) error {
	return errors.New("atomic custody workers require Linux")
}
