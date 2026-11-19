//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/update"
)

func maintenancePackageAdoptLocalCommand(action string, args []string, out io.Writer) error {
	if len(args) != 16 || args[6] != "--listen" || args[8] != "--legacy-candidate" || args[10] != "--retained-candidate" || args[12] != "--prefix" || args[14] != "--data-dir" {
		return errors.New("one exact Local package and stopped predecessor required")
	}
	owner, location, instance, err := packageMaintenanceArguments(args[:6])
	if err != nil || location != "local" || compiledMaintenanceOptions != nil {
		return errors.New("protected legacy adoption applies only to the pinned Local release")
	}
	a, bridge, err := maintenance.ValidateLocalAdoptionBridge(owner, instance)
	if err != nil {
		return err
	}
	if action != "prepare" {
		applyLock, err := maintenance.LocalAdoptionApplyLock(owner, instance)
		if err != nil {
			return err
		}
		defer applyLock.Close()
	}
	lock, err := maintenance.PackageLock(owner, location, instance)
	if err != nil {
		return err
	}
	defer lock.Close()
	current, currentBridge, err := maintenance.ValidateLocalAdoptionBridge(owner, instance)
	if err != nil || current != a || currentBridge != bridge {
		return errors.New("protected adoption authority changed")
	}
	receipt, err := maintenance.ReadPackageCandidateReceipt(owner, location, instance)
	if err != nil || receipt.Scope != "fresh" || receipt.PacketSHA256 != maintenance.LegacyLocalRetainedPacket || receipt.InstallerSHA256 != a.ImageSHA256 {
		return errors.New("stage the original authenticated retained candidate through its pinned helper first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := localAdoptionChild(ctx, bridge, a.Owner, a.Group, "export", args[9], args[11], args[13], args[15])
	control, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer control.Close()
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	waited := false
	defer func() {
		if !waited {
			cancel()
			_ = cmd.Wait()
		}
	}()
	r := bufio.NewReaderSize(pipe, 4096)
	stage, raw, err := readLocalAdoptionImage(r)
	if err != nil {
		return errors.New("could not verify and export the stopped legacy workspace as its owner")
	}
	defer clear(raw)
	adoption := localAdoptionImport{ctx: ctx, bridge: bridge, stage: stage, image: raw, verifySource: func() error {
		if _, err := io.WriteString(control, "verify\n"); err != nil {
			return err
		}
		reply, err := r.ReadSlice('\n')
		if err != nil || string(reply) != "ok\n" {
			return errors.New("the original Local source lease changed; adoption remains fenced")
		}
		return nil
	}}
	var result any
	if action == "prepare" {
		if err := provisionPackageCandidate(ctx, a, location, instance, args[7], lock, &adoption); err != nil {
			return err
		}
		result = struct {
			bootstrap.LocalHandoffStage
			PackageInstance string `json:"packageInstance"`
		}{stage, instance}
	} else {
		if !stage.MatchesRetainedPackage(owner, receipt.PacketSHA256, maintenance.LegacyLocalRetainedBundle) {
			return errors.New("the source and retained Local package differ from the adoption")
		}
		applied, err := custodyinstall.ApplyLegacyLocalAdoption(ctx, owner, instance, args[7], action == "recover", lock, adoption.commitment(), adoption.verifyImage, adoption.verifySource)
		if err != nil {
			return err
		}
		result = applied
	}
	closeErr := control.Close()
	waitErr := cmd.Wait()
	waited = true
	if err := errors.Join(closeErr, waitErr); err != nil {
		return errors.New("Local adoption source did not close cleanly; preserve the fenced instance")
	}
	return json.NewEncoder(out).Encode(result)
}

// Both child roles run the same protected image at ordinary UIDs. In
// particular, the privileged controller does not open any predecessor path.
func localAdoptionChild(ctx context.Context, bridge string, uid, gid int, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bridge, append([]string{"maintenance-local-adoption-image"}, args...)...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = "/", []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}, 2*time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
	return cmd
}

func readLocalAdoptionImage(r *bufio.Reader) (bootstrap.LocalHandoffStage, []byte, error) {
	var stage bootstrap.LocalHandoffStage
	line, err := r.ReadSlice('\n')
	if err != nil || update.StrictJSON(line, &stage) != nil {
		return stage, nil, errors.New("invalid private Local adoption frame")
	}
	var raw []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(raw)+len(part) > core.MaxEnrollmentEncodedBytes || err != nil && !errors.Is(err, bufio.ErrBufferFull) {
			clear(raw)
			return stage, nil, errors.New("invalid private Local adoption image size")
		}
		raw = append(raw, part...)
		if err == nil {
			return stage, raw, nil
		}
	}
}

type localAdoptionImport struct {
	ctx          context.Context
	bridge       string
	stage        bootstrap.LocalHandoffStage
	image        []byte
	verifySource func() error
}

func (a localAdoptionImport) commitment() custodyinstall.PackageLocalAdoption {
	return custodyinstall.PackageLocalAdoption{
		LegacyManifestSHA256: a.stage.LegacyManifestSHA256,
		InstanceID:           a.stage.InstanceID, StateSHA256: a.stage.StateSHA256,
	}
}

func (a localAdoptionImport) importImage(m custodyinstall.Manifest) error {
	if err := a.checkImage(m, "import"); err != nil {
		return err
	}
	return a.verifySource()
}

func (a localAdoptionImport) verifyImage(m custodyinstall.Manifest) error {
	return a.checkImage(m, "verify")
}

func (a localAdoptionImport) checkImage(m custodyinstall.Manifest, action string) error {
	cmd := localAdoptionChild(a.ctx, a.bridge, m.App.UID, m.App.GID, action, filepath.Join(m.Root, "app-state"), a.stage.InstanceID, a.stage.StateSHA256)
	cmd.Stdin = bytes.NewReader(a.image)
	if err := cmd.Run(); err != nil {
		return errors.New("the protected app identity could not verify the staged workspace")
	}
	return nil
}

func maintenanceLocalAdoptionImageCommand(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || in == nil || os.Getuid() <= 0 || os.Geteuid() != os.Getuid() {
		return errors.New("ordinary Local adoption image identity required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	switch args[0] {
	case "export":
		if len(args) != 5 || compiledFirstInstallBinding == nil {
			return errors.New("compiled trust and original Local predecessor required")
		}
		options := bootstrap.Options{Seed: compiledFirstInstallBinding.Seed, SeedSHA256: compiledFirstInstallBinding.SHA256}
		return bootstrap.ExportLegacyLocalHandoff(ctx, options, args[1], args[2], localinstall.Paths{Root: args[3], Data: args[4]}, in, out)
	case "import", "verify":
		if len(args) != 4 || filepath.Base(args[1]) != "app-state" {
			return errors.New("one protected app-state directory required")
		}
		raw, err := io.ReadAll(io.LimitReader(in, core.MaxEnrollmentEncodedBytes+1))
		defer clear(raw)
		if err != nil {
			return err
		}
		image, err := core.DecodeEnrollmentImage(raw)
		if err != nil || image.Schema != core.StateSchema || image.InstanceID != args[2] || core.EnrollmentImageDigest(image) != args[3] {
			return errors.New("the imported workspace or state digest differs")
		}
		if args[0] == "verify" {
			return core.VerifyImportedEnrollmentImage(ctx, args[1], image)
		}
		return core.ImportEnrollmentImage(ctx, args[1], image)
	default:
		return errors.New("unsupported Local adoption image operation")
	}
}
