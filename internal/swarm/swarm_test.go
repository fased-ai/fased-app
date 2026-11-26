package swarm

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fased-ai/fased-app/internal/providersecrets"
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

type memSwarmSecretSource struct {
	key []byte
	err error
}

func (m *memSwarmSecretSource) Key(_ context.Context, _ string, _ bool) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([]byte, len(m.key))
	copy(out, m.key)
	return out, nil
}

func TestSwarmSeedOSVaultSealingAndMigration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := &memSwarmSecretSource{key: bytes.Repeat([]byte{0x5A}, 32)}

	svc, err := NewService(Config{DataDir: dir, SecretSource: src})
	if err != nil {
		t.Fatalf("NewService(os_vault) failed: %v", err)
	}
	st, err := svc.Status(context.Background())
	if err != nil || st.KeyStorage != "os_vault" {
		t.Fatalf("expected keyStorage=os_vault, got %+v err=%v", st, err)
	}
	_ = svc.Close()

	keyFile, err := os.ReadFile(filepath.Join(dir, "swarm", "swarm.key"))
	if err != nil || !bytes.HasPrefix(keyFile, []byte(sealedSwarmKeyMagic)) {
		t.Fatalf("expected sealed swarm.key prefix, got len=%d err=%v", len(keyFile), err)
	}

	// Unavailable secret source refuses to open sealed swarm seed cleanly.
	if _, err := NewService(Config{DataDir: dir, SecretSource: &memSwarmSecretSource{err: providersecrets.ErrUnavailable}}); err == nil {
		t.Fatal("expected sealed swarm.key without OS vault key to return error")
	}

	// Legacy raw 32-byte swarm.key migrates seamlessly preserving LocalPeerID and LocalPubKeyHex.
	legDir := t.TempDir()
	svcLeg, err := NewService(Config{DataDir: legDir})
	if err != nil {
		t.Fatalf("NewService(file_0600) failed: %v", err)
	}
	stLeg, _ := svcLeg.Status(context.Background())
	_ = svcLeg.Close()
	if stLeg.KeyStorage != "file_0600" {
		t.Fatalf("expected file_0600 on unsealed store, got %+v", stLeg)
	}

	svcMig, err := NewService(Config{DataDir: legDir, SecretSource: src})
	if err != nil {
		t.Fatalf("migrate swarm.key failed: %v", err)
	}
	defer svcMig.Close()
	stMig, _ := svcMig.Status(context.Background())
	if stMig.KeyStorage != "os_vault" || stMig.Config.LocalPeerID != stLeg.Config.LocalPeerID || stMig.Config.LocalPubKeyHex != stLeg.Config.LocalPubKeyHex {
		t.Fatalf("migration altered identity or storage: %+v vs %+v", stMig, stLeg)
	}
}

func TestSwarmPeerEndpointValidation(t *testing.T) {
	t.Parallel()
	valid := []struct {
		raw  string
		want string
	}{
		{"", ""},
		{"  ", ""},
		{"https://desk-alpha.tail1234.ts.net/", "https://desk-alpha.tail1234.ts.net"},
		{"https://100.100.1.2:8443/prefix/", "https://100.100.1.2:8443/prefix"},
		{"https://[fd7a:115c:a1e0::1]:8443", "https://[fd7a:115c:a1e0::1]:8443"},
		{"https://203.0.113.10:8443", "https://203.0.113.10:8443"},
	}
	for _, tc := range valid {
		got, err := ValidatePeerEndpoint(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("ValidatePeerEndpoint(%q) = (%q, %v), want (%q, nil)", tc.raw, got, err, tc.want)
		}
	}

	invalid := []string{
		"http://desk-alpha.tail1234.ts.net",
		"https://localhost:8443",
		"https://node.localhost",
		"https://127.0.0.1:8443",
		"https://169.254.169.254/latest/meta-data",
		"https://10.0.0.5:8443",
		"https://192.168.1.10:8443",
		"https://user:pass@desk.ts.net",
		"https://desk.ts.net?foo=bar",
	}
	for _, raw := range invalid {
		if got, err := ValidatePeerEndpoint(raw); err == nil {
			t.Fatalf("expected error for invalid endpoint %q, got %q", raw, got)
		}
	}
}

type relayRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn relayRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestSwarmPeerRelayAndChatTwoNode(t *testing.T) {
	t.Parallel()
	curNow := time.Unix(1795600000, 0).UTC()

	nodeB, err := NewService(Config{
		DataDir: t.TempDir(),
		Now:     func() time.Time { return curNow },
	})
	if err != nil {
		t.Fatalf("NewService(nodeB) failed: %v", err)
	}
	defer nodeB.Close()

	en := true
	if _, err := nodeB.SetConfig(context.Background(), ConfigPatch{Enabled: &en}); err != nil {
		t.Fatalf("enable nodeB failed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/swarm/relay" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var env Envelope
		if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&env); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ing, err := nodeB.IngestEnvelope(r.Context(), env)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true, "envelopeId": ing.EnvelopeID, "untrusted": ing.Untrusted})
	}))
	defer srv.Close()

	nodeA, err := NewService(Config{
		DataDir:     t.TempDir(),
		FixtureMode: false,
		Now:         func() time.Time { return curNow },
		RelayTransport: relayRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "node-b.tail1234.ts.net" {
				t.Fatalf("unexpected relay target host %q", r.URL.Host)
			}
			forwarded, err := http.NewRequestWithContext(r.Context(), r.Method, srv.URL+r.URL.Path, r.Body)
			if err != nil {
				return nil, err
			}
			forwarded.Header = r.Header.Clone()
			return srv.Client().Do(forwarded)
		}),
	})
	if err != nil {
		t.Fatalf("NewService(nodeA) failed: %v", err)
	}
	defer nodeA.Close()

	if _, err := nodeA.SetConfig(context.Background(), ConfigPatch{Enabled: &en}); err != nil {
		t.Fatalf("enable nodeA failed: %v", err)
	}
	stA, _ := nodeA.Status(context.Background())
	stB, _ := nodeB.Status(context.Background())

	// Mutual peer registration with Tailscale HTTPS endpoint on nodeA -> nodeB.
	if _, err := nodeB.AddPeer(context.Background(), PeerInput{
		PeerID:    stA.Config.LocalPeerID,
		Alias:     "Node A",
		PubKeyHex: stA.Config.LocalPubKeyHex,
	}); err != nil {
		t.Fatalf("nodeB.AddPeer failed: %v", err)
	}
	if _, err := nodeA.AddPeer(context.Background(), PeerInput{
		PeerID:    stB.Config.LocalPeerID,
		Alias:     "Node B",
		PubKeyHex: stB.Config.LocalPubKeyHex,
		Endpoint:  "https://node-b.tail1234.ts.net",
	}); err != nil {
		t.Fatalf("nodeA.AddPeer failed: %v", err)
	}

	// Broadcast both a structured signal and a chat message.
	sigEnv, err := nodeA.BroadcastSignal(context.Background(), SignalInput{
		Kind:       "signal",
		Symbol:     "SOL",
		Direction:  "LONG",
		Thesis:     "Breakout backed by spot delta",
		Confidence: 0.85,
	})
	if err != nil {
		t.Fatalf("BroadcastSignal(signal) failed: %v", err)
	}
	if len(sigEnv.RelayResults) != 1 || !sigEnv.RelayResults[0].Delivered {
		t.Fatalf("expected relay delivery to nodeB, got %+v", sigEnv.RelayResults)
	}

	chatEnv, err := nodeA.BroadcastSignal(context.Background(), SignalInput{
		Kind:   "chat",
		Thesis: "Desk sync: holding paper risk flat into CPI",
	})
	if err != nil {
		t.Fatalf("BroadcastSignal(chat) failed: %v", err)
	}
	if chatEnv.Kind != "chat" || chatEnv.Symbol != "GENERAL" || chatEnv.Direction != "NEUTRAL" || len(chatEnv.RelayResults) != 1 || !chatEnv.RelayResults[0].Delivered {
		t.Fatalf("unexpected relayed chat envelope: %+v", chatEnv)
	}

	received, err := nodeB.ListSignals(context.Background(), 10)
	if err != nil || len(received) != 2 {
		t.Fatalf("expected 2 envelopes on nodeB, got len=%d err=%v", len(received), err)
	}
	for _, env := range received {
		if !env.Untrusted {
			t.Fatalf("expected Untrusted=true on relayed envelope: %+v", env)
		}
	}

	// Replaying an already-delivered envelope onto nodeB fails closed.
	if _, err := nodeB.IngestEnvelope(context.Background(), chatEnv); !errors.Is(err, ErrReplayDetected) {
		t.Fatalf("expected ErrReplayDetected on replayed chat envelope, got %v", err)
	}
}

func TestSwarmPeersEndpointMigration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	swarmDir := filepath.Join(dir, swarmDirName)
	if err := os.MkdirAll(swarmDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(swarmDir, swarmDBName))
	if err != nil {
		t.Fatal(err)
	}
	// Simulate pre-R4B schema missing endpoint column.
	if _, err := db.Exec(`
		CREATE TABLE swarm_peers(
			peer_id TEXT PRIMARY KEY,
			alias TEXT NOT NULL,
			pubkey_hex TEXT NOT NULL,
			status TEXT NOT NULL,
			added_at INTEGER NOT NULL
		);
	`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_ = db.Close()

	svc, err := NewService(Config{DataDir: dir})
	if err != nil {
		t.Fatalf("NewService on legacy swarm_peers failed: %v", err)
	}
	defer svc.Close()

	pub, _, _ := ed25519.GenerateKey(nil)
	peer, err := svc.AddPeer(context.Background(), PeerInput{
		PeerID:    "peer-migrated",
		Alias:     "Migrated Peer",
		PubKeyHex: hex.EncodeToString(pub),
		Endpoint:  "https://migrated.tailnet.ts.net",
	})
	if err != nil || peer.Endpoint != "https://migrated.tailnet.ts.net" {
		t.Fatalf("AddPeer after migration failed: %+v err=%v", peer, err)
	}
}
