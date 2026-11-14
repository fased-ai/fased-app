//go:build linux

package enrollment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
)

func preparedRecoveryFixture(t *testing.T) (*runtimeHost, Source, Options, journal, string) {
	t.Helper()
	base := t.TempDir()
	parent, dir := filepath.Join(base, "source"), filepath.Join(base, "journal")
	for _, path := range []string{parent, dir, filepath.Join(parent, "data")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	lock, err := core.LockInstance(filepath.Join(dir, "controller.lock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	s := Source{Profile: "local", Data: filepath.Join(parent, "data"), Listen: "127.0.0.1:7332", ExpectedInstanceID: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), UID: os.Getuid(), GID: os.Getgid(), Running: true, Autostart: true}
	s.Validate = func(context.Context) error { return nil }
	s.Suspend = func(context.Context) error { return nil }
	s.Restore = func(context.Context, bool, bool) error { return nil }
	o := Options{NativeOwnerAccess: true, Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.ControlledCustodyProfile, Network: "devnet", PrepareHost: true, Disposable: true}
	n, err := realNode(s.Data)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Phase: "prepared", Source: record(s), Node: n, CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64), Network: o.Network, NativeOwnerAccess: true, Mode: o.Mode, Profile: o.Profile, Provenance: o.Provenance}
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(dir, ".pending-123")
	if err = os.WriteFile(pending, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return &runtimeHost{parent: f, path: parent, base: "data", state: filepath.Join(dir, "state.json")}, s, o, j, pending
}

func TestPreparedRecoveryContinuesOriginalControllerOperation(t *testing.T) {
	h, s, o, original, pending := preparedRecoveryFixture(t)
	before, err := os.ReadFile(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverPreparedOwnerJournal(t.Context(), h, s, o, original.CoreHash, original.SignerHash, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if after, err := os.ReadFile(pending); err != nil || string(after) != string(before) {
		t.Fatal("original prepared file was not preserved", err)
	}
	saved, err := readOwnerJournal(h.state)
	if err != nil || saved != original {
		t.Fatal("original prepared operation changed", err)
	}
	stop := errors.New("observed original prepare")
	var calls int
	op := operations{owner: original.Owner, load: func() (journal, error) { return readOwnerJournal(h.state) },
		prepare: func(_ context.Context, got journal, _ Options) (custodyinstall.Manifest, error) {
			calls++
			if got != original {
				t.Fatal("controller selected a different operation")
			}
			return custodyinstall.Manifest{}, stop
		}}
	if _, err = run(t.Context(), s, o, original.CoreHash, original.SignerHash, op); !errors.Is(err, stop) || calls != 1 {
		t.Fatal("original controller did not reach the same prepare entry", err, calls)
	}
	if err = recoverPreparedOwnerJournal(t.Context(), h, s, o, original.CoreHash, original.SignerHash, func() error { return nil }); err != nil {
		t.Fatal("completed publication must be idempotent", err)
	}
	next := original
	next.Phase = "suspending"
	next.Target = custodyinstall.Manifest{Version: 3, ID: original.ID, Owner: original.Owner, Enrollment: true,
		NativeOwnerAccess: true, Location: "local", Listen: s.Listen, Network: original.Network,
		Mode: original.Mode, Profile: original.Profile, Provenance: original.Provenance,
		CoreHash: original.CoreHash, SignerHash: original.SignerHash,
		App: custodyinstall.Identity{UID: original.Owner + 1}, Signer: custodyinstall.Identity{UID: original.Owner + 2}}
	if err = writeOwnerJournal(h.state, next); err != nil {
		t.Fatal(err)
	}
	if got, err := readOwnerJournal(h.state); err != nil || got != next {
		t.Fatal("original prepared witness blocked the same operation's next phase", err)
	}
	if after, err := os.ReadFile(pending); err != nil || string(after) != string(before) {
		t.Fatal("prepared witness changed after advancing", err)
	}
}

func TestPreparedRecoveryAcceptsOwnerReadOnlyPending(t *testing.T) {
	h, s, o, j, pending := preparedRecoveryFixture(t)
	if err := os.Chmod(pending, 0400); err != nil {
		t.Fatal(err)
	}
	if err := recoverPreparedOwnerJournal(t.Context(), h, s, o, j.CoreHash, j.SignerHash, func() error { return nil }); err != nil {
		t.Fatal("owner-readable record from restrictive umask was rejected", err)
	}
	if _, err := readOwnerJournal(h.state); err != nil {
		t.Fatal("recovered operation was not readable", err)
	}
}

func TestPreparedRemoteRecoveryPublishesOnlyVerifiedOriginal(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "refuse-unverified", true: "resume-original"}[trusted], func(t *testing.T) {
			h, s, o, original, pending := preparedRecoveryFixture(t)
			s.Profile = "remote"
			s.PublicOrigin = "https://controlled.owner.ts.net"
			o.NativeOwnerAccess = false
			original.Source = record(s)
			original.NativeOwnerAccess = false
			raw, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pending, raw, 0600); err != nil {
				t.Fatal(err)
			}
			called := 0
			verify := func() error {
				called++
				if !trusted {
					return errors.New("original Remote instance not verified")
				}
				return nil
			}
			err = recoverPreparedOwnerJournal(t.Context(), h, s, o, original.CoreHash, original.SignerHash, verify)
			if trusted {
				if err != nil || called != 1 {
					t.Fatal("verified Remote operation was not recovered", err, called)
				}
				if got, err := readOwnerJournal(h.state); err != nil || got != original {
					t.Fatal("Remote operation identity changed", err)
				}
			} else if err == nil || called != 1 {
				t.Fatal("unverified Remote operation was accepted", err, called)
			} else if _, err := os.Lstat(h.state); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unverified Remote state was published", err)
			}
			if after, err := os.ReadFile(pending); err != nil || string(after) != string(raw) {
				t.Fatal("original Remote pending record changed", err)
			}
		})
	}
}

