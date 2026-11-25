//go:build linux

package custodyinstall

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

// PackagePendingInstance recognizes an interrupted fresh installation without
// selecting, changing or deleting it. Unclassified state remains a refusal.
func PackagePendingInstance(owner int, location, name string) (string, error) {
	if !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".pending") {
		return "", errors.New("not a pending package instance")
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, "."), ".pending")
	root, err := update.PackageInstanceRoot(owner, location, id)
	if err != nil || name != "."+id+".pending" {
		return "", errors.New("invalid pending package identity")
	}
	stage := filepath.Join(filepath.Dir(root), name)
	for dir := stage; ; dir = filepath.Dir(dir) {
		info, e := os.Lstat(dir)
		if e != nil || !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm()&0022 != 0 {
			return "", errors.New("unprotected pending package directory")
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != 0 || dir == stage && info.Mode().Perm() != 0755 {
			return "", errors.New("foreign pending package directory")
		}
		if dir == "/" {
			break
		}
	}
	state, err := update.PackageMaintenanceRoot(owner, location, id)
	if err != nil {
		return "", err
	}
	f, err := update.OpenProtected(filepath.Join(state, "provision.json"), 32<<10, 0644)
	if err != nil {
		return "", errors.New("pending package intent is unavailable")
	}
	b, readErr := io.ReadAll(io.LimitReader(f, (32<<10)+1))
	if err = errors.Join(readErr, f.Close()); err != nil || len(b) > 32<<10 {
		return "", errors.New("pending package intent is unavailable")
	}
	var intent packageProvisionIntent
	receipt, receiptErr := maintenance.ReadPackageCandidateReceipt(owner, location, id)
	authority, authorityErr := maintenance.ReadPackageAuthority(owner, location, id)
	if update.StrictJSON(b, &intent) != nil || receiptErr != nil || authorityErr != nil || !pendingPackageProof(owner, location, id, intent, receipt, authority) {
		return "", errors.New("pending package identity differs; preserve it")
	}
	return id, nil
}

func pendingPackageProof(owner int, location, id string, intent packageProvisionIntent, receipt maintenance.CandidateReceipt, authority maintenance.Authority) bool {
	i := intent.Input
	return intent.Version == 1 && intent.Owner == owner && intent.Location == location && intent.Instance == id &&
		receipt.Validate() == nil && receipt.Owner == owner && receipt.Scope == "fresh" && receipt.PacketSHA256 == receipt.ReleaseSHA256 &&
		authority.Owner == owner && authority.Profile == location && authority.ImageSHA256 == receipt.InstallerSHA256 &&
		i.installerDigest() == receipt.InstallerSHA256 && i.ReleaseSHA256 == receipt.ReleaseSHA256 && i.Sequence == receipt.Sequence && i.Version == receipt.ProductVersion &&
		i.Core.File == "fased-app" && i.Core.SHA256 == receipt.EngineSHA256 && i.Core.Size > 0 && i.Signer.File == "fased-signer" && i.Signer.Size > 0 && validHex(i.Signer.SHA256, 32) &&
		validHex(i.Source, 20) && validHex(i.ProvenanceSHA256, 32) && validListen(i.Listen) &&
		validLocation(location, i.PublicOrigin) && (location != "remote" || i.PublicOrigin != "") && validPendingDistribution(i)
}

func validPendingDistribution(i PackageProvisionInput) bool {
	if i.Bundle != nil {
		b := i.Bundle
		return i.Native.InstallerSHA256 == "" && (b.Archive.File == "fased-app-linux-amd64.tar.gz" || b.Archive.File == "fased-app-linux-arm64.tar.gz") &&
			b.Archive.Size > 0 && b.Archive.Size <= 128<<20 && validHex(b.Archive.SHA256, 32) && validHex(b.ManifestSHA256, 32)
	}
	return (i.Native.Family == "apt" && (i.Native.Artifact.File == "fased-app-linux-amd64.deb" || i.Native.Artifact.File == "fased-app-linux-arm64.deb") ||
		i.Native.Family == "dnf" && (i.Native.Artifact.File == "fased-app-linux-amd64.rpm" || i.Native.Artifact.File == "fased-app-linux-arm64.rpm")) &&
		i.Native.Artifact.Size > 0 && validHex(i.Native.Artifact.SHA256, 32)
}
