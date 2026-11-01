package main

import (
	"github.com/fased-ai/fased-app/internal/privateaccess"
	"strings"
	"testing"
)

func TestInstallationSSHMetadataIsDisplayOnlyAndBounded(t *testing.T) {
	original := privateaccess.Plan{Mode: "ssh", Instructions: []string{"ssh -N -L 127.0.0.1:7331:127.0.0.1:7331 USER@VPS"}}
	got := installationSSHPlanFor(original, "192.0.2.1 45678 198.51.100.2 2222", "fased-operator")
	if !strings.Contains(got.Instructions[0], "ssh -p 2222 -N") || !strings.Contains(got.Instructions[0], "fased-operator@198.51.100.2") {
		t.Fatal(got)
	}
	if !strings.Contains(original.Instructions[0], "USER@VPS") {
		t.Fatal("mutated shared plan")
	}
	for _, bad := range []string{"", "client 22 $(id) 22", "192.0.2.1 22 198.51.100.2 22;id", "192.0.2.1 22 198.51.100.2 0"} {
		if got := installationSSHPlanFor(original, bad, "fased-operator"); got.Instructions[0] != original.Instructions[0] {
			t.Fatal(bad, got)
		}
	}
	if got := installationSSHPlanFor(original, "192.0.2.1 22 198.51.100.2 22", "x;id"); got.Instructions[0] != original.Instructions[0] {
		t.Fatal(got)
	}
}

func TestRootBootstrapSSHUsesExistingAdminWithoutRootApplication(t *testing.T) {
	t.Setenv("FASED_INSTALL_SSH_USER", "root")
	p := privateaccess.Plan{Mode: "ssh", Instructions: []string{"ssh -N USER@VPS"}}
	p = installationSSHPlan(p, "192.0.2.1 1234 198.51.100.2 22")
	if !strings.Contains(p.Instructions[0], "root@198.51.100.2") {
		t.Fatal(p)
	}
}
