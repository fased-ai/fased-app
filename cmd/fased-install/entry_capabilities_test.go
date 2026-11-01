//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledUpdateEntryCapabilitiesFailClosed(t *testing.T) {
	for _, test := range []struct {
		body string
		ok   bool
	}{
		{`printf '{"version":1,"update":true}\n'`, true},
		{`printf '{"version":"old"}\n'`, false},
		{`exit 2`, false},
		{`printf '{"version":2,"update":true}\n'`, false},
	} {
		path := filepath.Join(t.TempDir(), "core")
		script := "#!/bin/sh\n" + `[ "$1" = version ] && [ "$2" = --entry-json ] || exit 19` + "\n" + test.body + "\n"
		if e := os.WriteFile(path, []byte(script), 0700); e != nil {
			t.Fatal(e)
		}
		if got := installedUpdateEntrySupported(context.Background(), path); got != test.ok {
			t.Fatal(test, got)
		}
	}
}
