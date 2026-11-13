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

func maintenanceBootstrapCommand([]string, io.Writer) error {
	return errors.New("protected maintenance is supported only on Linux")
}

func maintenanceTransitionStatusCommand([]string, io.Writer) error {
	return errors.New("protected maintenance is supported only on Linux")
}
