//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func atomicRecoveryState(t InstallTransaction, o installOps) (string, bool, error) {
	digest := update.InstallDigest(t.Binding)
	id := t.Binding.Index.Binding.Pause.Request.IntentID
	marker, err := o.read(t.Base.Root + "/update-start-blocked.json")
	direction := ""
	if err == nil {
		var v signer.InstallationGuardMarker
		if strict(marker.Data, &v) != nil || !known(marker, mustJSON(v), 0644) || v.Version != 1 || v.Owner != t.Base.Owner || v.SignerID != t.Base.ID || v.IntentID != id || v.Digest != digest || (v.Direction != "forward" && v.Direction != "rollback" && v.Direction != "completion" && v.Direction != "cleanup-rollback" && v.Direction != "cleanup-complete") {
			return "", false, errors.New("foreign atomic recovery guard; preserving it")
		}
		direction = v.Direction
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	f, err := o.read(t.dir() + "/rollback.json")
	if errors.Is(err, os.ErrNotExist) {
		return direction, false, nil
	}
	if err != nil || !known(f, mustJSON(phaseRecord{1, digest, "rollback"}), 0644) {
		return "", false, errors.New("foreign rollback decision; preserving it")
	}
	return direction, true, nil
}

func needsAtomicCleanup(t InstallTransaction, direction string, o installOps) (bool, error) {
	f, err := o.read(t.Base.Root + "/update-active.json")
	if errors.Is(err, os.ErrNotExist) {
		return direction != "", nil
	}
	if err != nil || !known(f, mustJSON(phaseRecord{1, update.InstallDigest(t.Binding), t.Binding.Index.Binding.Pause.Request.IntentID}), 0644) {
		return false, errors.New("foreign active installation; preserving it")
	}
	return true, nil
}

// Atomic transactions use signer-owned phases and fixed OS workers. Unlike the
// legacy controller, the owner never publishes a root phase or replacement.
func runAtomicTransaction(ctx context.Context, t InstallTransaction, rollback bool, o installOps) (*update.Installation, error) {
	if !t.valid() || o.atomic == nil || o.state == nil || o.hold == nil {
		return nil, errors.New("guarded installation context required")
	}
	if err := o.topology(t.Base); err != nil {
		return nil, err
	}
	if err := allowedInstallation(t, o.read); err != nil {
		return nil, err
	}
	direction, decision, err := atomicRecoveryState(t, o)
	if err != nil {
		return nil, err
	}
	current, err := o.state(ctx)
	if err != nil {
		if !errors.Is(err, signer.ErrNoInstallationHold) || direction != "" || decision || rollback {
			return nil, errors.New("guarded signer state unavailable or rollback has no hold; preserve recovery")
		}
		current, err = o.hold(ctx, "active", t.Binding)
		if err != nil {
			return nil, err
		}
	}
	if current == nil || !update.ValidInstallation(*current) || current.Digest != update.InstallDigest(t.Binding) {
		return nil, errors.New("actual signer installation differs")
	}
	if current.InstalledSequenceCommitted {
		return nil, errors.New("sequence committed; use completion recovery")
	}
	if direction == "completion" || direction == "cleanup-complete" || (direction == "cleanup-rollback" && current.Phase != "rolled-back-held") {
		return nil, errors.New("another atomic phase owns recovery")
	}
	if current.Phase == "rolled-back-held" {
		if !decision {
			return nil, errors.New("rollback result lacks its durable decision")
		}
		needed, err := needsAtomicCleanup(t, direction, o)
		if err != nil {
			return nil, err
		}
		if needed {
			if err := o.atomic(ctx, "cleanup-rollback"); err != nil {
				return nil, err
			}
		}
		if err := o.health(ctx, t.Base); err != nil {
			return nil, err
		}
		if err := o.observe(ctx, t.Base, t.Binding); err != nil {
			return nil, err
		}
		return current, nil
	}
	if current.Phase != "active" && current.Phase != "healthy-held" {
		return nil, errors.New("unsupported guarded installation phase")
	}
	if direction == "rollback" {
		decision = true
	}
	if direction == "forward" && current.Phase != "active" {
		return nil, errors.New("forward recovery differs from signer phase")
	}
	if current.Phase == "healthy-held" && !rollback && !decision {
		if err := o.health(ctx, t.Target); err != nil {
			return nil, err
		}
		if err := o.observe(ctx, t.Target, t.Binding); err != nil {
			return nil, err
		}
		return current, nil
	}
	finishRollback := func() (*update.Installation, error) {
		if err := o.atomic(ctx, "rollback"); err != nil {
			return nil, err
		}
		_, recorded, err := atomicRecoveryState(t, o)
		if err != nil || !recorded {
			return nil, errors.New("durable rollback decision missing")
		}
		if err := o.health(ctx, t.Base); err != nil {
			return nil, err
		}
		if err := o.observe(ctx, t.Base, t.Binding); err != nil {
			return nil, err
		}
		result, err := o.hold(ctx, "rolled-back-held", t.Binding)
		if err != nil || result == nil || !update.ValidInstallation(*result) || result.Digest != update.InstallDigest(t.Binding) || result.Phase != "rolled-back-held" {
			return nil, errors.New("actual signer rollback receipt required")
		}
		if err := o.atomic(ctx, "cleanup-rollback"); err != nil {
			return nil, err
		}
		return result, nil
	}
	if rollback || decision {
		return finishRollback()
	}
	if err := o.atomic(ctx, "forward"); err != nil {
		return nil, err
	}
	if err := o.health(ctx, t.Target); err != nil {
		return finishRollback()
	}
	if err := o.observe(ctx, t.Target, t.Binding); err != nil {
		return finishRollback()
	}
	result, err := o.hold(ctx, "healthy-held", t.Binding)
	if err != nil || result == nil || !update.ValidInstallation(*result) || result.Digest != update.InstallDigest(t.Binding) || result.Phase != "healthy-held" {
		return nil, errors.New("actual signer healthy receipt required")
	}
	return result, nil
}

