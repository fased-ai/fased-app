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
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"golang.org/x/term"
)

func aiConnectionCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("ai-connection", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private instance directory")
	stdin := f.Bool("request-stdin", false, "one strict credential command from a private pipe; never arguments")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("use read-only status or --request-stdin; never pass secrets as arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	var v core.AIConnectionView
	if *stdin {
		if term.IsTerminal(int(in.Fd())) {
			return errors.New("credential JSON requires a private pipe, not an echoing terminal")
		}
		b, e := io.ReadAll(io.LimitReader(in, 4097))
		if e != nil {
			return e
		}
		defer clear(b)
		if len(b) > 4096 {
			return errors.New("oversized credential command")
		}
		var command core.AIConnectionCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&command) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("one strict credential command required")
		}
		e = c.Do(ctx, http.MethodPost, "/api/v1/ai/connection", command, &v)
	} else {
		e = c.Do(ctx, http.MethodGet, "/api/v1/ai/connection", nil, &v)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(v)
}
