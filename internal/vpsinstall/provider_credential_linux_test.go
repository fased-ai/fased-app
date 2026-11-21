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
	"github.com/fased-ai/fased-app/internal/tailnet"
)

func TestRemoteAcceptsOnlyExactProtectedIngressDropIn(t *testing.T) {
	t.Parallel()
	h, o, m, _ := credentialFixture(t)
	m.PublicOrigin = "https://fixture.example.ts.net"
	path, body, err := tailnet.ProtectedSocketDropIn("vps", m.OwnerUID)
	if err != nil {
		t.Fatal(err)
	}
	name, socket, err := tailnet.ProtectedSocketUnit("vps", m.OwnerUID)
	if err != nil {
		t.Fatal(err)
	}
	h.files[filepath.Dir(path)] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
	h.files[path] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: body}
	h.files["/etc/systemd/system/"+name] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: socket}
	if !validTaskCredentialDropIn(o, m, path) {
		t.Fatal("exact protected ingress drop-in refused")
	}
	if validTaskCredentialDropIn(o, m, path+" "+path) || validTaskCredentialDropIn(o, m, path+" /etc/systemd/system/foreign.conf") {
		t.Fatal("duplicate or foreign drop-in accepted")
	}
	delete(h.files, "/etc/systemd/system/"+name)
	if validTaskCredentialDropIn(o, m, path) {
		t.Fatal("drop-in accepted without its exact protected socket unit")
	}
}

func TestLoadedProtectedIngressRequiresReloadedSystemd(t *testing.T) {
	t.Parallel()
	h, o, m, _ := credentialFixture(t)
	m.PublicOrigin = "https://fixture.example.ts.net"
	h.files[m.UnitPath] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: unit(m)}
	path, body, err := tailnet.ProtectedSocketDropIn("vps", m.OwnerUID)
	if err != nil {
		t.Fatal(err)
	}
	name, socket, err := tailnet.ProtectedSocketUnit("vps", m.OwnerUID)
	if err != nil {
		t.Fatal(err)
	}
	h.files[filepath.Dir(path)] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
	h.files[path] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: body}
	h.files["/etc/systemd/system/"+name] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: socket}
	command := o.command
	stale := "no"
	o.command = func(ctx context.Context, args ...string) ([]byte, error) {
		b, e := command(ctx, args...)
		b = bytes.Replace(b, []byte("DropInPaths=\n"), []byte("DropInPaths="+path+"\n"), 1)
		return append(b, []byte("NeedDaemonReload="+stale+"\n")...), e
	}
	if err := loaded(t.Context(), o, m); err != nil {
		t.Fatal(err)
	}
	stale = "yes"
	if err := loaded(t.Context(), o, m); err == nil {
		t.Fatal("unloaded ingress configuration accepted")
	}
}

func TestProtectedIngressProvisionPreservesForeignAndRetries(t *testing.T) {
	t.Parallel()
	h, o, m, _ := credentialFixture(t)
	m.PublicOrigin = "https://fixture.example.ts.net"
	probeCalls := 0
	o.ingressContract = func(_ context.Context, target Manifest) error {
		probeCalls++
		if target.Core != m.Core || target.AppUID != m.AppUID {
			t.Fatal("foreign service probed")
		}
		return nil
	}
	o.protectedPublish = func(path string, data []byte, mode os.FileMode) error {
		if previous, ok := h.files[path]; ok && !known(previous, data, mode) {
			return os.ErrExist
		}
		h.files[path] = hostFile{UID: 0, GID: 0, Mode: mode, Regular: true, Data: bytes.Clone(data)}
		return nil
	}
	path, body, _ := tailnet.ProtectedSocketDropIn("vps", m.OwnerUID)
	name, unit, _ := tailnet.ProtectedSocketUnit("vps", m.OwnerUID)
	for range 2 {
		if err := prepareProtectedIngress(t.Context(), o, m); err != nil {
			t.Fatal(err)
		}
	}
	if probeCalls != 2 || !known(h.files[path], body, 0644) || !known(h.files["/etc/systemd/system/"+name], unit, 0644) {
		t.Fatal("matching socket files not retained")
	}
	foreign := []byte("foreign service policy")
	h.files[path] = hostFile{UID: 0, GID: 0, Mode: 0644, Regular: true, Data: foreign}
	if err := prepareProtectedIngress(t.Context(), o, m); err == nil || !bytes.Equal(h.files[path].Data, foreign) {
		t.Fatal("foreign drop-in was overwritten")
	}
	delete(h.files, path)
	o.ingressContract = nil
	if err := prepareProtectedIngress(t.Context(), o, m); err == nil {
		t.Fatal("unverified core accepted")
	}
}

