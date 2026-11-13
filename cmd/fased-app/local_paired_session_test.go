package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/update"
)

// Real existing session storage and HTTP inventory; the OS-approved native peer
// is simulated. The unchanged predecessor loader is the final consumer.
func TestLocalPairedPredecessorWithoutSession(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("protected custody requires Linux")
	}
	for _, kind := range []string{"absent", "absent-installed", "expired", "valid", "foreign-file", "malformed-file", "broad-file", "wrong-instance", "wrong-owner", "remote", "unclaimed", "native-refused"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "app-state")
			store, err := core.OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { store.Close() }()
			original := ""
			if kind != "unclaimed" {
				code, e := store.IssueClaimCode(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				original, err = store.Claim(t.Context(), code.Code, "preserved Local owner identity")
				if err != nil {
					t.Fatal(err)
				}
			}
			instance := store.Health().InstanceID
			m := custodyinstall.Manifest{Root: root, Location: "local", Owner: 1400, NativeOwnerAccess: true}
			file := filepath.Join(dir, "cli-session.json")
			seed := []byte(nil)
			if kind == "expired" || kind == "valid" || kind == "foreign-file" || kind == "broad-file" {
				token := strings.Repeat("A", 43)
				if kind == "valid" {
					token = original
				}
				id := instance
				if kind == "foreign-file" {
					id = "foreign"
				}
				seed, _ = json.Marshal(core.SavedSession{InstanceID: id, Token: token})
			} else if kind == "malformed-file" {
				seed = []byte("must preserve damaged session")
			}
			if seed != nil {
				if err := os.WriteFile(file, seed, 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "broad-file" {
					if err := os.Chmod(file, 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "core.sqlite")+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			identity := func() string {
				var salt, pw []byte
				var schema int
				if err := db.QueryRow("SELECT password_salt,password_hash FROM instance WHERE id=1").Scan(&salt, &pw); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
					t.Fatal(err)
				}
				b, _ := json.Marshal([]any{salt, pw, schema})
				return string(b)
			}
			before := identity()
			nativeCalls := 0
			transport := bootstrapInventoryTransport(func(req *http.Request) (*http.Response, error) {
				rec := httptest.NewRecorder()
				if req.URL.Path == "/api/v1/health" {
					h := store.Health()
					h.Connection = &core.ConnectionInfo{Location: "local", OwnerUID: 1400}
					if kind == "wrong-instance" {
						h.InstanceID = "foreign"
					}
					if kind == "wrong-owner" {
						h.Connection.OwnerUID++
					}
					if kind == "remote" {
						h.Connection.Location = "remote"
					}
					_ = json.NewEncoder(rec).Encode(h)
					return rec.Result(), nil
				}
				if req.URL.Path == "/api/v1/auth/native-local" {
					nativeCalls++
					if kind == "native-refused" {
						rec.WriteHeader(403)
						_, _ = io.WriteString(rec, `{"error":"refused"}`)
						return rec.Result(), nil
					}
					if req.Method != "POST" {
						t.Fatal("wrong native method")
					}
					var command map[string]string
					if json.NewDecoder(req.Body).Decode(&command) != nil || command["action"] != "session" {
						t.Fatal("wrong native request")
					}
					token, e := core.IssueMaintenanceSession(req.Context(), dir, instance)
					if e != nil {
						t.Fatal(e)
					}
					_ = json.NewEncoder(rec).Encode(map[string]string{"sessionToken": token, "csrfToken": "native-response-csrf"})
					return rec.Result(), nil
				}
				handler, e := core.NewHTTPHandler(store, "http://127.0.0.1:7332", "", nil)
				if e != nil {
					t.Fatal(e)
				}
				handler.ServeHTTP(rec, req)
				return rec.Result(), nil
			})
			fresh := func() *core.Client {
				return &core.Client{Endpoint: core.Endpoint{URL: "http://127.0.0.1:7332", InstanceID: instance}, HTTP: &http.Client{Transport: transport}}
			}
			if kind == "absent" {
				if e := fresh().LoadSession(t.Context(), dir); !errors.Is(e, core.ErrUnauthorized) {
					t.Fatal("predecessor no-session failure not reproduced", e)
				}
			}
			client := fresh()
			if kind == "absent-installed" {
				err = loadUpdateSessionWithLocal(t.Context(), client, dir, func(owner int, image, data string) (custodyinstall.Manifest, string, error) {
					self, e := os.Executable()
					if e != nil || owner != m.Owner || image != self || data != dir {
						t.Fatal("installed role validation bypassed")
					}
					return m, instance, nil
				})
			} else {
				err = prepareLocalReaderSession(t.Context(), client, m, instance)
			}
			good := kind == "absent" || kind == "absent-installed" || kind == "expired" || kind == "valid"
			if (err == nil) != good {
				t.Fatalf("result %v", err)
			}
			if !good {
				if kind != "native-refused" && nativeCalls != 0 {
					t.Fatal("foreign/unclaimed state acquired session")
				}
				got, e := os.ReadFile(file)
				if seed == nil {
					if !os.IsNotExist(e) {
						t.Fatal("created session on refusal")
					}
				} else if e != nil || !bytes.Equal(got, seed) {
					t.Fatal("foreign/damaged session replaced")
				}
			} else {
				if nativeCalls != map[bool]int{true: 0, false: 1}[kind == "valid"] {
					t.Fatal("unnecessary native session refresh")
				}
				info, e := os.Stat(file)
				if e != nil || info.Mode().Perm() != 0600 {
					t.Fatal("app session not private")
				}
				// Fresh client uses the ORIGINAL ordinary inventory adapter and saved-session
				// loader. This does not invoke bootstrap or replace either installed reader.
				cli := fresh()
				if e := loadUpdateSession(t.Context(), cli, dir); e != nil {
					t.Fatal(e)
				}
				probe := strings.Repeat("a", 32)
				var snapshot update.CoreSnapshot
				if e := cli.Do(t.Context(), "GET", "/api/v1/updates/inventory?probe="+probe, nil, &snapshot); e != nil {
					t.Fatal(e)
				}
				if snapshot.InstanceID != instance || snapshot.ProbeID != probe {
					t.Fatal("wrong ordinary snapshot")
				}
				for _, consumer := range []string{"pause", "backup", "recovery", "status", "preferences"} {
					t.Run(consumer, func(t *testing.T) {
						c := fresh()
						if e := c.LoadSession(t.Context(), dir); e != nil || c.CSRF == "" {
							t.Fatal("coupled predecessor cannot load app session", e)
						}
					})
				}
				// Reopen the actual store, without reissuing a claim or another session.
				if e := store.Close(); e != nil {
					t.Fatal(e)
				}
				store, err = core.OpenStore(dir)
				if err != nil {
					t.Fatal(err)
				}
				if e := loadUpdateSession(t.Context(), fresh(), dir); e != nil {
					t.Fatal("session not usable after core restart", e)
				}
			}
			if before != identity() || (original != "" && !store.Session(t.Context(), original)) {
				t.Fatal("owner/schema/original auth changed")
			}
		})
	}
}

