//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
