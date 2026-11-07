//go:build linux

package custodyinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/signer"
)

func recoveryModel(t *testing.T) (enrollmentCoreRecovery, string, installOps, func() error) {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{Version: 1, Mode: "disposable-fixture:v1", ID: strings.Repeat("a", 32), Owner: 1000, Root: Root(1000), Listen: "127.0.0.1:17331", CoreHash: sum([]byte("old-core")), SignerHash: sum([]byte("old-signer")), App: Identity{Name: accountName(1000, "app"), UID: 1001, GID: 1001}, Signer: Identity{Name: accountName(1000, "signer"), UID: 1002, GID: 1002}}
	m.Version = 3
	m.Mode = signer.OrdinaryCustodyMode
	m.Profile = signer.OwnerCustodyProfile
	m.Provenance = signer.ControlledCustodyProfile
	m.Network = "devnet"
	m.Enrollment = true
	m.Location = "remote"
	m.PublicOrigin = "https://owner.example.ts.net"
	r := enrollmentCoreRecovery{1, strings.Repeat("d", 32), m, sum([]byte("corrected-core"))}
	if !r.valid() {
		t.Fatal("invalid recovery fixture")
	}
	if e := os.WriteFile(dir+"/install.json", mustJSON(m), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(dir+strings.TrimPrefix(r.dir(), m.Root), 0755); e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string]string{"fased-app": "old-core", "fased-signer": "old-signer", "financial-history": "retained-unrelated-sentinel"} {
		mode := os.FileMode(0755)
		if name == "financial-history" {
			mode = 0600
		}
		if e := os.WriteFile(dir+"/"+name, []byte(data), mode); e != nil {
			t.Fatal(e)
		}
	}
	x := InstallTransaction{Base: m, Target: r.next()}
	o := modelOps(t, x, dir)
	if e := publish(t.Context(), r.dir()+"/previous-owner-intent.json", r.ID, mustJSON(recoveryOwnerIntent(m)), 0644, o.read, o.run); e != nil {
		t.Fatal(e)
	}
	owner := func() error { return os.WriteFile(dir+"/owner-generation", []byte(r.NextCoreHash), 0600) }
	return r, dir, o, owner
}

func TestEnrollmentCoreRecoveryEveryOSInterruption(t *testing.T) {
	r, _, o, owner := recoveryModel(t)
	count := 0
	execute := o.run
	o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		count++
		return execute(ctx, b, args...)
	}
	if _, e := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, owner); e != nil {
		t.Fatal(e)
	}
	for at := 1; at <= count; at++ {
		for _, after := range []bool{false, true} {
			t.Run(strings.Join([]string{fmt.Sprint(at), map[bool]string{false: "before", true: "after"}[after]}, "-"), func(t *testing.T) {
				r, dir, o, owner := recoveryModel(t)
				initial, _ := os.ReadFile(dir + "/financial-history")
				calls := 0
				failed := false
				execute := o.run
				o.run = func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
					calls++
					if calls == at && !after {
						failed = true
						return nil, errors.New("disposable OS interruption")
					}
					v, e := execute(ctx, b, args...)
					if calls == at && after && e == nil {
						failed = true
						return nil, errors.New("lost OS acknowledgement")
					}
					return v, e
				}
				if _, e := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, owner); e == nil || !failed {
					t.Fatal("interruption missed", e)
				}
				o.run = execute
				for i := 0; i < 2; i++ {
					m, e := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, owner)
					if e != nil || m != r.next() {
						t.Fatal("cold same-intent retry failed", at, after, e)
					}
				}
				b, e := os.ReadFile(dir + "/financial-history")
				if e != nil || string(b) != string(initial) {
					t.Fatal("unrelated financial state changed", e)
				}
				for name, want := range map[string][]byte{"previous-core": []byte("old-core"), "previous-manifest": mustJSON(r.Base), "previous-owner-intent.json": mustJSON(recoveryOwnerIntent(r.Base))} {
					f, e := o.read(r.dir() + "/" + name)
					if e != nil || string(f.Data) != string(want) {
						t.Fatal("original recovery evidence changed", name, e)
					}
				}
			})
		}
	}
}

