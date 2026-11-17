//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/ownerclient"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

// The OS package is a stable command entry. Ordinary commands execute the
// selected instance's verified current image, while offline recovery remains
// available from the bootstrap even if that image cannot start.
func dispatchPackageInstance(args []string) error {
	image, err := os.Executable()
	if err != nil || image != "/usr/lib/fased-app/fased-app" || len(args) == 0 {
		return err
	}
	data, explicit, delegate, err := packageCommandTarget(args)
	if err != nil || !delegate {
		return err
	}
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || signer.VerifyExecutablePath(image) != nil {
		return errors.New("the package command requires its ordinary OS owner")
	}
	var walletIntent *signer.OwnerIntent
	if !explicit {
		// A wallet link identifies a workspace, which can differ from the
		// default package. Select its committed owner target before exec.
		if args[0] == "open" && len(args) == 2 && strings.HasPrefix(args[1], "fased-wallet://") {
			intent, origin, e := ownerclient.ParseLink(args[1])
			if e != nil || origin != "" {
				return errors.New("the original Local wallet request is required")
			}
			walletIntent = &intent
			image, e := ownerclient.LocalPackageOwnerTarget(intent.InstanceID)
			if e != nil || image == "" {
				return errors.New("the original Local wallet is unavailable")
			}
			data = filepath.Join(filepath.Dir(image), "app-state")
		} else {
			data, err = defaultPackageLocalData("", false)
		}
		if err != nil {
			return err
		}
	}
	data, err = filepath.Abs(data)
	if err != nil {
		return err
	}
	root := filepath.Dir(data)
	m, err := custodyinstall.LoadPackageInstance(os.Getuid(), root)
	if err != nil || m.Layout != custodyinstall.PackageLayout || m.Owner != os.Getuid() || m.Root != root || data != filepath.Join(root, "app-state") {
		return errors.New("select the original package instance with --data-dir")
	}
	if err := verifyPackageWalletEntry(m, walletIntent, custodyinstall.VerifyPackageInstance, custodyinstall.PendingPackageWallet); err != nil {
		return errors.New("the installed pair is incomplete; use fased update recover with the same --data-dir")
	}
	executable := filepath.Join(root, "fased-app")
	return syscall.Exec(executable, append([]string{executable}, args...), os.Environ())
}

func redirectLocalPackageWallet(instance, link, data string) error {
	image, err := ownerclient.LocalPackageOwnerTarget(instance)
	if err != nil || image == "" || data != filepath.Join(filepath.Dir(image), "app-state") {
		return errors.New("the original Local wallet is unavailable")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self == image {
		return nil
	}
	return syscall.Exec(image, []string{image, "open", link}, os.Environ())
}

// During wallet initialization the protected journal may contain an exact
// mix of base and target metadata. Only its original public wallet entry may
// execute the already verified pair to resume that same operation.
func verifyPackageWalletEntry(m custodyinstall.Manifest, intent *signer.OwnerIntent, verify func(custodyinstall.Manifest) error, pending func(int, string, string) (custodyinstall.Manifest, *update.WalletInitialization, error)) error {
	if err := verify(m); err == nil {
		return nil
	}
	if intent == nil || intent.Version != 1 || intent.Validate() != nil || m.Location != "local" {
		return errors.New("the original wallet request is required")
	}
	base, operation, err := pending(m.Owner, m.Location, m.ID)
	if err != nil || operation == nil || !operation.Valid() || operation.InstanceID != intent.InstanceID || operation.Network != intent.Network || base.Owner != m.Owner || base.ID != m.ID || base.Root != m.Root || base.Location != m.Location || base.Network != "" {
		return errors.New("the original pending wallet is unavailable")
	}
	target := base
	target.Network = operation.Network
	if m != base && m != target {
		return errors.New("the pending wallet package differs")
	}
	return nil
}

func packageCommandTarget(args []string) (data string, explicit, delegate bool, err error) {
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "open", "status", "health", "claim", "login", "logout", "agents", "accounts", "custody", "ai-connection", "update", "updates", "update-status", "update-preferences", "version":
		delegate = true
	default:
		return
	}
	// The pinned bootstrap must still perform recovery when the target core is
	// missing or refuses execution. It uses the same recorded worker operation.
	if len(args) > 1 && ((args[0] == "update" || args[0] == "updates") && args[1] == "recover" || args[0] == "version" && args[1] == "--entry-json") {
		delegate = false
		return
	}
	// Remote updates use the separately saved private connection and do not
	// require an installed Local instance to select a command image.
	if args[0] == "update" {
		remote := false
		for i := 1; i < len(args); i++ {
			arg := args[i]
			if arg == "--" {
				break
			}
			if arg == "--data-dir" || arg == "-data-dir" || arg == "--prefix" || arg == "-prefix" {
				i++
				continue
			}
			if arg == "--remote" || arg == "-remote" {
				remote = true
			} else if value, ok := strings.CutPrefix(strings.TrimPrefix(arg, "-"), "-remote="); ok {
				remote, _ = strconv.ParseBool(value)
			} else if value, ok := strings.CutPrefix(arg, "-remote="); ok {
				remote, _ = strconv.ParseBool(value)
			}
		}
		if remote {
			delegate = false
			return
		}
	}
	for i := 1; i < len(args); i++ {
		value, found := strings.CutPrefix(args[i], "--data-dir=")
		if args[i] == "--data-dir" {
			i++
			if i == len(args) {
				err = errors.New("--data-dir requires the original instance directory")
				return
			}
			value, found = args[i], true
		}
		if found {
			if explicit || value == "" {
				err = errors.New("select exactly one package data directory")
				return
			}
			data, explicit = value, true
		}
	}
	return
}

