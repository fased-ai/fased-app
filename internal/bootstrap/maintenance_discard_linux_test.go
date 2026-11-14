//go:build linux

package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/bundle"
	"golang.org/x/sys/unix"
)

func TestDiscardMaintenancePreservesReplacedDirectory(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "replaced"}[replace], func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "acquisition")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "bundle"), []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			p := &Prepared{root: root, directoryInfo: info, options: Options{Directory: path, protectedOwner: os.Getuid()}}
			if replace {
				if err = os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(path, "foreign"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = p.DiscardMaintenance()
			if replace {
				defer root.Close()
				if err == nil {
					t.Fatal("replaced acquisition was deleted")
				}
				if b, e := os.ReadFile(filepath.Join(path, "foreign")); e != nil || string(b) != "preserve" {
					t.Fatal("replacement changed", e)
				}
				if _, e := os.Stat(filepath.Join(path+".original", "bundle")); e != nil {
					t.Fatal("original changed", e)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, e := os.Lstat(path); !os.IsNotExist(e) {
					t.Fatal("owned acquisition was not removed", e)
				}
			}
		})
	}
}

func TestResumeMaintenanceReverifiesOriginalAcquisitionOffline(t *testing.T) {
	for _, profile := range []string{"development", "attested"} {
		t.Run(profile, func(t *testing.T) {
			if os.Getuid() <= 0 {
				t.Skip("ordinary test user required")
			}
			var o Options
			if profile == "development" {
				f := newFirstFixture(t)
				o = f.options()
			} else {
				_, opts, _ := attestedFixture(t, "staged")
				o = opts
			}
			p, err := Prepare(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			want := p.Report()
			if err = p.Close(); err != nil {
				t.Fatal(err)
			}
			o.protectedOwner = os.Getuid()
			resumed, err := resumeMaintenance(t.Context(), o)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.Report() != want {
				t.Fatal("original release identity changed")
			}
			if !resumed.MatchesMaintenanceRelease(want.Sequence, want.DescriptorSHA256) || resumed.MatchesMaintenanceRelease(want.Sequence+1, want.DescriptorSHA256) || resumed.MatchesMaintenanceRelease(want.Sequence, strings.Repeat("0", 64)) {
				t.Fatal("original release identity was not bound")
			}
			if err = resumed.Close(); err != nil {
				t.Fatal(err)
			}
			engine, _ := bundle.Names()
			if err = os.WriteFile(filepath.Join(o.Directory, "bundle", engine), []byte("changed"), 0700); err != nil {
				t.Fatal(err)
			}
			if p, err = resumeMaintenance(t.Context(), o); err == nil {
				p.Close()
				t.Fatal("changed release bytes accepted")
			}
		})
	}
}

func TestDiscardIncompleteMaintenancePreservesCompletedAndNeighbor(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "incomplete", true: "completion-record"}[completed], func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "custody-operation")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "partial"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			neighbor := filepath.Join(parent, "owner-data")
			if err := os.WriteFile(neighbor, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if completed {
				if err := os.WriteFile(filepath.Join(dir, "bootstrap.json"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := discardIncompleteMaintenance(parent, dir)
			if completed {
				if err == nil {
					t.Fatal("completed acquisition removed")
				}
				if _, err = os.Stat(filepath.Join(dir, "bootstrap.json")); err != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if b, err := os.ReadFile(neighbor); err != nil || string(b) != "preserve" {
				t.Fatal("neighbor changed", err)
			}
		})
	}
}

func TestBootstrapCompletionPublishesWholeRecordWithoutReplacement(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "foreign"}[existing], func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if existing {
				if err = os.WriteFile(filepath.Join(dir, "bootstrap.json"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = writeBootstrapCompletion(root, []byte(`{"complete":true}`))
			if existing && err == nil {
				t.Fatal("foreign marker replaced")
			}
			if !existing && err != nil {
				t.Fatal(err)
			}
			want := `{"complete":true}`
			if existing {
				want = "preserve"
			}
			if got, e := os.ReadFile(filepath.Join(dir, "bootstrap.json")); e != nil || string(got) != want {
				t.Fatal("marker changed", e)
			}
		})
	}
}

func TestBootstrapMetadataUsesPrivateModeUnderRestrictiveUmask(t *testing.T) {
	if os.Getenv("FASED_TEST_BOOTSTRAP_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBootstrapMetadataUsesPrivateModeUnderRestrictiveUmask$")
		cmd.Env = append(os.Environ(), "FASED_TEST_BOOTSTRAP_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated umask check: %v: %s", err, out)
		}
		return
	}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	old := unix.Umask(0400)
	defer unix.Umask(old)
	if err = writeNew(root, "metadata.json", []byte("verified")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "metadata.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("metadata mode: %v %v", info, err)
	}
}

func TestPrepareUnderRestrictiveOwnerUmask(t *testing.T) {
	if os.Getenv("FASED_TEST_PREPARE_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPrepareUnderRestrictiveOwnerUmask$")
		cmd.Env = append(os.Environ(), "FASED_TEST_PREPARE_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated umask check: %v: %s", err, out)
		}
		return
	}
	f := newFirstFixture(t)
	o := f.options()
	old := unix.Umask(0227)
	defer unix.Umask(old)
	p, err := Prepare(t.Context(), o)
	if err != nil {
		t.Fatal("verified acquisition refused restrictive owner umask", err)
	}
	defer p.Close()
	if !p.Report().BundleVerified {
		t.Fatal("acquisition did not verify the bundle")
	}
}
