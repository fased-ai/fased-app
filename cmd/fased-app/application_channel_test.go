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
