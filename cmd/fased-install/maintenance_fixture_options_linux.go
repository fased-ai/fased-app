//go:build linux && custody_acceptance_fixture && wallet_payload

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

// These public pins are supplied only when building the explicitly tagged
// disposable helper. No environment, browser or command-line override exists.
var fixtureSeedSHA256 string
var fixtureCASHA256 string

func fixtureProtectedInput(path, expected string) ([]byte, error) {
	f, err := update.OpenProtected(path, update.MaxDocument, 0644)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, update.MaxDocument+1))
	err = errors.Join(err, f.Close())
	h := sha256.Sum256(b)
	if err != nil || len(b) == 0 || len(b) > update.MaxDocument || hex.EncodeToString(h[:]) != expected {
		return nil, errors.New("protected acceptance input differs")
	}
	return b, nil
}

func init() {
	compiledMaintenanceOptions = func(parent string) (bootstrap.Options, error) {
		product, err := os.ReadFile("/sys/class/dmi/id/product_name")
		if err != nil || !(strings.Contains(string(product), "QEMU") || strings.Contains(string(product), "Standard PC")) {
			return bootstrap.Options{}, errors.New("disposable QEMU fixture required")
		}
		seed, err := fixtureProtectedInput("/var/lib/fased-update-fixture/seed.json", fixtureSeedSHA256)
		if err != nil {
			return bootstrap.Options{}, err
		}
		var binding bootstrap.Seed
		if update.StrictJSON(seed, &binding) != nil || binding.Version != 1 || !binding.DevelopmentOnly || binding.Purpose != bootstrap.Purpose || binding.Channel != "development" || binding.Origin != "https://127.0.0.1:17449" {
			return bootstrap.Options{}, errors.New("loopback-only development seed required")
		}
		ca, err := fixtureProtectedInput("/var/lib/fased-update-fixture/ca.pem", fixtureCASHA256)
		if err != nil {
			return bootstrap.Options{}, err
		}
		parent = filepath.Join(parent, "fased-app", "install")
		if err := core.EnsurePrivateDirectory(parent); err != nil {
			return bootstrap.Options{}, err
		}
		return bootstrap.Options{Development: true, Seed: seed, SeedSHA256: fixtureSeedSHA256, TLSCA: ca, Directory: filepath.Join(parent, "verified-"+update.NewProbe())}, nil
	}
}
