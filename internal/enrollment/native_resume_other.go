//go:build !linux

package enrollment

import "errors"

func CanResumeNative(string, string, string, string, string, bool) (bool, error) {
	return false, errors.New("protected enrollment requires Linux")
}

func CanRecoverNative(string, string, string, string, string, bool) (bool, error) { return false, nil }