func TestLocalPairedUpdateConsentAndDispatch(t *testing.T) {
	if _, e := custodyUpdateChildArgs([]string{"preferences", "--disposable-fixture", "--prepare-host"}, 1400, io.Discard); e != nil {
		t.Fatal("read-only preferences regression", e)
	}
	consent := []string{"--disposable-fixture", "--prepare-host"}
	for _, action := range []string{"inventory", "prepare", "stage", "preflight", "backup", "abort", "status", "preferences"} {
		args := append([]string{action}, consent...)
		if action == "preferences" {
			args = append(args, "--request-stdin")
		}
		command, err := custodyUpdateChildArgs(args, 1400, io.Discard)
		if err != nil {
			t.Fatal(action, err)
		}
		if action == "status" || action == "preferences" {
			if command[0] != "custody" {
				t.Fatal(command)
			}
		} else if command[0] != "owner-updates" {
			t.Fatal(command)
		}
	}
	for _, args := range [][]string{{"inventory"}, {"inventory", "--disposable-fixture", "--prepare-host=false"}, {"inventory", "--disposable-fixture=false", "--prepare-host"}, {"inventory", "--disposable-fixture", "--prepare-host", "--config", "/foreign"}, {"inventory", "--disposable-fixture", "--prepare-host", "--action", "backup"}, {"unknown", "--disposable-fixture", "--prepare-host"}} {
		if _, err := custodyUpdateChildArgs(args, 1400, io.Discard); err == nil {
			t.Fatal("invalid action/consent admitted", args)
		}
	}
}

