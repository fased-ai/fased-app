package releaseops

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const githubQuerySecret = "DO-NOT-RETAIN-GITHUB-QUERY-TOKEN"

type githubRequest struct{ host, path, accept string }
type githubFixture struct {
	t         *testing.T
	policy    GitHubPolicy
	asset     GitHubAsset
	payload   []byte
	metadata  []byte
	server    *httptest.Server
	ca        []byte
	mu        sync.Mutex
	requests  []githubRequest
	intercept func(http.ResponseWriter, *http.Request) bool
}

func newGitHubFixture(t *testing.T) *githubFixture {
	t.Helper()
	f := &githubFixture{t: t, payload: []byte("disposable archive bytes; never executed"), metadata: []byte(`{"signed":{"_type":"timestamp"},"signatures":[]}`)}
	f.policy = GitHubPolicy{Version: 1, Repository: "fased-ai/fased-app", RepositoryID: "7", MetadataBranch: "fixture-metadata", MetadataHost: "raw.githubusercontent.com", APIHost: "api.github.com", CDNHost: "release-assets.githubusercontent.com", CDNPathPrefix: "/github-production-release-asset/7/", APIVersion: GitHubAPIVersion}
	f.asset = GitHubAsset{RepositoryID: "7", ReleaseID: "11", AssetID: "17", Name: "fased-app-linux-amd64.tar.gz", SHA256: sha(f.payload), CDNPath: f.policy.CDNPathPrefix + "opaque-asset", Size: int64(len(f.payload))}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "disposable GitHub transport fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{f.policy.APIHost, f.policy.MetadataHost, f.policy.CDNHost}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	f.ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	f.server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	return f
}

func githubWrite(w http.ResponseWriter, b []byte, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

func (f *githubFixture) serve(w http.ResponseWriter, r *http.Request) {
	for _, key := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Range", "Referer"} {
		if r.Header.Get(key) != "" {
			f.t.Errorf("credential/header forwarded: %s", key)
		}
	}
	if r.Method != http.MethodGet || r.Header.Get("Accept-Encoding") != "identity" || r.TLS == nil || r.TLS.ServerName != r.Host {
		f.t.Error("request method, compression or TLS identity differs from pinned host")
	}
	if r.Host == f.policy.APIHost && r.Header.Get("X-GitHub-Api-Version") != GitHubAPIVersion {
		f.t.Error("GitHub API request omitted pinned version")
	}
	if r.Host == f.policy.APIHost && !strings.Contains(r.URL.Path, "/releases/assets/") && r.Header.Get("Accept") != "application/vnd.github+json" {
		f.t.Error("identity preflight is not an official GitHub JSON request")
	}
	if r.Host != f.policy.CDNHost && r.URL.RawQuery != "" {
		f.t.Error("query authority reached metadata or API")
	}
	f.mu.Lock()
	f.requests = append(f.requests, githubRequest{r.Host, r.URL.Path, r.Header.Get("Accept")})
	f.mu.Unlock()
	if f.intercept != nil && f.intercept(w, r) {
		return
	}
	switch {
	case r.Host == f.policy.MetadataHost && strings.HasPrefix(r.URL.Path, "/fased-ai/fased-app/fixture-metadata/"):
		githubWrite(w, f.metadata, "text/plain; charset=utf-8")
	case r.Host == f.policy.APIHost && r.URL.Path == "/repos/fased-ai/fased-app":
		// Official responses have many fields outside the identity contract.
		githubWrite(w, []byte(`{"id":7,"full_name":"fased-ai/fased-app","private":false,"owner":{"login":"fased-ai","id":3}}`), "application/json")
	case r.Host == f.policy.APIHost && r.URL.Path == "/repos/fased-ai/fased-app/releases/11":
		b, err := json.Marshal(map[string]any{"id": 11, "tag_name": "fixture", "draft": false, "assets": []any{map[string]any{"id": 17, "name": f.asset.Name, "size": f.asset.Size, "state": "uploaded", "content_type": "application/gzip", "browser_download_url": "https://github.com/fased-ai/fased-app/releases/download/fixture/" + f.asset.Name}}})
		if err != nil {
			f.t.Error(err)
			http.Error(w, "fixture encoding failed", http.StatusInternalServerError)
			return
		}
		githubWrite(w, b, "application/json")
	case r.Host == f.policy.APIHost && r.URL.Path == "/repos/fased-ai/fased-app/releases/assets/17":
		if r.Header.Get("Accept") != "application/octet-stream" {
			f.t.Error("asset locator is not a binary request")
		}
		w.Header().Set("Location", "https://"+f.policy.CDNHost+f.asset.CDNPath+"?jwt="+githubQuerySecret+"&response-content-disposition=attachment")
		w.WriteHeader(http.StatusFound)
	case r.Host == f.policy.CDNHost && r.URL.Path == f.asset.CDNPath:
		if r.URL.Query().Get("jwt") != githubQuerySecret {
			f.t.Error("transient signed query not preserved to exact CDN request")
		}
		githubWrite(w, f.payload, "application/octet-stream")
	default:
		f.t.Errorf("unexpected route %s%s", r.Host, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *githubFixture) lookup(_ context.Context, host string) ([]netip.Addr, error) {
	if host != f.policy.APIHost && host != f.policy.MetadataHost && host != f.policy.CDNHost {
		f.t.Error("unapproved DNS host", host)
		return nil, errors.New("unapproved fixture host")
	}
	return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
}

func (f *githubFixture) dial(ctx context.Context, network, address string) (net.Conn, error) {
	if address != "93.184.216.34:443" {
		f.t.Error("dial bypassed verified IP", address)
		return nil, errors.New("unchecked fixture dial")
	}
	return (&net.Dialer{}).DialContext(ctx, network, f.server.Listener.Addr().String())
}

func (f *githubFixture) wire() *GitHubTransport {
	f.t.Helper()
	g, err := NewControlledGitHubTransport(f.policy, f.ca, f.lookup, f.dial)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = g.Close() })
	return g
}

