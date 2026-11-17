//go:build linux

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func TestMaintenancePolicyReviewCannotRunAsOwnerUpdate(t *testing.T) {
	args := []string{"--action", "prepare", "--disposable-fixture", "--prepare-host", "--review-maintenance-policy"}
	if err := ownerUpdatesCommand(args, nil, io.Discard, io.Discard); err == nil {
		t.Fatal("read-only policy review reached owner update execution")
	}
	options, err := parseOwnerUpdateOptions(args, io.Discard)
	if err != nil || !options.ReviewHost || options.Action != "prepare" {
		t.Fatal("explicit policy review was not recognized", err)
	}
	if _, err = parseOwnerUpdateOptions([]string{"--action", "inventory", "--disposable-fixture", "--prepare-host", "--review-maintenance-policy"}, io.Discard); err == nil {
		t.Fatal("read-only inventory accepted setup review")
	}
}

func TestSignedMaintenanceTargetsRefuseChangedArtifacts(t *testing.T) {
	dir := t.TempDir()
	coreBytes, signerBytes := []byte("controlled core image"), []byte("controlled signer image")
	coreDigest, signerDigest := sha256.Sum256(coreBytes), sha256.Sum256(signerBytes)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	m := update.Manifest{Version: 1, Product: "fased-app", Channel: "development", Sequence: 1, IssuedAt: now - 1, ExpiresAt: now + 600, Target: update.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	for _, item := range []struct {
		contract update.Contract
		data     []byte
		digest   [32]byte
	}{{core.UpdateContract(), coreBytes, coreDigest}, {signer.UpdateContract(), signerBytes, signerDigest}} {
		m.Components = append(m.Components, update.Component{Version: "0.0.0-fixture.1", Contract: item.contract, Artifact: update.Artifact{File: item.contract.Component, Size: int64(len(item.data)), SHA256: hex.EncodeToString(item.digest[:])}})
		if err = os.WriteFile(filepath.Join(dir, item.contract.Component), item.data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	payload := mustJSON(m)
	manifest := mustJSON(update.Envelope{Version: 1, KeyID: update.KeyID(pub), Payload: payload, Signature: ed25519.Sign(key, append([]byte(update.SignatureDomain), payload...))})
	trust := mustJSON(update.Trust{Version: 1, Purpose: update.Purpose, Keys: []update.TrustKey{{ID: update.KeyID(pub), PublicKey: pub, NotBefore: now - 60, NotAfter: now + 3600}}})
	manifestPath, trustPath := filepath.Join(dir, "manifest.json"), filepath.Join(dir, "trust.json")
	for path, data := range map[string][]byte{manifestPath: manifest, trustPath: trust} {
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	o := ownerUpdateOptions{Manifest: manifestPath, Trust: trustPath, Artifacts: dir}
	c, s, err := signedMaintenanceTargets(o, now)
	if err != nil || c != hex.EncodeToString(coreDigest[:]) || s != hex.EncodeToString(signerDigest[:]) {
		t.Fatalf("signed candidate refused: %v", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "signer"), []byte("changed signer"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = signedMaintenanceTargets(o, now); err == nil {
		t.Fatal("changed signed component accepted")
	}
	if err = os.WriteFile(filepath.Join(dir, "signer"), signerBytes, 0600); err != nil {
		t.Fatal(err)
	}
	manifest[len(manifest)-2] ^= 1
	if err = os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = signedMaintenanceTargets(o, now); err == nil {
		t.Fatal("changed signed manifest accepted")
	}
}
