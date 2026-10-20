package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
)

func accountsCommand(args []string, in *os.File, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("accounts", flag.ContinueOnError)
	flags.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := flags.String("data-dir", base, "private instance directory")
	update := flags.Bool("request-stdin", false, "revision-checked public account record JSON from stdin; no secrets")
	if e = flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected account arguments")
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
	var result core.AccountsView
	if *update {
		b, readErr := io.ReadAll(io.LimitReader(in, 16385))
		if readErr != nil {
			return readErr
		}
		if len(b) > 16384 {
			return errors.New("oversized public account record")
		}
		var input core.AccountsRecord
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("invalid public account record; credentials and approval flags are forbidden")
		}
		e = c.Do(ctx, http.MethodPut, "/api/v1/accounts", input, &result)
	} else {
		e = c.Do(ctx, http.MethodGet, "/api/v1/accounts", nil, &result)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
