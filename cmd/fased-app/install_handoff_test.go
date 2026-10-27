package main

import (
	"bytes"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/privateaccess"
)

func TestInstallationHandoffUsesInstalledPathsAndPrivateAccess(t *testing.T) {
	m := localinstall.Manifest{Root: "/home/owner/Fased app", Data: "/home/owner/private state", Location: "remote"}
	for _, mode := range []string{"local", "ssh", "tailscale"} {
		plan := privateaccess.Plan{Mode: mode, Origin: "https://fixture.ts.net", Instructions: []string{"AUTHORIZED-DEVICE-GUIDE"}}
		var out bytes.Buffer
		installationHandoff(&out, m, plan, "http://127.0.0.1:7441")
		text := out.String()
		for _, want := range []string{m.Root, m.Data, "claim-code", "service", "status", "AUTHORIZED-DEVICE-GUIDE", "Sign in"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing %q: %s", mode, want, text)
			}
		}
		if strings.Contains(text, "Open: http://127.0.0.1") || strings.Contains(text, "Owner setup code: fased-app") {
			t.Fatalf("ambiguous or PATH-dependent handoff: %s", text)
		}
	}
}

func TestInstallationHandoffSSHUsesConfiguredPort(t *testing.T) {
	plan, err := privateaccess.System().Plan(t.Context(), "ssh", "127.0.0.1:7441")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	installationHandoff(&out, localinstall.Manifest{Root: "/fixture/app", Data: "/fixture/state"}, plan, plan.Target)
	for _, want := range []string{"ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:7441:127.0.0.1:7441 USER@VPS", "on your computer", "host key", "claim-code"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q: %s", want, out.String())
		}
	}
}

func TestInstallationCommandPreservesLiteralArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell execution; Windows renders PowerShell commands")
	}
	args := []string{"/owner/Fased app", "/owner/it's private", "$(printf altered)", "data; printf altered", "--data-dir"}
	command := installationCommand(append([]string{"printf", "%s\\n"}, args...)...)
	output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput()
	if err != nil || string(output) != strings.Join(args, "\n")+"\n" {
		t.Fatalf("arguments changed: %q, %v", output, err)
	}
}

func TestInstallationHandoffStoppedDoesNotOfferUnreadyUI(t *testing.T) {
	m := localinstall.Manifest{Root: "/fixture/app", Data: "/fixture/state", Location: "remote"}
	for _, mode := range []string{"local", "ssh", "tailscale"} {
		var out bytes.Buffer
		installationHandoff(&out, m, privateaccess.Plan{Mode: mode, Instructions: []string{"UNREADY-UI"}}, "")
		text := out.String()
		if !strings.Contains(text, "start") || strings.Contains(text, "UNREADY-UI") || strings.Contains(text, "claim-code") {
			t.Fatal(text)
		}
		if strings.Contains(text, "access") != (mode == "tailscale") {
			t.Fatalf("missing or unexpected private endpoint setup: %s", text)
		}
	}
}
