package swarm

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSwarmOptInReplaySkewAndLiveLock(t *testing.T) {
	t.Parallel()
	if SwarmLiveCopyEnabled {
		t.Fatal("SwarmLiveCopyEnabled must remain false")
	}
	curNow := time.Unix(1795600000, 0).UTC()
	svc, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: true,
		Now:         func() time.Time { return curNow },
	})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	// Default disabled (opt-in required).
	st, err := svc.Status(context.Background())
	if err != nil || st.Config.Enabled || !st.LiveCopyLocked || !st.WENHeld || st.Config.CopyMode != CopyModeShadow {
		t.Fatalf("unexpected default swarm status: %+v err=%v", st, err)
	}
	if _, err := svc.BroadcastSignal(context.Background(), SignalInput{
		Symbol:     "BTC",
		Direction:  "LONG",
		Thesis:     "Should fail while disabled",
		Confidence: 0.7,
	}); !errors.Is(err, ErrSwarmDisabled) {
		t.Fatalf("expected ErrSwarmDisabled while opt-in false, got %v", err)
	}

	// Copy mode "live" fails closed.
	liveStr := "live"
	if _, err := svc.SetConfig(context.Background(), ConfigPatch{CopyMode: &liveStr}); !errors.Is(err, ErrLiveCopyDisabled) {
		t.Fatalf("expected ErrLiveCopyDisabled for live copy mode, got %v", err)
	}

	// Enable Swarm in paper mode.
	en := true
	paper := CopyModePaper
	cfg, err := svc.SetConfig(context.Background(), ConfigPatch{Enabled: &en, CopyMode: &paper})
	if err != nil || !cfg.Enabled || cfg.CopyMode != CopyModePaper {
		t.Fatalf("SetConfig failed: %+v err=%v", cfg, err)
	}

	// Subscribe to bus events and verify BroadcastSignal emits untrusted=true envelope.
	ch, unsub := svc.Subscribe()
	defer unsub()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPeer(context.Background(), PeerInput{
		PeerID:    "peer-signer-one",
		Alias:     "Signal Peer 1",
		PubKeyHex: hex.EncodeToString(pub),
	}); err != nil {
		t.Fatalf("AddPeer failed: %v", err)
	}

	// Skew > 5m rejected.
	skewEnv := Envelope{
		EnvelopeID: "sig-skew-1",
		Nonce:      "nonce-skew-0001",
		Kind:       "signal",
		Symbol:     "ETH",
		Direction:  "LONG",
		Thesis:     "Momentum breakout",
		Confidence: 0.75,
		Timestamp:  curNow.Add(-10 * time.Minute).Unix(),
	}
	if err := SignEnvelope(priv, &skewEnv); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestEnvelope(context.Background(), skewEnv); !errors.Is(err, ErrTimestampSkew) {
		t.Fatalf("expected ErrTimestampSkew, got %v", err)
	}

	// Unregistered signer key fails closed with ErrUnknownPeer.
	_, unknownPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	unknownEnv := Envelope{
		EnvelopeID: "sig-unknown-1",
		Nonce:      "nonce-unknown-0001",
		Kind:       "signal",
		Symbol:     "BTC",
		Direction:  "LONG",
		Thesis:     "Unregistered peer should be rejected",
		Confidence: 0.7,
		Timestamp:  curNow.Unix(),
	}
	if err := SignEnvelope(unknownPriv, &unknownEnv); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestEnvelope(context.Background(), unknownEnv); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("expected ErrUnknownPeer for unregistered sender key, got %v", err)
	}

	// Fresh signed envelope succeeds and forces Untrusted=true.
	goodEnv := Envelope{
		EnvelopeID: "sig-fresh-1",
		Nonce:      "nonce-fresh-0001",
		Kind:       "signal",
		Symbol:     "SOL",
		Direction:  "LONG",
		Thesis:     "Spot volume expansion",
		Confidence: 0.82,
		Timestamp:  curNow.Unix(),
		Untrusted:  false,
	}
	if err := SignEnvelope(priv, &goodEnv); err != nil {
		t.Fatal(err)
	}
	ingested, err := svc.IngestEnvelope(context.Background(), goodEnv)
	if err != nil || !ingested.Untrusted {
		t.Fatalf("IngestEnvelope failed or untrusted flag false: %+v err=%v", ingested, err)
	}

	// Replay with identical nonce fails closed.
	if _, err := svc.IngestEnvelope(context.Background(), goodEnv); !errors.Is(err, ErrReplayDetected) {
		t.Fatalf("expected ErrReplayDetected on duplicate nonce, got %v", err)
	}

	select {
	case evt := <-ch:
		if !evt.Envelope.Untrusted || evt.Envelope.EnvelopeID != "sig-fresh-1" {
			t.Fatalf("unexpected bus event: %+v", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Swarm bus event")
	}
}

func TestSwarmMainnetCappedKeepsLiveCopyLockedAndWENHeld(t *testing.T) {
	svc, err := NewService(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	defer svc.Close()

	if err := svc.ConfigureMainnetCapped(250_000_001); err == nil {
		t.Fatal("expected >$250 max order to fail")
	}
	if err := svc.ConfigureMainnetCapped(75_000_000); err != nil {
		t.Fatalf("ConfigureMainnetCapped($75) failed: %v", err)
	}
	st, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !st.LiveCopyLocked || !st.WENHeld || !st.CappedMode || st.MaxOrderMicroUSD != 75_000_000 {
		t.Fatalf("unexpected swarm capped status: %+v", st)
	}
}

func TestSwarmSeedMovedOutOfSQLiteAndPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	svc, err := NewService(Config{DataDir: dir})
	if err != nil {
		t.Fatalf("NewService failed: %v", err)
	}
	st1, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	_ = svc.Close()

	keyPath := filepath.Join(dir, "swarm", "swarm.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("expected swarm.key file at %s: %v", keyPath, err)
	}
	if info.Size() != ed25519.SeedSize {
		t.Fatalf("expected 32-byte swarm.key, got %d", info.Size())
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("expected swarm.key mode 0600, got %v", info.Mode().Perm())
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "swarm", "swarm.sqlite"))
	if err != nil {
		t.Fatalf("open swarm.sqlite failed: %v", err)
	}
	var dbSeed []byte
	if err := db.QueryRow(`SELECT local_seed FROM swarm_config WHERE id = 1`).Scan(&dbSeed); err != nil {
		_ = db.Close()
		t.Fatalf("scan local_seed failed: %v", err)
	}
	_ = db.Close()
	if len(dbSeed) != 0 {
		t.Fatalf("expected empty local_seed in swarm.sqlite, got %d bytes", len(dbSeed))
	}

	svc2, err := NewService(Config{DataDir: dir})
	if err != nil {
		t.Fatalf("reopen NewService failed: %v", err)
	}
	defer svc2.Close()
	st2, err := svc2.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after reopen failed: %v", err)
	}
	if st2.Config.LocalPeerID != st1.Config.LocalPeerID || st2.Config.LocalPubKeyHex != st1.Config.LocalPubKeyHex {
		t.Fatalf("identity drifted across reopen: %+v vs %+v", st1.Config, st2.Config)
	}
}
