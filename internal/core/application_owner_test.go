package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/update"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplicationOwnerLaunchLeasePrivateIPCAndOutcome(t *testing.T) {
	s := fixtureStore(t)
	fixture := fixtureUpdateController()
	launched := 0
	if e := s.BindApplicationMaintenance(fixture.Check, func(context.Context, string) error { launched++; return nil }); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	queued := updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, _ := s.ApplicationUpdates(t.Context())
	if v.Operation.Phase != "applying" || launched != 1 {
		t.Fatal(v, launched)
	}
	id := v.Operation.ID
	lease := update.NewProbe()
	claim, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: lease})
	if e != nil || !claim.MayApply {
		t.Fatal(claim, e)
	}
	duplicate, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: update.NewProbe()})
	if e != nil || duplicate.MayApply {
		t.Fatal("owner duplicated", duplicate, e)
	}
	if _, e = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: update.NewProbe()}); e == nil {
		t.Fatal("wrong lease")
	}
	checkpoint, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: lease})
	if e != nil || checkpoint.Checkpoint == nil {
		t.Fatal(checkpoint, e)
	}
	offline, err := ReadApplicationOwnerOffline(t.Context(), s.dir, id)
	if err != nil || offline.View.Operation.OwnerLease != lease || offline.Checkpoint == nil {
		t.Fatal("offline original inspection", offline, err)
	}
	if err = CheckApplicationRollbackMetadata(t.Context(), s.dir, id, StateSchema); err != nil {
		t.Fatal("failed target IPC must not prevent compatible rollback", err)
	}
	if err = CheckApplicationRollbackMetadata(t.Context(), s.dir, id, StateSchema+1); err == nil {
		t.Fatal("incompatible reader admitted")
	}
	proof := ApplicationUpdateReceipt{ID: id, Sequence: queued.Operation.Release.Sequence, ManifestSHA256: queued.Operation.Release.ManifestSHA256, EngineSHA256: queued.Operation.Release.EngineSHA256, Phase: "completed"}
	finished, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: id, Lease: lease, Receipt: &proof})
	if e != nil || finished.View.Operation.Phase != "completed" {
		t.Fatal(finished, e)
	}
	if _, e = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: id, Lease: lease, Receipt: &proof}); e != nil {
		t.Fatal("idempotent owner receipt", e)
	}
	notice, e := s.ApplicationUpdates(t.Context())
	if e != nil || notice.Notice == nil || notice.Notice.Read {
		t.Fatal("missing actionable update notice", notice, e)
	}
	if _, e = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "acknowledge", Revision: notice.Revision, PolicyRevision: notice.PolicyRevision}); e != nil {
		t.Fatal(e)
	}
	readNotice, _ := s.ApplicationUpdates(t.Context())
	if readNotice.Notice == nil || !readNotice.Notice.Read {
		t.Fatal("notice acknowledgement not preserved", readNotice)
	}
	h, e := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(ApplicationOwnerCommand{Action: "inspect", ID: id})
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/updates/application/owner", bytes.NewReader(raw))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	public := httptest.NewRecorder()
	h.ServeHTTP(public, request())
	if public.Code != 403 {
		t.Fatal("public owner protocol", public.Code)
	}
	private := httptest.NewRecorder()
	privateIPCHandler(h).ServeHTTP(private, request())
	if private.Code != 200 {
		t.Fatal("private owner inspect", private.Code, private.Body.String())
	}
	bad := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/updates/application/owner", strings.NewReader(`{"action":"inspect","id":"`+id+`","path":"/arbitrary"}`))
	bad.Header.Set("Origin", "http://127.0.0.1:7331")
	bad.Header.Set("Content-Type", "application/json")
	refused := httptest.NewRecorder()
	privateIPCHandler(h).ServeHTTP(refused, bad)
	if refused.Code != 400 {
		t.Fatal("arbitrary owner input", refused.Code)
	}
}

func TestApplicationOwnerReleaseAndOutcomeCommitAtomically(t *testing.T) {
	s := fixtureStore(t)
	fixture := fixtureUpdateController()
	if e := s.BindApplicationMaintenance(fixture.Check, func(context.Context, string) error { return nil }); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, _ := s.ApplicationUpdates(t.Context())
	id := v.Operation.ID
	lease := update.NewProbe()
	if _, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: lease}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: lease}); e != nil {
		t.Fatal(e)
	}
	proof := ApplicationUpdateReceipt{ID: id, Sequence: v.Operation.Release.Sequence, ManifestSHA256: v.Operation.Release.ManifestSHA256, EngineSHA256: v.Operation.Release.EngineSHA256, Phase: "completed"}
	s.pauseFault = func(step string) error {
		if step == "application-release:before-commit" {
			return errors.New("disposable crash before atomic release")
		}
		return nil
	}
	if _, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: id, Lease: lease, Receipt: &proof}); e == nil {
		t.Fatal("fault not exercised")
	}
	held, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	if e != nil || held.State != "paused" {
		t.Fatal("partial release", held, e)
	}
	before, _ := s.ApplicationUpdates(t.Context())
	if before.Operation.Receipt != nil || before.Operation.Phase != "applying" {
		t.Fatal("partial outcome", before)
	}
	s.pauseFault = nil
	done, e := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: id, Lease: lease, Receipt: &proof})
	if e != nil || done.View.Operation.Phase != "completed" || done.View.Operation.Receipt == nil {
		t.Fatal(done, e)
	}
	released, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	if e != nil || released.State != "released" {
		t.Fatal(released, e)
	}
}
