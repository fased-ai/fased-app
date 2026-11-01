//go:build linux

package bootstrap

import (
	"encoding/json"
	"encoding/pem"
	"github.com/fased-ai/fased-app/internal/bundle"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in ordinary-user controlled HTTPS producer for the authorized real VPS
// test. No public activation/credentials or application endpoint override.
func TestOneCommandVPSFixture(t *testing.T) {
	output, archivePath, manifestPath, verifierPath := os.Getenv("FASED_ENTRY_EVIDENCE"), os.Getenv("FASED_ENTRY_ARCHIVE"), os.Getenv("FASED_ENTRY_MANIFEST"), os.Getenv("FASED_ENTRY_VERIFIER")
	if output == "" || archivePath == "" || manifestPath == "" || verifierPath == "" {
		t.Skip("explicit frozen disposable fixture inputs required")
	}
	if os.Geteuid() == 0 {
		t.Fatal("fixture producer cannot run as root")
	}
	project, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(output, 0700); e != nil {
		t.Fatal(e)
	}
	archive, e := os.ReadFile(archivePath)
	if e != nil {
		t.Fatal(e)
	}
	manifestBytes, e := os.ReadFile(manifestPath)
	if e != nil {
		t.Fatal(e)
	}
	var m bundle.Manifest
	if json.Unmarshal(manifestBytes, &m) != nil || m.Schema != 1 || !m.UnsignedEngineering {
		t.Fatal("disposable engineering input only")
	}
	verifier, e := os.ReadFile(verifierPath)
	if e != nil {
		t.Fatal(e)
	}
	f := newFirstFixture(t)
	originalHandler := f.server.Config.Handler
	originalServer := f.server
	logRequest := func(stage, path string) {
		f, e := os.OpenFile(filepath.Join(output, "requests-live.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if e == nil {
			b, _ := json.Marshal(map[string]any{"stage": stage, "path": path, "at": time.Now().UTC()})
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logRequest("start", r.URL.Path)
		defer logRequest("end", r.URL.Path)
		if r.URL.Path == "/fased-install" {
			f.mu.Lock()
			f.requests = append(f.requests, r.URL.Path)
			f.mu.Unlock()
			data := verifier
			if _, e := os.Stat(filepath.Join(output, "tamper-installer")); e == nil {
				data = append([]byte(nil), verifier...)
				data[0] ^= 1
			}
			_, _ = w.Write(data)
			return
		}
		originalHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	originalServer.Close()
	f.server = server
	f.seed.Origin = server.URL
	f.archive = archive
	if os.Getenv("FASED_ENTRY_UPDATE_JOURNEY") == "1" {
		f.seed.MinimumSequence = 6
		f.descriptor.Sequence = 6
	}
	f.descriptor.ProductVersion = m.Version
	f.descriptor.Source = m.Source
	f.descriptor.BundleSHA256 = digest(manifestBytes)
	f.descriptor.Archive.Size = int64(len(archive))
	f.descriptor.Archive.SHA256 = digest(archive)
	f.publish()
	options := f.options()
	seedFile, caFile, scriptFile := filepath.Join(output, "seed.json"), filepath.Join(output, "ca.pem"), filepath.Join(output, "install.fixture.sh")
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	for name, data := range map[string][]byte{seedFile: options.Seed, caFile: ca} {
		if e = os.WriteFile(name, data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	config := map[string]any{"version": "0.0.0-fixture", "installer": verifierPath, "seed": seedFile, "fixture": true, "url": server.URL + "/fased-install", "ca": caFile}
	configFile := filepath.Join(output, "render.json")
	if e = os.WriteFile(configFile, jsonBytes(t, config), 0600); e != nil {
		t.Fatal(e)
	}
	c := exec.Command("node", filepath.Join(project, "scripts/render-install-entry.mjs"), configFile, scriptFile)
	if out, e := c.CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	script, e := os.ReadFile(scriptFile)
	if e != nil {
		t.Fatal(e)
	}
	f.mu.Lock()
	f.files["/install.sh"] = script
	f.mu.Unlock()
	// Persist unique signed public fixture metadata; private signing keys stay disposable memory.
	if e = os.MkdirAll(filepath.Join(output, "metadata"), 0700); e != nil {
		t.Fatal(e)
	}
	for name, data := range f.files {
		if strings.HasSuffix(name, ".json") {
			if e = os.WriteFile(filepath.Join(output, "metadata", strings.ReplaceAll(strings.TrimPrefix(name, "/"), "/", "_")), data, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	ready := map[string]any{"fixtureOnly": true, "origin": server.URL, "scriptSHA256": digest(script), "verifierSHA256": digest(verifier), "archiveSHA256": digest(archive), "seedSHA256": options.SeedSHA256}
	if e = os.WriteFile(filepath.Join(output, "ready.json"), jsonBytes(t, ready), 0600); e != nil {
		t.Fatal(e)
	}
	t.Log("controlled one-command VPS fixture ready")
	timer := time.NewTimer(12 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timer.C:
			t.Fatal("bounded VPS fixture expired")
		case <-ticker.C:
			if _, e := os.Stat(filepath.Join(output, "stop")); e == nil {
				f.mu.Lock()
				requests := append([]string(nil), f.requests...)
				f.mu.Unlock()
				if e = os.WriteFile(filepath.Join(output, "requests.json"), jsonBytes(t, requests), 0600); e != nil {
					t.Fatal(e)
				}
				return
			}
		}
	}
}
