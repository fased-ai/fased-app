//go:build linux

package custodyinstall

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeOwnerPolicyGrantsOnlyFixedAppClientAndStarts(t *testing.T) {
	m := Manifest{Owner: 1000, Root: Root(1000), App: Identity{UID: 991, GID: 991}}
	p := string(nativePolicy(m))
	for _, bad := range []string{"NOPASSWD: ALL", "fased-signer", "*", "bash", "--password", "--request-stdin"} {
		if strings.Contains(p, bad) {
			t.Fatal("broad native grant", bad)
		}
	}
	if !strings.Contains(p, "#1000 ALL=(root)") || !strings.Contains(p, "--reuid 991") || !strings.Contains(p, "native-client") || !strings.Contains(p, "systemctl start "+Unit(1000, "signer")) {
		t.Fatal("native grant is incomplete")
	}
	if _, err := os.Stat("/usr/sbin/visudo"); err != nil {
		t.Skip("host has no sudo policy validator; syntax acceptance remains open")
	}
	path := filepath.Join(t.TempDir(), "policy")
	if err := os.WriteFile(path, []byte(p), 0600); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("/usr/sbin/visudo", "-cf", path).CombinedOutput(); err != nil {
		t.Fatal("invalid exact native policy", err, string(b))
	}
}
