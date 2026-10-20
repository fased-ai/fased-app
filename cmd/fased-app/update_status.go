package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"

	"github.com/fased-ai/fased-app/internal/core"
)

func updateStatusCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("updates status", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private instance directory; existing CLI login required")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("updates status is read-only; unexpected arguments")
	}
	ctx := context.Background()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	var v core.UpdateStatus
	if e = c.Do(ctx, http.MethodGet, "/api/v1/updates/status", nil, &v); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(v)
}
