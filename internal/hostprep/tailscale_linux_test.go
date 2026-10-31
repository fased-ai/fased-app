//go:build linux

package hostprep

import "testing"

func TestTailscalePreparationToolAdmission(t *testing.T) {
	for _, a := range [][]string{{"status", "--json"}, {"debug", "prefs"}, {"login", "--timeout=120s"}, {"set", "--operator=owner"}} {
		if !AllowedTailscalePreparation("owner", a) {
			t.Fatal(a)
		}
	}
	for _, a := range [][]string{{"up", "--reset"}, {"login", "--authkey=secret"}, {"login", "--timeout=0"}, {"logout"}, {"switch", "other"}, {"set", "--operator=other"}, {"set", "--ssh"}, {"serve", "--bg", "http://localhost"}, {"funnel", "443"}, {"status", "--json", "--operator=owner"}} {
		if AllowedTailscalePreparation("owner", a) {
			t.Fatal("unbounded root operation accepted", a)
		}
	}
	for _, name := range []string{"", "root", "--evil", "owner\nroot", "owner/other", "owner=other"} {
		if AllowedTailscalePreparation(name, []string{"status", "--json"}) {
			t.Fatal(name)
		}
	}
}
