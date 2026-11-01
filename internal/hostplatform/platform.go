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
	ID                   string `json:"id"`
	Version              string `json:"version"`
	Codename             string `json:"codename"`
	GTK                  string `json:"gtk"`
	Remote               bool   `json:"remote"`
	RestrictedNamespaces bool   `json:"restrictedNamespaces"`
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
		packages = append(packages, p.GTK, "libwebkit2gtk-4.1-0", "xdg-utils")
	}
	return packages
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
