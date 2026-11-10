//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		mode := os.FileMode(0644)
		if len(args) > 2 && args[2] == "755" {
			mode = 0755
		}
		data := input
		if src := args[len(args)-2]; src != "/dev/stdin" {
			f, ok := h.files[src]
			if !ok {
				return nil, os.ErrNotExist
			}
			data = f.Data
		}
		h.files[args[len(args)-1]] = hostFile{Data: bytes.Clone(data), UID: 0, Mode: mode, Regular: true}
	case "/usr/bin/systemd-run":
		if !AllowedBoundUnitWrite(os.Getuid(), input, args) && !AllowedBoundPolicyWrite(os.Getuid(), input, args) {
			return nil, errors.New("unbound service staging request")
		}
		stage := strings.TrimPrefix(args[7], "--property=StandardOutput=truncate:")
		mode := os.FileMode(0644)
		if args[6] == "--property=UMask=0227" {
			mode = 0440
		}
		h.files[stage] = hostFile{Data: bytes.Clone(input), UID: 0, Mode: mode, Regular: true}
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

func TestScopedUpdateExecutablesUseApprovedImmutableSource(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	intent := strings.Repeat("b", 32)
	data := []byte("disposable approved signer")
	source := ApprovedExecutablePath(owner, data)
	root := Root(owner)
	for _, target := range []string{
		root + "/updates/" + intent + "/next-signer",
		root + "/.replace-" + id + "-fased-signer-" + sum(data),
	} {
		h := &memoryHost{files: map[string]hostFile{source: {Data: bytes.Clone(data), UID: 0, Mode: 0444, Regular: true}}}
		used := false
		run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
			if args[0] == "/usr/bin/install" {
				used = len(input) == 0 && args[len(args)-2] == source
			}
			return h.run(ctx, input, args...)
		}
		if err := publish(t.Context(), target, id, data, 0755, h.read, run); err != nil || !used || !known(h.files[target], data, 0755) {
			t.Fatalf("unbound update executable %s: %v", target, err)
		}
		h = &memoryHost{files: map[string]hostFile{}}
		refuse := func(path, digest string, size int64) error {
			if path != source || digest != filepath.Base(source) || size != int64(len(data)) {
				t.Fatal("wrong executable identity passed to verifier")
			}
			return errors.New("approved image changed")
		}
		if err := publish(t.Context(), target, id, data, 0755, h.read, h.run, refuse); err == nil || h.calls != 0 {
			t.Fatal("changed approved image reached a privileged operation")
		}
	}
	for _, target := range []string{
		root + "/updates/" + intent + "/unrelated",
		root + "/.replace-" + id + "-fased-signer-" + strings.Repeat("0", 64),
		Root(owner+1) + "/updates/" + intent + "/next-signer",
	} {
		if scopedUpdateExecutable(target, id, owner, data) {
			t.Fatalf("foreign update destination accepted: %s", target)
		}
	}
}

func TestScopedExecutablePublicationUsesApprovedImmutableSource(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	target := fmt.Sprintf("/var/lib/.fased-custody-%d-%s.pending/fased-signer", owner, id)
	data := []byte("disposable fixture signer image")
	source := ApprovedExecutablePath(owner, data)
	h := &memoryHost{files: map[string]hostFile{source: {Data: bytes.Clone(data), UID: 0, Mode: 0444, Regular: true}}}
	used := false
	run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/install" {
			used = args[len(args)-2] == source && len(input) == 0
		}
		return h.run(ctx, input, args...)
	}
	if err := publish(context.Background(), target, id, data, 0755, h.read, run); err != nil || !used || !known(h.files[target], data, 0755) {
		t.Fatal("approved signer source was not bound", err)
	}
	h = &memoryHost{files: map[string]hostFile{source: {Data: bytes.Clone(data), UID: 0, Mode: 0444, Regular: true}}}
	streamed := false
	verify := func(path, digest string, size int64) error {
		streamed = path == source && digest == filepath.Base(source) && size == int64(len(data))
		return nil
	}
	read := func(path string) (hostFile, error) {
		if path == source {
			return hostFile{}, errors.New("whole-image read was attempted")
		}
		return h.read(path)
	}
	if err := publish(context.Background(), target, id, data, 0755, read, h.run, verify); err != nil || !streamed {
		t.Fatal("approved signer did not use streaming verification", err)
	}
	h = &memoryHost{files: map[string]hostFile{source: {Data: []byte("different"), UID: 0, Mode: 0444, Regular: true}}}
	if err := publish(context.Background(), target, id, data, 0755, h.read, h.run); err == nil || h.calls != 0 {
		t.Fatal("changed approved signer source was accepted")
	}
}

