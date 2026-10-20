package main

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
)

func TestUpdateEntryPointRequiresExplicitDevelopmentTrust(t *testing.T) {
	var b bytes.Buffer
	if err := run([]string{"updates", "compatibility"}, nil, &b, io.Discard); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v["walletSigner"] != false || v["financialExecution"] != false || v["updaterEnabled"] != false {
		t.Fatal("enabled capability")
	}
	for _, a := range [][]string{{"updates"}, {"updates", "apply"}, {"updates", "inspect"}, {"updates", "inspect", "--development"}, {"updates", "inspect", "--development", "--manifest", "https://example.com/x"}, {"updates", "inspect", "--development", "--channel", "stable"}} {
		b.Reset()
		if err := run(a, nil, &b, io.Discard); err == nil || b.Len() != 0 {
			t.Fatal("untrusted update produced a plan", a)
		}
	}
}

func TestUpdateStatusRejectsMutationAndExtraInputs(t *testing.T) {
	for _, args := range [][]string{{"updates", "status", "apply"}, {"updates", "status", "--automatic"}, {"updates", "status", "--request-stdin"}, {"update-status", "--intent-id", "untrusted"}, {"custody", "updates", "status", "--disposable-fixture", "--prepare-host", "--request-stdin"}} {
		var b bytes.Buffer
		if e := run(args, nil, &b, io.Discard); e == nil || b.Len() != 0 {
			t.Fatal("status accepted mutation input", args, e)
		}
	}
}
