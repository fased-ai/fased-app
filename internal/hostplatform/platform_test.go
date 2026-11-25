package hostplatform

import (
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLinuxPackageFamiliesUseSharedLocalPreparation(t *testing.T) {
	for _, tc := range []struct {
		name, release, query, install string
	}{
		{"Debian", "ID=debian\nVERSION_ID=11\n", "/usr/bin/dpkg-query", "/usr/bin/apt-get"},
		{"Ubuntu derivative", "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\nVERSION_ID=22\n", "/usr/bin/dpkg-query", "/usr/bin/apt-get"},
		{"RHEL derivative", "ID=rocky\nID_LIKE=\"rhel fedora\"\nVERSION_ID=9.5\n", "/usr/bin/rpm", "/usr/bin/dnf"},
		{"New Fedora", "ID=fedora\nVERSION_ID=46\n", "/usr/bin/rpm", "/usr/bin/dnf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Lookup(tc.release, "amd64")
			if err != nil {
				t.Fatal(err)
			}
			manager, err := p.PackageManager()
			if err != nil || len(manager.Query) == 0 || len(manager.Install) == 0 || manager.Query[0] != tc.query || manager.Install[0] != tc.install {
				t.Fatalf("wrong family package manager: %+v (%v)", manager, err)
			}
			if p.Remote {
				t.Fatal("family detection alone must not grant unverified Remote authority")
			}
		})
	}
	for _, release := range []string{
		"ID=custom\nID_LIKE=\"debian fedora\"\nVERSION_ID=1\n",
		"ID=custom\nID_LIKE=\"debian;id\"\nVERSION_ID=1\n",
		"ID=custom\nVERSION_ID=1\n",
	} {
		if _, err := Lookup(release, "amd64"); err == nil {
			t.Fatalf("accepted ambiguous or unsupported Linux family: %q", release)
		}
	}
}

