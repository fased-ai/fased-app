package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

// This handoff is scoped to the publisher's approved public v0.1.6
// predecessor and retained build 44550182096, artifact 14542082707.
const (
	legacyLocalPacket  = "bc64b3c658c36e6e62ec667eb8cdb4936d1c74132dd06c2d448faf241c72eae5"
	legacyLocalCore    = "349a8145b7d00ece82faad5e95db0325505378d54253e9b4af19275662b7abbc"
	localHandoffTarget = "f009a6ad164403688e062e384f23c4ead7d994f1f94d339b8fdf4ee82ffb4725"
)

// LocalHandoffPreflight records verified inputs for review. It is not an
// update approval, a journal reservation, or permission to move state.
type LocalHandoffPreflight struct {
	Owner                int    `json:"ownerUid"`
	LegacyPacketSHA256   string `json:"legacyPacketSha256"`
	LegacyManifestSHA256 string `json:"legacyManifestSha256"`
	TargetPacketSHA256   string `json:"targetPacketSha256"`
	TargetBundleSHA256   string `json:"targetBundleSha256"`
	ApplyAllowed         bool   `json:"applyAllowed"`
}

func VerifyLegacyLocalHandoff(o Options, predecessor, target string, paths localinstall.Paths) (LocalHandoffPreflight, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() {
		return LocalHandoffPreflight{}, errors.New("ordinary Linux amd64 owner required")
	}
	if _, err := verifyHandoffPacket(o, predecessor, legacyLocalPacket, false); err != nil {
		return LocalHandoffPreflight{}, fmt.Errorf("legacy predecessor: %w", err)
	}
	report, err := verifyHandoffPacket(o, target, localHandoffTarget, true)
	if err != nil {
		return LocalHandoffPreflight{}, fmt.Errorf("retained target: %w", err)
	}
	legacy, err := inspectLegacyLocalHandoff(paths, legacyLocalCore)
	if err != nil {
		return LocalHandoffPreflight{}, err
	}
	return LocalHandoffPreflight{
		Owner: os.Getuid(), LegacyPacketSHA256: legacyLocalPacket,
		LegacyManifestSHA256: legacy, TargetPacketSHA256: localHandoffTarget,
		TargetBundleSHA256: report.BundleSHA256, ApplyAllowed: false,
	}, nil
}

func verifyHandoffPacket(o Options, directory, expected string, linuxBundle bool) (Report, error) {
	if !exactHandoffDirectory(directory) {
		return Report{}, errors.New("original absolute candidate directory required")
	}
	packet := filepath.Join(directory, AttestedPacketFile)
	raw, err := update.ReadDocument(packet)
	var identity AttestedPacket
	if err != nil || digest(raw) != expected || update.StrictJSON(raw, &identity) != nil {
		return Report{}, errors.New("exact approved packet required")
	}
	report, err := VerifyAttestedCandidate(o, directory)
	if err != nil {
		return Report{}, err
	}
	raw, err = update.ReadDocument(packet)
	if err != nil || digest(raw) != expected || !report.ProvenanceVerified || !report.BundleVerified ||
		report.ArchiveSHA256 != identity.Archive.SHA256 || report.BundleSHA256 != identity.BundleSHA256 ||
		report.ProvenanceSHA256 != identity.Release.Provenance.SHA256 ||
		report.LinuxBundleVerified != linuxBundle || report.InstallerInvoked {
		return Report{}, errors.New("approved packet or verification scope changed")
	}
	return report, nil
}

func exactHandoffDirectory(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func inspectLegacyLocalHandoff(paths localinstall.Paths, coreSHA256 string) (string, error) {
	if !exactHandoffDirectory(paths.Root) || !exactHandoffDirectory(paths.Data) {
		return "", errors.New("original absolute legacy installation and data directories required")
	}
	m, err := localinstall.Load(paths)
	if err != nil {
		return "", err
	}
	if m.Phase != "ready" || m.Version != "0.1.6" || m.Location != "local" ||
		m.PublicOrigin != "" || m.ProviderFixture || m.ApplicationSequence != 1000007 ||
		m.Files[localinstall.EngineName()] != coreSHA256 || m.Files["fased-signer"] != "" {
		return "", errors.New("exact app-only v0.1.6 Local installation required")
	}
	if err := localinstall.ValidateEnrollment(m); err != nil {
		return "", err
	}
	if _, err := os.Lstat(filepath.Join(paths.Data, "enrollment.json")); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("existing custody enrollment must be preserved")
	}
	if err := localinstall.RequireNoApplicationUpdateFiles(paths); err != nil {
		return "", err
	}
	return localinstall.ManifestSHA256(m), nil
}
