//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestScopedSignerReceiptUsesApprovedSource(t *testing.T) {
	owner := os.Getuid()
	id := strings.Repeat("a", 32)
	intent := strings.Repeat("b", 32)
	target := Root(owner) + "/updates/" + intent + "/held.json"
	data := []byte(`{"version":1,"phase":"active"}`)
	source := ApprovedExecutablePath(owner, data)
	h := &memoryHost{files: map[string]hostFile{source: {Data: bytes.Clone(data), UID: 0, Mode: 0444, Regular: true}}}
	used := false
	run := func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if args[0] == "/usr/bin/install" {
			used = len(input) == 0 && args[len(args)-2] == source
		}
		return h.run(ctx, input, args...)
	}
	if err := publish(t.Context(), target, id, data, 0644, h.read, run); err != nil || !used || !known(h.files[target], data, 0644) {
		t.Fatalf("receipt source is not bound: %v", err)
	}
	h = &memoryHost{files: map[string]hostFile{}}
	reject := func(string, string, int64) error { return errors.New("receipt source changed") }
	if err := publish(t.Context(), target, id, data, 0644, h.read, h.run, reject); err == nil || h.calls != 0 {
		t.Fatal("changed receipt reached privileged write")
	}
	if scopedSignerReceipt(Root(owner+1)+"/updates/"+intent+"/held.json", owner) || scopedSignerReceipt(Root(owner)+"/updates/"+intent+"/other.json", owner) {
		t.Fatal("foreign receipt destination was admitted")
	}
	// Existing non-scoped installations keep their prior route when no approved
	// source is configured; the scoped permission must never allow that fallback.
	h = &memoryHost{files: map[string]hostFile{}}
	if err := publish(t.Context(), target, id, data, 0644, h.read, h.run); err != nil || !known(h.files[target], data, 0644) {
		t.Fatal("unscoped installation compatibility changed", err)
	}
}

func TestScopedReceiptPreservesRealHoldAcrossRefusalAndRetry(t *testing.T) {
	tx, dir := modelTransaction(t)
	o := modelOps(t, tx, dir)
	var receipt []byte
	hold := o.hold
	o.hold = func(ctx context.Context, phase string, binding update.InstallBinding) (*update.Installation, error) {
		r, err := hold(ctx, phase, binding)
		if err == nil && phase == "active" {
			receipt = mustJSON(*r)
		}
		return r, err
	}
	approved := false
	o.verify = func(path, digest string, size int64) error {
		if len(receipt) > 0 && path == ApprovedExecutablePath(os.Getuid(), receipt) {
			if !approved || digest != filepath.Base(path) || size != int64(len(receipt)) {
				return errors.New("approved receipt unavailable")
			}
			return nil
		}
		return os.ErrNotExist
	}
	run := o.run
	o.run = func(ctx context.Context, input []byte, args ...string) ([]byte, error) {
		if len(receipt) > 0 && args[0] == "/usr/bin/install" && args[len(args)-2] == ApprovedExecutablePath(os.Getuid(), receipt) {
			input = bytes.Clone(receipt)
		}
		return run(ctx, input, args...)
	}
	if _, err := runTransaction(t.Context(), tx, false, o); err == nil || len(receipt) == 0 {
		t.Fatal("missing approved receipt did not stop after the actual hold")
	}
	actual, err := o.state(t.Context())
	if err != nil || actual.Phase != "active" || !bytes.Equal(mustJSON(*actual), receipt) {
		t.Fatal("actual signer hold was not retained", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "updates", tx.Binding.Index.Binding.Pause.Request.IntentID, "held.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refusal wrote a held receipt", err)
	}
	approved = true
	result, err := runTransaction(t.Context(), tx, false, o)
	if err != nil || result == nil || result.Phase != "healthy-held" {
		t.Fatal("same held transaction did not recover", err)
	}
}
