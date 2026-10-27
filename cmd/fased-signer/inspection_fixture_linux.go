//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

func inspectionFixtureCommand(args []string, input *os.File, out, errOut io.Writer) error {
	if len(args) == 1 && args[0] == "fixture-inspection-compatibility" {
		return json.NewEncoder(out).Encode(update.FixtureInspectionContract())
	}
	f := flag.NewFlagSet("fixture inspection", flag.ContinueOnError)
	f.SetOutput(errOut)
	owner := f.Int("owner", 0, "fixed fixture owner")
	fixture := f.Bool("disposable-fixture", false, "disposable only")
	id := f.String("intent-id", "", "exact maintenance intent")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 || !*fixture || *owner <= 0 {
		return errors.New("explicit installed fixture required")
	}
	root := "/var/lib/fased-app-custody-" + strconv.Itoa(*owner)
	c, e := signer.LoadConfig(root + "/identity.json")
	if e != nil {
		return e
	}
	switch args[0] {
	case "fixture-inspection-empty":
		if e := signer.CheckColdIdentity(c); e != nil {
			return e
		}
		empty, e := update.FixtureInspectionNamespaceEmpty(root, "signer")
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]bool{"empty": empty})
	case "fixture-inspection-init":
		return update.InitializeInstalledInspectionFixture(context.Background(), root, "signer")
	case "fixture-inspection-cold":
		v, e := signer.InspectFixtureCold(c, *id)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(v)
	case "fixture-reader-guard":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		server, e := signer.ServeInspectionFixture(ctx, c)
		if e != nil {
			return e
		}
		defer server.Close()
		<-ctx.Done()
		return nil
	}
	return errors.New("unknown fixture command")
}
