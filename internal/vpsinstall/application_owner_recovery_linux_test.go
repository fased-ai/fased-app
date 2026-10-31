//go:build linux

package vpsinstall

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
)

func retainedOwnerFixture(t *testing.T) (*remoteUpdateFixture, applicationJournal, string) {
	t.Helper()
	f := remoteApplicationFixture(t)
	f.denyFence = true
	if _, e := f.apply(t, false); e == nil {
		t.Fatal("fixture fence did not stop before dispatch")
	}
	j, e := readApplicationJournal(f.o)
	if e != nil || j.Status.Phase != "prepared" {
		t.Fatal(j, e)
	}
	return f, j, f.old.Root + "-update-" + f.c.ID + "/previous-core"
}

func appOwnerOps(f *remoteUpdateFixture, image string) hostOps {
	o := f.o
	o.uid, o.euid, o.gid = f.old.AppUID, f.old.AppUID, f.old.AppGID
	o.executable = func() (string, error) { return image, nil }
	return o
}

func TestApplicationOwnerRetainedHelperAcrossGenerationSwap(t *testing.T) {
	for _, kind := range []string{"original", "new-code-old-config", "old-code-new-config", "target", "rolling-back"} {
		t.Run(kind, func(t *testing.T) {
			f, j, image := retainedOwnerFixture(t)
			dir := strings.TrimSuffix(image, "/previous-core")
			switch kind {
			case "new-code-old-config", "target":
				f.h.files[f.old.Core] = f.h.files[dir+"/next-core"]
			case "rolling-back":
				j.Status.Phase = "recovery"
				f.h.journals[applicationJournalName] = encoded(j)
			}
			if kind == "old-code-new-config" || kind == "target" || kind == "rolling-back" {
				f.h.files[f.old.Config] = f.h.files[dir+"/next-install.json"]
			}
			if kind == "target" {
				f.h.files[f.old.Root+"/intent.json"] = f.h.files[dir+"/next-intent.json"]
			}
			got, e := applicationOwnerImage(f.o, f.old, f.c.ID)
			if e != nil || got != image {
				t.Fatal("owner did not select retained original", got, e)
			}
			m, e := validateApplicationOwnerHelper(f.old.Config, image, f.old.AppUID, appOwnerOps(f, image))
			if e != nil || m.Root != f.old.Root || m.Data != f.old.Data || m.AppUID != f.old.AppUID {
				t.Fatal("retained app helper refused admitted partial generation", m, e)
			}
		})
	}
}

func TestApplicationOwnerRetainedRefusesForeignState(t *testing.T) {
	for _, kind := range []string{"private-journal", "public-journal", "missing-public", "foreign-id", "code", "root-role", "data", "extra-secret", "symlink", "foreign-owner", "app-identity", "actual-image", "groups", "terminal"} {
		t.Run(kind, func(t *testing.T) {
			f, j, image := retainedOwnerFixture(t)
			dir := strings.TrimSuffix(image, "/previous-core")
			o := appOwnerOps(f, image)
			switch kind {
			case "private-journal":
				f.h.journals[applicationJournalName] = []byte(`{"foreign":true}`)
			case "public-journal":
				v := f.h.files[dir+"/owner-helper.json"]
				v.Data = []byte(`{"foreign":true}`)
				f.h.files[dir+"/owner-helper.json"] = v
			case "missing-public":
				delete(f.h.files, dir+"/owner-helper.json")
			case "foreign-id":
				image = f.old.Root + "-update-" + strings.Repeat("b", 32) + "/previous-core"
				o.executable = func() (string, error) { return image, nil }
			case "code":
				v := f.h.files[image]
				v.Data = []byte("foreign original")
				f.h.files[image] = v
			case "root-role":
				o.uid, o.euid = 0, 0
			case "data":
				v := f.h.files[f.old.Data]
				v.Mode = os.ModeDir | 0755
				f.h.files[f.old.Data] = v
			case "extra-secret":
				f.h.files[dir+"/credentials"] = hostFile{UID: 0, GID: 0, Regular: true, Mode: 0600, Data: []byte("inert fixture only")}
			case "symlink":
				v := f.h.files[image]
				v.Regular, v.Mode, v.Link = false, os.ModeSymlink|0777, "/foreign"
				f.h.files[image] = v
			case "foreign-owner":
				v := f.h.files[dir]
				v.UID = f.old.OwnerUID
				f.h.files[dir] = v
			case "app-identity":
				o.uid, o.euid = f.old.OwnerUID, f.old.OwnerUID
			case "actual-image":
				o.executable = func() (string, error) { return "/foreign", nil }
			case "groups":
				o.groups = func() ([]int, error) { return []int{f.old.AppGID, 27}, nil }
			case "terminal":
				j.Status.Phase = "completed"
				f.h.journals[applicationJournalName] = encoded(j)
			}
			if kind != "root-role" && kind != "app-identity" && kind != "actual-image" && kind != "groups" && kind != "missing-public" && kind != "terminal" && kind != "private-journal" && kind != "foreign-id" {
				if _, e := applicationOwnerImage(f.o, f.old, f.c.ID); e == nil {
					t.Fatal("owner selected foreign retained helper")
				}
			}
			if kind == "private-journal" {
				if _, e := applicationOwnerImage(f.o, f.old, f.c.ID); e == nil {
					t.Fatal("unknown private journal accepted")
				}
				return // AppUID deliberately cannot read the private owner journal.
			}
			if kind == "terminal" {
				got, e := applicationOwnerImage(f.o, f.old, f.c.ID)
				if e != nil || got != f.old.Core {
					t.Fatal("terminal transaction retained executable authority", got, e)
				}
				return // Public record grants no OS or fresh-operation authority.
			}
			if _, e := validateApplicationOwnerHelper(f.old.Config, image, f.old.AppUID, o); e == nil {
				t.Fatal("invalid retained runtime accepted")
			}
		})
	}
}

