package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func updateInventoryCommand(args []string, out, errOut io.Writer) error {
	return updateInventoryCommandWithBootstrap(args, out, errOut, bootstrapLocalInventoryCommand)
}

func updateInventoryCommandWithBootstrap(args []string, out, errOut io.Writer, bootstrap func(context.Context, string, int, string, string, string, io.Writer) error) error {
	f := flag.NewFlagSet("update-inventory", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private app state")
	probe := f.String("probe-id", "", "owner inventory challenge")
	bootstrapOwner := f.Int("bootstrap-owner", 0, "protected Local predecessor owner; staged bootstrap only")
	bootstrapID := f.String("bootstrap-id", "", "exact staged Local bootstrap identity")
	bootstrapHash := f.String("bootstrap-core-sha256", "", "signed staged core digest")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !update.IDOK(*probe) {
		return errors.New("one inventory challenge required")
	}
	ctx := context.Background()
	if *bootstrapOwner != 0 || *bootstrapID != "" || *bootstrapHash != "" {
		if *bootstrapOwner <= 0 || !update.IDOK(*bootstrapID) || len(*bootstrapHash) != 64 {
			return errors.New("complete staged Local bootstrap identity required")
		}
		return bootstrap(ctx, *dir, *bootstrapOwner, *bootstrapID, *bootstrapHash, *probe, out)
	}
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = loadUpdateSession(ctx, c, *dir); e != nil {
		return e
	}
	var s update.CoreSnapshot
	if e = c.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+*probe, nil, &s); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(s)
}

// The caller has verified the root-owned candidate, original enrollment and app
// UID. Use private IPC directly, not the predecessor's public native proxy.
// A token hash is persisted by existing auth; the token itself is never saved.
func readBootstrapLocalInventory(ctx context.Context, c *core.Client, instance string, owner int, probe string, out io.Writer) error {
	defer func() { c.Token, c.CSRF = "", "" }()
	if instance == "" || c.Endpoint.InstanceID != instance || owner <= 0 || !update.IDOK(probe) {
		return errors.New("Local bootstrap inventory instance/challenge changed")
	}
	var health core.Health
	if e := c.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); e != nil {
		return e
	}
	if health.InstanceID != instance || health.Connection == nil || health.Connection.Location != "local" || health.Connection.OwnerUID != owner {
		return errors.New("Local bootstrap inventory owner changed")
	}
	var auth struct {
		Claimed bool `json:"claimed"`
	}
	if e := c.Do(ctx, http.MethodGet, "/api/v1/auth/state", nil, &auth); e != nil {
		return e
	}
	if !auth.Claimed {
		return errors.New("Local predecessor must already be claimed")
	}
	if e := c.NativeLocalOwner(ctx); e != nil {
		return e
	}
	var snapshot update.CoreSnapshot
	if e := c.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+probe, nil, &snapshot); e != nil {
		return e
	}
	if snapshot.Version != 1 || snapshot.InstanceID != instance || snapshot.ProbeID != probe || !update.IDOK(snapshot.BootID) || snapshot.State.Name != "core-db" {
		return errors.New("Local bootstrap inventory response changed")
	}
	return json.NewEncoder(out).Encode(snapshot)
}
