//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func TestInstalledLocalSetupOpensSameInstanceWithoutChangingData(t *testing.T) {
	base, e := filepath.Abs("../../.evidence/entry-fixtures")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(base, 0700); e != nil {
		t.Fatal(e)
	}
	home, e := os.MkdirTemp(base, "setup-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(paths.Root, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(paths.Data, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(paths.Root, localinstall.EngineName()), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(paths.Data, "draft")
	if e = os.WriteFile(marker, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	m := localinstall.Manifest{Schema: 1, Phase: "ready", Location: "local", Root: paths.Root, Data: paths.Data, Files: map[string]string{"fased-app": strings.Repeat("a", 64), "fased-app.service": strings.Repeat("b", 64)}}
	if e = core.WritePrivateJSON(filepath.Join(paths.Root, "install.json"), m); e != nil {
		t.Fatal(e)
	}
	var out, diagnostic bytes.Buffer
	handled, e := installedLocalEntry(t.Context(), "setup", strings.NewReader(""), &out, &diagnostic)
	if e != nil || !handled {
		t.Fatal(handled, e, diagnostic.String())
	}
	if want := "open\n--prefix\n" + paths.Root + "\n--data-dir\n" + paths.Data + "\n"; out.String() != want {
		t.Fatal(out.String())
	}
	b, e := os.ReadFile(marker)
	if e != nil || string(b) != "preserve" {
		t.Fatal("draft changed", e)
	}
}
