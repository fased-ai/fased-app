package main

import (
	"bufio"
	"io"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func TestSetupWizardReviewAndCancel(t *testing.T) {
	s := core.DefaultSetup()
	if err := setupWizard(bufio.NewReader(strings.NewReader("public\nvps\nwatch\n\nyes\n")), io.Discard, &s); err != nil {
		t.Fatal(err)
	}
	if s.Host != "vps" || s.WalletMode != "watch" || s.Provider != "none" || !s.Complete {
		t.Fatal("wizard choices not preserved")
	}
	s = core.DefaultSetup()
	if err := setupWizard(bufio.NewReader(strings.NewReader("vps\n\n\nno\n")), io.Discard, &s); err == nil || s.Complete {
		t.Fatal("cancelled setup was marked complete")
	}
}

func TestInteractiveFlagsCannotOverrideReviewedChoices(t *testing.T) {
	err := run([]string{"setup", "--interactive", "--host", "vps"}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "every saved choice is reviewed") {
		t.Fatal("flags could change setup after the owner's review")
	}
}
