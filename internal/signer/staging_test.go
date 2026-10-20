//go:build linux

package signer

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

func stageFixture(t *testing.T, v *Vault) (update.Intent, []byte) {
	t.Helper()
	r := intentFixture(t, v, 1)
	var env update.Envelope
	var m update.Manifest
	if e := json.Unmarshal(r.Manifest, &env); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(env.Payload, &m); e != nil {
		t.Fatal(e)
	}
	data := bytes.Repeat([]byte("disposable artifact bytes\n"), 1800)
	h := sha256.Sum256(data)
	for n := range m.Components {
		m.Components[n].Artifact.Size = int64(len(data))
		m.Components[n].Artifact.SHA256 = hex.EncodeToString(h[:])
	}
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().Unix()
	r.Trust, _ = json.Marshal(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: now - 60, NotAfter: now + 3600}}})
	payload, _ := json.Marshal(m)
	r.Manifest, _ = json.Marshal(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: payload, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), payload...))})
	i, e := v.PrepareUpdate(r)
	if e != nil {
		t.Fatal(e)
	}
	return *i, data
}
func stageRequest(i update.Intent) update.StageRequest {
	return update.StageRequest{IntentID: i.ID, Sequence: i.Sequence, ManifestSHA256: i.ManifestSHA256, Core: &i.Core}
}
func writeStage(t *testing.T, v *Vault, i update.Intent, data []byte) {
	t.Helper()
	r := stageRequest(i)
	for _, a := range i.Artifacts {
		r.File = a.File
		for off := 0; off < len(data); off += update.StageChunk {
			r.Offset = int64(off)
			r.Data = data[off:min(off+update.StageChunk, len(data))]
			if _, e := v.StageUpdate("fixture-update-stage-write", r); e != nil {
				t.Fatal(e)
			}
		}
	}
}

