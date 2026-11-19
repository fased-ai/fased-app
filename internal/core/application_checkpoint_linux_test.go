//go:build linux

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemotePackageDoesNotFallBackToAppWritableSocket(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	t.Setenv("LISTEN_FDS", "")
	t.Setenv("LISTEN_FDNAMES", "")
	data := "/var/lib/fased-app-package/1000/remote/" + strings.Repeat("a", 32) + "/app-state"
	h := &HTTPHandler{publicHost: "node.example.ts.net"}
	server := &Server{HTTP: &http.Server{Handler: h}, dir: data}
	err := server.ConfigureConnection("remote", 1000)
	if err == nil || !strings.Contains(err.Error(), "protected private listener") || server.Remote != nil || server.RemoteLegacy != nil {
		t.Fatal("Remote package fell back without its protected listener", err)
	}
}

func TestUnconfiguredProtectedSignerAllowsLocalOwnerAndApplicationCheckpoint(t *testing.T) {
	uid := os.Getuid()
	if uid <= 0 {
		t.Skip("ordinary app identity required")
	}
	s := fixtureStore(t)
	config := signer.Config{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance,
		SignerID: strings.Repeat("a", 32), OwnerUID: uid + 1, AppUID: uid, SignerUID: uid + 2, StateDir: "/fixture/signer-state", SocketDir: "/fixture/signer-ipc"}
	if err := s.configureVerifiedSigner(config); err != nil {
		t.Fatal("valid unconfigured signer was refused", err)
	}
	foreign := &Server{Store: s, HTTP: &http.Server{Handler: &HTTPHandler{store: s}}}
	if err := foreign.ConfigureConnection("local", config.OwnerUID+1); err == nil {
		t.Fatal("a different Local owner was accepted")
	}
	h, err := NewHTTPHandler(s, "http://127.0.0.1:7331", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: s, HTTP: &http.Server{Handler: h}}
	if err := server.ConfigureConnection("local", config.OwnerUID); err != nil {
		t.Fatal("the verified Local owner could not open the fresh installation", err)
	}
	if !h.protectedLocalOwnerAllowed() || h.localOwnerAllowed() {
		t.Fatal("fresh custody did not retain the protected Local owner boundary")
	}
	request := func(private bool) int {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/auth/native-local", bytes.NewBufferString(`{"action":"launch"}`))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if private {
			privateIPCHandler(h).ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		return w.Code
	}
	if request(false) != http.StatusForbidden || request(true) != http.StatusOK {
		t.Fatal("Local launch must be available only through the protected owner entry")
	}
	remoteHandler := &HTTPHandler{store: s}
	remote := &Server{Store: s, HTTP: &http.Server{Handler: remoteHandler}}
	if err := remote.ConfigureConnection("remote", config.OwnerUID); err != nil || remoteHandler.instanceOwnerKind != "custody" {
		t.Fatal("the original Remote custody owner was not retained", err)
	}
	if state := s.walletSetup(FixtureView{}); state.State != "unavailable" {
		t.Fatalf("unverified signer files must remain unavailable, got %q", state.State)
	}
	calls := 0
	s.fixture = func(context.Context, signer.Request) (signer.Response, error) {
		calls++
		return signer.Response{}, errors.New("unconfigured signer must not be contacted")
	}
	view, err := s.Accounts(t.Context())
	if err != nil || calls != 0 || view.Setup.State != "unavailable" {
		t.Fatal("pre-wallet account view contacted or assumed an empty signer", calls, err)
	}
	status := s.ReadUpdateStatus(t.Context())
	if calls != 0 || status.State != "not-configured" || status.Signer.State != "not-configured" || status.Signer.Locked != nil || status.Signer.UpdateHeld != nil || status.Signer.RecoveryRequired != nil {
		t.Fatal("pre-wallet update status contacted or inferred signer state", calls, status)
	}
	for _, guidance := range status.Guidance {
		if guidance.Code == "owner-review" {
			t.Fatal("pre-wallet update status exposed a fixture-only owner command")
		}
	}
	d := fixtureUpdateController()
	if err := s.BindApplicationUpdateController(d); err != nil {
		t.Fatal(err)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	v, err := s.ApplicationUpdates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	op := v.Operation
	op.Phase = "applying"
	if err = s.publishApplicationUpdate(t.Context(), op, v.Revision); err != nil {
		t.Fatal(err)
	}
	ownerCall := func(private bool, command ApplicationOwnerCommand) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(command)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7331/api/v1/updates/application/owner", bytes.NewReader(body))
		r.Header.Set("Origin", "http://127.0.0.1:7331")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if private {
			privateIPCHandler(h).ServeHTTP(w, r)
		} else {
			h.ServeHTTP(w, r)
		}
		return w
	}
	lease := update.NewProbe()
	claim := ApplicationOwnerCommand{Action: "claim", ID: op.ID, Lease: lease}
	if w := ownerCall(false, claim); w.Code != http.StatusForbidden {
		t.Fatal("browser reached the protected update owner entry", w.Code)
	}
	if w := ownerCall(true, claim); w.Code != http.StatusOK {
		t.Fatal("protected update owner claim failed", w.Code, w.Body.String())
	}
	if w := ownerCall(true, ApplicationOwnerCommand{Action: "prepare", ID: op.ID, Lease: update.NewProbe()}); w.Code != http.StatusConflict {
		t.Fatal("another lease reached the checkpoint", w.Code)
	}
	w := ownerCall(true, ApplicationOwnerCommand{Action: "prepare", ID: op.ID, Lease: lease})
	var prepared ApplicationOwnerView
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &prepared) != nil || prepared.Checkpoint == nil || prepared.Checkpoint.OperationID != op.ID || prepared.Checkpoint.Pause.State != "paused" {
		t.Fatal("the original pre-wallet application checkpoint was refused", w.Code, w.Body.String())
	}
	if s.custodyOwnerSupported || s.custodyNetwork != "" {
		t.Fatal("application checkpoint enabled wallet authority")
	}
}

