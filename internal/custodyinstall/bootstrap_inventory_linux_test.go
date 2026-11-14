//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestLocalBootstrapInventoryStagesOnlySignedCore(t *testing.T) {
	for _, scenario := range []string{"ok", "tampered-source", "foreign-staged", "wrong-contract", "refused-auth", "remote", "managed", "no-native"} {
		t.Run(scenario, func(t *testing.T) {
			tx, dir, _, _ := ordinaryBootstrapModel(t)
			// Remove only this unit fixture's prepared controller, to model the real
			// pre-review entry. Existing root/key/history records remain untouched.
			if err := os.RemoveAll(dir + "/bootstraps"); err != nil {
				t.Fatal(err)
			}
			artifacts := t.TempDir()
			if err := os.WriteFile(filepath.Join(artifacts, "fased-app"), []byte("next-core"), 0700); err != nil {
				t.Fatal(err)
			}
			if scenario == "tampered-source" {
				_ = os.WriteFile(filepath.Join(artifacts, "fased-app"), []byte("tampered"), 0700)
			}
			if scenario == "foreign-staged" {
				_ = os.MkdirAll(dir+"/bootstraps/"+tx.ID+"/candidate", 0700)
				_ = os.WriteFile(dir+"/bootstraps/"+tx.ID+"/candidate/fased-app", []byte("foreign"), 0755)
			}
			if scenario == "remote" {
				tx.Base.Location, tx.Target.Location = "remote", "remote"
			}
			if scenario == "managed" {
				tx.Base.Version, tx.Target.Version = 2, 2
			}
			if scenario == "no-native" {
				tx.Base.NativeOwnerAccess, tx.Target.NativeOwnerAccess = false, false
			}
			probe := strings.Repeat("d", 32)
			model := modelOps(t, InstallTransaction{Base: tx.Base, Target: tx.Target}, dir)
			calls := 0
			run := func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
				if args[0] != "/usr/bin/setpriv" {
					return model.run(ctx, b, args...)
				}
				calls++
				prefix := []string{"/usr/bin/setpriv", "--reuid", strconv.Itoa(tx.Base.App.UID), "--regid", strconv.Itoa(tx.Base.App.GID), "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", tx.Dir() + "/candidate/fased-app"}
				if len(args) < len(prefix) || !reflect.DeepEqual(args[:len(prefix)], prefix) {
					t.Fatal("wrong staged image/OS role", args)
				}
				if calls == 1 {
					if !reflect.DeepEqual(args[len(prefix):], []string{"updates", "ordinary-custody-compatibility"}) {
						t.Fatal("wrong contract probe", args)
					}
					contract := core.OrdinaryUpdateContract()
					if scenario == "wrong-contract" {
						contract.Protocol++
					}
					return mustJSON(map[string]any{"version": 1, "target": update.Target{OS: "linux", Arch: "amd64"}, "contract": contract, "walletSigner": false, "financialExecution": false, "updaterEnabled": false}), nil
				}
				want := []string{"update-inventory", "--data-dir", tx.Base.Root + "/app-state", "--probe-id", probe, "--bootstrap-owner", strconv.Itoa(tx.Base.Owner), "--bootstrap-id", tx.ID, "--bootstrap-core-sha256", tx.Target.CoreHash}
				if !reflect.DeepEqual(args[len(prefix):], want) {
					t.Fatal("wrong typed inventory command", args)
				}
				if scenario == "refused-auth" {
					return nil, errors.New("native authentication refused")
				}
				return []byte(`{"inventory":"only"}`), nil
			}
			var out bytes.Buffer
			err := stageBootstrapInventory(t.Context(), tx, artifacts, probe, &out, bootstrapInventoryOps{read: model.read, run: run, mkdir: func(_ context.Context, path string) error {
				return os.MkdirAll(dir+strings.TrimPrefix(path, tx.Base.Root), 0700)
			}})
			if (err == nil) != (scenario == "ok") {
				t.Fatal("stage result", err)
			}
			if scenario == "ok" && (calls != 2 || out.String() != `{"inventory":"only"}`) {
				t.Fatal("complete signed stage/role/auth dispatch not exercised")
			}
			if scenario != "ok" && out.Len() != 0 {
				t.Fatal("refused stage emitted inventory")
			}
			_ = filepath.WalkDir(dir+"/bootstraps", func(path string, d os.DirEntry, e error) error {
				if e != nil {
					return nil
				}
				if !d.IsDir() && path != dir+"/bootstraps/"+tx.ID+"/candidate/fased-app" {
					t.Fatal("pre-review stage left controller metadata", path)
				}
				return nil
			})
			if scenario == "foreign-staged" {
				b, _ := os.ReadFile(dir + "/bootstraps/" + tx.ID + "/candidate/fased-app")
				if string(b) != "foreign" {
					t.Fatal("foreign image overwritten")
				}
			}
			if _, err := os.Stat(dir + "/update-active.json"); !os.IsNotExist(err) {
				t.Fatal("pre-review active marker created")
			}
		})
	}
}

