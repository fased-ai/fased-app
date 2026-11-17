//go:build linux

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type pendingOwnerCompletionController struct {
	*updateControllerFixture
	settled     bool
	statusPhase string
	recoveries  int
	recoveredID string
	recoveryErr error
}

func (d *pendingOwnerCompletionController) Apply(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	r, err := d.updateControllerFixture.Apply(ctx, o)
	r.Phase = "applying"
	return r, err
}

func (d *pendingOwnerCompletionController) Status(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	r, err := d.updateControllerFixture.Status(ctx, o)
	if !d.settled {
		r.Phase = "applying"
	}
	if d.statusPhase != "" {
		r.Phase = d.statusPhase
	}
	return r, err
}

func (d *pendingOwnerCompletionController) Recover(_ context.Context, o ApplicationUpdateOperation) error {
	d.recoveries++
	d.recoveredID = o.ID
	if d.recoveryErr != nil {
		return d.recoveryErr
	}
	d.statusPhase = "applying"
	return nil
}

func TestApplicationUpdateOwnerCompletionRemainsRecoverableThroughUserEntry(t *testing.T) {
	s := fixtureStore(t)
	token := claimFixture(t, s)
	d := &pendingOwnerCompletionController{updateControllerFixture: fixtureUpdateController()}
	if err := s.BindApplicationUpdateController(d); err != nil {
		t.Fatal(err)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, err := s.ApplicationUpdates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id := v.Operation.ID
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: id, Lease: id, Receipt: &d.receipt}); err != nil {
		t.Fatal(err)
	}
	// The user endpoint must not report success while protected completion is pending.
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	read := func() ApplicationUpdateView {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7331/api/v1/updates/application", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(w, r)
		var got ApplicationUpdateView
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal("update endpoint unavailable", w.Code, w.Body.String())
		}
		return got
	}
	v = read()
	if v.Operation.ID != id || v.Operation.Phase != "applying" || v.Notice != nil {
		t.Fatalf("reported completed before protected completion: %+v", v.Operation)
	}
	if _, err = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "recover", Revision: v.Revision, PolicyRevision: v.PolicyRevision}); err == nil || d.recoveries != 0 {
		t.Fatal("started recovery while the original worker was still running")
	}
	d.statusPhase = "recovery"
	v = read()
	if v.Operation.ID != id || v.Operation.Phase != "recovery" {
		t.Fatalf("interrupted completion is not recoverable: %+v", v.Operation)
	}
	if v.Notice == nil || v.Notice.Read {
		t.Fatal("the protected recovery notice was not shown")
	}
	if _, err = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "acknowledge", Revision: v.Revision, PolicyRevision: v.PolicyRevision}); err != nil {
		t.Fatal("could not acknowledge the visible recovery notice", err)
	}
	v = read()
	if v.Notice == nil || !v.Notice.Read {
		t.Fatal("acknowledgement did not apply to the visible recovery notice")
	}
	original := d.receipt
	d.receipt.EngineSHA256 = strings.Repeat("f", 64)
	if _, err = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "recover", Revision: v.Revision, PolicyRevision: v.PolicyRevision}); err == nil || d.recoveries != 0 {
		t.Fatal("a foreign protected outcome was allowed to start recovery")
	}
	d.receipt = original
	d.recoveryErr = errors.New("fixture refused the protected worker")
	if _, err = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "recover", Revision: v.Revision, PolicyRevision: v.PolicyRevision}); err == nil || read().Operation.Phase != "recovery" {
		t.Fatal("a refused recovery was not retained")
	}
	d.recoveryErr = nil
	payload, err := json.Marshal(ApplicationUpdateCommand{Action: "recover", Revision: v.Revision, PolicyRevision: v.PolicyRevision})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/updates/application", bytes.NewReader(payload))
	r.Header.Set("Origin", "http://127.0.0.1:7331")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-CSRF-Token", csrfToken(token))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var response ApplicationUpdateView
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Operation.ID != id || response.Operation.Phase != "applying" {
		t.Fatal("could not resume the original protected worker through the user entry", w.Code, w.Body.String())
	}
	if d.recoveries != 2 || d.recoveredID != id || d.applies != 1 || read().Operation.Phase != "applying" {
		t.Fatal("recovery replaced or duplicated the update", d.recoveries, d.recoveredID, d.applies)
	}
	d.statusPhase, d.settled = "", true
	v = read()
	if v.Operation.ID != id || v.Operation.Phase != "completed" || v.Notice == nil || v.Notice.Read {
		t.Fatal("verified completion was not exposed", v.Operation)
	}
}

func TestApplicationUpdateDoesNotReplaceOwnerOperationBeforeProtectedCompletion(t *testing.T) {
	s := fixtureStore(t)
	d := &pendingOwnerCompletionController{updateControllerFixture: fixtureUpdateController()}
	if err := s.BindApplicationUpdateController(d); err != nil {
		t.Fatal(err)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, err := s.ApplicationUpdates(t.Context())
	if err != nil || v.Operation.Phase != "applying" {
		t.Fatal("update was not dispatched", err)
	}
	id := v.Operation.ID
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: id, Lease: id, Receipt: &d.receipt}); err != nil {
		t.Fatal(err)
	}
	// The core has committed the receipt, but the protected controller has not
	// yet durably completed its journal or removed the original update fence.
	updateModeFixture(t, s, UpdatePolicy{Mode: "automatic", Notify: true})
	v, err = s.ApplicationUpdates(t.Context())
	if err != nil || v.Operation.ID != id || v.Operation.Phase != "completed" {
		t.Fatal("original owner operation is unavailable", err)
	}
	if _, err = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "check", Revision: v.Revision, PolicyRevision: v.PolicyRevision}); err == nil {
		t.Fatal("manual check replaced an operation still held by its controller")
	}
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if err = s.runApplicationUpdates(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := s.ApplicationUpdates(t.Context())
	if err != nil || after.Operation.ID != id || after.Operation.Phase != "completed" || d.applies != 1 {
		t.Fatal("automatic check replaced or replayed the original operation", after.Operation, err)
	}
	// A terminal status for different bytes is not the original completion.
	d.settled = true
	originalReceipt := d.receipt
	d.receipt.EngineSHA256 = strings.Repeat("f", 64)
	if _, err = s.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "check", Revision: after.Revision, PolicyRevision: after.PolicyRevision}); err == nil {
		t.Fatal("a different terminal receipt released the original operation")
	}
	d.receipt, d.settled = originalReceipt, false
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err = restarted.BindApplicationUpdateController(d); err != nil {
		t.Fatal(err)
	}
	if original, err := ReadApplicationOwnerOffline(t.Context(), dir, id); err != nil || original.View.Operation.ID != id || original.Checkpoint == nil {
		t.Fatal("restart lost the original recovery checkpoint", err)
	}
	d.settled = true
	v, err = restarted.ApplicationUpdates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next, err := restarted.CommandApplicationUpdate(t.Context(), ApplicationUpdateCommand{Action: "check", Revision: v.Revision, PolicyRevision: v.PolicyRevision})
	if err != nil || next.Operation.Phase != "check-queued" || d.applies != 1 {
		t.Fatal("completed controller should permit the next check without replay", next.Operation, err)
	}
}
