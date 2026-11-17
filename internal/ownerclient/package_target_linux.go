//go:build linux

package ownerclient

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// ResumeLocal checks the protected wallet journal before changing the owner
// draft, including when an earlier initialization stopped before dispatch.
func ResumeLocal(root, link string) (signer.OwnerIntent, string, error) {
	intent, origin, err := ParseLink(link)
	if err != nil || origin != "" || intent.Version != 1 {
		return signer.OwnerIntent{}, "", errors.New("the original Local wallet request is required")
	}
	return resume(root, link, func(request signer.OwnerIntent, saved *draft) error {
		path, err := localPackageOwnerTarget(os.Getuid(), request.InstanceID)
		if err != nil || path == "" {
			return errors.New("the protected Local wallet is unavailable")
		}
		m, err := custodyinstall.LoadPackageInstance(os.Getuid(), filepath.Dir(path))
		if err != nil {
			return errors.New("the original Local package is unavailable")
		}
		_, pending, err := custodyinstall.PendingPackageWallet(m.Owner, m.Location, m.ID)
		if err != nil {
			return err
		}
		return checkPendingOwnerDraft(request, saved, pending)
	})
}

func checkPendingOwnerDraft(request signer.OwnerIntent, saved *draft, pending *update.WalletInitialization) error {
	if pending == nil {
		return nil
	}
	if !pending.Valid() || saved == nil || saved.Origin != "" || saved.Intent.OperationID != pending.OperationID || saved.Intent.InstanceID != pending.InstanceID || saved.Intent.Network != pending.Network || request.InstanceID != pending.InstanceID || request.Network != pending.Network || request.Action != saved.Intent.Action {
		return errors.New("resume the original pending wallet operation")
	}
	return nil
}

// LocalOwnerPackage selects the existing protected pair by committed instance,
// including only the original pending wallet operation during recovery.
func LocalOwnerPackage(intent signer.OwnerIntent) (Package, error) {
	var result Package
	if intent.Validate() != nil || intent.Version != 1 {
		return result, errors.New("the original Local wallet request is required")
	}
	path, err := localPackageOwnerTarget(os.Getuid(), intent.InstanceID)
	if err != nil || path == "" {
		return result, errors.New("the protected Local wallet is unavailable")
	}
	root := filepath.Dir(path)
	m, err := custodyinstall.LoadPackageInstance(os.Getuid(), root)
	if err != nil {
		return result, errors.New("the original Local package is unavailable")
	}
	base, pending, err := custodyinstall.PendingPackageWallet(m.Owner, m.Location, m.ID)
	if err != nil {
		return result, err
	}
	if pending != nil {
		if pending.InstanceID != intent.InstanceID || pending.Network != intent.Network || pending.OperationID != intent.OperationID || base.Root != root {
			return result, errors.New("resume the original wallet setup")
		}
		m = base
	} else if err = custodyinstall.VerifyPackageOwnerTarget(m); err != nil {
		return result, errors.New("the protected Local package is unavailable")
	}
	result = Package{Core: path, Signer: filepath.Join(root, "fased-signer"), CoreSHA256: m.CoreHash, SignerSHA256: m.SignerHash, LocalPaths: localinstall.Paths{Root: root, Data: filepath.Join(root, "app-state")}}
	return result, result.verify()
}

// Select by the protected owner commitment, never by a browser-provided path.
// A different or unfinished package is not converted into a legacy target.
func localPackageOwnerTarget(owner int, instance string) (string, error) {
	if owner <= 0 || owner != os.Getuid() || owner != os.Geteuid() {
		return "", errors.New("the ordinary Local owner is required")
	}
	entries, err := os.ReadDir(filepath.Join("/var/lib/fased-app-package", strconv.Itoa(owner), "local"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return selectLocalPackageOwnerTarget(owner, instance, entries, custodyinstall.LoadPackageInstance, custodyinstall.ReadEnrollmentCommit, custodyinstall.VerifyPackageOwnerTarget, custodyinstall.PendingPackageWallet, custodyinstall.PackagePendingInstance)
}

func selectLocalPackageOwnerTarget(owner int, instance string, entries []os.DirEntry, load func(int, string) (custodyinstall.Manifest, error), commit func(custodyinstall.Manifest) (custodyinstall.EnrollmentCommit, error), verify func(custodyinstall.Manifest) error, pending func(int, string, string) (custodyinstall.Manifest, *update.WalletInitialization, error), pendingStage func(int, string, string) (string, error)) (string, error) {
	var selected, selectedID string
	staged := make(map[string]bool)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".pending") {
			if pendingStage == nil || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return "", errors.New("unclassified Local package instance; preserve it")
			}
			id, err := pendingStage(owner, "local", entry.Name())
			if err != nil || !update.IDOK(id) || entry.Name() != "."+id+".pending" || staged[id] {
				return "", errors.New("unclassified Local package instance; preserve it")
			}
			staged[id] = true
			continue
		}
		root, err := update.PackageInstanceRoot(owner, "local", entry.Name())
		if err != nil || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return "", errors.New("unclassified Local package instance; preserve it")
		}
		base, wallet, err := pending(owner, "local", entry.Name())
		if err != nil {
			return "", errors.New("Local package recovery is unavailable; preserve it")
		}
		if wallet != nil {
			if wallet.InstanceID == instance {
				if selected != "" || base.Root != root {
					return "", errors.New("the original Local package wallet is not uniquely ready")
				}
				selected, selectedID = filepath.Join(root, "fased-app"), entry.Name()
			}
			continue
		}
		m, err := load(owner, root)
		if err != nil || m.Owner != owner || m.Root != root || m.ID != entry.Name() || m.Location != "local" || m.Layout != custodyinstall.PackageLayout {
			return "", errors.New("Local package identity is unavailable; preserve it")
		}
		ownerCommit, err := commit(m)
		if err != nil {
			return "", errors.New("Local package owner is unavailable; preserve it")
		}
		if ownerCommit.InstanceID != instance {
			continue
		}
		if selected != "" || verify(m) != nil {
			return "", errors.New("the original Local package wallet is not uniquely ready")
		}
		selected, selectedID = filepath.Join(root, "fased-app"), entry.Name()
	}
	if selected != "" && staged[selectedID] {
		return "", errors.New("the selected package has an unfinished installation; preserve it")
	}
	if selected == "" && len(staged) != 0 {
		return "", errors.New("an unfinished Local installation must be preserved")
	}
	return selected, nil
}
