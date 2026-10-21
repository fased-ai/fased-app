//go:build linux

package main

import (
	"bytes"
	"io"
	"testing"
)

func TestPortableCommandRejectsImplicitAndActivationInputs(t *testing.T) {
	for _, a := range [][]string{{"fixture-portable-export"}, {"fixture-portable-recover"}, {"fixture-portable-recover", "--production"}, {"fixture-portable-export", "--disposable-fixture", "--activate"}, {"fixture-portable-recover", "--disposable-fixture", "--automatic"}, {"fixture-portable-export", "--disposable-fixture", "--config", "unused", "--file", "unused", "--directory", "unused"}, {"fixture-portable-recover", "--disposable-fixture", "--config", "unused", "--file", "unused", "--directory", "unused"}} {
		var out bytes.Buffer
		if e := run(a, nil, &out, io.Discard); e == nil || out.Len() != 0 {
			t.Fatal("accepted untrusted recovery", a, e)
		}
	}
}
