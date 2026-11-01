package bootstrap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntryProfileAndListenerRefusedBeforeAcquisition(t *testing.T) {
	var p *Prepared
	for _, options := range []InstallOptions{{Profile: "root"}, {Listen: "0.0.0.0:7331"}, {Listen: "127.0.0.1:80"}, {Listen: "127.0.0.1:07331"}} {
		e := p.Run(context.Background(), options, nil, io.Discard, io.Discard)
		if e == nil || strings.Contains(e.Error(), "verified bootstrap") {
			t.Fatal(options, e)
		}
	}
}

func TestEntryProfileReachesOnlyVerifiedWizard(t *testing.T) {
	f := newFirstFixture(t)
	p, e := Prepare(t.Context(), f.options())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	if e = p.Run(t.Context(), InstallOptions{Start: false, Profile: "remote", Listen: "127.0.0.1:7333"}, strings.NewReader(""), io.Discard, io.Discard); e != nil {
		t.Fatal(e)
	}
	args, e := os.ReadFile(filepath.Join(f.parent, "arguments"))
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"--profile\nremote\n", "--listen\n127.0.0.1:7333\n", "--interactive\n"} {
		if !strings.Contains(string(args), value) {
			t.Fatal(value, string(args))
		}
	}
}
