//go:build linux

package maintenance

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPackageAuthorityIsScopedToExactInstance(t *testing.T) {
	id := strings.Repeat("a", 32)
	a := Authority{Version: 1, Owner: 1400, Group: 1400, Home: "/home/owner", Profile: "local", ImageSHA256: strings.Repeat("b", 64)}
	policy, err := packageSudoPolicy(a, "local", id)
	if err != nil {
		t.Fatal(err)
	}
	want := "#1400 ALL=(root) NOPASSWD: NOSETENV: " + ImagePath(a.ImageSHA256) + " maintenance-package-request --owner 1400 --location local --instance " + id
	lines := strings.Split(strings.TrimSpace(string(policy)), "\n")
	if len(lines) != 2 || lines[1] != want {
		t.Fatalf("unexpected package authority: %q", policy)
	}
	for _, forbidden := range []string{"*", "/bin/sh", "/usr/bin/install", "/usr/bin/systemctl"} {
		if strings.Contains(string(policy), forbidden) {
			t.Fatalf("unexpected OS authority %q", forbidden)
		}
	}
	for _, row := range []struct{ location, instance string }{{"remote", id}, {"../local", id}, {"local", "../" + id}, {"local", strings.ToUpper(id)}} {
		if _, err := packageSudoPolicy(a, row.location, row.instance); err == nil {
			t.Fatal("foreign or malformed scope accepted", row)
		}
	}
	if visudo, err := exec.LookPath("visudo"); err == nil {
		cmd := exec.Command(visudo, "-c", "-f", "-")
		cmd.Stdin = bytes.NewReader(policy)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("policy refused: %v: %s", err, out)
		}
	}
}

func TestPackageSystemLockSerializesAndSurvivesRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "system-package.lock")
	first, err := lockPackageFile(path, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockPackageFile(path, os.Getuid(), os.Getgid()); err == nil {
		second.Close()
		first.Close()
		t.Fatal("concurrent package operation acquired the same lock")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := waitPackageFile(cancelled, path, os.Getuid(), os.Getgid()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled package lock wait continued", err)
	}
	type lockResult struct {
		file *os.File
		err  error
	}
	ready := make(chan lockResult, 1)
	ctx, done := context.WithTimeout(t.Context(), 2*time.Second)
	defer done()
	go func() {
		f, err := waitPackageFile(ctx, path, os.Getuid(), os.Getgid())
		ready <- lockResult{file: f, err: err}
	}()
	select {
	case result := <-ready:
		if result.file != nil {
			result.file.Close()
		}
		first.Close()
		t.Fatal("a concurrent package operation did not wait", result.err)
	case <-time.After(30 * time.Millisecond):
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	result := <-ready
	if result.err != nil {
		t.Fatal("the waiting package operation did not continue", result.err)
	}
	if err = result.file.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "foreign.lock")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if bad, err := lockPackageFile(link, os.Getuid(), os.Getgid()); err == nil {
		bad.Close()
		t.Fatal("linked package lock was accepted")
	}
}

func TestPackageRequestCannotSelectCommandsOrPaths(t *testing.T) {
	for _, input := range []string{`{"version":1,"action":"status"}`, `{"version":1,"action":"initialize"}`, `{"version":1,"action":"owner-channel"}`, `{"version":1,"action":"provision","listen":"127.0.0.1:7331"}`, `{"version":1,"action":"candidate-stage","candidate":{"packetSha256":"` + strings.Repeat("a", 64) + `","scope":"fresh"}}`} {
		if _, err := DecodePackageRequest([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []string{
		`{"version":1,"action":"initialize","path":"/tmp"}`,
		`{"version":1,"action":"candidate-stage","candidate":{"packetSha256":"` + strings.Repeat("a", 64) + `","scope":"fresh","path":"/tmp"}}`,
		`{"version":1,"action":"status","path":"/etc/shadow"}`,
		`{"version":1,"action":"status","action":"update"}`,
		`{"version":1,"action":"status","instance":"other"}`,
		`{"version":1,"action":"owner-channel","path":"/etc/shadow"}`,
		`{"version":1,"action":"provision","listen":"0.0.0.0:7331"}`,
		`{"version":1,"action":"provision","listen":"127.0.0.1:80"}`,
		`{"version":1,"action":"provision","listen":"127.0.0.1:07331"}`,
		`{"version":1,"action":"provision","listen":"127.0.0.1:7331","path":"/tmp"}`,
		`{"Version":1,"action":"status"}`,
		`{"version":1,"Action":"status"}`,
	} {
		if _, err := DecodePackageRequest([]byte(input)); err == nil {
			t.Fatal("unimplemented or caller-directed operation accepted", input)
		}
	}
}

func TestPendingPackageInputResumesOnlyExactBytes(t *testing.T) {
	data := []byte("protected authority contents\n")
	for _, tc := range []struct {
		name  string
		input []byte
		ok    bool
	}{{"empty", nil, true}, {"partial", data[:8], true}, {"complete", data, true}, {"different", []byte("other"), false}, {"long", append(append([]byte(nil), data...), 'x'), false}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pending")
			if err := os.WriteFile(path, tc.input, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			err = resumePendingInput(f, data, 0644, uint32(os.Getuid()), uint32(os.Getgid()))
			f.Close()
			if (err == nil) != tc.ok {
				t.Fatalf("resume error %v", err)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || tc.ok && !bytes.Equal(got, data) || !tc.ok && !bytes.Equal(got, tc.input) {
				t.Fatalf("pending bytes changed unexpectedly: %q, %v", got, readErr)
			}
		})
	}
	t.Run("hard link", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "pending")
		if err := os.WriteFile(path, data[:8], 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, path+"-link"); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = resumePendingInput(f, data, 0644, uint32(os.Getuid()), uint32(os.Getgid()))
		f.Close()
		got, readErr := os.ReadFile(path)
		if err == nil || readErr != nil || !bytes.Equal(got, data[:8]) {
			t.Fatal("linked pending input was accepted or modified", err, readErr)
		}
	})
}

func TestPackageAuthorityRejectsOrdinaryProcessBeforeMutation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("ordinary-owner refusal requires an unprivileged test runner")
	}
	id := strings.Repeat("a", 32)
	if _, err := RegisterPackage(os.Getuid(), "local", id); err == nil {
		t.Fatal("ordinary process registered privileged package authority")
	}
	if _, err := ValidatePackageProcess(os.Getuid(), "local", id); err == nil {
		t.Fatal("ordinary process entered privileged package handler")
	}
}
