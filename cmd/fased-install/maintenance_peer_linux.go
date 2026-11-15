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
	var owner int
	var location, instance string
	var err error
	if len(args) == 2 && args[0] == "--owner" {
		owner, err = strconv.Atoi(args[1])
		if err != nil || owner <= 0 || strconv.Itoa(owner) != args[1] {
			return errors.New("invalid maintenance owner")
		}
	} else {
		owner, location, instance, err = packageMaintenanceArguments(args)
		if err != nil {
			return err
		}
	}
	var a maintenance.Authority
	if location == "" {
		a, err = maintenance.ReadAuthority(owner)
	} else {
		a, err = maintenance.ReadPackageAuthority(owner, location, instance)
	}
	if err != nil {
		return err
	}
	if location != "" {
		if err = maintenance.ValidateOwnerIdentity(a); err != nil {
			return err
		}
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
	if self != maintenance.ImagePath(a.ImageSHA256) && location != "" {
		return errors.New("the instance's protected maintenance peer is required")
	} else if self != maintenance.ImagePath(a.ImageSHA256) {
		handoff, err = maintenance.RunningSuccessor(owner)
		if err != nil {
			return errors.New("protected installed maintenance peer required")
		}
		bindHandoff = true
	} else if location == "" {
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
	if location != "" {
		root, err = update.PackageInstanceRoot(owner, location, instance)
		if err != nil {
			return err
		}
	}
	cfg, err := signer.LoadConfig(root + "/identity.json")
	if err != nil {
		return err
	}
	if cfg.OwnerUID != owner || location != "" && (cfg.SignerID != instance || cfg.StateDir != root+"/signer-state" || cfg.SocketDir != root+"/signer-ipc") {
		return errors.New("maintenance peer belongs to another installation")
	}
	readRecord := func() (update.MaintenanceRecord, error) {
		if location != "" {
			return update.ReadPackageMaintenanceRecord(owner, location, instance)
		}
		return update.ReadMaintenanceRecord(owner)
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
	if q.Action == "application-offline" {
		if err = validatePackageApplicationPeerRequest(location, instance, cfg, q, readRecord); err != nil {
			return err
		}
		view, err := core.ReadApplicationOwnerOffline(ctx, root+"/app-state", q.OperationID)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(view)
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
	case "application-claim", "application-prepare", "application-finish":
		if err = validatePackageApplicationPeerRequest(location, instance, cfg, q, readRecord); err != nil {
			return err
		}
		record, err := readRecord()
		if err != nil {
			return err
		}
		command := core.ApplicationOwnerCommand{ID: q.OperationID, Lease: record.OwnerLease}
		switch q.Action {
		case "application-claim":
			command.Action = "claim"
		case "application-prepare":
			command.Action = "prepare"
		case "application-finish":
			command.Action = "finish"
			var engine string
			for _, component := range record.Package.Components {
				if component.Contract.Component == "core" {
					engine = component.Artifact.SHA256
				}
			}
			command.Receipt = &core.ApplicationUpdateReceipt{ID: q.OperationID, Sequence: record.Package.Sequence, ManifestSHA256: record.Package.ReleaseSHA256, EngineSHA256: engine, Phase: "completed"}
		}
		var view core.ApplicationOwnerView
		if err = client.Do(ctx, http.MethodPost, "/api/v1/updates/application/owner", command, &view); err != nil {
			return err
		}
		if view.View.Operation.ID != q.OperationID || view.View.Operation.OwnerLease != record.OwnerLease {
			return errors.New("application operation or lease changed")
		}
		return json.NewEncoder(out).Encode(view)
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
		r, err := readRecord()
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
			r, err := readRecord()
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

// The app peer cannot turn the metadata channel into a general owner command.
// It derives the lease and outcome from the exact protected package journal.
func validatePackageApplicationPeerRequest(location, instance string, cfg signer.Config, q maintenance.AppMaintenanceRequest, readRecord func() (update.MaintenanceRecord, error)) error {
	if location == "" || instance != cfg.SignerID || cfg.Network != "" || !update.IDOK(q.OperationID) || q.Pause != nil || q.ProbeID != "" || readRecord == nil {
		return errors.New("the original empty-signer package operation is required")
	}
	r, err := readRecord()
	if err != nil || r.OperationID != q.OperationID || r.Owner != cfg.OwnerUID || !r.RecoveryEligible() || r.OwnerLease != q.OperationID {
		return errors.New("protected application recovery record differs")
	}
	base, err := update.PackageManifestIdentityFromJSON(r.Base)
	if err != nil || base.ID != instance || base.Location != location || base.Owner != cfg.OwnerUID || base.Network != "" || base.App.UID != cfg.AppUID || base.Signer.UID != cfg.SignerUID {
		return errors.New("package application identity differs")
	}
	switch q.Action {
	case "application-claim", "application-prepare":
		if r.Phase != "prepared" || r.Start != "base" || r.Application != nil {
			return errors.New("only the original prepared application may be claimed")
		}
	case "application-offline":
	case "application-finish":
		if r.Phase != "ready" || r.Start != "target" || r.Application == nil {
			return errors.New("the original target checkpoint is required")
		}
	default:
		return errors.New("unsupported application recovery action")
	}
	return nil
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