// Package commands select their original instance automatically. Older
// commands and an explicitly selected data directory keep their target.
func defaultPackageLocalData(fallback string, explicit bool) (string, error) {
	if explicit {
		return fallback, nil
	}
	image, err := os.Executable()
	if err != nil {
		return "", err
	}
	return packageLocalDataForExecutable(fallback, image, os.Getuid(), os.Geteuid(), signer.VerifyExecutablePath, os.ReadDir, custodyinstall.LoadPackageInstance)
}

func packageLocalDataForExecutable(fallback, image string, owner, effectiveUID int, verify func(string) error, readDir func(string) ([]os.DirEntry, error), load func(int, string) (custodyinstall.Manifest, error)) (string, error) {
	const packageTree = "/var/lib/fased-app-package/"
	const systemImage = "/usr/lib/fased-app/fased-app"
	instanceImage := strings.HasPrefix(image, packageTree)
	if image != systemImage && !instanceImage {
		return fallback, nil
	}
	if owner <= 0 || owner != effectiveUID || verify(image) != nil {
		return "", errors.New("the installed package must be opened by its ordinary OS owner")
	}
	if instanceImage {
		parts := strings.Split(strings.TrimPrefix(image, packageTree), "/")
		if len(parts) != 4 || parts[0] != strconv.Itoa(owner) || parts[1] != "local" || parts[3] != "fased-app" {
			return "", errors.New("the original Local package executable is required")
		}
		root, err := update.PackageInstanceRoot(owner, parts[1], parts[2])
		if err != nil || image != filepath.Join(root, "fased-app") {
			return "", errors.New("the original Local package executable is required")
		}
		m, err := load(owner, root)
		if err != nil || m.Layout != custodyinstall.PackageLayout || m.Owner != owner || m.Root != root || m.Location != "local" || m.ID != parts[2] {
			return "", errors.New("Local package instance cannot be verified; preserve it")
		}
		return filepath.Join(root, "app-state"), nil
	}
	parent := filepath.Join("/var/lib/fased-app-package", strconv.Itoa(owner), "local")
	entries, err := readDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return "", errors.New("no Local package instance is installed")
	}
	if err != nil {
		return "", err
	}
	return selectPackageLocalData(owner, entries, load, custodyinstall.PackagePendingInstance)
}

// Never guess between instances or adopt a malformed or foreign directory.
// A separately verified pending installation remains untouched.
func selectPackageLocalData(owner int, entries []os.DirEntry, load func(int, string) (custodyinstall.Manifest, error), pending func(int, string, string) (string, error)) (string, error) {
	if owner <= 0 || load == nil {
		return "", errors.New("ordinary package owner required")
	}
	var selected, selectedID string
	staged := make(map[string]bool)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") && strings.HasSuffix(entry.Name(), ".pending") {
			if pending == nil || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return "", errors.New("unclassified Local package instance; preserve it")
			}
			id, err := pending(owner, "local", entry.Name())
			if err != nil || !update.IDOK(id) || entry.Name() != "."+id+".pending" || staged[id] {
				return "", errors.New("unclassified Local package instance; preserve it")
			}
			staged[id] = true
			continue
		}
		root, err := update.PackageInstanceRoot(owner, "local", entry.Name())
		if err != nil || entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return "", errors.New("unclassified Local package instance; preserve it")
		}
		m, err := load(owner, root)
		if err != nil || m.Layout != custodyinstall.PackageLayout || m.Root != root || m.Owner != owner || m.Location != "local" || m.ID != entry.Name() {
			return "", errors.New("Local package instance cannot be verified; preserve it")
		}
		if selected != "" {
			return "", errors.New("multiple Local package instances; select one with --data-dir")
		}
		selected, selectedID = filepath.Join(root, "app-state"), entry.Name()
	}
	if selected == "" {
		return "", errors.New("no Local package instance is installed")
	}
	if staged[selectedID] {
		return "", errors.New("the selected package has an unfinished installation; preserve it")
	}
	return selected, nil
}
