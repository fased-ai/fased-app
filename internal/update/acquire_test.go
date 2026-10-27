package update_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestAcquireDevelopmentHTTPS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private acquisition directory ACL acceptance is not available on Windows")
	}
	for _, name := range []string{"complete", "trust-substitution", "no-trust-pin", "no-development", "untrusted-tls", "wrong-host", "invalid-ca", "http", "wrong-origin", "credentials", "query", "fragment", "escaped-path", "manifest-redirect", "artifact-redirect", "expired", "future", "replay", "bad-signature", "unknown-key", "wrong-target", "missing-pair", "oversize-document", "compressed", "partial-response", "short-artifact", "long-artifact", "altered-artifact", "missing-artifact", "reserved-name", "foreign-output", "symlink-output", "public-parent", "expired-during-fetch", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			m, inv, tr, key := fixture(t)
			now := time.Now().Unix()
			m.IssuedAt, m.ExpiresAt = now-10, now+300
			tr.Keys[0].NotBefore, tr.Keys[0].NotAfter = now-60, now+600
			parent := t.TempDir()
			if err := os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(parent, "acquired")
			var mu sync.Mutex
			requests := []string{}
			var envelope []byte
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.URL.Path)
				mu.Unlock()
				if r.URL.Path == "/release/manifest.json" {
					switch name {
					case "manifest-redirect":
						http.Redirect(w, r, "/other", http.StatusFound)
						return
					case "oversize-document":
						_, _ = w.Write([]byte(strings.Repeat("x", update.MaxDocument+1)))
						return
					case "compressed":
						w.Header().Set("Content-Encoding", "gzip")
					case "partial-response":
						w.Header().Set("Content-Range", "bytes 0-9/100")
					}
					_, _ = w.Write(envelope)
					return
				}
				if name == "cancelled" {
					<-r.Context().Done()
					return
				}
				if r.URL.Path == "/release/signer" {
					switch name {
					case "expired-during-fetch":
						time.Sleep(time.Until(time.Unix(m.ExpiresAt, 0)))
					case "artifact-redirect":
						http.Redirect(w, r, "https://untrusted.invalid/signer", http.StatusTemporaryRedirect)
						return
					case "missing-artifact":
						http.NotFound(w, r)
						return
					case "short-artifact":
						_, _ = w.Write([]byte("ab"))
						return
					case "long-artifact":
						_, _ = w.Write([]byte("abcd"))
						return
					case "altered-artifact":
						_, _ = w.Write([]byte("bad"))
						return
					}
				}
				_, _ = w.Write([]byte("abc"))
			}))
			defer server.Close()
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			if _, err := x509.ParseCertificate(server.Certificate().Raw); err != nil {
				t.Fatal(err)
			}
			opts := update.AcquisitionOptions{Development: true, Origin: server.URL, ManifestURL: server.URL + "/release/manifest.json", Trust: j(tr), TrustSHA256: sha(j(tr)), Inventory: j(inv), Directory: output, TLSCA: ca}
			switch name {
			case "trust-substitution":
				opts.TrustSHA256 = strings.Repeat("0", 64)
			case "no-trust-pin":
				opts.TrustSHA256 = ""
			case "no-development":
				opts.Development = false
			case "untrusted-tls":
				opts.TLSCA = nil
			case "wrong-host":
				opts.Origin = strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
				opts.ManifestURL = opts.Origin + "/release/manifest.json"
			case "invalid-ca":
				opts.TLSCA = []byte("not a certificate")
			case "http":
				opts.ManifestURL = strings.Replace(opts.ManifestURL, "https:", "http:", 1)
			case "wrong-origin":
				opts.Origin = "https://untrusted.invalid"
			case "credentials":
				opts.ManifestURL = strings.Replace(opts.ManifestURL, "https://", "https://secret@", 1)
			case "query":
				opts.ManifestURL += "?secret=value"
			case "fragment":
				opts.ManifestURL += "#other"
			case "escaped-path":
				opts.ManifestURL = server.URL + "/release/%2e%2e/manifest.json"
			case "expired":
				m.ExpiresAt = now
			case "future":
				m.IssuedAt = now + 100
			case "expired-during-fetch":
				m.ExpiresAt = now + 2
			case "replay":
				inv.SequenceFloor = m.Sequence
				opts.Inventory = j(inv)
			case "wrong-target":
				m.Target.OS = "windows"
			case "missing-pair":
				m.Components = m.Components[:1]
			case "unknown-key":
				pub, next, e := ed25519.GenerateKey(rand.Reader)
				if e != nil {
					t.Fatal(e)
				}
				tr.Keys[0].ID, tr.Keys[0].PublicKey, key = update.KeyID(pub), pub, next
			case "reserved-name":
				m.Components[0].Artifact.File = "acquisition.json"
			case "foreign-output", "symlink-output":
				foreign := filepath.Join(parent, "foreign")
				if err := os.Mkdir(foreign, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(foreign, "keep"), []byte("owner"), 0600); err != nil {
					t.Fatal(err)
				}
				if name == "foreign-output" {
					opts.Directory = foreign
				} else if err := os.Symlink(foreign, output); err != nil {
					t.Fatal(err)
				}
			case "public-parent":
				if err := os.Chmod(parent, 0755); err != nil {
					t.Fatal(err)
				}
			}
			envelope = signed(m, tr, key)
			if name == "bad-signature" {
				envelope = []byte(strings.Replace(string(envelope), `"sequence":"2"`, `"sequence":"3"`, 1))
			}
			ctx := context.Background()
			if name == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer cancel()
			}
			result, err := update.AcquireDevelopment(ctx, opts)
			if name == "complete" {
				if err != nil || !result.DevelopmentOnly || result.TrustSHA256 != opts.TrustSHA256 || !result.Plan.ArtifactsVerified || result.Plan.InstallAllowed || result.Plan.UpdaterEnabled || result.Plan.ResumeAllowed || result.Plan.WalletSigner || result.Plan.FinancialExecution || result.Plan.Sequence != 2 {
					t.Fatalf("unexpected authority/result: %+v %v", result, err)
				}
				entries, err := os.ReadDir(output)
				if err != nil || len(entries) != 6 {
					t.Fatalf("incomplete output: %v %v", entries, err)
				}
				for _, f := range entries {
					info, err := f.Info()
					if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
						t.Fatalf("unsafe output permissions: %v %v", info, err)
					}
				}
				if _, err := update.Inspect(envelope, opts.Trust, opts.Inventory, output, time.Now().Unix()); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || result.Plan.ArtifactsVerified {
				t.Fatalf("unsafe acquisition accepted: %+v %v", result, err)
			}
			if name == "foreign-output" || name == "symlink-output" {
				b, err := os.ReadFile(filepath.Join(parent, "foreign", "keep"))
				if err != nil || string(b) != "owner" {
					t.Fatal("foreign output modified")
				}
			} else if _, e := os.Lstat(output); !os.IsNotExist(e) {
				t.Fatalf("partial output retained: %v", e)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, p := range requests {
				if p == "/other" {
					t.Fatal("redirect followed")
				}
				for _, beforeArtifacts := range []string{"expired", "future", "replay", "bad-signature", "unknown-key", "wrong-target", "missing-pair", "reserved-name"} {
					if name == beforeArtifacts && p != "/release/manifest.json" {
						t.Fatal("unauthenticated/incompatible manifest fetched an artifact", requests)
					}
				}
			}
			for _, early := range []string{"trust-substitution", "no-trust-pin", "no-development", "http", "wrong-origin", "credentials", "query", "escaped-path", "foreign-output", "symlink-output", "public-parent"} {
				if early == name && len(requests) != 0 {
					t.Fatal("untrusted input caused network request", requests)
				}
			}
		})
	}
}
