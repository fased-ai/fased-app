//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"time"
)

func maintenancePeerCommand(args []string, in io.Reader, out io.Writer, app bool) error {
	if len(args) != 2 || args[0] != "--owner" {
		return errors.New("fixed maintenance peer owner required")
	}
	owner, err := strconv.Atoi(args[1])
	if err != nil || owner <= 0 || strconv.Itoa(owner) != args[1] {
		return errors.New("invalid maintenance owner")
	}
	a, err := maintenance.ReadAuthority(owner)
	if err != nil {
		return err
	}
	if err = maintenance.VerifyImage(a); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return errors.New("protected installed maintenance peer required")
	}
	var successor *maintenance.Successor
	var handoff maintenance.Successor
	var bindHandoff bool
	if self != maintenance.ImagePath(a.ImageSHA256) {
		handoff, err = maintenance.RunningSuccessor(owner)
		if err != nil {
			return errors.New("protected installed maintenance peer required")
		}
		bindHandoff = true
	} else {
		// Rotation must not drop the original-operation restriction while its
		// durable transition fence remains in place.
		handoff, err = maintenance.ReadSuccessor(owner)
		if err != nil && !errors.Is(err, maintenance.ErrNoSuccessor) {
			return err
		}
		if err == nil && handoff.SuccessorImage == a.ImageSHA256 {
			bindHandoff, err = maintenance.SuccessorFenced(handoff)
			if err != nil {
				return err
			}
		}
	}
	if bindHandoff {
		lock, err := maintenance.OpenSuccessorLock(owner)
		if err != nil {
			return err
		}
		defer lock.Close()
		successor = &handoff
	}
	root := fmt.Sprintf("/var/lib/fased-app-custody-%d", owner)
	cfg, err := signer.LoadConfig(root + "/identity.json")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	b, err := readMaintenanceInput(ctx, in)
	if err != nil {
		return err
	}
	if !app {
		if err = signer.CheckOwner(cfg); err != nil {
			return err
		}
		var q signer.Request
		if update.StrictJSON(b, &q) != nil {
			return errors.New("invalid owner maintenance request")
		}
		if successor != nil {
			if err = validateSuccessorOwnerRequest(*successor, q); err != nil {
				return err
			}
		}
		switch q.Action {
		case "fixture-update-inventory", "fixture-update-status", "fixture-update-preflight":
		case "fixture-install-controller":
			if q.Install == nil || (q.Install.Action != "status" && q.Install.Action != "active" && q.Install.Action != "healthy-held" && q.Install.Action != "committed-held" && q.Install.Action != "completed") {
				return errors.New("unsupported owner installation phase")
			}
		default:
			return errors.New("only signer maintenance metadata is allowed")
		}
		r, err := signer.Call(ctx, cfg, true, q)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	}
	if os.Getuid() != cfg.AppUID || os.Geteuid() != cfg.AppUID {
		return errors.New("original app identity required")
	}
	if err = signer.CheckApplication(root+"/identity.json", root, root+"/app-state"); err != nil {
		return err
	}
	var q maintenance.AppMaintenanceRequest
	if update.StrictJSON(b, &q) != nil {
		return errors.New("invalid app maintenance request")
	}
	if successor != nil && (q.OperationID != successor.OperationID || q.Action != "operation" && q.Action != "pause" && q.Action != "reconcile") {
		return errors.New("successor is limited to its held operation")
	}
	client, err := core.NewClient(ctx, root+"/app-state")
	if err != nil {
		return err
	}
	defer client.Close()
	if err = prepareMaintenanceClient(ctx, client, root+"/app-state"); err != nil {
		return err
	}
	switch q.Action {
	case "inventory":
		if !update.IDOK(q.ProbeID) || q.Pause != nil {
			return errors.New("actual inventory challenge required")
		}
		var r update.CoreSnapshot
		if err = client.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+q.ProbeID, nil, &r); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	case "operation":
		if !update.IDOK(q.OperationID) || q.Pause != nil {
			return errors.New("one application operation required")
		}
		var r core.ApplicationOwnerView
		if err = client.Do(ctx, http.MethodPost, "/api/v1/updates/application/owner", core.ApplicationOwnerCommand{Action: "inspect", ID: q.OperationID}, &r); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	case "reconcile":
		if successor == nil || q.Pause != nil || q.ProbeID != "" {
			return errors.New("one completed successor operation required")
		}
		r, err := update.ReadMaintenanceRecord(owner)
		if err != nil || r.OperationID != q.OperationID || r.Phase != "completed" || r.Installation == nil || r.Installation.Phase != "completed" || !update.ValidInstallation(*r.Installation) || r.Package == nil {
			return errors.New("actual completed maintenance record required")
		}
		if _, err = os.Lstat(root + "/update-active.json"); !errors.Is(err, os.ErrNotExist) {
			return errors.New("original maintenance fence must already be released")
		}
		if err = reconcileMaintenanceApplication(ctx, client, r); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(struct {
			Complete bool `json:"complete"`
		}{true})
	case "pause":
		if q.Pause == nil || q.Pause.Action != "status" && q.Pause.Action != "complete" {
			return errors.New("only held status or exact completion is allowed")
		}
		if q.Pause.Action == "complete" {
			r, err := update.ReadMaintenanceRecord(owner)
			if err != nil || r.OperationID != q.OperationID || r.Installation == nil || r.Installation.Phase != "completed" || q.Pause.Completed == nil || update.SnapshotDigest("completion", *r.Installation) != update.SnapshotDigest("completion", *q.Pause.Completed) {
				return errors.New("actual completed signer record required")
			}
		}
		var r update.CorePauseReceipt
		if err = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", q.Pause, &r); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(r)
	default:
		return errors.New("unsupported app maintenance metadata")
	}
}

