//go:build !linux

package main

import "errors"

func packageLinuxSystem(_, _, _, _, _, _ string) error {
	return errors.New("Linux system packages require a Linux build host")
}
