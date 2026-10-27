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
	"strings"
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
	autoInspect := f.Bool("auto-inspect", false, "inspect fixture runs; requires profile")
	flags := map[string]*bool{}
	for _, action := range []string{"review", "create", "step", "action-review", "control", "auto-review", "auto-start", "auto-control", "auto-reconcile"} {
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
	if *autoInspect {
		count++
		if *profile == "" {
			return errors.New("auto-inspect requires profile")
		}
	} else if *profile != "" {
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
		route := "/api/v1/wen/sessions"
		if *autoInspect {
			route = "/api/v1/wen/session-runs"
		}
		e = client.Do(ctx, http.MethodGet, route+"?profile="+url.QueryEscape(*profile), nil, &result)
	} else {
		raw, err := io.ReadAll(io.LimitReader(in, update.MaxDocument+1))
		if err != nil {
			return err
		}
		var typed any
		switch mode {
		case "auto-review":
			typed = &core.WENFixtureRunPrepare{}
		case "auto-start":
			typed = &core.WENFixtureRunReview{}
		case "auto-control":
			typed = &core.WENFixtureRunControl{}
		case "auto-reconcile":
			typed = &core.WENFixtureRunReconcile{}
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
		route := "/api/v1/wen/sessions/" + mode
		if strings.HasPrefix(mode, "auto-") {
			e = core.DecodeWENFixtureRun(raw, typed)
			route = "/api/v1/wen/session-runs/" + strings.TrimPrefix(mode, "auto-")
		} else {
			e = core.DecodeWENSession(raw, typed)
		}
		if e != nil {
			return e
		}
		e = client.Do(ctx, http.MethodPost, route, json.RawMessage(raw), &result)
	}
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(result)
}
