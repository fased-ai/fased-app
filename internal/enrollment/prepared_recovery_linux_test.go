//go:build linux

package enrollment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
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