func TestEnrollmentCoreRecoveryRefusesForeignAndHeldState(t *testing.T) {
	for _, mode := range []string{"core", "signer", "inventory", "update-active.json", "enrollment-ready.json", "ready.json", "receipt", "phase", "post-import-profile", "local", "changed-signer"} {
		t.Run(mode, func(t *testing.T) {
			r, dir, o, owner := recoveryModel(t)
			if mode == "receipt" || mode == "phase" {
				if _, e := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, owner); e != nil {
					t.Fatal(e)
				}
			}
			path := r.Base.Root + "/" + map[string]string{"core": "fased-app", "signer": "fased-signer", "inventory": "install.json"}[mode]
			switch mode {
			case "core", "signer", "inventory":
			case "receipt":
				path = r.dir() + "/recovery.json"
			case "phase":
				path = r.dir() + "/inventory-replaced.json"
			case "post-import-profile":
				r.Base.Enrollment = false
			case "local":
				r.Base.Location = "local"
				r.Base.PublicOrigin = ""
			case "changed-signer":
				r.Base.SignerHash = strings.Repeat("f", 64)
			default:
				path = r.Base.Root + "/" + mode
			}
			if mode != "post-import-profile" && mode != "local" && mode != "changed-signer" {
				p := dir + strings.TrimPrefix(path, r.Base.Root)
				if e := os.WriteFile(p, []byte("foreign-preserved"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			before := map[string][]byte{}
			filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
				if e == nil && !d.IsDir() {
					before[p], _ = os.ReadFile(p)
				}
				return e
			})
			if _, e := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, owner); e == nil {
				t.Fatal("unsafe target adopted")
			}
			for p, b := range before {
				got, e := os.ReadFile(p)
				if e != nil || string(got) != string(b) {
					t.Fatal("refusal changed existing state", p, e)
				}
			}
		})
	}
}

func TestEnrollmentCoreRecoveryOwnerIntentInterruption(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint("after-", after), func(t *testing.T) {
			r, dir, o, write := recoveryModel(t)
			owner := func() error {
				if after {
					if err := write(); err != nil {
						return err
					}
				}
				return errors.New("interrupted owner-intent write")
			}
			if _, err := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, owner); err == nil {
				t.Fatal("owner-intent failure ignored")
			}
			for i := 0; i < 2; i++ {
				if m, err := recoverEnrollmentCore(t.Context(), r, []byte("corrected-core"), o, write); err != nil || m != r.next() {
					t.Fatal("lost owner-intent acknowledgement could not reconcile", err)
				}
			}
			if b, err := os.ReadFile(dir + "/owner-generation"); err != nil || string(b) != r.NextCoreHash {
				t.Fatal("owner generation not completed", err)
			}
		})
	}
}

func TestEnrollmentRecoveryKernelGuards(t *testing.T) {
	m := Manifest{Owner: os.Getuid(), Root: Root(os.Getuid())}
	absent := []byte("LoadState=not-found\nActiveState=inactive\nMainPID=0\nUnitFileState=\nFragmentPath=\n")
	read := func(string) (hostFile, error) { return hostFile{}, os.ErrNotExist }
	for _, mode := range []string{"absent", "active", "pid", "enabled", "loaded", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			b := string(absent)
			switch mode {
			case "active":
				b = strings.ReplaceAll(b, "inactive", "active")
			case "pid":
				b = strings.ReplaceAll(b, "MainPID=0", "MainPID=7")
			case "enabled":
				b = strings.ReplaceAll(b, "UnitFileState=\n", "UnitFileState=enabled\n")
			case "loaded":
				b = strings.ReplaceAll(b, "not-found", "loaded")
			}
			run := func(context.Context, []byte, ...string) ([]byte, error) {
				if mode == "read-error" {
					return nil, errors.New("denied")
				}
				return []byte(b), nil
			}
			e := checkEnrollmentRecoveryRoles(t.Context(), m, read, run)
			if (e == nil) != (mode == "absent") {
				t.Fatal("incorrect unit admission", e)
			}
		})
	}
	for _, dirty := range []string{"", "app-state", "signer-state", "signer-ipc"} {
		t.Run("empty-"+dirty, func(t *testing.T) {
			run := func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
				if len(args) != 9 || args[0] != "/usr/bin/find" {
					t.Fatal("unbounded probe", args)
				}
				if dirty != "" && args[1] == m.Root+"/"+dirty {
					return []byte("x"), nil
				}
				return nil, nil
			}
			e := checkEnrollmentRecoveryEmpty(t.Context(), m, run)
			if (e == nil) != (dirty == "") {
				t.Fatal("nonempty target admitted", e)
			}
		})
	}
}
