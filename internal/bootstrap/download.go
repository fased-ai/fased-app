package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseops"
	"github.com/fased-ai/fased-app/internal/update"
)

// No CLI, seed field or environment variable can enable ordinary dispatch.
const productionGitHubActivationEnabled = false

type downloadSource struct {
	metadata func(context.Context, string, int64) ([]byte, error)
	archive  func(context.Context, Descriptor) (io.ReadCloser, error)
	close    func()
	window   time.Duration
}

func initialDownloads(o Options, seed Seed) (downloadSource, error) {
	if seed.Version == 1 {
		client, transport, err := update.DevelopmentAcquisitionClient(o.TLSCA)
		if err != nil {
			return downloadSource{}, err
		}
		return downloadSource{metadata: func(ctx context.Context, name string, limit int64) ([]byte, error) {
			return fetch(ctx, client, seed.Origin+"/"+name, limit)
		}, archive: func(ctx context.Context, d Descriptor) (io.ReadCloser, error) {
			r, err := update.DevelopmentAcquisitionResponse(ctx, client, seed.Origin+"/targets/"+d.Archive.SHA256+"."+d.Archive.File, d.Archive.Size)
			if err != nil {
				return nil, err
			}
			return r.Body, nil
		}, close: transport.CloseIdleConnections, window: time.Minute}, nil
	}
	if len(o.TLSCA) != 0 {
		return downloadSource{}, errors.New("GitHub profile cannot use the legacy TLS override")
	}
	var g *releaseops.GitHubTransport
	var err error
	if o.githubFixture != nil {
		g, err = o.githubFixture(seed.GitHub)
	} else {
		if !productionGitHubActivationEnabled {
			return downloadSource{}, errors.New("ordinary GitHub bootstrap activation is disabled")
		}
		g, err = releaseops.NewGitHubTransport(seed.GitHub)
	}
	if err != nil {
		return downloadSource{}, err
	}
	return downloadSource{metadata: func(ctx context.Context, name string, limit int64) ([]byte, error) {
		b, err := g.Metadata(ctx, name)
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > limit {
			return nil, errors.New("GitHub metadata exceeds signed bound")
		}
		return b, nil
	}, archive: func(ctx context.Context, d Descriptor) (io.ReadCloser, error) {
		b, err := g.Asset(ctx, d.GitHub)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(b)), nil
	}, close: func() { g.Close() }, window: 2 * time.Minute}, nil
}
