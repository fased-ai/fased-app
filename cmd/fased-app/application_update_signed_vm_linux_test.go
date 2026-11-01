//go:build linux

package main

// Signed disposable-VM composition only. None of these bindings or barriers
// exist in the product executable. The installed controller and owner IPC are
// used unchanged; the test stops immediately before their final metadata call.
import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bootstrap"
	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/localinstall"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/fased-ai/fased-app/internal/vpsinstall"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

const applicationVMInputs = "/var/lib/fased-update-fixture"
const applicationVMVersion = "0.0.0-b01-vm.2"
const applicationVMPassword = "disposable-b01-owner-password"

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
var applicationVMRequests atomic.Uint64

type applicationVMTrust struct {
	Finalized        bool   `json:"finalized,omitempty"`
	DescriptorSHA256 string `json:"descriptorSha256,omitempty"`
	ProvenanceSHA256 string `json:"provenanceSha256,omitempty"`
	PlanSHA256       string `json:"planSha256,omitempty"`

	Connected    bool   `json:"connected,omitempty"`
	SeedSHA256   string `json:"seedSha256"`
	CASHA256     string `json:"caSha256"`
	SourceSHA256 string `json:"sourceSha256"`
	TargetSHA256 string `json:"targetSha256"`
}
type applicationVMBefore struct {
	InstallationID       string                          `json:"installationId"`
	PreviousEngineSHA256 string                          `json:"previousEngineSha256"`
	InstanceID           string                          `json:"instanceId"`
	WorkspaceSHA256      string                          `json:"workspaceSha256"`
	CredentialRef        string                          `json:"credentialRef"`
	CiphertextSHA256     string                          `json:"ciphertextSha256"`
	Operation            core.ApplicationUpdateOperation `json:"operation"`
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

// Root has the overflow UID inside the one-UID Local Bubblewrap namespace.
// Accept that view only after proving that exact map and the unchanged product
// sandbox (the proc mount expresses the parent UID as zero). No writable/owner-created trust file or ordinary runtime is admitted.
func applicationVMRootOwner(uid uint32) bool {
	if uid == 0 {
		return true
	}
	if uid != 65534 || os.Getuid() != 1100 {
		return false
	}
	b, e := os.ReadFile("/proc/self/uid_map")
	if e != nil || strings.Join(strings.Fields(string(b)), " ") != "1100 0 1" {
		return false
	}
	paths, e := localinstall.DefaultPaths()
	if e != nil {
		return false
	}
	return localinstall.CheckSandbox(paths.Root, paths.Data) == nil
}
func applicationVMTrustRead() (applicationVMTrust, error) {
	var c applicationVMTrust
	for _, p := range []string{applicationVMInputs, applicationVMInputs + "/fixture.json", applicationVMInputs + "/seed.json", applicationVMInputs + "/ca.pem"} {
		i, e := os.Lstat(p)
		if e != nil {
			return c, e
		}
		s, ok := i.Sys().(*syscall.Stat_t)
		if !ok || !applicationVMRootOwner(s.Uid) || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 {
			return c, errors.New("root-published disposable fixture required")
		}
	}
	b, e := os.ReadFile(applicationVMInputs + "/fixture.json")
	if e != nil {
		return c, e
	}
	e = update.StrictJSON(b, &c)
	return c, e
}
func applicationVMPrepare(ctx context.Context, parent string) (*bootstrap.Prepared, error) {
	trust, e := applicationVMTrustRead()
	if e != nil {
		return nil, e
	}
	seed, e := os.ReadFile(applicationVMInputs + "/seed.json")
	if e != nil {
		return nil, e
	}
	ca, e := os.ReadFile(applicationVMInputs + "/ca.pem")
	if e != nil || applicationVMDigest(ca) != trust.CASHA256 {
		return nil, errors.New("fixture CA changed")
	}
	dir, e := os.MkdirTemp(parent, ".signed-update-fixture-")
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(dir, 0700); e != nil {
		return nil, e
	}
	if trust.Finalized {
		if applicationVMFinalizer == nil {
			return nil, errors.New("finalizer fixture build required")
		}
		options, e := applicationVMFinalizer.Options(filepath.Join(dir, "verified"), seed, ca, trust)
		if e != nil {
			return nil, e
		}
		return bootstrap.Prepare(ctx, options)
	}
	return bootstrap.Prepare(ctx, bootstrap.Options{Development: true, Seed: seed, SeedSHA256: trust.SeedSHA256, TLSCA: ca, Directory: filepath.Join(dir, "verified")})
}

func applicationVMOptions(directory string) (bootstrap.Options, error) {
	trust, e := applicationVMTrustRead()
	if e != nil {
		return bootstrap.Options{}, e
	}
	seed, e := os.ReadFile(applicationVMInputs + "/seed.json")
	if e != nil {
		return bootstrap.Options{}, e
	}
	ca, e := os.ReadFile(applicationVMInputs + "/ca.pem")
	if e != nil || applicationVMDigest(ca) != trust.CASHA256 {
		return bootstrap.Options{}, errors.New("fixture CA changed")
	}
	if trust.Finalized {
		if applicationVMFinalizer == nil {
			return bootstrap.Options{}, errors.New("finalizer fixture build required")
		}
		return applicationVMFinalizer.Options(directory, seed, ca, trust)
	}
	return bootstrap.Options{Development: true, Seed: seed, SeedSHA256: trust.SeedSHA256, TLSCA: ca, Directory: directory}, nil
}

// This mode exercises the product channel factories without the separate older
// reboot barrier. The owner, image and root-published inputs remain fixed.
func applicationConnectedRuntime(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "serve" && args[0] != "maintain-update" && args[0] != "maintenance-recover" {
		return false, nil
	}
	remote := false
	for _, a := range args {
		if a == "--remote" || a == "--remote-config" {
			remote = true
		}
	}
	var data, parent string
	var sequence uint64
	var release string
	if remote {
		var m vpsinstall.Manifest
		raw, e := os.ReadFile(vpsinstall.ConfigPath(1100))
		if e != nil {
			return false, e
		}
		if json.Unmarshal(raw, &m) != nil || m.OwnerUID != 1100 || (os.Getuid() != 1100 && os.Getuid() != m.AppUID) {
			return false, errors.New("fixed fixture Remote identity required")
		}
		data, sequence, release = m.Data, m.ApplicationSequence, m.ApplicationReleaseSHA256
		if args[0] != "serve" {
			parent, e = vpsinstall.ApplicationAcquisitionParent(m)
			if e != nil {
				return false, e
			}
		}
	} else {
		if os.Getuid() != 1100 {
			return false, errors.New("fixed fixture Local owner required")
		}
		paths, e := localinstall.DefaultPaths()
		if e != nil {
			return false, e
		}
		m, e := localinstall.Load(paths)
		if e != nil {
			return false, e
		}
		data, sequence, release = m.Data, m.ApplicationSequence, m.ApplicationReleaseSHA256
		if args[0] != "serve" {
			parent, e = localinstall.ApplicationAcquisitionParent(m)
			if e != nil {
				return false, e
			}
		}
	}
	compiledApplicationUpdateCheck = channelCheck(applicationVMOptions, filepath.Join(data, "update-channel"), sequence, release)
	if parent != "" {
		compiledApplicationUpdateSource = channelSource(applicationVMOptions, parent)
	}
	return false, nil
}

// Called only by the pre-existing compiled-test dispatcher. Validate installed
// immutable bytes and real guest identity before binding any fixture source.
func applicationUpdateVMRuntime(image string, args []string) (bool, error) {
	// The delegate only requests a fixed OS unit; it binds no fixture channel.
	if len(args) > 0 && args[0] == "maintenance-delegate" {
		return false, nil
	}
	if _, e := os.Lstat(applicationVMInputs + "/fixture.json"); errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	product, e := os.ReadFile("/sys/class/dmi/id/product_name")
	if e != nil || !(strings.Contains(string(product), "QEMU") || strings.Contains(string(product), "Standard PC")) {
		return false, errors.New("signed-update fixture requires QEMU")
	}
	trust, e := applicationVMTrustRead()
	if e != nil {
		return false, e
	}
	b, e := os.ReadFile(image)
	if e != nil {
		return false, e
	}
	h := applicationVMDigest(b)
	if h != trust.SourceSHA256 && h != trust.TargetSHA256 {
		return false, errors.New("unrecognized compiled fixture image")
	}
	if trust.Connected {
		return applicationConnectedRuntime(args)
	}
	m, e := vpsinstall.Load(1100)
	if e != nil { // AppUID cannot use owner Load; public fixed config is sufficient only for checker binding.
		raw, re := os.ReadFile(vpsinstall.ConfigPath(1100))
		if re != nil || json.Unmarshal(raw, &m) != nil {
			return false, e
		}
	}
	compiledApplicationUpdateCheck = func(ctx context.Context) (*core.ApplicationRelease, error) {
		raw, e := os.ReadFile(vpsinstall.ConfigPath(1100))
		if e != nil {
			return nil, e
		}
		var current vpsinstall.Manifest
		if json.Unmarshal(raw, &current) != nil {
			return nil, errors.New("invalid fixture identity")
		}
		if trust.Finalized {
			return channelCheck(applicationVMOptions, filepath.Join(m.Data, "update-channel"), current.ApplicationSequence, current.ApplicationReleaseSHA256)(ctx)
		}
		if current.ApplicationSequence >= 7 {
			return nil, nil
		}
		p, e := applicationVMPrepare(ctx, m.Data)
		if e != nil {
			return nil, e
		}
		defer p.Close()
		defer os.RemoveAll(filepath.Dir(filepath.Dir(p.Report().BundleDirectory)))
		r := p.Report()
		mf, e := bundle.Verify(r.BundleDirectory)
		if e != nil {
			return nil, e
		}
		return &core.ApplicationRelease{EngineSHA256: mf.Files["fased-app"].SHA256, Version: mf.Version, Sequence: r.Sequence, ManifestSHA256: r.DescriptorSHA256, Target: r.Target}, nil
	}
	compiledApplicationUpdateSource = func(ctx context.Context, want core.ApplicationRelease, id string) (*bootstrap.Prepared, error) {
		if !update.IDOK(id) || os.Getuid() != 1100 {
			return nil, errors.New("fixture source requires exact ordinary owner")
		}
		p, e := applicationVMPrepare(ctx, filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer"))
		if e != nil {
			return nil, e
		}
		r := p.Report()
		mf, e := bundle.Verify(r.BundleDirectory)
		if e != nil || want.Sequence != r.Sequence || want.ManifestSHA256 != r.DescriptorSHA256 || want.EngineSHA256 != mf.Files["fased-app"].SHA256 || want.Version != mf.Version {
			p.Close()
			return nil, errors.New("independent fixture admission differs")
		}
		return p, nil
	}
	if len(args) > 0 && args[0] == "maintain-update" {
		if e = vpsinstall.ValidateApplicationMaintenanceEntry(m, image); e != nil {
			return false, e
		}
		status, e := os.ReadFile("/proc/self/status")
		if e != nil {
			return false, e
		}
		if os.Getuid() != 1100 || !bytes.Contains(status, []byte("CapEff:\t0000000000000000\n")) {
			return false, errors.New("fixture maintainer retained privilege")
		}
		boot, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if e != nil {
			return false, e
		}
		path := filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer/update-fixture-actors.jsonl")
		f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if e != nil {
			return false, e
		}
		b := applicationVMJSON(map[string]any{"bootId": strings.TrimSpace(string(boot)), "uid": os.Getuid(), "euid": os.Geteuid(), "pid": os.Getpid(), "capEff": "0", "inertActor": false})
		_, e = f.Write(append(b, '\n'))
		e = errors.Join(e, f.Sync(), f.Close())
		if e != nil {
			return false, e
		}
	}
	if len(args) == 0 || args[0] != "application-owner-client" {
		return false, nil
	}
	// The real metadata helper validates the immutable config/executable and owns
	// the IPC call. The test-only wrapper retains and forwards its exact bytes.
	input, e := io.ReadAll(io.LimitReader(os.Stdin, 16385))
	if e != nil || len(input) > 16384 {
		return true, errors.New("bounded fixture owner command required")
	}
	var command core.ApplicationOwnerCommand
	if update.StrictJSON(input, &command) != nil {
		return true, errors.New("invalid fixture owner command")
	}
	if command.Action == "finish" {
		offline, e := core.ReadApplicationOwnerOffline(context.Background(), m.Data, command.ID)
		if e != nil || offline.Checkpoint == nil || command.Lease == "" || command.Lease != offline.View.Operation.OwnerLease {
			return true, fmt.Errorf("original checkpoint/lease required: %v", e)
		}
		leaseHash := applicationVMDigest([]byte(command.Lease))
		checkpointHash := applicationVMDigest(applicationVMJSON(offline.Checkpoint))
		cut := filepath.Join(m.Data, "update-fixture-cut.json")
		boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if _, e = os.Lstat(cut); errors.Is(e, os.ErrNotExist) {
			if len(args) != 3 || args[1] != "--config" {
				return true, errors.New("fixed fixture helper required")
			}
			if _, e = vpsinstall.ValidateApplicationOwnerHelper(args[2], image, os.Getuid()); e != nil {
				return true, e
			}
			if command.Receipt == nil || command.Receipt.Phase != "completed" {
				return true, errors.New("target completion receipt required")
			}
			if e = core.WritePrivateJSON(cut, map[string]any{"result": "CUT", "ownerLeaseSha256": leaseHash, "checkpointSha256": checkpointHash, "fixtureOnly": true, "phase": "healthy-held", "id": command.ID, "bootId": strings.TrimSpace(string(boot)), "sequence": command.Receipt.Sequence, "manifestSha256": command.Receipt.ManifestSHA256, "engineSha256": command.Receipt.EngineSHA256}); e != nil {
				return true, e
			}
			if e = syscall.Kill(os.Getpid(), syscall.SIGSTOP); e != nil {
				return true, e
			}
			for {
				_ = syscall.Pause()
			}
		} else if e != nil {
			return true, e
		} else {
			var prior struct {
				BootID           string `json:"bootId"`
				OwnerLeaseSHA256 string `json:"ownerLeaseSha256"`
				CheckpointSHA256 string `json:"checkpointSha256"`
			}
			raw, e := os.ReadFile(cut)
			if e != nil || json.Unmarshal(raw, &prior) != nil || prior.BootID == strings.TrimSpace(string(boot)) || prior.OwnerLeaseSHA256 != leaseHash || prior.CheckpointSHA256 != checkpointHash {
				return true, errors.New("fixture recovery requires a real reboot")
			}
			if e = core.WritePrivateJSON(filepath.Join(m.Data, "update-fixture-resumed.json"), map[string]any{"fixtureOnly": true, "id": command.ID, "ownerLeaseSha256": leaseHash, "checkpointSha256": checkpointHash, "bootId": strings.TrimSpace(string(boot)), "result": "RESUMED"}); e != nil {
				return true, e
			}

		}
	}
	r, w, e := os.Pipe()
	if e != nil {
		return true, e
	}
	defer r.Close()
	if _, e = w.Write(input); e != nil {
		w.Close()
		return true, e
	}
	w.Close()
	return true, applicationOwnerClient(args[1:], r, os.Stdout, os.Stderr)
}

func applicationVMWorkspace(v core.AgentView) string {
	return applicationVMDigest(applicationVMJSON(map[string]any{"revision": v.Revision, "tasks": v.Tasks, "memories": v.Memories, "messages": v.Messages, "runs": v.Runs, "profiles": v.Profiles}))
}
func applicationVMHTTP() *core.Client {
	return &core.Client{Endpoint: core.Endpoint{URL: "http://127.0.0.1:17448"}, HTTP: &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}}
}
func applicationVMServer(t *testing.T) error {
	cert, e := tls.LoadX509KeyPair(applicationVMInputs+"/cert.pem", applicationVMInputs+"/key.pem")
	if e != nil {
		return e
	}
	l, e := net.Listen("tcp", "127.0.0.1:17449")
	if e != nil {
		return e
	}
	handler := http.Handler(http.FileServer(http.Dir(applicationVMInputs + "/repo")))
	trust, e := applicationVMTrustRead()
	if e != nil {
		return e
	}
	if trust.Finalized {
		if applicationVMFinalizer == nil {
			return errors.New("finalizer fixture server required")
		}
		handler, e = applicationVMFinalizer.Handler(trust)
		if e != nil {
			return e
		}
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { applicationVMRequests.Add(1); handler.ServeHTTP(w, r) }), TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.Serve(tls.NewListener(l, s.TLSConfig)) }()
	t.Cleanup(func() { _ = s.Close() })
	return nil
}
func applicationUpdateVMOwnerPrepare(t *testing.T, inputDir string) (vpsinstall.Manifest, error) {
	if inputDir != applicationVMInputs {
		return vpsinstall.Manifest{}, errors.New("fixed input directory required")
	}
	if e := applicationVMServer(t); e != nil {
		return vpsinstall.Manifest{}, e
	}
	image := filepath.Join(inputDir, "fased-app-tests")
	f, e := bundle.Inspect(image)
	if e != nil {
		return vpsinstall.Manifest{}, e
	}
	system := &custodyinstall.System{In: strings.NewReader(""), Out: os.Stderr}
	m, e := vpsinstall.Install(t.Context(), vpsinstall.Options{Core: image, CoreSHA256: f.SHA256, Listen: "127.0.0.1:17448", PrepareHost: true, Start: true}, system)
	if e != nil {
		return m, e
	}
	if e = vpsinstall.InstallApplicationMaintenance(t.Context(), m, system); e != nil {
		return m, e
	}
	var claim bytes.Buffer
	if e = vpsinstall.Claim(t.Context(), m, system, false, &claim); e != nil {
		return m, e
	}
	c := applicationVMHTTP()
	defer c.Close()
	if e = c.Claim(t.Context(), strings.TrimSpace(claim.String()), applicationVMPassword); e != nil {
		return m, e
	}
	var v core.AgentView
	if e = c.Do(t.Context(), "GET", "/api/v1/agents?profile=trading-analyst", nil, &v); e != nil {
		return m, e
	}
	for _, command := range []core.AgentCommand{{Action: "memory-put", Text: "Preserve this synthetic preference across the update.", Source: "B01 disposable acceptance"}, {Action: "task-create", Name: "Retained reminder", Text: "Synthetic reminder", IntervalSeconds: 86400, MaxRuns: 1}} {
		command.ProfileID = "trading-analyst"
		command.Revision = v.Revision
		if e = c.Do(t.Context(), "POST", "/api/v1/agents", command, &v); e != nil {
			return m, e
		}
	}
	if len(v.Tasks) != 1 {
		return m, errors.New("synthetic reminder missing")
	}
	if e = c.Do(t.Context(), "POST", "/api/v1/agents", core.AgentCommand{Action: "task-pause", ProfileID: "trading-analyst", Revision: v.Revision, ID: v.Tasks[0].ID}, &v); e != nil {
		return m, e
	}
	var vault core.AIConnectionView
	if e = c.Do(t.Context(), "POST", "/api/v1/ai/connection", core.AIConnectionCommand{Action: "add", Provider: "openai", CredentialMode: "test", Key: "fased-test-disposable-openai-connection-key", Passphrase: "disposable-b01-vault-password"}, &vault); e != nil {
		return m, e
	}
	if vault.LiveCallsEnabled || !vault.TestOnly {
		return m, errors.New("fixture vault enabled live calls")
	}
	var health core.Health
	if e = c.Do(t.Context(), "GET", "/api/v1/health", nil, &health); e != nil {
		return m, e
	}
	before := applicationVMBefore{InstallationID: m.ID, PreviousEngineSHA256: m.CoreSHA256, InstanceID: health.InstanceID, WorkspaceSHA256: applicationVMWorkspace(v), CredentialRef: vault.Ref}
	// Ciphertext hash is read through an explicitly dropped app helper during
	// temporary guest setup; no plaintext or session is copied into the owner.
	command := exec.CommandContext(t.Context(), "/usr/bin/sudo", "-n", "--", "/usr/bin/cat", "--", filepath.Join(m.Data, "ai-credentials.json"))
	raw, e := command.Output()
	if e != nil {
		return m, fmt.Errorf("ciphertext fixture: %w", e)
	}
	before.CiphertextSHA256 = applicationVMDigest(raw)
	if e = core.WritePrivateJSON(filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer/update-fixture-before.json"), before); e != nil {
		return m, e
	}
	return m, nil
}
func applicationUpdateVMOwnerLaunch(t *testing.T, m vpsinstall.Manifest) error {
	c := applicationVMHTTP()
	defer c.Close()
	if e := c.Login(t.Context(), applicationVMPassword); e != nil {
		return e
	}
	var v core.ApplicationUpdateView
	if e := c.Do(t.Context(), "GET", "/api/v1/updates/application", nil, &v); e != nil {
		return e
	}
	if !v.ControllerAvailable || v.ActivationEnabled || v.Policy.Mode == "automatic" {
		return errors.New("fixture-only manual controller required")
	}
	command := core.ApplicationUpdateCommand{Action: "check", Revision: v.Revision, PolicyRevision: v.PolicyRevision}
	if e := c.Do(t.Context(), "POST", "/api/v1/updates/application", command, &v); e != nil {
		return e
	}
	for deadline := time.Now().Add(15 * time.Second); v.Operation.Phase == "check-queued" || v.Operation.Phase == "checking"; {
		if time.Now().After(deadline) {
			return errors.New("fixture check did not complete")
		}
		time.Sleep(100 * time.Millisecond)
		if e := c.Do(t.Context(), "GET", "/api/v1/updates/application", nil, &v); e != nil {
			return e
		}
	}
	if v.Operation.Phase != "available" || v.Operation.Release == nil {
		return fmt.Errorf("signed fixture unavailable: id=%s phase=%s", v.Operation.ID, v.Operation.Phase)
	}
	beforePath := filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer/update-fixture-before.json")
	raw, e := os.ReadFile(beforePath)
	if e != nil {
		return e
	}
	var before applicationVMBefore
	if e = json.Unmarshal(raw, &before); e != nil {
		return e
	}
	if e := c.Do(t.Context(), "POST", "/api/v1/updates/application", core.ApplicationUpdateCommand{Action: "apply", Revision: v.Revision, PolicyRevision: v.PolicyRevision, ManifestSHA256: v.Operation.Release.ManifestSHA256}, &v); e != nil {
		return e
	}
	before.Operation = v.Operation
	if e = core.WritePrivateJSON(beforePath, before); e != nil {
		return e
	}
	for deadline := time.Now().Add(25 * time.Second); ; {
		j, e := vpsinstall.ApplicationUpdateState(1100)
		if e == nil && j.ID == v.Operation.ID && j.Phase == "healthy-held" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("actual signed replacement did not reach durable cut: %+v %v", j, e)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
func applicationUpdateVMOwnerVerify(t *testing.T, m vpsinstall.Manifest) (map[string]any, error) {
	if c, e := net.DialTimeout("tcp", "127.0.0.1:17449", time.Second); e == nil {
		c.Close()
		return nil, errors.New("recovery must run without the fixture download server")
	}

	raw, e := os.ReadFile(filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer/update-fixture-before.json"))
	if e != nil {
		return nil, e
	}
	var before applicationVMBefore
	if e = json.Unmarshal(raw, &before); e != nil {
		return nil, e
	}
	c := applicationVMHTTP()
	defer c.Close()
	for deadline := time.Now().Add(15 * time.Second); ; {
		var health core.Health
		if e = c.Do(t.Context(), "GET", "/api/v1/health", nil, &health); e == nil && health.InstanceID == before.InstanceID {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("rebooted original instance not healthy: %v", e)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if e = c.Login(t.Context(), applicationVMPassword); e != nil {
		return nil, e
	}
	var v core.ApplicationUpdateView
	for deadline := time.Now().Add(20 * time.Second); ; {
		if e = c.Do(t.Context(), "GET", "/api/v1/updates/application", nil, &v); e == nil && v.Operation.Phase == "completed" {
			j, je := vpsinstall.ApplicationUpdateState(1100)
			unit := "fased-vps-maintenance-1100@" + v.Operation.ID + ".service"
			enabled, _ := exec.Command("/usr/bin/systemctl", "is-enabled", unit).CombinedOutput()
			status, _ := exec.Command("/usr/bin/systemctl", "show", unit, "--property=MainPID,ExecMainStatus,Result,ActiveState,SubState").Output()
			if je == nil && j.ID == v.Operation.ID && j.Phase == "completed" && applicationVMActorRetired(string(enabled), string(status)) {
				break
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("original recovery did not complete: id=%s phase=%s: %v", v.Operation.ID, v.Operation.Phase, e)
		}
		time.Sleep(150 * time.Millisecond)
	}
	old := before.Operation
	o := v.Operation
	r := o.Receipt
	if v.InstanceID != before.InstanceID || o.ID != old.ID || o.Release == nil || old.Release == nil || *o.Release != *old.Release || r == nil || r.ID != o.ID || r.Sequence != old.Release.Sequence || r.ManifestSHA256 != old.Release.ManifestSHA256 || r.EngineSHA256 != old.Release.EngineSHA256 || o.Automatic || v.ActivationEnabled {
		return nil, errors.New("original manual operation/receipt changed")
	}
	current, e := vpsinstall.Load(1100)
	if e != nil {
		return nil, e
	}
	j, e := vpsinstall.ApplicationUpdateState(1100)
	if e != nil || j.Phase != "completed" || j.ID != o.ID || current.ApplicationSequence != r.Sequence || current.ApplicationReleaseSHA256 != r.ManifestSHA256 || current.ID != before.InstallationID || before.PreviousEngineSHA256 == r.EngineSHA256 || current.CoreSHA256 != r.EngineSHA256 {
		return nil, errors.New("installed journal/floor/identity differs")
	}
	image, e := bundle.Inspect(current.Core)
	if e != nil || image.SHA256 != r.EngineSHA256 {
		return nil, errors.New("installed target bytes differ")
	}
	var agents core.AgentView
	if e = c.Do(t.Context(), "GET", "/api/v1/agents?profile=trading-analyst", nil, &agents); e != nil {
		return nil, e
	}
	if applicationVMWorkspace(agents) != before.WorkspaceSHA256 {
		return nil, errors.New("workspace/schedule/hold changed")
	}
	var vault core.AIConnectionView
	if e = c.Do(t.Context(), "GET", "/api/v1/ai/connection", nil, &vault); e != nil {
		return nil, e
	}
	if !applicationVMVaultRetained(vault, before.CredentialRef) {
		return nil, errors.New("credential lease or identity changed")
	}
	// Protected IPC confirms the exact settled operation; the ordinary Settings
	// mutation below proves the update fence is actually released. No CLI token
	// is invented, copied, or implicitly renewed after reboot.
	limited := &custodyinstall.System{NonInteractive: true, Out: os.Stderr}
	owner, e := vpsinstall.ApplicationOwnerCall(t.Context(), current, core.ApplicationOwnerCommand{Action: "inspect", ID: o.ID}, limited)
	if e != nil || owner.View.InstanceID != v.InstanceID || owner.RunningSHA256 != r.EngineSHA256 {
		return nil, fmt.Errorf("settled protected identity: %v", e)
	}
	var setup core.SetupRecord
	if e = c.Do(t.Context(), "GET", "/api/v1/setup", nil, &setup); e != nil {
		return nil, e
	}
	if e = c.Do(t.Context(), "PUT", "/api/v1/setup", setup, &setup); e != nil {
		return nil, fmt.Errorf("metadata fence still held: %w", e)
	}
	actors, e := os.ReadFile(filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer/update-fixture-actors.jsonl"))
	if e != nil {
		return nil, e
	}
	rows := bytes.Split(bytes.TrimSpace(actors), []byte{'\n'})
	if len(rows) != 2 {
		return nil, fmt.Errorf("expected original and recovery actors, got %d", len(rows))
	}
	boots := map[string]bool{}
	for _, row := range rows {
		var a struct {
			BootID string `json:"bootId"`
			UID    int    `json:"uid"`
			EUID   int    `json:"euid"`
			CapEff string `json:"capEff"`
			Inert  bool   `json:"inertActor"`
		}
		if json.Unmarshal(row, &a) != nil || a.UID != 1100 || a.EUID != 1100 || a.CapEff != "0" || a.Inert || boots[a.BootID] {
			return nil, errors.New("actor UID/capability/reboot proof differs")
		}
		boots[a.BootID] = true
	}
	unit := "fased-vps-maintenance-1100@" + o.ID + ".service"
	enabled, _ := exec.Command("/usr/bin/systemctl", "is-enabled", unit).CombinedOutput()
	status, _ := exec.Command("/usr/bin/systemctl", "show", unit, "--property=MainPID,ExecMainStatus,Result,ActiveState,SubState").Output()
	if !applicationVMActorRetired(string(enabled), string(status)) {
		return nil, errors.New("original owner actor has not settled and disabled")
	}

	trust, e := applicationVMTrustRead()
	if e != nil {
		return nil, e
	}
	noop := false
	if trust.Finalized {
		if r.ManifestSHA256 != trust.DescriptorSHA256 {
			return nil, errors.New("installed receipt differs from finalizer output")
		}
		beforeActors := applicationVMDigest(actors)
		if e = applicationVMServer(t); e != nil {
			return nil, e
		}
		if v, e = applicationVMUpdateView(t.Context(), c, "GET", nil); e != nil {
			return nil, e
		}
		command := core.ApplicationUpdateCommand{Action: "check", Revision: v.Revision, PolicyRevision: v.PolicyRevision}
		if v, e = applicationVMUpdateView(t.Context(), c, "POST", command); e != nil {
			return nil, e
		}
		for end := time.Now().Add(15 * time.Second); v.Operation.Phase == "check-queued" || v.Operation.Phase == "checking"; {
			if time.Now().After(end) {
				return nil, errors.New("authenticated no-op did not complete")
			}
			time.Sleep(100 * time.Millisecond)
			if v, e = applicationVMUpdateView(t.Context(), c, "GET", nil); e != nil {
				return nil, e
			}
		}
		afterActors, ae := os.ReadFile(filepath.Join(maintenanceProbeHome, ".config/fased-app-vps-installer/update-fixture-actors.jsonl"))
		unchanged, ue := vpsinstall.ApplicationUpdateState(1100)
		if v.Operation.Phase != "current" || v.Operation.Release != nil || applicationVMRequests.Load() == 0 || ae != nil || ue != nil || unchanged.ID != j.ID || unchanged.Phase != "completed" || applicationVMDigest(afterActors) != beforeActors {
			return nil, errors.New("authenticated current check changed installed operation or actor")
		}
		noop = true
	}
	return map[string]any{"finalizerProduced": trust.Finalized, "finalizedDescriptorSha256": trust.DescriptorSHA256, "provenanceSha256": trust.ProvenanceSHA256, "finalizerPlanSha256": trust.PlanSHA256, "authenticatedNoop": noop, "metadataRequests": applicationVMRequests.Load(), "result": "PASS", "fixtureOnly": true, "id": o.ID, "sequence": r.Sequence, "manifestSha256": r.ManifestSHA256, "engineSha256": r.EngineSHA256, "instanceId": v.InstanceID, "signedInstalledUpdateAcceptance": true, "manualReview": true, "offlineOriginalRecovery": true, "workspaceScheduleHoldPreserved": true, "credentialLocked": true, "fenceReleased": true, "consumedReleaseFloor": current.ApplicationSequence, "ownerActorRetired": true, "ordinaryActorStarts": len(rows), "distinctActorBoots": len(boots), "realAI": false, "realWallets": false, "publicActivation": false}, nil
}

func applicationVMUpdateView(ctx context.Context, c *core.Client, method string, input any) (core.ApplicationUpdateView, error) {
	var view core.ApplicationUpdateView
	err := c.Do(ctx, method, "/api/v1/updates/application", input, &view)
	return view, err
}

func TestApplicationUpdateVMNoopFreshResponses(t *testing.T) {
	phases := []string{"completed", "check-queued", "checking", "current"}
	n := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n >= len(phases) || r.URL.Path != "/api/v1/updates/application" {
			t.Error("unexpected no-op request", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		release := ""
		if n == 0 {
			release = `,"release":{"manifestSha256":"previous"}`
		}
		fmt.Fprintf(w, `{"revision":3,"policyRevision":2,"operation":{"phase":%q%s}}`, phases[n], release)
		n++
	}))
	defer server.Close()
	c := &core.Client{Endpoint: core.Endpoint{URL: server.URL}, HTTP: server.Client()}
	var view core.ApplicationUpdateView
	for i, phase := range phases {
		method := "GET"
		var input any
		if i == 1 {
			method = "POST"
			input = core.ApplicationUpdateCommand{Action: "check", Revision: view.Revision, PolicyRevision: view.PolicyRevision}
		}
		var err error
		view, err = applicationVMUpdateView(t.Context(), c, method, input)
		if err != nil || view.Operation.Phase != phase || (view.Operation.Release != nil) != (i == 0) {
			t.Fatal("stale or invalid no-op response", phase, view.Operation, err)
		}
	}
}

func TestApplicationUpdateVMDefaultsRemainClosed(t *testing.T) {
	if compiledApplicationUpdateSource != nil || compiledApplicationUpdateCheck != nil || compiledApplicationReaderSource != nil {
		t.Fatal("ordinary composition unexpectedly activated")
	}
	handled, e := applicationUpdateVMRuntime("/not/an/installed/fixture", []string{"serve"})
	if _, exists := os.Lstat(applicationVMInputs + "/fixture.json"); os.IsNotExist(exists) && (handled || e != nil || compiledApplicationUpdateSource != nil || compiledApplicationUpdateCheck != nil) {
		t.Fatal("absent fixture bound source", handled, e)
	}
}

// RemainAfterExit=yes deliberately retains the successful receipt as
// active(exited). Retirement means no process and no reboot start authority.
func applicationVMActorRetired(enabled, status string) bool {
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || fields[k] != "" {
			return false
		}
		fields[k] = v
	}
	settled := fields["ActiveState"] == "active" && fields["SubState"] == "exited" || fields["ActiveState"] == "inactive" && fields["SubState"] == "dead"
	return strings.TrimSpace(enabled) == "disabled" && fields["MainPID"] == "0" && fields["ExecMainStatus"] == "0" && fields["Result"] == "success" && settled
}
func TestApplicationUpdateVMRetiredOneshot(t *testing.T) {
	valid := "MainPID=0\nExecMainStatus=0\nResult=success\nActiveState=active\nSubState=exited\n"
	if !applicationVMActorRetired("disabled\n", valid) {
		t.Fatal("successful RemainAfterExit oneshot incorrectly held")
	}
	for _, invalid := range []string{strings.Replace(valid, "MainPID=0", "MainPID=42", 1), strings.Replace(valid, "ExecMainStatus=0", "ExecMainStatus=1", 1), strings.Replace(valid, "Result=success", "Result=exit-code", 1), strings.Replace(valid, "SubState=exited", "SubState=running", 1)} {
		if applicationVMActorRetired("disabled", invalid) {
			t.Fatal("running/failed owner was accepted")
		}
	}
	if applicationVMActorRetired("enabled", valid) {
		t.Fatal("reboot actor remains enabled")
	}
}

func applicationVMVaultRetained(v core.AIConnectionView, ref string) bool {
	if ref == "" || v.LiveCallsEnabled {
		return false
	}
	found := 0
	for _, account := range v.Connections {
		if account.Ref == ref {
			if !account.Locked || account.LiveCallsEnabled || !account.TestOnly {
				return false
			}
			found++
		}
	}
	return found == 1
}
func TestApplicationUpdateVMRetainsNondefaultAccount(t *testing.T) {
	account := core.AIConnectionView{Ref: "retained-test-account", Locked: true, TestOnly: true}
	view := core.AIConnectionView{Connections: []core.AIConnectionView{account}}
	if !applicationVMVaultRetained(view, account.Ref) {
		t.Fatal("nondefault account incorrectly lost")
	}
	for _, bad := range []core.AIConnectionView{{Ref: account.Ref, Locked: false, TestOnly: true}, {Ref: account.Ref, Locked: true, TestOnly: false}, {Ref: account.Ref, Locked: true, TestOnly: true, LiveCallsEnabled: true}} {
		if applicationVMVaultRetained(core.AIConnectionView{Connections: []core.AIConnectionView{bad}}, account.Ref) {
			t.Fatal("unlocked/live account accepted")
		}
	}
	if applicationVMVaultRetained(core.AIConnectionView{Connections: []core.AIConnectionView{account, account}}, account.Ref) || applicationVMVaultRetained(view, "missing") {
		t.Fatal("ambiguous/missing account accepted")
	}
}
