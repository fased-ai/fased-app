// Package hostplatform owns reviewed OS prerequisite mappings shared by the
// compiled installer, sandbox and release-stamped shell entry. New releases
// arrive through normal authenticated application/installer updates.
package hostplatform

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

//go:embed platforms.json
var catalog []byte

type Platform struct {
	ID                   string   `json:"id"`
	Version              string   `json:"version"`
	Codename             string   `json:"codename"`
	GTK                  string   `json:"gtk"`
	Architectures        []string `json:"architectures,omitempty"`
	Remote               bool     `json:"remote"`
	RestrictedNamespaces bool     `json:"restrictedNamespaces"`
}

// PackageManager contains only fixed distribution-owned package operations.
// Callers add package names from this package's reviewed platform mapping.
type PackageManager struct {
	Query, Refresh, Install, MaintenanceInstall []string
	InstalledOutput, PolicyKit                  string
}

func releaseValues(text string) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k != "ID" && k != "VERSION_ID" && k != "VERSION_CODENAME" {
			continue
		}
		if _, exists := values[k]; exists {
			return nil, errors.New("ambiguous OS release metadata")
		}
		v = strings.Trim(v, "\"'")
		if strings.ContainsAny(v, "\x00\r\t $`\\;") {
			return nil, errors.New("invalid OS release metadata")
		}
		values[k] = v
	}
	return values, nil
}
func Lookup(release, arch string) (Platform, error) {
	if arch != "amd64" && arch != "arm64" {
		return Platform{}, errors.New("no verified package for this CPU architecture")
	}
	values, e := releaseValues(release)
	if e != nil {
		return Platform{}, e
	}
	var platforms []Platform
	if e = json.Unmarshal(catalog, &platforms); e != nil {
		return Platform{}, e
	}
	for _, p := range platforms {
		if values["ID"] == p.ID && values["VERSION_ID"] == p.Version {
			if code := values["VERSION_CODENAME"]; code != "" && code != p.Codename {
				return Platform{}, errors.New("OS version/codename mismatch")
			}
			if len(p.Architectures) != 0 {
				allowed := false
				for _, supported := range p.Architectures {
					allowed = allowed || supported == arch
				}
				if !allowed {
					return Platform{}, errors.New("no reviewed package for this OS and CPU architecture")
				}
			}
			return p, nil
		}
	}
	return Platform{}, fmt.Errorf("OS %s %s has no reviewed package mapping; update the installer for newly supported releases", values["ID"], values["VERSION_ID"])
}
func Current() (Platform, error) {
	b, e := os.ReadFile("/etc/os-release")
	if e != nil {
		return Platform{}, e
	}
	return Lookup(string(b), runtime.GOARCH)
}

// RestrictedUbuntu is conservative across OS upgrades: an unknown new Ubuntu
// must not accidentally select the weaker fallback while awaiting a catalog update.
func RestrictedUbuntu(release string) bool {
	v, e := releaseValues(release)
	if e != nil {
		return true
	}
	if v["ID"] != "ubuntu" {
		return false
	}
	major, _, _ := strings.Cut(v["VERSION_ID"], ".")
	n, e := strconv.Atoi(major)
	return e != nil || n >= 24
}
func (p Platform) Packages(desktop bool) []string {
	packages := []string{}
	if p.RestrictedNamespaces {
		packages = append(packages, "bubblewrap", "apparmor")
	}
	if desktop {
		webkit := "libwebkit2gtk-4.1-0"
		if p.ID == "fedora" {
			webkit = "webkit2gtk4.1"
		}
		packages = append(packages, p.GTK, webkit, "xdg-utils")
	}
	return packages
}

func (p Platform) PackageManager() (PackageManager, error) {
	switch p.ID {
	case "ubuntu":
		return PackageManager{
			Query: []string{"/usr/bin/dpkg-query", "-W", "-f=${Status}"}, InstalledOutput: "install ok installed",
			Refresh:            []string{"/usr/bin/apt-get", "update"},
			Install:            []string{"/usr/bin/apt-get", "install", "--yes", "--no-remove", "--no-install-recommends"},
			MaintenanceInstall: []string{"/usr/bin/apt-get", "install", "--yes", "--no-install-recommends"}, PolicyKit: "polkitd",
		}, nil
	case "fedora":
		return PackageManager{
			Query:              []string{"/usr/bin/rpm", "-q", "--quiet", "--"},
			Install:            []string{"/usr/bin/dnf", "install", "--assumeyes", "--setopt=install_weak_deps=False"},
			MaintenanceInstall: []string{"/usr/bin/dnf", "install", "--assumeyes", "--setopt=install_weak_deps=False"}, PolicyKit: "polkit",
		}, nil
	default:
		return PackageManager{}, errors.New("unsupported OS package manager")
	}
}
func (p Platform) TailscaleSource(key string) string {
	return "deb [signed-by=" + key + "] https://pkgs.tailscale.com/stable/" + p.ID + " " + p.Codename + " main\n"
}

func LookupRemote(release, arch string) (Platform, error) {
	p, e := Lookup(release, arch)
	if e != nil {
		return p, e
	}
	if !p.Remote {
		return p, errors.New("this release supports Local; isolated Remote updates require Ubuntu24.04 or newer and sudo >=1.9.10")
	}
	return p, nil
}
