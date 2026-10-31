package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitialInstallerHasNoPublicOrImplicitExecutionDefaults(t *testing.T) {
	for _, args := range [][]string{nil, {"--install"}, {"--development"}, {"--development", "--seed-file", "absent", "--seed-sha256", "test", "--output", "/absent", "--prefix", "/app"}} {
		if err := run(args, nil, io.Discard, io.Discard); err == nil {
			t.Fatal("public/implicit bootstrap accepted", args)
		}
	}
}

func TestInstallerUpgradeRefusesMixedModesBeforeAcquisition(t *testing.T) {
	for _, args := range [][]string{{"--upgrade", "--install"}, {"--remote"}, {"--upgrade", "--start=false"}} {
		if e := run(args, nil, io.Discard, io.Discard); e == nil || !strings.Contains(e.Error(), "choose installation") {
			t.Fatal(args, e)
		}
	}
}

func TestRetailInstallerUsesPublisherBindingWithoutCustomerTrustForms(t *testing.T) {
	if _, e := retailInstallerOptions(nil, t.TempDir()); e == nil || strings.Contains(e.Error(), "seed") {
		t.Fatal("customer exposed to internal trust fields", e)
	}
	root := t.TempDir()
	o, e := retailInstallerOptions(&firstInstallBinding{Seed: []byte("disposable public root fixture"), SHA256: strings.Repeat("a", 64)}, root)
	if e != nil || o.Development || len(o.TLSCA) != 0 || !strings.HasPrefix(o.Directory, filepath.Join(root, "fased-app", "install", "verified-")) {
		t.Fatal(o, e)
	}
	if _, e = os.Stat(o.Directory); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("output created before authenticated acquisition", e)
	}
}