func TestStagingRecoveryConcurrencyAndFinancialPreservation(t *testing.T) {
	dir := fixtureDir(t)
	v, _, g := configuredFixture(t, dir)
	op := intentFor(g, 7)
	if _, e := v.ReserveFixture(op); e != nil {
		t.Fatal(e)
	}
	if _, e := v.ExecuteFixture(op.OperationID); e != nil {
		t.Fatal(e)
	}
	pending := intentFor(g, 9)
	if _, e := v.ReserveFixture(pending); e != nil {
		t.Fatal(e)
	}
	i, data := stageFixture(t, v)
	r := stageRequest(i)
	vault, _ := os.ReadFile(filepath.Join(dir, "vault.json"))
	mock, _ := os.ReadFile(filepath.Join(dir, "mock-receipts.log"))
	r.File = i.Artifacts[0].File
	r.Data = data[:update.StageChunk]
	for _, tc := range []string{"gap", "offset", "file", "digest", "floor"} {
		bad := r
		switch tc {
		case "gap":
			bad.Offset = 1
		case "offset":
			bad.Offset = -1
		case "file":
			bad.File = "../vault.json"
		case "digest":
			bad.ManifestSHA256 = "wrong"
		case "floor":
			bad.Sequence++
		}
		if _, e := v.StageUpdate("fixture-update-stage-write", bad); e == nil {
			t.Fatal("invalid chunk accepted", tc)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Go(func() { _, e := v.StageUpdate("fixture-update-stage-write", r); errs <- e })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("identical competing chunk", e)
		}
	}
	bad := r
	bad.Data = bytes.Repeat([]byte{0}, len(r.Data))
	if _, e := v.StageUpdate("fixture-update-stage-write", bad); e == nil {
		t.Fatal("conflicting retry overwrote prefix")
	}
	if _, e := v.StageUpdate("fixture-update-stage-finish", stageRequest(i)); e == nil {
		t.Fatal("partial stage finalized")
	}
	if e := v.Close(); e != nil {
		t.Fatal(e)
	}
	v = openFixture(t, dir)
	s, e := v.StageUpdate("fixture-update-stage-status", stageRequest(i))
	if e != nil || s.Files[0].Bytes != update.StageChunk || s.Verified {
		t.Fatal("partial prefix lost", s, e)
	}
	writeStage(t, v, i, data)
	r = stageRequest(i)
	badCore := i.Core
	badCore.BootID = update.NewProbe()
	r.Core = &badCore
	if _, e = v.StageUpdate("fixture-update-stage-finish", r); e == nil {
		t.Fatal("stale core accepted")
	}
	r = stageRequest(i)
	r.Sequence++
	if _, e = v.StageUpdate("fixture-update-stage-finish", r); e == nil {
		t.Fatal("stale floor accepted")
	}
	r = stageRequest(i)
	r.ManifestSHA256 = update.SnapshotDigest("wrong", 0)
	if _, e = v.StageUpdate("fixture-update-stage-finish", r); e == nil {
		t.Fatal("wrong manifest accepted")
	}
	seq := v.ledger.main.seq
	for n := 0; n < 2; n++ {
		s, e = v.StageUpdate("fixture-update-stage-finish", stageRequest(i))
		if e != nil || !s.Verified || s.State != "staged" || s.InstallAllowed || s.ResumeAllowed || s.FinancialExecution {
			t.Fatal(s, e)
		}
	}
	if v.ledger.main.seq != seq+1 {
		t.Fatal("duplicate completion record")
	}
	if e = v.Close(); e != nil {
		t.Fatal(e)
	}
	v = openFixture(t, dir)
	s, e = v.StageUpdate("fixture-update-stage-status", stageRequest(i))
	if e != nil || !s.Verified || !v.Status().Locked {
		t.Fatal("staged restart", e)
	}
	if e = v.Unlock(fixturePassword, time.Minute); e == nil {
		t.Fatal("stage enabled wallet")
	}
	if _, e = v.AbortUpdate(i.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = v.StageUpdate("fixture-update-stage-write", r); e == nil {
		t.Fatal("aborted intent mutated stage")
	}
	if v.ledger.updateFloor != 1 || !v.Status().Locked {
		t.Fatal("abort reset floor/lock")
	}
	requireSummary(t, v, 7, 9)
	got, e := v.FixtureOperation(pending.OperationID)
	if e != nil || got.State != "reserved" {
		t.Fatal("pending changed", e)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "vault.json")); !bytes.Equal(b, vault) {
		t.Fatal("key changed")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mock-receipts.log")); !bytes.Equal(b, mock) {
		t.Fatal("original receipts changed")
	}
	if _, e = os.Stat(filepath.Join(dir, "update-"+i.ID, "0.artifact")); e != nil {
		t.Fatal("abort discarded staged evidence", e)
	}
}