// A successor can use the existing owner socket only for the protected
// original update, while holding the inherited root transaction lock. The
// signer still independently validates the live hold and every transition.
func validateSuccessorOwnerRequest(h maintenance.Successor, q signer.Request) error {
	if !update.IDOK(h.OperationID) {
		return errors.New("original signer operation required")
	}
	allowed := signer.Request{Action: q.Action}
	switch q.Action {
	case "fixture-update-inventory":
		if !update.IDOK(q.ProbeID) {
			return errors.New("signer inventory challenge required")
		}
		allowed.ProbeID = q.ProbeID
	case "fixture-update-status":
		if q.IntentID != h.OperationID {
			return errors.New("original signer operation required")
		}
		allowed.IntentID = q.IntentID
	case "fixture-update-preflight":
		if q.Preflight == nil || q.Preflight.IntentID != h.OperationID {
			return errors.New("original signer preflight required")
		}
		allowed.Preflight = q.Preflight
	case "fixture-install-controller":
		if q.Install == nil || q.Install.IntentID != h.OperationID {
			return errors.New("original signer installation required")
		}
		switch q.Install.Action {
		case "status":
			if q.Install.Binding != nil || q.Install.Preflight != nil {
				return errors.New("read-only signer status required")
			}
		case "healthy-held", "committed-held", "completed":
			if q.Install.Binding == nil || update.InstallDigest(*q.Install.Binding) != h.InstallationDigest || q.Install.Preflight == nil || q.Install.Preflight.IntentID != h.OperationID {
				return errors.New("original signer hold and preflight required")
			}
		default:
			return errors.New("unsupported signer transition")
		}
		allowed.Install = q.Install
	default:
		return errors.New("unsupported successor owner request")
	}
	if !reflect.DeepEqual(q, allowed) {
		return errors.New("unexpected successor owner request fields")
	}
	return nil
}

func prepareMaintenanceClient(ctx context.Context, client *core.Client, data string) error {
	var err error
	client.Token, err = core.IssueMaintenanceSession(ctx, data, client.Endpoint.InstanceID)
	if err != nil {
		return err
	}
	return client.RefreshCSRF(ctx)
}