func TestUnconfiguredProtectedSignerCheckpointRefusesSignerBoundAccounts(t *testing.T) {
	uid := os.Getuid()
	if uid <= 0 {
		t.Skip("ordinary app identity required")
	}
	s := fixtureStore(t)
	config := signer.Config{Version: 3, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance,
		SignerID: strings.Repeat("a", 32), OwnerUID: uid + 1, AppUID: uid, SignerUID: uid + 2, StateDir: "/fixture/signer-state", SocketDir: "/fixture/signer-ipc"}
	if err := s.configureVerifiedSigner(config); err != nil {
		t.Fatal(err)
	}
	if err := s.BindApplicationUpdateController(fixtureUpdateController()); err != nil {
		t.Fatal(err)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	v, err := s.ApplicationUpdates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	op := v.Operation
	op.Phase = "applying"
	if err = s.publishApplicationUpdate(t.Context(), op, v.Revision); err != nil {
		t.Fatal(err)
	}
	document, err := json.Marshal(AccountsRecord{Version: 1, Accounts: []Account{{Signer: &signer.AccountRef{}}}, Proposals: []ProposalDraft{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), "UPDATE accounts SET document=? WHERE id=1", string(document)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PrepareApplicationCheckpoint(t.Context(), op.ID); err == nil {
		t.Fatal("a signer-bound account entered the empty-signer checkpoint")
	}
}

type checkpointController struct {
	*updateControllerFixture
	s      *Store
	fail   bool
	backup ApplicationCheckpoint
}

func (d *checkpointController) Apply(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	d.applies++
	c, e := d.s.PrepareApplicationCheckpoint(ctx, o.ID)
	if e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	d.backup = c
	setup, e := d.s.Setup(ctx)
	if e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	setup.Settings.Host = "vps"
	if _, e = d.s.UpdateSetup(ctx, setup.Revision, setup.Settings); !errors.Is(e, ErrCorePaused) {
		return ApplicationUpdateReceipt{}, errors.New("metadata fence failed")
	}
	d.receipt = ApplicationUpdateReceipt{EngineSHA256: o.Release.EngineSHA256, ID: o.ID, Sequence: o.Release.Sequence, ManifestSHA256: o.Release.ManifestSHA256, Phase: "completed"}
	if d.fail {
		return ApplicationUpdateReceipt{}, errors.New("disposable controller interruption")
	}
	if e = d.s.CompleteApplicationCheckpoint(ctx, o.ID, d.receipt); e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	return d.receipt, nil
}
func (d *checkpointController) Status(ctx context.Context, o ApplicationUpdateOperation) (ApplicationUpdateReceipt, error) {
	d.statuses++
	if e := d.s.CompleteApplicationCheckpoint(ctx, o.ID, d.receipt); e != nil {
		return ApplicationUpdateReceipt{}, e
	}
	return d.receipt, nil
}
func TestApplicationCheckpointFencesBacksUpAndRecoversWithoutWallet(t *testing.T) {
	s := fixtureStore(t)
	d := &checkpointController{updateControllerFixture: fixtureUpdateController(), s: s, fail: true}
	if e := s.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	marker := []byte("disposable encrypted provider bytes; no keys or funds")
	if e := os.WriteFile(filepath.Join(s.dir, "retained-ciphertext"), marker, 0600); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, e := s.ApplicationUpdates(t.Context())
	if e != nil || v.Operation.Phase != "recovery" || d.applies != 1 {
		t.Fatal(v, e)
	}
	p, e := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "status", ProbeID: update.NewProbe()})
	if e != nil || p.State != "paused" {
		t.Fatal(p, e)
	}
	if !strings.HasPrefix(d.backup.Snapshot.File, "application-") || d.backup.Snapshot.Size <= 0 {
		t.Fatal(d.backup)
	}
	other, e := OpenStore(s.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	d.s = other
	if e = other.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	updateTickFixture(t, other)
	if d.statuses != 0 {
		t.Fatal("held transaction retried")
	}
	updateCommandFixture(t, other, "recover")
	updateTickFixture(t, other)
	v, e = other.ApplicationUpdates(t.Context())
	if e != nil || v.Operation.Phase != "completed" || d.applies != 1 || d.statuses != 1 {
		t.Fatal(v, e)
	}
	if e = other.CompleteApplicationCheckpoint(t.Context(), v.Operation.ID, d.receipt); e != nil {
		t.Fatal("idempotent release", e)
	}
	// A replay after restart is idempotent only for the original outcome. In
	// particular, a different rollback must not be acknowledged as completed.
	changed := d.receipt
	changed.Phase = "rolled-back"
	changed.EngineSHA256 = strings.Repeat("b", 64)
	if e = other.CompleteApplicationCheckpoint(t.Context(), v.Operation.ID, changed); e == nil {
		t.Fatal("a different outcome was accepted after the original fence was released")
	}
	got, e := os.ReadFile(filepath.Join(s.dir, "retained-ciphertext"))
	if e != nil || string(got) != string(marker) {
		t.Fatal("ciphertext changed", e)
	}
	setup, e := other.Setup(t.Context())
	if e != nil || setup.Settings.Host != "local" {
		t.Fatal("owner records changed", setup, e)
	}
}

func TestApplicationCheckpointOwnerRetryAfterRestartReusesOriginalHold(t *testing.T) {
	s := fixtureStore(t)
	fixture := fixtureUpdateController()
	starts := 0
	if err := s.BindApplicationMaintenance(fixture.Check, func(context.Context, string) error {
		starts++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	view, err := s.ApplicationUpdates(t.Context())
	if err != nil || view.Operation.Phase != "applying" || starts != 1 {
		t.Fatal("original update was not dispatched once", starts, err)
	}
	id := view.Operation.ID
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: id}); err != nil {
		t.Fatal(err)
	}
	first, err := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: id})
	if err != nil || first.Checkpoint == nil {
		t.Fatal("original checkpoint was not saved", err)
	}
	// The application committed its checkpoint, but the protected controller
	// has not yet saved it. Reopen the same store as the original app identity.
	dir, boot := s.dir, s.bootID
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.bootID == boot {
		t.Fatal("the reopened core did not have a new process identity")
	}
	if _, err = restarted.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: update.NewProbe()}); err == nil {
		t.Fatal("another lease was allowed to reuse the checkpoint")
	}
	claim, err := restarted.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: id})
	if err != nil || claim.MayApply || claim.View.Operation.OwnerLease != id {
		t.Fatal("restarted owner did not retain the original claim", claim, err)
	}
	second, err := restarted.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: id})
	if err != nil || second.Checkpoint == nil || update.SnapshotDigest("original-checkpoint", *second.Checkpoint) != update.SnapshotDigest("original-checkpoint", *first.Checkpoint) {
		t.Fatal("restarted owner did not reopen the original checkpoint", err)
	}
	offline, err := ReadApplicationOwnerOffline(t.Context(), dir, id)
	if err != nil || offline.Checkpoint == nil || offline.View.Operation.OwnerLease != id ||
		update.SnapshotDigest("original-checkpoint", *offline.Checkpoint) != update.SnapshotDigest("original-checkpoint", *first.Checkpoint) || starts != 1 {
		t.Fatal("offline recovery changed the original operation", starts, err)
	}
}

