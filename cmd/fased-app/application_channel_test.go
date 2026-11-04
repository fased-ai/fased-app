package main

import (
	"context"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelSourceUsesValidatedIDUnderOwnerControl(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	id := strings.Repeat("a", 32)
	called := ""
	factory := func(directory string) (bootstrap.Options, error) {
		called = directory
		return bootstrap.Options{Directory: directory}, nil
	}
	source := channelSource(factory, parent)
	if _, e := source(context.Background(), core.ApplicationRelease{}, id); e == nil {
		t.Fatal("missing trust accepted")
	}
	if !filepath.IsAbs(called) || !strings.HasPrefix(called, parent+string(filepath.Separator)+"acquisition-"+id+"-") {
		t.Fatal(called)
	}
	called = ""
	if _, e := source(context.Background(), core.ApplicationRelease{}, "/foreign/path"); e == nil || called != "" {
		t.Fatal("untrusted path reached acquisition", e)
	}
	if _, e := channelOptions(nil, parent); e == nil {
		t.Fatal("ordinary channel activated")
	}
}

func TestChannelReleaseCurrentNeverOffersDowngradeOrReplacement(t *testing.T) {
	d := bootstrap.Descriptor{Sequence: 1000008}
	identity := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name            string
		installed       uint64
		identity        string
		current, refuse bool
	}{
		{"installed candidate ahead of public", 1000009, identity, true, false},
		{"same release", 1000008, identity, true, false},
		{"same sequence different content", 1000008, strings.Repeat("b", 64), false, true},
		{"genuinely newer public release", 1000007, identity, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current, err := channelReleaseCurrent(d, identity, tc.installed, tc.identity)
			if current != tc.current || (err != nil) != tc.refuse {
				t.Fatalf("current=%v err=%v", current, err)
			}
		})
	}
}
