package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func updateBackupCommand(args []string, in *os.File, out, errOut io.Writer) error {
	f := flag.NewFlagSet("update-backup", flag.ContinueOnError)
	f.SetOutput(errOut)
	base, e := defaultDir()
	if e != nil {
		return e
	}
	dir := f.String("data-dir", base, "private app state")
	stdin := f.Bool("request-stdin", false, "public held preflight JSON; never snapshot bytes")
	if e = f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !*stdin || in == nil {
		return errors.New("one public preflight on stdin required")
	}
	b, e := io.ReadAll(io.LimitReader(in, 16385))
	if e != nil {
		return e
	}
	var p update.Preflight
	if len(b) > 16384 || update.StrictJSON(b, &p) != nil {
		return errors.New("invalid backup request")
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
	var index update.RecoveryIndex
	if e = c.Do(ctx, http.MethodPost, "/api/v1/updates/backup", p, &index); e != nil {
		return e
	}
	return json.NewEncoder(out).Encode(index)
}

type coreBackup func(context.Context, update.Preflight) (update.RecoveryIndex, error)

// Both preflights independently read the actual signer/floor and rehash its
// staged files. The core cannot grant financial authority by writing an index.
// Interrupted/aborted calls retain the backup but never grant installation.
func conductBackup(ctx context.Context, id string, control coreControl, backup coreBackup, call updateRPC, out io.Writer) error {
	preflight := func() (update.Preflight, error) {
		var b bytes.Buffer
		e := conductPreflight(ctx, id, control, call, &b)
		var p update.Preflight
		if e == nil {
			e = update.StrictJSON(b.Bytes(), &p)
		}
		return p, e
	}
	p, e := preflight()
	if e != nil {
		return e
	}
	b, e := update.BackupBinding(p)
	if e != nil {
		return e
	}
	index, e := backup(ctx, p)
	if e != nil {
		return e
	}
	if !update.ValidRecoveryIndex(index, b) {
		return errors.New("unbound recovery index reply; preserve held state")
	}
	current, e := preflight()
	if e != nil {
		return e
	}
	now, e := update.BackupBinding(current)
	if e != nil {
		return e
	}
	if update.RecoveryDigest(b) != update.RecoveryDigest(now) {
		return errors.New("signer, staged bytes or pause changed during backup; preserve held state")
	}
	return json.NewEncoder(out).Encode(index)
}