func TestApplicationOwnerRetainedBridgeFixedOriginalArguments(t *testing.T) {
	f, _, image := retainedOwnerFixture(t)
	for _, p := range []string{"/usr", "/usr/bin"} {
		f.h.files[p] = hostFile{UID: 0, GID: 0, Mode: os.ModeDir | 0755, Directory: true}
	}
	for _, p := range []string{"/usr/bin/sudo", "/usr/bin/setpriv", "/usr/bin/env"} {
		f.h.files[p] = hostFile{UID: 0, GID: 0, Mode: 0755, Regular: true}
	}
	command := core.ApplicationOwnerCommand{Action: "offline", ID: f.c.ID}
	_, e := applicationOwnerCall(t.Context(), f.old, command, f.o, func(_ context.Context, _ []byte, args []string) ([]byte, error) {
		if !contains(args, image) || contains(args, f.old.Core) {
			t.Fatal("current mutable executable selected", args)
		}
		return encoded(core.ApplicationOwnerView{View: core.ApplicationUpdateView{Version: 1, InstanceID: "fixture", Operation: core.ApplicationUpdateOperation{ID: command.ID}}}), nil
	})
	if e != nil {
		t.Fatal(e)
	}
	command.ID = strings.Repeat("b", 32)
	if _, e = applicationOwnerCall(t.Context(), f.old, command, f.o, func(context.Context, []byte, []string) ([]byte, error) {
		t.Fatal("foreign operation reached helper")
		return nil, nil
	}); e == nil {
		t.Fatal("foreign active operation accepted")
	}
}

func TestApplicationMaintenanceLoadExactMixedGeneration(t *testing.T) {
	for _, kind := range []string{"new-code", "new-config", "new-intent", "new-code-intent"} {
		t.Run(kind, func(t *testing.T) {
			f, j, image := retainedOwnerFixture(t)
			dir := strings.TrimSuffix(image, "/previous-core")
			if kind == "new-code" || kind == "new-code-intent" {
				f.h.files[f.old.Core] = f.h.files[dir+"/next-core"]
			}
			if kind == "new-config" {
				f.h.files[f.old.Config] = f.h.files[dir+"/next-install.json"]
			}
			if kind == "new-intent" || kind == "new-code-intent" {
				f.h.files[f.old.Root+"/intent.json"] = f.h.files[dir+"/next-intent.json"]
			}
			before := len(f.h.calls)
			if _, e := load(f.o, f.old.OwnerUID); e == nil {
				t.Fatal("ordinary Load admitted a partial generation")
			}
			got, e := loadApplicationMaintenance(f.o, f.old.OwnerUID, f.c.ID)
			want := j.Previous
			if kind == "new-config" {
				want = j.Target
			}
			if e != nil || got != want || len(f.h.calls) != before {
				t.Fatal("same-journal maintenance load failed or invoked host authority", got, e)
			}
		})
	}
}

func TestApplicationMaintenanceLoadRefusesForeignOrMissingTransaction(t *testing.T) {
	for _, kind := range []string{"missing", "different-id", "malformed-id", "foreign-owner", "root", "terminal", "public-record", "foreign-config", "foreign-code"} {
		t.Run(kind, func(t *testing.T) {
			f, j, image := retainedOwnerFixture(t)
			id, owner := f.c.ID, f.old.OwnerUID
			switch kind {
			case "missing":
				delete(f.h.journals, applicationJournalName)
			case "different-id":
				id = strings.Repeat("b", 32)
			case "malformed-id":
				id = "../foreign"
			case "foreign-owner":
				owner++
			case "root":
				f.o.uid, f.o.euid = 0, 0
			case "terminal":
				j.Status.Phase = "completed"
				f.h.journals[applicationJournalName] = encoded(j)
			case "public-record":
				delete(f.h.files, strings.TrimSuffix(image, "/previous-core")+"/owner-helper.json")
			case "foreign-config":
				foreign := f.old
				foreign.Listen = "127.0.0.1:9944"
				v := f.h.files[f.old.Config]
				v.Data = encoded(foreign)
				f.h.files[f.old.Config] = v
			case "foreign-code":
				v := f.h.files[f.old.Core]
				v.Data = []byte("foreign")
				f.h.files[f.old.Core] = v
			}
			before := len(f.h.calls)
			if _, e := loadApplicationMaintenance(f.o, owner, id); e == nil || len(f.h.calls) != before {
				t.Fatal("invalid maintenance load accepted or invoked host authority", e)
			}
		})
	}
}
