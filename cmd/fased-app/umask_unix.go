//go:build unix

package main

import "syscall"

func applyRestrictiveUmask() {
	syscall.Umask(0o077)
}
