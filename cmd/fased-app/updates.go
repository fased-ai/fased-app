package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"runtime"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func updatesCommand(args []string, out, errOut io.Writer) error {
	if len(args) == 1 && args[0] == "compatibility" {
		return json.NewEncoder(out).Encode(struct {
			Version            int             `json:"version"`
			Target             update.Target   `json:"target"`
			Contract           update.Contract `json:"contract"`
			WalletSigner       bool            `json:"walletSigner"`
			FinancialExecution bool            `json:"financialExecution"`
			UpdaterEnabled     bool            `json:"updaterEnabled"`
		}{Version: 1, Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}, Contract: core.UpdateContract()})
	}
	if len(args) == 0 || args[0] != "inspect" {
		return errors.New("usage: fased-app updates compatibility|inspect --development --manifest <file> --trust-file <file> --inventory <file> --artifact-dir <directory>")
	}
	f := flag.NewFlagSet("updates inspect", flag.ContinueOnError)
	f.SetOutput(errOut)
	dev := f.Bool("development", false, "explicit disposable development trust; never installation authority")
	m := f.String("manifest", "", "signed development envelope")
	t := f.String("trust-file", "", "explicit development public trust (no built-in or public fallback)")
	i := f.String("inventory", "", "disposable state inventory, not an installed-state probe")
	dir := f.String("artifact-dir", "", "complete local artifact directory; no download or execution")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if !*dev || f.NArg() != 0 || *m == "" || *t == "" || *i == "" || *dir == "" {
		return errors.New("explicit development mode and all input paths required; automatic updates unavailable")
	}
	mb, err := update.ReadDocument(*m)
	if err != nil {
		return err
	}
	tb, err := update.ReadDocument(*t)
	if err != nil {
		return err
	}
	ib, err := update.ReadDocument(*i)
	if err != nil {
		return err
	}
	p, err := update.Inspect(mb, tb, ib, *dir, time.Now().Unix())
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(p)
}
