//go:build linux

package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/bundle"
)

// Opt-in compiled producer/verifier/installer proof. Its fake user manager and
// stopped disposable instance cannot establish real Ubuntu/VPS acceptance.
func TestFirstInstallCompiledWizard(t *testing.T) {
	binary, receipt := os.Getenv("FASED_FIRST_INSTALL_BINARY"), os.Getenv("FASED_FIRST_INSTALL_PACKAGE")
	if binary == "" || receipt == "" {
		t.Skip("explicit frozen verifier and producer receipt required")
	}
	if os.Geteuid() == 0 {
		t.Fatal("project code must run as an ordinary user")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var producer struct {
		Archive          string          `json:"archive"`
		Manifest         bundle.Manifest `json:"manifest"`
		ManifestIdentity bundle.File     `json:"manifestIdentity"`
		Identity         bundle.File     `json:"identity"`
		PublicRelease    bool            `json:"publicRelease"`
	}
	if err = json.Unmarshal(b, &producer); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(producer.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if digest(archive) != producer.Identity.SHA256 || producer.PublicRelease {
		t.Fatal("unbound/non-development producer output")
	}
	for _, mode := range []string{"local", "remote"} {
		t.Run(mode, func(t *testing.T) {
			f := newFirstFixture(t)
			f.archive = archive
			f.descriptor.ProductVersion = producer.Manifest.Version
			f.descriptor.Source = producer.Manifest.Source
			f.descriptor.BundleSHA256 = producer.ManifestIdentity.SHA256
			f.descriptor.Archive.Size = int64(len(archive))
			f.descriptor.Archive.SHA256 = digest(archive)
			f.publish()
			o := f.options()
			seedFile, caFile := filepath.Join(f.parent, "seed.json"), filepath.Join(f.parent, "ca.pem")
			if err := os.WriteFile(seedFile, o.Seed, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			app, data := filepath.Join(f.parent, "app"), filepath.Join(f.parent, "data")
			if err := os.Mkdir(data, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, "original"), []byte("original disposable owner record"), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--development", "--seed-file", seedFile, "--seed-sha256", o.SeedSHA256, "--tls-ca-file", caFile, "--output", o.Directory, "--install", "--prefix", app, "--data-dir", data, "--start=false"}
			bad := append([]string(nil), args...)
			bad[4] = strings.Repeat("0", 64)
			refuse := exec.Command(binary, bad...)
			if out, err := refuse.CombinedOutput(); err == nil {
				t.Fatal("compiled verifier accepted wrong pin", string(out))
			}
			if _, err := os.Stat(app); !os.IsNotExist(err) {
				t.Fatal("wrong pin installed bundle")
			}
			if _, err := os.Stat(o.Directory); !os.IsNotExist(err) {
				t.Fatal("wrong pin created download output")
			}
			tools := filepath.Join(f.parent, "tools")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"loginctl": "#!/bin/sh\nprintf 'yes\\n'\n", "systemctl": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$FASED_FIRST_MANAGER_LOG\"\ncase \"$2\" in show) printf 'LoadState=not-found\\n';; esac\n"} {
				if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, python, append([]string{"-c", `
import errno, os, pty, subprocess, sys
master, slave = pty.openpty()
child = subprocess.Popen(sys.argv[1:], stdin=slave, stdout=slave, stderr=slave, close_fds=True)
os.close(slave)
os.write(master, sys.stdin.buffer.read())
try:
    while True:
        try:
            data = os.read(master, 8192)
        except OSError as error:
            if error.errno == errno.EIO: break
            raise
        if not data: break
        sys.stdout.buffer.write(data)
        sys.stdout.buffer.flush()
finally:
    os.close(master)
sys.exit(child.wait())
`, binary}, args...)...)
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
			command.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "FASED_FIRST_MANAGER_LOG="+filepath.Join(f.parent, "manager.log"))
			input := "local\nn\n"
			if mode == "remote" {
				input = "remote\nssh\n\n"
			}
			command.Stdin = strings.NewReader(input)
			out, err := command.CombinedOutput()
			if err != nil {
				t.Fatal("compiled verifier/wizard failed", err, string(out))
			}
			if !bytes.Contains(out, []byte(`"installerInvoked":true`)) || !bytes.Contains(out, []byte(`"walletsConnected":false`)) {
				t.Fatal("missing explicit disabled installer result", string(out))
			}
			installed, err := os.ReadFile(filepath.Join(app, "fased-app"))
			if err != nil || digest(installed) != producer.Manifest.Files["fased-app"].SHA256 {
				t.Fatal("wrong installed bytes", err)
			}
			var install struct {
				Phase, Location string
				Autostart       bool
			}
			b, err := os.ReadFile(filepath.Join(app, "install.json"))
			if err != nil || json.Unmarshal(b, &install) != nil || install.Phase != "ready" || install.Location != mode || install.Autostart != (mode == "remote") {
				t.Fatal("wrong Local/Remote installation", err, string(b))
			}
			original, err := os.ReadFile(filepath.Join(data, "original"))
			if err != nil || string(original) != "original disposable owner record" {
				t.Fatal("existing data changed")
			}
			if mode == "local" {
				if _, err := os.Stat(filepath.Join(f.parent, "manager.log")); !os.IsNotExist(err) {
					t.Fatal("stopped Local installed a service")
				}
			}
		})
	}
}
