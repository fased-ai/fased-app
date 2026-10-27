package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/releaseops"
)

func controlledGitHubServer(t *testing.T, handler http.Handler) (*httptest.Server, []byte) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"raw.githubusercontent.com", "api.github.com", "release-assets.githubusercontent.com"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(handler)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestGitHubBootstrapSignedTransportJoin(t *testing.T) {
	for _, variant := range []string{"complete", "ordinary-disabled", "wrong-signed-repository", "wrong-signed-size", "wrong-signed-hash", "wrong-policy-prefix", "legacy-upgrade", "bad-CDN-bytes", "metadata-redirect"} {
		t.Run(variant, func(t *testing.T) {
			f := newFirstFixture(t)
			f.seed.Version, f.seed.Purpose, f.seed.Origin = 2, GitHubPurpose, ""
			policy := releaseops.GitHubPolicy{Version: 1, Repository: "fased-ai/fased-app", RepositoryID: "7", MetadataBranch: "fixture-metadata", MetadataHost: "raw.githubusercontent.com", APIHost: "api.github.com", CDNHost: "release-assets.githubusercontent.com", CDNPathPrefix: "/github-production-release-asset/7/", APIVersion: releaseops.GitHubAPIVersion}
			f.seed.GitHub = policy
			f.descriptor.Binding = f.seed.Binding
			asset := releaseops.GitHubAsset{RepositoryID: "7", ReleaseID: "11", AssetID: "17", Name: archiveTarget, Size: int64(len(f.archive)), SHA256: digest(f.archive), CDNPath: policy.CDNPathPrefix + "fixture-artifact"}
			f.descriptor.GitHub = asset
			switch variant {
			case "wrong-signed-repository":
				f.descriptor.GitHub.RepositoryID = "8"
			case "wrong-signed-size":
				f.descriptor.GitHub.Size++
			case "wrong-signed-hash":
				f.descriptor.GitHub.SHA256 = strings.Repeat("0", 64)
			case "wrong-policy-prefix":
				f.seed.GitHub.CDNPathPrefix = "/github-production-release-asset/8/"
			case "legacy-upgrade":
				f.seed.Version, f.seed.Purpose, f.seed.Origin = 1, Purpose, f.server.URL
				f.descriptor.Binding = f.seed.Binding
			}
			f.publish()
			var hits atomic.Int64
			server, ca := controlledGitHubServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
					t.Error("credential reached GitHub fixture")
				}
				switch r.Host {
				case policy.MetadataHost:
					if variant == "metadata-redirect" {
						http.Redirect(w, r, "https://"+policy.CDNHost+asset.CDNPath+"?token=transient", http.StatusFound)
						return
					}
					rel, ok := strings.CutPrefix(r.URL.Path, "/"+policy.Repository+"/"+policy.MetadataBranch)
					if !ok {
						http.NotFound(w, r)
						return
					}
					b := f.files[rel]
					if b == nil {
						http.NotFound(w, r)
						return
					}
					w.Write(b)
				case policy.APIHost:
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/repos/fased-ai/fased-app":
						json.NewEncoder(w).Encode(map[string]any{"id": 7, "full_name": policy.Repository, "private": false})
					case "/repos/fased-ai/fased-app/releases/11":
						json.NewEncoder(w).Encode(map[string]any{"id": 11, "assets": []any{map[string]any{"id": 17, "name": asset.Name, "size": asset.Size, "state": "uploaded"}}})
					case "/repos/fased-ai/fased-app/releases/assets/17":
						http.Redirect(w, r, "https://"+policy.CDNHost+asset.CDNPath+"?jwt=transient-fixture-secret", http.StatusFound)
					default:
						http.NotFound(w, r)
					}
				case policy.CDNHost:
					if r.URL.Path != asset.CDNPath {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Length", strconv.Itoa(len(f.archive)))
					if variant == "bad-CDN-bytes" {
						w.Write([]byte(strings.Repeat("x", len(f.archive))))
					} else {
						w.Write(f.archive)
					}
				default:
					t.Error("unexpected host")
					http.NotFound(w, r)
				}
			}))
			o := f.options()
			o.TLSCA = nil
			o.githubFixture = func(p releaseops.GitHubPolicy) (*releaseops.GitHubTransport, error) {
				return releaseops.NewControlledGitHubTransport(p, ca, func(context.Context, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
				}, func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != "93.184.216.34:443" {
						t.Error("unchecked/rebound IP", address)
					}
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				})
			}
			if variant == "ordinary-disabled" {
				o.githubFixture = nil
			}
			p, err := Prepare(t.Context(), o)
			if variant != "complete" {
				if err == nil {
					p.Close()
					t.Fatal("invalid GitHub bootstrap accepted")
				}
				if strings.Contains(err.Error(), "transient") {
					t.Fatal("CDN token leaked")
				}
				if _, e := os.Stat(f.marker); !os.IsNotExist(e) {
					t.Fatal("unverified GitHub bytes executed")
				}
				if _, e := os.Stat(o.Directory); !os.IsNotExist(e) {
					t.Fatal("failed GitHub bootstrap left output")
				}
				if variant == "ordinary-disabled" && hits.Load() != 0 {
					t.Fatal("ordinary GitHub activation made a request")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if !p.Report().GitHubTransport || p.Report().Purpose != GitHubPurpose || p.Report().PublicActivation {
				t.Fatal(p.Report())
			}
			if _, e := os.Stat(f.marker); !os.IsNotExist(e) {
				t.Fatal("verification alone executed GitHub bundle")
			}
			if err = p.Run(t.Context(), InstallOptions{Prefix: filepath.Join(f.parent, "app"), DataDirectory: filepath.Join(f.parent, "data"), Start: false}, nil, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			if b, e := os.ReadFile(f.marker); e != nil || string(b) != "verified installer\n" {
				t.Fatal("verified GitHub handoff failed", e)
			}
			if err := filepath.WalkDir(o.Directory, func(path string, entry os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if !entry.IsDir() {
					b, e := os.ReadFile(path)
					if e != nil {
						return e
					}
					if strings.Contains(string(b), "transient-fixture-secret") {
						t.Error("CDN query token persisted", path)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
