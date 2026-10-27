package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func updatePreferencesCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("updates preferences", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private instance directory; existing CLI login required")
	request := f.Bool("request-stdin", false, "revision-checked policy/proposal metadata JSON only; no installation, permissions or secrets")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected update preference arguments")
	}
	var input core.UpdatePreferencesRequest
	if *request {
		if in == nil {
			return errors.New("public metadata JSON required on stdin")
		}
		b, err := io.ReadAll(io.LimitReader(in, 16*1024+1))
		if err != nil {
			return err
		}
		if len(b) > 16*1024 || update.StrictJSON(b, &input) != nil {
			return errors.New("invalid public update metadata; permissions, unknown and duplicate fields are refused")
		}
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
	var v core.UpdatePreferencesView
	if *request {
		e = c.Do(ctx, http.MethodPut, "/api/v1/updates/preferences", input, &v)
	} else {
		e = c.Do(ctx, http.MethodGet, "/api/v1/updates/preferences", nil, &v)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(v)
}