func TestApplicationCheckpointOfflineRetainsAutomaticPolicy(t *testing.T) {
	s := fixtureStore(t)
	fixture := fixtureUpdateController()
	starts := 0
	if err := s.BindApplicationMaintenance(fixture.Check, func(context.Context, string) error {
		starts++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p := UpdatePolicy{Mode: "automatic", Notify: true, Pin: &UpdatePin{Sequence: "7", ManifestSHA256: fixture.release.ManifestSHA256}}
	updateModeFixture(t, s, p)
	updateTickFixture(t, s)
	updateTickFixture(t, s)
	updateTickFixture(t, s)
	view, err := s.ApplicationUpdates(t.Context())
	if err != nil || !view.Operation.Automatic || view.Operation.Phase != "applying" || starts != 1 {
		t.Fatal("controlled Automatic was not dispatched once", err)
	}
	id := view.Operation.ID
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: id}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: id}); err != nil {
		t.Fatal(err)
	}
	dir := s.dir
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	offline, err := ReadApplicationOwnerOffline(t.Context(), dir, id)
	if err != nil || offline.Checkpoint == nil || offline.View.Operation.OwnerLease != id || !offline.View.Operation.Automatic ||
		update.SnapshotDigest("application-update-policy", offline.View.Policy) != view.Operation.PolicySHA256 {
		t.Fatal("stopped-service readback lost the original Automatic lease or policy", err)
	}
}

