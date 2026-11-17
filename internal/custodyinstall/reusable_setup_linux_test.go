//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReusableSetupPartialRetriesWithoutDirectRemoval(t *testing.T) {
	owner, id := os.Getuid(), strings.Repeat("a", 32)
	if owner <= 0 {
		t.Skip("ordinary installation owner required")
	}
	target := Root(owner) + "/maintenance/authority.json"
	pending := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"-"+id+".pending")
	body := []byte("exact reviewed setup metadata")
	partial := bytes.Clone(body[:8])
	h := &memoryHost{files: map[string]hostFile{pending: {UID: 0, Mode: 0644, Regular: true, Data: bytes.Clone(partial)}}}
	run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "/usr/bin/rm" {
			return nil, errors.New("direct pending-file deletion is forbidden")
		}
		return h.run(ctx, input, args...)
	}
	if err := publishReusableBound(t.Context(), owner, id, target, body, h.read, run, BoundReusableMetadataWriteArgs); err != nil {
		t.Fatalf("safe retry did not publish exact setup metadata: %v", err)
	}
	if f, err := h.read(pending); err != nil || !bytes.Equal(f.Data, partial) {
		t.Fatalf("original incomplete file was not preserved: %v", err)
	}
	if f, err := h.read(target); err != nil || !known(f, body, 0644) {
		t.Fatalf("retried setup metadata is missing or changed: %v", err)
	}
}

func TestReusableTemporaryPolicyDoesNotGrantDirectPendingRemoval(t *testing.T) {
	m := fixtureManifest()
	for _, slot := range reusableSetupSlotIDs(m.ID) {
		if _, err := BoundReusablePolicyWriteArgs(m.Owner, reusablePolicyCandidate(m, slot), []byte("test")); err != nil {
			t.Fatalf("policy slot %s is not admitted: %v", reusablePolicyCandidate(m, slot), err)
		}
	}
	basePolicy, err := reusableAtomicPolicy(m, "fased_owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("reusable policy bytes: %d", len(basePolicy))
	pin := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	policy, err := draftReusableSetupTemporaryPolicy(m, pin, 1000, "fased_owner", 100, 200, 1893456000)
	if err != nil {
		t.Fatal(err)
	}
	if err = rejectUnguardedSetupPolicy(policy); err != nil {
		t.Fatalf("temporary policy contains an unguarded publication or removal: %v", err)
	}
}

func TestReusableTemporaryPolicyRefusesDirectStagingOperations(t *testing.T) {
	m := fixtureManifest()
	pin := reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("e", 64), GuardSHA256: strings.Repeat("d", 64)}
	policy, err := draftReusableSetupTemporaryPolicy(m, pin, 1000, "fased_owner", 100, 200, 1893456000)
	if err != nil {
		t.Fatal(err)
	}
	if err = rejectUnguardedSetupPolicy(policy); err != nil {
		t.Fatal("temporary authority includes direct staging publication or cleanup", err)
	}
	if admitted, err := reusableSetupTemporaryPolicy(m, pin, 1000, "fased_owner", 100, 200, 1893456000); err == nil || len(admitted) != 0 || !strings.Contains(err.Error(), "guarded staging recovery") {
		t.Fatal("incomplete staging recovery reached the setup policy entry", err)
	}
	for _, command := range []string{"/usr/bin/ln --no-target-directory -- /a /b", "/usr/bin/rm -- /a"} {
		if err = rejectUnguardedSetupPolicy([]byte("fased ALL=(root) NOPASSWD: " + command + "\n")); err == nil {
			t.Fatalf("direct command was not rejected: %s", command)
		}
	}
	if err = rejectUnguardedSetupPolicy([]byte("fased ALL=(root) NOPASSWD: /usr/bin/systemctl start -- example.service\n")); err != nil {
		t.Fatal("known direct-operation check rejected an unrelated fixed command", err)
	}
}

