//go:build linux

package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

// Reconcile only the original update after the protected signer transaction
// has completed. It never checks for or starts another release.
func reconcileMaintenanceApplication(ctx context.Context, client *core.Client, r update.MaintenanceRecord) error {
	if r.Package == nil || !update.IDOK(r.OperationID) {
		return errors.New("original update identity required")
	}
	engine := ""
	for _, component := range r.Package.Components {
		if component.Contract.Component == "core" {
			if engine != "" {
				return errors.New("one original core target required")
			}
			engine = component.Artifact.SHA256
		}
	}
	if engine == "" {
		return errors.New("original core target required")
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	requested := false
	for {
		var view core.ApplicationUpdateView
		if err := client.Do(ctx, http.MethodGet, "/api/v1/updates/application", nil, &view); err != nil {
			return err
		}
		op := view.Operation
		if view.InstanceID != client.Endpoint.InstanceID || !view.ControllerAvailable || op.ID != r.OperationID || op.Release == nil || op.Release.Sequence != r.Package.Sequence || op.Release.ManifestSHA256 != r.Package.ReleaseSHA256 || op.Release.EngineSHA256 != engine {
			return errors.New("application update differs from the protected transaction")
		}
		switch op.Phase {
		case "completed":
			return nil
		case "recovery":
			if requested {
				return errors.New("application recovery did not complete; preserve the operation")
			}
			command := core.ApplicationUpdateCommand{Action: "recover", Revision: view.Revision, PolicyRevision: view.PolicyRevision}
			if err := client.Do(ctx, http.MethodPost, "/api/v1/updates/application", command, nil); err != nil {
				return err
			}
			requested = true
		case "applying":
		default:
			return errors.New("application update cannot reconcile this operation")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
