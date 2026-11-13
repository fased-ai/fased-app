//go:build linux

package main

// Produces only the finite loopback repository used by the disposable custody
// VM. No fixture admission or key material is part of a product executable.
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
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

func TestProduceCustodyPairFixture(t *testing.T) {
	dir, source, buildPin := os.Getenv("FASED_CUSTODY_FIXTURE_DIR"), os.Getenv("FASED_CUSTODY_FIXTURE_SOURCE"), os.Getenv("FASED_CUSTODY_FIXTURE_BUILD_SHA256")
	if dir == "" && source == "" && buildPin == "" {
		t.Skip("explicit disposable custody fixture only")
	}
	decoded, err := hex.DecodeString(source)
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || !filepath.IsAbs(dir) || len(decoded) != 20 || hex.EncodeToString(decoded) != source {
		t.Fatal("ordinary owner, absolute fixture paths and exact source required")
	}
	info, err := os.Lstat(dir)
	stat, owned := infoSys(info)
	if err != nil || !owned || int(stat.Uid) != os.Getuid() || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("exclusive private fixture directory required", err)
	}
	rootDir, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer rootDir.Close()
	opened, err := rootDir.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		t.Fatal("fixture directory changed", err)
	}
	for _, name := range []string{"repo-base", "repo-target", "seed.json", "ca.pem", "key.pem", "fixture.json", "input-receipt.json"} {
		if _, err := rootDir.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("fixture output must not already exist", name, err)
		}
	}
	receiptInfo, err := rootDir.Lstat("build-receipt.json")
	receiptStat, owned := infoSys(receiptInfo)
	if err != nil || !owned || int(receiptStat.Uid) != os.Getuid() || receiptStat.Nlink != 1 || !receiptInfo.Mode().IsRegular() || receiptInfo.Size() <= 0 || receiptInfo.Size() > 65536 {
		t.Fatal("bounded owner fixture build receipt required", err)
	}
	receiptFile, err := rootDir.OpenFile("build-receipt.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal("fixed fixture build receipt required", err)
	}
	openedReceipt, err := receiptFile.Stat()
	if err != nil || !os.SameFile(receiptInfo, openedReceipt) {
		receiptFile.Close()
		t.Fatal("fixture build receipt identity changed", err)
	}
	receiptBytes, err := io.ReadAll(io.LimitReader(receiptFile, 65537))
	err = errors.Join(err, receiptFile.Close())
	var build struct {
		Version              int               `json:"version"`
		FixtureOnly          bool              `json:"fixtureOnly"`
		DeclaredSourceCommit string            `json:"declaredSourceCommit"`
		Artifacts            map[string]string `json:"artifacts"`
	}
	if err != nil || len(receiptBytes) > 65536 || applicationVMDigest(receiptBytes) != buildPin || update.StrictJSON(receiptBytes, &build) != nil || build.Version != 1 || !build.FixtureOnly || build.DeclaredSourceCommit != source || len(build.Artifacts) != 4 {
		t.Fatal("pinned fixture build receipt required")
	}
	put := func(name string, b []byte) {
		t.Helper()
		if err := rootDir.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		f, err := rootDir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write(b)
		if err == nil {
			err = f.Sync()
		}
		if err = errors.Join(err, f.Close()); err != nil {
			t.Fatal(err)
		}
	}
	readBinary := func(name, action string) ([]byte, []byte) {
		t.Helper()
		entry, err := rootDir.Lstat(name)
		st, owned := infoSys(entry)
		if err != nil || !owned || int(st.Uid) != os.Getuid() || st.Nlink != 1 || !entry.Mode().IsRegular() || entry.Size() <= 0 || entry.Size() > 64<<20 {
			t.Fatal("bounded compiled fixture binary required", err)
		}
		f, err := rootDir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !os.SameFile(entry, opened) {
			t.Fatal("compiled fixture identity changed", err)
		}
		b, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
		if err != nil || int64(len(b)) != entry.Size() || applicationVMDigest(b) != build.Artifacts[name] {
			t.Fatal("compiled fixture bytes changed", err)
		}
		cmd := exec.Command("/proc/self/fd/3", action)
		cmd.ExtraFiles = []*os.File{f}
		out, err := cmd.Output()
		if err != nil {
			t.Fatal("compiled fixture probe failed", err)
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		after, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
		if err != nil || applicationVMDigest(after) != applicationVMDigest(b) {
			t.Fatal("compiled fixture changed during its probe", err)
		}
		return b, out
	}
	initial, baseVersion := readBinary("fased-app-tests", "version")
	target, targetVersion := readBinary("fased-app-next", "version")
	if strings.TrimSpace(string(baseVersion)) != "0.1.11" || strings.TrimSpace(string(targetVersion)) != "0.1.12" {
		t.Fatal("two distinct expected compiled versions required")
	}
	if applicationVMDigest(initial) == applicationVMDigest(target) {
		t.Fatal("distinct initial and successor binaries required")
	}
	signerHashes := make(map[string]string)
	for _, name := range []string{"fased-signer", "fased-signer-next"} {
		b, output := readBinary(name, "ordinary-custody-version")
		var profile struct {
			Mode         string `json:"mode"`
			OwnerFlow    int    `json:"ownerFlow"`
			Profile      string `json:"profile"`
			Protocol     int    `json:"protocol"`
			Provenance   string `json:"provenance"`
			WalletSigner *bool  `json:"walletSigner"`
		}
		if update.StrictJSON(output, &profile) != nil || profile.Mode != "ordinary-custody:v1" || profile.OwnerFlow != 1 || profile.Profile != "owner-authorized:v1" || profile.Protocol != 3 || profile.Provenance != "controlled-disposable:v1" || profile.WalletSigner == nil || *profile.WalletSigner {
			t.Fatal("controlled signer fixture required")
		}
		signerHashes[name] = applicationVMDigest(b)
	}
	if signerHashes["fased-signer"] == signerHashes["fased-signer-next"] {
		t.Fatal("distinct initial and successor signers required")
	}
	for _, name := range []string{"repo-base", "repo-target"} {
		if err := rootDir.Mkdir(name, 0700); err != nil {
			t.Fatal("exclusive fixture repository required", name, err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fased disposable custody HTTPS"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	key, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	put("ca.pem", ca)
	put("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	root := metadata.Root(now.Add(24 * time.Hour))
	root.Signed.ConsistentSnapshot = true
	keys := map[string][]signature.Signer{}
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		count := 1
		if role == "root" || role == "targets" {
			count = 3
			root.Signed.Roles[role].Threshold = 2
		}
		for i := 0; i < count; i++ {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			s, err := signature.LoadSigner(private, crypto.Hash(0))
			if err != nil {
				t.Fatal(err)
			}
			k, err := metadata.KeyFromPublicKey(public)
			if err != nil || root.Signed.AddKey(k, role) != nil {
				t.Fatal("fixture root key", err)
			}
			keys[role] = append(keys[role], s)
		}
	}
	for _, s := range keys["root"][:2] {
		if _, err := root.Sign(s); err != nil {
			t.Fatal(err)
		}
	}
	raw := func(v interface{ ToBytes(bool) ([]byte, error) }) []byte {
		t.Helper()
		b, err := v.ToBytes(false)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	seed := bootstrap.Seed{Binding: bootstrap.Binding{Version: 1, Purpose: bootstrap.Purpose, DevelopmentOnly: true, Repository: "fased-ai/fased-app", Channel: "development", Target: update.Target{OS: "linux", Arch: "amd64"}}, Origin: "https://127.0.0.1:17449", Root: raw(root), CreatedAt: now.Add(-time.Minute).Unix(), ExpiresAt: now.Add(12 * time.Hour).Unix(), MinimumSequence: 8}
	receipts := make(map[string]any)
	for _, release := range []struct {
		name, version string
		sequence      uint64
		core          []byte
	}{{"base", "0.1.11", 8, initial}, {"target", "0.1.12", 9, target}} {
		manifest := bundle.Manifest{Schema: 1, Platform: "linux", Arch: "amd64", Version: release.version, Source: source, UnsignedEngineering: true, Files: map[string]bundle.File{}}
		payload := map[string][]byte{"fased-app": release.core, "INSTALL.txt": []byte("Disposable custody acceptance fixture only.\n")}
		for _, name := range []string{"LICENSE", "NOTICE"} {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			payload[name] = b
		}
		for name, path := range bundle.PlatformFiles() {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			payload[name] = b
		}
		for name, b := range payload {
			manifest.Files[name] = bundle.File{SHA256: applicationVMDigest(b), Bytes: int64(len(b))}
		}
		payload["bundle.json"] = applicationVMJSON(manifest)
		var packed bytes.Buffer
		gz := gzip.NewWriter(&packed)
		tr := tar.NewWriter(gz)
		for name, b := range payload {
			if err := tr.WriteHeader(&tar.Header{Name: "fased-app-linux-amd64/" + name, Size: int64(len(b)), Mode: 0700}); err != nil {
				t.Fatal(err)
			}
			if _, err := tr.Write(b); err != nil {
				t.Fatal(err)
			}
		}
		if err := errors.Join(tr.Close(), gz.Close()); err != nil {
			t.Fatal(err)
		}
		archive := packed.Bytes()
		d := bootstrap.Descriptor{Binding: seed.Binding, Sequence: release.sequence, ProductVersion: release.version, Source: source, BundleSHA256: applicationVMDigest(payload["bundle.json"]), Archive: update.Artifact{File: "fased-app-linux-amd64.tar.gz", Size: int64(len(archive)), SHA256: applicationVMDigest(archive)}}
		db := applicationVMJSON(d)
		targets := metadata.Targets(now.Add(8 * time.Hour))
		targets.Signed.Version = int64(release.sequence)
		for name, b := range map[string][]byte{"bootstrap/descriptor.json": db, d.Archive.File: archive} {
			f, err := metadata.TargetFile().FromBytes(name, b, "sha256")
			if err != nil {
				t.Fatal(err)
			}
			targets.Signed.Targets[name] = f
		}
		for _, s := range keys["targets"][:2] {
			if _, err := targets.Sign(s); err != nil {
				t.Fatal(err)
			}
		}
		tg := raw(targets)
		meta := func(b []byte) *metadata.MetaFiles {
			h := sha256.Sum256(b)
			return &metadata.MetaFiles{Version: int64(release.sequence), Length: int64(len(b)), Hashes: metadata.Hashes{"sha256": h[:]}}
		}
		snapshot := metadata.Snapshot(now.Add(4 * time.Hour))
		snapshot.Signed.Version = int64(release.sequence)
		snapshot.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": meta(tg)}
		if _, err := snapshot.Sign(keys["snapshot"][0]); err != nil {
			t.Fatal(err)
		}
		sn := raw(snapshot)
		timestamp := metadata.Timestamp(now.Add(time.Hour))
		timestamp.Signed.Version = int64(release.sequence)
		timestamp.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": meta(sn)}
		if _, err := timestamp.Sign(keys["timestamp"][0]); err != nil {
			t.Fatal(err)
		}
		prefix := "repo-" + release.name + "/"
		put(prefix+"timestamp.json", raw(timestamp))
		put(prefix+strconv.FormatUint(release.sequence, 10)+".snapshot.json", sn)
		put(prefix+strconv.FormatUint(release.sequence, 10)+".targets.json", tg)
		put(prefix+"targets/bootstrap/"+applicationVMDigest(db)+".descriptor.json", db)
		put(prefix+"targets/"+d.Archive.SHA256+"."+d.Archive.File, archive)
		receipts[release.name] = map[string]any{"version": release.version, "sequence": release.sequence, "descriptorSha256": applicationVMDigest(db), "coreSha256": applicationVMDigest(release.core)}
	}
	sb := applicationVMJSON(seed)
	put("seed.json", sb)
	put("fixture.json", applicationVMJSON(applicationVMTrust{Connected: true, SeedSHA256: applicationVMDigest(sb), CASHA256: applicationVMDigest(ca), SourceSHA256: applicationVMDigest(initial), TargetSHA256: applicationVMDigest(target), TargetSignerSHA256: signerHashes["fased-signer-next"]}))
	put("input-receipt.json", applicationVMJSON(map[string]any{"fixtureOnly": true, "declaredSourceCommit": source, "releases": receipts, "signerHashes": signerHashes, "privateReleaseSigningKeysRetained": false, "disposableTLSPrivateKeyRetained": true}))
}

func infoSys(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return st, ok
}
