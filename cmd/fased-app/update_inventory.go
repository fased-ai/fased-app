package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

func updateInventoryCommand(args []string, out, errOut io.Writer) error {
	return updateInventoryCommandWithBootstrap(args, out, errOut, bootstrapCustodyInventoryCommand)
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
	bootstrapOwner := f.Int("bootstrap-owner", 0, "protected custody predecessor owner; staged bootstrap only")
	bootstrapID := f.String("bootstrap-id", "", "exact staged custody bootstrap identity")
	bootstrapHash := f.String("bootstrap-core-sha256", "", "signed staged custody core digest")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !update.IDOK(*probe) {
		return errors.New("one inventory challenge required")
	}
	ctx := context.Background()
	if *bootstrapOwner != 0 || *bootstrapID != "" || *bootstrapHash != "" {
		if *bootstrapOwner <= 0 || !update.IDOK(*bootstrapID) || len(*bootstrapHash) != 64 {
			return errors.New("complete staged custody bootstrap identity required")
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
func readBootstrapCustodyInventory(ctx context.Context, c *core.Client, m custodyinstall.Manifest, instance string, probe string, out io.Writer) error {
	defer func() { c.Token, c.CSRF = "", "" }()
	if instance == "" || c.Endpoint.InstanceID != instance || m.Owner <= 0 || !update.IDOK(probe) || (m.Location != "local" && m.Location != "remote") {
		return errors.New("custody bootstrap inventory instance/challenge changed")
	}
	var health core.Health
	if e := c.Do(ctx, http.MethodGet, "/api/v1/health", nil, &health); e != nil {
		return e
	}
	if health.InstanceID != instance || health.Connection == nil || health.Connection.Location != m.Location || health.Connection.OwnerUID != m.Owner {
		return errors.New("custody bootstrap inventory owner changed")
	}
	var auth struct {
		Claimed bool `json:"claimed"`
	}
	if e := c.Do(ctx, http.MethodGet, "/api/v1/auth/state", nil, &auth); e != nil {
		return e
	}
	if !auth.Claimed {
		return errors.New("custody predecessor must already be claimed")
	}
	if m.Location == "local" {
		if e := c.NativeLocalOwner(ctx); e != nil {
			return e
		}
	} else {
		// The staged receiver already validated the custody root/data, enrollment,
		// executable/hash and dropped AppUID/GID. Do not load/save a session or use
		// the unrelated ordinary VPS configuration. This opens only the existing
		// session table (no schema migration or owner reset); the token stays here.
		token, e := core.IssueMaintenanceSession(ctx, m.Root+"/app-state", instance)
		if e != nil {
			return e
		}
		c.Token = token
	}
	var snapshot update.CoreSnapshot
	if e := c.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+probe, nil, &snapshot); e != nil {
		return e
	}
	if snapshot.Version != 1 || snapshot.InstanceID != instance || snapshot.ProbeID != probe || !update.IDOK(snapshot.BootID) || snapshot.State.Name != "core-db" {
		return errors.New("custody bootstrap inventory response changed")
	}
	return json.NewEncoder(out).Encode(snapshot)
}
