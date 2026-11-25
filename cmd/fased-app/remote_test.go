package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/vpsnetwork"
)

func shortFixtureDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return dir
}

func TestManagedRemoteNetworkConsentAndHandoff(t *testing.T) {
	for _, input := range []string{"", "n\n", "yes\nn\n", "yes\n"} {
		if _, err := networkConsent(strings.NewReader(input), io.Discard); err == nil {
			t.Fatal("missing/cancelled network approval accepted")
		}
	}
	approval, err := networkConsent(strings.NewReader("yes\ny\n"), io.Discard)
	if err != nil || !approval.PrivateUI || !approval.ConsoleRecovery {
		t.Fatal(approval, err)
	}
	for _, args := range [][]string{
		nil,
		{"confirm"},
		{"confirm", "one", "two"},
		{"network", "extra"},
		{"harden", "--yes"},
		{"status"},
		{"start"},
		{"access"},
		{"claim-code"},
	} {
		if vpsCommand(args, nil, io.Discard, io.Discard) == nil {
			t.Fatal("invalid network command accepted", args)
		}
	}
	for _, action := range []string{"harden", "network", "rollback", "finish"} {
		if err := vpsCommand([]string{action, "--data-dir", "/nonexistent/app-state"}, nil, io.Discard, io.Discard); err == nil || strings.Contains(err.Error(), "only supported for vps network commands") || strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("network action %s failed flag validation: %v", action, err)
		}
	}
	if err := vpsCommand([]string{"confirm", "--data-dir", "/nonexistent/app-state", "attempt"}, nil, io.Discard, io.Discard); err == nil || strings.Contains(err.Error(), "only supported for vps network commands") || strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("confirm action failed flag validation: %v", err)
	}
}

func TestRuntimeUIDValidation(t *testing.T) {
	for _, ids := range [][2]int{{0, 0}, {1000, 0}, {1000, 1001}, {-1, -1}} {
		if validateRuntimeUID(ids[0], ids[1]) == nil {
			t.Fatal("privileged or switched identity accepted", ids)
		}
	}
	if validateRuntimeUID(1000, 1000) != nil {
		t.Fatal("ordinary owner rejected")
	}
}

func TestPackageNetworkHandoffAndDispatch(t *testing.T) {
	var out bytes.Buffer
	packageNetworkHandoff(
		&out,
		vpsnetwork.Manifest{Phase: "package-remote", Core: "/pkg/fased-app", Data: "/pkg/app-state"},
		vpsnetwork.Status{Phase: "pending", AttemptID: "attempt", Deadline: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)},
	)
	if !strings.Contains(out.String(), "'vps' 'confirm' '--data-dir' '/pkg/app-state' 'attempt'") {
		t.Fatal("missing Format-4 vpsnetwork confirmation instruction", out.String())
	}
}
