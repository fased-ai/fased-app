//go:build linux

package core

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/tailnet"
	"github.com/fased-ai/fased-app/internal/update"
)

// Enroll through the real owner API rather than stamping a schema number. This
// is the existing Remote shape that older-reader acceptance did not exercise.
func currentRemoteAdmissionFixture(t *testing.T) (*Store, ApplicationReaderBootstrapRequest, string, string) {
	t.Helper()
	s := fixtureStore(t)
	legacyToken := claimFixture(t, s)
	token, err := s.remoteSession(t.Context(), tailnet.Identity{UserID: 7, NodeID: "test-owner-device", Login: "owner@example.test"}, "", legacyToken)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.UpdateSnapshot(t.Context(), update.NewProbe())
	if err != nil || snapshot.State.Version != RemoteOwnerStateSchema {
		t.Fatal(snapshot, err)
	}
	release := fixtureUpdateController().release
	pause := update.CorePauseRequest{Version: 1, IntentID: update.NewProbe(), Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, Expected: snapshot}
	r := ApplicationReaderBootstrapRequest{Version: 4, OperationID: pause.IntentID, OwnerLease: update.NewProbe(), PreviousEngineSHA256: strings.Repeat("b", 64), Release: release, Predecessor: update.CorePauseReceipt{Version: 1, State: "prepared", Core: snapshot, Request: &pause}}
	return s, r, legacyToken, token
}

func TestCurrentRemoteAdmissionPreservesFormatAndIdentity(t *testing.T) {
	for _, point := range []string{"", "reader:before-commit", "reader:after-commit"} {
		t.Run(point, func(t *testing.T) {
			s, r, legacyToken, token := currentRemoteAdmissionFixture(t)
			original := r.Predecessor.Core
			if err := PrepareApplicationReaderBootstrap(t.Context(), s.dir, r); err != nil {
				t.Fatal("current-format admission", err)
			}
			held, err := s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: r.Predecessor.Request})
			if err != nil {
				t.Fatal(err)
			}
			r.Predecessor = held
			if err = StageApplicationReaderBootstrap(t.Context(), s.dir, r); err != nil {
				t.Fatal(err)
			}
			dir := s.dir
			s.Close()
			lock, err := LockInstance(filepath.Join(dir, "instance.lock"))
			if err != nil {
				t.Fatal(err)
			}
			lock.Close()
			_, err = bootstrapApplicationReader(t.Context(), dir, r, func(p string) error {
				if point != "" && p == point {
					return errors.New("controlled interruption")
				}
				return nil
			})
			if (point != "") != (err != nil) {
				t.Fatal("unexpected admission result", err)
			}
			receipt, err := ResumeApplicationReaderBootstrap(t.Context(), dir, r.OperationID)
			if err != nil || receipt.Phase != "current-reader-admitted" || readerSchema(t, dir) != RemoteOwnerStateSchema {
				t.Fatal(receipt, err)
			}
			s, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := s.UpdateSnapshot(t.Context(), update.NewProbe())
			if err != nil || !update.SameCoreMetadata(got, original) || !s.Session(t.Context(), legacyToken) || !s.Session(t.Context(), token) {
				t.Fatal("owner, data or sessions changed", err)
			}
			proof := ApplicationUpdateReceipt{ID: r.OperationID, Sequence: r.Release.Sequence, ManifestSHA256: r.Release.ManifestSHA256, EngineSHA256: r.Release.EngineSHA256, Phase: "completed"}
			if _, err = s.ApplicationOwnerCommand(t.Context(), ApplicationOwnerCommand{Action: "finish", ID: r.OperationID, Lease: r.OwnerLease, Receipt: &proof}); err != nil {
				t.Fatal(err)
			}
			s.Close()
			if _, err = ResumeApplicationReaderBootstrap(t.Context(), dir, r.OperationID); err == nil || !strings.Contains(err.Error(), "exact original reader-bootstrap metadata fence required") {
				t.Fatal("completed authority did not refuse the released fence", err)
			}
		})
	}
}

func TestCurrentRemoteAdmissionRejectsHeldAndChangedAuthority(t *testing.T) {
	t.Run("held operation", func(t *testing.T) {
		s, r, _, _ := currentRemoteAdmissionFixture(t)
		op := ApplicationUpdateOperation{ID: update.NewProbe(), Phase: "recovery", OwnerLease: update.NewProbe(), Release: &r.Release}
		raw, _ := json.Marshal(op)
		if _, err := s.db.Exec("UPDATE application_updates SET document=? WHERE id=1", string(raw)); err != nil {
			t.Fatal(err)
		}
		if err := PrepareApplicationReaderBootstrap(t.Context(), s.dir, r); err == nil {
			t.Fatal("unresolved operation replaced")
		}
	})
	for _, field := range []string{"lease", "release", "operation", "engine"} {
		t.Run(field, func(t *testing.T) {
			s, r, _, _ := currentRemoteAdmissionFixture(t)
			if err := PrepareApplicationReaderBootstrap(t.Context(), s.dir, r); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "lease":
				r.OwnerLease = update.NewProbe()
			case "release":
				r.Release.EngineSHA256 = strings.Repeat("f", 64)
			case "operation":
				r.OperationID = update.NewProbe()
			case "engine":
				r.PreviousEngineSHA256 = strings.Repeat("d", 64)
			}
			if err := PrepareApplicationReaderBootstrap(t.Context(), s.dir, r); err == nil {
				t.Fatal("reserved authority replaced", field)
			}
		})
	}
}

func TestCurrentRemoteAdmissionVersionAndTarget(t *testing.T) {
	for _, schema := range []int{33, 35, 36, 38} {
		version, err := ApplicationReaderRequestVersion(schema)
		if err != nil {
			t.Fatal(err)
		}
		target, err := ApplicationReaderTargetSchema(schema)
		if err != nil || schema == 38 && (version != 4 || target != 38) || schema != 38 && (version == 4 || target != 36) {
			t.Fatal(schema, version, target, err)
		}
	}
	for _, schema := range []int{0, 34, 37, 39} {
		if _, err := ApplicationReaderTargetSchema(schema); err == nil {
			t.Fatal("unreviewed format admitted", schema)
		}
	}
}

func TestCurrentRemoteAdmissionRejectsChangedOwnerAndOldRequest(t *testing.T) {
	s, r, _, _ := currentRemoteAdmissionFixture(t)
	old := r
	old.Version = 3
	if err := PrepareApplicationReaderBootstrap(t.Context(), s.dir, old); err == nil {
		t.Fatal("old request admitted current owner state")
	}
	if err := PrepareApplicationReaderBootstrap(t.Context(), s.dir, r); err != nil {
		t.Fatal(err)
	}
	owner, err := s.remoteOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	owner.Devices = append(owner.Devices, tailnet.Identity{UserID: 7, NodeID: "another-device", Login: "owner@example.test"})
	raw, _ := json.Marshal(owner)
	if _, err = s.db.Exec("UPDATE remote_owner SET document=? WHERE id=1", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdatePause(t.Context(), update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: r.Predecessor.Request}); err == nil {
		t.Fatal("changed owner admitted under stale snapshot")
	}
}
