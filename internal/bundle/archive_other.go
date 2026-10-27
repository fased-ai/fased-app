//go:build !linux

package bundle

import "errors"

// ExtractVerifiedArchive is intentionally unavailable outside Linux.
func ExtractVerifiedArchive(archivePath, directory string, expected File, manifestSHA256 string) (Manifest, error) {
	return Manifest{}, errors.New("verified archive extraction is supported only on Linux")
}
