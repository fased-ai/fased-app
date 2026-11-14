//go:build linux

package custodyinstall

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"strconv"
	"time"
)

func protectedOwnerPeer(ctx context.Context, a maintenance.Authority, m Manifest, q signer.Request) (signer.Response, error) {
	return protectedOwnerPeerImage(ctx, a, m, maintenance.ImagePath(a.ImageSHA256), nil, q)
}
func protectedOwnerPeerImage(ctx context.Context, a maintenance.Authority, m Manifest, image string, lock *os.File, q signer.Request) (signer.Response, error) {
	var r signer.Response
	if image != maintenance.ImagePath(a.ImageSHA256) && lock == nil {
		return r, errors.New("original successor lock required")
	}
	c, err := startMaintenanceChildWithLock(ctx, m, "owner", image, lock, "maintenance-peer", "--owner", strconv.Itoa(a.Owner))
	if err != nil {
		return r, err
	}
	err = c.once(q, &r)
	return r, errors.Join(err, c.close())
}
func protectedAppPeer(ctx context.Context, a maintenance.Authority, m Manifest, q maintenance.AppMaintenanceRequest, out any) error {
	return protectedAppPeerImage(ctx, a, m, maintenance.ImagePath(a.ImageSHA256), nil, q, out)
}
func protectedAppPeerImage(ctx context.Context, a maintenance.Authority, m Manifest, image string, lock *os.File, q maintenance.AppMaintenanceRequest, out any) error {
	if image != maintenance.ImagePath(a.ImageSHA256) && lock == nil {
		return errors.New("original successor lock required")
	}
	c, err := startMaintenanceChildWithLock(ctx, m, "app", image, lock, "maintenance-app", "--owner", strconv.Itoa(a.Owner))
	if err != nil {
		return err
	}
	err = c.once(q, out)
	return errors.Join(err, c.close())
}

func ReadProtectedApplicationOperation(ctx context.Context, owner int, id string) (core.ApplicationOwnerView, error) {
	a, err := maintenance.ValidateProcess(owner)
	if err != nil {
		return core.ApplicationOwnerView{}, err
	}
	m, err := Load(owner)
	if err != nil {
		return core.ApplicationOwnerView{}, err
	}
	var r core.ApplicationOwnerView
	err = protectedAppPeer(ctx, a, m, maintenance.AppMaintenanceRequest{Action: "operation", OperationID: id}, &r)
	return r, err
}

func protectedLivePreflight(ctx context.Context, a maintenance.Authority, r *update.MaintenanceRecord, m Manifest, appImage string, lock *os.File) (update.Preflight, error) {
	probe := update.NewProbe()
	var paused update.CorePauseReceipt
	if err := protectedAppPeerImage(ctx, a, m, appImage, lock, maintenance.AppMaintenanceRequest{Action: "pause", OperationID: r.OperationID, Pause: &update.CorePauseCommand{Action: "status", ProbeID: probe}}, &paused); err != nil {
		return update.Preflight{}, err
	}
	sig, err := protectedOwnerPeerImage(ctx, a, m, appImage, lock, signer.Request{Action: "fixture-update-inventory", ProbeID: probe})
	if err != nil || sig.UpdateInventory == nil {
		return update.Preflight{}, errors.Join(err, errors.New("actual live signer inventory required"))
	}
	if r.Staged == nil {
		return update.Preflight{}, errors.New("original staged intent missing")
	}
	q := update.PreflightRequest{IntentID: r.OperationID, Sequence: r.Staged.Sequence, ManifestSHA256: r.Staged.ManifestSHA256, ProbeID: probe, Pause: paused, ExpectedSigner: *sig.UpdateInventory}
	verified, err := protectedOwnerPeerImage(ctx, a, m, appImage, lock, signer.Request{Action: "fixture-update-preflight", Preflight: &q})
	if err != nil || verified.Preflight == nil {
		return update.Preflight{}, errors.Join(err, errors.New("actual live held preflight required"))
	}
	if !update.RecoveryMatches(*r.Recovery, *verified.Preflight) {
		return update.Preflight{}, errors.New("live state differs from the original backup")
	}
	return *verified.Preflight, nil
}

