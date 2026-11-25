//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"io"
)

func verifyRootExecutableTool(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	for dir := filepath.Dir(resolved); ; dir = filepath.Dir(dir) {
		info, lErr := os.Lstat(dir)
		if lErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("trusted root-owned tool directory required")
		}
		if dir == "/" {
			break
		}
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 {
		return errors.New("trusted root-owned executable required")
	}
	return nil
}

func runHostMigrateUnits(location, instance string, out, errOut io.Writer) error {
	uid := os.Getuid()
	if uid <= 0 || os.Geteuid() != uid {
		return errors.New("host unit migration requires an ordinary Linux administrator")
	}
	ref, err := resolveHostPackageInstance(uid, location, instance, os.ReadDir)
	if err != nil {
		return err
	}
	root, err := custodyinstall.PackageRoot(uid, ref.Location, ref.ID)
	if err != nil {
		return err
	}
	m, err := custodyinstall.LoadPackageInstance(uid, root)
	if err != nil {
		return err
	}
	if err := custodyinstall.VerifyPackageInstance(m); err != nil {
		return err
	}
	auth, err := maintenance.ReadPackageAuthority(uid, ref.Location, ref.ID)
	if err != nil {
		return err
	}
	if err := maintenance.VerifyImage(auth); err != nil {
		return err
	}
	if err := verifyRootExecutableTool("/usr/bin/sudo"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		"/usr/bin/sudo", "--",
		maintenance.ImagePath(auth.ImageSHA256),
		"maintenance-package-migrate-units",
		"--owner", strconv.Itoa(uid),
		"--location", ref.Location,
		"--instance", ref.ID,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}
