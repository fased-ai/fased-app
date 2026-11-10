package ownerclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func intent() signer.OwnerIntent {
	return signer.OwnerIntent{Version: 1, InstanceID: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{13}, 32)), OperationID: strings.Repeat("a", 32), Action: "setup", Network: "devnet"}
}

func TestRemoteOwnerTargetVerifiedBeforePrivateInput(t *testing.T) {
	i := intent()
	peer := Peer{Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: 1007}
	var calls int
	client, err := open(t.Context(), i, "https://owner.example.ts.net:8443", "alice", "/private/known_hosts", 1000, func(_ context.Context, path string, args []string, input []byte) ([]byte, error) {
		calls++
		if path != "/usr/bin/ssh" || len(input) != 0 {
			t.Fatal("preflight sent private input")
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "owner.example.ts.net:8443") || !strings.Contains(joined, "owner.example.ts.net") {
			t.Fatal("HTTPS port leaked into SSH target", joined)
		}
		for _, flag := range []string{"-F /dev/null", "-T -a", "BatchMode=yes", "StrictHostKeyChecking=yes", "PasswordAuthentication=no", "KbdInteractiveAuthentication=no", "ClearAllForwardings=yes", "ProxyCommand=none", "ControlPath=none"} {
			if !strings.Contains(joined, flag) {
				t.Fatal("missing SSH boundary", flag)
			}
		}
		if calls == 1 {
			if !strings.HasSuffix(joined, "/usr/bin/id -u") {
				t.Fatal(joined)
			}
			return []byte("1007\n"), nil
		}
		if !strings.Contains(joined, "/var/lib/fased-app-custody-1007/fased-app custody window") || !strings.HasSuffix(joined, "--inspect") {
			t.Fatal(joined)
		}
		return json.Marshal(peer)
	})
	if err != nil || calls != 2 || client.Peer != peer {
		t.Fatal("target not bound", err)
	}
	for _, origin := range []string{"http://owner.example.ts.net", "https://public.example", "https://owner.example.ts.net:22"} {
		if _, err := open(t.Context(), i, origin, "alice", "pins", 1000, func(context.Context, string, []string, []byte) ([]byte, error) {
			t.Fatal("untrusted origin executed SSH")
			return nil, nil
		}); err == nil {
			t.Fatal("unsafe target accepted", origin)
		}
	}
	_, err = open(t.Context(), i, "https://owner.example.ts.net", "alice", "pins", 1000, func(context.Context, string, []string, []byte) ([]byte, error) {
		return nil, errors.New("unverified key")
	})
	if err == nil {
		t.Fatal("unknown host key admitted")
	}
}

func TestPrivateWalletLinkRetainsIsolatedHTTPSPort(t *testing.T) {
	i := intent()
	origin := "https://owner.example.ts.net:8443"
	link, err := Link(i.InstanceID, i.Network, i.Action, origin)
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := ParseLink(link)
	if err != nil || got != origin {
		t.Fatal(got, err)
	}
	if _, err = Link(i.InstanceID, i.Network, i.Action, "https://owner.example.ts.net:22"); err == nil {
		t.Fatal("privileged port accepted")
	}
}

func TestOwnerTargetMismatchRefusedBeforeSecrets(t *testing.T) {
	i := intent()
	for _, change := range []func(*Peer){func(p *Peer) { p.InstanceID = "foreign" }, func(p *Peer) { p.Network = "mainnet-beta" }, func(p *Peer) { p.OwnerUID = 1001 }, func(p *Peer) { p.SignerID = "bad" }} {
		p := Peer{Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: 1000}
		change(&p)
		_, err := open(t.Context(), i, "", "", "", 1000, func(context.Context, string, []string, []byte) ([]byte, error) { return json.Marshal(p) })
		if err == nil {
			t.Fatal("different enrollment admitted")
		}
	}
}

