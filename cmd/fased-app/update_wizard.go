package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"strings"
	"time"
)

// updateWizard composes ordinary authenticated endpoints, including asynchronous
// check/restart completion. It never retries apply or enables automatic policy.
// Authenticate once at entry. A later authentication failure holds the original
// operation instead of reauthorizing it or repeating Apply.
func authenticatedUpdateWizard(call func(string, io.Writer) error, signIn func() error, out, errOut io.Writer) error {
	first := true
	return updateWizard(func(action string, dest io.Writer) error {
		if !first {
			return call(action, dest)
		}
		first = false
		var response bytes.Buffer
		err := call(action, &response)
		if errors.Is(err, core.ErrUnauthorized) {
			if err = signIn(); err != nil {
				return err
			}
			response.Reset()
			err = call(action, &response)
		}
		if err != nil {
			return err
		}
		_, err = dest.Write(response.Bytes())
		return err
	}, out, errOut)
}

func updateWizard(call func(string, io.Writer) error, out, errOut io.Writer) error {
	return runUpdateWizard(call, out, errOut, func() { time.Sleep(time.Second) }, 900)
}
func runUpdateWizard(call func(string, io.Writer) error, out, errOut io.Writer, wait func(), limit int) error {
	var view core.ApplicationUpdateView
	deadline := time.Now().Add(15 * time.Minute)
	var approved *core.ApplicationRelease
	admittedID := ""
	read := func(action string) error {
		if time.Now().After(deadline) {
			return errors.New("update is still running; use fased update status")
		}
		var b bytes.Buffer
		if e := call(action, &b); e != nil {
			return e
		}
		if b.Len() > 64*1024 {
			return errors.New("oversized update response")
		}
		if e := json.Unmarshal(b.Bytes(), &view); e != nil {
			return e
		}
		if approved != nil {
			if view.Operation.Release == nil || *view.Operation.Release != *approved {
				return errors.New("approved update release changed")
			}
			if admittedID != "" && view.Operation.ID != admittedID {
				return errors.New("update operation changed")
			}
			if action == "apply" || admittedID == "" && (view.Operation.Phase == "queued" || view.Operation.Phase == "applying") {
				admittedID = view.Operation.ID
			}
		}
		return nil
	}
	if e := read("check"); e != nil {
		return e
	}
	last := ""
	applied := false
	for step := 0; step < limit; step++ {
		phase := view.Operation.Phase
		switch phase {
		case "current":
			fmt.Fprintln(out, "Already current")
			return nil
		case "available":
			if applied {
				return errors.New("update was not queued; review its current state")
			}
			applied = true
			if view.Operation.Release != nil {
				copy := *view.Operation.Release
				approved = &copy
			}
			if e := read("apply"); e != nil {
				if errors.Is(e, core.ErrCoreUnavailable) {
					view.Operation.Phase = "applying"
					continue
				}
				return e
			}
			continue
		case "completed":
			fmt.Fprintln(out, "Updated")
			return nil
		case "rolled-back":
			return errors.New("previous version restored; inspect Updates before retrying")
		case "recovery", "check-failed":
			return fmt.Errorf("Update %s: %s", phase, view.Operation.Failure)
		case "check-queued", "checking", "queued", "applying":
			if phase != last {
				fmt.Fprintln(errOut, "Update:", strings.ReplaceAll(phase, "-", " "))
				last = phase
			}
		default:
			return fmt.Errorf("unexpected update state %q; preserve the installation", phase)
		}
		wait()
		if e := read("status"); e != nil {
			if applied && errors.Is(e, core.ErrCoreUnavailable) {
				continue
			}
			return e
		}
	}
	return errors.New("update is still running; use fased update status. Do not start another operation")
}
func ordinaryUpdateCommand(args []string, in *os.File, out, errOut io.Writer) error {
	action := "wizard"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	switch action {
	case "wizard", "status", "check", "apply", "recover":
	default:
		return errors.New("usage: fased update [status|check|apply|recover]")
	}
	f := flag.NewFlagSet("update", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "existing instance")
	remote := f.Bool("remote", false, "update the connected Tailscale instance")
	prefix := f.String("prefix", "", "installed root for custom recovery; defaults to this installed executable")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected update arguments")
	}
	if *remote {
		ctx := context.Background()
		client, err := connectedRemoteClient(ctx, *dir)
		if err != nil {
			return err
		}
		defer client.Close()
		call := func(action string, dest io.Writer) error {
			return applicationUpdatesWithClient(ctx, client, action, in, dest, errOut)
		}
		if action == "wizard" {
			return updateWizard(call, out, errOut)
		}
		return call(action, out)
	}
	if action == "recover" {
		// Enrolled Local custody uses the same authenticated core recovery
		// operation as Settings. Never fall back to predecessor recovery when
		// the enrollment is unavailable or ambiguous.
		ctx := context.Background()
		client, enrolled, err := applicationUpdatesPublicClient(ctx, *dir)
		if err == nil {
			if enrolled {
				defer client.Close()
				if *prefix != "" {
					return errors.New("enrolled recovery uses its original installation; omit --prefix")
				}
				if err = client.NativeLocalOwner(ctx); err != nil {
					return err
				}
				return applicationUpdatesWithClient(ctx, client, "recover", in, out, errOut)
			}
			client.Close()
		} else if !errors.Is(err, core.ErrCoreUnavailable) || !ordinaryUpdateRecoveryData(*dir) {
			return err
		}
		return recoverInstalledLocalUpdate(*prefix, *dir, in, out, errOut)
	}
	if action == "wizard" {
		ctx := context.Background()
		client, err := openApplicationUpdatesClient(ctx, *dir)
		if err != nil {
			return err
		}
		defer client.Close()
		call := func(action string, dest io.Writer) error {
			return applicationUpdatesWithClient(ctx, client, action, in, dest, errOut)
		}
		return updateWizard(call, out, errOut)
	}
	return applicationUpdatesCommand([]string{action, "--data-dir", *dir}, in, out, errOut)
}
