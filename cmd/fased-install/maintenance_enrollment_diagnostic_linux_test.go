//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func TestEnrollmentDiagnosticChild(t *testing.T) {
	mode := os.Getenv("FASED_TEST_ENROLLMENT_DIAGNOSTIC")
	if mode == "" {
		return
	}
	switch mode {
	case "conflict", "success", "signaled":
		fmt.Fprintln(os.Stderr, "fased-app: enrollment import stage: invalid or conflicting enrollment image; preserve original state")
	case "oversized":
		fmt.Fprintln(os.Stderr, strings.Repeat("private-data", 1000))
	default:
		fmt.Fprintln(os.Stderr, "private-data")
	}
	if mode == "success" {
		os.Exit(0)
	}
	if mode == "signaled" {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	}
	os.Exit(1)
}

func TestProtectedEnrollmentImageDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, mode, action, want string
	}{
		{"conflict", "conflict", "import", "enrollment import stage: invalid or conflicting enrollment image; preserve original state"},
		{"unknown", "unknown", "import", "protected enrollment image operation failed; preserve original state"},
		{"oversized", "oversized", "import", "protected enrollment image operation failed; preserve original state"},
		{"signaled", "signaled", "import", "protected enrollment image operation failed; preserve original state"},
		{"other action", "conflict", "verify", "protected enrollment image operation failed; preserve original state"},
		{"success", "success", "import", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := exec.Command(os.Args[0], "-test.run=^TestEnrollmentDiagnosticChild$")
			c.Env = append(os.Environ(), "FASED_TEST_ENROLLMENT_DIAGNOSTIC="+tc.mode)
			err := runProtectedEnrollmentImage(c, tc.action)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("unexpected diagnostic: %v", err)
			}
		})
	}
}
