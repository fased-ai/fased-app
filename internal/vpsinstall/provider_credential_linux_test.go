//go:build linux

package vpsinstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
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

func TestProviderCredentialOverrideUsesBoundPrivilegedWrite(t *testing.T) {
	h, o, m, _ := credentialFixture(t)
	if err := prepareTaskCredentialDropIn(t.Context(), o, m); err != nil {
		t.Fatal(err)
	}
	for _, args := range h.calls {
		if custodyinstall.AllowedBoundUnitWrite(o.uid, taskCredentialDropIn(m), args) {
			return
		}
	}
	t.Fatal("provider credential override did not use a content-bound privileged write")
}

func TestProtectedProviderCredentialOrdinaryReader(t *testing.T) {
	h, o, m, count := credentialFixture(t)
	read := o.read
	o.read = func(p string) (hostFile, error) {
		for parent := filepath.Dir(p); parent != "/"; parent = filepath.Dir(parent) {
			if f, ok := h.files[parent]; ok && f.UID != o.uid && f.Mode.Perm()&0001 == 0 {
				return hostFile{}, os.ErrPermission
			}
		}
		f, err := read(p)
		if err == nil && f.Regular && f.UID != o.uid && f.Mode.Perm()&0004 == 0 {
			return f, os.ErrPermission
		}
		return f, err
	}
	for range 2 {
		if err := prepareTaskCredential(context.Background(), o, m); err != nil {
			t.Fatal("ordinary installer cannot verify encrypted identity", err)
		}
	}
	if *count != 1 || h.files[taskCredentialPath(m)].Mode.Perm() != 0644 {
		t.Fatal("ciphertext was replaced or cannot be verified without elevation")
	}
}

func TestProtectedProviderCredentialRefusesForeignState(t *testing.T) {
	for _, fault := range []string{"oversized-target", "oversized-stage", "foreign-dropin", "replaceable-tool-parent", "foreign-directory", "private-directory", "foreign-file", "wrong-file-mode"} {
		t.Run(fault, func(t *testing.T) {
			h, o, m, _ := credentialFixture(t)
			path := taskCredentialPath(m)
			switch fault {
			case "oversized-target":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
				h.files[path] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: make([]byte, 16385)}
			case "oversized-stage":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
				stage := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"-"+m.ID+".pending")
				h.files[stage] = hostFile{UID: 0, Mode: 0644, Regular: true, Data: make([]byte, 16385)}
			case "foreign-directory":
				h.files[taskCredentialDirectory] = hostFile{UID: 1000, Mode: os.ModeDir | 0755, Directory: true}
			case "private-directory":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0700, Directory: true}
			case "foreign-file", "wrong-file-mode":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
				f := hostFile{UID: 0, Mode: 0644, Regular: true, Data: bytes.Repeat([]byte{7}, 100)}
				if fault == "foreign-file" {
					f.UID = 1000
				} else {
					f.Mode = 0600
				}
				h.files[path] = f
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
