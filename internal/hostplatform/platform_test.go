package hostplatform

import (
	"strings"
	"testing"
)

func TestReleaseAndArchitectureMapping(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, version := range []string{"22.04", "24.04", "26.04", "26.10"} {
			p, e := Lookup("ID=\"ubuntu\"\nVERSION_ID=\""+version+"\"\n", arch)
			if e != nil || p.Codename == "" || p.GTK == "" {
				t.Fatal(version, arch, p, e)
			}
			packages := p.Packages(true)
			if !strings.Contains(strings.Join(packages, " "), "libwebkit2gtk-4.1-0") {
				t.Fatal(packages)
			}
			if strings.Contains(strings.Join(p.Packages(false), " "), "libgtk") {
				t.Fatal("headless installs desktop", p)
			}
		}
	}
	for _, release := range []string{"ID=ubuntu\nVERSION_ID=20.04\n", "ID=ubuntu\nVERSION_ID=99.04\n", "ID=fedora\nVERSION_ID=26.04\n", "ID_LIKE=ubuntu\nVERSION_ID=24.04\n", "ID=ubuntu\nID=debian\nVERSION_ID=24.04\n", "ID=ubuntu\nVERSION_ID=26.04\nVERSION_CODENAME=jammy\n"} {
		if _, e := Lookup(release, "amd64"); e == nil {
			t.Fatal("accepted ambiguous/unknown release", release)
		}
	}
	if _, e := Lookup("ID=ubuntu\nVERSION_ID=24.04\n", "386"); e == nil {
		t.Fatal("accepted wrong CPU")
	}
}
func TestFutureUbuntuRetainsRestrictiveSandbox(t *testing.T) {
	if !RestrictedUbuntu("ID=ubuntu\nVERSION_ID=28.04\n") {
		t.Fatal("unknown future release disabled sandbox")
	}
	if RestrictedUbuntu("ID=ubuntu\nVERSION_ID=22.04\n") {
		t.Fatal("wrong AppArmor dialect on older release")
	}
}

func TestRemoteMappingsKeepRequiredAuthorityBoundary(t *testing.T) {
	if _, e := LookupRemote("ID=ubuntu\nVERSION_ID=22.04\n", "amd64"); e == nil {
		t.Fatal("old sudo/sandbox profile advertised as Remote")
	}
	for _, v := range []string{"24.04", "26.04", "26.10"} {
		if _, e := LookupRemote("ID=ubuntu\nVERSION_ID="+v+"\n", "arm64"); e != nil {
			t.Fatal(v, e)
		}
	}
}
