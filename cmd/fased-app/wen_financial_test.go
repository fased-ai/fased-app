package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWENFinancialCLIArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"submit"}, {"read"}, {"context", "--input-file", "x"}, {"context", "extra"}} {
		var out bytes.Buffer
		if wenFinancialCommand(args, &out, io.Discard) == nil || out.Len() != 0 {
			t.Fatal("accepted", args)
		}
	}
	for _, data := range []string{"not-json", strings.Repeat("x", 4097)} {
		file := filepath.Join(t.TempDir(), "input.json")
		if e := os.WriteFile(file, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		var out bytes.Buffer
		if e := wenFinancialCommand([]string{"read", "--input-file", file}, &out, io.Discard); e == nil || !strings.Contains(e.Error(), "bounded financial input") || out.Len() != 0 {
			t.Fatal("unbounded input", e)
		}
	}
}
