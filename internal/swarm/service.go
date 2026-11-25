package swarm

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

const hardMaxCappedOrderMicroUSD uint64 = 250_000_000

// Config parameterizes the opt-in Stage 6 Fased Network Swarm service.
type Config struct {
	DataDir     string
	FixtureMode bool
	Now         func() time.Time
}

// Service manages opt-in Swarm configuration, Ed25519 peer registry,
// replay-protected A2A ingestion, and bounded event pub/sub.
type Service struct {
	store            *store
	bus              *Bus
	fixtureMode      bool
	now              func() time.Time
	mu               sync.RWMutex
	cappedEnabled    bool
	maxOrderMicroUSD uint64
}

// ConfigureMainnetCapped records opt-in small-cap ceiling metadata while keeping
// SwarmLiveCopyEnabled strictly false and Untrusted=true on all A2A payloads.
func (s *Service) ConfigureMainnetCapped(maxOrderMicroUSD uint64) error {
	if s == nil {
		return errors.New("swarm service closed")
	}
	if maxOrderMicroUSD == 0 || maxOrderMicroUSD > hardMaxCappedOrderMicroUSD {
		return errors.New("swarm mainnet-capped max order must be between 1 and 250000000 micro-USD ($250)")
	}
	s.mu.Lock()
	s.cappedEnabled = true
	s.maxOrderMicroUSD = maxOrderMicroUSD
	s.mu.Unlock()
	return nil
}

// NewService opens <dataDir>/swarm/swarm.sqlite with Swarm disabled (opt-in) by default.
func NewService(cfg Config) (*Service, error) {
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	st, err := openStore(cfg.DataDir, nowFn)
	if err != nil {
		return nil, err
	}
	svc := &Service{
		store:       st,
		bus:         NewBus(),
		fixtureMode: cfg.FixtureMode,
		now:         nowFn,
	}
	if cfg.FixtureMode {
		if err := svc.seedFixturePeers(context.Background()); err != nil {
			_ = st.close()
			return nil, err
		}
	}
	return svc, nil
}

// Close stops the Swarm bus and closes SQLite connections cleanly.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	if s.bus != nil {
		s.bus.Close()
	}
	if s.store != nil {
		return s.store.close()
	}
	return nil
}

// Subscribe listens for verified inbound/local Swarm events without blocking publishers.
func (s *Service) Subscribe() (<-chan Event, func()) {
	if s == nil || s.bus == nil {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	return s.bus.Subscribe()
}

func (s *Service) seedFixturePeers(ctx context.Context) error {
	peers, err := s.store.listPeers(ctx)
	if err != nil || len(peers) > 0 {
		return err
	}
	seed := sha256.Sum256([]byte("fased-swarm-fixture-peer:desk-alpha"))
	pub := ed25519.NewKeyFromSeed(seed[:]).Public().(ed25519.PublicKey)
	return s.store.savePeer(ctx, Peer{
		PeerID:    "peer-desk-alpha",
		Alias:     "Desk Alpha Observer",
		PubKeyHex: hex.EncodeToString(pub),
		Status:    "verified",
		AddedAt:   s.now().UTC().Unix(),
	})
}

// Status returns opt-in Swarm configuration, locks, peers, and recent signals.
func (s *Service) Status(ctx context.Context) (StatusView, error) {
	if s == nil || s.store == nil {
		return StatusView{}, errors.New("swarm service closed")
	}
	cfg, err := s.store.getConfig(ctx)
	if err != nil {
		return StatusView{}, err
	}
	peers, err := s.store.listPeers(ctx)
	if err != nil {
		return StatusView{}, err
	}
	signals, err := s.store.listSignals(ctx, 25)
	if err != nil {
		return StatusView{}, err
	}
	s.mu.RLock()
	capped, maxUSD := s.cappedEnabled, s.maxOrderMicroUSD
	s.mu.RUnlock()
	return StatusView{
		Config:           cfg,
		LiveCopyLocked:   !SwarmLiveCopyEnabled,
		CappedMode:       capped,
		MaxOrderMicroUSD: maxUSD,
		WENHeld:          true,
		PeerCount:        len(peers),
		SignalCount:      len(signals),
		Peers:            peers,
		RecentSignals:    signals,
		FixtureMode:      s.fixtureMode,
	}, nil
}

// SetConfig toggles opt-in Swarm activation or switches copy mode between shadow and paper.
func (s *Service) SetConfig(ctx context.Context, patch ConfigPatch) (SwarmConfig, error) {
	if s == nil || s.store == nil {
		return SwarmConfig{}, errors.New("swarm service closed")
	}
	cur, err := s.store.getConfig(ctx)
	if err != nil {
		return SwarmConfig{}, err
	}
	enabled := cur.Enabled
	if patch.Enabled != nil {
		enabled = *patch.Enabled
	}
	copyMode := cur.CopyMode
	if patch.CopyMode != nil {
		validMode, mErr := ValidateCopyMode(*patch.CopyMode)
		if mErr != nil {
			return SwarmConfig{}, mErr
		}
		copyMode = validMode
	}
	return s.store.setConfig(ctx, enabled, copyMode)
}

// AddPeer validates and stores a trusted Ed25519 Swarm peer public key.
func (s *Service) AddPeer(ctx context.Context, in PeerInput) (Peer, error) {
	if s == nil || s.store == nil {
		return Peer{}, errors.New("swarm service closed")
	}
	peerID := strings.TrimSpace(in.PeerID)
	if !idPattern.MatchString(peerID) {
		return Peer{}, errors.New("invalid swarm peerId")
	}
	alias := strings.TrimSpace(in.Alias)
	if len(alias) == 0 || len(alias) > 64 {
		return Peer{}, errors.New("swarm peer alias must be 1..64 characters")
	}
	pubHex := strings.ToLower(strings.TrimSpace(in.PubKeyHex))
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return Peer{}, errors.New("swarm peer pubKeyHex must be 32-byte Ed25519 public key hex")
	}
	peer := Peer{
		PeerID:    peerID,
		Alias:     alias,
		PubKeyHex: pubHex,
		Status:    "verified",
		AddedAt:   s.now().UTC().Unix(),
	}
	if err := s.store.savePeer(ctx, peer); err != nil {
		return Peer{}, err
	}
	return peer, nil
}

