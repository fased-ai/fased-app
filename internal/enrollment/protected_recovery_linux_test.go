//go:build linux

package enrollment

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"golang.org/x/sys/unix"
)

func TestProtectedFenceRestoresTheExactArchivedSource(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "published"}[published], func(t *testing.T) {
			h, j, archive, a := interruptedFenceFixture(t, published)
			if err := restoreProtectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
				t.Fatal(err)
			}
			n, err := realNode(h.pinned(h.base))
			if err != nil || n != j.Node {
				t.Fatal("restored source identity changed", err)
			}
			if b, err := os.ReadFile(filepath.Join(h.path, h.base, "owner-data")); err != nil || string(b) != "original" {
				t.Fatal("original data changed", err)
			}
			if _, err := os.Lstat(archive); !os.IsNotExist(err) {
				t.Fatal("archive still contains a second source")
			}
		})
	}
}

func TestProtectedFenceRestoresRestrictedInterruptedArchive(t *testing.T) {
	h, j, archive, a := interruptedFenceFixture(t, false)
	pending := filepath.Dir(filepath.Dir(archive))
	if err := os.Chmod(pending, 0700); err != nil {
		t.Fatal(err)
	}
	if err := restoreProtectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
		t.Fatalf("restricted interrupted archive could not be restored: %v", err)
	}
	if n, err := realNode(h.pinned(h.base)); err != nil || n != j.Node {
		t.Fatal("the original source was not restored", err)
	}
}

func TestProtectedFenceRecoveryRefusesBusyOrForeignSource(t *testing.T) {
	for _, mode := range []string{"busy", "foreign-binding"} {
		t.Run(mode, func(t *testing.T) {
			h, j, archive, a := interruptedFenceFixture(t, false)
			if mode == "busy" {
				lock, err := core.LockInstance(filepath.Join(archive, "instance.lock"))
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			} else if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(archive)), "enrollment.json"), []byte(`{}`), 0644); err != nil {
				t.Fatal(err)
			}
			if err := restoreProtectedFence(h, j, os.Getuid(), os.Getgid(), a); err == nil {
				t.Fatal("busy or foreign source was restored")
			}
			if _, err := os.Stat(filepath.Join(archive, "owner-data")); err != nil {
				t.Fatal("original archive changed", err)
			}
			if _, err := os.Lstat(filepath.Join(h.path, h.base)); !os.IsNotExist(err) {
				t.Fatal("source path was replaced on refusal")
			}
		})
	}
}

