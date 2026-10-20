// This deliberately incomplete candidate is built only by disposable acceptance.
// It can report its actual state-reader contract but cannot become healthy.
package main

import (
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"os"
)

func main() {
	if os.Geteuid() == 0 {
		os.Exit(90)
	}
	if len(os.Args) == 3 && os.Args[1] == "updates" && os.Args[2] == "compatibility" {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"version": 1, "contract": core.UpdateContract(), "walletSigner": false, "financialExecution": false, "updaterEnabled": false})
		return
	}
	os.Exit(42)
}
