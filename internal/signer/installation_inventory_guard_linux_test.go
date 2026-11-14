//go:build linux

package signer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/fased-ai/fased-app/internal/update"
)

func TestInstallationGuardChecksActualPairAndRecoveryInputs(t *testing.T) {
	v, _, _ := configuredFixture(t, fixtureDir(t))
	defer v.Close()
	intent, binding := installationFixture(t, v)
	config := Config{Version: 1, Mode: FixtureNetwork, SignerID: binding.InstallationID, OwnerUID: 1800, AppUID: 1801, SignerUID: 1802, StateDir: "/var/lib/fased-app-custody-1800/signer-state", SocketDir: "/var/lib/fased-app-custody-1800/signer-ipc"}
	root := fmt.Sprintf("/var/lib/fased-app-custody-%d", config.OwnerUID)
	base := map[string]any{"version": 1, "id": binding.InstallationID, "owner": config.OwnerUID, "root": root, "coreHash": metadataFixtureHash([]byte("old core")), "signerHash": metadataFixtureHash([]byte("old signer")), "listen": "127.0.0.1:7332", "app": map[string]any{"uid": config.AppUID}, "signer": map[string]any{"uid": config.SignerUID}}
	baseJSON, _ := json.Marshal(base)
	binding.PreviousCore = base["coreHash"].(string)
	binding.PreviousSigner = base["signerHash"].(string)
	binding.PreviousManifest = metadataFixtureHash(baseJSON)
	target := map[string]any{}
	for k, v := range base {
		target[k] = v
	}
	target["coreHash"], target["signerHash"] = binding.TargetCore, binding.TargetSigner
	targetJSON, _ := json.Marshal(target)
	txn, _ := json.Marshal(struct {
		Version int                   `json:"version"`
		Base    json.RawMessage       `json:"base"`
		Target  json.RawMessage       `json:"target"`
		Binding update.InstallBinding `json:"binding"`
	}{1, baseJSON, targetJSON, binding})
	held := &update.Installation{Version: 1, Binding: binding, Digest: update.InstallDigest(binding), Phase: "active"}
	if !update.ValidInstallation(*held) {
		t.Fatal("invalid fixture installation")
	}
	dir := root + "/updates/" + intent.ID
	files := map[string][]byte{dir + "/transaction.json": txn, dir + "/base-install.json": baseJSON, dir + "/target-install.json": targetJSON, root + "/install.json": baseJSON}
	hashes := map[string]string{dir + "/runner": binding.PreviousCore, dir + "/previous-signer": binding.PreviousSigner, dir + "/next-core": binding.TargetCore, dir + "/next-signer": binding.TargetSigner, root + "/fased-app": binding.PreviousCore, root + "/fased-signer": binding.PreviousSigner}
	read := func(p string) ([]byte, error) {
		b, ok := files[p]
		if !ok {
			return nil, os.ErrNotExist
		}
		return b, nil
	}
	hash := func(p string) (string, error) {
		h, ok := hashes[p]
		if !ok {
			return "", os.ErrNotExist
		}
		return h, nil
	}
	linked := true
	same := func(first, second string) error {
		if !linked || first != root+"/maintenance/predecessor-proof-v1.json" || second != dir+"/atomic-held.json" {
			return errors.New("different protected inode")
		}
		return nil
	}
	check := func(direction, stage string) error {
		return validateInstallationGuardInventory(config, held, intent.ID, direction, stage, read, hash, same)
	}
	if err := check("forward", "before"); err != nil {
		t.Fatal(err)
	}
	if err := check("forward", "predecessor"); err != nil {
		t.Fatal("exact predecessor was refused", err)
	}
	hashes[root+"/fased-app"] = binding.TargetCore
	if check("forward", "predecessor") == nil || check("rollback", "predecessor") == nil {
		t.Fatal("mixed pair without protected predecessor proof was accepted")
	}
	hashes[root+"/fased-signer"] = binding.TargetSigner
	files[root+"/install.json"] = targetJSON
	if check("rollback", "predecessor") == nil {
		t.Fatal("target pair without protected predecessor proof was accepted")
	}
	hashes[root+"/fased-signer"] = binding.PreviousSigner
	files[root+"/install.json"] = baseJSON
	files[root+"/maintenance/predecessor-proof-v1.json"], _ = json.Marshal(held)
	files[dir+"/atomic-held.json"], _ = json.Marshal(held)
	linked = false
	if check("rollback", "predecessor") == nil {
		t.Fatal("equal bytes on a different inode were accepted as proof")
	}
	linked = true
	if err := check("rollback", "predecessor"); err != nil {
		t.Fatal("protected predecessor proof did not permit recovery", err)
	}
	files[root+"/maintenance/predecessor-proof-v1.json"] = []byte(`{"foreign":true}`)
	if check("rollback", "predecessor") == nil {
		t.Fatal("foreign predecessor proof accepted")
	}
	delete(files, root+"/maintenance/predecessor-proof-v1.json")
	hashes[root+"/fased-app"] = binding.PreviousCore
	if err := check("forward", "after"); err == nil {
		t.Fatal("old pair accepted as target")
	}
	hashes[root+"/fased-app"] = binding.TargetCore // interruption mid-replacement
	if err := check("forward", "before"); err != nil {
		t.Fatal("recoverable mixed pair refused", err)
	}
	hashes[dir+"/previous-signer"] = strings.Repeat("0", 64)
	if err := check("forward", "before"); err == nil {
		t.Fatal("changed rollback reader accepted")
	}
	hashes[dir+"/previous-signer"] = binding.PreviousSigner
	hashes[root+"/fased-signer"] = binding.TargetSigner
	files[root+"/install.json"] = targetJSON
	if err := check("forward", "after"); err != nil {
		t.Fatal(err)
	}
	files[root+"/install.json"] = []byte(`{"foreign":true}`)
	if err := check("forward", "before"); err == nil {
		t.Fatal("foreign inventory accepted")
	}
	files[root+"/install.json"] = targetJSON
	files[dir+"/base-install.json"] = targetJSON
	if err := check("rollback", "before"); err == nil {
		t.Fatal("changed recovery inventory accepted")
	}
	files[dir+"/base-install.json"] = baseJSON
	target["owner"] = config.OwnerUID + 1
	foreignTarget, _ := json.Marshal(target)
	files[dir+"/transaction.json"], _ = json.Marshal(struct {
		Version int                   `json:"version"`
		Base    json.RawMessage       `json:"base"`
		Target  json.RawMessage       `json:"target"`
		Binding update.InstallBinding `json:"binding"`
	}{1, baseJSON, foreignTarget, binding})
	files[dir+"/target-install.json"] = foreignTarget
	files[root+"/install.json"] = foreignTarget
	if err := check("forward", "after"); err == nil {
		t.Fatal("target changing owner identity accepted")
	}
	files[dir+"/transaction.json"] = txn
	files[dir+"/target-install.json"] = targetJSON
	files[root+"/install.json"] = targetJSON
	baseIntent := map[string]any{"version": 1, "id": binding.InstallationID, "owner": config.OwnerUID, "coreHash": binding.PreviousCore, "signerHash": binding.PreviousSigner, "listen": "127.0.0.1:7332"}
	targetIntent := map[string]any{"version": 1, "id": binding.InstallationID, "owner": config.OwnerUID, "coreHash": binding.TargetCore, "signerHash": binding.TargetSigner, "listen": "127.0.0.1:7332"}
	files[dir+"/base-intent.json"], _ = json.Marshal(baseIntent)
	files[dir+"/target-intent.json"], _ = json.Marshal(targetIntent)
	files[dir+"/base-ready.json"], _ = json.Marshal(map[string]any{"schema": 1, "manifest": metadataFixtureHash(baseJSON), "fixtureOnly": true})
	files[dir+"/target-ready.json"], _ = json.Marshal(map[string]any{"schema": 1, "manifest": metadataFixtureHash(targetJSON), "fixtureOnly": true})
	files[root+"/intent.json"] = files[dir+"/base-intent.json"]
	files[root+"/ready.json"] = files[dir+"/base-ready.json"]
	if err := check("completion", "before"); err == nil {
		t.Fatal("uncommitted completion accepted")
	}
	held.Phase, held.InstalledSequenceCommitted = "committed-held", true
	if err := check("completion", "before"); err != nil {
		t.Fatal("committed completion refused", err)
	}
	files[root+"/intent.json"] = files[dir+"/target-intent.json"]
	if err := check("completion", "before"); err != nil {
		t.Fatal("partial completion cannot resume", err)
	}
	if err := check("completion", "after"); err == nil {
		t.Fatal("incomplete completion accepted")
	}
	files[root+"/ready.json"] = files[dir+"/target-ready.json"]
	if err := check("completion", "after"); err != nil {
		t.Fatal("completed metadata refused", err)
	}
	files[root+"/ready.json"] = []byte(`{"foreign":true}`)
	if err := check("completion", "before"); err == nil {
		t.Fatal("foreign completion record accepted")
	}
	files[root+"/ready.json"] = files[dir+"/target-ready.json"]
	held.Phase = "completed"
	files[root+"/update-active.json"], _ = json.Marshal(struct {
		Version int    `json:"version"`
		Digest  string `json:"digest"`
		Phase   string `json:"phase"`
	}{1, held.Digest, intent.ID})
	if err := check("cleanup-complete", "before"); err != nil {
		t.Fatal("completed cleanup refused", err)
	}
	if err := check("cleanup-complete", "after"); err == nil {
		t.Fatal("active record not cleared")
	}
	delete(files, root+"/update-active.json")
	if err := check("cleanup-complete", "after"); err != nil {
		t.Fatal("completed cleanup did not resume", err)
	}
	held.Phase, held.InstalledSequenceCommitted = "rolled-back-held", false
	hashes[root+"/fased-app"], hashes[root+"/fased-signer"] = binding.PreviousCore, binding.PreviousSigner
	files[root+"/install.json"] = baseJSON
	files[root+"/intent.json"], files[root+"/ready.json"] = files[dir+"/base-intent.json"], files[dir+"/base-ready.json"]
	if err := check("cleanup-rollback", "before"); err == nil {
		t.Fatal("rollback cleanup without decision accepted")
	}
	files[dir+"/rollback.json"], _ = json.Marshal(struct {
		Version int    `json:"version"`
		Digest  string `json:"digest"`
		Phase   string `json:"phase"`
	}{1, held.Digest, "rollback"})
	if err := check("cleanup-rollback", "before"); err != nil {
		t.Fatal("rollback cleanup refused", err)
	}
}