func TestApplicationCheckpointRetryRequiresExactRolledBackOutcome(t *testing.T) {
	s := fixtureStore(t)
	fixture := fixtureUpdateController()
	if err := s.BindApplicationMaintenance(fixture.Check, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	updateTickFixture(t, s)
	v, err := s.ApplicationUpdates(t.Context())
	if err != nil || v.Operation.Release == nil {
		t.Fatal("missing update", err)
	}
	id, lease := v.Operation.ID, update.NewProbe()
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: lease}); err != nil {
		t.Fatal(err)
	}
	held, err := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: lease})
	if err != nil || held.Checkpoint == nil {
		t.Fatal("missing original checkpoint", err)
	}
	proof := ApplicationUpdateReceipt{ID: id, Sequence: v.Operation.Release.Sequence, ManifestSHA256: v.Operation.Release.ManifestSHA256, EngineSHA256: held.Checkpoint.PreviousEngineSHA256, Phase: "rolled-back"}
	if err = s.CompleteApplicationCheckpoint(t.Context(), id, proof); err != nil {
		t.Fatal("original rollback", err)
	}
	if err = s.CompleteApplicationCheckpoint(t.Context(), id, proof); err != nil {
		t.Fatal("identical rollback retry", err)
	}
	proof.EngineSHA256 = strings.Repeat("b", 64)
	if proof.EngineSHA256 == held.Checkpoint.PreviousEngineSHA256 {
		proof.EngineSHA256 = strings.Repeat("c", 64)
	}
	if err = s.CompleteApplicationCheckpoint(t.Context(), id, proof); err == nil {
		t.Fatal("different rollback executable was accepted after release")
	}
}