func TestOwnerResumeAndDurableBackup(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	i := intent()
	link, err := Link(i.InstanceID, i.Network, i.Action, "")
	if err != nil {
		t.Fatal(err)
	}
	a, origin, err := Resume(root, link)
	if err != nil || origin != "" {
		t.Fatal(err)
	}
	b, _, err := Resume(root, link)
	if err != nil || a != b {
		t.Fatal("relaunch changed operation", err)
	}
	result := signer.OwnerResult{Intent: a, Backup: []byte("encrypted-test-backup"), Fingerprint: signer.BackupFingerprint([]byte("encrypted-test-backup"))}
	path, saved, err := SaveBackup(root, result)
	if err != nil || !bytes.Equal(saved, result.Backup) {
		t.Fatal(err)
	}
	if _, _, err = SaveBackup(root, result); err != nil {
		t.Fatal("resume backup", err)
	}
	result.Backup = []byte("different-encrypted-backup")
	result.Fingerprint = signer.BackupFingerprint(result.Backup)
	if _, _, err = SaveBackup(root, result); err == nil {
		t.Fatal("backup overwritten")
	}
	actual, err := os.ReadFile(path)
	if err != nil || bytes.Contains(actual, result.Backup) {
		t.Fatal("original backup changed")
	}
	foreign, _ := Link(i.InstanceID, i.Network, "import", "")
	changed, _, err := Resume(root, foreign)
	if err != nil || changed.Action != "import" || changed.OperationID == a.OperationID {
		t.Fatal("cancel-before-dispatch prevented correction", err)
	}
	if err = MarkDispatched(root, changed); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Resume(root, link); err == nil {
		t.Fatal("possibly dispatched operation silently replaced")
	}
	if _, _, err = ParseLink(link + "&password=secret"); err == nil {
		t.Fatal("secret field in public intent")
	}
	symlink := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(root, symlink); err != nil {
		t.Fatal(err)
	}
	if _, _, err = Resume(symlink, foreign); err == nil {
		t.Fatal("linked backup directory accepted")
	}
}

func TestOwnerExchangeRejectsUnboundAndUnsafeReply(t *testing.T) {
	i := intent()
	p := Peer{Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: 1000}
	c := &Client{Intent: i, Peer: p, run: func(context.Context, string, []string, []byte) ([]byte, error) {
		return []byte(`{"walletSigner":true}`), nil
	}}
	in := signer.OwnerInput{Intent: i, SignerID: p.SignerID, Phase: "prepare", Unlock: []byte(strings.Repeat("x", 16)), BackupPassword: []byte(strings.Repeat("y", 16))}
	if _, err := c.Exchange(t.Context(), in); err == nil {
		t.Fatal("unsafe receipt accepted")
	}
	in.Intent.OperationID = strings.Repeat("c", 32)
	called := false
	c.run = func(context.Context, string, []string, []byte) ([]byte, error) { called = true; return nil, nil }
	if _, err := c.Exchange(t.Context(), in); err == nil || called {
		t.Fatal("changed intent dispatched")
	}
}

func TestOwnerOpenResumesOnlyVerifiedNotReadyTarget(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained-not-ready", true: "foreign-refuses"}[foreign], func(t *testing.T) {
			i := intent()
			ready := false
			calls := 0
			p := Peer{Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: 1007, Ready: &ready}
			if foreign {
				p.InstanceID = "foreign"
			}
			c, err := open(t.Context(), i, "https://owner.example.ts.net", "alice", "/private/pins", 1000, func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
				calls++
				if calls == 1 {
					return []byte("1007\n"), nil
				}
				if strings.Contains(strings.Join(args, " "), "/usr/bin/test") {
					return nil, nil
				}
				return json.Marshal(p)
			})
			if c != nil {
				t.Fatal("not-ready owner exposed private client")
			}
			if errors.Is(err, ErrEnrollmentRequired) == foreign || err == nil {
				t.Fatal("resumption classification lost identity check", err)
			}
		})
	}
}

func TestOwnerTargetReadinessIsNotPersistedIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "owner")
	i := intent()
	p := Peer{Version: 1, InstanceID: i.InstanceID, SignerID: strings.Repeat("b", 32), Network: i.Network, OwnerUID: 1007}
	origin := "https://owner.example.ts.net"
	// Preserve older approved targets that have no readiness field.
	if err := RememberTarget(root, origin, "alice", p); err != nil {
		t.Fatal(err)
	}
	for _, state := range []bool{true, true, false, true} {
		next := p
		v := state
		next.Ready = &v
		if err := RememberTarget(root, origin, "alice", next); err != nil {
			t.Fatal("transient readiness changed target", err)
		}
	}
	path := filepath.Join(root, i.InstanceID+".target.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(`"ready"`)) {
		t.Fatal("runtime readiness persisted as identity")
	}
	// Normalize a document produced by the earlier unshipped adapter too.
	v := true
	p.Ready = &v
	if err := writeState(path, ownerTarget{origin, "alice", p}); err != nil {
		t.Fatal(err)
	}
	if err := RememberTarget(root, origin, "alice", p); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if bytes.Contains(b, []byte(`"ready"`)) {
		t.Fatal("old transient state not removed")
	}
	p.SignerID = strings.Repeat("c", 32)
	if err := RememberTarget(root, origin, "alice", p); err == nil {
		t.Fatal("readiness normalization lost signer identity")
	}
}
