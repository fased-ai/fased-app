package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func updatePauseCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("update-pause", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private app state")
	stdin := f.Bool("request-stdin", false, "public metadata pause command JSON")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !*stdin || in == nil {
		return errors.New("one public pause command on stdin required")
	}
	b, e := io.ReadAll(io.LimitReader(in, 16385))
	if e != nil {
		return e
	}
	var command update.CorePauseCommand
	if len(b) > 16384 || update.StrictJSON(b, &command) != nil {
		return errors.New("invalid pause command")
	}
	ctx := context.Background()
	c, e := core.NewClient(ctx, *dir)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = c.LoadSession(ctx, *dir); e != nil {
		return e
	}
	var receipt update.CorePauseReceipt
	if e = c.Do(ctx, http.MethodPost, "/api/v1/updates/pause", command, &receipt); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(receipt)
}

type coreControl func(context.Context, update.CorePauseCommand) (update.CorePauseReceipt, error)

func conductPreflight(ctx context.Context, id string, control coreControl, call updateRPC, out io.Writer) error {
	if !update.IDOK(id) {
		return errors.New("exact intent ID required")
	}
	r, e := call(ctx, signer.Request{Action: "fixture-update-status", IntentID: id})
	if e != nil {
		return e
	}
	if r.UpdateIntent == nil || r.UpdateIntent.State != "staged" {
		return errors.New("durable staged intent required for preflight")
	}
	i := *r.UpdateIntent
	p := update.NewProbe()
	r, e = call(ctx, signer.Request{Action: "fixture-update-inventory", ProbeID: p})
	if e != nil {
		return e
	}
	if r.UpdateInventory == nil {
		return errors.New("fresh signer inventory unavailable")
	}
	s := *r.UpdateInventory
	want := update.PauseFor(i)
	paused, e := control(ctx, update.CorePauseCommand{Action: "pause", ProbeID: p, Request: &want})
	if e != nil {
		return e
	}
	if e = update.ValidatePaused(i, paused, p); e != nil {
		return e
	}
	r, e = call(ctx, signer.Request{Action: "fixture-update-preflight", Preflight: &update.PreflightRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, ProbeID: p, Pause: paused, ExpectedSigner: s}})
	if e != nil {
		return e
	}
	if r.Preflight == nil || !r.Preflight.Verified || r.Preflight.IntentID != id || r.Preflight.Sequence != i.Sequence || r.Preflight.ManifestSHA256 != i.ManifestSHA256 || r.Preflight.InstallAllowed || r.Preflight.ResumeAllowed || r.Preflight.FinancialExecution {
		return errors.New("verified held preflight unavailable")
	}
	proof := *r.Preflight
	binding := proof.BindingSHA256
	proof.BindingSHA256 = ""
	if proof.Version != 1 || binding != update.SnapshotDigest("preflight:v1", proof) || update.SnapshotDigest("core-pause", proof.Pause) != update.SnapshotDigest("core-pause", paused) || update.SnapshotDigest("signer", proof.Signer) != update.SnapshotDigest("signer", s) {
		return errors.New("preflight reply binding mismatch")
	}
	current, e := control(ctx, update.CorePauseCommand{Action: "status", ProbeID: p})
	if e != nil {
		return e
	}
	if update.SnapshotDigest("core-pause", current) != update.SnapshotDigest("core-pause", paused) {
		return errors.New("core pause changed during preflight; retry while held")
	}
	return json.NewEncoder(out).Encode(r.Preflight)
}

func abortAndRelease(ctx context.Context, id string, control coreControl, call updateRPC, out io.Writer) error {
	r, e := call(ctx, signer.Request{Action: "fixture-update-abort", IntentID: id})
	if e != nil {
		return e
	}
	if r.UpdateIntent == nil || r.UpdateIntent.ID != id || r.UpdateIntent.State != "aborted" {
		return errors.New("aborted signer receipt unavailable; core stays paused")
	}
	p, e := control(ctx, update.CorePauseCommand{Action: "release", ProbeID: update.NewProbe(), Aborted: r.UpdateIntent})
	if e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(struct {
		Intent        *update.Intent          `json:"intent"`
		Core          update.CorePauseReceipt `json:"core"`
		ResumeAllowed bool                    `json:"resumeAllowed"`
	}{Intent: r.UpdateIntent, Core: p})
}
