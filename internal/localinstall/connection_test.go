package localinstall

import (
	"strings"
	"testing"
)

func TestInstallationRuntimeLocation(t *testing.T) {
	p := Paths{Root: "/fixture/app", Data: "/fixture/state"}
	for _, location := range []string{"local", "remote"} {
		b, e := definition(p, Options{Listen: "127.0.0.1:7331", Location: location})
		if e != nil || !strings.Contains(string(b), "--instance-location") || !strings.Contains(string(b), location) {
			t.Fatal(string(b), e)
		}
	}
	if _, e := definition(p, Options{Listen: "127.0.0.1:7331", Location: "public"}); e == nil {
		t.Fatal("unknown runtime profile accepted")
	}
}