func TestCustodyUpdateRunnerPreservesPredecessorAndUsesPinnedOwnerEntry(t *testing.T) {
	m := custodyinstall.Manifest{Owner: 1400, Root: "/var/lib/fased-app-custody-1400"}
	installed := filepath.Join(m.Root, "fased-app")
	pinned := filepath.Join(m.Root, "maintenance", "runner")
	choose := func(command string, selected string, selectionErr error) (string, string, error) {
		return custodyUpdateRunner(m, []string{command}, func(owner int) (string, error) {
			if owner != m.Owner {
				t.Fatal("foreign owner selected")
			}
			return selected, selectionErr
		})
	}
	if runner, session, err := choose("owner-updates", "", os.ErrNotExist); err != nil || runner != installed || session != "" {
		t.Fatal("legacy predecessor changed", runner, session, err)
	}
	if runner, session, err := choose("owner-updates", pinned, nil); err != nil || runner != pinned || session != pinned {
		t.Fatal("pinned owner entry not selected", runner, session, err)
	}
	if runner, session, err := choose("custody", pinned, nil); err != nil || runner != installed || session != pinned {
		t.Fatal("installed status entry changed", runner, session, err)
	}
	if _, _, err := choose("owner-updates", "/tmp/foreign", nil); err == nil {
		t.Fatal("foreign runner accepted")
	}
	if _, _, err := choose("owner-updates", "", errors.New("damaged pin")); err == nil {
		t.Fatal("damaged pin fell back to predecessor")
	}
}

func TestPinnedInitialInstallRefreshesOnlyTheMatchingCustodySession(t *testing.T) {
	m := custodyinstall.Manifest{Version: 3, Owner: 1400, Root: "/var/lib/fased-app-custody-1400", Enrollment: true, Location: "local", NativeOwnerAccess: true}
	pinned := filepath.Join(m.Root, "maintenance", "runner")
	if !needsPinnedInitialSession(m, pinned) || needsPinnedInitialSession(m, filepath.Join(m.Root, "fased-app")) {
		t.Fatal("initial Local handoff session selection differs")
	}
	m.Location, m.NativeOwnerAccess, m.PublicOrigin = "remote", false, "https://fixture.example.ts.net"
	if !needsPinnedInitialSession(m, pinned) {
		t.Fatal("initial Remote handoff session was not selected")
	}
	m.Enrollment = false
	if needsPinnedInitialSession(m, pinned) {
		t.Fatal("unenrolled instance was selected")
	}
}

