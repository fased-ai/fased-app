package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"

	"github.com/fased-ai/fased-app/internal/tailnet"
)

// This fixed check is invoked as the unprivileged service user. It carries no
// authority to create a listener or change Tailscale configuration.
func privateIngressCommand(args []string, out io.Writer) error {
	if runtime.GOOS != "linux" || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return errors.New("unprivileged Linux private ingress required")
	}
	if len(args) == 1 && args[0] == "private-ingress-capability" {
		_, err := fmt.Fprintln(out, "fased-protected-ingress-v1")
		return err
	}
	if len(args) != 5 || args[0] != "private-ingress-check" || args[1] != "--owner" || args[3] != "--kind" {
		return errors.New("fixed private ingress identity required")
	}
	owner, err := strconv.Atoi(args[2])
	if err != nil || owner <= 0 || strconv.Itoa(owner) != args[2] || !tailnet.ProtectedSocketReady(args[4], owner) {
		return errors.New("protected private listener is unavailable")
	}
	return nil
}