func TestLocalBootstrapInventoryOSBindings(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t)
	for _, bad := range []string{"", "owner-as-app", "root-euid", "wrong-gid", "remote", "no-native", "no-enrollment", "managed", "foreign-owner", "data", "image", "id", "hash"} {
		t.Run(bad, func(t *testing.T) {
			m := tx.Base
			uid, euid, gid := m.App.UID, m.App.UID, m.App.GID
			id, hash := tx.ID, tx.Target.CoreHash
			data, exe := m.Root+"/app-state", tx.Dir()+"/candidate/fased-app"
			switch bad {
			case "owner-as-app":
				uid = m.Owner
			case "root-euid":
				euid = 0
			case "wrong-gid":
				gid++
			case "remote":
				m.Location = "remote"
			case "no-native":
				m.NativeOwnerAccess = false
			case "no-enrollment":
				m.Enrollment = false
			case "managed":
				m.Version = 2
			case "foreign-owner":
				m.Owner++
			case "data":
				data += "/other"
			case "image":
				exe = m.Root + "/fased-app"
			case "id":
				id = strings.Repeat("d", 32)
			case "hash":
				hash = "invalid"
			}
			if err := validateBootstrapInventory(m, uid, euid, gid, exe, data, id, hash); (err == nil) != (bad == "") {
				t.Fatal("foreign Local bootstrap binding", bad, err)
			}
		})
	}
}

func TestLocalBootstrapPairDoesNotMakeSnapshotOptional(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t)
	tx.Core.Version = 0
	if _, err := tx.pairPlan(); err != nil {
		t.Fatal("signed pair rejected before snapshot", err)
	}
	if _, err := tx.Plan(); err == nil {
		t.Fatal("inert staged pair became a transaction without real inventory")
	}
	tx.Target.CoreHash = strings.Repeat("e", 64)
	if _, err := tx.pairPlan(); err == nil {
		t.Fatal("foreign staged hash admitted")
	}
}

// The installed Remote custody app and ordinary VPS app are separate identities.
// Source regression: the exact custody pair must not require the VPS app session.
func TestRemoteBootstrapInventoryUsesCustodyIdentityWithoutSession(t *testing.T) {
	tx, dir, _, _ := ordinaryBootstrapModel(t)
	tx.Base.Location, tx.Target.Location = "remote", "remote"
	tx.Base.NativeOwnerAccess, tx.Target.NativeOwnerAccess = false, false
	tx.Base.PublicOrigin, tx.Target.PublicOrigin = "https://fased-fixture-2.tail7bbde2.ts.net", "https://fased-fixture-2.tail7bbde2.ts.net"
	tx.Base.App.UID, tx.Target.App.UID = 996, 996
	tx.Base.App.GID, tx.Target.App.GID = 989, 989
	tx.Base.Signer.UID, tx.Target.Signer.UID = 995, 995
	tx.Base.Signer.GID, tx.Target.Signer.GID = 988, 988
	if err := os.RemoveAll(dir + "/bootstraps"); err != nil {
		t.Fatal(err)
	}
	artifacts := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifacts, "fased-app"), []byte("next-core"), 0700); err != nil {
		t.Fatal(err)
	}
	model := modelOps(t, InstallTransaction{Base: tx.Base, Target: tx.Target}, dir)
	probe := strings.Repeat("d", 32)
	calls := 0
	run := func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
		if args[0] != "/usr/bin/setpriv" {
			return model.run(ctx, b, args...)
		}
		calls++
		prefix := []string{"/usr/bin/setpriv", "--reuid", "996", "--regid", "989", "--clear-groups", "--no-new-privs", "--inh-caps=-all", "--ambient-caps=-all", "--bounding-set=-all", "--", tx.Dir() + "/candidate/fased-app"}
		if len(args) < len(prefix) || !reflect.DeepEqual(args[:len(prefix)], prefix) {
			t.Fatal("borrowed ordinary VPS identity/root", args)
		}
		if calls == 1 {
			return mustJSON(map[string]any{"version": 1, "target": update.Target{OS: "linux", Arch: "amd64"}, "contract": core.OrdinaryUpdateContract(), "walletSigner": false, "financialExecution": false, "updaterEnabled": false}), nil
		}
		want := []string{"update-inventory", "--data-dir", tx.Base.Root + "/app-state", "--probe-id", probe, "--bootstrap-owner", strconv.Itoa(tx.Base.Owner), "--bootstrap-id", tx.ID, "--bootstrap-core-sha256", tx.Target.CoreHash}
		if !reflect.DeepEqual(args[len(prefix):], want) {
			t.Fatal("not the typed custody inventory", args)
		}
		return []byte(`{"inventory":"custody-without-saved-session"}`), nil
	}
	var out bytes.Buffer
	if err := stageBootstrapInventory(t.Context(), tx, artifacts, probe, &out, bootstrapInventoryOps{read: model.read, run: run, mkdir: func(_ context.Context, path string) error {
		return os.MkdirAll(dir+strings.TrimPrefix(path, tx.Base.Root), 0700)
	}}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || out.String() != `{"inventory":"custody-without-saved-session"}` {
		t.Fatal("incomplete custody inventory path")
	}
	if _, err := os.Stat(dir + "/update-active.json"); !os.IsNotExist(err) {
		t.Fatal("pre-review controller mutation")
	}
}

