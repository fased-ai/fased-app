package hostplatform

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestFedora45LocalMapping(t *testing.T) {
	release := "ID=fedora\nVERSION_ID=45\n"
	p, err := Lookup(release, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if p.Remote || p.RestrictedNamespaces {
		t.Fatal("Fedora must use the existing Local systemd sandbox and not be admitted as Remote", p)
	}
	if got, want := p.Packages(true), []string{"gtk3", "webkit2gtk4.1", "xdg-utils"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Fedora runtime packages = %v, want %v", got, want)
	}
	manager, err := p.PackageManager()
	if err != nil || manager.PolicyKit != "polkit" || !reflect.DeepEqual(manager.Query, []string{"/usr/bin/rpm", "-q", "--quiet", "--"}) || !reflect.DeepEqual(manager.Install, []string{"/usr/bin/dnf", "install", "--assumeyes", "--setopt=install_weak_deps=False"}) || len(manager.Refresh) != 0 {
		t.Fatalf("wrong Fedora package preparation: %+v (%v)", manager, err)
	}
	if _, err = LookupRemote(release, "amd64"); err == nil {
		t.Fatal("unverified Fedora Remote was admitted")
	}
	if _, err = Lookup(release, "arm64"); err == nil {
		t.Fatal("unverified Fedora architecture was admitted")
	}
	if _, err = Lookup("ID=fedora\nVERSION_ID=46\n", "amd64"); err == nil {
		t.Fatal("unknown Fedora release was admitted")
	}
}

func TestCurrentFedora45Host(t *testing.T) {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		t.Fatal(err)
	}
	v, err := releaseValues(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if v["ID"] != "fedora" || v["VERSION_ID"] != "45" {
		t.Skip("this check requires the actual Fedora 45 host")
	}
	p, err := Current()
	if err != nil || p.ID != "fedora" || p.Version != "45" {
		t.Fatalf("actual Fedora host rejected: platform=%+v err=%v", p, err)
	}
}

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
