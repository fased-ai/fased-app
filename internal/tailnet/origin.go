package tailnet

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
)

var privateDNS = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*\.ts\.net$`)

// Origin accepts the canonical Tailscale HTTPS hostname and, for an isolated
// Serve endpoint, an explicit unprivileged TCP port. It admits no other host.
func Origin(value string) (string, string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != value || !privateDNS.MatchString(u.Hostname()) {
		return "", "", errors.New("exact private Tailscale HTTPS origin required")
	}
	port := u.Port()
	if port == "" {
		if u.Host != u.Hostname() {
			return "", "", errors.New("invalid private port")
		}
		return u.Hostname(), "443", nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1024 || n > 65535 || strconv.Itoa(n) != port || u.Host != u.Hostname()+":"+port {
		return "", "", errors.New("invalid private port")
	}
	return u.Hostname(), port, nil
}
