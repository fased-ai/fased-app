package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// Staging consumes only the already reviewed immutable intent. It requires no
// second permission prompt and cannot install, unlock, or clear recovery holds.
func conductStage(ctx context.Context, id, dir string, probe coreProbe, call updateRPC, out io.Writer) error {
	if !update.IDOK(id) {
		return errors.New("exact intent ID required")
	}
	r, e := call(ctx, signer.Request{Action: "fixture-update-status", IntentID: id})
	if e != nil {
		return e
	}
	if r.UpdateIntent == nil || !update.Stageable(*r.UpdateIntent) {
		return errors.New("stageable durable intent required")
	}
	i := *r.UpdateIntent
	request := update.StageRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256}
	checkCore := func() (*update.CoreSnapshot, error) {
		c, e := probe(ctx, i.Core.ProbeID)
		if e != nil {
			return nil, e
		}
		if update.SnapshotDigest("core", c) != update.SnapshotDigest("core", i.Core) {
			return nil, errors.New("core metadata/boot changed; abort and review a fresh intent")
		}
		return &c, nil
	}
	if _, e = checkCore(); e != nil {
		return e
	}
	if i.State != "staged" {
		for _, a := range i.Artifacts {
			request.File = a.File
			e = update.StreamArtifact(dir, a, func(offset int64, data []byte) error {
				request.Offset = offset
				request.Data = data
				_, e := call(ctx, signer.Request{Action: "fixture-update-stage-write", Stage: &request})
				return e
			})
			if e != nil {
				return e
			}
		}
	}
	request.File = ""
	request.Offset = 0
	request.Data = nil
	request.Core, e = checkCore()
	if e != nil {
		return e
	}
	r, e = call(ctx, signer.Request{Action: "fixture-update-stage-finish", Stage: &request})
	if e != nil {
		return e
	}
	if r.Stage == nil || !r.Stage.Verified {
		return errors.New("verified durable staging receipt unavailable")
	}
	return json.NewEncoder(out).Encode(r.Stage)
}
