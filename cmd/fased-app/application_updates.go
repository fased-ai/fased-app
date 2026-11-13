package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/desktop"
	"github.com/fased-ai/fased-app/internal/enrollment"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var applicationUpdatesPublicClient = newApplicationUpdatesClient

// Preserve the existing ordinary-client behavior for an owner directory under
// a symlinked parent. An enrolled or ambiguous root-owned fence must still fail
// closed and can never fall back to the predecessor's IPC.
func newApplicationUpdatesClient(ctx context.Context, dir string) (*core.Client, bool, error) {
	c, enrolled, err := desktop.NewPublicClient(ctx, dir)
	if err == nil || runtime.GOOS != "linux" {
		return c, enrolled, err
	}
	abs, pathErr := filepath.Abs(dir)
	if pathErr != nil {
		return nil, false, err
	}
	real, pathErr := filepath.EvalSymlinks(abs)
	if pathErr != nil || real == abs {
		return nil, false, err
	}
	if _, fenceErr := enrollment.EnrolledTarget(real, os.Getuid()); !errors.Is(fenceErr, os.ErrNotExist) {
		return nil, false, err
	}
	c, pathErr = core.NewClient(ctx, dir)
	return c, false, pathErr
}

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
	dir := f.String("data-dir", base, "existing instance")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected update arguments")
	}
	ctx := context.Background()
	c, e := openApplicationUpdatesClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	return applicationUpdatesWithClient(ctx, c, action, in, out, errOut)
}

func openApplicationUpdatesClient(ctx context.Context, dir string) (*core.Client, error) {
	c, enrolled, e := applicationUpdatesPublicClient(ctx, dir)
	if e != nil {
		return nil, e
	}
	if enrolled {
		e = c.NativeLocalOwner(ctx)
	} else {
		e = loadUpdateSession(ctx, c, dir)
	}
	if e != nil {
		c.Close()
		return nil, e
	}
	return c, nil
}

func applicationUpdatesWithClient(ctx context.Context, c *core.Client, action string, in *os.File, out, errOut io.Writer) error {
	var e error
	var v core.ApplicationUpdateView
	if e = c.Do(ctx, http.MethodGet, "/api/v1/updates/application", nil, &v); e != nil {
		return e
	}
	if c.Endpoint.InstanceID == "" || v.InstanceID != c.Endpoint.InstanceID {
		return errors.New("update instance changed; preserve the original operation")
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
	var result core.ApplicationUpdateView
	if e = c.Do(ctx, http.MethodPost, "/api/v1/updates/application", command, &result); e != nil {
		return e
	}
	if result.InstanceID != c.Endpoint.InstanceID {
		return errors.New("update instance changed; inspect the original operation before retrying")
	}
	return json.NewEncoder(out).Encode(result)
}
