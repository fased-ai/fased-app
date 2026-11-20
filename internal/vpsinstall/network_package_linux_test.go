//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func packageNetworkFixtureManifest(ownerUID, appUID int, id string) Manifest {
	root := "/var/lib/fased-app-package/" + "1000" + "/remote/" + id
	if ownerUID != 1000 {
		root = "/var/lib/fased-app-package/2000/remote/" + id
	}
	unit := "fased-package-" + "1000" + "-remote-" + id + "-app.service"
	if ownerUID != 1000 {
		unit = "fased-package-2000-remote-" + id + "-app.service"
	}
	return Manifest{
		Version:      1,
		ID:           id,
		Phase:        packageRemotePhase,
		OwnerUID:     ownerUID,
		OwnerGID:     ownerUID,
		AppUID:       appUID,
		AppGID:       appUID,
		AppUser:      "fs-pkg-a",
		Root:         root,
		Data:         root + "/app-state",
		Core:         root + "/fased-app",
		Config:       root + "/install.json",
		Unit:         unit,
		UnitPath:     "/etc/systemd/system/" + unit,
		Listen:       "127.0.0.1:17490",
		PublicOrigin: "https://controlled.owner.ts.net",
		CoreSHA256:   strings.Repeat("a", 64),
	}
}

func TestPackageNetworkValidationAndOwnership(t *testing.T) {
	id := strings.Repeat("b", 32)
	m := packageNetworkFixtureManifest(1000, 1005, id)
	if err := m.validatePackageNetwork(); err != nil {
		t.Fatal("valid package network manifest rejected", err)
	}
	if err := ownedByNetwork(hostOps{uid: 1000, euid: 1000}, m); err != nil {
		t.Fatal("matching package owner rejected", err)
	}
	if err := ownedByNetwork(hostOps{uid: 2000, euid: 2000}, m); err == nil {
		t.Fatal("other OS owner accepted")
	}
	if err := ownedByNetwork(hostOps{uid: 0, euid: 0}, m); err == nil {
		t.Fatal("root UID accepted")
	}

	cases := []struct {
		name string
		edit func(*Manifest)
	}{
		{"wrong phase", func(v *Manifest) { v.Phase = "installed" }},
		{"bad id", func(v *Manifest) { v.ID = "not-hex" }},
		{"bad digest", func(v *Manifest) { v.CoreSHA256 = "bad" }},
		{"equal app owner", func(v *Manifest) { v.AppUID = v.OwnerUID }},
		{"foreign root", func(v *Manifest) { v.Root = "/var/lib/fased-app-vps-1000" }},
		{"foreign data", func(v *Manifest) { v.Data = "/tmp/app-state" }},
		{"foreign unit", func(v *Manifest) { v.Unit = "fased-vps-1000.service" }},
		{"missing origin", func(v *Manifest) { v.PublicOrigin = "" }},
		{"non-loopback listen", func(v *Manifest) { v.Listen = "0.0.0.0:443" }},
	}
	for _, tc := range cases {
		bad := packageNetworkFixtureManifest(1000, 1005, id)
		tc.edit(&bad)
		if isPackageRemoteManifest(bad) && bad.validatePackageNetwork() == nil {
			t.Fatalf("tampered manifest accepted for %s", tc.name)
		}
	}
}

func TestPackageNetworkBeginConfirmAndRollbackLifecycle(t *testing.T) {
	id := strings.Repeat("c", 32)
	host, n, _ := networkFixture(t)
	m := packageNetworkFixtureManifest(1000, 1005, id)
	host.base.files[ConfigPath(1000)] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0644, Data: encoded(m)}
	packageChecked := false
	n.packageCheck = func(got Manifest) error {
		if got != m {
			return os.ErrInvalid
		}
		packageChecked = true
		return nil
	}

	pending, err := n.begin(context.Background(), m, NetworkApproval{PrivateUI: true, ConsoleRecovery: true})
	if err != nil {
		t.Fatal("package network begin failed", err)
	}
	if !packageChecked || pending.Phase != "pending" {
		t.Fatalf("unexpected begin result: checked=%v phase=%q", packageChecked, pending.Phase)
	}
	a, err := n.load(m)
	if err != nil {
		t.Fatal("load pending attempt", err)
	}
	rules := string(host.base.files[networkAttemptRoot(m, a.ID)+"/rules.nft"].Data)
	for _, want := range []string{
		`table inet ` + networkTable(m, a),
		`policy drop;`,
		`iifname "tailscale0" tcp dport { 22, 443 } accept`,
		`tcp dport 22 counter drop`,
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("missing firewall rule fragment %q in %s", want, rules)
		}
	}
	sshDropIn := string(host.base.files[networkSSH(m, a)].Data)
	for _, want := range []string{"PermitRootLogin no", "PasswordAuthentication no", "KbdInteractiveAuthentication no"} {
		if !strings.Contains(sshDropIn, want) {
			t.Fatalf("missing ssh restriction %q", want)
		}
	}
	if !bytes.Equal(host.tables["foreign"], []byte("foreign firewall bytes")) {
		t.Fatal("foreign firewall table modified")
	}

	host.proof.SessionID = "second-private-connection"
	committed, err := n.confirm(context.Background(), m, pending.AttemptID)
	if err != nil || committed.Phase != "committed" {
		t.Fatalf("package network confirm failed: status=%+v err=%v", committed, err)
	}

	rolledBack, err := n.rollback(context.Background(), m)
	if err != nil || rolledBack.Phase != "rolled-back" {
		t.Fatalf("package network rollback failed: status=%+v err=%v", rolledBack, err)
	}
	if _, ok := host.tables[networkTable(m, a)]; ok {
		t.Fatal("owned firewall table remained after rollback")
	}
	if !bytes.Equal(host.tables["foreign"], []byte("foreign firewall bytes")) {
		t.Fatal("foreign firewall table lost during rollback")
	}
}
