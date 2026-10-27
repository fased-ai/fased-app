// Package releaseops rehearses release operations with disposable, inert inputs.
// It is not imported by the application, signer, installer or updater. The exact
// .invalid hosts below deliberately cannot supply production release transport.
package releaseops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const purpose = "fased-app-release-operations-fixture:v1"
const inertPrefix = "FASED-INERT-ARTIFACT:v1\n"
const apiHost, assetHost, metadataHost = "api.fased.invalid", "assets.fased.invalid", "metadata.fased.invalid"

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func hashOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func number(s string) bool {
	n, e := strconv.ParseUint(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatUint(n, 10) == s
}

type resolver func(context.Context, string) ([]netip.Addr, error)
type dialer func(context.Context, string, string) (net.Conn, error)
type transport struct {
	client *http.Client
	wire   *http.Transport
}

// Defense in depth beyond IsGlobalUnicast (which includes several special-use
// ranges). Reject an entire DNS answer if any address is unsafe. Dial the checked
// IP itself; a second lookup must never select an unchecked address.
func publicIP(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	if a.Is6() && !netip.MustParsePrefix("2000::/3").Contains(a) {
		return false
	}
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return true
}

func newTransport(ca []byte, lookup resolver, dial dialer) (*transport, error) {
	if lookup == nil {
		lookup = func(c context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(c, "ip", h)
		}
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: 20 * time.Second}).DialContext
	}
	roots, e := x509.SystemCertPool()
	if e != nil {
		return nil, errors.New("fixture TLS roots unavailable")
	}
	if len(ca) > 64<<10 || len(ca) > 0 && !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid controlled fixture CA")
	}
	w := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 20 * time.Second, TLSHandshakeTimeout: 20 * time.Second, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	w.DialContext = func(c context.Context, network, address string) (net.Conn, error) {
		h, p, e := net.SplitHostPort(address)
		if e != nil || p != "443" || h != apiHost && h != assetHost && h != metadataHost {
			return nil, errors.New("fixture dial host refused")
		}
		ips, e := lookup(c, h)
		if e != nil || len(ips) == 0 || len(ips) > 16 {
			return nil, errors.New("fixture DNS refused")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("fixture special-address DNS refused")
			}
		}
		return dial(c, network, net.JoinHostPort(ips[0].Unmap().String(), "443"))
	}
	return &transport{wire: w, client: &http.Client{Transport: w, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func parsed(raw, host, path string, query bool) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 8192 || u.Scheme != "https" || u.Host != host || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || u.Path != path || !query && (u.RawQuery != "" || u.ForceQuery) {
		return nil, errors.New("fixture URL refused")
	}
	return u, nil
}

type object struct {
	Name    string `json:"name"`
	AssetID string `json:"assetId,omitempty"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// Only the repository-bound API locator can redirect, once, to the one exact
// fixture CDN host/path. CDN queries exist only in memory; errors never include
// request URLs, response bodies, Location, TLS tokens or net/http error strings.
func (t *transport) fetch(ctx context.Context, repository string, o object) ([]byte, error) {
	limit := int64(64 << 10)
	host, path := metadataHost, "/metadata/"+o.Name
	if o.AssetID != "" {
		limit = 256 << 20
		host = apiHost
		path = "/repositories/" + repository + "/releases/assets/" + o.AssetID
	}
	if !number(repository) || !hashOK(o.SHA256) || o.Size <= 0 || o.Size > limit || !safeName(o.Name) || o.AssetID != "" && !number(o.AssetID) {
		return nil, errors.New("fixture object binding refused")
	}
	u, _ := url.Parse("https://" + host + path)
	for hop := 0; hop < 2; hop++ {
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return nil, errors.New("fixture request refused")
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Accept", "application/octet-stream")
		r, e := t.client.Do(req)
		if e != nil {
			return nil, errors.New("fixture HTTPS response unavailable")
		}
		if r.StatusCode == 301 || r.StatusCode == 302 || r.StatusCode == 303 || r.StatusCode == 307 || r.StatusCode == 308 {
			location := r.Header.Get("Location")
			_ = r.Body.Close()
			if o.AssetID == "" || hop != 0 {
				return nil, errors.New("fixture redirect refused")
			}
			u, e = parsed(location, assetHost, "/asset/"+o.AssetID, true)
			if e != nil {
				return nil, e
			}
			continue
		}
		if r.StatusCode != http.StatusOK || r.Header.Get("Content-Encoding") != "" || r.Header.Get("Content-Range") != "" || r.ContentLength != o.Size {
			_ = r.Body.Close()
			return nil, errors.New("fixture incomplete or encoded response refused")
		}
		b, e := io.ReadAll(io.LimitReader(r.Body, o.Size+1))
		ce := r.Body.Close()
		if e != nil || ce != nil || int64(len(b)) != o.Size || sha(b) != o.SHA256 {
			return nil, errors.New("fixture complete size/hash refused")
		}
		if o.AssetID != "" && (len(b) > 64<<10 || !bytes.HasPrefix(b, []byte(inertPrefix))) {
			return nil, errors.New("only inert fixture artifacts permitted")
		}
		return b, nil
	}
	return nil, errors.New("fixture redirect budget exhausted")
}

func safeName(s string) bool {
	if s == "" || len(s) > 64 || strings.Contains(s, "..") {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}
