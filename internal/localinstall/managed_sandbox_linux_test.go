//go:build linux

package localinstall

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestManagedSandboxActualReadOnlyMount(t *testing.T) {
	if os.Getenv("FASED_MARKER_KERNEL_CHILD") == "1" {
		if os.Getuid() == 0 {
			t.Fatal("fixture must never execute project code as root")
		}
		fmt.Println("KernelFixtureStarted")
		root, data := os.Getenv("FASED_MARKER_ROOT"), os.Getenv("FASED_MARKER_DATA")
		if e := CheckSandbox(root, data); e == nil {
			t.Fatal("root-owned file unexpectedly satisfied the owner-writable probe")
		}
		if e := CheckManagedSandbox(root, data); e != nil {
			t.Fatal(e)
		}
		return
	}
	base, e := filepath.Abs("../../.evidence/p3-managed")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(base, 0700); e != nil {
		t.Fatal(e)
	}
	fixture, e := os.MkdirTemp(base, "marker-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(fixture)
	root, data := fixture+"/app", fixture+"/state"
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(data, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(root+"/app-readonly-probe", nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("/usr/bin/true", root+"/"+definitionName); e != nil {
		t.Fatal(e)
	}
	bwrap, e := exec.LookPath("bwrap")
	if e != nil {
		t.Skip("kernel namespace fixture requires OS bubblewrap; actual managed services remain a CI gate")
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	command := exec.Command(bwrap, "--unshare-user", "--uid", strconv.Itoa(os.Getuid()), "--gid", strconv.Itoa(os.Getgid()), "--unshare-pid", "--cap-drop", "ALL", "--ro-bind", "/", "/", "--bind", data, data, "--proc", "/proc", "--dev", "/dev", "--new-session", "--", executable, "-test.run=^TestManagedSandboxActualReadOnlyMount$")
	command.Env = append(os.Environ(), "FASED_MARKER_KERNEL_CHILD=1", "FASED_MARKER_ROOT="+root, "FASED_MARKER_DATA="+data)
	output, e := command.CombinedOutput()
	if we := os.WriteFile(base+"/readonly-marker-kernel.log", output, 0600); we != nil {
		t.Fatal(we)
	}
	if e != nil && !bytes.Contains(output, []byte("KernelFixtureStarted")) {
		t.Skipf("OS denied disposable namespace before project execution: %s", output)
	}
	if e != nil {
		t.Fatalf("managed marker failed actual read-only namespace: %s", output)
	}
}
