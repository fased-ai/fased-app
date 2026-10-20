//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func fixtureManifest() Manifest {
	return Manifest{Version: 1, Mode: "disposable-fixture:v1", ID: strings.Repeat("1", 32), Owner: 1000, Root: Root(1000), Listen: "127.0.0.1:7332", CoreHash: strings.Repeat("2", 64), SignerHash: strings.Repeat("3", 64), App: Identity{accountName(1000, "app"), 998, 998}, Signer: Identity{accountName(1000, "signer"), 997, 997}}
}
func TestIdentityAndPrivateListenerContract(t *testing.T) {
	m := fixtureManifest()
	if e := m.validate(); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*Manifest){func(m *Manifest) { m.App.UID = m.Owner }, func(m *Manifest) { m.Signer.UID = m.App.UID }, func(m *Manifest) { m.App.GID = m.Signer.GID }, func(m *Manifest) { m.Signer.UID = 0 }, func(m *Manifest) { m.Root = "/tmp/foreign" }, func(m *Manifest) { m.Mode = "solana-mainnet" }, func(m *Manifest) { m.Listen = "0.0.0.0:7332" }, func(m *Manifest) { m.Listen = "127.0.0.1:0" }, func(m *Manifest) { m.ID = "../../foreign" }} {
		bad := m
		change(&bad)
		if bad.validate() == nil {
			t.Fatal("unsafe manifest accepted")
		}
	}
	for _, role := range []string{"app", "signer"} {
		line := accountName(1000, role) + ":x:998:998:" + marker(m.ID, role) + ":/nonexistent:/usr/sbin/nologin"
		if _, e := parseAccount(line, accountName(1000, role), marker(m.ID, role)); e != nil {
			t.Fatal(e)
		}
		if _, e := parseAccount(strings.ReplaceAll(line, "/usr/sbin/nologin", "/bin/bash"), accountName(1000, role), marker(m.ID, role)); e == nil {
			t.Fatal("interactive/foreign account adopted")
		}
	}
}
func TestConsentFailsBeforeHostOrInputAccess(t *testing.T) {
	for _, o := range []Options{{}, {Disposable: true}, {PrepareHost: true}} {
		_, e := Install(context.Background(), o, &System{Out: io.Discard})
		if e == nil || !strings.Contains(e.Error(), "consent") {
			t.Fatal("missing consent reached host work")
		}
	}
}

type memoryHost struct {
	files     map[string]hostFile
	calls     int
	failAfter int
}

func (h *memoryHost) read(p string) (hostFile, error) {
	f, ok := h.files[p]
	if !ok {
		return hostFile{}, os.ErrNotExist
	}
	return f, nil
}
func (h *memoryHost) run(_ context.Context, input []byte, args ...string) ([]byte, error) {
	h.calls++
	switch args[0] {
	case "/usr/bin/install":
		h.files[args[len(args)-1]] = hostFile{Data: bytes.Clone(input), UID: 0, Mode: 0644, Regular: true}
	case "/usr/bin/ln":
		target := args[len(args)-1]
		if _, ok := h.files[target]; ok {
			return nil, os.ErrExist
		}
		h.files[target] = h.files[args[len(args)-2]]
	case "/usr/bin/rm":
		delete(h.files, args[len(args)-1])
	case "/usr/bin/sync":
	default:
		return nil, errors.New("unexpected elevated tool")
	}
	if h.calls == h.failAfter {
		return nil, errors.New("disposable interruption after OS side effect")
	}
	return nil, nil
}
func TestExclusivePublicationRetryPreservesBytes(t *testing.T) {
	for fail := 1; fail <= 6; fail++ {
		h := &memoryHost{files: map[string]hostFile{}, failAfter: fail}
		data := []byte("reconstructible fixed fixture input")
		target := "/root-owned/fixture.json"
		if e := publish(context.Background(), target, "fixture", data, 0644, h.read, h.run); e == nil {
			t.Fatalf("injection %d did not fail", fail)
		}
		h.failAfter = 0
		if e := publish(context.Background(), target, "fixture", data, 0644, h.read, h.run); e != nil {
			t.Fatal(e)
		}
		if !known(h.files[target], data, 0644) || len(h.files) != 1 {
			t.Fatal("retry changed bytes or retained staging")
		}
		calls := h.calls
		if e := publish(context.Background(), target, "fixture", data, 0644, h.read, h.run); e != nil || h.calls != calls {
			t.Fatal("ready retry wrote host state")
		}
	}
}
func TestForeignTargetsAndStagesAreNeverMutated(t *testing.T) {
	target := "/root-owned/fixture.json"
	stage := "/root-owned/.fixture.json-fixture.pending"
	for _, where := range []string{target, stage} {
		for _, f := range []hostFile{{Data: []byte("foreign"), UID: 0, Mode: 0644, Regular: true}, {Data: []byte("expected"), UID: 1000, Mode: 0644, Regular: true}, {Data: []byte("expected"), UID: 0, Mode: 0666, Regular: true}, {Data: []byte("expected"), UID: 0, Mode: 0644}} {
			h := &memoryHost{files: map[string]hostFile{where: f}}
			if publish(context.Background(), target, "fixture", []byte("expected"), 0644, h.read, h.run) == nil || h.calls != 0 || !bytes.Equal(h.files[where].Data, f.Data) {
				t.Fatal("foreign root state mutated")
			}
		}
	}
}