func TestBoundServicePublicationRecoversEachInterruptedStep(t *testing.T) {
	target := fmt.Sprintf("/etc/systemd/system/%s", Unit(os.Getuid(), "app"))
	id := strings.Repeat("a", 32)
	data := []byte("[Service]\nUser=991\nNoNewPrivileges=yes\n")
	for fail := 1; fail <= 6; fail++ {
		h := &memoryHost{files: map[string]hostFile{}, failAfter: fail}
		if err := publish(context.Background(), target, id, data, 0644, h.read, h.run); err == nil {
			t.Fatalf("interruption %d did not fail", fail)
		}
		h.failAfter = 0
		if err := publish(context.Background(), target, id, data, 0644, h.read, h.run); err != nil || !known(h.files[target], data, 0644) || len(h.files) != 1 {
			t.Fatalf("interruption %d did not recover the exact service: %v", fail, err)
		}
	}
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

func TestNetworkInstallerIdentity(t *testing.T) {
	for _, network := range []string{"devnet", "mainnet-beta"} {
		m := fixtureManifest()
		m.Version = 2
		m.Mode = modeFor(network)
		m.Network = network
		if e := m.validate(); e != nil {
			t.Fatal(e)
		}
		c := identityConfig(m)
		if e := c.Validate(); e != nil || c.Network != network || c.Version != 2 {
			t.Fatal(c, e)
		}
		j := journal{Version: 2, Network: network, ID: m.ID, Owner: m.Owner, CoreHash: m.CoreHash, SignerHash: m.SignerHash, Listen: m.Listen}
		if e := j.validate(m.Owner); e != nil {
			t.Fatal(e)
		}
		if installerIntent(m) != j {
			t.Fatal("completion lost installed network identity")
		}
		j.Version = 1
		if j.validate(m.Owner) == nil {
			t.Fatal("old intent accepted network")
		}
		m.Version = 1
		if m.validate() == nil {
			t.Fatal("old manifest accepted network")
		}
		m.Version = 2
		m.InspectionBridge = strings.Repeat("a", 32)
		if m.validate() == nil {
			t.Fatal("fixture reader bridge accepted network custody")
		}
	}
	m := fixtureManifest()
	c := identityConfig(m)
	if c.Version != 1 || c.Network != "" {
		t.Fatal("legacy identity changed")
	}
	for _, network := range []string{"testnet", "https://rpc.invalid", "solana"} {
		if custodyMode(2, modeFor(network), network) {
			t.Fatal("unsupported network")
		}
	}
}

func TestNetworkCustodyCannotUseLegacyControllers(t *testing.T) {
	tx, _ := modelTransaction(t)
	if !tx.valid() {
		t.Fatal("invalid fixture baseline")
	}
	for _, m := range []*Manifest{&tx.Base, &tx.Target} {
		m.Version = 2
		m.Mode = modeFor("devnet")
		m.Network = "devnet"
	}
	tx.Binding.PreviousManifest = sum(mustJSON(tx.Base))
	if tx.valid() {
		t.Fatal("legacy installation controller accepted network custody")
	}
	boot, _, _ := bootstrapModel(t)
	if _, e := boot.Plan(); e != nil {
		t.Fatal(e)
	}
	for _, m := range []*Manifest{&boot.Base, &boot.Target} {
		m.Version = 2
		m.Mode = modeFor("devnet")
		m.Network = "devnet"
	}
	if _, e := boot.Plan(); e == nil {
		t.Fatal("legacy bootstrap accepted network custody")
	}
}