// Reconcile the same core operation only after fresh live signer and pause
// checks. A root journal or a matching file pair alone is not completion.
func reconcileProtectedSuccessor(ctx context.Context, a maintenance.Authority, r update.MaintenanceRecord, m Manifest, image string, lock *os.File) error {
	if r.Phase != "completed" || r.Installation == nil || r.Installation.Phase != "completed" || !update.ValidInstallation(*r.Installation) {
		return errors.New("completed original signer installation required")
	}
	actual, err := protectedOwnerPeerImage(ctx, a, m, image, lock, signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: r.OperationID}})
	if err != nil || actual.Installation == nil || update.SnapshotDigest("completion", *actual.Installation) != update.SnapshotDigest("completion", *r.Installation) {
		return errors.Join(err, errors.New("fresh original signer completion required"))
	}
	var pause update.CorePauseReceipt
	if err = protectedAppPeerImage(ctx, a, m, image, lock, maintenance.AppMaintenanceRequest{Action: "pause", OperationID: r.OperationID, Pause: &update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}}, &pause); err != nil || !update.SameReleasedPause(r.OperationID, r.Installation.Binding.Index.Binding.Pause, pause) {
		return errors.Join(err, errors.New("original core pause must be released"))
	}
	var result struct {
		Complete bool `json:"complete"`
	}
	if err = protectedAppPeerImage(ctx, a, m, image, lock, maintenance.AppMaintenanceRequest{Action: "reconcile", OperationID: r.OperationID}, &result); err != nil || !result.Complete {
		return errors.Join(err, errors.New("original application update is not reconciled"))
	}
	return nil
}

func completeProtectedInstallation(ctx context.Context, a maintenance.Authority, r *update.MaintenanceRecord, m Manifest, appImage string, lock *os.File) error {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var last error
	for {
		var p update.CorePauseReceipt
		last = protectedAppPeerImage(ctx, a, m, appImage, lock, maintenance.AppMaintenanceRequest{Action: "pause", OperationID: r.OperationID, Pause: &update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()}}, &p)
		if last == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return last
		case <-tick.C:
		}
	}
	if err := verifyProtectedPair(m); err != nil {
		return err
	}
	for _, phase := range []string{"healthy-held", "committed-held", "completed"} {
		current, err := protectedOwnerPeerImage(ctx, a, m, appImage, lock, signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: r.OperationID}})
		if err != nil || current.Installation == nil {
			return errors.Join(err, errors.New("actual installation status required"))
		}
		if current.Installation.Digest != r.Installation.Digest {
			return errors.New("actual installation identity changed")
		}
		actual := current.Installation.Phase
		if actual == "completed" || phase == "healthy-held" && (actual == "healthy-held" || actual == "committed-held") || phase == "committed-held" && actual == "committed-held" {
			r.Installation = current.Installation
			if phase == "committed-held" {
				if err = completeProtectedMetadata(ctx, a, *r, m, appImage, lock); err != nil {
					return err
				}
			}
			continue
		}
		p, err := protectedLivePreflight(ctx, a, r, m, appImage, lock)
		if err != nil {
			return err
		}
		q := update.InstallRequest{Action: phase, IntentID: r.OperationID, Binding: &r.Installation.Binding, Preflight: &update.PreflightRequest{IntentID: r.OperationID, Sequence: p.Sequence, ManifestSHA256: p.ManifestSHA256, ProbeID: p.Signer.ProbeID, Pause: p.Pause, ExpectedSigner: p.Signer}}
		reply, err := protectedOwnerPeerImage(ctx, a, m, appImage, lock, signer.Request{Action: "fixture-install-controller", Install: &q})
		if err != nil || reply.Installation == nil || !update.ValidInstallation(*reply.Installation) || reply.Installation.Digest != r.Installation.Digest || reply.Installation.Phase != phase {
			return errors.Join(err, errors.New("actual signer phase was not confirmed"))
		}
		r.Installation = reply.Installation
		r.Phase = "signer-" + phase
		if err = storeProtectedRecord(*r); err != nil {
			return err
		}
		if phase == "committed-held" {
			if err = completeProtectedMetadata(ctx, a, *r, m, appImage, lock); err != nil {
				return err
			}
		}
	}
	actual, err := protectedOwnerPeerImage(ctx, a, m, appImage, lock, signer.Request{Action: "fixture-install-controller", Install: &update.InstallRequest{Action: "status", IntentID: r.OperationID}})
	if err != nil || actual.Installation == nil || actual.Installation.Phase != "completed" || update.SnapshotDigest("completion", *actual.Installation) != update.SnapshotDigest("completion", *r.Installation) {
		return errors.Join(err, errors.New("fresh actual signer completion required"))
	}
	return completeVerifiedSignerReceipt(r, actual.Installation, storeProtectedRecord, func(receipt *update.Installation) (update.CorePauseReceipt, error) {
		var released update.CorePauseReceipt
		err := protectedAppPeerImage(ctx, a, m, appImage, lock, maintenance.AppMaintenanceRequest{Action: "pause", OperationID: r.OperationID, Pause: &update.CorePauseCommand{Action: "complete", ProbeID: update.NewProbe(), Completed: receipt}}, &released)
		return released, err
	}, func() error {
		return maintenance.RemoveProtected(m.Root+"/update-active.json", sum(update.MaintenanceFence(r.OperationID)), 0644)
	})
}

