//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/localinstall"
)

func TestProviderFixtureOptionsOwnModeAndExactPackage(t *testing.T) {
	args := []string{"--disposable-fixture", "--engine", "/tmp/verified/fased-app", "--engine-sha256", strings.Repeat("a", 64)}
	o, e := providerOptions(args)
	if e != nil || !o.ProviderFixture || !o.Start || !o.Autostart || o.PublicOrigin != "" || o.Listen != "127.0.0.1:17485" {
		t.Fatal(o, e)
	}
	for _, bad := range [][]string{nil, args[1:], append(append([]string{}, args...), "--provider-fixture=false"), append(append([]string{}, args...), "--engine-sha256", strings.Repeat("A", 64)), append(append([]string{}, args...), "--engine", "relative"), append(append([]string{}, args...), "--unknown", "0")} {
		if _, e := providerOptions(bad); e == nil {
			t.Fatal("accepted unbound mode", bad)
		}
	}
	var out bytes.Buffer
	if e := run(append([]string{"installed-provider"}, args...), &out); e == nil || out.Len() != 0 {
		t.Fatal("owner host admitted to disposable installer", e)
	}
}

func TestRealLocalProviderPreparesOnlyFreshAppAndState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	contents := []byte("fixture executable bytes")
	engine := filepath.Join(home, "verified-app")
	if err := os.WriteFile(engine, contents, 0700); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(contents))
	args := []string{"installed-provider", "--real-local", "--disposable-fixture", "--engine", engine, "--engine-sha256", want}
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatal(err)
	}
	var m localinstall.Manifest
	if err := json.NewDecoder(&out).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if !m.ProviderFixture || m.Autostart || m.Phase != "ready" || m.Listen != "127.0.0.1:17485" || len(m.Files) != 2 || m.Files["fased-app"] != want {
		t.Fatal(m)
	}
	if parent := filepath.Dir(m.Root); filepath.Dir(parent) != home || filepath.Dir(m.Data) != parent {
		t.Fatal("fixture escaped its private directory", m)
	}
	unit, err := os.ReadFile(filepath.Join(m.Root, "fased-app.service"))
	if err != nil || !bytes.Contains(unit, []byte("--require-sandbox")) || !bytes.Contains(unit, []byte("--ai-fixture")) || bytes.Contains(unit, []byte("--custody-config")) {
		t.Fatal("incorrect generated app-only unit", err)
	}
}

func TestProviderNamespaceRequiresActualReadonlyOwnedProcess(t *testing.T) {
	uid := strconv.Itoa(os.Getuid())
	status := "Uid:\t" + strings.Repeat(uid+"\t", 4) + "\nNoNewPrivs:\t1\n"
	mounts := "1 0 0:1 / / ro - tmpfs tmpfs ro\n2 1 0:2 / /home/owner/data rw - tmpfs tmpfs rw\n"
	if !providerNamespace(status, mounts, "/home/owner/app", "/home/owner/data") {
		t.Fatal("valid namespace refused")
	}
	for _, s := range []string{strings.ReplaceAll(status, "NoNewPrivs:\t1", "NoNewPrivs:\t0"), strings.ReplaceAll(status, uid+"\t", "0\t")} {
		if providerNamespace(s, mounts, "/home/owner/app", "/home/owner/data") {
			t.Fatal("unrestricted process accepted")
		}
	}
	if providerNamespace(status, strings.ReplaceAll(mounts, " / ro ", " / rw "), "/home/owner/app", "/home/owner/data") {
		t.Fatal("writable app root accepted")
	}
}
