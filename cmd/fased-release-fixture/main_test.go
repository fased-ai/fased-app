package main

import (
	"bytes"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"testing"
)

func TestReleaseReadinessCannotActivate(t *testing.T) {
	var out bytes.Buffer
	if e := run([]string{"readiness", "--disposable-fixture"}, &out); e != nil {
		t.Fatal(e)
	}
	var r releaseops.Readiness
	if e := json.Unmarshal(out.Bytes(), &r); e != nil || r.Result != "BLOCKED" || len(r.Blockers) != 8 || !r.FixtureOnly || r.ProductionAdmission || r.PublicActivation || r.RealWallets || r.AutomaticUpdates {
		t.Fatal("unsafe readiness", r, e)
	}
	for _, a := range [][]string{nil, {"readiness"}, {"activate", "--disposable-fixture"}, {"readiness", "--production"}, {"readiness", "--disposable-fixture", "--approve"}, {"publish", "--disposable-fixture"}, {"readiness", "--disposable-fixture", "--automatic"}} {
		out.Reset()
		if e := run(a, &out); e == nil || out.Len() != 0 {
			t.Fatal("accepted authority", a, e)
		}
	}
}
