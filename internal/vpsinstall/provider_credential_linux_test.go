//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func credentialFixture(t *testing.T) (*memoryHost, hostOps, Manifest, *int) {
	t.Helper()
	h, o, opt := fixtureHost()
	m, e := install(context.Background(), opt, o)
	if e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{"/usr", "/usr/bin"} {
		h.files[dir] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
	}
	h.files["/usr/bin/systemd-creds"] = hostFile{UID: 0, Mode: 0755, Regular: true}
	count := new(int)
	original := o.run
	o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/systemd-creds" {
			if len(b) != 32 || !contains(args, "--with-key=host") || !contains(args, "--name=fased-provider-task-key") {
				t.Fatal("incorrect secret handoff")
			}
			*count++
			return bytes.Repeat([]byte("encrypted-fixture-"), 8), nil
		}
		return original(ctx, b, args...)
	}
	command := o.command
	o.command = func(ctx context.Context, args ...string) ([]byte, error) {
		b, e := command(ctx, args...)
		if args[0] == "/usr/bin/systemctl" && args[1] == "show" {
			if _, ok := h.files[taskCredentialDropInPath(m)]; ok {
				b = bytes.Replace(b, []byte("DropInPaths=\n"), []byte("DropInPaths="+taskCredentialDropInPath(m)+"\n"), 1)
			}
		}
		return b, e
	}
	return h, o, m, count
}

func TestProtectedProviderCredentialInstallRetryRemove(t *testing.T) {
	h, o, m, count := credentialFixture(t)
	ctx := context.Background()
	for range 2 {
		if e := prepareTaskCredential(ctx, o, m); e != nil {
			t.Fatal(e)
		}
	}
	if *count != 1 {
		t.Fatal("retry replaced encryption identity")
	}
	path := taskCredentialPath(m)
	original := bytes.Clone(h.files[path].Data)
	if !strings.Contains(string(taskCredentialDropIn(m)), ":"+path+"\n") {
		t.Fatal("credential lookup is not pinned to retained file")
	}
	if e := start(ctx, o, m); e != nil {
		t.Fatal(e)
	}
	if e := remove(ctx, o, m); e != nil {
		t.Fatal(e)
	}
	if _, ok := h.files[taskCredentialDropInPath(m)]; ok {
		t.Fatal("removed service retained override")
	}
	if !bytes.Equal(original, h.files[path].Data) {
		t.Fatal("removal lost retained OS identity")
	}
	if e := prepareTaskCredential(ctx, o, m); e != nil {
		t.Fatal(e)
	}
	if *count != 1 || !bytes.Equal(original, h.files[path].Data) {
		t.Fatal("repair changed OS identity")
	}
}

func TestProtectedProviderCredentialRefusesForeignState(t *testing.T) {
	for _, fault := range []string{"oversized-target", "oversized-stage", "foreign-dropin", "replaceable-tool-parent"} {
		t.Run(fault, func(t *testing.T) {
			h, o, m, _ := credentialFixture(t)
			path := taskCredentialPath(m)
			switch fault {
			case "oversized-target":
				h.files["/etc/credstore.encrypted"] = hostFile{UID: 0, Mode: os.ModeDir | 0700, Directory: true}
				h.files[path] = hostFile{UID: 0, Mode: 0600, Regular: true, Data: make([]byte, 16385)}
			case "oversized-stage":
				h.files["/etc/credstore.encrypted"] = hostFile{UID: 0, Mode: os.ModeDir | 0700, Directory: true}
				stage := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"-"+m.ID+".pending")
				h.files[stage] = hostFile{UID: 0, Mode: 0600, Regular: true, Data: make([]byte, 16385)}
			case "foreign-dropin":
				h.files[filepath.Dir(taskCredentialDropInPath(m))] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
				h.files[taskCredentialDropInPath(m)] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: []byte("foreign override")}
			case "replaceable-tool-parent":
				h.files["/usr/bin"] = hostFile{UID: 1000, Mode: os.ModeDir | 0777, Directory: true}
			}
			if e := prepareTaskCredential(context.Background(), o, m); e == nil {
				t.Fatal("unsafe OS state accepted", fault)
			}
		})
	}
}
