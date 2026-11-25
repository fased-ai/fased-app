package swarm

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
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
