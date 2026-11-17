//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledLocalCommandRoutesAccounts(t *testing.T) {
	home := t.TempDir()
	corePath := filepath.Join(home, "core")
	if err := os.WriteFile(corePath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(home, "data")
	path, err := installCommandAt(home, corePath, home, data, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(path, "accounts").CombinedOutput()
	if err != nil || string(got) != "accounts\n--data-dir\n"+data+"\n" {
		t.Fatal("installed accounts entry unavailable", err, string(got))
	}
}

func TestLocalEnrollmentRebindsOnlyKnownCommand(t *testing.T) {
	home := t.TempDir()
	root, data := filepath.Join(home, "original"), filepath.Join(home, "data")
	oldCore, newCore := filepath.Join(root, "fased-app"), filepath.Join(home, "protected", "fased-app")
	if err := os.MkdirAll(filepath.Dir(oldCore), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(newCore), 0700); err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{oldCore, newCore} {
		if err := os.WriteFile(image, []byte("#!/bin/sh\nprintf '%s\\n' \"$0\" \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	path, err := installCommandAt(home, oldCore, root, data, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = rebindLocalInstalledCommand(home, oldCore, newCore, root, data); err != nil {
		t.Fatal(err)
	}
	if err = rebindLocalInstalledCommand(home, oldCore, newCore, root, data); err != nil {
		t.Fatal("rebind must be idempotent", err)
	}
	got, err := exec.Command(path, "update", "status").CombinedOutput()
	if err != nil || string(got) != newCore+"\nupdate\nstatus\n--data-dir\n"+data+"\n" {
		t.Fatal("updated command did not use the enrolled executable", string(got), err)
	}
	foreign := []byte("#!/bin/sh\nexit 77\n")
	if err = os.WriteFile(path, foreign, 0700); err != nil {
		t.Fatal(err)
	}
	if err = rebindLocalInstalledCommand(home, oldCore, newCore, root, data); err == nil {
		t.Fatal("foreign command accepted")
	}
	if b, _ := os.ReadFile(path); string(b) != string(foreign) {
		t.Fatal("foreign command overwritten")
	}
}

func TestLocalEnrollmentRebindsPrimaryWhenSecondaryAlreadyCurrent(t *testing.T) {
	home := t.TempDir()
	root, data := filepath.Join(home, "original"), filepath.Join(home, "data")
	oldCore, newCore := filepath.Join(root, "fased-app"), filepath.Join(home, "protected", "fased-app")
	path, err := installCommandAt(home, oldCore, root, data, false)
	if err != nil {
		t.Fatal(err)
	}
	secondary := filepath.Join(home, ".local", "bin", "fased-app")
	if err = os.WriteFile(secondary, launcherBody(newCore, root, data, false), 0700); err != nil {
		t.Fatal(err)
	}
	if err = rebindLocalInstalledCommand(home, oldCore, newCore, root, data); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(launcherBody(newCore, root, data, false)) {
		t.Fatal("primary command still points at the predecessor", err)
	}
}

func TestLocalEnrollmentRebindsBothKnownAliases(t *testing.T) {
	home := t.TempDir()
	root, data := filepath.Join(home, "original"), filepath.Join(home, "data")
	oldCore, newCore := filepath.Join(root, "fased-app"), filepath.Join(home, "protected", "fased-app")
	primary, err := installCommandAt(home, oldCore, root, data, false)
	if err != nil {
		t.Fatal(err)
	}
	secondary := filepath.Join(home, ".local", "bin", "fased-app")
	if err = os.WriteFile(secondary, launcherBody(oldCore, root, data, false), 0700); err != nil {
		t.Fatal(err)
	}
	if err = rebindLocalInstalledCommand(home, oldCore, newCore, root, data); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{primary, secondary} {
		got, err := os.ReadFile(name)
		if err != nil || string(got) != string(launcherBody(newCore, root, data, false)) {
			t.Fatal("command still points at the predecessor", name, err)
		}
	}
}

func TestInstalledCommandPreservesForeignFilesAndRoutesUpdate(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "app 'quoted")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(root, "fased-app")
	if e := os.WriteFile(target, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	for _, remote := range []bool{false, true} {
		host := filepath.Join(home, map[bool]string{false: "local", true: "remote"}[remote])
		if e := os.Mkdir(host, 0700); e != nil {
			t.Fatal(e)
		}
		path, e := installCommandAt(host, target, root, filepath.Join(home, "data"), remote)
		if e != nil {
			t.Fatal(e)
		}
		got, e := exec.Command(path, "update", "status").CombinedOutput()
		if e != nil {
			t.Fatal(e, string(got))
		}
		expected := "update\nstatus\n--data-dir\n"
		if remote {
			expected = "vps\nupdates\nstatus\n"
		}
		if !strings.HasPrefix(string(got), expected) {
			t.Fatal(string(got))
		}
		if _, e = installCommandAt(host, target, root, filepath.Join(home, "data"), remote); e != nil {
			t.Fatal("idempotence", e)
		}
	}
	host := filepath.Join(home, "foreign")
	if e := os.MkdirAll(filepath.Join(host, ".local", "bin"), 0755); e != nil {
		t.Fatal(e)
	}
	foreign := filepath.Join(host, ".local", "bin", "fased")
	if e := os.WriteFile(foreign, []byte("original"), 0700); e != nil {
		t.Fatal(e)
	}
	path, e := installCommandAt(host, target, root, "/data", false)
	if e != nil || filepath.Base(path) != "fased-app" {
		t.Fatal(path, e)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "original" {
		t.Fatal("foreign command changed")
	}
}

func TestVerifiedInstalledCommandPreservesForeignFirstName(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, ".local/bin")
	if e := os.MkdirAll(directory, 0755); e != nil {
		t.Fatal(e)
	}
	foreign := []byte("#!/bin/sh\nexit 19\n")
	if e := os.WriteFile(filepath.Join(directory, "fased"), foreign, 0700); e != nil {
		t.Fatal(e)
	}
	expected, e := installCommandAt(home, "/fixed/core", "/fixed/root", "/fixed/data", true)
	if e != nil {
		t.Fatal(e)
	}
	got, e := verifiedInstalledCommand(home, "/fixed/core", "/fixed/root", "/fixed/data", true)
	if e != nil || got != expected || filepath.Base(got) != "fased-app" {
		t.Fatal(got, expected, e)
	}
	b, e := os.ReadFile(filepath.Join(directory, "fased"))
	if e != nil || string(b) != string(foreign) {
		t.Fatal("foreign launcher changed", e)
	}
}
