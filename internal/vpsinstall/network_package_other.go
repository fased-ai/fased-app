//go:build !linux

package vpsinstall

import "github.com/fased-ai/fased-app/internal/custodyinstall"

func PackageNetworkManifest(custodyinstall.Manifest) (Manifest, error) {
	return Manifest{}, unsupported
}

func LoadPackageNetwork(int, string) (Manifest, error) {
	return Manifest{}, unsupported
}