func TestApplicationCheckpointRefusesCustodyAndTamperedIndex(t *testing.T) {
	s := fixtureStore(t)
	d := fixtureUpdateController()
	if e := s.BindApplicationUpdateController(d); e != nil {
		t.Fatal(e)
	}
	updateCommandFixture(t, s, "check")
	updateTickFixture(t, s)
	updateCommandFixture(t, s, "apply")
	v, _ := s.ApplicationUpdates(t.Context())
	o := v.Operation
	o.Phase = "applying"
	if e := s.publishApplicationUpdate(t.Context(), o, v.Revision); e != nil {
		t.Fatal(e)
	}
	s.custodyNetwork = "disposable"
	if _, e := s.PrepareApplicationCheckpoint(t.Context(), o.ID); e == nil {
		t.Fatal("custody accepted")
	}
	s.custodyNetwork = ""
	c, e := s.PrepareApplicationCheckpoint(t.Context(), o.ID)
	if e != nil {
		t.Fatal(e)
	}
	dir, e := recoveryDirectory(s.dir, o.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer closeCheckpointDir(dir)
	if e = corruptCheckpointFile(dir, c.Snapshot.File); e != nil {
		t.Fatal(e)
	}
	if _, e = s.PrepareApplicationCheckpoint(t.Context(), o.ID); e == nil {
		t.Fatal("tampered backup accepted")
	}
}

func TestApplicationOwnerOfflineRefusesDamagedCheckpoint(t *testing.T) {
	s := fixtureStore(t)
	fixture := fixtureUpdateController()
	if err := s.BindApplicationMaintenance(fixture.Check, func(context.Context, string) error { return nil }); err != nil {
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
	id, lease := v.Operation.ID, update.NewProbe()
	if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "claim", ID: id, Lease: lease}); err != nil {
		t.Fatal(err)
	}
	view, err := s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "prepare", ID: id, Lease: lease})
	if err != nil || view.Checkpoint == nil {
		t.Fatal("original checkpoint unavailable", err)
	}
	path := filepath.Join(s.dir, "lifecycle", id, "application.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*ApplicationCheckpoint){
		"unknown version":    func(c *ApplicationCheckpoint) { c.Version++ },
		"invalid old binary": func(c *ApplicationCheckpoint) { c.PreviousEngineSHA256 = "invalid" },
		"pause not held":     func(c *ApplicationCheckpoint) { c.Pause.State = "released" },
	} {
		t.Run(name, func(t *testing.T) {
			c := *view.Checkpoint
			change(&c)
			b, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Error(err)
				}
			}()
			if _, err = ReadApplicationOwnerOffline(t.Context(), s.dir, id); err == nil {
				t.Fatal("damaged checkpoint was accepted for offline recovery")
			}
			if _, err = s.PrepareApplicationCheckpoint(t.Context(), id); err == nil {
				t.Fatal("damaged checkpoint was accepted by the live owner entry")
			}
		})
	}
	t.Run("different live fence", func(t *testing.T) {
		var originalPause string
		if err := s.db.QueryRowContext(t.Context(), "SELECT document FROM update_pause WHERE id=1").Scan(&originalPause); err != nil {
			t.Fatal(err)
		}
		var pause pauseRecord
		if err := json.Unmarshal([]byte(originalPause), &pause); err != nil {
			t.Fatal(err)
		}
		pause.Request.Expected.State.Anchor = strings.Repeat("f", 64)
		if pause.Request.Expected.State.Anchor == view.Checkpoint.Pause.Request.Expected.State.Anchor {
			pause.Request.Expected.State.Anchor = strings.Repeat("e", 64)
		}
		changed, err := json.Marshal(pause)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.ExecContext(t.Context(), "UPDATE update_pause SET document=? WHERE id=1", string(changed)); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := s.db.ExecContext(context.Background(), "UPDATE update_pause SET document=? WHERE id=1", originalPause); err != nil {
				t.Error(err)
			}
		}()
		if _, err = ReadApplicationOwnerOffline(t.Context(), s.dir, id); err == nil {
			t.Fatal("checkpoint for a different live fence was accepted")
		}
	})
	if got, err := ReadApplicationOwnerOffline(t.Context(), s.dir, id); err != nil || got.Checkpoint == nil || got.View.Operation.OwnerLease != lease {
		t.Fatal("original recovery was not preserved", err)
	}
}

func closeCheckpointDir(d int) { _ = unix.Close(d) }
func corruptCheckpointFile(d int, name string) error {
	fd, e := unix.Openat(d, name, unix.O_WRONLY|unix.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	_, e = unix.Write(fd, []byte("invalid"))
	return e
}