func runAtomicCompletion(ctx context.Context, t InstallTransaction, o installOps) (*update.Installation, error) {
	if !t.valid() || o.atomic == nil || o.state == nil || o.hold == nil || o.ownerRecord == nil || o.release == nil {
		return nil, errors.New("guarded completion context required")
	}
	if err := o.topology(t.Target); err != nil {
		return nil, err
	}
	if err := allowedInstallation(t, o.read); err != nil {
		return nil, err
	}
	for _, item := range []struct{ path, hash string }{{t.Base.Root + "/fased-app", t.Target.CoreHash}, {t.Base.Root + "/fased-signer", t.Target.SignerHash}} {
		f, err := o.read(item.path)
		if err != nil || f.UID != 0 || !f.Regular || f.Mode != 0755 || sum(f.Data) != item.hash {
			return nil, errors.New("completion requires the exact target pair")
		}
	}
	f, err := o.read(t.Base.Root + "/install.json")
	if err != nil || !known(f, mustJSON(t.Target), 0644) {
		return nil, errors.New("completion requires the target inventory")
	}
	if err := o.ownerRecord(false); err != nil {
		return nil, err
	}
	direction, decision, err := atomicRecoveryState(t, o)
	if err != nil || decision || direction == "forward" || direction == "rollback" {
		return nil, errors.New("rollback or another phase owns recovery")
	}
	current, err := o.state(ctx)
	if err != nil || current == nil || !update.ValidInstallation(*current) || current.Digest != update.InstallDigest(t.Binding) {
		return nil, errors.New("actual signer completion state unavailable")
	}
	if current.Phase == "healthy-held" {
		if direction != "" {
			return nil, errors.New("finish the interrupted atomic phase first")
		}
		if err = o.health(ctx, t.Target); err != nil {
			return nil, err
		}
		if err = o.observe(ctx, t.Target, t.Binding); err != nil {
			return nil, err
		}
		current, err = o.hold(ctx, "committed-held", t.Binding)
		if err != nil {
			return nil, err
		}
	}
	if current == nil || !update.ValidInstallation(*current) || current.Digest != update.InstallDigest(t.Binding) || !current.InstalledSequenceCommitted || (current.Phase != "committed-held" && current.Phase != "completed") {
		return nil, errors.New("committed signer receipt required")
	}
	cleanupDone := false
	if current.Phase == "completed" && direction == "cleanup-complete" {
		// The OS worker independently verifies the core is already released
		// before clearing the marker and restarting the stopped services.
		if err = o.atomic(ctx, "cleanup-complete"); err != nil {
			return nil, err
		}
		direction = ""
		cleanupDone = true
	}
	if direction == "cleanup-rollback" {
		return nil, errors.New("rollback cleanup differs from the committed signer")
	}
	if current.Phase == "committed-held" {
		if direction == "cleanup-complete" || direction == "cleanup-rollback" {
			return nil, errors.New("cleanup cannot precede signer completion")
		}
		if err = o.atomic(ctx, "completion"); err != nil {
			return nil, err
		}
		if err = o.health(ctx, t.Target); err != nil {
			return nil, err
		}
	}
	if err = o.ownerRecord(true); err != nil {
		return nil, err
	}
	if current.Phase != "completed" {
		if err = o.observe(ctx, t.Target, t.Binding); err != nil {
			return nil, err
		}
		current, err = o.hold(ctx, "completed", t.Binding)
		if err != nil {
			return nil, err
		}
	}
	if current == nil || !update.ValidInstallation(*current) || current.Digest != update.InstallDigest(t.Binding) || current.Phase != "completed" {
		return nil, errors.New("actual signer completion receipt required")
	}
	actual, err := o.state(ctx)
	if err != nil || actual == nil || update.SnapshotDigest("completion", *actual) != update.SnapshotDigest("completion", *current) {
		return nil, errors.New("signer completion changed")
	}
	if err = o.release(ctx, *actual); err != nil {
		return nil, err
	}
	if !cleanupDone {
		needed, checkErr := needsAtomicCleanup(t, direction, o)
		if checkErr != nil {
			return nil, checkErr
		}
		if needed {
			if err = o.atomic(ctx, "cleanup-complete"); err != nil {
				return nil, err
			}
		}
	}
	return actual, nil
}
