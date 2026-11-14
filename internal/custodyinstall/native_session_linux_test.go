//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestPinnedNativeUpdateSessionRequiresExactApplicationIdentity(t *testing.T) {
	for _, remote := range []bool{false, true} {
		tx, _, _, _ := ordinaryBootstrapModel(t, remote)
		m := tx.Base
		pin := &reusableMaintenanceAuthority{Version: 1, Owner: m.Owner, InstallationID: m.ID, RunnerSHA256: strings.Repeat("c", 64), GuardSHA256: strings.Repeat("d", 64)}
		image, record := reusableMaintenancePath(m, "runner"), reusableMaintenancePath(m, "authority.json")
		check := func(current Manifest, uid, euid, gid int, gotImage, gotRecord string, p *reusableMaintenanceAuthority) error {
			return validatePinnedNativeUpdateSession(current, p, uid, euid, gid, gotImage, gotRecord)
		}
		if err := check(m, m.App.UID, m.App.UID, m.App.GID, image, record, pin); err != nil {
			t.Fatal("exact pinned session refused", err)
		}
		updated := m
		updated.CoreHash, updated.SignerHash = strings.Repeat("e", 64), strings.Repeat("f", 64)
		if err := check(updated, updated.App.UID, updated.App.UID, updated.App.GID, image, record, pin); err != nil {
			t.Fatal("unchanged installation identity did not survive update", err)
		}
		for _, bad := range []struct {
			uid, euid, gid int
			image, record  string
			pin            *reusableMaintenanceAuthority
		}{
			{m.Owner, m.Owner, m.App.GID, image, record, pin},
			{m.App.UID, 0, m.App.GID, image, record, pin},
			{m.App.UID, m.App.UID, m.App.GID + 1, image, record, pin},
			{m.App.UID, m.App.UID, m.App.GID, m.Root + "/fased-app", record, pin},
			{m.App.UID, m.App.UID, m.App.GID, image, "/tmp/foreign", pin},
			{m.App.UID, m.App.UID, m.App.GID, image, record, nil},
		} {
			if err := check(m, bad.uid, bad.euid, bad.gid, bad.image, bad.record, bad.pin); err == nil {
				t.Fatal("foreign pinned session was accepted")
			}
		}
		policy, err := reusableAtomicPolicy(m, "fased_owner")
		if err != nil || !strings.Contains(string(policy), reusableMaintenancePath(m, "runner")+" reader-session-client --custody-owner "+strconv.Itoa(m.Owner)+" --binding "+record) {
			t.Fatal("fixed application-identity session command missing", err)
		}
	}
}

func TestNativeUpdateSessionRefusesAnyRecoveryFence(t *testing.T) {
	m := fixtureManifest()
	for _, tc := range []struct {
		name    string
		err     error
		wantErr bool
	}{
		{"absent", os.ErrNotExist, false},
		{"present", nil, true},
		{"unreadable", errors.New("unreadable"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := requireUnfencedUpdateSession(m, func(path string) (hostFile, error) {
				if path != m.Root+"/update-start-blocked.json" {
					t.Fatalf("unexpected path %s", path)
				}
				return hostFile{}, tc.err
			})
			if (got != nil) != tc.wantErr {
				t.Fatalf("fence check: %v", got)
			}
		})
	}
}

