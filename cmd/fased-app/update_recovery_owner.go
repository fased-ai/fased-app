package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/localinstall"
)

func recoverInstalledLocalUpdate(root, dir string, in *os.File, out, errOut io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if root == "" {
		image, e := os.Executable()
		if e != nil {
			return e
		}
		root = filepath.Dir(image)
	}
	paths := localinstall.Paths{Root: root, Data: dir}
	m, e := localinstall.Load(paths)
	if e != nil {
		return e
	}
	if m.Root != root || m.Data != dir {
		return errors.New("recovery belongs to another installation")
	}
	id := ""
	client, clientErr := core.NewClient(ctx, dir)
	if clientErr == nil {
		defer client.Close()
		if e = client.LoadSession(ctx, dir); e != nil {
			return e
		}
		var view core.ApplicationUpdateView
		if e = client.Do(ctx, http.MethodGet, "/api/v1/updates/application", nil, &view); e != nil {
			return e
		}
		if view.Operation.Phase != "recovery" || view.Operation.Release == nil || !view.ControllerAvailable || client.Endpoint.InstanceID != view.InstanceID {
			return errors.New("inspect the existing update; no owner recovery is admitted")
		}
		id = view.Operation.ID
	} else {
		if !errors.Is(clientErr, core.ErrCoreUnavailable) {
			return clientErr
		}
		// A stopped core cannot authenticate a new command. Only its already
		// authenticated, exact private controller journal/metadata may resume.
		journal, e := localinstall.ApplicationUpdateState(paths)
		if e != nil {
			return e
		}
		held, e := core.ReadApplicationOwnerOffline(ctx, dir, journal.ID)
		if e != nil {
			return e
		}
		if !recoveryMetadataMatches(held, journal.ID, journal.Sequence, journal.ReleaseSHA256) {
			return errors.New("offline recovery differs from its original owner journal")
		}
		id = journal.ID
	}
	return localinstall.RecoverApplicationMaintenance(ctx, m, id, in, out, errOut)
}
func recoveryMetadataMatches(v core.ApplicationOwnerView, id string, sequence uint64, manifest string) bool {
	op := v.View.Operation
	if v.View.InstanceID == "" || op.ID != id || op.Release == nil || op.Release.Sequence != sequence || op.Release.ManifestSHA256 != manifest {
		return false
	}
	switch op.Phase {
	case "applying", "recovery", "completed", "rolled-back":
		return true
	}
	return false
}
