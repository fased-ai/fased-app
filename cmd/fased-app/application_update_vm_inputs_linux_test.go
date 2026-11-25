//go:build linux

package main

// Shared disposable-VM fixture composition inputs. None of these bindings or
// helpers exist in the product executable.
import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

const applicationVMInputs = "/var/lib/fased-update-fixture"
const applicationVMVersion = "0.0.0-b01-vm.2"

type applicationVMFinalizerComposition struct {
	Dir                         string
	Now                         time.Time
	Seed                        bootstrap.Seed
	Manifest                    bundle.Manifest
	Keys                        map[string][]signature.Signer
	Archive, Source, Target, CA []byte
	Put                         func(string, []byte, os.FileMode)
}
type applicationVMFinalizerHooks struct {
	Finish  func(*testing.T, applicationVMFinalizerComposition)
	Options func(string, []byte, []byte, applicationVMTrust) (bootstrap.Options, error)
	Handler func(applicationVMTrust) (http.Handler, error)
}

var applicationVMFinalizer *applicationVMFinalizerHooks

type applicationVMTrust struct {
	Finalized        bool   `json:"finalized,omitempty"`
	DescriptorSHA256 string `json:"descriptorSha256,omitempty"`
	ProvenanceSHA256 string `json:"provenanceSha256,omitempty"`
	PlanSHA256       string `json:"planSha256,omitempty"`

	Connected          bool   `json:"connected,omitempty"`
	SeedSHA256         string `json:"seedSha256"`
	CASHA256           string `json:"caSha256"`
	SourceSHA256       string `json:"sourceSha256"`
	TargetSHA256       string `json:"targetSha256"`
	TargetSignerSHA256 string `json:"targetSignerSha256,omitempty"`
}

func applicationVMDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func applicationVMJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}