func TestRemoteBootstrapInventoryOSBindings(t *testing.T) {
	tx, _, _, _ := ordinaryBootstrapModel(t)
	tx.Base.Location = "remote"
	tx.Base.NativeOwnerAccess = false
	tx.Base.PublicOrigin = "https://fased-fixture-2.tail7bbde2.ts.net"
	tx.Base.App.UID = 996
	tx.Base.App.GID = 989
	tx.Base.Signer.UID = 995
	tx.Base.Signer.GID = 988
	for _, bad := range []string{"", "vps-app-uid", "root-euid", "wrong-gid", "native-local", "missing-origin", "no-enrollment", "managed", "foreign-owner", "foreign-root", "data", "image", "id", "hash"} {
		t.Run(bad, func(t *testing.T) {
			m := tx.Base
			uid, euid, gid := m.App.UID, m.App.UID, m.App.GID
			id, hash := tx.ID, tx.Target.CoreHash
			data, exe := m.Root+"/app-state", tx.Dir()+"/candidate/fased-app"
			switch bad {
			case "vps-app-uid":
				uid, euid = 999, 999
			case "root-euid":
				euid = 0
			case "wrong-gid":
				gid++
			case "native-local":
				m.NativeOwnerAccess = true
			case "missing-origin":
				m.PublicOrigin = ""
			case "no-enrollment":
				m.Enrollment = false
			case "managed":
				m.Version = 2
			case "foreign-owner":
				m.Owner++
			case "foreign-root":
				m.Root = "/var/lib/fased-app-vps-1000"
			case "data":
				data = "/var/lib/fased-app-vps-1000/app-state"
			case "image":
				exe = m.Root + "/fased-app"
			case "id":
				id = strings.Repeat("d", 32)
			case "hash":
				hash = "invalid"
			}
			if err := validateBootstrapInventory(m, uid, euid, gid, exe, data, id, hash); (err == nil) != (bad == "") {
				t.Fatal("foreign Remote bootstrap role", bad, err)
			}
		})
	}
}

func TestBootstrapInventoryBindsApprovedReader(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(strconv.FormatBool(approved), func(t *testing.T) {
			tx, dir, _, _ := ordinaryBootstrapModel(t)
			// Privileged staging binds its path to the invoking owner. The
			// shared model uses UID 1000, which is not every CI runner's UID.
			for _, m := range []*Manifest{&tx.Base, &tx.Target} {
				m.Owner = os.Getuid()
				m.Root = Root(m.Owner)
				m.App = Identity{Name: accountName(m.Owner, "app"), UID: m.Owner + 10, GID: m.Owner + 10}
				m.Signer = Identity{Name: accountName(m.Owner, "signer"), UID: m.Owner + 11, GID: m.Owner + 11}
			}
			if err := os.RemoveAll(dir + "/bootstraps"); err != nil {
				t.Fatal(err)
			}
			artifacts := t.TempDir()
			image := []byte("next-core")
			if err := os.WriteFile(filepath.Join(artifacts, "fased-app"), image, 0700); err != nil {
				t.Fatal(err)
			}
			model := modelOps(t, InstallTransaction{Base: tx.Base, Target: tx.Target}, dir)
			source := ApprovedExecutablePath(os.Getuid(), image)
			bound := false
			probes := 0
			verify := func(path, digest string, size int64) error {
				if !approved || path != source || digest != filepath.Base(source) || size != int64(len(image)) {
					return errors.New("approved reader unavailable")
				}
				return nil
			}
			run := func(ctx context.Context, b []byte, args ...string) ([]byte, error) {
				if args[0] == "/usr/bin/install" && args[len(args)-2] == source {
					bound = len(b) == 0
					b = bytes.Clone(image)
				}
				if args[0] != "/usr/bin/setpriv" {
					return model.run(ctx, b, args...)
				}
				probes++
				if probes == 1 {
					return mustJSON(map[string]any{"version": 1, "target": update.Target{OS: "linux", Arch: "amd64"}, "contract": core.OrdinaryUpdateContract(), "walletSigner": false, "financialExecution": false, "updaterEnabled": false}), nil
				}
				return []byte(`{"inventory":"only"}`), nil
			}
			var out bytes.Buffer
			err := stageBootstrapInventory(t.Context(), tx, artifacts, strings.Repeat("d", 32), &out, bootstrapInventoryOps{read: model.read, run: run, verify: verify, mkdir: func(_ context.Context, path string) error {
				return os.MkdirAll(dir+strings.TrimPrefix(path, tx.Base.Root), 0700)
			}})
			if approved {
				if err != nil || !bound || probes != 2 {
					t.Fatal("approved reader was not used through inventory", err)
				}
			} else if err == nil || bound || probes != 0 {
				t.Fatal("unapproved reader reached privileged staging or probe", err)
			}
		})
	}
}
