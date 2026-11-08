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
	for _, kind := range []string{"ok", "repeat", "foreign-helper", "foreign-binding", "refused-auth", "wrong-output"} {
		t.Run(kind, func(t *testing.T) {
			tx, dir, _, _ := ordinaryBootstrapModel(t)
			m := tx.Base
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
