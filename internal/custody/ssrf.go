package custody

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/netguard"
)

// IsPublicUnicastIP delegates to the unified netguard IP classifier.
func IsPublicUnicastIP(raw netip.Addr) bool {
	return netguard.IsPublicUnicastIP(raw)
}

func isLoopbackHostname(host string) bool {
	return netguard.IsLoopbackHostname(host)
}

func isLoopbackURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return isLoopbackHostname(u.Hostname())
}

// NewSafeTransport constructs an HTTP transport backed by internal/netguard.
func NewSafeTransport(allowLoopback bool) *http.Transport {
	return netguard.NewSafeTransport(netguard.Options{
		AllowLoopback:       allowLoopback,
		DialTimeout:         5 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
	})
}

func dialSSRFSafe(
	ctx context.Context,
	network, address string,
	allowLoopback bool,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	dial func(context.Context, string, string) (net.Conn, error),
) (net.Conn, error) {
	return netguard.SafeDialContext(netguard.Options{
		AllowLoopback: allowLoopback,
		Lookup:        lookup,
		Dial:          dial,
	})(ctx, network, address)
}
