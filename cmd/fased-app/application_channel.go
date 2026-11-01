package main

import (
	"context"
	"errors"
	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"os"
	"path/filepath"
)

// Public trust only. Publisher review supplies this binding in a future release;
// nil ordinary builds remain inactive. No environment, UI, CLI or JSON override.
type applicationChannelBinding struct {
	Seed   []byte
	SHA256 string
}

var compiledApplicationChannel *applicationChannelBinding

func channelOptions(binding *applicationChannelBinding, directory string) (bootstrap.Options, error) {
	if binding == nil || len(binding.Seed) == 0 {
		return bootstrap.Options{}, errors.New("trusted update channel is not published")
	}
	// Production-only binding: fixture transports exist solely in test Go callers.
	var seed bootstrap.Seed
	if update.StrictJSON(binding.Seed, &seed) != nil || seed.Version != 3 || seed.DevelopmentOnly || seed.Channel != "stable" {
		return bootstrap.Options{}, errors.New("production update channel required")
	}
	return bootstrap.Options{Seed: append([]byte(nil), binding.Seed...), SeedSHA256: binding.SHA256, Directory: directory}, nil
}

type channelOptionsFactory func(string) (bootstrap.Options, error)

func channelSource(options channelOptionsFactory, parent string) func(context.Context, core.ApplicationRelease, string) (*bootstrap.Prepared, error) {
	return func(ctx context.Context, release core.ApplicationRelease, id string) (*bootstrap.Prepared, error) {
		if !update.IDOK(id) || !filepath.IsAbs(parent) {
			return nil, errors.New("validated owner acquisition location required")
		}
		if e := core.EnsurePrivateDirectory(parent); e != nil {
			return nil, e
		}
		directory := filepath.Join(parent, "acquisition-"+id+"-"+update.NewProbe())
		o, e := options(directory)
		if e != nil {
			return nil, e
		}
		p, e := bootstrap.Prepare(ctx, o)
		if e != nil {
			return nil, e
		}
		p.RetainAcquisitionEvidence()
		got, e := p.ApplicationRelease()
		if e != nil || got == nil || *got != release {
			_ = p.Close()
			return nil, errors.New("independent owner release differs from the approved update")
		}
		return p, nil
	}
}
func channelCheck(options channelOptionsFactory, parent string, installedSequence uint64, installedRelease string) func(context.Context) (*core.ApplicationRelease, error) {
	return func(ctx context.Context) (*core.ApplicationRelease, error) {
		if e := core.EnsurePrivateDirectory(parent); e != nil {
			return nil, e
		}
		directory := filepath.Join(parent, "check-"+update.NewProbe())
		o, e := options(directory)
		if e != nil {
			return nil, e
		}
		descriptor, digest, e := bootstrap.Inspect(ctx, o)
		if e != nil {
			return nil, e
		}
		if descriptor.Sequence < installedSequence || descriptor.Sequence == installedSequence && digest != installedRelease {
			return nil, errors.New("release rollback or identity change refused")
		}
		if descriptor.Sequence == installedSequence && digest == installedRelease {
			return nil, nil
		}
		p, e := bootstrap.Prepare(ctx, o)
		if e != nil {
			return nil, e
		}
		// This cache is created exclusively by Prepare. Close before exact disposal;
		// failed/foreign output is retained by the bootstrap's own recovery policy.
		owned, statErr := os.Lstat(directory)
		defer func() {
			_ = p.Close()
			if current, e := os.Lstat(directory); statErr == nil && e == nil && os.SameFile(owned, current) {
				_ = os.RemoveAll(directory)
			}
		}()
		got, e := p.ApplicationRelease()
		if e != nil {
			return nil, e
		}
		if got.ManifestSHA256 != digest || got.Sequence != descriptor.Sequence {
			return nil, errors.New("release changed during check")
		}
		return got, nil
	}
}
func installedChannelSource(parent string) func(context.Context, core.ApplicationRelease, string) (*bootstrap.Prepared, error) {
	if compiledApplicationChannel == nil {
		return compiledApplicationUpdateSource
	}
	return channelSource(func(path string) (bootstrap.Options, error) { return channelOptions(compiledApplicationChannel, path) }, parent)
}

func installedReaderChannelSource(parent string) func(context.Context, string) (*bootstrap.Prepared, error) {
	return func(ctx context.Context, id string) (*bootstrap.Prepared, error) {
		if !update.IDOK(id) || !filepath.IsAbs(parent) {
			return nil, errors.New("validated reader operation required")
		}
		if e := core.EnsurePrivateDirectory(parent); e != nil {
			return nil, e
		}
		directory := filepath.Join(parent, "reader-acquisition-"+id+"-"+update.NewProbe())
		o, e := channelOptions(compiledApplicationChannel, directory)
		if e != nil {
			return nil, e
		}
		p, e := bootstrap.Prepare(ctx, o)
		if e == nil {
			p.RetainAcquisitionEvidence()
		}
		return p, e
	}
}
