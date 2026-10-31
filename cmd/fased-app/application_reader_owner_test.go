package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
)

// The predecessor is an explicitly compiled, disposable schema33 source fixture.
// Its API/session/pause/lock are real; it installs no OS service and uses no live
// provider, RPC, signer, funds, channel or customer state.
func TestApplicationReaderCompiledPredecessorIPC(t *testing.T) {
	applicationReaderIPC(t, os.Getenv("FASED_READER_PREDECESSOR"), 33, "")
}
func TestApplicationReader35IPC(t *testing.T) {
	for _, reboot := range []string{"", "before-pause", "after-pause"} {
		t.Run(reboot, func(t *testing.T) { applicationReaderIPC(t, os.Getenv("FASED_READER_PREDECESSOR35"), 35, reboot) })
	}
}
func applicationReaderIPC(t *testing.T, binary string, schema int, reboot string) {
	t.Helper()
	if binary == "" {
		t.Skip("compiled predecessor fixture is supplied by the focused reader handoff check")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	var child *exec.Cmd
	var stderr readerIPCLog
	var pipe io.ReadCloser
	waited := true
	start := func() string {
		child = exec.CommandContext(ctx, binary, dir)
		child.Stderr = &stderr
		var err error
		pipe, err = child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err = child.Start(); err != nil {
			t.Fatal(err)
		}
		waited = false
		var initial struct {
			Code string `json:"code"`
		}
		if err = json.NewDecoder(pipe).Decode(&initial); err != nil {
			t.Fatal("predecessor did not start", err, stderr.String())
		}
		return initial.Code
	}
	stopSource := func() {
		if err := child.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if err := child.Wait(); err != nil {
			t.Fatal(err, stderr.String())
		}
		waited = true
		_, _ = io.Copy(io.Discard, pipe)
	}
	code := start()
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	client, e := core.NewClient(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = client.Claim(ctx, code, "Disposable reader fixture password"); e != nil {
		t.Fatal(e)
	}
	if e = client.SaveSession(dir); e != nil {
		t.Fatal(e)
	}
	var snapshot update.CoreSnapshot
	if e = client.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+update.NewProbe(), nil, &snapshot); e != nil || snapshot.State.Version != schema {
		t.Fatal(snapshot, e)
	}
	if e = core.CheckApplicationReaderPredecessor(ctx, dir, snapshot); e != nil {
		t.Fatal("real predecessor snapshot incompatible", e)
	}
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	image, e := os.ReadFile(self)
	if e != nil {
		t.Fatal(e)
	}
	targetHash := sha256.Sum256(image)
	old, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	oldHash := sha256.Sum256(old)
	release := core.ApplicationRelease{Version: "0.0.0-reader-fixture", EngineSHA256: hex.EncodeToString(targetHash[:]), Sequence: 7, ManifestSHA256: strings.Repeat("a", 64), Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	id := update.NewProbe()
	want := update.CorePauseRequest{Version: 1, IntentID: id, Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, Expected: snapshot}
	var pause update.CorePauseReceipt
	request := core.ApplicationReaderBootstrapRequest{Version: 1, OperationID: id, OwnerLease: update.NewProbe(), PreviousEngineSHA256: hex.EncodeToString(oldHash[:]), Release: release}
	if schema == 35 {
		request.Version = 2
		request.Predecessor = update.CorePauseReceipt{Version: 1, State: "prepared", Core: snapshot, Request: &want}
		if e = core.PrepareApplicationReaderBootstrap(ctx, dir, request); e != nil {
			t.Fatal(e)
		}
		preparationPath := filepath.Join(dir, "lifecycle", id, "reader-preparation.json")
		originalPreparation, err := os.ReadFile(preparationPath)
		if err != nil {
			t.Fatal(err)
		}
		originalLease := request.OwnerLease

		if reboot == "after-pause" {
			if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &want}, &pause); e != nil {
				t.Fatal(e)
			}
		}
		if reboot != "" {
			client.Close()
			stopSource()
			start()
			client, e = core.NewClient(ctx, dir)
			if e != nil {
				t.Fatal(e)
			}
			if e = client.LoadSession(ctx, dir); e != nil {
				t.Fatal(e)
			}
		}
		if e = stageLocalReader35(ctx, dir, &request); e != nil {
			t.Fatal("original35 handoff failed", reboot, e)
		}
		retainedPreparation, err := os.ReadFile(preparationPath)
		if err != nil || !bytes.Equal(originalPreparation, retainedPreparation) || request.OwnerLease != originalLease {
			t.Fatal("reboot rewrote preparation or lease", err)
		}
		pause = request.Predecessor
	} else {
		if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &want}, &pause); e != nil {
			t.Fatal(e)
		}
		request.Predecessor = pause
		if e = core.StageApplicationReaderBootstrap(ctx, dir, request); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = core.BootstrapApplicationReader(ctx, dir, request); e == nil {
		t.Fatal("migration accepted a live predecessor engine")
	}
	client.Close()
	stopSource()
	if _, e = core.ResumeApplicationReaderBootstrap(ctx, dir, id); e != nil {
		t.Fatal("actual stopped-reader handoff failed", e)
	}
	server, e := core.Start(ctx, dir, "127.0.0.1:0", "", nil)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Close(closeCtx)
		<-done
	}()
	ipc := &applicationOwnerIPC{directory: dir, instance: snapshot.InstanceID, id: id, lease: request.OwnerLease}
	state, e := ipc.call(ctx, "inspect", nil)
	if e != nil || state.View.Schema != core.StateSchema || state.View.InstanceID != snapshot.InstanceID {
		t.Fatal(state, e)
	}
	proof := core.ApplicationUpdateReceipt{ID: id, Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, EngineSHA256: release.EngineSHA256, Phase: "completed"}
	if _, e = ipc.call(ctx, "finish", &proof); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "cli-session.json")); e != nil {
		t.Fatal("source session not preserved", e)
	}
	if e = core.CheckApplicationRollbackMetadata(ctx, dir, id, schema); e == nil {
		t.Fatal("old reader rollback accepted after migration")
	}
}

type readerIPCLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *readerIPCLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *readerIPCLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }
