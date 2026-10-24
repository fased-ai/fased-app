package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
)

func setupWizard(in *bufio.Reader, out io.Writer, settings *core.Setup) error {
	fmt.Fprintln(out, "Fased preferences. Installation and the active connection are managed separately.")
	for _, field := range []struct {
		label   string
		value   *string
		choices string
	}{
		{"Saved deployment preference", &settings.Host, "local/vps"},
		{"Wallet preference", &settings.WalletMode, "skip/watch/external/automation"},
		{"AI preference", &settings.Provider, "none/openai/anthropic/xai"},
	} {
		for {
			fmt.Fprintf(out, "%s (%s) [%s]: ", field.label, field.choices, *field.value)
			line, err := in.ReadString('\n')
			if err != nil {
				return errors.New("setup cancelled before saving")
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			valid := false
			for _, choice := range strings.Split(field.choices, "/") {
				if line == choice {
					valid = true
					break
				}
			}
			if valid {
				*field.value = line
				break
			}
			fmt.Fprintln(out, "Choose one of the listed values, or press Enter to keep the current value.")
		}
	}
	fmt.Fprintf(out, "Save %s / %s / %s? (yes/no): ", settings.Host, settings.WalletMode, settings.Provider)
	line, err := in.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "yes" {
		return errors.New("setup cancelled before saving")
	}
	settings.Step, settings.Complete = 3, true
	return settings.Validate()
}
