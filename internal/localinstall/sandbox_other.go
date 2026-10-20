//go:build !linux

package localinstall

import "errors"

func CheckSandbox(string, string) error {
	return errors.New("Linux sandbox enforcement is not applicable on this platform")
}
func CheckManagedSandbox(string, string) error {
	return errors.New("managed Linux sandbox enforcement is unsupported on this platform")
}
