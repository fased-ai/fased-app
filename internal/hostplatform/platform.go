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
	Family               string   `json:"-"`
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
		if k != "ID" && k != "ID_LIKE" && k != "VERSION_ID" && k != "VERSION_CODENAME" {
			continue
		}
		if _, exists := values[k]; exists {
			return nil, errors.New("ambiguous OS release metadata")
		}
		v = strings.Trim(v, "\"'")
		if strings.ContainsAny(v, "\x00\r\t$`\\;") || k != "ID_LIKE" && strings.Contains(v, " ") {
			return nil, errors.New("invalid OS release metadata")
		}
		for _, part := range strings.Fields(v) {
			for _, c := range part {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
					return nil, errors.New("invalid OS release metadata")
				}
			}
		}
		values[k] = v
	}
	return values, nil
}

// packageFamily selects only fixed package-manager commands. An ID_LIKE value
// is data, not a command, and conflicting families are refused.
func packageFamily(values map[string]string) (string, error) {
	if values["ID"] == "" || values["VERSION_ID"] == "" {
		return "", errors.New("Linux OS identity and version required")
	}
	family := ""
	for _, id := range append([]string{values["ID"]}, strings.Fields(values["ID_LIKE"])...) {
		next := ""
		switch id {
		case "ubuntu", "debian":
			next = "apt"
		case "fedora", "rhel", "centos", "rocky", "almalinux":
			next = "dnf"
		}
		if next != "" && family != "" && next != family {
			return "", errors.New("conflicting Linux package families")
		}
		if next != "" {
			family = next
		}
	}
	if family == "" {
		return "", errors.New("Linux package family is not supported")
	}
	return family, nil
}

func Lookup(release, arch string) (Platform, error) {
	if arch != "amd64" && arch != "arm64" {
		return Platform{}, errors.New("no verified package for this CPU architecture")
	}
	values, e := releaseValues(release)
	if e != nil {
		return Platform{}, e
	}
	family, e := packageFamily(values)
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
			p.Family = family
			return p, nil
		}
	}
	gtk := "libgtk-3-0"
	if family == "dnf" {
		gtk = "gtk3"
	}
	// A family mapping chooses prerequisites only. Remote security profiles
	// remain separately admitted, and installed acceptance is host-specific.
	return Platform{ID: values["ID"], Version: values["VERSION_ID"], Codename: values["VERSION_CODENAME"], Family: family, GTK: gtk, RestrictedNamespaces: RestrictedUbuntu(release)}, nil
}
func Current() (Platform, error) {
	b, e := os.ReadFile("/etc/os-release")
	if e != nil {
		return Platform{}, e
	}
	p, err := Lookup(string(b), runtime.GOARCH)
	if err != nil {
		return Platform{}, err
	}
	// Some Linux systems restrict unprivileged user namespaces independently of
	// distribution name. Preserve the protected sandbox when that knob is on.
	value, e := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	return applyNamespaceRestriction(p, value, e)
}

func applyNamespaceRestriction(p Platform, value []byte, e error) (Platform, error) {
	if e == nil {
		switch strings.TrimSpace(string(value)) {
		case "0":
		case "1":
			p.RestrictedNamespaces = true
		default:
			return Platform{}, errors.New("unknown user namespace restriction")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return Platform{}, fmt.Errorf("cannot inspect user namespace restriction: %w", e)
	}
	return p, nil
}

// RestrictedUbuntu is conservative across OS upgrades: an unknown new Ubuntu
// must not accidentally select the weaker fallback while awaiting a catalog update.
func RestrictedUbuntu(release string) bool {
	v, e := releaseValues(release)
	if e != nil {
		return true
	}
	if v["ID"] == "" {
		return false
	}
	if v["ID"] != "ubuntu" {
		// Ubuntu derivatives need the conservative policy until the actual
		// namespace capability has been verified on their installed host.
		for _, id := range strings.Fields(v["ID_LIKE"]) {
			if id == "ubuntu" {
				return true
			}
		}
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
		if p.Family == "dnf" {
			webkit = "webkit2gtk4.1"
		}
		packages = append(packages, p.GTK, webkit, "xdg-utils")
	}
	return packages
}

func (p Platform) PackageManager() (PackageManager, error) {
	switch p.Family {
	case "apt":
		return PackageManager{
			Query: []string{"/usr/bin/dpkg-query", "-W", "-f=${Status}"}, InstalledOutput: "install ok installed",
			Refresh:            []string{"/usr/bin/apt-get", "update"},
			Install:            []string{"/usr/bin/apt-get", "install", "--yes", "--no-remove", "--no-install-recommends"},
			MaintenanceInstall: []string{"/usr/bin/apt-get", "install", "--yes", "--no-install-recommends"}, PolicyKit: "polkitd",
		}, nil
	case "dnf":
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
