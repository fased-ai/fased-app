package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"os"
	"strings"
)

func applicationUpdatesCommand(args []string, in *os.File, out, errOut io.Writer) error {
	action := "status"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}
	if action != "status" && action != "check" && action != "apply" && action != "recover" {
		return errors.New("usage: fased-app updates [status|check|apply|recover] [--data-dir <directory>]")
	}
	base, e := core.DefaultDataDir()
	if e != nil {
		return e
	}
	f := flag.NewFlagSet("updates", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("data-dir", base, "existing instance; owner sign-in required")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected update arguments")
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
	var v core.ApplicationUpdateView
	if e = c.Do(ctx, http.MethodGet, "/api/v1/updates/application", nil, &v); e != nil {
		return e
	}
	if action == "status" {
		return json.NewEncoder(out).Encode(v)
	}
	if !v.ControllerAvailable {
		return errors.New("trusted release channel and installation controller are not configured; activation remains disabled")
	}
	command := core.ApplicationUpdateCommand{Action: action, Revision: v.Revision, PolicyRevision: v.PolicyRevision}
	if action == "apply" {
		if v.Operation.Phase != "available" || v.Operation.Release == nil {
			return errors.New("check for an available release first")
		}
		fmt.Fprintf(errOut, "Update to %s? [y/N]: ", v.Operation.Release.Version)
		if in == nil {
			return errors.New("owner confirmation required")
		}
		answer, e := bufio.NewReader(in).ReadString('\n')
		if e != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return errors.New("update cancelled")
		}
		command.ManifestSHA256 = v.Operation.Release.ManifestSHA256
	}
	if e = c.Do(ctx, http.MethodPost, "/api/v1/updates/application", command, &v); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(v)
}