func TestProtectedFenceUsesPinnedDirectoriesAndCanRetry(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, data string }{{"instance.lock", ""}, {"owner-data", "original"}} {
		if err := os.WriteFile(filepath.Join(source, entry.name), []byte(entry.data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := realNode(source)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := &runtimeHost{parent: f, path: parent, base: "source"}
	a := testFenceAuthority(t, h, j, "")
	if err = a.reserve(h, j); err != nil {
		t.Fatal(err)
	}
	lock, err := core.LockInstance(filepath.Join(source, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = protectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = restoreProtectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
		t.Fatal("interrupted published fence could not restore", err)
	}
	lock, err = core.LockInstance(filepath.Join(source, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err = protectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
		t.Fatal("same source could not be fenced again", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = finishProtectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(source, ".original", "data", "owner-data")); err != nil || string(b) != "original" {
		t.Fatal("the exact original was not preserved", err)
	}
}

func TestProtectedFenceRefusesUnclassifiedOrLinkedDestination(t *testing.T) {
	for _, mode := range []string{"foreign", "symlink", "restricted-foreign"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			source := filepath.Join(parent, "source")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			n, err := realNode(source)
			if err != nil {
				t.Fatal(err)
			}
			j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
			pending := filepath.Join(parent, ".enrollment-"+j.ID)
			outside := t.TempDir()
			if mode == "symlink" {
				if err = os.Symlink(outside, pending); err != nil {
					t.Fatal(err)
				}
			} else {
				perm := os.FileMode(0755)
				if mode == "restricted-foreign" {
					perm = 0700
				}
				if err = os.Mkdir(pending, perm); err != nil {
					t.Fatal(err)
				}
				if err = os.Chmod(pending, perm); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(pending, "foreign"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.Open(parent)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			h := &runtimeHost{parent: f, path: parent, base: "source"}
			a := testFenceAuthority(t, h, j, "")
			if err = a.reserve(h, j); err == nil {
				t.Fatal("foreign destination was accepted")
			}
			if got, err := realNode(source); err != nil || got != n {
				t.Fatal("original source changed on refusal", err)
			}
			if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
				t.Fatal("linked destination was modified", err)
			}
		})
	}
}

func TestProtectedFenceRecoversEmptyRestrictedDirectory(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "instance.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	n, err := realNode(source)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := &runtimeHost{parent: f, path: parent, base: "source"}
	a := testFenceAuthority(t, h, j, "")
	if err = a.reserve(h, j); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(parent, ".enrollment-"+j.ID), 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := core.LockInstance(filepath.Join(source, "instance.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = protectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
		t.Fatalf("empty interrupted directory was not resumed: %v", err)
	}
}

func interruptedFenceFixture(t *testing.T, published bool) (*runtimeHost, journal, string, *protectedFenceAuthority) {
	t.Helper()
	parent := t.TempDir()
	base := "source"
	source := filepath.Join(parent, base)
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ name, data string }{{"instance.lock", ""}, {"owner-data", "original"}} {
		if err := os.WriteFile(filepath.Join(source, entry.name), []byte(entry.data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := realNode(source)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
	pending := filepath.Join(parent, ".enrollment-"+j.ID)
	if err = os.Mkdir(pending, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(pending, 0755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(pending, ".original")
	if err = os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(pending, "enrollment.json"), jsonBytes(fenceFor(j)), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(pending, "enrollment.json"), 0644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(original, "data")
	if err = os.Rename(source, archive); err != nil {
		t.Fatal(err)
	}
	if published {
		if err = os.Rename(pending, source); err != nil {
			t.Fatal(err)
		}
		archive = filepath.Join(source, ".original", "data")
	}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	h := &runtimeHost{parent: f, path: parent, base: base}
	name := ".enrollment-" + j.ID
	if published {
		name = base
	}
	a := testFenceAuthority(t, h, j, name)
	return h, j, archive, a
}

// Simulate protected operation records without running the installer as root.
func testFenceAuthority(t *testing.T, h *runtimeHost, j journal, boundName string) *protectedFenceAuthority {
	t.Helper()
	f, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	a := &protectedFenceAuthority{dir: f, uid: os.Getuid(), gid: os.Getgid()}
	if boundName != "" {
		b, err := a.binding(h, j)
		if err != nil {
			t.Fatal(err)
		}
		if err = publishEnrollmentRecordAt(int(f.Fd()), "fence-reservation.json", j.ID, jsonBytes(b), a.uid, a.gid); err != nil {
			t.Fatal(err)
		}
		dir, err := os.Open(filepath.Join(h.path, boundName))
		if err != nil {
			t.Fatal(err)
		}
		defer dir.Close()
		var st unix.Stat_t
		if err = unix.Fstat(int(dir.Fd()), &st); err != nil {
			t.Fatal(err)
		}
		b.Device, b.Inode = uint64(st.Dev), st.Ino
		if err = publishEnrollmentRecordAt(int(f.Fd()), "fence-inode.json", j.ID, jsonBytes(b), a.uid, a.gid); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func TestProtectedFenceRejectsUnboundPrivilegedSubstitution(t *testing.T) {
	parent := t.TempDir()
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := &runtimeHost{parent: f, path: parent, base: "source"}
	j := journal{Version: 1, ID: strings.Repeat("c", 32), Owner: os.Getuid(), Digest: strings.Repeat("b", 64), Source: sourceRecord{Profile: "local"}, Node: node{UID: os.Getuid() + 1, GID: os.Getgid() + 1}}
	a := testFenceAuthority(t, h, j, "")
	if err = a.reserve(h, j); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(parent, ".enrollment-"+j.ID)
	if err = os.Mkdir(pending, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := os.Open(pending)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// The directory is owned by the simulated privileged manager, not the
	// source owner. Binding it would make root chmod someone else's directory.
	if err = a.bind(h, j, int(d.Fd())); err == nil {
		t.Fatal("unbound privileged directory accepted")
	}
	if st, err := d.Stat(); err != nil || st.Mode().Perm() != 0700 {
		t.Fatal("foreign directory changed", err)
	}
	if _, err = os.Lstat(filepath.Join(a.dir.Name(), "fence-inode.json")); !os.IsNotExist(err) {
		t.Fatal("foreign inode recorded", err)
	}
}

func TestProtectedFenceNormalizesCreatedDirectoryWithRestrictiveUmask(t *testing.T) {
	if os.Getenv("FASED_TEST_FENCE_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProtectedFenceNormalizesCreatedDirectoryWithRestrictiveUmask$")
		cmd.Env = append(os.Environ(), "FASED_TEST_FENCE_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated umask check: %v: %s", err, out)
		}
		return
	}
	old := unix.Umask(0027)
	defer unix.Umask(old)
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "instance.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	n, err := realNode(source)
	if err != nil {
		t.Fatal(err)
	}
	j := journal{Version: 1, ID: strings.Repeat("a", 32), Owner: os.Getuid(), Node: n, Digest: strings.Repeat("b", 64)}
	f, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := &runtimeHost{parent: f, path: parent, base: "source"}
	a := testFenceAuthority(t, h, j, "")
	if err = a.reserve(h, j); err != nil {
		t.Fatal(err)
	}
	if err = protectedFence(h, j, os.Getuid(), os.Getgid(), a); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(source); err != nil || st.Mode().Perm() != 0755 {
		t.Fatalf("published mode: %v %v", st, err)
	}
}

func TestProtectedFenceRejectsReplacedBoundDirectory(t *testing.T) {
	h, j, _, a := interruptedFenceFixture(t, false)
	pending := filepath.Join(h.path, ".enrollment-"+j.ID)
	if err := os.Rename(pending, pending+".preserved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(pending, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := os.Open(pending)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = a.verify(h, j, int(d.Fd())); err == nil {
		t.Fatal("replacement inode accepted")
	}
	if _, err = os.Stat(filepath.Join(pending+".preserved", ".original", "data", "owner-data")); err != nil {
		t.Fatal("original not preserved", err)
	}
}

func TestProtectedPrepareRejectsRequestsOutsideHeldOperation(t *testing.T) {
	j := journal{Owner: 101, ID: strings.Repeat("a", 32), Phase: "prepared", Mode: signer.OrdinaryCustodyMode, Profile: signer.OwnerCustodyProfile, Provenance: signer.OwnerKeyProvenance, Network: "devnet", CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64), Source: sourceRecord{Profile: "local", Listen: "127.0.0.1:7331", ApplicationSequence: 7, ApplicationReleaseSHA256: strings.Repeat("f", 64)}}
	a := maintenance.Authority{Owner: j.Owner, Profile: "local"}
	q := maintenance.CustodyEnrollmentRequest{OperationID: j.ID, Network: j.Network, Listen: j.Source.Listen, CoreSHA256: j.CoreHash, SignerSHA256: j.SignerHash, Sequence: j.Source.ApplicationSequence, ReleaseSHA256: j.Source.ApplicationReleaseSHA256}
	if err := validateProtectedPrepareJournal(a, j, q, false); err != nil {
		t.Fatal(err)
	}
	if validateProtectedPrepareJournal(a, j, q, true) == nil {
		t.Fatal("different signer provenance reserved")
	}
	for _, mutate := range []func(*maintenance.CustodyEnrollmentRequest){
		func(q *maintenance.CustodyEnrollmentRequest) { q.OperationID = strings.Repeat("d", 32) },
		func(q *maintenance.CustodyEnrollmentRequest) { q.Listen = "not-a-listener" },
		func(q *maintenance.CustodyEnrollmentRequest) { q.SignerSHA256 = strings.Repeat("e", 64) },
		func(q *maintenance.CustodyEnrollmentRequest) { q.ReleaseSHA256 = strings.Repeat("e", 64) },
	} {
		changed := q
		mutate(&changed)
		if validateProtectedPrepareJournal(a, j, changed, false) == nil {
			t.Fatal("unrelated acquisition accepted")
		}
	}
}

func TestProtectedFenceRecordRecoversInterruptedUmaskStage(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching-prefix", true: "foreign-content"}[foreign], func(t *testing.T) {
			dir := t.TempDir()
			f, err := os.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			id := strings.Repeat("a", 32)
			data := []byte(`{"version":1,"binding":"original"}`)
			stage := filepath.Join(dir, ".fence-reservation.json-"+id+".pending")
			part := data[:8]
			if foreign {
				part = []byte("foreign")
			}
			if err = os.WriteFile(stage, part, 0640); err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(stage, 0640); err != nil {
				t.Fatal(err)
			}
			err = publishEnrollmentRecordAt(int(f.Fd()), "fence-reservation.json", id, data, os.Getuid(), os.Getgid())
			if foreign {
				if err == nil {
					t.Fatal("foreign stage accepted")
				}
				if got, e := os.ReadFile(stage); e != nil || string(got) != "foreign" {
					t.Fatal("foreign stage changed", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(dir, "fence-reservation.json"))
			if err != nil || string(got) != string(data) {
				t.Fatal("record did not recover", err)
			}
		})
	}
}

func TestProtectedReservationRejectsReleaseSubstitution(t *testing.T) {
	j := journal{ID: strings.Repeat("a", 32), Network: "devnet", CoreHash: strings.Repeat("b", 64), SignerHash: strings.Repeat("c", 64), Source: sourceRecord{Listen: "127.0.0.1:7331", ApplicationSequence: 7, ApplicationReleaseSHA256: strings.Repeat("d", 64)}}
	q := maintenance.CustodyEnrollmentRequest{OperationID: j.ID, Network: j.Network, Listen: j.Source.Listen, CoreSHA256: j.CoreHash, SignerSHA256: j.SignerHash, Sequence: j.Source.ApplicationSequence, ReleaseSHA256: j.Source.ApplicationReleaseSHA256}
	raw := jsonBytes(q)
	if !protectedReservationMatches(j, raw) {
		t.Fatal("original reservation refused")
	}
	j.Source.ApplicationReleaseSHA256 = strings.Repeat("e", 64)
	if protectedReservationMatches(j, raw) {
		t.Fatal("different release accepted after preparation")
	}
}
