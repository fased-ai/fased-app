//go:build linux

package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// Real process death complements deterministic error-return cuts. These remain
// private-filesystem fixtures; the separate VM journey supplies installed/UID proof.
func TestInspectionAcceptanceCrashCuts(t *testing.T) {
	base := newInspectionTest(t)
	for _, role := range []string{"owner", "signer"} {
		for _, stage := range []string{"pending", "result"} {
			for _, side := range []string{"state", "anchor"} {
				for _, point := range []string{"partial-head", "published"} {
					t.Run(role+"/"+stage+"/"+side+"/"+point, func(t *testing.T) {
						x := base.clone(t)
						a := x.session(t, role)
						if _, e := a.inspect(context.Background()); e != nil {
							t.Fatal(e)
						}
						before := append([]byte(nil), a.previous...)
						exe, e := os.Executable()
						if e != nil {
							t.Fatal(e)
						}
						cmd := exec.Command(exe, "-test.run=^TestInspectionAcceptanceCrashChild$", "-test.timeout=15s")
						occurrence := 1
						if stage == "result" {
							occurrence = 2
						}
						cmd.Env = append(os.Environ(), "FASED_TEST_CRASH_DIR="+x.dir, "FASED_TEST_CRASH_ROLE="+role, "FASED_TEST_CRASH_SIDE="+side, "FASED_TEST_CRASH_POINT="+point, "FASED_TEST_CRASH_OCCURRENCE="+strconv.Itoa(occurrence), "FASED_TEST_CRASH_PIN="+digest(x.files["catalog.json"]))
						out, e := cmd.CombinedOutput()
						exit, ok := e.(*exec.ExitError)
						if !ok || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
							t.Fatalf("not the actual kill cut: %v %s", e, out)
						}
						reopened, e := newInspectionSession(a.store, false)
						if e == nil {
							c, e := a.store.parse(reopened.previous)
							if e != nil || c.Pending != "" || !c.Historical {
								t.Fatal("crash became authority", e)
							}
							if string(reopened.previous) != string(before) && stage != "result" {
								t.Fatal("pending crash became completion")
							}
						}
						if _, e := newInspectionSession(a.store, true); e == nil {
							t.Fatal("crash state reset by initialization")
						}
					})
				}
			}
		}
	}
}
func TestInspectionAcceptanceCrashChild(t *testing.T) {
	dir := os.Getenv("FASED_TEST_CRASH_DIR")
	if dir == "" {
		return
	}
	if os.Getuid() == 0 || os.Geteuid() == 0 {
		t.Fatal("fixture child refuses root")
	}
	role := os.Getenv("FASED_TEST_CRASH_ROLE")
	in, e := openInspectionInputs(filepath.Join(dir, "inputs"), os.Getenv("FASED_TEST_CRASH_PIN"), role, os.Getuid())
	if e != nil {
		t.Fatal(e)
	}
	store, e := openInspectionStore(filepath.Join(dir, role+"-state"), filepath.Join(dir, role+"-anchor"), in)
	if e != nil {
		t.Fatal(e)
	}
	defer store.close()
	session, e := newInspectionSession(store, false)
	if e != nil {
		t.Fatal(e)
	}
	f := store.state
	if os.Getenv("FASED_TEST_CRASH_SIDE") == "anchor" {
		f = store.anchor
	}
	wanted, _ := strconv.Atoi(os.Getenv("FASED_TEST_CRASH_OCCURRENCE"))
	calls := 0
	f.fault = func(phase string) error {
		if phase == os.Getenv("FASED_TEST_CRASH_POINT") {
			calls++
			if calls == wanted {
				return syscall.Kill(os.Getpid(), syscall.SIGKILL)
			}
		}
		return nil
	}
	if _, e := session.inspect(context.Background()); e != nil {
		t.Fatal(e)
	}
	t.Fatal("crash cut not reached")
}
