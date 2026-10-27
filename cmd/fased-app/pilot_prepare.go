package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

// Maintainer preparation only. There is deliberately no activate/run switch.
func pilotPrepareCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("pilot prepare", flag.ContinueOnError)
	f.SetOutput(errOut)
	instance := f.String("instance-dir", "", "new private isolated instance directory")
	review := f.String("review-dir", "", "new private directory for an unapplied activation patch")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || *instance == "" || *review == "" {
		return errors.New("two new instance/review directories required; no secrets or activation options")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, e := core.PrepareOpenAIPilotActivation(ctx, *instance, *review)
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(v)
}
