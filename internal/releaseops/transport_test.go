package releaseops

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureServer(t *testing.T, h http.Handler) (*httptest.Server, []byte) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "disposable release fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{apiHost, assetHost, metadataHost}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewUnstartedServer(h)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
func fixtureWire(t *testing.T, s *httptest.Server, ca []byte) *transport {
	t.Helper()
	wire, e := newTransport(ca, func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}, func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "93.184.216.34:443" {
			t.Errorf("unchecked dial: %s", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(wire.wire.CloseIdleConnections)
	return wire
}
func TestReleaseTransportExactReadback(t *testing.T) {
	b := []byte(inertPrefix + "core")
	var calls atomic.Int64
	s, ca := fixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		for _, key := range []string{"Authorization", "Cookie", "Proxy-Authorization", "Range", "Referer"} {
			if r.Header.Get(key) != "" {
				t.Errorf("credential/header forwarded %s", key)
			}
		}
		if r.Method != "GET" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("unexpected request")
		}
		if r.Host == apiHost && r.URL.Path == "/repositories/7/releases/assets/1" {
			w.Header().Set("Location", "https://"+assetHost+"/asset/1?transient_token=secret")
			w.WriteHeader(302)
			return
		}
		if r.Host != assetHost || r.URL.Path != "/asset/1" {
			t.Error("wrong exact route")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	}))
	wire := fixtureWire(t, s, ca)
	got, e := wire.fetch(context.Background(), "7", object{"core.fixture", "1", int64(len(b)), sha(b)})
	if e != nil || string(got) != string(b) || calls.Load() != 2 {
		t.Fatal(e, calls.Load())
	}
	if wire.wire.Proxy != nil || wire.client.Jar != nil || !wire.wire.DisableCompression || !wire.wire.DisableKeepAlives || wire.wire.MaxResponseHeaderBytes != 16<<10 || wire.client.Timeout != 20*time.Second {
		t.Fatal("transport limits weakened")
	}
}
func TestReleaseTransportRefusals(t *testing.T) {
	names := []string{"cross-host", "wildcard", "downgrade", "userinfo", "fragment", "port", "path", "relative", "oversize-url", "extra-hop", "metadata-redirect", "encoding", "range", "partial", "chunked", "large-header", "wrong-hash", "untrusted-tls", "cancelled", "active-artifact"}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			b := []byte(inertPrefix + "core")
			const token = "DO-NOT-RETAIN-THIS-TOKEN"
			s, ca := fixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				dest := "https://" + assetHost + "/asset/1?token=" + token
				switch name {
				case "cross-host":
					dest = "https://evil.invalid/asset/1?" + token
				case "wildcard":
					dest = "https://sub." + assetHost + "/asset/1"
				case "downgrade":
					dest = "http://" + assetHost + "/asset/1"
				case "userinfo":
					dest = "https://user:password@" + assetHost + "/asset/1"
				case "fragment":
					dest += "#fragment"
				case "port":
					dest = "https://" + assetHost + ":443/asset/1"
				case "path":
					dest = "https://" + assetHost + "/other"
				case "relative":
					dest = "/asset/1"
				case "oversize-url":
					dest += strings.Repeat("a", 8192)
				}
				if r.Host == apiHost || name == "metadata-redirect" || name == "extra-hop" {
					w.Header().Set("Location", dest)
					w.WriteHeader(302)
					return
				}
				switch name {
				case "encoding":
					w.Header().Set("Content-Encoding", "gzip")
				case "range":
					w.Header().Set("Content-Range", "bytes 0-2/3")
				case "partial":
					w.WriteHeader(206)
					return
				case "chunked":
					w.(http.Flusher).Flush()
					_, _ = w.Write(b)
					return
				case "large-header":
					w.Header().Set("X-Large", strings.Repeat("x", 17<<10))
				case "wrong-hash":
					b[len(b)-1] ^= 1
				case "active-artifact":
					b = []byte(strings.Repeat("x", len(b)))
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(b)))
				_, _ = w.Write(b)
			}))
			wire := fixtureWire(t, s, ca)
			if name == "untrusted-tls" {
				wire = fixtureWire(t, s, nil)
			}
			o := object{"core.fixture", "1", int64(len(b)), sha(b)}
			if name == "metadata-redirect" {
				o.AssetID = ""
				o.Name = "root.json"
			}
			if name == "active-artifact" {
				o.SHA256 = sha([]byte(strings.Repeat("x", len(b))))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "cancelled" {
				cancel()
			}
			got, e := wire.fetch(ctx, "7", o)
			if e == nil || got != nil {
				t.Fatal("unsafe transport accepted", name)
			}
			if strings.Contains(e.Error(), token) || strings.Contains(e.Error(), "https://") || strings.Contains(e.Error(), "password") {
				t.Fatal("token/URL leaked", e)
			}
		})
	}
}
func TestReleaseTransportDNSAndRebinding(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "224.0.0.1", "0.0.0.0", "::", "::1", "::7f00:1", "fec0::1", "fc00::1", "fe80::1", "2001:db8::1", "2002:7f00:1::", "64:ff9b::7f00:1", "::ffff:127.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			var dials atomic.Int64
			w, e := newTransport(nil, func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr(ip)}, nil
			}, func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, fmt.Errorf("unexpected dial")
			})
			if e != nil {
				t.Fatal(e)
			}
			defer w.wire.CloseIdleConnections()
			_, e = w.wire.DialContext(context.Background(), "tcp", apiHost+":443")
			if e == nil || dials.Load() != 0 {
				t.Fatal("special/mixed DNS dialed", e)
			}
		})
	}
	var lookups int
	w, e := newTransport(nil, func(context.Context, string) ([]netip.Addr, error) {
		lookups++
		if lookups > 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		if address != "93.184.216.34:443" {
			t.Error("DNS rebound", address)
		}
		return nil, fmt.Errorf("controlled end")
	})
	if e != nil {
		t.Fatal(e)
	}
	defer w.wire.CloseIdleConnections()
	_, _ = w.wire.DialContext(context.Background(), "tcp", apiHost+":443")
	if lookups != 1 {
		t.Fatal("second DNS lookup")
	}
	for _, answers := range [][]netip.Addr{nil, make([]netip.Addr, 17)} {
		w, e := newTransport(nil, func(context.Context, string) ([]netip.Addr, error) { return answers, nil }, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, e = w.wire.DialContext(context.Background(), "tcp", apiHost+":443")
		w.wire.CloseIdleConnections()
		if e == nil {
			t.Fatal("unbounded or empty DNS")
		}
	}
}
func TestReleaseTransportBindingLimits(t *testing.T) {
	w, e := newTransport(nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer w.wire.CloseIdleConnections()
	for _, o := range []object{{"../core.fixture", "1", 1, sha(nil)}, {"core.fixture", "01", 1, sha(nil)}, {"core.fixture", "1", 0, sha(nil)}, {"core.fixture", "1", 256<<20 + 1, sha(nil)}, {"root.json", "", 64<<10 + 1, sha(nil)}, {"core.fixture", "1", 1, "bad"}, {"archive.tar.gz/../../x", "1", 1, sha(nil)}} {
		if _, e := w.fetch(context.Background(), "7", o); e == nil {
			t.Fatal("invalid binding", o)
		}
	}
	for _, raw := range []string{"https://" + assetHost + "/asset/1#x", "https://" + assetHost + "/asset%2f1", "https://" + assetHost + "/a/../asset/1"} {
		if _, e := parsed(raw, assetHost, "/asset/1", true); e == nil {
			t.Fatal("ambiguous redirect path")
		}
	}
}