// Actual server/private IPC and command adapters; only the managed OS/native
// owner peer/profile are simulated. No installed service, signer or VM exists.
func TestLocalPairedCommandPaths(t *testing.T)  { testCustodyPairedCommandPaths(t, false) }
func TestRemotePairedCommandPaths(t *testing.T) { testCustodyPairedCommandPaths(t, true) }
func testCustodyPairedCommandPaths(t *testing.T, remote bool) {
	if runtime.GOOS != "linux" {
		t.Skip("protected custody requires Linux")
	}
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(root, "app-state")
	server, e := core.Start(t.Context(), dir, "127.0.0.1:0", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close(context.Background())
	store := server.Store
	code, e := store.IssueClaimCode(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	original, e := store.Claim(t.Context(), code.Code, "disposable preserved command owner")
	if e != nil {
		t.Fatal(e)
	}
	instance, url := store.Health().InstanceID, server.URL
	handler := server.IPCServer.Handler
	var native atomic.Int32
	server.IPCServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			h := store.Health()
			h.Connection = &core.ConnectionInfo{Location: "local", OwnerUID: 1400}
			if remote {
				h.Connection.Location, h.Connection.OwnerKind = "remote", "custody"
			}
			_ = json.NewEncoder(w).Encode(h)
			return
		}
		if r.URL.Path == "/api/v1/auth/native-local" {
			native.Add(1)
			token, e := core.IssueMaintenanceSession(r.Context(), dir, instance)
			if e != nil {
				http.Error(w, "fixture native session refused", 500)
				return
			}
			query := httptest.NewRequest("GET", url+"/api/v1/auth/state", nil)
			query.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, query)
			var state map[string]any
			if json.Unmarshal(rec.Body.Bytes(), &state) != nil || state["authenticated"] != true {
				http.Error(w, "fixture native authentication refused", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"sessionToken": token, "csrfToken": state["csrfToken"]})
			return
		}
		handler.ServeHTTP(w, r)
	})
	go server.Serve()
	client, e := core.NewClient(t.Context(), dir)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	m := custodyinstall.Manifest{Root: root, Location: "local", Owner: 1400, NativeOwnerAccess: true}
	if remote {
		m.Location, m.NativeOwnerAccess, m.App.UID = "remote", false, os.Getuid()
	}
	if e = prepareCustodyReaderSession(t.Context(), client, m, instance); e != nil {
		t.Fatal(e)
	}
	snapshot, e := store.UpdateSnapshot(t.Context(), strings.Repeat("d", 32))
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = updateInventoryCommand([]string{"--data-dir", dir, "--probe-id", strings.Repeat("a", 32)}, &out, io.Discard); e != nil {
		t.Fatal("actual inventory", e)
	}
	out.Reset()
	if e = updateStatusCommand([]string{"--data-dir", dir}, &out, io.Discard); e != nil {
		t.Fatal("actual status", e)
	}
	out.Reset()
	if e = updatePreferencesCommand([]string{"--data-dir", dir}, nil, &out, io.Discard); e != nil {
		t.Fatal("actual read-only preferences", e)
	}
	for name, command := range map[string]func([]string, *os.File, io.Writer, io.Writer) error{"pause": updatePauseCommand, "backup": updateBackupCommand, "recovery": updateRecoveryCommand, "preferences-write": updatePreferencesCommand} {
		t.Run(name, func(t *testing.T) {
			f, e := os.CreateTemp(dir, "public-command-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.Remove(f.Name())
			defer f.Close()
			if _, e = f.WriteString("{}\n"); e != nil {
				t.Fatal(e)
			}
			if _, e = f.Seek(0, 0); e != nil {
				t.Fatal(e)
			}
			var output bytes.Buffer
			e = command([]string{"--data-dir", dir, "--request-stdin"}, f, &output, io.Discard)
			// Real private handler refuses the invalid metadata after authenticating.
			// This test never supplies a financial/update mandate or makes a pause.
			if e == nil || (!strings.Contains(e.Error(), "(400)") && !strings.Contains(e.Error(), "(409)")) {
				t.Fatal("expected typed-metadata refusal through actual command", e)
			}
		})
	}
	after, e := store.UpdateSnapshot(t.Context(), strings.Repeat("d", 32))
	if e != nil || snapshot.State.Anchor != after.State.Anchor || !store.Session(t.Context(), original) || native.Load() != map[bool]int32{true: 0, false: 1}[remote] {
		t.Fatal("command preflight changed protected state or repeated authentication", e)
	}
}
