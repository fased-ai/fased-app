package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
	"github.com/fased-ai/fased-app/internal/update"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type firstFixture struct {
	t                                *testing.T
	now                              time.Time
	seed                             Seed
	descriptor                       Descriptor
	root                             *metadata.Metadata[metadata.RootType]
	targets                          *metadata.Metadata[metadata.TargetsType]
	snapshot                         *metadata.Metadata[metadata.SnapshotType]
	timestamp                        *metadata.Metadata[metadata.TimestampType]
	keys                             map[string][]signature.Signer
	files                            map[string][]byte
	server                           *httptest.Server
	archive                          []byte
	parent, marker, arguments        string
	rootSignatures, targetSignatures int
	variant                          string
	mu                               sync.Mutex
	requests                         []string
}

func jsonBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (f *firstFixture) bytes(v interface{ ToBytes(bool) ([]byte, error) }) []byte {
	f.t.Helper()
	b, e := v.ToBytes(false)
	if e != nil {
		f.t.Fatal(e)
	}
	return b
}
func metaFile(v int64, b []byte) *metadata.MetaFiles {
	h, _ := hex.DecodeString(digest(b))
	return &metadata.MetaFiles{Version: v, Length: int64(len(b)), Hashes: metadata.Hashes{"sha256": h}}
}

func newFirstFixture(t *testing.T) *firstFixture {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Ubuntu bootstrap targets Linux amd64")
	}
	f := &firstFixture{t: t, now: time.Now().UTC().Truncate(time.Second), keys: map[string][]signature.Signer{}, files: map[string][]byte{}, parent: t.TempDir(), rootSignatures: 2, targetSignatures: 2}
	if err := os.Chmod(f.parent, 0700); err != nil {
		t.Fatal(err)
	}
	f.marker, f.arguments = filepath.Join(f.parent, "executed"), filepath.Join(f.parent, "arguments")
	t.Setenv("FASED_BOOTSTRAP_MARKER", f.marker)
	t.Setenv("FASED_BOOTSTRAP_ARGS", f.arguments)
	f.root = metadata.Root(f.now.Add(7 * 24 * time.Hour))
	f.root.Signed.ConsistentSnapshot = true
	for role, count := range map[string]int{"root": 3, "targets": 3, "snapshot": 1, "timestamp": 1} {
		for i := 0; i < count; i++ {
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := signature.LoadSigner(priv, crypto.Hash(0))
			if err != nil {
				t.Fatal(err)
			}
			key, err := metadata.KeyFromPublicKey(pub)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.root.Signed.AddKey(key, role); err != nil {
				t.Fatal(err)
			}
			f.keys[role] = append(f.keys[role], signer)
		}
		if count == 3 {
			f.root.Signed.Roles[role].Threshold = 2
		}
	}
	f.seed = Seed{Binding: Binding{Version: 1, Purpose: Purpose, DevelopmentOnly: true, Repository: "fased-ai/fased-app", Channel: "development", Target: update.Target{OS: "linux", Arch: "amd64"}}, CreatedAt: f.now.Add(-time.Minute).Unix(), ExpiresAt: f.now.Add(6 * 24 * time.Hour).Unix(), MinimumSequence: 7}
	m := bundle.Manifest{Schema: 1, Platform: "linux", Arch: "amd64", Version: "fixture", Source: "fixture-source", UnsignedEngineering: true, Files: map[string]bundle.File{}}
	payload := map[string][]byte{"fased-app": []byte("#!/bin/sh\nprintf 'verified installer\\n' >> \"$FASED_BOOTSTRAP_MARKER\"\nprintf '%s\\n' \"$@\" > \"$FASED_BOOTSTRAP_ARGS\"\n"), "LICENSE": []byte("fixture"), "NOTICE": []byte("fixture"), "INSTALL.txt": []byte("fixture guide")}
	for name := range bundle.PlatformFiles() {
		payload[name] = []byte("fixture " + name)
	}
	for name, b := range payload {
		m.Files[name] = bundle.File{SHA256: digest(b), Bytes: int64(len(b))}
	}
	payload["bundle.json"] = jsonBytes(t, m)
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
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.archive = packed.Bytes()
	f.descriptor = Descriptor{Binding: f.seed.Binding, Sequence: 7, ProductVersion: m.Version, Source: m.Source, BundleSHA256: digest(payload["bundle.json"]), Archive: update.Artifact{File: archiveTarget, Size: int64(len(f.archive)), SHA256: digest(f.archive)}}
	f.targets = metadata.Targets(f.now.Add(2 * 24 * time.Hour))
	f.snapshot = metadata.Snapshot(f.now.Add(24 * time.Hour))
	f.timestamp = metadata.Timestamp(f.now.Add(time.Hour))
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("download inherited credential, query or compression authority")
		}
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Path)
		b := f.files[r.URL.Path]
		variant := f.variant
		f.mu.Unlock()
		if variant == "metadata-redirect" && r.URL.Path == "/timestamp.json" || variant == "archive-redirect" && strings.HasSuffix(r.URL.Path, archiveTarget) {
			http.Redirect(w, r, "https://untrusted.invalid/elsewhere", http.StatusFound)
			return
		}
		if variant == "compressed" {
			w.Header().Set("Content-Encoding", "gzip")
		}
		if variant == "partial" {
			w.Header().Set("Content-Range", "bytes 0-2/9")
		}
		if b == nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	f.server = server
	f.seed.Origin = server.URL
	f.publish()
	return f
}