func githubSafeError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("unsafe GitHub request accepted")
	}
	for _, forbidden := range []string{githubQuerySecret, "https://", "jwt=", "credential-password", "response-content-disposition", "DO-NOT-RETAIN-BODY"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatal("request URL/query/body leaked in error")
		}
	}
}

func TestGitHubTransportExactAnonymousReadback(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "one-cdn-redirect", true: "api-200"}[direct], func(t *testing.T) {
			f := newGitHubFixture(t)
			t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
			t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
			t.Setenv("GITHUB_TOKEN", "DO-NOT-USE-ENV-TOKEN")
			t.Setenv("GH_TOKEN", "DO-NOT-USE-ENV-TOKEN")
			if direct {
				f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path != "/repos/fased-ai/fased-app/releases/assets/17" {
						return false
					}
					githubWrite(w, f.payload, "application/octet-stream")
					return true
				}
			}
			g := f.wire()
			got, err := g.Metadata(t.Context(), "timestamp.json")
			if err != nil || !bytes.Equal(got, f.metadata) {
				t.Fatal("exact metadata read failed", err)
			}
			got, err = g.Asset(t.Context(), f.asset)
			if err != nil || !bytes.Equal(got, f.payload) {
				t.Fatal("exact asset read failed", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			want := []githubRequest{{f.policy.MetadataHost, "/fased-ai/fased-app/fixture-metadata/timestamp.json", ""}, {f.policy.APIHost, "/repos/fased-ai/fased-app", ""}, {f.policy.APIHost, "/repos/fased-ai/fased-app/releases/11", ""}, {f.policy.APIHost, "/repos/fased-ai/fased-app/releases/assets/17", "application/octet-stream"}}
			if !direct {
				want = append(want, githubRequest{f.policy.CDNHost, f.asset.CDNPath, ""})
			}
			if len(f.requests) != len(want) {
				t.Fatal("wrong number of requests", len(f.requests))
			}
			for i := range want {
				if f.requests[i].host != want[i].host || f.requests[i].path != want[i].path || want[i].accept != "" && f.requests[i].accept != want[i].accept {
					t.Fatal("identity preflight order or exact route changed")
				}
			}
		})
	}
}

