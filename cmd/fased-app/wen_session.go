package main

import (
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
	"github.com/fased-ai/fased-app/internal/update"
)

func wenSessionCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("wen-session", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private core directory")
	profile := f.String("profile", "", "inspect profile rehearsals")
	flags := map[string]*bool{}
	for _, action := range []string{"review", "create", "step", "action-review", "control"} {
		flags[action] = f.Bool(action+"-stdin", false, "one explicit fixture session "+action)
	}
	if e = f.Parse(args); e != nil {
		return e
	}
	mode := ""
	count := 0
	for action, selected := range flags {
		if *selected {
			mode = action
			count++
		}
	}
	if *profile != "" {
		count++
	}
	if count != 1 || f.NArg() != 0 {
		return errors.New("select one session profile or stdin action")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer client.Close()
	if e = client.LoadSession(ctx, *dir); e != nil {
		return e
	}
	var result json.RawMessage
	if *profile != "" {
		e = client.Do(ctx, http.MethodGet, "/api/v1/wen/sessions?profile="+url.QueryEscape(*profile), nil, &result)
	} else {
		raw, err := io.ReadAll(io.LimitReader(in, update.MaxDocument+1))
		if err != nil {
			return err
		}
		var typed any
		switch mode {
		case "review":
			typed = &core.WENSessionPrepare{}
		case "create":
			typed = &core.WENSessionReview{}
		case "step":
			typed = &core.WENSessionStep{}
		case "action-review":
			typed = &core.WENSessionActionInput{}
		case "control":
			typed = &core.WENSessionControl{}
		}
		if e = core.DecodeWENSession(raw, typed); e != nil {
			return e
		}
		e = client.Do(ctx, http.MethodPost, "/api/v1/wen/sessions/"+mode, json.RawMessage(raw), &result)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