func TestFedora45LocalMapping(t *testing.T) {
	for _, version := range []string{"41", "42", "43", "44", "45"} {
		release := "ID=fedora\nVERSION_ID=" + version + "\n"
		for _, arch := range []string{"amd64", "arm64"} {
			p, err := LookupRemote(release, arch)
			if err != nil {
				t.Fatalf("fedora %s %s rejected: %v", version, arch, err)
			}
			if !p.Remote || p.RestrictedNamespaces {
				t.Fatalf("Fedora %s %s wrong Remote/namespace settings: %+v", version, arch, p)
			}
			if got, want := p.Packages(true), []string{"gtk3", "webkit2gtk4.1", "xdg-utils"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("Fedora runtime packages = %v, want %v", got, want)
			}
			manager, err := p.PackageManager()
			if err != nil || manager.PolicyKit != "polkit" || !reflect.DeepEqual(manager.Query, []string{"/usr/bin/rpm", "-q", "--quiet", "--"}) || !reflect.DeepEqual(manager.Install, []string{"/usr/bin/dnf", "install", "--assumeyes", "--setopt=install_weak_deps=False"}) || len(manager.Refresh) != 0 {
				t.Fatalf("wrong Fedora package preparation: %+v (%v)", manager, err)
			}
			src := p.TailscaleSource("/etc/pki/rpm-gpg/RPM-GPG-KEY-fased-tailscale")
			if !strings.Contains(src, "[fased-tailscale-stable]") || !strings.Contains(src, "gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-fased-tailscale") {
				t.Fatalf("unexpected Fedora Tailscale source: %s", src)
			}
		}
	}
	if _, err := LookupRemote("ID=fedora\nVERSION_ID=40\n", "amd64"); err == nil {
		t.Fatal("unverified older Fedora Remote was admitted")
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
	for _, release := range []string{"ID_LIKE=ubuntu\nVERSION_ID=24.04\n", "ID=ubuntu\nID=debian\nVERSION_ID=24.04\n", "ID=ubuntu\nVERSION_ID=26.04\nVERSION_CODENAME=jammy\n"} {
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

func TestKernelNamespaceRestrictionRemainsFailClosed(t *testing.T) {
	p := Platform{ID: "debian", Family: "apt"}
	if got, err := applyNamespaceRestriction(p, []byte("1\n"), nil); err != nil || !got.RestrictedNamespaces {
		t.Fatalf("active host restriction ignored: %+v %v", got, err)
	}
	if got, err := applyNamespaceRestriction(p, []byte("0\n"), nil); err != nil || got.RestrictedNamespaces {
		t.Fatalf("unexpected restriction: %+v %v", got, err)
	}
	p.RestrictedNamespaces = true
	if got, err := applyNamespaceRestriction(p, []byte("0\n"), nil); err != nil || !got.RestrictedNamespaces {
		t.Fatal("existing conservative platform protection weakened")
	}
	for _, value := range []string{"", "unknown", "2"} {
		if _, err := applyNamespaceRestriction(p, []byte(value), nil); err == nil {
			t.Fatalf("unknown kernel restriction accepted: %q", value)
		}
	}
	if _, err := applyNamespaceRestriction(p, nil, os.ErrPermission); err == nil {
		t.Fatal("unreadable kernel restriction accepted")
	}
}

func TestRemoteMappingsKeepRequiredAuthorityBoundary(t *testing.T) {
	if _, e := LookupRemote("ID=ubuntu\nVERSION_ID=22.04\n", "amd64"); e == nil {
		t.Fatal("old sudo/sandbox profile advertised as Remote")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, v := range []string{"24.04", "26.04", "26.10"} {
			if _, e := LookupRemote("ID=ubuntu\nVERSION_ID="+v+"\n", arch); e != nil {
				t.Fatal(arch, v, e)
			}
		}
		for _, tc := range []struct {
			version, codename string
		}{
			{"12", "bookworm"},
			{"13", "trixie"},
		} {
			p, e := LookupRemote("ID=debian\nVERSION_ID="+tc.version+"\n", arch)
			if e != nil {
				t.Fatalf("debian %s %s rejected: %v", tc.version, arch, e)
			}
			src := p.TailscaleSource("/usr/share/keyrings/fased-tailscale.gpg")
			if !strings.Contains(src, "stable/debian "+tc.codename+" main") {
				t.Fatalf("unexpected Debian Tailscale source: %s", src)
			}
		}
		for _, v := range []string{"41", "42", "43", "44", "45"} {
			if _, e := LookupRemote("ID=fedora\nVERSION_ID="+v+"\n", arch); e != nil {
				t.Fatalf("fedora %s %s rejected: %v", v, arch, e)
			}
		}
	}
}

func TestSoakLiveHostPlatform(t *testing.T) {
	if os.Getenv("FASED_SOAK_LIVE_HOST") != "1" {
		t.Skip("live host platform soak disabled unless FASED_SOAK_LIVE_HOST=1")
	}
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		t.Fatal(err)
	}
	cur, err := Current()
	if err != nil {
		t.Fatalf("Current() failed: %v", err)
	}
	remote, err := LookupRemote(string(raw), runtime.GOARCH)
	if err != nil {
		t.Fatalf("LookupRemote() failed on live host %s/%s: %v", cur.ID, cur.Version, err)
	}
	mgr, err := cur.PackageManager()
	if err != nil {
		t.Fatalf("PackageManager() failed: %v", err)
	}
	for _, bin := range []string{mgr.Query[0], mgr.Install[0]} {
		info, err := os.Stat(bin)
		if err != nil {
			t.Fatalf("missing package manager binary %s: %v", bin, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("package manager binary %s is not executable: %v", bin, info.Mode())
		}
	}
	out := struct {
		ID                   string   `json:"id"`
		Version              string   `json:"version"`
		Codename             string   `json:"codename"`
		Family               string   `json:"family"`
		Arch                 string   `json:"arch"`
		Remote               bool     `json:"remote"`
		RestrictedNamespaces bool     `json:"restrictedNamespaces"`
		QueryBin             string   `json:"queryBin"`
		InstallBin           string   `json:"installBin"`
		RuntimePackages      []string `json:"runtimePackages"`
	}{
		ID:                   remote.ID,
		Version:              remote.Version,
		Codename:             remote.Codename,
		Family:               remote.Family,
		Arch:                 runtime.GOARCH,
		Remote:               remote.Remote,
		RestrictedNamespaces: cur.RestrictedNamespaces,
		QueryBin:             mgr.Query[0],
		InstallBin:           mgr.Install[0],
		RuntimePackages:      remote.Packages(true),
	}
	enc, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("SOAK_HOST_PLATFORM:%s", enc)
}
