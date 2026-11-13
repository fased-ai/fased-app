package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

// The real CLI and core API are exercised; only the already-reviewed native
// owner transport is substituted. This is not installed OS-identity proof.
func TestApplicationUpdatesUsesEnrolledOwnerTransport(t *testing.T) {
	for _, mode := range []string{"status", "check", "apply", "auth-refused", "identity-refused"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			original := []byte("preserve original session")
			path := filepath.Join(dir, "cli-session.json")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			auth, reads, writes := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/auth/native-local":
					auth++
					if mode == "auth-refused" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"sessionToken": "transient-owner", "csrfToken": "owner-csrf"})
				case "/api/v1/updates/application":
					if r.Header.Get("Authorization") != "Bearer transient-owner" {
						t.Error("missing owner session")
					}
					view := core.ApplicationUpdateView{InstanceID: "instance", Revision: 7, PolicyRevision: 2, ControllerAvailable: true,
						Operation: core.ApplicationUpdateOperation{Phase: "available", Release: &core.ApplicationRelease{Version: "fixture", ManifestSHA256: strings.Repeat("a", 64)}}}
					if r.Method == http.MethodPost {
						writes++
						var command core.ApplicationUpdateCommand
						if r.Header.Get("X-CSRF-Token") != "owner-csrf" || json.NewDecoder(r.Body).Decode(&command) != nil || command.Action != mode || command.Revision != 7 || command.PolicyRevision != 2 {
							t.Error("unexpected update command or CSRF")
						}
						if mode == "apply" && command.ManifestSHA256 != strings.Repeat("a", 64) {
							t.Error("approved target changed")
						}
					} else {
						reads++
					}
					_ = json.NewEncoder(w).Encode(view)
				default:
					t.Error("unexpected protected route", r.URL.Path)
					w.WriteHeader(http.StatusForbidden)
				}
			}))
			defer server.Close()
			prior := applicationUpdatesPublicClient
			defer func() { applicationUpdatesPublicClient = prior }()
			applicationUpdatesPublicClient = func(_ context.Context, data string) (*core.Client, bool, error) {
				if data != dir {
					t.Fatal("changed owner directory")
				}
				if mode == "identity-refused" {
					return nil, false, errors.New("foreign enrollment")
				}
				return &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: "instance"}, HTTP: server.Client()}, true, nil
			}
			action := mode
			if mode == "auth-refused" || mode == "identity-refused" {
				action = "status"
			}
			var input *os.File
			if mode == "apply" {
				p := filepath.Join(dir, "approval")
				if err := os.WriteFile(p, []byte("y\n"), 0600); err != nil {
					t.Fatal(err)
				}
				var err error
				input, err = os.Open(p)
				if err != nil {
					t.Fatal(err)
				}
				defer input.Close()
			}
			var out bytes.Buffer
			err := ordinaryUpdateCommand([]string{action, "--data-dir", dir}, input, &out, io.Discard)
			good := mode == "status" || mode == "check" || mode == "apply"
			if (err == nil) != good || good && (auth != 1 || reads != 1) || !good && (reads != 0 || writes != 0) {
				t.Fatalf("%s: err=%v auth=%d reads=%d writes=%d", mode, err, auth, reads, writes)
			}
			if good && ((mode == "status" && writes != 0) || (mode != "status" && writes != 1)) {
				t.Fatal("duplicate or missing update command")
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("saved session changed", err)
			}
		})
	}
}

func TestApplicationUpdatesClientPreservesSymlinkedOrdinaryParent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux custody enrollment only")
	}
	base, err := os.MkdirTemp("", "fased-update-client-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	real := filepath.Join(base, "real")
	if err = os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "link")
	if err = os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(alias, "state")
	server, err := core.Start(t.Context(), data, "127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(ctx)
	}()
	c, enrolled, err := applicationUpdatesPublicClient(t.Context(), data)
	if err != nil || enrolled {
		t.Fatalf("ordinary symlinked parent refused: enrolled=%v err=%v", enrolled, err)
	}
	c.Close()

	foreign := filepath.Join(base, "root-owned")
	if err = os.Symlink("/etc", foreign); err != nil {
		t.Fatal(err)
	}
	if c, _, err = applicationUpdatesPublicClient(t.Context(), foreign); err == nil {
		c.Close()
		t.Fatal("ambiguous root-owned enrollment accepted through symlink")
	}
}

