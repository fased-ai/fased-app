package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	if update.StrictJSON(binding.Seed, &seed) != nil || (seed.Version != 3 && seed.Version != 4) || seed.DevelopmentOnly || seed.Channel != "stable" {
		return bootstrap.Options{}, errors.New("production update channel required")
	}
	return bootstrap.Options{Seed: append([]byte(nil), binding.Seed...), SeedSHA256: binding.SHA256, Directory: directory}, nil
}

type channelOptionsFactory func(string) (bootstrap.Options, error)

func channelMaintenanceRelease(d bootstrap.Descriptor, release core.ApplicationRelease) (core.ApplicationRelease, error) {
	if release.Version != d.ProductVersion || release.Sequence != d.Sequence || release.Target != d.Target {
		return core.ApplicationRelease{}, errors.New("verified descriptor differs from checked release")
	}
	// Other trust profiles retain their original verified identity, including
	// their original serialization. Only attested main-dispatch changed ABI.
	if d.Version != 4 || d.Release.BuildRef != "refs/heads/main" || d.Release.TagObject != "" {
		return release, nil
	}
	if !bootstrap.ApplicationDescriptorMatches(d, release.ManifestSHA256) {
		return core.ApplicationRelease{}, errors.New("verified descriptor differs from checked release")
	}
	// Retained maintenance readers compare the complete authenticated delivery
	// descriptor. Keep that wire identity; do not replace their executable,
	// weaken the tuple comparison, or rewrite any previously approved operation.
	release.ManifestSHA256 = channelDescriptorDigest(d)
	return release, nil
}

func channelDescriptorDigest(d bootstrap.Descriptor) string {
	b, _ := json.Marshal(d)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func channelRememberDescriptor(parent string, d bootstrap.Descriptor) error {
	return core.WritePrivateJSON(filepath.Join(parent, "descriptors", channelDescriptorDigest(d)+".json"), d)
}

// An old staged descriptor is only an equality witness for an already installed
// immutable floor. It can never select a source, authorize Apply or advance it.
func channelReleaseCurrentWithProof(parent string, d bootstrap.Descriptor, digest string, sequence uint64, installed string) (bool, error) {
	current, err := channelReleaseCurrent(d, digest, sequence, installed)
	if err == nil || d.Sequence != sequence || len(installed) != 64 {
		return current, err
	}
	if _, decodeErr := hex.DecodeString(installed); decodeErr != nil {
		return false, err
	}
	proofPath := filepath.Join(parent, "descriptors", installed+".json")
	if e := core.EnsurePrivateDirectory(filepath.Dir(proofPath)); e != nil {
		return false, err
	}
	info, e := os.Lstat(proofPath)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return false, err
	}
	b, e := update.ReadDocument(proofPath)
	if e != nil || len(b) > 65536 {
		return false, err
	}
	h := sha256.Sum256(bytes.TrimSuffix(b, []byte("\n")))
	var prior bootstrap.Descriptor
	if hex.EncodeToString(h[:]) != installed || update.StrictJSON(b, &prior) != nil || prior.Sequence != sequence || !bootstrap.ApplicationDescriptorMatches(prior, installed) || bootstrap.ApplicationDescriptorIdentity(prior) != digest || bootstrap.ApplicationDescriptorIdentity(d) != digest {
		return false, err
	}
	return true, nil
}

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
		e = p.BindApplicationRelease(release)
		if e != nil {
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
		current, e := channelReleaseCurrentWithProof(parent, descriptor, digest, installedSequence, installedRelease)
		if e != nil {
			return nil, e
		}
		if current {
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
		ownerRelease, e := channelMaintenanceRelease(descriptor, *got)
		if e != nil {
			return nil, e
		}
		// Bind against Prepare's independently verified full descriptor too:
		// delivery metadata may have changed between Inspect and acquisition.
		if e = p.BindApplicationRelease(ownerRelease); e != nil {
			return nil, e
		}
		if e = channelRememberDescriptor(parent, descriptor); e != nil {
			return nil, e
		}
		return p.ApplicationRelease()
	}
}

// An authenticated public head behind an installed candidate offers no update.
// It never authorizes fetching/applying that older executable. Equal-sequence
// replacement remains a hard identity error, including for staged builds.
func channelReleaseCurrent(descriptor bootstrap.Descriptor, digest string, installedSequence uint64, installedRelease string) (bool, error) {
	if descriptor.Sequence < installedSequence {
		return true, nil
	}
	if descriptor.Sequence == installedSequence {
		if digest != installedRelease && !bootstrap.ApplicationDescriptorMatches(descriptor, installedRelease) {
			return false, errors.New("release identity change refused")
		}
		return true, nil
	}
	return false, nil
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