func TestStagingStorageFailureAndTampering(t *testing.T) {
	for _, tc := range []string{"disk-full", "partial-write", "reply-loss", "journal-reply-loss", "symlink", "hardlink", "corrupt"} {
		t.Run(tc, func(t *testing.T) {
			dir := fixtureDir(t)
			v, _, _ := configuredFixture(t, dir)
			i, data := stageFixture(t, v)
			r := stageRequest(i)
			r.File = i.Artifacts[0].File
			r.Data = data[:update.StageChunk]
			if tc == "journal-reply-loss" || tc == "corrupt" {
				writeStage(t, v, i, data)
			}
			if tc == "symlink" || tc == "hardlink" {
				d := filepath.Join(dir, "update-"+i.ID)
				if e := os.Mkdir(d, 0700); e != nil {
					t.Fatal(e)
				}
				src := filepath.Join(dir, "vault.json")
				var e error
				if tc == "symlink" {
					e = os.Symlink(src, filepath.Join(d, "0.artifact"))
				} else {
					e = os.Link(src, filepath.Join(d, "0.artifact"))
				}
				if e != nil {
					t.Fatal(e)
				}
			}
			if tc == "corrupt" {
				if e := os.WriteFile(filepath.Join(dir, "update-"+i.ID, "0.artifact"), bytes.Repeat([]byte{0}, len(data)), 0600); e != nil {
					t.Fatal(e)
				}
			}
			v.store.crash = func(phase string) error {
				if tc == "disk-full" && phase == "stage:before-write" {
					return syscall.ENOSPC
				}
				if tc == "partial-write" && phase == "stage:before-write" {
					// Model a kernel short write followed by ENOSPC, preserving its prefix.
					if e := os.WriteFile(filepath.Join(dir, "update-"+i.ID, "0.artifact"), r.Data[:13], 0600); e != nil {
						return e
					}
					return syscall.ENOSPC
				}
				if tc == "reply-loss" && phase == "stage:durable" || tc == "journal-reply-loss" && phase == "journal:update-staged:durable" {
					return errors.New("lost response")
				}
				return nil
			}
			action := "fixture-update-stage-write"
			if tc == "journal-reply-loss" || tc == "corrupt" {
				action = "fixture-update-stage-finish"
			}
			if _, e := v.StageUpdate(action, r); e == nil {
				t.Fatal("unsafe staging succeeded")
			}
			v.store.crash = nil
			if e := v.Close(); e != nil {
				t.Fatal(e)
			}
			v = openFixture(t, dir)
			if tc == "symlink" || tc == "hardlink" || tc == "corrupt" {
				if _, e := v.StageUpdate("fixture-update-stage-finish", stageRequest(i)); e == nil {
					t.Fatal("unsafe bytes accepted")
				}
				return
			}
			if tc == "journal-reply-loss" {
				s, e := v.StageUpdate("fixture-update-stage-status", stageRequest(i))
				if e != nil || !s.Verified {
					t.Fatal("durable receipt lost", e)
				}
				return
			}
			writeStage(t, v, i, data)
			if _, e := v.StageUpdate("fixture-update-stage-finish", stageRequest(i)); e != nil {
				t.Fatal("restart could not finish", e)
			}
		})
	}
}

func TestStagingActualProcessTermination(t *testing.T) {
	if dir := os.Getenv("FASED_STAGE_CHILD"); dir != "" {
		v, e := Open(dir, "11111111111111111111111111111111")
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(dir, "stage-request.json"))
		if e != nil {
			t.Fatal(e)
		}
		var r update.StageRequest
		if e = json.Unmarshal(b, &r); e != nil {
			t.Fatal(e)
		}
		v.store.crash = func(p string) error {
			if p == os.Getenv("FASED_STAGE_EXIT") {
				os.Exit(78)
			}
			return nil
		}
		_, e = v.StageUpdate("fixture-update-stage-write", r)
		t.Fatal("child did not terminate", e)
	}
	for _, phase := range []string{"stage:written", "stage:durable"} {
		t.Run(phase, func(t *testing.T) {
			dir := fixtureDir(t)
			v, _, _ := configuredFixture(t, dir)
			i, data := stageFixture(t, v)
			r := stageRequest(i)
			r.File = i.Artifacts[0].File
			r.Data = data[:update.StageChunk]
			b, _ := json.Marshal(r)
			if e := os.WriteFile(filepath.Join(dir, "stage-request.json"), b, 0600); e != nil {
				t.Fatal(e)
			}
			if e := v.Close(); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestStagingActualProcessTermination$")
			cmd.Env = append(os.Environ(), "FASED_STAGE_CHILD="+dir, "FASED_STAGE_EXIT="+phase)
			if b, e := cmd.CombinedOutput(); e == nil || cmd.ProcessState.ExitCode() != 78 {
				t.Fatalf("exit proof: %s %v", b, e)
			}
			v = openFixture(t, dir)
			writeStage(t, v, i, data)
			if _, e := v.StageUpdate("fixture-update-stage-finish", stageRequest(i)); e != nil {
				t.Fatal("process recovery", e)
			}
		})
	}
}
