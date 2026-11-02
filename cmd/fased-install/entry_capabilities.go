package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

var errEntryUpgradeRequired = errors.New("verified newer update entry required")

type entryCapabilityOutput struct{ data []byte }

func (b *entryCapabilityOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > 8192 {
		return 0, errors.New("oversized entry capability response")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func installedUpdateEntrySupported(ctx context.Context, corePath string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, corePath, "version", "--entry-json")
	var out entryCapabilityOutput
	c.Stdout = &out
	c.Stderr = io.Discard
	if c.Run() != nil {
		return false
	}
	var v struct {
		Version int  `json:"version"`
		Update  bool `json:"update"`
	}
	return json.Unmarshal(out.data, &v) == nil && v.Version == 1 && v.Update
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
