//go:build linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// This request carries only the selected network and public operation IDs.
// The fixed protected helper verifies the journal and physical empty signer.
func initializePackageWallet(instance, network, operation string, fixture, ordinary bool, diagnostics io.Writer) error {
	request := update.WalletInitialization{Version: 1, OperationID: operation, InstanceID: instance, Network: network}
	if !request.Valid() {
		return errors.New("invalid wallet initialization")
	}
	data, err := packageWalletCommandData(os.Getuid())
	if err != nil || data == "" {
		return errors.New("the original package wallet is required")
	}
	m, err := custodyinstall.LoadPackageInstance(os.Getuid(), filepath.Dir(data))
	if err != nil || data != filepath.Join(m.Root, "app-state") || m.Network != "" && m.Network != network {
		return errors.New("the original package wallet is required")
	}
	if _, err = walletOwnerMode(m, fixture, ordinary, false); err != nil {
		return err
	}
	if _, err = custodyOwnerFlag(m, "setup"); err != nil {
		return err
	}
	commit, err := custodyinstall.ReadEnrollmentCommit(m)
	if err != nil || commit.InstanceID != instance {
		// In-flight initialization may already have replaced some metadata.
		// Only its original operation may reach the helper in that case.
		_, pending, e := custodyinstall.PendingPackageWallet(m.Owner, m.Location, m.ID)
		if e != nil || pending == nil || *pending != request {
			return errors.New("the original package workspace and wallet operation are required")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return maintenance.PackageCall(ctx, m.Location, m.ID, maintenance.PackageRequest{Version: 1, Action: "wallet-initialize", Wallet: &request}, diagnostics)
}

// Remote owner commands must execute the exact protected image for their
// instance. A caller cannot pick a path through an argument or environment.
func packageWalletCommandData(owner int) (string, error) {
	image, err := os.Executable()
	if err != nil {
		return "", err
	}
	base := filepath.Join("/var/lib/fased-app-package", strconv.Itoa(owner), "remote") + "/"
	if !strings.HasPrefix(image, base) {
		return defaultPackageLocalData("", false)
	}
	root := filepath.Dir(image)
	expected, err := update.PackageInstanceRoot(owner, "remote", filepath.Base(root))
	if err != nil || root != expected || image != filepath.Join(root, "fased-app") || signer.VerifyExecutable() != nil {
		return "", errors.New("the original protected Remote owner command is required")
	}
	return filepath.Join(root, "app-state"), nil
}

func pendingPackageWalletTarget(owner int, instance, network string) (custodyinstall.Manifest, string, error) {
	data, err := packageWalletCommandData(owner)
	if err != nil || data == "" {
		return custodyinstall.Manifest{}, "", err
	}
	m, err := custodyinstall.LoadPackageInstance(owner, filepath.Dir(data))
	if err != nil || data != filepath.Join(m.Root, "app-state") {
		return custodyinstall.Manifest{}, "", errors.New("the original package wallet is required")
	}
	base, pending, err := custodyinstall.PendingPackageWallet(owner, m.Location, m.ID)
	if err != nil || pending == nil {
		return custodyinstall.Manifest{}, "", err
	}
	if pending.InstanceID != instance || pending.Network != network {
		return custodyinstall.Manifest{}, "", errors.New("a different wallet setup requires recovery")
	}
	return base, pending.OperationID, nil
}

// An installed package command must never fall back to owner-wide custody.
// Its journal and owner commitment stay authoritative for Wallets too.
func custodyWindowTarget(owner int) (custodyinstall.Manifest, bool, error) {
	if owner <= 0 || os.Getuid() != owner || os.Geteuid() != owner {
		return custodyinstall.Manifest{}, false, errors.New("ordinary package owner required")
	}
	data, err := packageWalletCommandData(owner)
	if err != nil {
		return custodyinstall.Manifest{}, false, err
	}
	if data == "" {
		m, err := custodyinstall.Load(owner)
		return m, false, err
	}
	root := filepath.Dir(data)
	m, err := custodyinstall.LoadPackageInstance(owner, root)
	if err != nil || data != filepath.Join(root, "app-state") || custodyinstall.VerifyPackageOwnerTarget(m) != nil {
		return custodyinstall.Manifest{}, true, errors.New("original package owner required")
	}
	r, err := update.ReadPackageMaintenanceRecord(owner, m.Location, m.ID)
	if err != nil {
		return custodyinstall.Manifest{}, true, err
	}
	if err = packageWalletWindowIdle(root, r); err != nil {
		return custodyinstall.Manifest{}, true, err
	}
	return m, true, nil
}

// Call only after the protected record and selected deployment have been
// verified. A matching update fence is still a reason to keep Wallets closed.
func packageWalletWindowIdle(root string, r update.MaintenanceRecord) error {
	if r.Phase != "idle" && (r.Phase != "completed" || r.Start != "target") {
		return errors.New("finish the original package update before Wallets")
	}
	for _, name := range []string{"update-active.json", "update-start-blocked.json"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			return errors.New("finish the original package recovery before Wallets")
		}
	}
	return nil
}
