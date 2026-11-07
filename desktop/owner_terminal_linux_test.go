//go:build linux

package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestOwnerEnrollmentTerminalWaitsForChild(t *testing.T) {
	if os.Getenv("FASED_NATIVE_OWNER_RUNTIME") != "1" {
		t.Skip("actual graphical approval terminal not selected")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := launchOwnerEnrollment(ctx, "/usr/bin/sleep", []string{"0.25"}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Fatal("approval terminal detached before its child completed")
	}
}