func TestRemotePreparedStatusRequiresOriginalInstance(t *testing.T) {
	s := Source{Profile: "remote", PublicOrigin: "https://controlled.owner.ts.net", ExpectedInstanceID: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	owner := os.Getuid()
	status := map[string]any{"installed": true, "active": true, "unit": fmt.Sprintf("fased-vps-%d.service", owner), "state": "active", "instanceId": s.ExpectedInstanceID, "url": s.PublicOrigin}
	encode := func(action string, complete bool) []byte {
		t.Helper()
		payload, err := json.Marshal(status)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(maintenance.Response{Version: 1, Action: action, Complete: complete, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := verifyRemotePreparedStatus(encode("remote-enrollment-prepared-status", true), s, owner); err != nil {
		t.Fatal("original protected Remote instance rejected", err)
	}
	for _, key := range []string{"active", "instanceId", "url", "unit", "state"} {
		original := status[key]
		if key == "active" {
			status[key] = false
		} else {
			status[key] = "foreign"
		}
		if err := verifyRemotePreparedStatus(encode("remote-enrollment-prepared-status", true), s, owner); err == nil {
			t.Fatalf("changed %s accepted", key)
		}
		status[key] = original
	}
	if err := verifyRemotePreparedStatus(encode("remote-status", true), s, owner); err == nil {
		t.Fatal("foreign protected action accepted")
	}
	if err := verifyRemotePreparedStatus(encode("remote-enrollment-prepared-status", false), s, owner); err == nil {
		t.Fatal("incomplete protected response accepted")
	}
}

func TestPreparedRecoveryPreservesTwoSameOperationWrites(t *testing.T) {
	h, s, o, original, witness := preparedRecoveryFixture(t)
	if err := writeOwnerJournal(h.state, original); err != nil {
		t.Fatal(err)
	}
	next := original
	next.Target = custodyinstall.Manifest{Version: 3, ID: original.ID, Owner: original.Owner, Enrollment: true,
		NativeOwnerAccess: true, Location: "local", Listen: s.Listen, Network: original.Network,
		Mode: original.Mode, Profile: original.Profile, Provenance: original.Provenance,
		CoreHash: original.CoreHash, SignerHash: original.SignerHash,
		App: custodyinstall.Identity{UID: original.Owner + 1}, Signer: custodyinstall.Identity{UID: original.Owner + 2}}
	second := filepath.Join(filepath.Dir(h.state), ".pending-456")
	raw, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(second, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(witness)
	if err != nil {
		t.Fatal(err)
	}
	if err = recoverPreparedOwnerJournal(t.Context(), h, s, o, original.CoreHash, original.SignerHash, func() error { return nil }); err != nil {
		t.Fatal("matching interrupted write was not recoverable", err)
	}
	stop := errors.New("observed same target")
	var prepares int
	op := operations{owner: original.Owner, load: func() (journal, error) { return readOwnerJournal(h.state) },
		save: func(j journal) error { return writeOwnerJournal(h.state, j) },
		checkPending: func(j journal, target *custodyinstall.Manifest, digest string) error {
			return checkPendingReplay(h.state, j, target, digest)
		},
		prepare: func(_ context.Context, got journal, _ Options) (custodyinstall.Manifest, error) {
			prepares++
			if got.ID != original.ID {
				t.Fatal("replacement operation selected")
			}
			return next.Target, nil
		}, target: func(m custodyinstall.Manifest) error {
			if m != next.Target {
				return errState
			}
			return nil
		}, emptySigner: func(context.Context, custodyinstall.Manifest) error { return stop }}
	if _, err = run(t.Context(), s, o, original.CoreHash, original.SignerHash, op); !errors.Is(err, stop) || prepares != 1 {
		t.Fatal("original controller did not safely replay its target preparation", err, prepares)
	}
	if got, err := readOwnerJournal(h.state); err != nil || got != next {
		t.Fatal("committed original operation could not be read with the retained writes", err)
	}
	if after, err := os.ReadFile(witness); err != nil || string(after) != string(before) {
		t.Fatal("original witness changed", err)
	}
	if after, err := os.ReadFile(second); err != nil || string(after) != string(raw) {
		t.Fatal("interrupted write changed", err)
	}
}

func TestPreparedRecoveryPreservesTwoRemoteWrites(t *testing.T) {
	h, s, o, original, witness := preparedRecoveryFixture(t)
	s.Profile = "remote"
	s.PublicOrigin = "https://controlled.owner.ts.net"
	o.NativeOwnerAccess = false
	original.Source = record(s)
	original.NativeOwnerAccess = false
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(witness, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeOwnerJournal(h.state, original); err != nil {
		t.Fatal(err)
	}
	next := original
	next.Target = custodyinstall.Manifest{Version: 3, ID: original.ID, Owner: original.Owner, Enrollment: true,
		Location: "remote", Listen: s.Listen, PublicOrigin: s.PublicOrigin, Network: original.Network,
		Mode: original.Mode, Profile: original.Profile, Provenance: original.Provenance,
		CoreHash: original.CoreHash, SignerHash: original.SignerHash,
		App: custodyinstall.Identity{UID: original.Owner + 1}, Signer: custodyinstall.Identity{UID: original.Owner + 2}}
	second := filepath.Join(filepath.Dir(h.state), ".pending-456")
	secondRaw, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, secondRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := recoverPreparedOwnerJournal(t.Context(), h, s, o, original.CoreHash, original.SignerHash, func() error { return nil }); err != nil {
		t.Fatal("matching Remote writes could not resume the committed operation", err)
	}
	if err := checkPendingReplay(h.state, original, &next.Target, ""); err != nil {
		t.Fatal("matching Remote replay target was rejected", err)
	}
	changed := next.Target
	changed.PublicOrigin = "https://foreign.owner.ts.net"
	if err := checkPendingReplay(h.state, original, &changed, ""); err == nil {
		t.Fatal("foreign Remote target was accepted")
	}
	stop := errors.New("observed Remote target")
	op := operations{owner: original.Owner, load: func() (journal, error) { return readOwnerJournal(h.state) },
		save: func(j journal) error { return writeOwnerJournal(h.state, j) },
		checkPending: func(j journal, target *custodyinstall.Manifest, digest string) error {
			return checkPendingReplay(h.state, j, target, digest)
		},
		prepare: func(_ context.Context, got journal, _ Options) (custodyinstall.Manifest, error) {
			if got != original {
				t.Fatal("Remote controller selected a different operation")
			}
			return next.Target, nil
		}, target: func(m custodyinstall.Manifest) error {
			if m != next.Target {
				return errState
			}
			return nil
		}, emptySigner: func(context.Context, custodyinstall.Manifest) error { return stop }}
	if _, err := run(t.Context(), s, o, original.CoreHash, original.SignerHash, op); !errors.Is(err, stop) {
		t.Fatal("Remote controller did not replay the same prepared target", err)
	}
	if got, err := readOwnerJournal(h.state); err != nil || got != next {
		t.Fatal("Remote controller did not preserve the committed operation", err)
	}
	if after, err := os.ReadFile(second); err != nil || string(after) != string(secondRaw) {
		t.Fatal("Remote pending write changed", err)
	}
	foreign := next
	foreign.Source.PublicOrigin = "https://foreign.owner.ts.net"
	foreign.Target.PublicOrigin = foreign.Source.PublicOrigin
	foreignRaw, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, foreignRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerJournal(h.state); err == nil {
		t.Fatal("foreign Remote source was accepted")
	}
	suspended := next
	suspended.Phase = "suspended"
	if err := writeOwnerJournal(h.state, suspended); err != nil {
		t.Fatal(err)
	}
	importing := suspended
	importing.Phase = "importing"
	importing.Digest = strings.Repeat("d", 64)
	importRaw, err := json.Marshal(importing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, importRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPendingReplay(h.state, suspended, nil, importing.Digest); err != nil {
		t.Fatal("matching Remote export digest was rejected", err)
	}
	if err := checkPendingReplay(h.state, suspended, nil, strings.Repeat("e", 64)); err == nil {
		t.Fatal("changed Remote export digest was accepted")
	}
	lost := suspended
	lost.Phase = "source-lock-lost"
	lost.Digest = importing.Digest
	if err := writeOwnerJournal(h.state, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerJournal(h.state); err == nil {
		t.Fatal("Remote recovery continued after source lease loss")
	}
}

func TestPreparedRecoveryRefusesChangedReplayTarget(t *testing.T) {
	h, s, o, original, _ := preparedRecoveryFixture(t)
	if err := writeOwnerJournal(h.state, original); err != nil {
		t.Fatal(err)
	}
	next := original
	next.Target = custodyinstall.Manifest{Version: 3, ID: original.ID, Owner: original.Owner, Enrollment: true,
		NativeOwnerAccess: true, Location: "local", Listen: s.Listen, Network: original.Network,
		Mode: original.Mode, Profile: original.Profile, Provenance: original.Provenance,
		CoreHash: original.CoreHash, SignerHash: original.SignerHash,
		App: custodyinstall.Identity{UID: original.Owner + 1}, Signer: custodyinstall.Identity{UID: original.Owner + 2}}
	raw, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(h.state), ".pending-456"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	changed := next.Target
	changed.App.UID++
	op := operations{owner: original.Owner, load: func() (journal, error) { return readOwnerJournal(h.state) },
		save: func(j journal) error { return writeOwnerJournal(h.state, j) },
		checkPending: func(j journal, target *custodyinstall.Manifest, digest string) error {
			return checkPendingReplay(h.state, j, target, digest)
		}, prepare: func(context.Context, journal, Options) (custodyinstall.Manifest, error) { return changed, nil }}
	if _, err = run(t.Context(), s, o, original.CoreHash, original.SignerHash, op); !errors.Is(err, errState) {
		t.Fatal("different prepared target was not refused", err)
	}
	if got, err := readOwnerJournal(h.state); err != nil || got != original {
		t.Fatal("committed operation changed on refusal", err)
	}
}

func TestPreparedRecoveryRefusesConflictingPendingTargets(t *testing.T) {
	h, _, _, original, _ := preparedRecoveryFixture(t)
	if err := writeOwnerJournal(h.state, original); err != nil {
		t.Fatal(err)
	}
	next := original
	next.Target = custodyinstall.Manifest{Version: 3, ID: original.ID, Owner: original.Owner, Enrollment: true,
		NativeOwnerAccess: true, Location: "local", Listen: original.Source.Listen, Network: original.Network,
		Mode: original.Mode, Profile: original.Profile, Provenance: original.Provenance,
		CoreHash: original.CoreHash, SignerHash: original.SignerHash,
		App: custodyinstall.Identity{UID: original.Owner + 1}, Signer: custodyinstall.Identity{UID: original.Owner + 2}}
	for i, uid := range []int{original.Owner + 1, original.Owner + 3} {
		next.Target.App.UID = uid
		raw, err := json.Marshal(next)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(filepath.Dir(h.state), []string{".pending-456", ".pending-789"}[i])
		if err = os.WriteFile(name, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := readOwnerJournal(h.state); !errors.Is(err, errState) {
		t.Fatal("conflicting pending targets were accepted", err)
	}
}

func TestPreparedRecoveryRefusesChangedReplayDigest(t *testing.T) {
	h, s, o, original, witness := preparedRecoveryFixture(t)
	image := fixtureImage(t)
	s.ExpectedInstanceID = image.InstanceID
	original.Source.ExpectedInstanceID = image.InstanceID
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(witness, raw, 0600); err != nil {
		t.Fatal(err)
	}
	current := original
	current.Phase = "suspended"
	current.Target = custodyinstall.Manifest{Version: 3, ID: original.ID, Owner: original.Owner, Enrollment: true,
		NativeOwnerAccess: true, Location: "local", Listen: s.Listen, Network: original.Network,
		Mode: original.Mode, Profile: original.Profile, Provenance: original.Provenance,
		CoreHash: original.CoreHash, SignerHash: original.SignerHash,
		App: custodyinstall.Identity{UID: original.Owner + 1}, Signer: custodyinstall.Identity{UID: original.Owner + 2}}
	if err = writeOwnerJournal(h.state, current); err != nil {
		t.Fatal(err)
	}
	next := current
	next.Phase, next.Digest = "importing", strings.Repeat("d", 64)
	if next.Digest == core.EnrollmentImageDigest(image) {
		t.Fatal("fixture digest did not differ")
	}
	raw, err = json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(h.state), ".pending-456"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	imported := false
	op := operations{owner: original.Owner, load: func() (journal, error) { return readOwnerJournal(h.state) },
		save: func(j journal) error { return writeOwnerJournal(h.state, j) }, node: realNode,
		checkPending: func(j journal, target *custodyinstall.Manifest, digest string) error {
			return checkPendingReplay(h.state, j, target, digest)
		}, target: func(custodyinstall.Manifest) error { return nil },
		emptySigner: func(context.Context, custodyinstall.Manifest) error { return nil },
		export: func(context.Context, custodyinstall.Manifest, sourceRecord) (*imageLease, error) {
			return &imageLease{image: image, close: func() error { return nil }}, nil
		}, importImage: func(context.Context, journal, core.EnrollmentImage) error { imported = true; return nil }}
	if _, err = run(t.Context(), s, o, original.CoreHash, original.SignerHash, op); !errors.Is(err, errState) || imported {
		t.Fatal("changed source digest reached import", err, imported)
	}
	if got, err := readOwnerJournal(h.state); err != nil || got != current {
		t.Fatal("committed operation changed on refusal", err)
	}
}

func TestPreparedRecoveryRefusesUnsafeSecondWrite(t *testing.T) {
	for _, change := range []string{"foreign-operation", "future-phase", "lost-lock", "partial"} {
		t.Run(change, func(t *testing.T) {
			h, s, o, original, witness := preparedRecoveryFixture(t)
			if err := writeOwnerJournal(h.state, original); err != nil {
				t.Fatal(err)
			}
			candidate := original
			switch change {
			case "foreign-operation":
				candidate.ID = strings.Repeat("d", 32)
			case "future-phase":
				candidate.Phase = "fencing"
			case "lost-lock":
				candidate.Phase = "source-lock-lost"
			}
			raw, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if change == "partial" {
				raw = []byte(`{"phase":`)
			}
			second := filepath.Join(filepath.Dir(h.state), ".pending-456")
			if err = os.WriteFile(second, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err = recoverPreparedOwnerJournal(t.Context(), h, s, o, original.CoreHash, original.SignerHash, func() error { return nil }); err == nil {
				t.Fatal("unsafe interrupted write accepted")
			}
			if after, err := os.ReadFile(second); err != nil || string(after) != string(raw) {
				t.Fatal("unsafe write was not preserved", err)
			}
			if _, err = os.Stat(witness); err != nil {
				t.Fatal("original witness was not preserved", err)
			}
		})
	}
}

func TestPreparedRecoveryRefusesConcurrentFileChange(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "in-place", true: "replacement"}[replacement], func(t *testing.T) {
			h, s, o, j, pending := preparedRecoveryFixture(t)
			original, err := os.ReadFile(pending)
			if err != nil {
				t.Fatal(err)
			}
			verify := func() error {
				if replacement {
					if err := os.Rename(pending, pending+".original"); err != nil {
						return err
					}
				}
				return os.WriteFile(pending, []byte("owner change"), 0600)
			}
			if err = recoverPreparedOwnerJournal(t.Context(), h, s, o, j.CoreHash, j.SignerHash, verify); err == nil {
				t.Fatal("concurrent pending-file change accepted")
			}
			if _, err = os.Lstat(h.state); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("state published after concurrent change", err)
			}
			if replacement {
				if retained, e := os.ReadFile(pending + ".original"); e != nil || string(retained) != string(original) {
					t.Fatal("original replaced file was not preserved", e)
				}
			}
		})
	}
}

func TestPreparedRecoveryRefusesChangedInputsWithoutMutation(t *testing.T) {
	for _, tc := range []string{"core", "signer", "instance", "node", "authority", "another-pending", "state", "symlink"} {
		t.Run(tc, func(t *testing.T) {
			h, s, o, j, pending := preparedRecoveryFixture(t)
			coreHash, signerHash := j.CoreHash, j.SignerHash
			verify := func() error { return nil }
			switch tc {
			case "core":
				coreHash = strings.Repeat("d", 64)
			case "signer":
				signerHash = strings.Repeat("e", 64)
			case "instance":
				s.ExpectedInstanceID = base64.RawURLEncoding.EncodeToString(bytes32(1))
			case "node":
				if err := os.Rename(s.Data, s.Data+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(s.Data, 0700); err != nil {
					t.Fatal(err)
				}
			case "authority":
				verify = func() error { return errors.New("foreign protected state") }
			case "another-pending":
				if err := os.WriteFile(filepath.Join(filepath.Dir(pending), ".pending-456"), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			case "state":
				if err := os.WriteFile(h.state, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(pending, pending+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(pending+"-original", pending); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(pending)
			if err != nil {
				t.Fatal(err)
			}
			if err = recoverPreparedOwnerJournal(t.Context(), h, s, o, coreHash, signerHash, verify); err == nil {
				t.Fatal("changed input accepted")
			}
			after, err := os.ReadFile(pending)
			if err != nil || string(after) != string(before) {
				t.Fatal("pending record changed", err)
			}
			if tc != "state" {
				if _, err = os.Lstat(h.state); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unexpected state was published", err)
				}
			}
		})
	}
}

func bytes32(b byte) []byte {
	v := make([]byte, 32)
	for i := range v {
		v[i] = b
	}
	return v
}