func TestProtectedRemoteInstallProvisionsIngressBeforeService(t *testing.T) {
	t.Parallel()
	h, o, options := fixtureHost()
	options.PublicOrigin = "https://fixture.example.ts.net"
	var published []string
	o.protectedPublish = func(path string, data []byte, mode os.FileMode) error {
		if previous, ok := h.files[path]; ok && !known(previous, data, mode) {
			return os.ErrExist
		}
		published = append(published, path)
		h.files[path] = hostFile{UID: 0, GID: 0, Mode: mode, Regular: true, Data: bytes.Clone(data)}
		return nil
	}
	o.ingressContract = func(_ context.Context, m Manifest) error {
		if f := h.files[m.Core]; !f.Regular || hash(f.Data) != m.CoreSHA256 {
			t.Fatal("unverified installed core used")
		}
		return nil
	}
	command := o.command
	o.command = func(ctx context.Context, args ...string) ([]byte, error) {
		b, err := command(ctx, args...)
		path, _, _ := tailnet.ProtectedSocketDropIn("vps", o.uid)
		if _, ok := h.files[path]; ok {
			b = bytes.Replace(b, []byte("DropInPaths=\n"), []byte("DropInPaths="+path+"\n"), 1)
			b = append(b, []byte("NeedDaemonReload=no\n")...)
		}
		return b, err
	}
	m, err := install(t.Context(), options, o)
	if err != nil {
		t.Fatal(err)
	}
	drop, _, _ := tailnet.ProtectedSocketDropIn("vps", m.OwnerUID)
	name, _, _ := tailnet.ProtectedSocketUnit("vps", m.OwnerUID)
	positions := map[string]int{}
	for i, path := range published {
		positions[path] = i + 1
	}
	if positions["/etc/systemd/system/"+name] == 0 || positions[drop] <= positions["/etc/systemd/system/"+name] || positions[m.UnitPath] <= positions[drop] {
		t.Fatal("protected listener was not installed before the service", published)
	}
}

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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	if *count != 1 || h.files[taskCredentialPath(m)].Mode.Perm() != 0600 {
		t.Fatal("ciphertext was replaced or cannot be verified without elevation")
	}
}

func TestProtectedProviderCredentialRefusesForeignState(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"oversized-target", "oversized-stage", "foreign-dropin", "replaceable-tool-parent", "foreign-directory", "private-directory", "foreign-file", "wrong-file-mode"} {
		t.Run(fault, func(t *testing.T) {
			h, o, m, _ := credentialFixture(t)
			path := taskCredentialPath(m)
			switch fault {
			case "oversized-target":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
				h.files[path] = hostFile{UID: 0, Mode: 0600, Regular: true, Data: make([]byte, 16385)}
			case "oversized-stage":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
				stage := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"-"+m.ID+".pending")
				h.files[stage] = hostFile{UID: 0, Mode: 0600, Regular: true, Data: make([]byte, 16385)}
			case "foreign-directory":
				h.files[taskCredentialDirectory] = hostFile{UID: 1000, Mode: os.ModeDir | 0755, Directory: true}
			case "private-directory":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0700, Directory: true}
			case "foreign-file", "wrong-file-mode":
				h.files[taskCredentialDirectory] = hostFile{UID: 0, Mode: os.ModeDir | 0755, Directory: true}
				f := hostFile{UID: 0, Mode: 0600, Regular: true, Data: bytes.Repeat([]byte{7}, 100)}
				if fault == "foreign-file" {
					f.UID = 1000
				} else {
					f.Mode = 0666
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