func (f *firstFixture) publish() {
	f.t.Helper()
	f.root.ClearSignatures()
	for _, s := range f.keys["root"][:f.rootSignatures] {
		if _, err := f.root.Sign(s); err != nil {
			f.t.Fatal(err)
		}
	}
	f.seed.Root = f.bytes(f.root)
	db := jsonBytes(f.t, f.descriptor)
	f.targets.ClearSignatures()
	for name, b := range map[string][]byte{descriptorTarget: db, archiveTarget: f.archive} {
		v, e := metadata.TargetFile().FromBytes(name, b, "sha256")
		if e != nil {
			f.t.Fatal(e)
		}
		f.targets.Signed.Targets[name] = v
	}
	for _, s := range f.keys["targets"][:f.targetSignatures] {
		if _, e := f.targets.Sign(s); e != nil {
			f.t.Fatal(e)
		}
	}
	tg := f.bytes(f.targets)
	f.snapshot.ClearSignatures()
	f.snapshot.Signed.Meta = map[string]*metadata.MetaFiles{"targets.json": metaFile(1, tg)}
	if _, e := f.snapshot.Sign(f.keys["snapshot"][0]); e != nil {
		f.t.Fatal(e)
	}
	sn := f.bytes(f.snapshot)
	f.timestamp.ClearSignatures()
	f.timestamp.Signed.Meta = map[string]*metadata.MetaFiles{"snapshot.json": metaFile(1, sn)}
	if _, e := f.timestamp.Sign(f.keys["timestamp"][0]); e != nil {
		f.t.Fatal(e)
	}
	f.files = map[string][]byte{"/timestamp.json": f.bytes(f.timestamp), "/1.snapshot.json": sn, "/1.targets.json": tg, "/targets/bootstrap/" + digest(db) + ".descriptor.json": db, "/targets/" + f.descriptor.Archive.SHA256 + "." + archiveTarget: f.archive}
}

func (f *firstFixture) options() Options {
	b := jsonBytes(f.t, f.seed)
	return Options{Development: true, Seed: b, SeedSHA256: digest(b), Directory: filepath.Join(f.parent, "verified"), TLSCA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), now: func() time.Time { return f.now }}
}

func TestFirstInstallVerifiesBeforeExecution(t *testing.T) {
	f := newFirstFixture(t)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	p, err := Prepare(t.Context(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r := p.Report()
	if !r.MetadataVerified || !r.ArchiveVerified || !r.BundleVerified || r.InstallerInvoked || r.PublicActivation {
		t.Fatal(r)
	}
	if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
		t.Fatal("verification executed downloaded code")
	}
	var out bytes.Buffer
	opts := InstallOptions{Prefix: filepath.Join(f.parent, "app"), DataDirectory: filepath.Join(f.parent, "data"), Start: false}
	if err = p.Run(t.Context(), opts, strings.NewReader("local\nn\n"), &out, &out); err != nil {
		t.Fatal(err, out.String())
	}
	if err = p.Run(t.Context(), opts, nil, &out, &out); err == nil {
		t.Fatal("execution replay accepted")
	}
	b, err := os.ReadFile(f.marker)
	if err != nil || string(b) != "verified installer\n" {
		t.Fatal("wrong execution count", err)
	}
	args, err := os.ReadFile(f.arguments)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"install\n", "--interactive\n", "--start=false\n", p.report.BundleSHA256, "--bootstrap-sequence\n7\n", "--bootstrap-release-sha256\n" + p.report.DescriptorSHA256 + "\n"} {
		if !strings.Contains(string(args), wanted) {
			t.Fatal("existing installer handoff lost a bound argument")
		}
	}
}