func TestApplicationUpdatesRetainsExistingSessionForOrdinaryInstall(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("A", 43)
	saved, err := json.Marshal(core.SavedSession{InstanceID: "instance", Token: token})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "cli-session.json")
	if err := os.WriteFile(path, saved, 0600); err != nil {
		t.Fatal(err)
	}
	states, reads := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("saved owner session missing")
		}
		switch r.URL.Path {
		case "/api/v1/auth/state":
			states++
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "csrfToken": "saved-csrf"})
		case "/api/v1/updates/application":
			reads++
			_ = json.NewEncoder(w).Encode(core.ApplicationUpdateView{InstanceID: "instance"})
		default:
			t.Error("unexpected route", r.URL.Path)
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer server.Close()
	prior := applicationUpdatesPublicClient
	defer func() { applicationUpdatesPublicClient = prior }()
	applicationUpdatesPublicClient = func(_ context.Context, data string) (*core.Client, bool, error) {
		return &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: "instance"}, HTTP: server.Client()}, false, nil
	}
	if err := ordinaryUpdateCommand([]string{"status", "--data-dir", dir}, nil, io.Discard, io.Discard); err != nil || states != 1 || reads != 1 {
		t.Fatalf("ordinary session path changed: %v states=%d reads=%d", err, states, reads)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, saved) {
		t.Fatal("ordinary saved session changed", err)
	}
}

func TestOrdinaryUpdateWizardReusesNativeOwnerSession(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "approval")
	if err := os.WriteFile(inputPath, []byte("y\n"), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var auth, checks, applies int
	var available bool
	release := &core.ApplicationRelease{Version: "fixture", ManifestSHA256: strings.Repeat("a", 64)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/native-local" {
			auth++
			_ = json.NewEncoder(w).Encode(map[string]string{"sessionToken": "transient-owner", "csrfToken": "owner-csrf"})
			return
		}
		if r.URL.Path != "/api/v1/updates/application" || r.Header.Get("Authorization") != "Bearer transient-owner" {
			t.Error("unexpected update route or session")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		view := core.ApplicationUpdateView{InstanceID: "instance", Revision: 7, PolicyRevision: 2, ControllerAvailable: true}
		if available {
			view.Operation.Phase, view.Operation.Release = "available", release
		} else {
			view.Operation.Phase = "idle"
		}
		if r.Method == http.MethodPost {
			var command core.ApplicationUpdateCommand
			if json.NewDecoder(r.Body).Decode(&command) != nil || r.Header.Get("X-CSRF-Token") != "owner-csrf" {
				t.Error("update mutation lost CSRF")
			}
			switch command.Action {
			case "check":
				checks++
				available = true
				view.Operation.Phase, view.Operation.Release = "available", release
			case "apply":
				applies++
				view.Operation.Phase, view.Operation.Release, view.Operation.ID = "completed", release, strings.Repeat("b", 32)
			default:
				t.Error("unexpected update action")
			}
		}
		_ = json.NewEncoder(w).Encode(view)
	}))
	defer server.Close()
	prior := applicationUpdatesPublicClient
	defer func() { applicationUpdatesPublicClient = prior }()
	applicationUpdatesPublicClient = func(_ context.Context, _ string) (*core.Client, bool, error) {
		return &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: "instance"}, HTTP: server.Client()}, true, nil
	}
	var out bytes.Buffer
	if err := ordinaryUpdateCommand([]string{"--data-dir", dir}, input, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if auth != 1 || checks != 1 || applies != 1 || !strings.Contains(out.String(), "Updated") {
		t.Fatalf("wizard repeated authority or operation: auth=%d check=%d apply=%d output=%q", auth, checks, applies, out.String())
	}
}

func TestApplicationUpdateRefusesForeignInstanceBeforeMutation(t *testing.T) {
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		_ = json.NewEncoder(w).Encode(core.ApplicationUpdateView{InstanceID: "foreign", ControllerAvailable: true})
	}))
	defer server.Close()
	client := &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: "original"}, HTTP: server.Client()}
	err := applicationUpdatesWithClient(t.Context(), client, "check", nil, io.Discard, io.Discard)
	if err == nil || mutations != 0 {
		t.Fatal("foreign instance received an update mutation", err)
	}
}

func TestApplicationUpdateRefusesMutationResponseWithoutInstance(t *testing.T) {
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mutations++
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_ = json.NewEncoder(w).Encode(core.ApplicationUpdateView{InstanceID: "original", ControllerAvailable: true})
	}))
	defer server.Close()
	client := &core.Client{Endpoint: core.Endpoint{URL: server.URL, InstanceID: "original"}, HTTP: server.Client()}
	err := applicationUpdatesWithClient(t.Context(), client, "check", nil, io.Discard, io.Discard)
	if err == nil || mutations != 1 {
		t.Fatal("missing update response identity was accepted or retried", err, mutations)
	}
}