// TestApplicationUpdateVMInputs produces a finite repository signed by fresh
// disposable threshold keys. Signing keys never leave memory. Only synthetic
// TLS key material is retained until the disposable guest/evidence is retired.
func TestApplicationUpdateVMInputs(t *testing.T) {
	dir := os.Getenv("FASED_UPDATE_FIXTURE_INPUTS")
	next := os.Getenv("FASED_UPDATE_FIXTURE_TARGET")
	if dir == "" || next == "" {
		t.Skip("explicit disposable fixture composition only")
	}
	if os.Getuid() == 0 || !filepath.IsAbs(dir) {
		t.Fatal("ordinary owner and absolute fixture root required")
	}
	finalized := os.Getenv("FASED_UPDATE_FIXTURE_FINALIZER") == "1"
	if finalized && (applicationVMFinalizer == nil || os.Getenv("FASED_UPDATE_FIXTURE_CONNECTED") == "1") {
		t.Fatal("explicit finalizer fixture build and interrupted-owner path required")
	}

	put := func(name string, b []byte, mode os.FileMode) {
		t.Helper()
		p := filepath.Join(dir, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, b, mode); e != nil {
			t.Fatal(e)
		}
	}
	target, e := os.ReadFile(next)
	if e != nil {
		t.Fatal(e)
	}
	source, e := os.ReadFile(filepath.Join(dir, "fased-app-tests"))
	if e != nil {
		t.Fatal(e)
	}
	if applicationVMDigest(source) == applicationVMDigest(target) {
		t.Fatal("target must be a distinct compiled version")
	}
	now := time.Now().UTC().Truncate(time.Second)
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "B01 disposable TLS"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	if finalized {
		cert.DNSNames = []string{"raw.githubusercontent.com", "api.github.com", "release-assets.githubusercontent.com"}
	}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
	if e != nil {
		t.Fatal(e)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	key, e := x509.MarshalPKCS8PrivateKey(priv)
	if e != nil {
		t.Fatal(e)
	}
	put("ca.pem", ca, 0600)
	put("cert.pem", ca, 0600)
	put("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600)
	root := metadata.Root(now.Add(48 * time.Hour))
	root.Signed.ConsistentSnapshot = true
	keys := map[string][]signature.Signer{}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		n := 1
		if role == "root" || role == "targets" {
			n = 3
			root.Signed.Roles[role].Threshold = 2
		}
		for i := 0; i < n; i++ {
			pub, priv, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				t.Fatal(e)
			}
			signer, e := signature.LoadSigner(priv, crypto.Hash(0))
			if e != nil {
				t.Fatal(e)
			}
			k, e := metadata.KeyFromPublicKey(pub)
			if e != nil {
				t.Fatal(e)
			}
			if e = root.Signed.AddKey(k, role); e != nil {
				t.Fatal(e)
			}
			keys[role] = append(keys[role], signer)
		}
	}
	for _, s := range keys["root"][:2] {
		if _, e = root.Sign(s); e != nil {
			t.Fatal(e)
		}
	}
	raw := func(v interface{ ToBytes(bool) ([]byte, error) }) []byte {
		b, e := v.ToBytes(false)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	seed := bootstrap.Seed{Binding: bootstrap.Binding{Version: 1, Purpose: bootstrap.Purpose, DevelopmentOnly: true, Repository: "fased-ai/fased-app", Channel: "development", Target: update.Target{OS: "linux", Arch: "amd64"}}, Origin: "https://127.0.0.1:17449", Root: raw(root), CreatedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix(), MinimumSequence: 7}
	manifest := bundle.Manifest{Schema: 1, Platform: "linux", Arch: "amd64", Version: applicationVMVersion, Source: "B01-disposable-VM", UnsignedEngineering: true, Files: map[string]bundle.File{}}
	if finalized {
		manifest.Schema = 2
		manifest.UnsignedEngineering = false
		manifest.Version = "0.1.0"
		manifest.Source = strings.Repeat("a", 40)
	}
	payload := map[string][]byte{"fased-app": target, "LICENSE": []byte("disposable fixture"), "NOTICE": []byte("disposable fixture"), "INSTALL.txt": []byte("disposable fixture only")}
	for name := range bundle.PlatformFiles() {
		payload[name] = []byte("disposable fixture " + name)
	}
	for name, b := range payload {
		manifest.Files[name] = bundle.File{SHA256: applicationVMDigest(b), Bytes: int64(len(b))}
	}
	payload["bundle.json"] = applicationVMJSON(manifest)
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	tr := tar.NewWriter(gz)
	for name, b := range payload {
		if e = tr.WriteHeader(&tar.Header{Name: "fased-app-linux-amd64/" + name, Size: int64(len(b)), Mode: 0700}); e != nil {
			t.Fatal(e)
		}
		if _, e = tr.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e = tr.Close(); e != nil {
		t.Fatal(e)
	}
	if e = gz.Close(); e != nil {
		t.Fatal(e)
	}
	archive := packed.Bytes()
	if finalized {
		applicationVMFinalizer.Finish(t, applicationVMFinalizerComposition{Dir: dir, Now: now, Seed: seed, Manifest: manifest, Keys: keys, Archive: archive, Source: source, Target: target, CA: ca, Put: put})
		return
	}
	descriptor := bootstrap.Descriptor{Binding: seed.Binding, Sequence: 7, ProductVersion: manifest.Version, Source: manifest.Source, BundleSHA256: applicationVMDigest(payload["bundle.json"]), Archive: update.Artifact{File: "fased-app-linux-amd64.tar.gz", Size: int64(len(archive)), SHA256: applicationVMDigest(archive)}}
	db := applicationVMJSON(descriptor)
	targets := metadata.Targets(now.Add(24 * time.Hour))
	for name, b := range map[string][]byte{"bootstrap/descriptor.json": db, "fased-app-linux-amd64.tar.gz": archive} {
		f, e := metadata.TargetFile().FromBytes(name, b, "sha256")
		if e != nil {
			t.Fatal(e)
		}
		targets.Signed.Targets[name] = f
	}
	for _, s := range keys["targets"][:2] {
		if _, e = targets.Sign(s); e != nil {
			t.Fatal(e)
		}
	}
	meta := func(b []byte) *metadata.MetaFiles {
		h := sha256.Sum256(b)
		return &metadata.MetaFiles{Version: 1, Length: int64(len(b)), Hashes: metadata.Hashes{"sha256": h[:]}}
	}
	tg := raw(targets)
	snap := metadata.Snapshot(now.Add(24 * time.Hour))
	snap.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": meta(tg)}
	if _, e = snap.Sign(keys["snapshot"][0]); e != nil {
		t.Fatal(e)
	}
	sn := raw(snap)
	stamp := metadata.Timestamp(now.Add(24 * time.Hour))
	stamp.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": meta(sn)}
	if _, e = stamp.Sign(keys["timestamp"][0]); e != nil {
		t.Fatal(e)
	}
	for name, b := range map[string][]byte{"timestamp.json": raw(stamp), "1.snapshot.json": sn, "1.targets.json": tg, "targets/bootstrap/" + applicationVMDigest(db) + ".descriptor.json": db, "targets/" + descriptor.Archive.SHA256 + ".fased-app-linux-amd64.tar.gz": archive} {
		put("repo/"+name, b, 0600)
	}
	sb := applicationVMJSON(seed)
	put("seed.json", sb, 0600)
	put("fixture.json", applicationVMJSON(applicationVMTrust{Connected: os.Getenv("FASED_UPDATE_FIXTURE_CONNECTED") == "1", SeedSHA256: applicationVMDigest(sb), CASHA256: applicationVMDigest(ca), SourceSHA256: applicationVMDigest(source), TargetSHA256: applicationVMDigest(target)}), 0600)
	put("../input-receipt.json", applicationVMJSON(map[string]any{"fixtureOnly": true, "sequence": 7, "descriptorSha256": applicationVMDigest(db), "engineSha256": applicationVMDigest(target), "sourceSha256": applicationVMDigest(source), "targetSha256": applicationVMDigest(target), "privateSigningKeysRetained": false}), 0600)
}
