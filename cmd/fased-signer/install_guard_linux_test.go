//go:build linux

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestReplacementGuardOptionsCannotAffectOtherCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"serve", "--config", "/not-read", "--disposable-fixture", "--guard-digest", "foreign"}, os.Stdin, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "replacement guard options") {
		t.Fatal("guard arguments reached another signer operation", err)
	}
}

func TestPredecessorGuardCapabilityIsExact(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"fixture-install-guard", "--predecessor-capability"}, os.Stdin, &out, &errOut); err != nil || out.Len() != 0 {
		t.Fatal("predecessor guard capability unavailable", err)
	}
	if err := run([]string{"fixture-install-guard", "--predecessor-capability", "--config", "/foreign"}, os.Stdin, &out, &errOut); err == nil {
		t.Fatal("capability probe accepted operational arguments")
	}
}

func TestProvisionRecheckCapabilityIsExact(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"fixture-validate-atomic-provision", "--verify-only-capability"}, os.Stdin, &out, &errOut); err != nil || out.Len() != 0 {
		t.Fatal("provision recheck capability unavailable", err)
	}
	if err := run([]string{"fixture-validate-atomic-provision", "--verify-only-capability", "--owner", "1"}, os.Stdin, &out, &errOut); err == nil {
		t.Fatal("capability probe accepted operational arguments")
	}
}

func TestStageRecoveryGuardRejectsUnscopedArguments(t *testing.T) {
	var capabilityOut, capabilityErr bytes.Buffer
	if err := run([]string{"fixture-validate-stage-recovery", "--worker-capability"}, os.Stdin, &capabilityOut, &capabilityErr); err != nil || capabilityOut.Len() != 0 {
		t.Fatal("stage worker capability unavailable", err)
	}
	for _, args := range [][]string{
		{"fixture-validate-stage-recovery"},
		{"fixture-validate-stage-recovery", "--worker-capability", "--owner", "1000"},
		{"fixture-validate-stage-recovery", "--owner", "1000", "--kind", "meta", "--slot", "transaction", "--worker", "--copy", "--disposable-fixture"},
		{"fixture-validate-stage-recovery", "--owner", "1000", "--kind", "meta", "--slot", "transaction"},
		{"fixture-validate-stage-recovery", "--owner", "1000", "--kind", "meta", "--slot", "../../foreign", "--disposable-fixture"},
		{"fixture-validate-stage-recovery", "--owner", "1000", "--kind", "foreign", "--slot", "transaction", "--disposable-fixture"},
		{"fixture-validate-stage-recovery", "--owner", "1000", "--kind", "meta", "--slot", "transaction", "--disposable-fixture", "extra"},
	} {
		var out, errOut bytes.Buffer
		if err := run(args, os.Stdin, &out, &errOut); err == nil || out.Len() != 0 {
			t.Fatal("unscoped stage recovery accepted or wrote output", args, err)
		}
	}
}
