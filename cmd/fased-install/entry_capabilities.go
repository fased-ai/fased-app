package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fased-ai/fased-app/internal/bootstrap"
)

var errEntryUpgradeRequired = errors.New("verified newer update entry required")

func installedUpdateEntrySupported(ctx context.Context, corePath string) bool {
	return bootstrap.InstalledUpdateEntrySupported(ctx, corePath)
}

// The initial wizard owns completion of older installations. An explicit
// decision keeps opening a workspace separate from approving its upgrade.
func entryUpgradeDecision(action string, supported bool, in io.Reader, out io.Writer) error {
	if supported || action != "install" && action != "update" {
		return nil
	}
	if action == "update" {
		return errEntryUpgradeRequired
	}
	fmt.Fprint(out, "Update the existing installation to finish setup? [y/N]: ")
	var answer string
	if in == nil {
		return errors.New("setup needs an interactive update decision")
	}
	if _, err := fmt.Fscanln(in, &answer); err != nil || !strings.EqualFold(answer, "y") {
		return errors.New("setup paused; the existing installation is preserved")
	}
	return errEntryUpgradeRequired
}