func TestNativeUpdateSessionBindingAndGeneration(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t)
	m := tx.Base
	b := nativeUpdateSessionBinding{1, m, sum([]byte("current verified reader"))}
	for _, bad := range []string{"", "owner-uid", "root", "gid", "foreign-generation", "remote", "no-native", "no-enrollment", "image", "record", "hash", "version"} {
		t.Run(bad, func(t *testing.T) {
			current, binding := m, b
			uid, euid, gid := m.App.UID, m.App.UID, m.App.GID
			image, record := b.dir()+"/helper", b.dir()+"/binding.json"
			switch bad {
			case "owner-uid":
				uid = m.Owner
			case "root":
				euid = 0
			case "gid":
				gid++
			case "foreign-generation":
				current.CoreHash = strings.Repeat("f", 64)
			case "remote":
				current.Location = "remote"
			case "no-native":
				current.NativeOwnerAccess = false
			case "no-enrollment":
				current.Enrollment = false
			case "image":
				image = m.Root + "/fased-app"
			case "record":
				record = "/tmp/binding.json"
			case "hash":
				binding.ImageSHA256 = "bad"
			case "version":
				binding.Version++
			}
			if bad == "" || bad == "owner-uid" || bad == "root" || bad == "gid" || bad == "remote" || bad == "no-native" || bad == "no-enrollment" {
				if err := validateInstalledNativeUpdateSession(current, uid, euid, gid, current.Root+"/fased-app", current.Root+"/app-state"); (err == nil) != (bad == "") {
					t.Fatal("installed role result", bad, err)
				}
			}
			if err := validateNativeUpdateSession(current, binding, uid, euid, gid, image, record); (err == nil) != (bad == "") {
				t.Fatal("binding result", bad, err)
			}
		})
	}
	next := b
	next.Installation.CoreHash = strings.Repeat("e", 64)
	if b.dir() == next.dir() {
		t.Fatal("next installed generation collides with old immutable helper")
	}
}
func TestNativeUpdateSessionStageDropsAndPreservesForeignState(t *testing.T) {
	for _, kind := range []string{"ok", "repeat", "foreign-helper", "foreign-binding", "refused-auth", "wrong-output", "remote-ok", "remote-repeat", "remote-foreign-helper", "remote-foreign-binding", "remote-refused-auth", "remote-wrong-output"} {
		t.Run(kind, func(t *testing.T) {
			tx, dir, _, _ := ordinaryBootstrapModel(t)
			m := tx.Base
			if strings.HasPrefix(kind, "remote-") {
				m.Location, m.NativeOwnerAccess, m.PublicOrigin = "remote", false, "https://owner.example.ts.net"
				kind = strings.TrimPrefix(kind, "remote-")
			}
			body := []byte("verified current source reader")
			binding := nativeUpdateSessionBinding{1, m, sum(body)}
			path := func(s string) string { return dir + strings.TrimPrefix(s, m.Root) }
			if err := os.MkdirAll(path(binding.dir()), 0700); err != nil {
				t.Fatal(err)
			}
			if kind == "foreign-helper" {
				if err := os.WriteFile(path(binding.dir()+"/helper"), []byte("foreign"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "foreign-binding" {
				if err := os.WriteFile(path(binding.dir()+"/binding.json"), []byte("foreign"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before := map[string]string{}
			_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
				if e == nil && !d.IsDir() && !strings.HasPrefix(path, filepath.Join(dir, "reader-session-")) {
					b, e := os.ReadFile(path)
					if e != nil {
						t.Fatal(e)
					}
					before[path] = sum(b)
				}
				return nil
			})
			model := modelOps(t, InstallTransaction{Base: m, Target: tx.Target}, dir)
			calls := 0
			o := nativeUpdateSessionOps{read: model.read, mkdir: func(_ context.Context, p string) error { return os.MkdirAll(path(p), 0700) }}
			o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
				if args[0] != "/usr/bin/setpriv" {
					return model.run(ctx, input, args...)
				}
				calls++
				want := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(m.App.UID), "--regid", strconv.Itoa(m.App.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", binding.dir() + "/helper", "reader-session-client", "--custody-owner", strconv.Itoa(m.Owner), "--binding", binding.dir() + "/binding.json"}
				if !reflect.DeepEqual(args, want) {
					t.Fatal("wrong source-session OS role", args)
				}
				if kind == "refused-auth" {
					return nil, errors.New("native denied")
				}
				if kind == "wrong-output" {
					return []byte("unexpected output"), nil
				}
				return []byte("Ready\n"), nil
			}
			err := stageNativeUpdateSession(t.Context(), m, body, o)
			good := kind == "ok" || kind == "repeat"
			if (err == nil) != good {
				t.Fatal(kind, err)
			}
			if kind == "repeat" {
				if err = stageNativeUpdateSession(t.Context(), m, body, o); err != nil {
					t.Fatal(err)
				}
			}
			if good && calls < 1 {
				t.Fatal("protected app helper not executed")
			}
			for path, h := range before {
				b, e := os.ReadFile(path)
				if e != nil || sum(b) != h {
					t.Fatal("session preflight changed existing state", path)
				}
			}

			if kind == "foreign-helper" || kind == "foreign-binding" {
				name := "helper"
				if kind == "foreign-binding" {
					name = "binding.json"
				}
				b, _ := os.ReadFile(path(binding.dir() + "/" + name))
				if string(b) != "foreign" {
					t.Fatal("overwrote foreign staged state")
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "update-active.json")); !os.IsNotExist(err) {
				t.Fatal("session preflight created update state")
			}
		})
	}
}

func TestRemoteSourceSessionBinding(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t, true)
	m := tx.Base
	b := nativeUpdateSessionBinding{1, m, sum([]byte("current source helper"))}
	for _, kind := range []string{"ok", "uid", "gid", "image", "generation", "native", "origin", "enrollment"} {
		t.Run(kind, func(t *testing.T) {
			current := m
			uid, gid := m.App.UID, m.App.GID
			image := b.dir() + "/helper"
			switch kind {
			case "uid":
				uid = m.Owner
			case "gid":
				gid++
			case "image":
				image = m.Root + "/fased-app"
			case "generation":
				current.CoreHash = strings.Repeat("f", 64)
			case "native":
				current.NativeOwnerAccess = true
			case "origin":
				current.PublicOrigin = ""
			case "enrollment":
				current.Enrollment = false
			}
			err := validateNativeUpdateSession(current, b, uid, uid, gid, image, b.dir()+"/binding.json")
			if (err == nil) != (kind == "ok") {
				t.Fatal(kind, err)
			}
		})
	}
}
