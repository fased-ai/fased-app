//go:build linux

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"golang.org/x/sys/unix"
)

func TestSealedCandidateInstallEntry(t *testing.T) {
	if mode := os.Getenv("FASED_TEST_SEALED_INSTALL"); mode != "" {
		dir, pin := os.Getenv("FASED_TEST_SEALED_BUNDLE"), os.Getenv("FASED_TEST_SEALED_PIN")
		engine, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = verifiedInstallationBundle(dir, engine, "", pin)
		if mode != "sealed" {
			if err == nil {
				t.Fatal("unsealed or different running image was accepted")
			}
			return
		}
		if err != nil {
			t.Fatal("sealed verified image was refused", err)
		}
		root, data := os.Getenv("FASED_TEST_SEALED_ROOT"), os.Getenv("FASED_TEST_SEALED_DATA")
		err = hostCommand([]string{"install", "--bundle", dir, "--bundle-sha256", pin, "--prefix", root, "--data-dir", data, "--start=false"}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "installation directory contains unclassified files") {
			t.Fatal("real installer did not preserve the existing target", err)
		}
		if b, e := os.ReadFile(filepath.Join(root, "keep")); e != nil || string(b) != "preserve" {
			t.Fatal("existing target changed", e)
		}
		return
	}
	if os.Geteuid() == 0 {
		t.Skip("ordinary-user fixture required")
	}
	dir, manifest, _ := installationBundleFixture(t, false)
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	engine, err := os.OpenFile(filepath.Join(dir, localinstall.EngineName()), os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(engine, source)
	if e := engine.Close(); err == nil {
		err = e
	}
	if err != nil {
		t.Fatal(err)
	}
	manifest.Files[localinstall.EngineName()], err = bundle.Inspect(filepath.Join(dir, localinstall.EngineName()))
	if err != nil {
		t.Fatal(err)
	}
	pin := saveInstallationBundle(t, dir, manifest)
	otherDir, _, otherPin := installationBundleFixture(t, false)
	parent := t.TempDir()
	root, data := filepath.Join(parent, "app"), filepath.Join(parent, "data")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "keep"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"sealed", "unsealed", "different"} {
		t.Run(mode, func(t *testing.T) {
			fd, e := unix.MemfdCreate("fased-verified-executable", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING|unix.MFD_EXEC)
			if e == unix.EINVAL {
				fd, e = unix.MemfdCreate("fased-verified-executable", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
			}
			if e != nil {
				t.Fatal(e)
			}
			image := os.NewFile(uintptr(fd), "sealed-fixture")
			defer image.Close()
			if _, e = source.Seek(0, io.SeekStart); e == nil {
				_, e = io.Copy(image, source)
			}
			if e == nil {
				e = image.Chmod(0500)
			}
			if e == nil && mode != "unsealed" {
				_, e = unix.FcntlInt(image.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL)
			}
			if e != nil {
				t.Fatal(e)
			}
			fixtureDir, fixturePin := dir, pin
			if mode == "different" {
				fixtureDir, fixturePin = otherDir, otherPin
			}
			command := exec.CommandContext(t.Context(), "/proc/self/fd/3", "-test.run=^TestSealedCandidateInstallEntry$")
			command.ExtraFiles = []*os.File{image}
			command.Env = append(os.Environ(), "FASED_TEST_SEALED_INSTALL="+mode, "FASED_TEST_SEALED_BUNDLE="+fixtureDir, "FASED_TEST_SEALED_PIN="+fixturePin, "FASED_TEST_SEALED_ROOT="+root, "FASED_TEST_SEALED_DATA="+data)
			if output, e := command.CombinedOutput(); e != nil {
				t.Fatalf("actual sealed installer entry: %v %s", e, output)
			}
		})
	}
}
