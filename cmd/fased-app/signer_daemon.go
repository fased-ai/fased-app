package main

import (
	"io"

	"github.com/fased-ai/fased-app/internal/custody"
)

func signerDaemonCommand(args []string, out, errOut io.Writer) error {
	dirDefault, err := defaultDir()
	if err != nil {
		dirDefault = ""
	}
	return custody.RunDaemon(args, dirDefault, out, errOut)
}
