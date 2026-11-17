//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"

	"github.com/fased-ai/fased-app/internal/bundle"
	"golang.org/x/sys/unix"
)

// Only a fully sealed running image with the bundle's exact identity can stand
// in for its on-disk engine. Installation itself copies from the verified bundle.
func sealedInstallationEngine(engine string, expected bundle.File) bool {
	name, err := os.Readlink("/proc/self/exe")
	if err != nil || name != "/memfd:fased-verified-executable (deleted)" || engine != name && engine != "/memfd:fased-verified-executable" {
		return false
	}
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Bytes || expected.Bytes <= 0 || expected.Bytes > 128<<20 {
		return false
	}
	seals, err := unix.FcntlInt(f.Fd(), unix.F_GET_SEALS, 0)
	required := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if err != nil || seals&required != required {
		return false
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(f, expected.Bytes+1), make([]byte, 32<<10))
	return err == nil && n == expected.Bytes && hex.EncodeToString(h.Sum(nil)) == expected.SHA256
}
