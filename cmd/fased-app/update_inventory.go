package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func updateInventoryCommand(args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("update-inventory", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private app state")
	probe := f.String("probe-id", "", "owner inventory challenge")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !update.IDOK(*probe) {
		return errors.New("one inventory challenge required")
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
	var s update.CoreSnapshot
	if e = c.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+*probe, nil, &s); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(s)
}
