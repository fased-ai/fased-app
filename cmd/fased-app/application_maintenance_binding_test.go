package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestApplicationMaintenanceVersionMetadataAndDisabledBinding(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if e := run([]string{"version", "--json"}, os.Stdin, &out, &diagnostic); e != nil {
		t.Fatal(e)
	}
	var value struct {
		Version             string
		StateSchema         int
		MaintenanceProtocol int
	}
	if e := json.Unmarshal(out.Bytes(), &value); e != nil || value.Version != core.Version || value.StateSchema != core.StateSchema || value.MaintenanceProtocol != 1 {
		t.Fatal(out.String(), e)
	}
	if compiledApplicationUpdateCheck != nil {
		t.Fatal("ordinary update source activated")
	}
	out.Reset()
	if e := run([]string{"version"}, os.Stdin, &out, &diagnostic); e != nil || out.String() != core.Version+"\n" {
		t.Fatal("plain version compatibility", e)
	}
	if e := run([]string{"version", "--unexpected"}, os.Stdin, &out, &diagnostic); e == nil {
		t.Fatal("unexpected version argument accepted")
	}
}
