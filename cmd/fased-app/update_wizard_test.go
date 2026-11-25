package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestRunUpdateWizardHandlesDesktopReleaseFallback(t *testing.T) {
	t.Parallel()

	t.Run("current", func(t *testing.T) {
		var out, errOut bytes.Buffer
		err := runUpdateWizard(func(action string, dst io.Writer) error {
			if action != "check" {
				t.Fatalf("unexpected action %q", action)
			}
			return json.NewEncoder(dst).Encode(core.DesktopReleaseCheckView{
				InstalledVersion: "0.2.4",
				LatestVersion:    "0.2.4",
				UpdateAvailable:  false,
			})
		}, &out, &errOut, func() {}, 5)
		if err != nil {
			t.Fatalf("runUpdateWizard(current) failed: %v", err)
		}
		if !strings.Contains(out.String(), "Already current (0.2.4)") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})

	t.Run("available", func(t *testing.T) {
		var out, errOut bytes.Buffer
		err := runUpdateWizard(func(action string, dst io.Writer) error {
			return json.NewEncoder(dst).Encode(core.DesktopReleaseCheckView{
				InstalledVersion: "0.2.4",
				LatestVersion:    "0.2.5",
				ReleaseTag:       "v0.2.5-desktop",
				ReleaseURL:       "https://github.com/fased-ai/fased-app/releases/tag/v0.2.5-desktop",
				UpdateAvailable:  true,
			})
		}, &out, &errOut, func() {}, 5)
		if err != nil {
			t.Fatalf("runUpdateWizard(available) failed: %v", err)
		}
		if !strings.Contains(out.String(), "Update available: 0.2.4 -> 0.2.5") || !strings.Contains(out.String(), "v0.2.5-desktop") {
			t.Fatalf("unexpected stdout: %q", out.String())
		}
	})
}
