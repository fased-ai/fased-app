package update

import (
	"encoding/hex"
	"errors"
	"path"
	"strconv"
	"strings"
)

const (
	MaxDesktopDocument         = 64 << 10
	MaxDesktopAssets           = 32
	desktopReleaseDownloadBase = "https://github.com/fased-ai/fased-app/releases/download/"
)

// DesktopAsset describes one downloadable binary, installer, or checksum file
// listed in desktop-release-manifest.json on a vX.Y.Z-desktop companion release.
type DesktopAsset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Kind   string `json:"kind"`
}

// DesktopManifest binds the companion desktop and multi-OS CLI release inventory
// to an exact semver product version and immutable release tag.
type DesktopManifest struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Version       string                  `json:"version"`
	ReleaseTag    string                  `json:"releaseTag"`
	Source        string                  `json:"source"`
	Signed        bool                    `json:"signed"`
	Assets        map[string]DesktopAsset `json:"assets"`
}

type semverTriplet struct {
	major, minor, patch uint64
}

func parseSemverTriplet(raw string) (semverTriplet, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return semverTriplet{}, errors.New("canonical X.Y.Z semver version required")
	}
	var vals [3]uint64
	for i, part := range parts {
		v, err := strconv.ParseUint(part, 10, 20)
		if err != nil || v >= 1000000 || strconv.FormatUint(v, 10) != part {
			return semverTriplet{}, errors.New("canonical bounded semver component required")
		}
		vals[i] = v
	}
	return semverTriplet{vals[0], vals[1], vals[2]}, nil
}

func (a semverTriplet) compare(b semverTriplet) int {
	for _, pair := range [][2]uint64{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	return 0
}

func sourceCommitOK(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 20 && hex.EncodeToString(b) == s
}

func desktopAssetNameOK(name string) bool {
	if len(name) == 0 || len(name) > 128 || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00 \r\n\t") || path.Base(name) != name {
		return false
	}
	return true
}

func validDesktopOS(os string) bool {
	return os == "linux" || os == "darwin" || os == "windows" || os == "all"
}

func validDesktopArch(arch string) bool {
	return arch == "amd64" || arch == "arm64" || arch == "all"
}

func validDesktopKind(kind string) bool {
	return kind == "cli" || kind == "desktop" || kind == "installer" || kind == "checksums"
}

// ParseDesktopManifest validates a serialized desktop-release-manifest.json
// using bounded strict JSON decoding and exact URL, digest, and version bindings.
func ParseDesktopManifest(raw []byte) (DesktopManifest, error) {
	var m DesktopManifest
	if err := StrictJSONBounded(raw, &m, MaxDesktopDocument); err != nil {
		return DesktopManifest{}, err
	}
	if m.SchemaVersion != 1 {
		return DesktopManifest{}, errors.New("unsupported desktop manifest schema version")
	}
	if _, err := parseSemverTriplet(m.Version); err != nil {
		return DesktopManifest{}, err
	}
	if m.ReleaseTag != "v"+m.Version+"-desktop" {
		return DesktopManifest{}, errors.New("desktop manifest releaseTag mismatch")
	}
	if !sourceCommitOK(m.Source) {
		return DesktopManifest{}, errors.New("canonical 40-hex source commit required")
	}
	if len(m.Assets) == 0 || len(m.Assets) > MaxDesktopAssets {
		return DesktopManifest{}, errors.New("desktop manifest requires 1..32 assets")
	}
	wantPrefix := desktopReleaseDownloadBase + m.ReleaseTag + "/"
	for filename, asset := range m.Assets {
		if !desktopAssetNameOK(filename) {
			return DesktopManifest{}, errors.New("invalid desktop manifest asset name")
		}
		if asset.URL != wantPrefix+filename {
			return DesktopManifest{}, errors.New("desktop manifest asset URL mismatch")
		}
		if !hashOK(asset.SHA256) || asset.Bytes <= 0 || asset.Bytes > MaxArtifact {
			return DesktopManifest{}, errors.New("invalid desktop manifest asset checksum or byte size")
		}
		if !validDesktopOS(asset.OS) || !validDesktopArch(asset.Arch) || !validDesktopKind(asset.Kind) {
			return DesktopManifest{}, errors.New("invalid desktop manifest asset metadata")
		}
	}
	return m, nil
}

// DesktopUpdateAvailable reports whether a validated desktop manifest offers
// a strictly newer X.Y.Z product version than currentVersion.
func DesktopUpdateAvailable(currentVersion string, m DesktopManifest) (bool, error) {
	rawManifest, err := parseSemverTriplet(m.Version)
	if err != nil {
		return false, err
	}
	if m.SchemaVersion != 1 || m.ReleaseTag != "v"+m.Version+"-desktop" || len(m.Assets) == 0 {
		return false, errors.New("validated desktop manifest required")
	}
	trimmed := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(currentVersion), "-desktop"), "v")
	cur, err := parseSemverTriplet(trimmed)
	if err != nil {
		return false, err
	}
	return rawManifest.compare(cur) > 0, nil
}
