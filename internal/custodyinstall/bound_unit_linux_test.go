//go:build linux

package custodyinstall

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestBoundUnitWriteBindsBytesAndOwnedDestination(t *testing.T) {
	id := strings.Repeat("a", 32)
	stage := "/etc/systemd/system/.fased-custody-1900-app.service-" + id + ".pending"
	body := []byte("[Service]\nUser=991\nNoNewPrivileges=yes\n")
	a, err := BoundUnitWriteArgs(1900, stage, body)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BoundUnitWriteArgs(1900, stage, append([]byte(nil), append(body, 'x')...))
	if err != nil || slices.Equal(a, b) {
		t.Fatal("different service bytes were not bound to the OS command", err)
	}
	if !AllowedBoundUnitWrite(1900, body, a) || AllowedBoundUnitWrite(1900, append(body, 'x'), a) {
		t.Fatal("bound service write accepted substituted input")
	}
	decoded, err := exec.Command(a[10], a[11:]...).Output()
	if err != nil || !bytes.Equal(decoded, body) {
		t.Fatal("the fixed OS writer changed the approved service bytes", err)
	}
	changed := append([]string(nil), a...)
	changed[len(changed)-1] += "x"
	if AllowedBoundUnitWrite(1900, body, changed) {
		t.Fatal("altered privileged command was accepted")
	}
	for _, p := range []string{
		"/etc/systemd/system/.fased-custody-1000-app.service-" + id + ".pending",
		"/etc/systemd/system/.sshd.service-" + id + ".pending",
		"/etc/systemd/system/../.fased-custody-1900-app.service-" + id + ".pending",
		"/var/lib/.fased-app-vps-1900-" + id + ".pending/.fased-vps-1900.service-" + strings.Repeat("b", 32) + ".pending",
	} {
		if _, err := BoundUnitWriteArgs(1900, p, body); err == nil {
			t.Fatal("foreign destination accepted", p)
		}
	}
	vps := "/var/lib/.fased-app-vps-1900-" + id + ".pending/.fased-vps-1900.service-" + id + ".pending"
	if args, err := BoundUnitWriteArgs(1900, vps, body); err != nil || !AllowedBoundUnitWrite(1900, body, args) {
		t.Fatal("owned Remote installer stage refused", err)
	}
	if _, err := BoundUnitWriteArgs(1900, stage, make([]byte, 8193)); err == nil {
		t.Fatal("oversized service content accepted")
	}
}

func TestBoundPolicyWriteBindsBytesAndOwner(t *testing.T) {
	id := strings.Repeat("a", 32)
	stage := "/etc/sudoers.d/.fased-remote-proof-1900-" + id + ".pending"
	body := []byte("# fixed policy\n#1900 ALL=(root) NOPASSWD: /usr/bin/true\n")
	args, err := BoundPolicyWriteArgs(1900, stage, body)
	if err != nil || !AllowedBoundPolicyWrite(1900, body, args) {
		t.Fatal("owned policy write refused", err)
	}
	if args[6] != "--property=UMask=0227" {
		t.Fatal("sudo policy staging is not restricted to mode 0440")
	}
	decoded, err := exec.Command(args[10], args[11:]...).Output()
	if err != nil || !bytes.Equal(decoded, body) {
		t.Fatal("fixed OS writer changed the approved policy bytes", err)
	}
	if AllowedBoundPolicyWrite(1900, append(bytes.Clone(body), 'x'), args) {
		t.Fatal("substituted sudo policy accepted")
	}
	if _, err := BoundPolicyWriteArgs(1900, strings.Replace(stage, "1900", "1000", 1), body); err == nil {
		t.Fatal("foreign sudo policy destination accepted")
	}
}
