package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"os"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
)

func vpsUpdatesCommand(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: fased-app vps updates prepare|status|check|apply|recover|wizard")
	}
	switch args[0] {
	case "prepare", "status", "check", "apply", "recover", "wizard":
	default:
		return errors.New("unknown update action")
	}

	m, e := vpsinstall.Load(os.Getuid())
	offline := false
	if e != nil && args[0] == "recover" {
		journal, je := vpsinstall.ApplicationUpdateState(os.Getuid())
		if je != nil {
			return e
		}
		m, e = vpsinstall.LoadApplicationMaintenance(os.Getuid(), journal.ID)
		offline = e == nil
	}
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteUpdateCommandBudget(args[0]))
	defer cancel()
	system := &custodyinstall.System{In: in, Out: errOut}
	if args[0] == "recover" {
		var view core.ApplicationUpdateView
		if !offline {
			var b bytes.Buffer
			e = vpsinstall.ApplicationUpdateClient(ctx, m, "status", system, &b)
			if e == nil {
				if json.Unmarshal(b.Bytes(), &view) != nil || view.Operation.Phase != "recovery" || view.Operation.Release == nil || !view.ControllerAvailable {
					return errors.New("inspect the original update; no owner recovery is admitted")
				}
			} else if errors.Is(e, core.ErrCoreUnavailable) {
				offline = true
			} else {
				return e
			}
		}
		if offline {
			journal, e := vpsinstall.ApplicationUpdateState(m.OwnerUID)
			if e != nil {
				return e
			}
			held, e := vpsinstall.ApplicationOwnerCall(ctx, m, core.ApplicationOwnerCommand{Action: "offline", ID: journal.ID}, system)
			if e != nil {
				return e
			}
			if !recoveryMetadataMatches(held, journal.ID, journal.Sequence, journal.ReleaseSHA256) {
				return errors.New("offline recovery differs from its original owner journal")
			}
			view = held.View
		}
		return vpsinstall.RecoverApplicationMaintenance(ctx, m, view.Operation.ID, system, out)
	}

	if args[0] != "prepare" {
		if args[0] == "wizard" {
			admitted := ""
			return updateWizard(func(action string, dest io.Writer) error {
				if action == "status" && admitted != "" {
					actor, e := vpsinstall.ApplicationMaintenanceStatus(ctx, m, admitted)
					if e != nil {
						return e
					}
					if actor.Phase == "running" {
						return core.ErrCoreUnavailable
					}
				}
				current, e := vpsinstall.Load(m.OwnerUID)
				if e != nil {
					return e
				}
				if current.ID != m.ID || current.OwnerUID != m.OwnerUID || current.OwnerGID != m.OwnerGID || current.AppUID != m.AppUID || current.AppGID != m.AppGID || current.AppUser != m.AppUser || current.Root != m.Root || current.Data != m.Data || current.Unit != m.Unit || current.UnitPath != m.UnitPath || current.Listen != m.Listen || current.PublicOrigin != m.PublicOrigin {
					return errors.New("Remote installation identity changed")
				}
				var result bytes.Buffer
				if e := vpsinstall.ApplicationUpdateClient(ctx, current, action, system, &result); e != nil {
					return e
				}
				if action == "apply" {
					var view core.ApplicationUpdateView
					if e := json.Unmarshal(result.Bytes(), &view); e != nil {
						return e
					}
					admitted = view.Operation.ID
				}
				_, e = dest.Write(result.Bytes())
				return e
			}, out, errOut)
		}

		return vpsinstall.ApplicationUpdateClient(ctx, m, args[0], system, out)
	}
	return prepareRemoteUpdateAuthority(ctx, m, system, out)
}
func prepareRemoteUpdateAuthority(ctx context.Context, m vpsinstall.Manifest, system *custodyinstall.System, out io.Writer) error {
	// The OS ceremony describes the durable grant explicitly; this is unrelated to
	// a provider, wallet or choosing Automatic in application preferences.
	return prepareRemoteUpdateAuthorityPlatform(ctx, m, system, out)
}
func remoteUpdateAuthorityResult(out io.Writer) error {
	return json.NewEncoder(out).Encode(map[string]any{"prepared": true, "automaticUpdates": false, "publicActivation": false})
}

func remoteUpdateCommandBudget(action string) time.Duration {
	if action == "wizard" || action == "recover" {
		return 16 * time.Minute
	}
	return 3 * time.Minute
}
