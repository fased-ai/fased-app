package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallChoiceExplicitAndCancelled(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"\n", "local"}, {"remote\n", "remote"}, {"vps\n", "remote"}, {"connect\n", "connect"}, {"bad\nconnect\n", "connect"}} {
		got, e := installationChoice(bufio.NewReader(strings.NewReader(tc.input)), io.Discard)
		if e != nil || got != tc.want {
			t.Fatal(got, e)
		}
	}
	for _, input := range []string{"", "local", "bad\n"} {
		if _, e := installationChoice(bufio.NewReader(strings.NewReader(input)), io.Discard); e == nil {
			t.Fatal("EOF continued installation")
		}
	}
}
func TestConnectDoesNotCreateLocalState(t *testing.T) {
	root, e := filepath.Abs("../../.evidence/connect-fixtures")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(root, "cli-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "absent"))
	id := strings.Repeat("a", 43)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(core.Health{Status: "ok", APIVersion: "1", InstanceID: id, Connection: &core.ConnectionInfo{Location: "remote", Transport: "loopback"}})
	}))
	defer server.Close()
	var out bytes.Buffer
	if e = run([]string{"connect", "--url", server.URL, "--instance-id", id, "--print"}, nil, &out, io.Discard); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), `"installationCreated":false`) || !strings.Contains(out.String(), id) {
		t.Fatal(out.String())
	}
	if _, e = os.Stat(filepath.Join(dir, "absent")); !os.IsNotExist(e) {
		t.Fatal("Remote connection created local state", e)
	}
	if e = run([]string{"connect", "--url", server.URL, "--instance-id", strings.Repeat("b", 43), "--print"}, nil, io.Discard, io.Discard); e == nil {
		t.Fatal("changed identity accepted")
	}
}

func TestInteractiveConnectHelper(t *testing.T) {
	if os.Getenv("FASED_INTERACTIVE_CONNECT_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := run(os.Args[i+1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
				os.Stderr.WriteString(err.Error())
				os.Exit(1)
			}
			return
		}
	}
	os.Exit(1)
}
func TestInteractiveConnectUsesTTYWithoutInstallation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux terminal acceptance")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python standard-library PTY harness unavailable")
	}
	root, err := filepath.Abs("../../.evidence/connect-fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "tty-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	id := strings.Repeat("t", 43)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected terminal probe")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(core.Health{Status: "ok", APIVersion: "1", InstanceID: id, Connection: &core.ConnectionInfo{Location: "remote", Transport: "loopback"}})
	}))
	defer target.Close()
	bin := filepath.Join(dir, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// This disposable OS browser fixture records the exact verified destination.
	if err = os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$FASED_CONNECT_CAPTURE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	prefix, data := filepath.Join(dir, "absent-install"), filepath.Join(dir, "absent-state")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A real PTY makes the production interactive-terminal guard part of this check.
	// Python's standard library is already present on the Ubuntu acceptance runner.
	child := exec.CommandContext(ctx, python, "-c", `
import errno, os, pty, subprocess, sys
master, slave = pty.openpty()
child = subprocess.Popen(sys.argv[1:], stdin=slave, stdout=slave, stderr=slave, close_fds=True)
os.close(slave)
os.write(master, sys.stdin.buffer.read())
while True:
    try:
        data = os.read(master, 8192)
    except OSError as error:
        if error.errno == errno.EIO:
            break
        raise
    if not data:
        break
    sys.stdout.buffer.write(data)
    sys.stdout.buffer.flush()
os.close(master)
sys.exit(child.wait())
`, executable, "-test.run=^TestInteractiveConnectHelper$", "--", "install", "--interactive", "--prefix", prefix, "--data-dir", data)
	capture := filepath.Join(dir, "opened-url")
	child.Env = append(os.Environ(), "FASED_INTERACTIVE_CONNECT_HELPER=1", "FASED_CONNECT_CAPTURE="+capture, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	child.Stdin = strings.NewReader("connect\n" + target.URL + "\n")
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	opened, err := os.ReadFile(capture)
	if err != nil || string(opened) != target.URL+"/?instance="+id {
		t.Fatal(string(opened), err, string(output))
	}
	for _, p := range []string{prefix, data} {
		if _, err = os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("connection installed local state", p, err)
		}
	}
	if !strings.Contains(string(output), `"installationCreated":false`) {
		t.Fatal(string(output))
	}
}
