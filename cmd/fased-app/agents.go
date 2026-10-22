package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

func agentsCommand(args []string, in *os.File, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("agents", flag.ContinueOnError)
	flags.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := flags.String("data-dir", base, "private instance directory")
	profile := flags.String("profile", "", "profile ID for scoped reading")
	search := flags.String("search", "", "search this profile's conversations, messages and completed AI questions/answers")
	change := flags.Bool("request-stdin", false, "read one revision-checked local agent command JSON (including task-run-explain, memory-chat-save and ai-reminder-review/create); no credentials")
	if e = flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 || (*change && (*profile != "" || *search != "")) {
		return errors.New("use scoped reading or one command from stdin")
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
	var v core.AgentView
	if *change {
		b, e := io.ReadAll(io.LimitReader(in, 16385))
		if e != nil {
			return e
		}
		if len(b) > 16384 {
			return errors.New("oversized agent command")
		}
		var command core.AgentCommand
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&command) != nil || d.Decode(new(any)) != io.EOF {
			return errors.New("one strict agent command required; credentials are forbidden")
		}
		e = c.Do(ctx, http.MethodPost, "/api/v1/agents", command, &v)
		if e != nil {
			return e
		}
	} else {
		q := url.Values{}
		if *profile != "" {
			q.Set("profile", *profile)
		}
		if *search != "" {
			q.Set("q", *search)
		}
		if e = c.Do(ctx, http.MethodGet, "/api/v1/agents?"+q.Encode(), nil, &v); e != nil {
			return e
		}
	}
	return json.NewEncoder(out).Encode(v)
}
