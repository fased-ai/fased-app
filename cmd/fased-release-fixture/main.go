// Maintainer-only capability report. Not included in installation packages.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/releaseops"
	"io"
	"os"
)

func run(args []string, out io.Writer) error {
	if os.Geteuid() == 0 || len(args) != 2 || args[0] != "readiness" || args[1] != "--disposable-fixture" {
		return errors.New("ordinary-user readiness --disposable-fixture only; no production or activation command")
	}
	return json.NewEncoder(out).Encode(releaseops.Status())
}
func main() {
	if e := run(os.Args[1:], os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "fased-release-fixture:", e)
		os.Exit(1)
	}
}