// ListPeers returns registered Ed25519 Swarm peers.
func (s *Service) ListPeers(ctx context.Context) ([]Peer, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("swarm service closed")
	}
	return s.store.listPeers(ctx)
}

// ListSignals returns recent verified A2A envelopes (always marked untrusted=true).
func (s *Service) ListSignals(ctx context.Context, limit int) ([]Envelope, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("swarm service closed")
	}
	return s.store.listSignals(ctx, limit)
}

// IngestEnvelope verifies opt-in enablement, timestamp skew (<= 5m), Ed25519 signature,
// replay nonce uniqueness, forces Untrusted=true, stores, and broadcasts the event.
func (s *Service) IngestEnvelope(ctx context.Context, env Envelope) (Envelope, error) {
	if s == nil || s.store == nil {
		return Envelope{}, errors.New("swarm service closed")
	}
	cfg, err := s.store.getConfig(ctx)
	if err != nil {
		return Envelope{}, err
	}
	if !cfg.Enabled {
		return Envelope{}, ErrSwarmDisabled
	}
	now := s.now().UTC()
	if err := VerifyEnvelope(&env, now); err != nil {
		return Envelope{}, err
	}
	localPubHex := hex.EncodeToString(s.store.localKey().Public().(ed25519.PublicKey))
	if !strings.EqualFold(env.SenderPubKeyHex, localPubHex) {
		known, err := s.store.hasPeerPubKey(ctx, strings.ToLower(env.SenderPubKeyHex))
		if err != nil {
			return Envelope{}, err
		}
		if !known {
			return Envelope{}, ErrUnknownPeer
		}
	}
	if err := s.store.recordEnvelopeIfFreshNonce(ctx, env); err != nil {
		return Envelope{}, err
	}
	if s.bus != nil {
		s.bus.Publish(Event{Type: "swarm.signal", Timestamp: env.VerifiedAt, Envelope: env})
	}
	return env, nil
}

// BroadcastSignal signs a local signal with the workspace Ed25519 identity and ingests it into Swarm.
func (s *Service) BroadcastSignal(ctx context.Context, in SignalInput) (Envelope, error) {
	if s == nil || s.store == nil {
		return Envelope{}, errors.New("swarm service closed")
	}
	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	now := s.now().UTC()
	env := Envelope{
		EnvelopeID: "sig-" + hex.EncodeToString(rnd[:6]),
		Nonce:      hex.EncodeToString(rnd[:]),
		Kind:       "signal",
		Symbol:     in.Symbol,
		Direction:  in.Direction,
		Thesis:     in.Thesis,
		Confidence: in.Confidence,
		Timestamp:  now.Unix(),
		Untrusted:  true,
	}
	if err := SignEnvelope(s.store.localKey(), &env); err != nil {
		return Envelope{}, err
	}
	return s.IngestEnvelope(ctx, env)
}