func TestGitHubTransportIdentityRefusals(t *testing.T) {
	variants := map[string]struct{ path, body string }{
		"repository-id":           {"/repos/fased-ai/fased-app", `{"id":8,"full_name":"fased-ai/fased-app"}`},
		"repository-name":         {"/repos/fased-ai/fased-app", `{"id":7,"full_name":"attacker/fased-app"}`},
		"repository-duplicate":    {"/repos/fased-ai/fased-app", `{"id":8,"id":7,"full_name":"fased-ai/fased-app"}`},
		"repository-alias":        {"/repos/fased-ai/fased-app", `{"id":8,"ID":7,"full_name":"fased-ai/fased-app"}`},
		"repository-string-id":    {"/repos/fased-ai/fased-app", `{"id":"7","full_name":"fased-ai/fased-app"}`},
		"repository-fraction":     {"/repos/fased-ai/fased-app", `{"id":7.1,"full_name":"fased-ai/fased-app"}`},
		"repository-null":         {"/repos/fased-ai/fased-app", `null`},
		"repository-trailing":     {"/repos/fased-ai/fased-app", `{"id":7,"full_name":"fased-ai/fased-app"} {}`},
		"release-id":              {"/repos/fased-ai/fased-app/releases/11", `{"id":12,"assets":[]}`},
		"release-missing-asset":   {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"assets":[]}`},
		"release-name":            {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"assets":[{"id":17,"name":"other.tar.gz","size":1,"state":"uploaded"}]}`},
		"release-size":            {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"assets":[{"id":17,"name":"fased-app-linux-amd64.tar.gz","size":1,"state":"uploaded"}]}`},
		"release-asset-id":        {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"assets":[{"id":18,"name":"fased-app-linux-amd64.tar.gz","size":1,"state":"uploaded"}]}`},
		"release-id-type":         {"/repos/fased-ai/fased-app/releases/11", `{"id":"11","assets":[]}`},
		"release-state":           {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"assets":[{"id":17,"name":"fased-app-linux-amd64.tar.gz","size":39,"state":"new"}]}`},
		"release-draft":           {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"draft":true,"assets":[{"id":17,"name":"fased-app-linux-amd64.tar.gz","size":1,"state":"uploaded"}]}`},
		"release-duplicate-match": {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"assets":[{"id":17,"name":"fased-app-linux-amd64.tar.gz","size":1,"state":"uploaded"},{"id":17,"name":"fased-app-linux-amd64.tar.gz","size":1,"state":"uploaded"}]}`},
		"release-duplicate-json":  {"/repos/fased-ai/fased-app/releases/11", `{"id":11,"id":12,"assets":[]}`},
	}
	for name, v := range variants {
		t.Run(name, func(t *testing.T) {
			f := newGitHubFixture(t)
			body := []byte(v.body)
			if name == "release-name" || name == "release-state" || name == "release-asset-id" || name == "release-draft" || name == "release-duplicate-match" {
				var doc map[string]any
				if err := json.Unmarshal(body, &doc); err != nil {
					t.Fatal(err)
				}
				for _, item := range doc["assets"].([]any) {
					item.(map[string]any)["size"] = f.asset.Size
				}
				var err error
				body, err = json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
			}
			var assetCalls atomic.Int64
			f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
				if strings.Contains(r.URL.Path, "/releases/assets/") {
					assetCalls.Add(1)
				}
				if r.URL.Path != v.path {
					return false
				}
				githubWrite(w, body, "application/json")
				return true
			}
			_, err := f.wire().Asset(t.Context(), f.asset)
			githubSafeError(t, err)
			if assetCalls.Load() != 0 {
				t.Fatal("binary request dispatched after identity refusal")
			}
		})
	}
}

func TestGitHubTransportRedirectRefusals(t *testing.T) {
	for _, variant := range []string{"repository", "release", "metadata", "extra-hop", "host", "host-suffix", "scheme", "userinfo", "fragment", "port", "wrong-path", "escaped-path", "relative", "empty-query", "oversize", "backslash", "newline"} {
		t.Run(variant, func(t *testing.T) {
			f := newGitHubFixture(t)
			dest := "https://" + f.policy.CDNHost + f.asset.CDNPath + "?jwt=" + githubQuerySecret
			switch variant {
			case "host":
				dest = "https://attacker.invalid" + f.asset.CDNPath + "?jwt=" + githubQuerySecret
			case "host-suffix":
				dest = "https://" + f.policy.CDNHost + ".attacker.invalid" + f.asset.CDNPath
			case "scheme":
				dest = strings.Replace(dest, "https:", "http:", 1)
			case "userinfo":
				dest = "https://user:credential-password@" + f.policy.CDNHost + f.asset.CDNPath + "?jwt=" + githubQuerySecret
			case "fragment":
				dest += "#fragment"
			case "port":
				dest = "https://" + f.policy.CDNHost + ":443" + f.asset.CDNPath + "?jwt=" + githubQuerySecret
			case "wrong-path":
				dest = "https://" + f.policy.CDNHost + f.policy.CDNPathPrefix + "different?jwt=" + githubQuerySecret
			case "escaped-path":
				dest = "https://" + f.policy.CDNHost + strings.Replace(f.asset.CDNPath, "opaque", "%6fpaque", 1) + "?jwt=" + githubQuerySecret
			case "relative":
				dest = f.asset.CDNPath + "?jwt=" + githubQuerySecret
			case "empty-query":
				dest = "https://" + f.policy.CDNHost + f.asset.CDNPath + "?"
			case "oversize":
				dest += strings.Repeat("x", 9000)
			case "backslash":
				dest = "https://" + f.policy.CDNHost + "\\attacker.invalid" + f.asset.CDNPath
			case "newline":
				dest = "https://" + f.policy.CDNHost + f.asset.CDNPath + "?jwt=" + githubQuerySecret + "%0a"
			}
			f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
				at := r.URL.Path == "/repos/fased-ai/fased-app/releases/assets/17"
				if variant == "repository" {
					at = r.URL.Path == "/repos/fased-ai/fased-app"
				}
				if variant == "release" {
					at = r.URL.Path == "/repos/fased-ai/fased-app/releases/11"
				}
				if variant == "metadata" {
					at = r.Host == f.policy.MetadataHost
				}
				if variant == "extra-hop" {
					at = at || r.Host == f.policy.CDNHost
				}
				if !at {
					return false
				}
				w.Header().Set("Location", dest)
				w.WriteHeader(http.StatusFound)
				return true
			}
			g := f.wire()
			var err error
			if variant == "metadata" {
				_, err = g.Metadata(t.Context(), "timestamp.json")
			} else {
				_, err = g.Asset(t.Context(), f.asset)
			}
			githubSafeError(t, err)
		})
	}
}

func TestGitHubTransportByteIntegrity(t *testing.T) {
	for _, variant := range []string{"hash", "short", "long", "length", "chunked", "partial", "encoding", "range", "oversize-header", "body-secret"} {
		t.Run(variant, func(t *testing.T) {
			f := newGitHubFixture(t)
			f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Host != f.policy.CDNHost {
					return false
				}
				b := append([]byte(nil), f.payload...)
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Length", strconv.Itoa(len(b)))
				switch variant {
				case "hash":
					b[0] ^= 1
				case "short":
					b = b[:len(b)-1]
				case "long":
					b = append(b, 'x')
					w.Header().Set("Content-Length", strconv.Itoa(len(b)))
				case "length":
					w.Header().Set("Content-Length", "134217729")
				case "chunked":
					w.Header().Del("Content-Length")
					w.(http.Flusher).Flush()
				case "partial":
					w.WriteHeader(http.StatusPartialContent)
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				case "range":
					w.Header().Set("Content-Range", "bytes 0-2/99")
				case "oversize-header":
					w.Header().Set("X-Large", strings.Repeat("x", 20<<10))
				case "body-secret":
					b = []byte("DO-NOT-RETAIN-BODY " + githubQuerySecret)
					w.Header().Set("Content-Length", strconv.Itoa(len(b)))
					w.WriteHeader(http.StatusForbidden)
				}
				_, _ = w.Write(b)
				return true
			}
			_, err := f.wire().Asset(t.Context(), f.asset)
			githubSafeError(t, err)
		})
	}
}

func TestGitHubTransportDNSRefusals(t *testing.T) {
	for _, name := range []string{"empty", "many", "invalid", "private", "loopback", "link-local", "tailnet", "test-v4", "benchmark", "multicast", "unspecified", "mapped-loopback", "test-v6", "6to4", "nat64", "zone-public", "mixed"} {
		t.Run(name, func(t *testing.T) {
			f := newGitHubFixture(t)
			answers := []netip.Addr{netip.MustParseAddr("93.184.216.34")}
			switch name {
			case "empty":
				answers = nil
			case "many":
				answers = make([]netip.Addr, 17)
			case "invalid":
				answers = []netip.Addr{{}}
			case "private":
				answers = []netip.Addr{netip.MustParseAddr("10.0.0.1")}
			case "loopback":
				answers = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
			case "link-local":
				answers = []netip.Addr{netip.MustParseAddr("169.254.169.254")}
			case "tailnet":
				answers = []netip.Addr{netip.MustParseAddr("100.64.0.1")}
			case "test-v4":
				answers = []netip.Addr{netip.MustParseAddr("192.0.2.1")}
			case "benchmark":
				answers = []netip.Addr{netip.MustParseAddr("198.18.0.1")}
			case "multicast":
				answers = []netip.Addr{netip.MustParseAddr("224.0.0.1")}
			case "unspecified":
				answers = []netip.Addr{netip.MustParseAddr("::")}
			case "mapped-loopback":
				answers = []netip.Addr{netip.MustParseAddr("::ffff:127.0.0.1")}
			case "test-v6":
				answers = []netip.Addr{netip.MustParseAddr("2001:db8::1")}
			case "6to4":
				answers = []netip.Addr{netip.MustParseAddr("2002:7f00:1::")}
			case "nat64":
				answers = []netip.Addr{netip.MustParseAddr("64:ff9b::7f00:1")}
			case "zone-public":
				answers = []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111%fixture")}
			case "mixed":
				answers = append(answers, netip.MustParseAddr("127.0.0.1"))
			}
			var dials atomic.Int64
			g, err := NewControlledGitHubTransport(f.policy, f.ca, func(context.Context, string) ([]netip.Addr, error) { return answers, nil }, func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("unchecked dial")
			})
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			_, err = g.Metadata(t.Context(), "timestamp.json")
			githubSafeError(t, err)
			if dials.Load() != 0 {
				t.Fatal("unsafe DNS answer reached dialer")
			}
		})
	}
}

func TestGitHubTransportPinsDNSWithoutReResolution(t *testing.T) {
	f := newGitHubFixture(t)
	var lookups atomic.Int64
	g, err := NewControlledGitHubTransport(f.policy, f.ca, func(context.Context, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}, f.dial)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if got, err := g.Metadata(t.Context(), "timestamp.json"); err != nil || !bytes.Equal(got, f.metadata) {
		t.Fatal("pinned lookup failed", err)
	}
	if lookups.Load() != 1 {
		t.Fatal("dial repeated DNS lookup")
	}
	if _, err := g.Metadata(t.Context(), "timestamp.json"); err == nil {
		t.Fatal("a later rebound answer was trusted")
	}
}

func TestGitHubTransportMetadataAndLifetimeRefusals(t *testing.T) {
	for _, path := range []string{"", "/timestamp.json", "../timestamp.json", "./timestamp.json", "a/../../timestamp.json", "timestamp.json?token=secret", "timestamp.json#fragment", "https://attacker.invalid/file", "targets/%2e%2e/secret", "targets\\timestamp.json", "timestamp.json\n"} {
		t.Run("path-"+strconv.Itoa(len(path))+strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			f := newGitHubFixture(t)
			_, err := f.wire().Metadata(t.Context(), path)
			githubSafeError(t, err)
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 0 {
				t.Fatal("invalid path reached network")
			}
		})
	}
	for _, name := range []string{"closed", "cancelled", "untrusted-ca"} {
		t.Run(name, func(t *testing.T) {
			f := newGitHubFixture(t)
			g := f.wire()
			ctx := t.Context()
			switch name {
			case "closed":
				if err := g.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "untrusted-ca":
				var err error
				g, err = NewControlledGitHubTransport(f.policy, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")}), f.lookup, f.dial)
				if err != nil {
					return
				}
				defer g.Close()
			}
			_, err := g.Metadata(ctx, "timestamp.json")
			githubSafeError(t, err)
		})
	}
}

func TestGitHubPolicyAndAssetBindings(t *testing.T) {
	f := newGitHubFixture(t)
	if err := f.policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := f.asset.Validate(f.policy); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*GitHubPolicy){
		"version":          func(p *GitHubPolicy) { p.Version = 0 },
		"repository":       func(p *GitHubPolicy) { p.Repository = "attacker/fased-app" },
		"repository-id":    func(p *GitHubPolicy) { p.RepositoryID = "07" },
		"zero-id":          func(p *GitHubPolicy) { p.RepositoryID = "0" },
		"overflow-id":      func(p *GitHubPolicy) { p.RepositoryID = "18446744073709551616" },
		"empty-branch":     func(p *GitHubPolicy) { p.MetadataBranch = "" },
		"traversal-branch": func(p *GitHubPolicy) { p.MetadataBranch = "../main" },
		"query-branch":     func(p *GitHubPolicy) { p.MetadataBranch = "main?secret=token" },
		"api-host":         func(p *GitHubPolicy) { p.APIHost = "api.fased.invalid" },
		"metadata-host":    func(p *GitHubPolicy) { p.MetadataHost = "raw.githubusercontent.com.attacker.invalid" },
		"cdn-host":         func(p *GitHubPolicy) { p.CDNHost = "https://release-assets.githubusercontent.com" },
		"cdn-port":         func(p *GitHubPolicy) { p.CDNHost += ":443" },
		"cdn-repository":   func(p *GitHubPolicy) { p.CDNPathPrefix = "/github-production-release-asset/8/" },
		"cdn-traversal":    func(p *GitHubPolicy) { p.CDNPathPrefix += "../" },
		"old-api-version":  func(p *GitHubPolicy) { p.APIVersion = "2022-11-28" },
	} {
		t.Run("policy-"+name, func(t *testing.T) {
			p := f.policy
			change(&p)
			githubSafeError(t, p.Validate())
			if g, err := NewGitHubTransport(p); err == nil {
				_ = g.Close()
				t.Fatal("invalid policy constructed transport")
			}
		})
	}
	for name, change := range map[string]func(*GitHubAsset){
		"repository":           func(a *GitHubAsset) { a.RepositoryID = "8" },
		"zero-release":         func(a *GitHubAsset) { a.ReleaseID = "0" },
		"release-alias":        func(a *GitHubAsset) { a.ReleaseID = "011" },
		"asset-id":             func(a *GitHubAsset) { a.AssetID = "17?query" },
		"overflow-id":          func(a *GitHubAsset) { a.AssetID = "18446744073709551616" },
		"traversal-name":       func(a *GitHubAsset) { a.Name = "../archive.tar.gz" },
		"query-name":           func(a *GitHubAsset) { a.Name += "?query" },
		"zero-size":            func(a *GitHubAsset) { a.Size = 0 },
		"large-size":           func(a *GitHubAsset) { a.Size = 256<<20 + 1 },
		"wrong-hash":           func(a *GitHubAsset) { a.SHA256 = "bad" },
		"uppercase-hash":       func(a *GitHubAsset) { a.SHA256 = strings.ToUpper(a.SHA256) },
		"wrong-cdn-repository": func(a *GitHubAsset) { a.CDNPath = "/github-production-release-asset/8/opaque" },
		"cdn-traversal":        func(a *GitHubAsset) { a.CDNPath = f.policy.CDNPathPrefix + "../opaque" },
		"cdn-query":            func(a *GitHubAsset) { a.CDNPath += "?jwt=" + githubQuerySecret },
		"cdn-escape":           func(a *GitHubAsset) { a.CDNPath = f.policy.CDNPathPrefix + "%6fpaque" },
	} {
		t.Run("asset-"+name, func(t *testing.T) {
			a := f.asset
			change(&a)
			githubSafeError(t, a.Validate(f.policy))
			g := f.wire()
			_, err := g.Asset(t.Context(), a)
			githubSafeError(t, err)
		})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 0 {
		t.Fatal("invalid policy/locator reached network")
	}
}

func TestGitHubTransportControlledHooksAreExplicit(t *testing.T) {
	f := newGitHubFixture(t)
	for _, which := range []string{"ca", "lookup", "dial"} {
		t.Run(which, func(t *testing.T) {
			ca, lookup, dial := f.ca, f.lookup, f.dial
			switch which {
			case "ca":
				ca = nil
			case "lookup":
				lookup = nil
			case "dial":
				dial = nil
			}
			if g, err := NewControlledGitHubTransport(f.policy, ca, lookup, dial); err == nil {
				_ = g.Close()
				t.Fatal("partial fixture hooks silently admitted public transport")
			}
		})
	}
	// The ordinary constructor accepts only policy; no fixture CA, dialer,
	// resolver, token or endpoint argument can be provided. Never fetch here.
	g, err := NewGitHubTransport(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubTransportMetadataResponseBounds(t *testing.T) {
	for _, name := range []string{"declared-large", "stream-large", "truncated", "encoding", "partial", "range", "header"} {
		t.Run(name, func(t *testing.T) {
			f := newGitHubFixture(t)
			f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Host != f.policy.MetadataHost {
					return false
				}
				b := append([]byte(nil), f.metadata...)
				switch name {
				case "declared-large":
					w.Header().Set("Content-Length", "65537")
				case "stream-large":
					b = bytes.Repeat([]byte("x"), 65537)
					w.(http.Flusher).Flush()
				case "truncated":
					w.Header().Set("Content-Length", strconv.Itoa(len(b)+1))
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				case "partial":
					w.WriteHeader(http.StatusPartialContent)
				case "range":
					w.Header().Set("Content-Range", "bytes 0-2/99")
				case "header":
					w.Header().Set("X-Large", strings.Repeat("x", 20<<10))
				}
				_, _ = w.Write(b)
				return true
			}
			_, err := f.wire().Metadata(t.Context(), "timestamp.json")
			githubSafeError(t, err)
		})
	}
}

func TestGitHubTransportMetadataRoutes(t *testing.T) {
	f := newGitHubFixture(t)
	g := f.wire()
	for _, name := range []string{"timestamp.json", "1.root.json", "1.snapshot.json", "1.targets.json", "targets/bootstrap/" + strings.Repeat("a", 64) + ".descriptor.json"} {
		if got, err := g.Metadata(t.Context(), name); err != nil || !bytes.Equal(got, f.metadata) {
			t.Fatal("valid pinned metadata path refused", name, err)
		}
	}
}

func TestGitHubTransportIdentityResponseBounds(t *testing.T) {
	for _, path := range []string{"/repos/fased-ai/fased-app", "/repos/fased-ai/fased-app/releases/11"} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			f := newGitHubFixture(t)
			f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != path {
					return false
				}
				w.Header().Set("Content-Length", "65537")
				_, _ = w.Write([]byte("DO-NOT-RETAIN-BODY"))
				return true
			}
			_, err := f.wire().Asset(t.Context(), f.asset)
			githubSafeError(t, err)
		})
	}
}

func TestGitHubTransportSanitizesNetworkErrors(t *testing.T) {
	for _, phase := range []string{"lookup", "dial", "cdn-dial"} {
		t.Run(phase, func(t *testing.T) {
			f := newGitHubFixture(t)
			var calls atomic.Int64
			lookup := f.lookup
			if phase == "lookup" {
				lookup = func(context.Context, string) ([]netip.Addr, error) {
					return nil, errors.New("https://private.invalid/?jwt=" + githubQuerySecret)
				}
			}
			dial := func(ctx context.Context, network, address string) (net.Conn, error) {
				if phase == "dial" || phase == "cdn-dial" && calls.Add(1) == 4 {
					return nil, errors.New("https://private.invalid/?jwt=" + githubQuerySecret)
				}
				return f.dial(ctx, network, address)
			}
			g, err := NewControlledGitHubTransport(f.policy, f.ca, lookup, dial)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			_, err = g.Asset(t.Context(), f.asset)
			githubSafeError(t, err)
		})
	}
}

func TestGitHubTransportFiniteRefreshBounds(t *testing.T) {
	for _, which := range []string{"deadline", "metadata", "roots", "assets"} {
		t.Run(which, func(t *testing.T) {
			f := newGitHubFixture(t)
			g := f.wire()
			g.mu.Lock()
			switch which {
			case "deadline":
				g.deadline = time.Now().Add(-time.Second)
			case "metadata":
				g.metadata = githubMaxMetadataFiles
			case "roots":
				g.roots = githubMaxRootSteps
			case "assets":
				g.assets = githubMaxAssets
			}
			g.mu.Unlock()
			var err error
			switch which {
			case "assets":
				_, err = g.Asset(t.Context(), f.asset)
			case "roots":
				_, err = g.Metadata(t.Context(), "1.root.json")
			default:
				_, err = g.Metadata(t.Context(), "timestamp.json")
			}
			githubSafeError(t, err)
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 0 {
				t.Fatal("expired/exhausted refresh dispatched a request")
			}
		})
	}
}

func TestGitHubTransportEncodedSignedQuery(t *testing.T) {
	f := newGitHubFixture(t)
	const token = "opaque/token+data="
	f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/repos/fased-ai/fased-app/releases/assets/17" {
			q := url.Values{"jwt": []string{token}, "response-content-disposition": []string{"attachment; filename=archive.tar.gz"}}
			w.Header().Set("Location", "https://"+f.policy.CDNHost+f.asset.CDNPath+"?"+q.Encode())
			w.WriteHeader(http.StatusFound)
			return true
		}
		if r.Host == f.policy.CDNHost {
			if r.URL.Query().Get("jwt") != token {
				t.Error("legitimate encoded query changed")
			}
			githubWrite(w, f.payload, "application/octet-stream")
			return true
		}
		return false
	}
	if got, err := f.wire().Asset(t.Context(), f.asset); err != nil || !bytes.Equal(got, f.payload) {
		t.Fatal("valid escaped signed query refused", err)
	}
}