// completeVerifiedSignerReceipt is the existing completion path after the
// live signer has returned its exact completed receipt. An interrupted write
// cannot release the pause, and replay retains the original operation.
func completeVerifiedSignerReceipt(r *update.MaintenanceRecord, actual *update.Installation, persist func(update.MaintenanceRecord) error, release func(*update.Installation) (update.CorePauseReceipt, error), removeFence func() error) error {
	if r == nil || actual == nil || persist == nil || release == nil || removeFence == nil || !update.IDOK(r.OperationID) || r.Start != "target" || r.Installation == nil || actual.Phase != "completed" || !update.ValidInstallation(*actual) || actual.Binding.Index.Binding.Pause.Request == nil || actual.Binding.Index.Binding.Pause.Request.IntentID != r.OperationID || update.SnapshotDigest("completion", *actual) != update.SnapshotDigest("completion", *r.Installation) {
		return errors.New("fresh actual signer completion required")
	}
	switch r.Phase {
	case "ready", "signer-healthy-held", "signer-committed-held", "signer-completed":
	default:
		return errors.New("original maintenance phase does not permit completion")
	}
	if r.Phase != "signer-completed" {
		candidate := *r
		candidate.Installation = actual
		candidate.Phase = "signer-completed"
		if err := persist(candidate); err != nil {
			return err
		}
		*r = candidate
	}
	released, err := release(actual)
	if err != nil {
		return err
	}
	if !update.SameReleasedPause(r.OperationID, actual.Binding.Index.Binding.Pause, released) {
		return errors.New("original core pause was not released")
	}
	if err = removeFence(); err != nil {
		return err
	}
	candidate := *r
	candidate.Phase = "completed"
	candidate.Start = "target"
	if err = persist(candidate); err != nil {
		return err
	}
	*r = candidate
	return nil
}

func completeProtectedMetadata(ctx context.Context, a maintenance.Authority, r update.MaintenanceRecord, m Manifest, image string, lock *os.File) error {
	if image != maintenance.ImagePath(a.ImageSHA256) && lock == nil {
		return errors.New("original successor lock required")
	}
	base, _, err := protectedRecordManifests(r)
	if err != nil {
		return err
	}
	for _, entry := range []struct {
		name           string
		previous, next []byte
	}{{"intent.json", mustJSON(installerIntent(base)), mustJSON(installerIntent(m))}, {"ready.json", readyRecord(base), readyRecord(m)}} {
		if err = maintenance.ReplaceProtected(m.Root+"/"+entry.name, entry.next, 0644, sum(entry.previous)); err != nil {
			return err
		}
	}
	var reply json.RawMessage
	c, err := startMaintenanceChildWithLock(ctx, m, "owner", image, lock, "maintenance-owner-record", "--owner", strconv.Itoa(m.Owner), "--operation", r.OperationID)
	if err != nil {
		return err
	}
	c.in.Close()
	b, err := c.out.ReadSlice('\n')
	if err == nil {
		err = update.StrictJSON(b, &reply)
	}
	return errors.Join(err, c.close())
}

func CompleteProtectedOwnerRecord(owner int, id string) error {
	if os.Getuid() != owner || os.Geteuid() != owner {
		return errors.New("original OS owner required")
	}
	r, err := update.ReadMaintenanceRecord(owner)
	if err != nil {
		return err
	}
	if r.OperationID != id || r.Installation == nil || !update.ValidInstallation(*r.Installation) || (r.Installation.Phase != "committed-held" && r.Installation.Phase != "completed") {
		return errors.New("actual committed installation required")
	}
	base, target, err := protectedRecordManifests(r)
	if err != nil {
		return err
	}
	if err = verifyProtectedPair(target); err != nil {
		return err
	}
	return ownerCompletionRecord(InstallTransaction{Version: 1, Base: base, Target: target, Binding: r.Installation.Binding}, true)
}
