//go:build linux

package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

// nativeUpdateFiles is called while p.mu is held. Both fixed programs are
// executed as the ordinary owner from sealed copies of the attested release.
// The target core, rather than caller-provided metadata, supplies the expected
// native and signer hashes; the matching installer supplies their actual bytes.
func (p *Prepared) nativeUpdateFiles(ctx context.Context, operation string) (_ nativeUpdateArtifacts, cleanup func() error, resultErr error) {
	var result nativeUpdateArtifacts
	if p == nil || p.closed || p.root == nil || p.seed.Version != 4 || !p.report.ProvenanceVerified || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || !update.IDOK(operation) {
		return result, nil, errors.New("one attested owner update is required for the native pair")
	}
	if err := p.revalidate(); err != nil {
		return result, nil, err
	}
	installerPath, installer, err := p.acquireAttestedInstaller(ctx)
	if err != nil || installer.SHA256 != p.descriptor.Release.InstallerSHA256 {
		return result, nil, errors.New("the exact attested installer is unavailable")
	}
	engine, err := p.sealedNativeInput(filepath.Join("bundle", "fased-app"), p.engine.Bytes, p.engine.SHA256)
	if err != nil {
		return result, nil, err
	}
	defer engine.Close()
	imageName := filepath.Base(installerPath)
	if imageName != "candidate-installer" && imageName != "maintenance-installer" {
		return result, nil, errors.New("unexpected attested installer location")
	}
	image, err := p.sealedNativeInput(imageName, installer.Size, installer.SHA256)
	if err != nil {
		return result, nil, err
	}
	defer image.Close()
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := nativeFixedCommand(bounded, engine, "native-release-binding")
	pipe, err := command.StdoutPipe()
	if err != nil {
		return result, nil, err
	}
	if err = command.Start(); err != nil {
		return result, nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(pipe, 4097))
	if readErr != nil || len(raw) > 4096 {
		_ = command.Process.Kill()
	}
	err = errors.Join(readErr, command.Wait())
	var binding NativeReleaseBinding
	if err != nil || len(raw) > 4096 || update.StrictJSON(raw, &binding) != nil || !binding.Valid(p.descriptor.ProductVersion, p.descriptor.Source) {
		return result, nil, errors.New("target core has no valid paired native release identity")
	}
	created := map[string]os.FileInfo{}
	cleanup = func() error {
		for name, before := range created {
			after, err := p.root.Lstat(name)
			if err != nil || before == nil || !os.SameFile(before, after) {
				return errors.New("native update staging changed; preserve it")
			}
		}
		for name := range created {
			if err := p.root.Remove(name); err != nil {
				return err
			}
		}
		return nil
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, cleanup())
			cleanup = nil
		}
	}()
	for _, item := range []struct {
		name, command, want string
		path, digest        *string
	}{
		{"native-" + operation + "-desktop", "--emit-embedded-native", binding.DesktopSHA256, &result.Desktop, &result.DesktopSHA256},
		{"native-" + operation + "-signer", "--emit-embedded-signer", binding.SignerSHA256, &result.Signer, &result.SignerSHA256},
	} {
		file, err := p.root.OpenFile(item.name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return result, cleanup, err
		}
		created[item.name], err = file.Stat()
		if err != nil {
			file.Close()
			return result, cleanup, err
		}
		cmd := nativeFixedCommand(bounded, image, item.command)
		output, err := cmd.StdoutPipe()
		if err != nil {
			file.Close()
			return result, cleanup, err
		}
		if err = cmd.Start(); err != nil {
			file.Close()
			return result, cleanup, err
		}
		identity, readErr := ReadWalletPayload(output, file, p.descriptor.ProductVersion, p.descriptor.Source)
		if readErr != nil {
			_ = cmd.Process.Kill()
		}
		err = errors.Join(readErr, cmd.Wait(), file.Chmod(0600), file.Sync(), file.Close())
		if err != nil || identity.SHA256 != item.want {
			return result, cleanup, errors.New("installer payload differs from the authenticated core binding")
		}
		*item.path = filepath.Join(p.options.Directory, item.name)
		*item.digest = identity.SHA256
	}
	if err := p.revalidate(); err != nil {
		return result, cleanup, err
	}
	return result, cleanup, nil
}

func (p *Prepared) sealedNativeInput(name string, size int64, digest string) (*os.File, error) {
	f, err := p.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	entry, entryErr := p.root.Lstat(name)
	if err != nil || entryErr != nil || !before.Mode().IsRegular() || !os.SameFile(before, entry) || before.Size() != size {
		return nil, errors.New("attested native executable changed")
	}
	return sealVerifiedExecutable(f, size, digest)
}

func nativeFixedCommand(ctx context.Context, image *os.File, operation string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/proc/self/fd/3", operation)
	cmd.ExtraFiles = []*os.File{image}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	cmd.Dir = "/"
	cmd.Stderr = io.Discard
	return cmd
}
