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
	binary := os.Getenv("FASED_READER_PREDECESSOR")
	if binary == "" {
		t.Skip("compiled predecessor fixture is supplied by the focused reader handoff check")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	child := exec.CommandContext(ctx, binary, dir)
	var stderr bytes.Buffer
	child.Stderr = &stderr
	pipe, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	var initial struct {
		Code string `json:"code"`
	}
	if e = json.NewDecoder(pipe).Decode(&initial); e != nil {
		t.Fatal("predecessor did not start", e, stderr.String())
	}
	client, e := core.NewClient(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = client.Claim(ctx, initial.Code, "Disposable reader fixture password"); e != nil {
		t.Fatal(e)
	}
	if e = client.SaveSession(dir); e != nil {
		t.Fatal(e)
	}
	var snapshot update.CoreSnapshot
	if e = client.Do(ctx, http.MethodGet, "/api/v1/updates/inventory?probe="+update.NewProbe(), nil, &snapshot); e != nil || snapshot.State.Version != 33 {
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
	if e = client.Do(ctx, http.MethodPost, "/api/v1/updates/pause", update.CorePauseCommand{Action: "pause", ProbeID: update.NewProbe(), Request: &want}, &pause); e != nil {
		t.Fatal("real predecessor pause failed", e)
	}
	request := core.ApplicationReaderBootstrapRequest{Version: 1, OperationID: id, OwnerLease: update.NewProbe(), PreviousEngineSHA256: hex.EncodeToString(oldHash[:]), Release: release, Predecessor: pause}
	if e = core.StageApplicationReaderBootstrap(ctx, dir, request); e != nil {
		t.Fatal(e)
	}
	if _, e = core.BootstrapApplicationReader(ctx, dir, request); e == nil {
		t.Fatal("migration accepted a live predecessor engine")
	}
	client.Close()
	if e = child.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	if e = child.Wait(); e != nil {
		t.Fatal("predecessor failed to stop", e, stderr.String())
	}
	waited = true
	_, _ = io.Copy(io.Discard, pipe)
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
	if e != nil || state.View.Schema != 35 || state.View.InstanceID != snapshot.InstanceID {
		t.Fatal(state, e)
	}
	proof := core.ApplicationUpdateReceipt{ID: id, Sequence: release.Sequence, ManifestSHA256: release.ManifestSHA256, EngineSHA256: release.EngineSHA256, Phase: "completed"}
	if _, e = ipc.call(ctx, "finish", &proof); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "cli-session.json")); e != nil {
		t.Fatal("source session not preserved", e)
	}
	if e = core.CheckApplicationRollbackMetadata(ctx, dir, id, 33); e == nil {
		t.Fatal("schema33 rollback accepted after migration")
	}
}