func TestFirstInstallLeavesProfileDefaultsToWizard(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default dedicated Remote", true: "explicit Local override"}[explicit], func(t *testing.T) {
			f := newFirstFixture(t)
			p, err := Prepare(t.Context(), f.options())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			opts := InstallOptions{Start: false}
			if explicit {
				opts.Prefix = filepath.Join(f.parent, "chosen-app")
			}
			if err = p.Run(t.Context(), opts, strings.NewReader("remote\nssh\n\n"), io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			args, err := os.ReadFile(f.arguments)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(args), "--data-dir\n") || strings.Contains(string(args), "--prefix\n") != explicit {
				t.Fatal("inferred Local defaults became explicit wizard overrides", string(args))
			}
			if explicit && !strings.Contains(string(args), opts.Prefix+"\n") {
				t.Fatal("explicit Local override lost", string(args))
			}
			if !strings.Contains(string(args), "--interactive\n") || !strings.Contains(string(args), p.report.BundleSHA256) {
				t.Fatal("verified wizard binding lost", string(args))
			}
		})
	}
}

func TestFirstInstallRefusesUntrustedInputsWithoutExecution(t *testing.T) {
	for _, name := range []string{"no-development", "no-pin", "substituted-pin", "wrong-purpose", "production-channel", "wrong-repository", "wrong-target", "expired-seed", "http", "origin-path", "seed-query", "one-root-signature", "one-target-signature", "weakened-root", "shared-role-key", "expired-timestamp", "expired-targets", "below-floor", "wrong-descriptor-purpose", "wrong-inner-source", "bad-archive-hash", "short-archive", "long-archive", "duplicate-metadata", "metadata-redirect", "archive-redirect", "compressed", "partial", "untrusted-tls", "cancelled", "existing-output", "symlink-output", "public-parent"} {
		t.Run(name, func(t *testing.T) {
			f := newFirstFixture(t)
			switch name {
			case "wrong-purpose":
				f.seed.Purpose = update.Purpose
			case "production-channel":
				f.seed.Channel = "stable"
			case "wrong-repository":
				f.seed.Repository = "other/repo"
			case "wrong-target":
				f.seed.Target.Arch = "arm64"
			case "expired-seed":
				f.seed.ExpiresAt = f.now.Unix()
			case "http":
				f.seed.Origin = strings.Replace(f.seed.Origin, "https:", "http:", 1)
			case "origin-path":
				f.seed.Origin += "/other"
			case "seed-query":
				f.seed.Origin += "?credential=fixture"
			case "one-root-signature":
				f.rootSignatures = 1
			case "one-target-signature":
				f.targetSignatures = 1
			case "weakened-root":
				f.root.Signed.Roles["root"].Threshold = 1
			case "shared-role-key":
				f.root.Signed.Roles["targets"].KeyIDs[0] = f.root.Signed.Roles["root"].KeyIDs[0]
			case "expired-timestamp":
				f.timestamp.Signed.Expires = f.now
			case "expired-targets":
				f.targets.Signed.Expires = f.now
			case "below-floor":
				f.seed.MinimumSequence = 8
			case "wrong-descriptor-purpose":
				f.descriptor.Purpose = update.Purpose
			case "wrong-inner-source":
				f.descriptor.Source = "different-source"
			}
			f.publish()
			o := f.options()
			ctx := t.Context()
			switch name {
			case "no-development":
				o.Development = false
			case "no-pin":
				o.SeedSHA256 = ""
			case "substituted-pin":
				o.SeedSHA256 = strings.Repeat("0", 64)
			case "bad-archive-hash":
				f.files["/targets/"+f.descriptor.Archive.SHA256+"."+archiveTarget] = bytes.Repeat([]byte{'x'}, len(f.archive))
			case "short-archive":
				f.files["/targets/"+f.descriptor.Archive.SHA256+"."+archiveTarget] = f.archive[:len(f.archive)-1]
			case "long-archive":
				f.files["/targets/"+f.descriptor.Archive.SHA256+"."+archiveTarget] = append(append([]byte(nil), f.archive...), 0)
			case "duplicate-metadata":
				f.files["/timestamp.json"] = append([]byte(`{"signed":{},`), f.files["/timestamp.json"][1:]...)
			case "metadata-redirect", "archive-redirect", "compressed", "partial":
				f.variant = name
			case "untrusted-tls":
				o.TLSCA = nil
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "existing-output":
				if err := os.Mkdir(o.Directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(o.Directory, "original"), []byte("owner data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-output":
				if err := os.Symlink(t.TempDir(), o.Directory); err != nil {
					t.Fatal(err)
				}
			case "public-parent":
				if err := os.Chmod(f.parent, 0755); err != nil {
					t.Fatal(err)
				}
			}
			p, err := Prepare(ctx, o)
			if err == nil {
				p.Close()
				t.Fatal("untrusted bootstrap accepted")
			}
			if p != nil {
				t.Fatal("failed bootstrap returned execution capability")
			}
			if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("failed bootstrap executed downloaded code")
			}
			if name == "existing-output" {
				b, e := os.ReadFile(filepath.Join(o.Directory, "original"))
				if e != nil || string(b) != "owner data" {
					t.Fatal("existing data changed")
				}
			} else if name != "symlink-output" {
				if _, e := os.Lstat(o.Directory); !os.IsNotExist(e) {
					t.Fatal("failed bootstrap left output", e)
				}
			}
		})
	}
}

func TestFirstInstallRevalidatesBeforeRun(t *testing.T) {
	for _, name := range []string{"expired", "backward-clock", "changed-engine", "changed-archive", "closed", "overlapping-state", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			f := newFirstFixture(t)
			p, err := Prepare(t.Context(), f.options())
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			opts := InstallOptions{Prefix: filepath.Join(f.parent, "app"), DataDirectory: filepath.Join(f.parent, "data")}
			ctx := t.Context()
			switch name {
			case "expired":
				f.now = f.now.Add(2 * time.Hour)
			case "backward-clock":
				f.now = f.now.Add(-time.Second)
			case "changed-engine":
				if err = os.WriteFile(filepath.Join(p.report.BundleDirectory, "fased-app"), []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
			case "changed-archive":
				if err = os.WriteFile(filepath.Join(p.options.Directory, "download.tar.gz"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "closed":
				p.Close()
			case "overlapping-state":
				opts.DataDirectory = filepath.Join(p.options.Directory, "state")
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err = p.Run(ctx, opts, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("invalidated bootstrap executed")
			}
			if _, err = os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("invalidated bootstrap ran downloaded code")
			}
		})
	}
}

func TestFirstInstallRefusesSamePublicKeyWithAlternateEncoding(t *testing.T) {
	f := newFirstFixture(t)
	rootID := f.root.Signed.Roles["root"].KeyIDs[0]
	oldTargetID := f.root.Signed.Roles["targets"].KeyIDs[0]
	encoded := strings.ToUpper(f.root.Signed.Keys[rootID].Value.PublicKey)
	alias := &metadata.Key{Type: "ed25519", Scheme: "ed25519", Value: metadata.KeyVal{PublicKey: encoded}}
	aliasID, err := alias.ID()
	if err != nil {
		t.Fatal(err)
	}
	delete(f.root.Signed.Keys, oldTargetID)
	f.root.Signed.Keys[aliasID] = alias
	f.root.Signed.Roles["targets"].KeyIDs[0] = aliasID
	f.publish()
	b := jsonBytes(t, f.seed)
	if _, err := parseSeed(b, digest(b), f.now); err == nil {
		t.Fatal("same public-key bytes counted as independent role identities")
	}
}

func TestFirstInstallDefersDefaultPathsUntilProfileChoice(t *testing.T) {
	f := newFirstFixture(t)
	t.Setenv("XDG_CONFIG_HOME", f.parent)
	o := f.options()
	o.Directory = filepath.Join(f.parent, "fased-app")
	p, err := Prepare(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Run(t.Context(), InstallOptions{Start: false}, strings.NewReader("remote\nssh\n\n"), io.Discard, io.Discard); err != nil {
		t.Fatal("Local defaults blocked another profile before selection", err)
	}
	args, err := os.ReadFile(f.arguments)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "--data-dir\n") || !strings.Contains(string(args), "--verification-output\n"+o.Directory+"\n") {
		t.Fatal("selected-profile output guard lost", string(args))
	}
}

func TestFirstInstallConcurrentInvocationIsSingleUse(t *testing.T) {
	f := newFirstFixture(t)
	p, err := Prepare(t.Context(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	options := InstallOptions{Prefix: filepath.Join(f.parent, "app"), DataDirectory: filepath.Join(f.parent, "data"), Start: false}
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- p.Run(t.Context(), options, nil, io.Discard, io.Discard) }()
	}
	success := 0
	for range 2 {
		if <-results == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent bootstrap execution count", success)
	}
	b, err := os.ReadFile(f.marker)
	if err != nil || string(b) != "verified installer\n" {
		t.Fatal("concurrent installer replay", err)
	}
}
