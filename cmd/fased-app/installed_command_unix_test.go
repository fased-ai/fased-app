//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstallCommandPreservesExistingFasedOnPath(t *testing.T) {
	home := t.TempDir()
	oldBin := t.TempDir()
	old := filepath.Join(oldBin, "fased")
	contents := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(old, contents, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", filepath.Join(home, ".local", "bin")+string(os.PathListSeparator)+oldBin)
	var out bytes.Buffer
	if err := installCommand("/verified/fased-app", "/installed/fased-app", "/private/fased-app", false, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "fased")); !os.IsNotExist(err) {
		t.Fatal("new command would shadow the existing Fased command", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "fased-app")); err != nil {
		t.Fatal("distinct Fased App command is missing", err)
	}
	if got, err := os.ReadFile(old); err != nil || !bytes.Equal(got, contents) {
		t.Fatal("existing Fased command changed", err)
	}
}

func TestInstalledCommandUpgradesOnlyKnownV1AndRoutesRemoteClient(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprint(remote), func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			corePath := "/verified/fased-app"
			root := "/installed/fased"
			data := "/private/fased"
			path := filepath.Join(dir, "fased")
			old := priorLauncherBody(corePath, root, data, remote)
			if bytes.Contains(old, []byte(" connect)")) {
				t.Fatal("v1 fixture contains the new command")
			}
			if err := os.WriteFile(path, old, 0700); err != nil {
				t.Fatal(err)
			}
			if got, err := verifiedInstalledCommand(home, corePath, root, data, remote); err != nil || got != path {
				t.Fatal("known predecessor command rejected before upgrade", got, err)
			}
			got, err := installCommandAt(home, corePath, root, data, remote)
			if err != nil || got != path {
				t.Fatal(got, err)
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, launcherBody(corePath, root, data, remote)) || !bytes.Contains(current, []byte(" connect)")) {
				t.Fatal("known launcher did not upgrade", err)
			}
			if remote && !bytes.Contains(current, []byte("--remote")) {
				t.Fatal("Remote client update not routed")
			}
			// Preserve an owner edit instead of treating it as a managed alias.
			custom := []byte("#!/bin/sh\nexit 77\n")
			if err := os.WriteFile(path, custom, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err = installCommandAt(home, corePath, root, data, remote); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(after, custom) {
				t.Fatal("owner command overwritten")
			}
		})
	}
}

func TestInstalledCommandKnownV1UpgradeRunsConnectedRoutes(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(home, "fased-app")
	if err := os.WriteFile(image, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fased")
	if err := os.WriteFile(path, priorLauncherBody(image, "/fixed/root", "/fixed/data", true), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := installCommandAt(home, image, "/fixed/root", "/fixed/data", true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"setup"}, "vps\naccess\n--configure\n"},
		{[]string{"connect", "--url", "https://test.ts.net"}, "connect\n--url\nhttps://test.ts.net\n"},
		{[]string{"update", "--remote", "check"}, "update\n--remote\ncheck\n"},
		{[]string{"update"}, "vps\nupdates\nwizard\n"},
	} {
		got, err := exec.Command(path, tc.args...).CombinedOutput()
		if err != nil || string(got) != tc.want {
			t.Fatal(tc.args, string(got), err)
		}
	}
}
