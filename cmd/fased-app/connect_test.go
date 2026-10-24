package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"github.com/fased-ai/fased-app/internal/core"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
