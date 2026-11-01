//go:build linux && fased_finalizer_vm

package bootstrap

// Explicit disposable-VM builds only. Ordinary app/installer builds cannot
// import this adapter. No runtime environment, endpoint or CA override exists.
import (
	"context"
	"errors"
	"net"
	"net/netip"

	"github.com/fased-ai/fased-app/internal/releaseops"
)

func FinalizerVMOptions(seed []byte, pin string, ca []byte, directory string) (Options, error) {
	s, err := ValidateProductionSeed(seed, pin)
	if err != nil {
		return Options{}, err
	}
	if s.GitHub.RepositoryID != "7" || s.GitHub.MetadataBranch != "release-metadata" || s.Provenance.OwnerID != "8" {
		return Options{}, errors.New("disposable VM trust identity required")
	}
	return Options{Seed: seed, SeedSHA256: pin, Directory: directory, githubFixture: func(p releaseops.GitHubPolicy) (*releaseops.GitHubTransport, error) {
		return releaseops.NewControlledGitHubTransport(p, ca, func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}, func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "93.184.216.34:443" {
				return nil, errors.New("fixed disposable VM dial required")
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:17449")
		})
	}}, nil
}
