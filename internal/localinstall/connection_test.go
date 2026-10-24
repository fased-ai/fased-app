package localinstall

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestInstallationRuntimeLocation(t *testing.T) {
	p := Paths{Root: "/fixture/app", Data: "/fixture/state"}
	for _, location := range []string{"local", "remote"} {
		b, e := definition(p, Options{Listen: "127.0.0.1:7331", Location: location})
		text := string(b)
		if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
			units := make([]uint16, (len(b)-2)/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(b[2+2*i:])
			}
			text = string(utf16.Decode(units))
		}
		if e != nil || !strings.Contains(text, "--instance-location") || !strings.Contains(text, location) {
			t.Fatal(text, e)
		}
	}
	if _, e := definition(p, Options{Listen: "127.0.0.1:7331", Location: "public"}); e == nil {
		t.Fatal("unknown runtime profile accepted")
	}
}