func TestReusableImageCopyRetryPreservesForeignData(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid <= 0 || gid <= 0 {
		t.Skip("ordinary test owner required")
	}
	dir := t.TempDir()
	source, pending := filepath.Join(dir, "approved"), filepath.Join(dir, "pending")
	if err := os.WriteFile(source, []byte("approved image"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0444); err != nil {
		t.Fatal(err)
	}
	trust := func(string) error { return nil }
	for _, tc := range []struct {
		name, data string
		ok         bool
	}{
		{"empty interruption", "", true},
		{"partial interruption", "approved", true},
		{"complete copy", "approved image", true},
		{"foreign copy", "different", false},
		{"oversized copy", "approved image extra", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(pending)
			_ = os.Remove(pending + ".source")
			if err := os.WriteFile(pending, []byte(tc.data), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(pending, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(pending, pending+".source"); err != nil {
				t.Fatal(err)
			}
			err := classifyReusableImageCopyAt(pending, source, int64(len("approved image")), uid, gid, trust)
			if (err == nil) != tc.ok {
				t.Fatalf("unexpected admission: %v", err)
			}
			if got, readErr := os.ReadFile(pending); readErr != nil || string(got) != tc.data {
				t.Fatal("copy data changed", readErr)
			}
		})
	}
}

func TestReusableImageCopyRefusesUnmarkedMatchingPrefix(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid <= 0 || gid <= 0 {
		t.Skip("ordinary test owner required")
	}
	dir := t.TempDir()
	source, pending := filepath.Join(dir, "approved"), filepath.Join(dir, "pending")
	if err := os.WriteFile(source, []byte("approved image"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, []byte("approved"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pending, 0755); err != nil {
		t.Fatal(err)
	}
	if err := classifyReusableImageCopyAt(pending, source, int64(len("approved image")), uid, gid, func(string) error { return nil }); err == nil {
		t.Fatal("unmarked file accepted solely because its bytes match a prefix")
	}
	if err := os.WriteFile(pending+".source", []byte("approved"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pending+".source", 0755); err != nil {
		t.Fatal(err)
	}
	if err := classifyReusableImageCopyAt(pending, source, int64(len("approved image")), uid, gid, func(string) error { return nil }); err == nil {
		t.Fatal("separate marker inode accepted as ownership proof")
	}
	if got, err := os.ReadFile(pending); err != nil || string(got) != "approved" {
		t.Fatal("unmarked input was not preserved", err)
	}
}

func TestReusableImageCopyRefusesUnexpectedMarkedMode(t *testing.T) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid <= 0 || gid <= 0 {
		t.Skip("ordinary test owner required")
	}
	dir := t.TempDir()
	source, pending := filepath.Join(dir, "approved"), filepath.Join(dir, "pending")
	if err := os.WriteFile(source, []byte("approved image"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pending, []byte("approved"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(pending, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(pending, pending+".source"); err != nil {
		t.Fatal(err)
	}
	if err := classifyReusableImageCopyAt(pending, source, int64(len("approved image")), uid, gid, func(string) error { return nil }); err == nil {
		t.Fatal("copy with permissions the writer never creates was accepted")
	}
}

func TestReusableSetupLauncherRequiresApprovedRunningBytes(t *testing.T) {
	path := "/usr/bin/true"
	approved, err := os.ReadFile(path)
	if err != nil {
		t.Skip("root-owned system executable unavailable", err)
	}
	info, err := os.Stat(path)
	if err != nil || executableInfo(info) != nil {
		t.Skip("system executable is not a root-owned immutable image")
	}
	check := func(digest string, size int64) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return verifyReusableSetupLauncherFile(f, digest, size)
	}
	if err := check(sum(approved), int64(len(approved))); err != nil {
		t.Fatal("approved reader refused", err)
	}
	if err := check(sum([]byte("different reader")), int64(len(approved))); err == nil {
		t.Fatal("different reader digest accepted")
	}
	if err := check(sum(approved), int64(len(approved)+1)); err == nil {
		t.Fatal("different reader length accepted")
	}
}

func TestReusableSetupLauncherLocationBindsRunningInode(t *testing.T) {
	path := "/usr/bin/true"
	f, err := os.Open(path)
	if err != nil {
		t.Skip("root-owned system executable unavailable", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || executableInfo(info) != nil || trustedParents(filepath.Dir(path)) != nil {
		t.Skip("protected system executable unavailable")
	}
	if err := verifyReusableSetupLauncherLocation(f, path); err != nil {
		t.Fatalf("matching protected pathname was refused: %v", err)
	}
	other := "/usr/bin/false"
	otherInfo, err := os.Lstat(other)
	if err == nil && executableInfo(otherInfo) == nil && !os.SameFile(info, otherInfo) {
		if err = verifyReusableSetupLauncherLocation(f, other); err == nil {
			t.Fatal("different protected executable was accepted as the running inode")
		}
	}
}

func TestReusableSetupLauncherRejectsOwnerWritableExecutable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Fatal("the maintenance launcher regression must run as an ordinary user")
	}
	path := filepath.Join(t.TempDir(), "reader")
	approved := []byte("same approved bytes in an owner-controlled executable")
	if err := os.WriteFile(path, approved, 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := verifyReusableSetupLauncherFile(f, sum(approved), int64(len(approved))); err == nil {
		t.Fatal("owner-controlled executable was accepted as the trusted successor")
	}
}

func TestReusableOutputRequiresOwnedHardlinkBeforeTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.env")
	if err := os.WriteFile(path, []byte("previous output"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, path+".owner"); err != nil {
		t.Fatal(err)
	}
	if err := verifyReusableOutputAt(path, uint32(os.Getuid()), uint32(os.Getgid())); err != nil {
		t.Fatal("owned output refused", err)
	}
	if err := os.Remove(path + ".owner"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".owner", []byte("foreign"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyReusableOutputAt(path, uint32(os.Getuid()), uint32(os.Getgid())); err == nil {
		t.Fatal("foreign output marker accepted")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "previous output" {
		t.Fatal("unclassified output changed", err)
	}
}
