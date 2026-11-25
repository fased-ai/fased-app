//go:build !linux

package main

import (
	"errors"
	"io"
)

func maintenanceCustodyCommand(string, []string, io.Writer) error {
	return errors.New("protected custody maintenance requires Linux")
}
func maintenanceSuccessorCommand([]string) error {
	return errors.New("protected custody maintenance requires Linux")
}
func maintenancePeerCommand([]string, io.Reader, io.Writer, bool) error {
	return errors.New("protected custody maintenance requires Linux")
}

func maintenanceRequestCommand([]string, io.Reader, io.Writer) error {
	return errors.New("protected maintenance requires supported Linux")
}

func maintenanceRegisterCommand([]string, io.Writer) error {
	return errors.New("protected maintenance requires supported Linux")
}

func maintenancePackageRegisterCommand([]string, io.Writer) error {
	return errors.New("protected maintenance requires supported Linux")
}

func maintenancePackageRequestCommand([]string, io.Reader, io.Writer) error {
	return errors.New("protected maintenance requires supported Linux")
}

func maintenancePackageGateCommand([]string) error {
	return errors.New("protected maintenance requires supported Linux")
}

func maintenancePackageWorkerCommand(string, []string) error {
	return errors.New("protected maintenance requires supported Linux")
}

func maintenanceBootstrapCommand([]string, io.Writer) error {
	return errors.New("protected maintenance is supported only on Linux")
}

func maintenanceTransitionStatusCommand([]string, io.Writer) error {
	return errors.New("protected maintenance is supported only on Linux")
}

func maintenancePackageTransitionCommand([]string) error {
	return errors.New("protected package maintenance requires Linux")
}

func maintenancePackageMigrateUnitsCommand([]string, io.Writer) error {
	return errors.New("protected package maintenance requires Linux")
}

func maintenancePackageAdoptLocalCommand(string, []string, io.Writer) error {
	return errors.New("protected Local adoption requires Linux")
}

func maintenanceLocalAdoptionImageCommand([]string, io.Reader, io.Writer) error {
	return errors.New("protected Local adoption requires Linux")
}
