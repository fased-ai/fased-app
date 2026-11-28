package custody

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/providersecrets"
	"github.com/fased-ai/fased-app/internal/vault"
	_ "modernc.org/sqlite"
)

const (
	vaultDirName    = "signer-vault"
	custodyDBName   = "custody.sqlite"
	masterKeyName   = "signer-master.key"
	masterKeyBytes  = 32
	recoveryFormat  = "fased-recovery/1"
	recoveryMinPass = 12
)

// Config configures the isolated custody signer vault.
type Config struct {
	DataDir            string
	FixtureMode        bool
	FastKDF            bool
	RequireOSKEK       bool
	RequireOwnerAuth   bool
	AllowLoopbackRPC   bool
	KEKSource          providersecrets.Source // Optional override in tests
	SocketPath         string                 // Optional explicit socket path reported in status
	RPCTransport       http.RoundTripper      // Tests only
	ManifestVerifyKeys map[string]string      // Optional test-only manifest trust key overrides
	Now                func() time.Time
}

// Service owns private wallet keys, encrypted execution RPCs, fail-closed policies,
// immutable transaction reviews, and the durable broadcast ledger.
type Service struct {
	dir               string
	db                *sql.DB
	masterKey         []byte
	auditHMACKey      []byte
	kekProtection     string
	cfg               Config
	now               func() time.Time
	mu                sync.Mutex
	proofMu           sync.Mutex
	ownerProofs       map[string]ownerProofEntry
	passkeyChallenges map[string]passkeyChallengeEntry
	kdfSlots          chan struct{}
	authFailMu        sync.Mutex
	authFailTimes     []time.Time
	authConsecFails   int
	authLockUntil     time.Time
	bpfVerifiedMu     sync.Mutex
	bpfVerified       map[string]int64
	closed            bool
	kdfTime           uint32
	kdfMemory         uint32
	kdfThreads        uint8
}

func sqliteURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	escaped := strings.ReplaceAll(slashPath, "?", "%3F")
	escaped = strings.ReplaceAll(escaped, "#", "%23")
	return "file://" + escaped
}

func randomHexID(prefix string, nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(b), nil
}

// NewService opens <dataDir>/signer-vault/ with mode 0o700 dir and 0o600 key/SQLite files.
func NewService(cfg Config) (*Service, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("custody DataDir required")
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	dir := filepath.Join(cfg.DataDir, vaultDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}

	ctxInit, cancelInit := context.WithTimeout(context.Background(), 5*time.Second)
	masterKey, kekProt, err := loadOrCreateMasterKeyWithKEK(ctxInit, dir, cfg)
	cancelInit()
	if err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, custodyDBName)
	dsn := sqliteURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		clear(masterKey)
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schema = `
CREATE TABLE IF NOT EXISTS wallets (
	id TEXT PRIMARY KEY,
	handle TEXT UNIQUE NOT NULL,
	name TEXT NOT NULL,
	custody TEXT NOT NULL,
	role TEXT NOT NULL,
	public_key TEXT UNIQUE NOT NULL,
	policy_json TEXT NOT NULL,
	policy_acknowledged INTEGER NOT NULL DEFAULT 1,
	connections_json TEXT NOT NULL,
	rpc_status_json TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS wallet_keys (
	wallet_id TEXT PRIMARY KEY REFERENCES wallets(id) ON DELETE CASCADE,
	key_nonce BLOB,
	key_ciphertext BLOB,
	rpc_nonce BLOB NOT NULL,
	rpc_ciphertext BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS reviews (
	id TEXT PRIMARY KEY,
	wallet_id TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
	status TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_reviews_wallet ON reviews (wallet_id, created_at DESC);

CREATE TABLE IF NOT EXISTS ledger (
	id TEXT PRIMARY KEY,
	review_id TEXT NOT NULL,
	wallet_id TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
	state TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ledger_wallet ON ledger (wallet_id, created_at DESC);

CREATE TABLE IF NOT EXISTS daily_usage (
	wallet_id TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
	day_utc TEXT NOT NULL,
	spent_raw INTEGER NOT NULL DEFAULT 0,
	reserved_raw INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (wallet_id, day_utc)
);

CREATE TABLE IF NOT EXISTS daily_asset_usage (
	wallet_id TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
	asset_key TEXT NOT NULL,
	day_utc TEXT NOT NULL,
	spent_raw INTEGER NOT NULL DEFAULT 0,
	reserved_raw INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (wallet_id, asset_key, day_utc)
);

CREATE TABLE IF NOT EXISTS mining_round_usage (
	wallet_id TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
	cluster TEXT NOT NULL,
	round_slot INTEGER NOT NULL,
	spent_lamports INTEGER NOT NULL DEFAULT 0,
	reserved_lamports INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (wallet_id, cluster, round_slot)
);

CREATE TABLE IF NOT EXISTS signer_meta (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	owner_pw_salt BLOB,
	owner_pw_hash BLOB,
	owner_pw_time INTEGER NOT NULL DEFAULT 0,
	owner_pw_memory INTEGER NOT NULL DEFAULT 0,
	owner_pw_threads INTEGER NOT NULL DEFAULT 0,
	audit_hmac_nonce BLOB,
	audit_hmac_ciphertext BLOB,
	audit_head_hash TEXT NOT NULL DEFAULT '',
	audit_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS owner_passkeys (
	credential_id TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	rp_id TEXT NOT NULL,
	alg INTEGER NOT NULL,
	pub_key_json TEXT NOT NULL,
	sign_count INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	last_used_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS audit_log (
	id TEXT PRIMARY KEY,
	wallet_id TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	timestamp INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_wallet ON audit_log (wallet_id, timestamp DESC);
`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		clear(masterKey)
		return nil, err
	}
	if err := os.Chmod(dbPath, 0o600); err != nil && !os.IsNotExist(err) {
		_ = db.Close()
		clear(masterKey)
		return nil, err
	}

	kdfT, kdfM, kdfP := vault.ProdKDFTime, vault.ProdKDFMemory, vault.ProdKDFThreads
	if cfg.FastKDF {
		kdfT, kdfM, kdfP = 1, 64, 1
	}
	svc := &Service{
		dir:               dir,
		db:                db,
		masterKey:         masterKey,
		kekProtection:     kekProt,
		cfg:               cfg,
		now:               nowFn,
		ownerProofs:       make(map[string]ownerProofEntry),
		passkeyChallenges: make(map[string]passkeyChallengeEntry),
		bpfVerified:       make(map[string]int64),
		kdfSlots:          make(chan struct{}, 2),
		kdfTime:           kdfT,
		kdfMemory:         kdfM,
		kdfThreads:        kdfP,
	}
	if err := svc.initSignerMetaLocked(ctx); err != nil {
		_ = svc.Close()
		return nil, err
	}
	return svc, nil
}

func (s *Service) initSignerMetaLocked(ctx context.Context) error {
	// Idempotent schema migration for pre-Wave-S3/S5/S6 custody vaults.
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN audit_head_hash TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN audit_count INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN audit_anchor_mac TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN global_freeze INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN global_freeze_reason TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN global_freeze_by TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN global_freeze_at INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN lv1x_manifest_json TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN lv1x_manifest_seq INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE signer_meta ADD COLUMN lv1x_manifest_verified_at INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mining_round_usage (
		wallet_id TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
		cluster TEXT NOT NULL,
		round_slot INTEGER NOT NULL,
		spent_lamports INTEGER NOT NULL DEFAULT 0,
		reserved_lamports INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (wallet_id, cluster, round_slot)
	)`)
	_, _ = s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mining_fallback_gas (
		wallet_id TEXT NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
		day_utc TEXT NOT NULL,
		spent_lamports INTEGER NOT NULL DEFAULT 0,
		reserved_lamports INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (wallet_id, day_utc)
	)`)
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO signer_meta (id) VALUES (1)`); err != nil {
		return err
	}
	// Backfill anchored audit head/count for existing vaults upgraded from pre-Wave-S3.
	var anchoredCount int
	var anchoredHead string
	if err := s.db.QueryRowContext(ctx, `SELECT audit_count, audit_head_hash FROM signer_meta WHERE id = 1`).Scan(&anchoredCount, &anchoredHead); err == nil && anchoredCount == 0 && anchoredHead == "" {
		var actualCount int
		if s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM audit_log`).Scan(&actualCount) == nil && actualCount > 0 {
			var lastRaw string
			if s.db.QueryRowContext(ctx, `SELECT payload_json FROM audit_log ORDER BY rowid DESC LIMIT 1`).Scan(&lastRaw) == nil {
				var last AuditEntry
				if json.Unmarshal([]byte(lastRaw), &last) == nil && last.EntryHash != "" {
					_, _ = s.db.ExecContext(ctx, `UPDATE signer_meta SET audit_head_hash = ?, audit_count = ? WHERE id = 1`, last.EntryHash, actualCount)
				}
			}
		}
	}
	var nonce, ct []byte
	if err := s.db.QueryRowContext(ctx, `SELECT audit_hmac_nonce, audit_hmac_ciphertext FROM signer_meta WHERE id = 1`).Scan(&nonce, &ct); err != nil {
		return err
	}
	if len(nonce) == 12 && len(ct) > 0 {
		key, err := s.openBytes("fased-audit-hmac-key:v1", nonce, ct)
		if err != nil || len(key) != 32 {
			return errors.New("corrupt sealed audit HMAC key")
		}
		s.auditHMACKey = key
		return nil
	}
	key, err := vault.RandomBytes(32)
	if err != nil {
		return err
	}
	n, c, err := s.sealBytes("fased-audit-hmac-key:v1", key)
	if err != nil {
		clear(key)
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE signer_meta SET audit_hmac_nonce = ?, audit_hmac_ciphertext = ? WHERE id = 1`, n, c); err != nil {
		clear(key)
		return err
	}
	s.auditHMACKey = key
	return nil
}

func (s *Service) isGlobalFrozenLocked(ctx context.Context) (bool, string) {
	if s.db == nil {
		return true, "custody store closed"
	}
	var active int
	var reason string
	if err := s.db.QueryRowContext(ctx, `SELECT global_freeze, global_freeze_reason FROM signer_meta WHERE id = 1`).Scan(&active, &reason); err != nil {
		return false, ""
	}
	return active != 0, reason
}

// GetGlobalFreeze reports the signer-authoritative global emergency freeze latch.
func (s *Service) GetGlobalFreeze(ctx context.Context) (GlobalFreezeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return GlobalFreezeState{}, errors.New("custody store closed")
	}
	var active int
	var st GlobalFreezeState
	if err := s.db.QueryRowContext(ctx, `SELECT global_freeze, global_freeze_reason, global_freeze_by, global_freeze_at FROM signer_meta WHERE id = 1`).Scan(&active, &st.Reason, &st.UpdatedBy, &st.UpdatedAt); err != nil {
		return GlobalFreezeState{}, err
	}
	st.Active = active != 0
	return st, nil
}

// SetGlobalFreeze latches or clears the signer-authoritative global emergency freeze.
func (s *Service) SetGlobalFreeze(ctx context.Context, active bool, reason, actor string) (GlobalFreezeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return GlobalFreezeState{}, errors.New("custody store closed")
	}
	nowSec := s.now().UTC().Unix()
	cleanReason := strings.TrimSpace(reason)
	if active && cleanReason == "" {
		cleanReason = "Global emergency freeze"
	}
	cleanActor := firstStr(actor, "owner")
	flag := 0
	if active {
		flag = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE signer_meta SET global_freeze = ?, global_freeze_reason = ?, global_freeze_by = ?, global_freeze_at = ? WHERE id = 1`,
		flag, cleanReason, cleanActor, nowSec,
	); err != nil {
		return GlobalFreezeState{}, err
	}
	action := "global_freeze_cleared"
	if active {
		action = "global_freeze_activated"
	}
	if err := s.appendAuditLocked(ctx, "global", action, cleanActor, cleanReason, ""); err != nil {
		return GlobalFreezeState{}, err
	}
	return GlobalFreezeState{
		Active:    active,
		Reason:    cleanReason,
		UpdatedBy: cleanActor,
		UpdatedAt: nowSec,
	}, nil
}

// SecurityStatus reports KEK protection and signer process isolation posture.
func (s *Service) SecurityStatus(ctx context.Context) (SecurityStatus, error) {
	hasOwner, _ := s.HasOwnerAuth(ctx)
	passkeys, _ := s.ListOwnerPasskeys(ctx)
	return SecurityStatus{
		ProcessIsolated:   false,
		KEKProtection:     firstStr(s.kekProtection, KEKFileDevOnly),
		OwnerAuthEnforced: hasOwner || len(passkeys) > 0 || s.cfg.RequireOwnerAuth,
		PasskeyCount:      len(passkeys),
		PasskeyEnabled:    len(passkeys) > 0,
		SocketPath:        s.cfg.SocketPath,
		AllowedPeerUID:    os.Getuid(),
	}, nil
}

// SetFastKDFForTests lowers Argon2id parameters strictly inside automated tests.
func (s *Service) SetFastKDFForTests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kdfTime = 1
	s.kdfMemory = 64
	s.kdfThreads = 1
}

// Dir returns the isolated signer directory path.
func (s *Service) Dir() string {
	return s.dir
}

// Close closes SQLite and clears the in-memory master key.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	clear(s.masterKey)
	clear(s.auditHMACKey)
	s.proofMu.Lock()
	clear(s.ownerProofs)
	clear(s.passkeyChallenges)
	s.proofMu.Unlock()
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

type sealedRPCPayload struct {
	PrimaryURL  string `json:"primaryUrl"`
	FallbackURL string `json:"fallbackUrl,omitempty"`
}

func (s *Service) sealBytes(aadLabel string, plain []byte) (nonce, ct []byte, err error) {
	nonce, err = vault.RandomBytes(12)
	if err != nil {
		return nil, nil, err
	}
	ct, err = vault.SealAESGCM(s.masterKey, nonce, plain, []byte(aadLabel))
	if err != nil {
		return nil, nil, err
	}
	return nonce, ct, nil
}

func (s *Service) openBytes(aadLabel string, nonce, ct []byte) ([]byte, error) {
	return vault.OpenAESGCM(s.masterKey, nonce, ct, []byte(aadLabel))
}

func (s *Service) dayUTC() string {
	return s.now().UTC().Format("2006-01-02")
}

type sqlExecQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func computeAuditEntryHashV1(prevHash string, entry AuditEntry) string {
	canon := AuditEntry{
		ID:         entry.ID,
		WalletID:   entry.WalletID,
		Action:     entry.Action,
		Actor:      entry.Actor,
		Summary:    entry.Summary,
		PolicyHash: entry.PolicyHash,
		Timestamp:  entry.Timestamp,
		PrevHash:   prevHash,
	}
	raw, _ := json.Marshal(canon)
	sum := sha256.Sum256(append([]byte("fased-audit-chain:v1:"+prevHash+"\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

func computeAuditEntryHashV2(hmacKey []byte, prevHash string, entry AuditEntry) string {
	canon := AuditEntry{
		ID:         entry.ID,
		WalletID:   entry.WalletID,
		Action:     entry.Action,
		Actor:      entry.Actor,
		Summary:    entry.Summary,
		PolicyHash: entry.PolicyHash,
		Timestamp:  entry.Timestamp,
		PrevHash:   prevHash,
	}
	raw, _ := json.Marshal(canon)
	mac := hmac.New(sha256.New, hmacKey)
	_, _ = mac.Write([]byte("fased-audit-chain:v2:" + prevHash + "\x00"))
	_, _ = mac.Write(raw)
	return "v2:" + hex.EncodeToString(mac.Sum(nil))
}

func computeAuditAnchorMAC(hmacKey []byte, count int, headHash string) string {
	mac := hmac.New(sha256.New, hmacKey)
	_, _ = mac.Write([]byte(fmt.Sprintf("fased-audit-anchor:v1:%d:%s", count, headHash)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) appendAuditExecLocked(ctx context.Context, q sqlExecQuerier, walletID, action, actor, summary, policyHash string) error {
	id, err := randomHexID("aud", 8)
	if err != nil {
		return err
	}
	var prevHash string
	var prevRaw string
	if err := q.QueryRowContext(ctx, `SELECT payload_json FROM audit_log ORDER BY rowid DESC LIMIT 1`).Scan(&prevRaw); err == nil && prevRaw != "" {
		var prev AuditEntry
		if json.Unmarshal([]byte(prevRaw), &prev) == nil && prev.EntryHash != "" {
			prevHash = prev.EntryHash
		}
	}
	entry := AuditEntry{
		ID:         id,
		WalletID:   walletID,
		Action:     action,
		Actor:      actor,
		Summary:    summary,
		PolicyHash: policyHash,
		Timestamp:  s.now().UTC().Unix(),
		PrevHash:   prevHash,
	}
	if len(s.auditHMACKey) == 32 {
		entry.EntryHash = computeAuditEntryHashV2(s.auditHMACKey, prevHash, entry)
	} else {
		entry.EntryHash = computeAuditEntryHashV1(prevHash, entry)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO audit_log(id, wallet_id, payload_json, timestamp) VALUES (?, ?, ?, ?)`,
		entry.ID, entry.WalletID, string(raw), entry.Timestamp,
	); err != nil {
		return err
	}
	var curCount int
	_ = q.QueryRowContext(ctx, `SELECT audit_count FROM signer_meta WHERE id = 1`).Scan(&curCount)
	nextCount := curCount + 1
	anchorMAC := ""
	if len(s.auditHMACKey) == 32 {
		anchorMAC = computeAuditAnchorMAC(s.auditHMACKey, nextCount, entry.EntryHash)
	}
	_, err = q.ExecContext(ctx,
		`UPDATE signer_meta SET audit_head_hash = ?, audit_count = ?, audit_anchor_mac = ? WHERE id = 1`,
		entry.EntryHash, nextCount, anchorMAC,
	)
	return err
}

func (s *Service) appendAuditLocked(ctx context.Context, walletID, action, actor, summary, policyHash string) error {
	return s.appendAuditExecLocked(ctx, s.db, walletID, action, actor, summary, policyHash)
}

// VerifyAuditChain walks the tamper-evident audit log in insertion order, verifies every link
// (HMAC-SHA256 v2 keyed by signer-sealed audit key, forbidding v1 downgrade after v2), and
// verifies the final head hash, row count, and keyed HMAC anchor against signer_meta to detect truncation.
func (s *Service) VerifyAuditChain(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return 0, errors.New("custody store closed")
	}
	var expectedHead string
	var expectedCount int
	var anchorMAC string
	if err := s.db.QueryRowContext(ctx, `SELECT audit_head_hash, audit_count, audit_anchor_mac FROM signer_meta WHERE id = 1`).Scan(&expectedHead, &expectedCount, &anchorMAC); err != nil {
		return 0, fmt.Errorf("read audit anchor: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM audit_log ORDER BY rowid ASC`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prevHash := ""
	count := 0
	seenV2 := false
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return count, err
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return count, fmt.Errorf("corrupt audit entry at index %d: %w", count, err)
		}
		if e.PrevHash != prevHash {
			return count, fmt.Errorf("audit chain broken at %s: prevHash mismatch", e.ID)
		}
		var expected string
		if strings.HasPrefix(e.EntryHash, "v2:") {
			if len(s.auditHMACKey) != 32 {
				return count, fmt.Errorf("audit entry %s requires HMAC key", e.ID)
			}
			expected = computeAuditEntryHashV2(s.auditHMACKey, prevHash, e)
			seenV2 = true
		} else {
			if seenV2 {
				return count, fmt.Errorf("audit entry %s downgraded to unkeyed v1 hash", e.ID)
			}
			expected = computeAuditEntryHashV1(prevHash, e)
		}
		if !hmac.Equal([]byte(e.EntryHash), []byte(expected)) {
			return count, fmt.Errorf("audit entry %s hash tampered", e.ID)
		}
		prevHash = e.EntryHash
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	if count != expectedCount || !hmac.Equal([]byte(prevHash), []byte(expectedHead)) {
		return count, fmt.Errorf("audit chain truncated or head mismatch (got count=%d, want count=%d)", count, expectedCount)
	}
	if anchorMAC != "" && len(s.auditHMACKey) == 32 {
		wantAnchor := computeAuditAnchorMAC(s.auditHMACKey, expectedCount, expectedHead)
		if !hmac.Equal([]byte(anchorMAC), []byte(wantAnchor)) {
			return count, errors.New("audit anchor HMAC tampered")
		}
	}
	return count, nil
}

func (s *Service) getWalletLocked(ctx context.Context, idOrHandle string) (WalletPublic, error) {
	clean := strings.TrimSpace(idOrHandle)
	cleanID := strings.TrimPrefix(clean, "@wallet:")
	var (
		w      WalletPublic
		polRaw string
		ackInt int
		conRaw string
		rpcRaw string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, handle, name, custody, role, public_key, policy_json, policy_acknowledged, connections_json, rpc_status_json, created_at, updated_at
FROM wallets WHERE id = ? OR handle = ?`, cleanID, clean).Scan(
		&w.ID, &w.Handle, &w.Name, &w.Custody, &w.Role, &w.PublicKey,
		&polRaw, &ackInt, &conRaw, &rpcRaw, &w.CreatedAt, &w.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return WalletPublic{}, ErrWalletNotFound
	}
	if err != nil {
		return WalletPublic{}, err
	}
	w.PolicyAcknowledged = ackInt != 0
	_ = json.Unmarshal([]byte(polRaw), &w.Policy)
	_ = json.Unmarshal([]byte(conRaw), &w.Connections)
	_ = json.Unmarshal([]byte(rpcRaw), &w.RPC)

	day := s.dayUTC()
	w.DayUTC = day
	_ = s.db.QueryRowContext(ctx, `SELECT spent_raw, reserved_raw FROM daily_usage WHERE wallet_id = ? AND day_utc = ?`, w.ID, day).
		Scan(&w.DailySpentLamports, &w.DailyReservedLamports)
	if tRows, tErr := s.db.QueryContext(ctx, `SELECT asset_key, spent_raw, reserved_raw FROM daily_asset_usage WHERE wallet_id = ? AND day_utc = ?`, w.ID, day); tErr == nil {
		for tRows.Next() {
			var aKey string
			var sp, res uint64
			if tRows.Scan(&aKey, &sp, &res) == nil && strings.HasPrefix(aKey, "spl:") {
				if w.DailyTokenUsage == nil {
					w.DailyTokenUsage = make(map[string]TokenDailyUsage)
				}
				w.DailyTokenUsage[strings.TrimPrefix(aKey, "spl:")] = TokenDailyUsage{
					SpentRaw:    sp,
					ReservedRaw: res,
				}
			}
		}
		_ = tRows.Close()
	}
	return w, nil
}

// ListWallets returns all public wallet metadata with daily signer usage.
func (s *Service) ListWallets(ctx context.Context) ([]WalletPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return nil, errors.New("custody store closed")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM wallets ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if sErr := rows.Scan(&id); sErr == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	out := make([]WalletPublic, 0, len(ids))
	for _, id := range ids {
		w, gErr := s.getWalletLocked(ctx, id)
		if gErr == nil {
			out = append(out, w)
		}
	}
	return out, nil
}

// GetWallet returns public wallet metadata for id or @wallet:<id>.
func (s *Service) GetWallet(ctx context.Context, idOrHandle string) (WalletPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletPublic{}, errors.New("custody store closed")
	}
	return s.getWalletLocked(ctx, idOrHandle)
}

// GetReview returns an immutable prepared transaction review for walletID/reviewID.
func (s *Service) GetReview(ctx context.Context, walletID, reviewID string) (WalletReview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletReview{}, errors.New("custody store closed")
	}
	w, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		return WalletReview{}, err
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM reviews WHERE id = ? AND wallet_id = ?`, strings.TrimSpace(reviewID), w.ID).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return WalletReview{}, ErrReviewNotFound
	} else if err != nil {
		return WalletReview{}, err
	}
	var rev WalletReview
	if err := json.Unmarshal([]byte(raw), &rev); err != nil {
		return WalletReview{}, err
	}
	return rev, nil
}

func (s *Service) allowLoopbackRPC() bool {
	return s.cfg.FixtureMode || s.cfg.AllowLoopbackRPC || s.cfg.RPCTransport != nil
}

func (s *Service) checkRPCURLsAllowed(primaryURL, fallbackURL string) error {
	if _, _, err := ValidateSolanaRPCURLWithOptions(primaryURL, s.allowLoopbackRPC()); err != nil {
		return err
	}
	if strings.TrimSpace(fallbackURL) != "" {
		if _, _, err := ValidateSolanaRPCURLWithOptions(fallbackURL, s.allowLoopbackRPC()); err != nil {
			return err
		}
	}
	return nil
}

func normalizeWalletSlug(name, role string) string {
	base := strings.ToLower(strings.TrimSpace(name))
	if base == "" {
		base = role
	}
	var b strings.Builder
	prevDash := false
	for i := 0; i < len(base) && b.Len() < 24; i++ {
		c := base[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = role
	}
	return slug
}

func (s *Service) uniqueWalletIDLocked(ctx context.Context, name, role string) (string, error) {
	base := normalizeWalletSlug(name, role)
	candidate := base
	for i := 0; i < 100; i++ {
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM wallets WHERE id = ?`, candidate).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			return candidate, nil
		}
	}
	return randomHexID(base, 4)
}

// CreateNativeWallet creates a brand-new Ed25519 key inside the signer, pins the RPC genesis,
// applies a fail-closed read-only policy baseline, and returns ONLY public metadata.
func (s *Service) CreateNativeWallet(ctx context.Context, name, role, primaryRPCURL string) (WalletPublic, error) {
	cleanRole := strings.ToLower(strings.TrimSpace(role))
	if !ValidRole(cleanRole) {
		return WalletPublic{}, errors.New("wallet role must be agent, trading, vault, or mining")
	}
	if cleanRole == RoleWatch {
		return WalletPublic{}, errors.New("watch role cannot create native signing key")
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		cleanName = strings.ToUpper(cleanRole[:1]) + cleanRole[1:]
	}
	if len(cleanName) > 64 {
		cleanName = cleanName[:64]
	}

	if err := s.checkRPCURLsAllowed(primaryRPCURL, ""); err != nil {
		return WalletPublic{}, err
	}
	rpcStatus, canonRPC, _, err := VerifyAndPinRPC(ctx, s.cfg.RPCTransport, primaryRPCURL, "", "", s.cfg.FixtureMode)
	if err != nil {
		return WalletPublic{}, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return WalletPublic{}, err
	}
	seed := append([]byte(nil), priv.Seed()...)
	clear(priv)
	defer clear(seed)

	pubB58 := Base58Encode(pub)
	return s.insertWalletRecord(ctx, cleanName, CustodyNativeSigner, cleanRole, pubB58, seed, rpcStatus, canonRPC, "", "owner-create")
}

// AttachExternalWallet attaches a Hardware Wallet Standard account or Watch-Only address (public key only).
func (s *Service) AttachExternalWallet(ctx context.Context, custodyType, name, role, publicKey, primaryRPCURL string) (WalletPublic, error) {
	cleanCustody := strings.ToLower(strings.TrimSpace(custodyType))
	if cleanCustody != CustodyHardware && cleanCustody != CustodyWatch {
		return WalletPublic{}, errors.New("external attach custody must be hardware_standard or watch_only")
	}
	cleanPub, _, err := ValidateSolanaPublicAddress(publicKey)
	if err != nil {
		return WalletPublic{}, err
	}
	cleanRole := strings.ToLower(strings.TrimSpace(role))
	if cleanCustody == CustodyWatch {
		cleanRole = RoleWatch
	} else if cleanRole == "" {
		cleanRole = RoleVault
	}
	if !ValidRole(cleanRole) {
		return WalletPublic{}, errors.New("unsupported external wallet role")
	}
	if cleanCustody == CustodyHardware && cleanRole != RoleVault {
		return WalletPublic{}, errors.New("external hardware wallets are restricted to vault custody; create an internal signer wallet for mining, trading, or agent automation")
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		if cleanCustody == CustodyHardware {
			cleanName = "Hardware Vault"
		} else {
			cleanName = "Watch Wallet"
		}
	}
	if err := s.checkRPCURLsAllowed(primaryRPCURL, ""); err != nil {
		return WalletPublic{}, err
	}
	rpcStatus, canonRPC, _, err := VerifyAndPinRPC(ctx, s.cfg.RPCTransport, primaryRPCURL, "", "", s.cfg.FixtureMode)
	if err != nil {
		return WalletPublic{}, err
	}
	return s.insertWalletRecord(ctx, cleanName, cleanCustody, cleanRole, cleanPub, nil, rpcStatus, canonRPC, "", "owner-attach")
}

func (s *Service) insertWalletRecord(ctx context.Context, name, custodyType, role, pubB58 string, seed []byte, rpcStatus WalletRPCStatus, canonPrimary, canonFallback, actor string) (WalletPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletPublic{}, errors.New("custody store closed")
	}
	id, err := s.uniqueWalletIDLocked(ctx, name, role)
	if err != nil {
		return WalletPublic{}, err
	}
	handle := "@wallet:" + id
	pol := DefaultBaselinePolicy(role)
	con := DefaultConnections()
	polBytes, _ := json.Marshal(pol)
	conBytes, _ := json.Marshal(con)
	rpcBytes, _ := json.Marshal(rpcStatus)
	nowSec := s.now().UTC().Unix()

	var keyNonce, keyCT []byte
	if len(seed) > 0 {
		keyNonce, keyCT, err = s.sealBytes("custody-key:"+id+":"+pubB58, seed)
		if err != nil {
			return WalletPublic{}, err
		}
	}
	rpcSecretBytes, _ := json.Marshal(sealedRPCPayload{PrimaryURL: canonPrimary, FallbackURL: canonFallback})
	rpcNonce, rpcCT, err := s.sealBytes("custody-rpc:"+id, rpcSecretBytes)
	clear(rpcSecretBytes)
	if err != nil {
		return WalletPublic{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WalletPublic{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO wallets (id, handle, name, custody, role, public_key, policy_json, policy_acknowledged, connections_json, rpc_status_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		id, handle, name, custodyType, role, pubB58, string(polBytes), string(conBytes), string(rpcBytes), nowSec, nowSec,
	); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return WalletPublic{}, errors.New("wallet with this public key already exists")
		}
		return WalletPublic{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO wallet_keys (wallet_id, key_nonce, key_ciphertext, rpc_nonce, rpc_ciphertext)
VALUES (?, ?, ?, ?, ?)`,
		id, keyNonce, keyCT, rpcNonce, rpcCT,
	); err != nil {
		return WalletPublic{}, err
	}
	if err := s.appendAuditExecLocked(ctx, tx, id, "wallet_created", actor, fmt.Sprintf("Created %s %s wallet (%s) pinned to %s", role, custodyType, pubB58, rpcStatus.Cluster), pol.PolicyHash); err != nil {
		return WalletPublic{}, err
	}
	if err := tx.Commit(); err != nil {
		return WalletPublic{}, err
	}
	return s.getWalletLocked(ctx, id)
}

// UpdatePolicy validates and applies a signer policy update.
// Tightening or freezing applies immediately; loosening requires confirmLoosen == true + matching expectedPolicyHash.
func (s *Service) UpdatePolicy(ctx context.Context, walletID string, rawNext WalletPolicy, confirmLoosen bool, expectedPolicyHash, actor string) (WalletPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletPublic{}, errors.New("custody store closed")
	}
	cur, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		return WalletPublic{}, err
	}
	next := NormalizePolicy(rawNext)
	next.Role = cur.Role
	next = NormalizePolicy(next)

	// Validate approval mode, numeric bounds, destinations, programs, and token caps strictly.
	switch next.ApprovalMode {
	case ApprovalManualReview, ApprovalSemiAuto, ApprovalBoundedAuto:
	default:
		return WalletPublic{}, fmt.Errorf("invalid approval mode %q", next.ApprovalMode)
	}
	if math.IsNaN(next.AutoApproveMaxUSD) || math.IsInf(next.AutoApproveMaxUSD, 0) || next.AutoApproveMaxUSD < 0 {
		return WalletPublic{}, errors.New("autoApproveMaxUsd must be a finite non-negative number")
	}
	if math.IsNaN(next.DailyLossCapUSD) || math.IsInf(next.DailyLossCapUSD, 0) || next.DailyLossCapUSD < 0 {
		return WalletPublic{}, errors.New("dailyLossCapUsd must be a finite non-negative number")
	}
	if next.MaxSlippageBps > 5000 {
		return WalletPublic{}, errors.New("maxSlippageBps cannot exceed 5000 (50%)")
	}
	seenMints := map[string]bool{}
	for _, tc := range next.TokenCaps {
		mint := strings.TrimSpace(tc.Mint)
		if _, _, mErr := ValidateSolanaPublicAddress(mint); mErr != nil {
			return WalletPublic{}, fmt.Errorf("invalid token cap mint %q", tc.Mint)
		}
		if seenMints[mint] {
			return WalletPublic{}, fmt.Errorf("duplicate token cap mint %q", mint)
		}
		seenMints[mint] = true
		if tc.Decimals > 18 {
			return WalletPublic{}, fmt.Errorf("invalid token decimals %d for %s", tc.Decimals, mint)
		}
		if tc.MaxDailyRaw > 0 && tc.MaxPerTxRaw > tc.MaxDailyRaw {
			return WalletPublic{}, fmt.Errorf("token cap maxPerTxRaw exceeds maxDailyRaw for %s", mint)
		}
	}
	for _, p := range next.Programs {
		if _, _, pErr := ValidateSolanaPublicAddress(p); pErr != nil {
			return WalletPublic{}, fmt.Errorf("invalid program ID %q", p)
		}
	}
	for _, d := range next.Destinations {
		if !strings.HasPrefix(d, "@wallet:") {
			if _, _, dErr := ValidateSolanaPublicAddress(d); dErr != nil {
				return WalletPublic{}, fmt.Errorf("invalid destination address %q", d)
			}
		}
	}
	if next.Mining != nil {
		if cur.Role != RoleMining {
			return WalletPublic{}, fmt.Errorf("%w: MiningPolicy requires role=mining", ErrRoleDenied)
		}
		var vm *onchain.LV1XVerifiedManifest
		if m, mErr := s.getVerifiedMiningManifestLocked(ctx); mErr == nil {
			vm = m
		}
		if mErr := ValidateMiningPolicyWithManifest(next.Mining, vm); mErr != nil {
			return WalletPublic{}, mErr
		}
		if next.Mining.Cluster == "mainnet-beta" && next.Mining.AllowMainnet {
			rpcCreds, rErr := s.unsealRPCLocked(ctx, cur.ID)
			if rErr != nil {
				return WalletPublic{}, rErr
			}
			cl := vm.Payload.Clusters["mainnet-beta"]
			if bErr := s.verifyMiningProgramOnChain(ctx, rpcCreds.PrimaryURL, next.Mining, cl); bErr != nil {
				return WalletPublic{}, fmt.Errorf("%w: Mainnet LV1X on-chain verification failed: %v", ErrPolicyDenied, bErr)
			}
		}
	}

	tightening := IsPolicyTighterOrEqual(cur.Policy, next)
	if !tightening {
		if !confirmLoosen {
			return WalletPublic{}, fmt.Errorf("%w: expanding policy requires explicit owner confirmation", ErrPolicyDenied)
		}
		if strings.TrimSpace(expectedPolicyHash) != cur.Policy.PolicyHash {
			return WalletPublic{}, ErrPolicyConflict
		}
	}

	next.Version = cur.Policy.Version + 1
	next.PolicyHash = ComputePolicyHash(next)
	polBytes, _ := json.Marshal(next)
	nowSec := s.now().UTC().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WalletPublic{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE wallets SET policy_json = ?, policy_acknowledged = 1, updated_at = ? WHERE id = ?`,
		string(polBytes), nowSec, cur.ID,
	); err != nil {
		return WalletPublic{}, err
	}
	kind := "policy_tightened"
	if !tightening {
		kind = "policy_updated"
	}
	if err := s.appendAuditExecLocked(ctx, tx, cur.ID, kind, firstStr(actor, "owner"), fmt.Sprintf("Policy v%d applied (mode=%s frozen=%t)", next.Version, next.ApprovalMode, next.Frozen), next.PolicyHash); err != nil {
		return WalletPublic{}, err
	}
	if err := tx.Commit(); err != nil {
		return WalletPublic{}, err
	}
	return s.getWalletLocked(ctx, cur.ID)
}

// SetFreeze toggles emergency freeze immediately on a wallet without requiring loosen confirmation.
func (s *Service) SetFreeze(ctx context.Context, walletID string, frozen bool, reason, actor string) (WalletPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletPublic{}, errors.New("custody store closed")
	}
	cur, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		return WalletPublic{}, err
	}
	if !frozen && cur.Policy.Frozen {
		// Unfreezing is an authority expansion -> bump version cleanly.
		cur.Policy.Frozen = false
		cur.Policy.FreezeReason = ""
	} else {
		cur.Policy.Frozen = frozen
		cur.Policy.FreezeReason = strings.TrimSpace(reason)
	}
	cur.Policy.Version++
	cur.Policy.PolicyHash = ComputePolicyHash(cur.Policy)
	polBytes, _ := json.Marshal(cur.Policy)
	nowSec := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WalletPublic{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE wallets SET policy_json = ?, updated_at = ? WHERE id = ?`,
		string(polBytes), nowSec, cur.ID,
	); err != nil {
		return WalletPublic{}, err
	}
	action := "wallet_frozen"
	if !frozen {
		action = "wallet_unfrozen"
	}
	if err := s.appendAuditExecLocked(ctx, tx, cur.ID, action, firstStr(actor, "owner"), fmt.Sprintf("%s: %s", action, cur.Policy.FreezeReason), cur.Policy.PolicyHash); err != nil {
		return WalletPublic{}, err
	}
	if err := tx.Commit(); err != nil {
		return WalletPublic{}, err
	}
	return s.getWalletLocked(ctx, cur.ID)
}

// UpdateConnections updates explicit module/agent bindings enforcing role constraints.
func (s *Service) UpdateConnections(ctx context.Context, walletID string, rawConn WalletConnections, actor string) (WalletPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletPublic{}, errors.New("custody store closed")
	}
	cur, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		return WalletPublic{}, err
	}
	conn, err := ValidateConnectionsForRole(cur.Role, rawConn)
	if err != nil {
		return WalletPublic{}, err
	}
	nowSec := s.now().UTC().Unix()
	if conn.DefaultAgentWallet {
		// Ensure singleton default agent wallet across custody store.
		rows, rErr := s.db.QueryContext(ctx, `SELECT id, connections_json FROM wallets WHERE id != ?`, cur.ID)
		if rErr == nil {
			var updates []struct {
				id  string
				raw string
			}
			for rows.Next() {
				var oid, craw string
				if rows.Scan(&oid, &craw) == nil {
					var oc WalletConnections
					if json.Unmarshal([]byte(craw), &oc) == nil && oc.DefaultAgentWallet {
						oc.DefaultAgentWallet = false
						b, _ := json.Marshal(oc)
						updates = append(updates, struct {
							id  string
							raw string
						}{oid, string(b)})
					}
				}
			}
			_ = rows.Close()
			for _, u := range updates {
				_, _ = s.db.ExecContext(ctx, `UPDATE wallets SET connections_json = ?, updated_at = ? WHERE id = ?`, u.raw, nowSec, u.id)
			}
		}
	}
	conBytes, _ := json.Marshal(conn)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WalletPublic{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`UPDATE wallets SET connections_json = ?, updated_at = ? WHERE id = ?`,
		string(conBytes), nowSec, cur.ID,
	); err != nil {
		return WalletPublic{}, err
	}
	if err := s.appendAuditExecLocked(ctx, tx, cur.ID, "connections_updated", firstStr(actor, "owner"), fmt.Sprintf("agents=%v manual=%t bots=%t venues=%v", conn.AgentIDs, conn.AllowTradeManual, conn.AllowTradeBots, conn.AllowedVenues), cur.Policy.PolicyHash); err != nil {
		return WalletPublic{}, err
	}
	if err := tx.Commit(); err != nil {
		return WalletPublic{}, err
	}
	return s.getWalletLocked(ctx, cur.ID)
}

// SetWalletRPC replaces the encrypted execution RPC URL only if the live genesis matches the wallet's pinned cluster.
func (s *Service) SetWalletRPC(ctx context.Context, walletID, primaryURL, fallbackURL, actor string) (WalletPublic, error) {
	s.mu.Lock()
	cur, err := s.getWalletLocked(ctx, walletID)
	s.mu.Unlock()
	if err != nil {
		return WalletPublic{}, err
	}

	if err := s.checkRPCURLsAllowed(primaryURL, fallbackURL); err != nil {
		return WalletPublic{}, err
	}
	nextStatus, canonPrimary, canonFallback, err := VerifyAndPinRPC(ctx, s.cfg.RPCTransport, primaryURL, fallbackURL, cur.RPC.GenesisHash, s.cfg.FixtureMode)
	if err != nil {
		return WalletPublic{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletPublic{}, errors.New("custody store closed")
	}
	cur2, err := s.getWalletLocked(ctx, cur.ID)
	if err != nil {
		return WalletPublic{}, err
	}
	nextStatus.Version = cur2.RPC.Version + 1
	rpcBytes, _ := json.Marshal(nextStatus)
	rpcSecretBytes, _ := json.Marshal(sealedRPCPayload{PrimaryURL: canonPrimary, FallbackURL: canonFallback})
	rpcNonce, rpcCT, err := s.sealBytes("custody-rpc:"+cur2.ID, rpcSecretBytes)
	clear(rpcSecretBytes)
	if err != nil {
		return WalletPublic{}, err
	}
	nowSec := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WalletPublic{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE wallets SET rpc_status_json = ?, updated_at = ? WHERE id = ?`, string(rpcBytes), nowSec, cur2.ID); err != nil {
		return WalletPublic{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE wallet_keys SET rpc_nonce = ?, rpc_ciphertext = ? WHERE wallet_id = ?`, rpcNonce, rpcCT, cur2.ID); err != nil {
		return WalletPublic{}, err
	}
	if err := s.appendAuditExecLocked(ctx, tx, cur2.ID, "rpc_updated", firstStr(actor, "owner"), fmt.Sprintf("RPC v%d -> %s (%s)", nextStatus.Version, nextStatus.Host, nextStatus.Cluster), cur2.Policy.PolicyHash); err != nil {
		return WalletPublic{}, err
	}
	if err := tx.Commit(); err != nil {
		return WalletPublic{}, err
	}
	return s.getWalletLocked(ctx, cur2.ID)
}

func (s *Service) unsealRPCLocked(ctx context.Context, walletID string) (sealedRPCPayload, error) {
	var nonce, ct []byte
	err := s.db.QueryRowContext(ctx, `SELECT rpc_nonce, rpc_ciphertext FROM wallet_keys WHERE wallet_id = ?`, walletID).Scan(&nonce, &ct)
	if err != nil {
		return sealedRPCPayload{}, err
	}
	plain, err := s.openBytes("custody-rpc:"+walletID, nonce, ct)
	if err != nil {
		return sealedRPCPayload{}, err
	}
	defer clear(plain)
	var out sealedRPCPayload
	if err := json.Unmarshal(plain, &out); err != nil {
		return sealedRPCPayload{}, err
	}
	return out, nil
}

// FetchBalance queries SOL balance using the wallet's signer-encrypted execution RPC.
func (s *Service) FetchBalance(ctx context.Context, walletID string) (BalanceResult, error) {
	s.mu.Lock()
	w, err := s.getWalletLocked(ctx, walletID)
	var rpcCreds sealedRPCPayload
	if err == nil {
		rpcCreds, err = s.unsealRPCLocked(ctx, w.ID)
	}
	s.mu.Unlock()
	if err != nil {
		return BalanceResult{}, err
	}

	var lamports uint64
	var latency int64
	var tokens []TokenBalance
	if s.cfg.FixtureMode && s.cfg.RPCTransport == nil {
		lamports = 2_500_000_000
		latency = 3
		tokens = []TokenBalance{
			{
				Mint:        "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
				AmountRaw:   250_000_000,
				Decimals:    6,
				AmountHuman: "250",
			},
		}
	} else {
		var out struct {
			Value uint64 `json:"value"`
		}
		lat, rErr := rpcJSONCall(ctx, s.cfg.RPCTransport, rpcCreds.PrimaryURL, "getBalance", []any{w.PublicKey}, &out)
		if rErr != nil {
			return BalanceResult{}, rErr
		}
		lamports = out.Value
		latency = lat
		if splTokens, sErr := fetchSPLBalancesRPC(ctx, s.cfg.RPCTransport, rpcCreds.PrimaryURL, w.PublicKey); sErr == nil {
			tokens = splTokens
		}
	}
	return BalanceResult{
		WalletID:  w.ID,
		PublicKey: w.PublicKey,
		Cluster:   w.RPC.Cluster,
		RPCHost:   w.RPC.Host,
		Lamports:  lamports,
		SOL:       FormatLamportsToSOL(lamports),
		Tokens:    tokens,
		LatencyMs: latency,
	}, nil
}

// ResolveWallet enforces deterministic wallet selection precedence and role boundaries.
func (s *Service) ResolveWallet(ctx context.Context, sel WalletSelector) (WalletPublic, error) {
	wallets, err := s.ListWallets(ctx)
	if err != nil {
		return WalletPublic{}, err
	}
	checkEligible := func(w WalletPublic) error {
		if w.Custody == CustodyWatch || w.Role == RoleWatch {
			return fmt.Errorf("%w: watch-only wallet cannot execute", ErrRoleDenied)
		}
		if w.Policy.Frozen {
			return fmt.Errorf("%w: wallet %s is frozen", ErrPolicyDenied, w.Handle)
		}
		isMiningVenue := strings.HasPrefix(strings.ToLower(strings.TrimSpace(sel.Venue)), "wen-mining")
		if sel.IsAutonomous && (w.Role == RoleVault || (w.Role == RoleMining && !isMiningVenue) || (w.Role != RoleMining && !w.Connections.AllowTradeBots)) {
			return fmt.Errorf("%w: wallet %s is not authorized for autonomous execution", ErrRoleDenied, w.Handle)
		}
		if sel.Venue != "" {
			cleanVenue := strings.ToLower(strings.TrimSpace(sel.Venue))
			if sel.IsAutonomous {
				if len(w.Connections.AllowedVenues) == 0 || !slices.Contains(w.Connections.AllowedVenues, cleanVenue) {
					return fmt.Errorf("%w: wallet %s does not authorize autonomous execution on venue %s", ErrPolicyDenied, w.Handle, sel.Venue)
				}
			} else if len(w.Connections.AllowedVenues) > 0 && !slices.Contains(w.Connections.AllowedVenues, cleanVenue) {
				return fmt.Errorf("%w: wallet %s is not connected to venue %s", ErrPolicyDenied, w.Handle, sel.Venue)
			}
		}
		return nil
	}

	// 1. Explicit handle or walletId.
	if target := firstStr(strings.TrimPrefix(strings.TrimSpace(sel.WalletHandle), "@wallet:"), strings.TrimSpace(sel.WalletID)); target != "" {
		for _, w := range wallets {
			if w.ID == target || w.Handle == sel.WalletHandle {
				if err := checkEligible(w); err != nil {
					return WalletPublic{}, err
				}
				return w, nil
			}
		}
		return WalletPublic{}, ErrWalletNotFound
	}

	// 2. Calling skill's approved wallet grant.
	if sk := strings.TrimSpace(sel.SkillGrantID); sk != "" {
		var matches []WalletPublic
		for _, w := range wallets {
			for _, g := range w.Connections.SkillGrants {
				if g.SkillID == sk && (!sel.IsAutonomous || g.ScheduledAllowed) {
					if checkEligible(w) == nil {
						matches = append(matches, w)
					}
					break
				}
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return WalletPublic{}, fmt.Errorf("%w: multiple wallets match skill %q; specify @wallet:<id>", ErrPolicyDenied, sk)
		}
	}

	// 3. Explicit agent assignment.
	if aid := strings.TrimSpace(sel.AgentID); aid != "" {
		var matches []WalletPublic
		for _, w := range wallets {
			if slices.Contains(w.Connections.AgentIDs, aid) && checkEligible(w) == nil {
				matches = append(matches, w)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return WalletPublic{}, fmt.Errorf("%w: multiple wallets assigned to agent %q; specify @wallet:<id>", ErrPolicyDenied, aid)
		}
	}

	// 4. Optional Default Agent wallet fallback.
	for _, w := range wallets {
		if w.Connections.DefaultAgentWallet && checkEligible(w) == nil {
			return w, nil
		}
	}
	return WalletPublic{}, fmt.Errorf("%w: no authorized wallet matched selection contract", ErrPolicyDenied)
}

func (s *Service) resolveDestinationAddressLocked(ctx context.Context, raw string) (canonical string, pubKey [32]byte, allowedForm string, err error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "@wallet:") {
		targetW, wErr := s.getWalletLocked(ctx, trimmed)
		if wErr != nil {
			return "", pubKey, "", wErr
		}
		canon, pk, vErr := ValidateSolanaPublicAddress(targetW.PublicKey)
		return canon, pk, targetW.Handle, vErr
	}
	canon, pk, err := ValidateSolanaPublicAddress(trimmed)
	return canon, pk, canon, err
}

func (s *Service) hasUnresolvedBroadcastLocked(ctx context.Context, walletID string) bool {
	var cnt int
	_ = s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM ledger WHERE wallet_id = ? AND state IN (?, ?, ?)`,
		walletID, LedgerReserved, LedgerBroadcast, LedgerUnknown,
	).Scan(&cnt)
	return cnt > 0
}

func (s *Service) fetchBlockhashOrFixture(ctx context.Context, walletID, rpcURL string) ([32]byte, error) {
	var bh [32]byte
	if s.cfg.FixtureMode && s.cfg.RPCTransport == nil {
		return sha256.Sum256([]byte("fixture-blockhash-" + walletID + "-" + fmt.Sprintf("%d", s.now().UnixNano()))), nil
	}
	var bhResp struct {
		Value struct {
			Blockhash string `json:"blockhash"`
		} `json:"value"`
	}
	if _, err := rpcJSONCall(ctx, s.cfg.RPCTransport, rpcURL, "getLatestBlockhash", []any{map[string]string{"commitment": "finalized"}}, &bhResp); err != nil {
		return bh, fmt.Errorf("fetch blockhash failed: %w", err)
	}
	rawBH, err := Base58Decode(bhResp.Value.Blockhash)
	if err != nil || len(rawBH) != 32 {
		return bh, errors.New("invalid recent blockhash from RPC")
	}
	copy(bh[:], rawBH)
	return bh, nil
}

func reviewSOLDebit(rev WalletReview) (uint64, error) {
	switch rev.Operation {
	case "solana.nativeTransfer", MiningOpCommit, MiningOpSession:
		if rev.AmountRaw > math.MaxUint64-rev.EstimatedFeeLamports {
			return 0, errors.New("debit overflows uint64")
		}
		return rev.AmountRaw + rev.EstimatedFeeLamports, nil
	case "jupiter.swap":
		if rev.Mint == onchain.SOL {
			if rev.AmountRaw > math.MaxUint64-rev.EstimatedFeeLamports {
				return 0, errors.New("debit overflows uint64")
			}
			return rev.AmountRaw + rev.EstimatedFeeLamports, nil
		}
		return rev.EstimatedFeeLamports, nil
	default:
		return rev.EstimatedFeeLamports, nil
	}
}

func reviewSPLDebit(rev WalletReview) (mint string, amountRaw uint64) {
	switch rev.Operation {
	case "solana.splTransferChecked":
		if rev.Mint != "" && rev.AmountRaw > 0 {
			return rev.Mint, rev.AmountRaw
		}
	case "jupiter.swap":
		if rev.Mint != "" && rev.Mint != onchain.SOL && rev.AmountRaw > 0 {
			return rev.Mint, rev.AmountRaw
		}
	}
	return "", 0
}

func findTokenCap(caps []AssetCap, mint string) (AssetCap, bool) {
	for _, tc := range caps {
		if strings.TrimSpace(tc.Mint) == mint {
			return tc, true
		}
	}
	return AssetCap{}, false
}

// PrepareReview validates signer policy fail-closed, constructs a canonical unsigned Solana transaction
// message, simulates it on the wallet's execution RPC, and persists an immutable review.
func (s *Service) PrepareReview(ctx context.Context, req PrepareReviewRequest) (WalletReview, error) {
	op := strings.TrimSpace(req.Operation)
	if op == "" {
		op = "solana.nativeTransfer"
	}
	if op != "solana.nativeTransfer" && op != "solana.splTransferChecked" && op != "jupiter.swap" && !isMiningOperation(op) {
		return WalletReview{}, fmt.Errorf("%w: unsupported prepare operation %q", ErrPolicyDenied, op)
	}

	s.mu.Lock()
	w, err := s.getWalletLocked(ctx, req.WalletID)
	if err != nil {
		s.mu.Unlock()
		return WalletReview{}, err
	}
	if w.Custody == CustodyWatch || w.Role == RoleWatch {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: watch-only wallet cannot sign", ErrRoleDenied)
	}
	if w.Policy.Frozen {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: wallet is frozen", ErrPolicyDenied)
	}
	if gFrozen, gReason := s.isGlobalFrozenLocked(ctx); gFrozen {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: global emergency freeze active: %s", ErrPolicyDenied, firstStr(gReason, "owner freeze"))
	}
	if s.hasUnresolvedBroadcastLocked(ctx, w.ID) {
		s.mu.Unlock()
		return WalletReview{}, ErrAmbiguousState
	}
	if w.Policy.ExpiresAt > 0 && s.now().UTC().Unix() > w.Policy.ExpiresAt {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: wallet policy expired", ErrPolicyDenied)
	}
	reqAgentID := strings.TrimSpace(req.AgentID)
	reqVenue := strings.ToLower(strings.TrimSpace(req.Venue))
	reqSkillID := strings.TrimSpace(req.SkillGrantID)
	var activeSkillGrant *SkillGrant
	if reqSkillID != "" {
		for i := range w.Connections.SkillGrants {
			if strings.TrimSpace(w.Connections.SkillGrants[i].SkillID) == reqSkillID {
				g := w.Connections.SkillGrants[i]
				activeSkillGrant = &g
				break
			}
		}
		if activeSkillGrant == nil {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: skill grant %q is not bound to wallet %s", ErrRoleDenied, reqSkillID, w.Handle)
		}
		if len(activeSkillGrant.Actions) == 0 || !slices.Contains(activeSkillGrant.Actions, op) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: skill grant %q does not permit operation %q", ErrPolicyDenied, reqSkillID, op)
		}
		if req.IsBotOrAuto && !activeSkillGrant.ScheduledAllowed {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: skill grant %q prohibits scheduled/autonomous execution", ErrPolicyDenied, reqSkillID)
		}
	}
	if req.IsBotOrAuto {
		if isMiningOperation(op) {
			if w.Role != RoleMining || w.Policy.ApprovalMode == ApprovalManualReview || w.Policy.Mining == nil {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: mining wallet requires Semi-Auto or Auto policy", ErrRoleDenied)
			}
			if w.Policy.ApprovalMode == ApprovalSemiAuto && !((isMiningClaimOrRevealOp(op) && w.Policy.Mining.AutoClaim) || (op == MiningOpFallbackSettle && w.Policy.Mining.AllowFallbackCrank)) {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: Semi-Auto mining prohibits unattended commit, stake, or session operations", ErrRoleDenied)
			}
			if op == MiningOpStake && !w.Policy.Mining.AutoStakeWensat {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: unattended stake requires AutoStakeWensat enabled on MiningPolicy", ErrRoleDenied)
			}
			if op == MiningOpUnstake {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: unstake requires manual owner authorization", ErrRoleDenied)
			}
		} else {
			if w.Role == RoleVault || w.Role == RoleMining || w.Policy.ApprovalMode == ApprovalManualReview || !w.Connections.AllowTradeBots {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: wallet prohibits autonomous execution", ErrRoleDenied)
			}
			if reqAgentID != "" && !slices.Contains(w.Connections.AgentIDs, reqAgentID) {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: agent %q is not bound to wallet %s", ErrRoleDenied, reqAgentID, w.Handle)
			}
			if reqVenue != "" && (len(w.Connections.AllowedVenues) == 0 || !slices.Contains(w.Connections.AllowedVenues, reqVenue)) {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: venue %q is not allowed on wallet %s", ErrPolicyDenied, reqVenue, w.Handle)
			}
		}
	}
	if !slices.Contains(w.Policy.Operations, op) {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: operation %q is not permitted in policy", ErrPolicyDenied, op)
	}
	if !slices.Contains(w.Policy.Programs, SolanaSystemProgram) || !slices.Contains(w.Policy.Programs, SolanaComputeBudget) {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: System and ComputeBudget programs must be allowed", ErrPolicyDenied)
	}

	var (
		destAddr       string
		destPK         [32]byte
		mintAddr       string
		outputMintAddr string
		mintPK         [32]byte
		symbol         = "SOL"
		amountRaw      = req.AmountRaw
		amountHuman    string
		quotedOutRaw   uint64
		minOutRaw      uint64
		slippageBps    uint32
		estimatedUSD   float64
		decimals       uint8  = 9
		estFeeLamports uint64 = 5_000
		programs       []string
		miningDetails  preparedMiningDetails
	)

	switch op {
	case "solana.nativeTransfer":
		if amountRaw == 0 {
			parsed, pErr := ParseHumanSOLToLamports(req.AmountHuman)
			if pErr != nil {
				s.mu.Unlock()
				return WalletReview{}, pErr
			}
			amountRaw = parsed
		}
		var destHandleForm string
		destAddr, destPK, destHandleForm, err = s.resolveDestinationAddressLocked(ctx, req.Destination)
		if err != nil {
			s.mu.Unlock()
			return WalletReview{}, err
		}
		if !slices.Contains(w.Policy.Destinations, destAddr) && !slices.Contains(w.Policy.Destinations, destHandleForm) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: destination %s is not in wallet allowlist", ErrPolicyDenied, destAddr)
		}
		if amountRaw > math.MaxUint64-estFeeLamports {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: amount overflows uint64 with fee", ErrPolicyDenied)
		}
		totalDebit := amountRaw + estFeeLamports
		if w.Policy.SOLCap.MaxPerTxRaw == 0 || totalDebit > w.Policy.SOLCap.MaxPerTxRaw {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: amount+fee exceeds per-transaction cap", ErrPolicyDenied)
		}
		if w.DailySpentLamports > math.MaxUint64-w.DailyReservedLamports ||
			w.DailySpentLamports+w.DailyReservedLamports > math.MaxUint64-totalDebit ||
			w.Policy.SOLCap.MaxDailyRaw == 0 ||
			w.DailySpentLamports+w.DailyReservedLamports+totalDebit > w.Policy.SOLCap.MaxDailyRaw {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: amount+fee exceeds remaining daily cap", ErrPolicyDenied)
		}
		amountHuman = FormatLamportsToSOL(amountRaw)
		// Conservative signer-side upper-bound USD valuation for SOL native transfers ($250/SOL ceiling)
		// so bounded_auto AutoApproveMaxUSD and SkillGrant.MaxPerTxUSD are strictly enforced.
		estimatedUSD = (float64(amountRaw) / 1e9) * 250.0
		programs = []string{SolanaComputeBudget, SolanaSystemProgram}

	case "solana.splTransferChecked":
		mintAddr, mintPK, err = ValidateSolanaPublicAddress(req.Mint)
		if err != nil {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: invalid SPL mint address", ErrPolicyDenied)
		}
		tc, ok := findTokenCap(w.Policy.TokenCaps, mintAddr)
		if !ok || tc.MaxPerTxRaw == 0 || tc.MaxDailyRaw == 0 {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: SPL mint %s has no active token cap", ErrPolicyDenied, mintAddr)
		}
		if !slices.Contains(w.Policy.Programs, SolanaTokenProgram) || !slices.Contains(w.Policy.Programs, SolanaATAProgram) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: Token and AssociatedToken programs must be allowed", ErrPolicyDenied)
		}
		decimals = req.Decimals
		if decimals == 0 {
			decimals = tc.Decimals
		} else if tc.Decimals != 0 && decimals != tc.Decimals {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: SPL decimals mismatch policy cap (%d != %d)", ErrPolicyDenied, decimals, tc.Decimals)
		}
		if amountRaw == 0 {
			parsed, pErr := ParseHumanAmountToRaw(req.AmountHuman, decimals)
			if pErr != nil {
				s.mu.Unlock()
				return WalletReview{}, pErr
			}
			amountRaw = parsed
		}
		if amountRaw > tc.MaxPerTxRaw || amountRaw > tc.MaxDailyRaw {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: SPL amount exceeds token cap", ErrPolicyDenied)
		}
		tokUse := w.DailyTokenUsage[mintAddr]
		if tokUse.SpentRaw > math.MaxUint64-tokUse.ReservedRaw ||
			tokUse.SpentRaw+tokUse.ReservedRaw > math.MaxUint64-amountRaw ||
			tokUse.SpentRaw+tokUse.ReservedRaw+amountRaw > tc.MaxDailyRaw {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: SPL amount exceeds remaining daily token cap", ErrPolicyDenied)
		}
		var destHandleForm string
		destAddr, destPK, destHandleForm, err = s.resolveDestinationAddressLocked(ctx, req.Destination)
		if err != nil {
			s.mu.Unlock()
			return WalletReview{}, err
		}
		if !slices.Contains(w.Policy.Destinations, destAddr) && !slices.Contains(w.Policy.Destinations, destHandleForm) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: destination %s is not in wallet allowlist", ErrPolicyDenied, destAddr)
		}
		if activeSkillGrant != nil && len(activeSkillGrant.AllowedMints) > 0 && !slices.Contains(activeSkillGrant.AllowedMints, mintAddr) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: skill grant %q does not allow mint %s", ErrPolicyDenied, activeSkillGrant.SkillID, mintAddr)
		}
		const splATARentLamports uint64 = 2_039_280
		if req.CreateDestATA {
			estFeeLamports += splATARentLamports
		}
		if w.Policy.SOLCap.MaxPerTxRaw < estFeeLamports ||
			w.Policy.SOLCap.MaxDailyRaw == 0 ||
			w.DailySpentLamports+w.DailyReservedLamports+estFeeLamports > w.Policy.SOLCap.MaxDailyRaw {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: fee and ATA rent lamports exceed remaining SOL cap", ErrPolicyDenied)
		}
		symbol = firstStr(tc.Symbol, mintAddr[:6])
		amountHuman = FormatRawAmount(amountRaw, decimals)
		if mintAddr == onchain.USDC {
			estimatedUSD = float64(amountRaw) / 1_000_000.0
		}
		programs = []string{SolanaComputeBudget, SolanaATAProgram, SolanaTokenProgram}

	case "jupiter.swap":
		if req.JupiterQuote == nil {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: jupiterQuote required for jupiter.swap", ErrPolicyDenied)
		}
		q := *req.JupiterQuote
		if !slices.Contains(w.Policy.Programs, SolanaJupiterV6Program) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: Jupiter v6 program is not permitted in policy", ErrPolicyDenied)
		}
		for _, ix := range q.Instructions {
			if !slices.Contains(w.Policy.Programs, ix.ProgramID) {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: swap instruction program %s not permitted in policy", ErrPolicyDenied, ix.ProgramID)
			}
		}
		maxSlip := w.Policy.MaxSlippageBps
		if activeSkillGrant != nil && activeSkillGrant.MaxSlippageBps > 0 && activeSkillGrant.MaxSlippageBps < maxSlip {
			maxSlip = activeSkillGrant.MaxSlippageBps
		}
		if maxSlip == 0 || uint32(q.SlippageBPS) > maxSlip {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap slippage %d bps exceeds max %d bps", ErrPolicyDenied, q.SlippageBPS, maxSlip)
		}
		if req.OutputMint != "" && q.OutputMint != strings.TrimSpace(req.OutputMint) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap output mint mismatch", ErrPolicyDenied)
		}
		if q.OutputMint != onchain.SOL {
			if _, ok := findTokenCap(w.Policy.TokenCaps, q.OutputMint); !ok {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: swap output mint %s is not allowlisted in wallet token caps", ErrPolicyDenied, q.OutputMint)
			}
		}
		if activeSkillGrant != nil && len(activeSkillGrant.AllowedMints) > 0 {
			if !slices.Contains(activeSkillGrant.AllowedMints, q.InputMint) || !slices.Contains(activeSkillGrant.AllowedMints, q.OutputMint) {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: skill grant %q does not allow swap mint pair", ErrPolicyDenied, activeSkillGrant.SkillID)
			}
		}
		if q.Owner != "" && q.Owner != w.PublicKey {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap quote owner differs from wallet", ErrPolicyDenied)
		}
		q.Owner = w.PublicKey
		inRaw, uErr := onchain.Uint(q.Amount)
		if uErr != nil || inRaw == 0 {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: invalid swap input amount", ErrPolicyDenied)
		}
		outRaw, oErr := onchain.Uint(q.Output)
		if oErr != nil || outRaw == 0 {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap output amount must be positive", ErrPolicyDenied)
		}
		// Compute conservative minOutRaw and enforce caller/floor minimums so worst-price
		// outputs (e.g. q.Output="1") cannot drain a wallet cap.
		minOutRaw = outRaw
		if q.SlippageBPS > 0 && q.SlippageBPS < 10_000 {
			minOutRaw = outRaw * uint64(10_000-q.SlippageBPS) / 10_000
		}
		if q.MinimumOutput != "" {
			if parsedMin, mErr := onchain.Uint(q.MinimumOutput); mErr == nil && parsedMin > minOutRaw {
				minOutRaw = parsedMin
			}
		}
		if req.MinOutputRaw > 0 && outRaw < req.MinOutputRaw {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap output %d below required floor %d", ErrPolicyDenied, outRaw, req.MinOutputRaw)
		}
		if minOutRaw == 0 {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap minimum output must be positive after slippage", ErrPolicyDenied)
		}
		// Catch degenerate USDC/SOL worst-price drain (e.g. >= 0.01 SOL quoted for < $0.01 USDC).
		if q.InputMint == onchain.SOL && q.OutputMint == onchain.USDC && inRaw >= 10_000_000 && outRaw < 10_000 {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap output %d micro-USDC for %d lamports fails minimum price floor", ErrPolicyDenied, outRaw, inRaw)
		}
		if q.InputMint == onchain.USDC {
			estimatedUSD = float64(inRaw) / 1_000_000.0
		} else if q.OutputMint == onchain.USDC {
			estimatedUSD = float64(outRaw) / 1_000_000.0
		}
		quotedOutRaw = outRaw
		outputMintAddr = q.OutputMint
		slippageBps = uint32(q.SlippageBPS)

		feeWorst := max(q.FeeLamports, q.MaxFeeLamports)
		if feeWorst > estFeeLamports {
			estFeeLamports = feeWorst
		}
		// Derive ATA creation rent inside signer from actual AssociatedToken setup instructions
		// instead of trusting caller-supplied q.RentLamports (which could be zeroed).
		const solanaATARentLamports uint64 = 2_039_280
		var ataSetupCount uint64
		for _, ix := range q.Instructions {
			if ix.ProgramID == SolanaATAProgram {
				ataSetupCount++
			}
		}
		effectiveRent := max(q.RentLamports, ataSetupCount*solanaATARentLamports)
		if effectiveRent > 0 {
			if estFeeLamports > math.MaxUint64-effectiveRent {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: swap fee+rent overflows uint64", ErrPolicyDenied)
			}
			estFeeLamports += effectiveRent
		}
		if q.InputMint == onchain.SOL {
			if inRaw > math.MaxUint64-estFeeLamports {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: swap SOL amount overflows with fee", ErrPolicyDenied)
			}
			totalDebit := inRaw + estFeeLamports
			if w.Policy.SOLCap.MaxPerTxRaw == 0 || totalDebit > w.Policy.SOLCap.MaxPerTxRaw ||
				w.DailySpentLamports+w.DailyReservedLamports+totalDebit > w.Policy.SOLCap.MaxDailyRaw {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: swap SOL amount+fee exceeds wallet cap", ErrPolicyDenied)
			}
			amountHuman = FormatLamportsToSOL(inRaw)
		} else {
			tc, ok := findTokenCap(w.Policy.TokenCaps, q.InputMint)
			if !ok || tc.MaxPerTxRaw == 0 || tc.MaxDailyRaw == 0 || inRaw > tc.MaxPerTxRaw || inRaw > tc.MaxDailyRaw {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: swap input mint %s exceeds or lacks token cap", ErrPolicyDenied, q.InputMint)
			}
			tokUse := w.DailyTokenUsage[q.InputMint]
			if tokUse.SpentRaw > math.MaxUint64-tokUse.ReservedRaw ||
				tokUse.SpentRaw+tokUse.ReservedRaw > math.MaxUint64-inRaw ||
				tokUse.SpentRaw+tokUse.ReservedRaw+inRaw > tc.MaxDailyRaw {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: swap input mint %s exceeds remaining daily token cap", ErrPolicyDenied, q.InputMint)
			}
			if w.Policy.SOLCap.MaxPerTxRaw < estFeeLamports ||
				w.DailySpentLamports+w.DailyReservedLamports+estFeeLamports > w.Policy.SOLCap.MaxDailyRaw {
				s.mu.Unlock()
				return WalletReview{}, fmt.Errorf("%w: fee lamports exceed remaining SOL cap", ErrPolicyDenied)
			}
			amountHuman = FormatRawAmount(inRaw, tc.Decimals)
		}
		if vErr := onchain.ValidateInstructions(q); vErr != nil {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: %v", ErrPolicyDenied, vErr)
		}
		mintAddr = q.InputMint
		destAddr = q.OutputMint
		amountRaw = inRaw
		symbol = "JUP-SWAP"
		progList := []string{SolanaComputeBudget}
		for _, ix := range q.Instructions {
			if !slices.Contains(progList, ix.ProgramID) {
				progList = append(progList, ix.ProgramID)
			}
		}
		programs = progList

	default:
		if !isMiningOperation(op) {
			break
		}
		md, mErr := s.validateMiningPrepareLocked(ctx, w, op, req)
		if mErr != nil {
			s.mu.Unlock()
			return WalletReview{}, mErr
		}
		miningDetails = md
		destAddr = md.DestAddr
		symbol = md.Symbol
		amountRaw = md.AmountLamports
		amountHuman = md.HumanAmount
		estimatedUSD = md.EstimatedUSD
		estFeeLamports = md.EstFeeLamports
		programs = md.Programs
	}

	rpcCreds, err := s.unsealRPCLocked(ctx, w.ID)
	s.mu.Unlock()
	if err != nil {
		return WalletReview{}, err
	}

	_, feePayerPK, err := ValidateSolanaPublicAddress(w.PublicKey)
	if err != nil {
		return WalletReview{}, err
	}

	priorityFeeMicro := uint64(1_000)
	if !(s.cfg.FixtureMode && s.cfg.RPCTransport == nil) && op != "jupiter.swap" {
		if est := estimatePriorityFeeRPC(ctx, s.cfg.RPCTransport, rpcCreds.PrimaryURL); est >= 1_000 {
			priorityFeeMicro = est
		}
	}

	var msgBytes []byte
	switch op {
	case "solana.nativeTransfer":
		bh, bErr := s.fetchBlockhashOrFixture(ctx, w.ID, rpcCreds.PrimaryURL)
		if bErr != nil {
			return WalletReview{}, bErr
		}
		msgBytes, err = onchain.BuildSolanaTransferMessage(feePayerPK, destPK, bh, amountRaw, 50_000, priorityFeeMicro)
	case "solana.splTransferChecked":
		bh, bErr := s.fetchBlockhashOrFixture(ctx, w.ID, rpcCreds.PrimaryURL)
		if bErr != nil {
			return WalletReview{}, bErr
		}
		msgBytes, err = onchain.BuildSPLTransferMessage(feePayerPK, destPK, mintPK, bh, amountRaw, decimals, req.CreateDestATA, 100_000, priorityFeeMicro)
	case "jupiter.swap":
		q := *req.JupiterQuote
		q.Owner = w.PublicKey
		if len(q.AddressLookupTables) > 0 && !(s.cfg.FixtureMode && s.cfg.RPCTransport == nil) {
			if altErr := verifyAddressLookupTablesRPC(ctx, s.cfg.RPCTransport, rpcCreds.PrimaryURL, q.AddressLookupTables); altErr != nil {
				return WalletReview{}, fmt.Errorf("%w: %v", ErrPolicyDenied, altErr)
			}
		}
		// Always populate recent blockhash from the signer's pinned RPC instead of trusting caller input.
		bh, bErr := s.fetchBlockhashOrFixture(ctx, w.ID, rpcCreds.PrimaryURL)
		if bErr != nil {
			return WalletReview{}, bErr
		}
		q.Blockhash = Base58Encode(bh[:])
		msgBytes, err = onchain.Message(q)
	default:
		if isMiningOperation(op) {
			bh, bErr := s.fetchBlockhashOrFixture(ctx, w.ID, rpcCreds.PrimaryURL)
			if bErr != nil {
				return WalletReview{}, bErr
			}
			msgBytes, err = buildMiningReviewMessage(feePayerPK, bh, priorityFeeMicro, op, miningDetails)
		}
	}
	if err != nil {
		return WalletReview{}, err
	}
	unsignedPkt := BuildUnsignedTransferPacket(msgBytes)
	simOK := true
	simErr := ""
	if !(s.cfg.FixtureMode && s.cfg.RPCTransport == nil) {
		simOK, simErr = simulateRPCMessage(ctx, s.cfg.RPCTransport, rpcCreds.PrimaryURL, unsignedPkt)
	}
	digestBytes := sha256.Sum256(msgBytes)
	revID, err := randomHexID("rev", 8)
	if err != nil {
		return WalletReview{}, err
	}
	nowSec := s.now().UTC().Unix()
	review := WalletReview{
		ID:                   revID,
		WalletID:             w.ID,
		Role:                 w.Role,
		Custody:              w.Custody,
		Operation:            op,
		Programs:             programs,
		Destination:          destAddr,
		Mint:                 mintAddr,
		OutputMint:           outputMintAddr,
		Symbol:               symbol,
		AmountRaw:            amountRaw,
		AmountHuman:          amountHuman,
		QuotedOutRaw:         quotedOutRaw,
		MinOutRaw:            minOutRaw,
		SlippageBps:          slippageBps,
		EstimatedUSD:         estimatedUSD,
		EstimatedFeeLamports: estFeeLamports,
		PolicyVersion:        w.Policy.Version,
		PolicyHash:           w.Policy.PolicyHash,
		ConnectionsHash:      ComputeConnectionsHash(w.Connections),
		TxDigest:             hex.EncodeToString(digestBytes[:]),
		UnsignedTxBase64:     base64.StdEncoding.EncodeToString(unsignedPkt),
		UnsignedMsgBase64:    base64.StdEncoding.EncodeToString(msgBytes),
		SimulationOK:         simOK,
		SimulationError:      simErr,
		IsBotOrAuto:          req.IsBotOrAuto,
		AgentID:              reqAgentID,
		SkillGrantID:         strings.TrimSpace(req.SkillGrantID),
		Venue:                reqVenue,
		MiningCluster:        miningDetails.Cluster,
		MiningRoundSlot:      miningDetails.RoundSlot,
		ExpiresAt:            nowSec + 90,
		Status:               ReviewStatusPrepared,
		CreatedAt:            nowSec,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return WalletReview{}, errors.New("custody store closed")
	}
	rawRev, _ := json.Marshal(review)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews (id, wallet_id, status, payload_json, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		review.ID, review.WalletID, review.Status, string(rawRev), review.ExpiresAt, review.CreatedAt,
	); err != nil {
		return WalletReview{}, err
	}
	if err := s.appendAuditLocked(ctx, w.ID, "review_prepared", "system", fmt.Sprintf("Prepared %s %s %s -> %s (simOK=%t)", review.ID, review.AmountHuman, review.Symbol, review.Destination, review.SimulationOK), w.Policy.PolicyHash); err != nil {
		return WalletReview{}, err
	}
	return review, nil
}

// ExecuteNativeReview signs an immutable review inside the signer after atomically reserving
// daily cap in SQLite, then broadcasts once and records durable state.
func (s *Service) ExecuteNativeReview(ctx context.Context, walletID, reviewID, actor string) (LedgerEntry, error) {
	return s.executePreparedReview(ctx, walletID, reviewID, "", firstStr(actor, "owner"))
}

// SubmitHardwareSignedReview verifies a hardware/Wallet-Standard signed transaction matches
// the prepared message byte-for-byte, reserves daily allowance atomically, and broadcasts once.
func (s *Service) SubmitHardwareSignedReview(ctx context.Context, walletID, reviewID, signedTxBase64, actor string) (LedgerEntry, error) {
	if strings.TrimSpace(signedTxBase64) == "" {
		return LedgerEntry{}, errors.New("signedTxBase64 required")
	}
	return s.executePreparedReview(ctx, walletID, reviewID, signedTxBase64, firstStr(actor, "hardware-device"))
}

func (s *Service) executePreparedReview(ctx context.Context, walletID, reviewID, hardwareSignedB64, actor string) (LedgerEntry, error) {
	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return LedgerEntry{}, errors.New("custody store closed")
	}
	w, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	if w.Policy.Frozen {
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: wallet is frozen", ErrPolicyDenied)
	}
	if gFrozen, gReason := s.isGlobalFrozenLocked(ctx); gFrozen {
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: global emergency freeze active: %s", ErrPolicyDenied, firstStr(gReason, "owner freeze"))
	}
	if s.hasUnresolvedBroadcastLocked(ctx, w.ID) {
		s.mu.Unlock()
		return LedgerEntry{}, ErrAmbiguousState
	}

	var revRaw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM reviews WHERE id = ? AND wallet_id = ?`, strings.TrimSpace(reviewID), w.ID).Scan(&revRaw); errors.Is(err, sql.ErrNoRows) {
		s.mu.Unlock()
		return LedgerEntry{}, ErrReviewNotFound
	} else if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	var review WalletReview
	if err := json.Unmarshal([]byte(revRaw), &review); err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	if review.Status != ReviewStatusPrepared {
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("review %s is %s, not prepared", review.ID, review.Status)
	}
	nowSec := s.now().UTC().Unix()
	if w.Policy.ExpiresAt > 0 && nowSec > w.Policy.ExpiresAt {
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: wallet policy expired", ErrPolicyDenied)
	}
	if review.ExpiresAt <= nowSec {
		s.mu.Unlock()
		return LedgerEntry{}, errors.New("review expired; prepare a new transaction")
	}
	if review.PolicyHash != w.Policy.PolicyHash {
		s.mu.Unlock()
		return LedgerEntry{}, ErrPolicyConflict
	}
	if review.ConnectionsHash != "" && review.ConnectionsHash != ComputeConnectionsHash(w.Connections) {
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: wallet connections or skill grants changed after review preparation", ErrPolicyConflict)
	}
	if !review.SimulationOK {
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: simulation failed: %s", ErrPolicyDenied, firstStr(review.SimulationError, "unknown error"))
	}
	if isMiningOperation(review.Operation) && review.MiningCluster == "mainnet-beta" {
		mPol := NormalizeMiningPolicy(w.Policy.Mining)
		vm, vmErr := s.getVerifiedMiningManifestLocked(ctx)
		if vmErr != nil || vm == nil || !vm.Compatible || mPol == nil || !mPol.AllowMainnet {
			s.mu.Unlock()
			return LedgerEntry{}, fmt.Errorf("%w: Mainnet LV1X execution requires verified compatible manifest and AllowMainnet policy", ErrPolicyDenied)
		}
		cl, ok := vm.Payload.Clusters["mainnet-beta"]
		if !ok || cl.Status != "active" || mPol.ProgramID != cl.ProgramID || mPol.LedgerID != cl.LedgerID {
			s.mu.Unlock()
			return LedgerEntry{}, fmt.Errorf("%w: Mainnet LV1X execution rejected by manifest gate", ErrPolicyDenied)
		}
		rpcCredsCheck, rErr := s.unsealRPCLocked(ctx, w.ID)
		if rErr != nil {
			s.mu.Unlock()
			return LedgerEntry{}, rErr
		}
		cachePrefix := fmt.Sprintf("mainnet-beta:%s:", mPol.ProgramID)
		cachedFresh := false
		nowSec := s.now().UTC().Unix()
		s.bpfVerifiedMu.Lock()
		for k, ts := range s.bpfVerified {
			if strings.HasPrefix(k, cachePrefix) && strings.HasSuffix(k, ":"+strings.ToLower(cl.ExpectedELFSHA256)) && nowSec-ts < 300 {
				cachedFresh = true
				break
			}
		}
		s.bpfVerifiedMu.Unlock()
		if !cachedFresh {
			s.mu.Unlock()
			if bErr := s.verifyMiningProgramOnChain(ctx, rpcCredsCheck.PrimaryURL, mPol, cl); bErr != nil {
				return LedgerEntry{}, fmt.Errorf("%w: live Mainnet BPF verification failed: %v", ErrPolicyDenied, bErr)
			}
			s.mu.Lock()
			if s.closed || s.db == nil {
				s.mu.Unlock()
				return LedgerEntry{}, errors.New("custody store closed")
			}
		}
	}

	msgBytes, err := base64.StdEncoding.DecodeString(review.UnsignedMsgBase64)
	if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, errors.New("corrupt prepared message bytes")
	}
	_, pubPK, err := ValidateSolanaPublicAddress(w.PublicKey)
	if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}

	var signedRawTx []byte
	var sigStr string
	if hardwareSignedB64 != "" {
		if w.Custody != CustodyHardware {
			s.mu.Unlock()
			return LedgerEntry{}, fmt.Errorf("%w: wallet is not hardware-backed", ErrRoleDenied)
		}
		sigStr, signedRawTx, err = VerifyHardwareSignedTransaction(msgBytes, pubPK, hardwareSignedB64)
		if err != nil {
			s.mu.Unlock()
			return LedgerEntry{}, err
		}
	} else {
		if w.Custody != CustodyNativeSigner {
			s.mu.Unlock()
			return LedgerEntry{}, fmt.Errorf("%w: wallet requires hardware signature submission", ErrRoleDenied)
		}
		var kn, kc []byte
		if err := s.db.QueryRowContext(ctx, `SELECT key_nonce, key_ciphertext FROM wallet_keys WHERE wallet_id = ?`, w.ID).Scan(&kn, &kc); err != nil {
			s.mu.Unlock()
			return LedgerEntry{}, err
		}
		seed, oErr := s.openBytes("custody-key:"+w.ID+":"+w.PublicKey, kn, kc)
		if oErr != nil {
			s.mu.Unlock()
			return LedgerEntry{}, oErr
		}
		priv := ed25519.NewKeyFromSeed(seed)
		clear(seed)
		signedRawTx, sigStr = SignLegacyMessage(msgBytes, priv)
		clear(priv)
	}

	rpcCreds, err := s.unsealRPCLocked(ctx, w.ID)
	if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}

	// Atomically reserve daily cap and mark review executed BEFORE broadcast, deriving day
	// from nowSec (matching entry.CreatedAt) to prevent UTC midnight rollover cap leaks.
	day := time.Unix(nowSec, 0).UTC().Format("2006-01-02")
	ledID, err := randomHexID("led", 8)
	if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	entry := LedgerEntry{
		ID:          ledID,
		ReviewID:    review.ID,
		WalletID:    w.ID,
		Operation:   review.Operation,
		Destination: review.Destination,
		Asset:       review.Symbol,
		AmountRaw:   review.AmountRaw,
		State:       LedgerReserved,
		TxSignature: sigStr,
		PolicyHash:  review.PolicyHash,
		CreatedAt:   nowSec,
		UpdatedAt:   nowSec,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO daily_usage(wallet_id, day_utc, spent_raw, reserved_raw) VALUES (?, ?, 0, 0)`, w.ID, day); err != nil {
		_ = tx.Rollback()
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	var spent, reserved uint64
	if err := tx.QueryRowContext(ctx, `SELECT spent_raw, reserved_raw FROM daily_usage WHERE wallet_id = ? AND day_utc = ?`, w.ID, day).Scan(&spent, &reserved); err != nil {
		_ = tx.Rollback()
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	debitRaw, dErr := reviewSOLDebit(review)
	if dErr != nil {
		_ = tx.Rollback()
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: %v", ErrPolicyDenied, dErr)
	}
	if spent > math.MaxUint64-reserved || spent+reserved > math.MaxUint64-debitRaw || w.Policy.SOLCap.MaxDailyRaw == 0 || spent+reserved+debitRaw > w.Policy.SOLCap.MaxDailyRaw {
		_ = tx.Rollback()
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: daily cap exhausted during atomic reservation", ErrPolicyDenied)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = reserved_raw + ? WHERE wallet_id = ? AND day_utc = ?`, debitRaw, w.ID, day); err != nil {
		_ = tx.Rollback()
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	if review.Operation == MiningOpCommit && review.AmountRaw > 0 {
		mPol := NormalizeMiningPolicy(w.Policy.Mining)
		if mPol == nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, fmt.Errorf("%w: missing mining policy on execution", ErrPolicyDenied)
		}
		if rErr := s.reserveMiningRoundTxLocked(ctx, tx, w.ID, firstStr(review.MiningCluster, mPol.Cluster), review.MiningRoundSlot, review.AmountRaw, mPol.MaxSOLPerRoundLamport); rErr != nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, rErr
		}
	}
	if review.Operation == MiningOpFallbackSettle {
		gasFee := review.EstimatedFeeLamports
		if gasFee == 0 {
			gasFee = 10_000
		}
		if fErr := s.reserveFallbackGasTxLocked(ctx, tx, w.ID, day, gasFee); fErr != nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, fErr
		}
	}
	splMint, splDebitRaw := reviewSPLDebit(review)
	if splMint != "" && splDebitRaw > 0 {
		tc, ok := findTokenCap(w.Policy.TokenCaps, splMint)
		if !ok || tc.MaxDailyRaw == 0 {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, fmt.Errorf("%w: SPL mint %s cap removed before execution", ErrPolicyDenied, splMint)
		}
		splAssetKey := "spl:" + splMint
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO daily_asset_usage(wallet_id, asset_key, day_utc, spent_raw, reserved_raw) VALUES (?, ?, ?, 0, 0)`, w.ID, splAssetKey, day); err != nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, err
		}
		var splSpent, splReserved uint64
		if err := tx.QueryRowContext(ctx, `SELECT spent_raw, reserved_raw FROM daily_asset_usage WHERE wallet_id = ? AND asset_key = ? AND day_utc = ?`, w.ID, splAssetKey, day).Scan(&splSpent, &splReserved); err != nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, err
		}
		if splSpent > math.MaxUint64-splReserved || splSpent+splReserved > math.MaxUint64-splDebitRaw || splSpent+splReserved+splDebitRaw > tc.MaxDailyRaw {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, fmt.Errorf("%w: daily SPL token cap exhausted during atomic reservation", ErrPolicyDenied)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE daily_asset_usage SET reserved_raw = reserved_raw + ? WHERE wallet_id = ? AND asset_key = ? AND day_utc = ?`, splDebitRaw, w.ID, splAssetKey, day); err != nil {
			_ = tx.Rollback()
			s.mu.Unlock()
			return LedgerEntry{}, err
		}
	}
	review.Status = ReviewStatusExecuted
	revBytes, _ := json.Marshal(review)
	if _, err := tx.ExecContext(ctx, `UPDATE reviews SET status = ?, payload_json = ? WHERE id = ?`, review.Status, string(revBytes), review.ID); err != nil {
		_ = tx.Rollback()
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	ledBytes, _ := json.Marshal(entry)
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger (id, review_id, wallet_id, state, payload_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.ReviewID, entry.WalletID, entry.State, string(ledBytes), entry.CreatedAt, entry.UpdatedAt,
	); err != nil {
		_ = tx.Rollback()
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	s.mu.Unlock()

	// Network broadcast runs outside DB mutex.
	nextState := LedgerBroadcast
	var sendErr error
	if s.cfg.FixtureMode && s.cfg.RPCTransport == nil {
		nextState = LedgerConfirmed
	} else {
		rpcSig, bErr := sendSignedRPCTx(ctx, s.cfg.RPCTransport, rpcCreds.PrimaryURL, signedRawTx)
		if bErr != nil {
			sendErr = bErr
			nextState = LedgerUnknown
		} else if rpcSig != "" {
			sigStr = rpcSig
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return entry, errors.New("custody store closed")
	}
	entry.State = nextState
	entry.TxSignature = sigStr
	entry.UpdatedAt = s.now().UTC().Unix()
	if sendErr != nil {
		entry.Error = sendErr.Error()
	}
	postTx, pErr := s.db.BeginTx(ctx, nil)
	if pErr != nil {
		return entry, pErr
	}
	defer func() { _ = postTx.Rollback() }()
	if nextState == LedgerConfirmed {
		if _, err := postTx.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = MAX(0, reserved_raw - ?), spent_raw = spent_raw + ? WHERE wallet_id = ? AND day_utc = ?`, debitRaw, debitRaw, w.ID, day); err != nil {
			return entry, err
		}
		if splMint != "" && splDebitRaw > 0 {
			if _, err := postTx.ExecContext(ctx, `UPDATE daily_asset_usage SET reserved_raw = MAX(0, reserved_raw - ?), spent_raw = spent_raw + ? WHERE wallet_id = ? AND asset_key = ? AND day_utc = ?`, splDebitRaw, splDebitRaw, w.ID, "spl:"+splMint, day); err != nil {
				return entry, err
			}
		}
	}
	if review.Operation == MiningOpCommit && review.AmountRaw > 0 && (nextState == LedgerConfirmed || nextState == LedgerFailed) {
		cluster := firstStr(review.MiningCluster, "devnet")
		if err := s.settleMiningRoundTxLocked(ctx, postTx, w.ID, cluster, review.MiningRoundSlot, review.AmountRaw, nextState == LedgerConfirmed); err != nil {
			return entry, err
		}
	}
	if review.Operation == MiningOpFallbackSettle && (nextState == LedgerConfirmed || nextState == LedgerFailed) {
		gasFee := review.EstimatedFeeLamports
		if gasFee == 0 {
			gasFee = 10_000
		}
		if err := s.settleFallbackGasTxLocked(ctx, postTx, w.ID, day, gasFee, nextState == LedgerConfirmed); err != nil {
			return entry, err
		}
	}
	updatedBytes, _ := json.Marshal(entry)
	if _, err := postTx.ExecContext(ctx, `UPDATE ledger SET state = ?, payload_json = ?, updated_at = ? WHERE id = ?`, entry.State, string(updatedBytes), entry.UpdatedAt, entry.ID); err != nil {
		return entry, err
	}
	if err := s.appendAuditExecLocked(ctx, postTx, w.ID, "tx_"+nextState, actor, fmt.Sprintf("%s %s %s -> %s sig=%s", review.ID, review.AmountHuman, review.Symbol, review.Destination, entry.TxSignature), review.PolicyHash); err != nil {
		return entry, err
	}
	if err := postTx.Commit(); err != nil {
		return entry, err
	}
	return entry, sendErr
}

// ReconcileLedgerEntry checks getSignatureStatuses on the signer execution RPC and transitions
// broadcast/unknown entries cleanly without rebroadcasting.
func (s *Service) ReconcileLedgerEntry(ctx context.Context, walletID, entryID string) (LedgerEntry, error) {
	s.mu.Lock()
	w, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM ledger WHERE id = ? AND wallet_id = ?`, strings.TrimSpace(entryID), w.ID).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		s.mu.Unlock()
		return LedgerEntry{}, ErrLedgerNotFound
	} else if err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	var entry LedgerEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		s.mu.Unlock()
		return LedgerEntry{}, err
	}
	if entry.State == LedgerConfirmed || entry.State == LedgerFailed {
		s.mu.Unlock()
		return entry, nil
	}
	rpcCreds, err := s.unsealRPCLocked(ctx, w.ID)
	s.mu.Unlock()
	if err != nil {
		return LedgerEntry{}, err
	}

	nextState := LedgerConfirmed
	statusErr := ""
	var checkErr error
	if !(s.cfg.FixtureMode && s.cfg.RPCTransport == nil) {
		nextState, statusErr, checkErr = checkSignatureStatusRPC(ctx, s.cfg.RPCTransport, rpcCreds.PrimaryURL, entry.TxSignature)
		if checkErr != nil && rpcCreds.FallbackURL != "" {
			if fbState, fbStatusErr, fbErr := checkSignatureStatusRPC(ctx, s.cfg.RPCTransport, rpcCreds.FallbackURL, entry.TxSignature); fbErr == nil {
				nextState, statusErr, checkErr = fbState, fbStatusErr, nil
			}
		}
		if checkErr != nil {
			return entry, checkErr
		}
		if nextState == LedgerFailed {
			witnessURL := strings.TrimSpace(rpcCreds.FallbackURL)
			if witnessURL == "" {
				if pubWitness := WitnessOriginForCluster(w.RPC.Cluster); pubWitness != "" && pubWitness != w.RPC.Host {
					witnessURL = pubWitness
				}
			}
			if witnessURL == "" {
				return entry, errors.New("no independent witness RPC configured to corroborate failure; keeping cap reservation")
			}
			wState, wStatusErr, wErr := checkSignatureStatusRPC(ctx, s.cfg.RPCTransport, witnessURL, entry.TxSignature)
			if wErr != nil {
				return entry, errors.New("independent witness RPC unreachable to corroborate failure; keeping cap reservation")
			}
			if wState == LedgerConfirmed {
				// Witness confirms the tx actually landed on-chain; treat as confirmed & spent!
				nextState = LedgerConfirmed
				statusErr = wStatusErr
			} else if wState != LedgerFailed {
				return entry, errors.New("independent witness RPC did not corroborate transaction failure; keeping reservation")
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return entry, errors.New("custody store closed")
	}
	reservedAmount := uint64(5_000)
	var splMint string
	var splReservedAmount uint64
	var revPayloadRaw string
	if s.db.QueryRowContext(ctx, `SELECT payload_json FROM reviews WHERE id = ? AND wallet_id = ?`, entry.ReviewID, w.ID).Scan(&revPayloadRaw) == nil {
		var rev WalletReview
		if json.Unmarshal([]byte(revPayloadRaw), &rev) == nil {
			if amt, err := reviewSOLDebit(rev); err == nil {
				reservedAmount = amt
			}
			splMint, splReservedAmount = reviewSPLDebit(rev)
		}
	} else if entry.Operation == "solana.nativeTransfer" && entry.AmountRaw <= math.MaxUint64-5_000 {
		reservedAmount = entry.AmountRaw + 5_000
	}
	day := time.Unix(entry.CreatedAt, 0).UTC().Format("2006-01-02")
	tx, txErr := s.db.BeginTx(ctx, nil)
	if txErr != nil {
		return entry, txErr
	}
	defer func() { _ = tx.Rollback() }()
	entry.State = nextState
	entry.Error = statusErr
	entry.UpdatedAt = s.now().UTC().Unix()
	b, _ := json.Marshal(entry)
	updRes, err := tx.ExecContext(ctx, `UPDATE ledger SET state = ?, payload_json = ?, updated_at = ? WHERE id = ? AND state NOT IN (?, ?)`, entry.State, string(b), entry.UpdatedAt, entry.ID, LedgerConfirmed, LedgerFailed)
	if err != nil {
		return entry, err
	}
	affected, _ := updRes.RowsAffected()
	if affected == 0 {
		// Another concurrent reconcile already finalized this entry; return its committed state without double-adjusting caps.
		var curRaw string
		if sErr := tx.QueryRowContext(ctx, `SELECT payload_json FROM ledger WHERE id = ? AND wallet_id = ?`, entry.ID, w.ID).Scan(&curRaw); sErr == nil {
			var curEntry LedgerEntry
			if json.Unmarshal([]byte(curRaw), &curEntry) == nil {
				return curEntry, nil
			}
		}
		return entry, nil
	}
	switch nextState {
	case LedgerConfirmed:
		if _, err := tx.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = MAX(0, reserved_raw - ?), spent_raw = spent_raw + ? WHERE wallet_id = ? AND day_utc = ?`, reservedAmount, reservedAmount, w.ID, day); err != nil {
			return entry, err
		}
		if splMint != "" && splReservedAmount > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE daily_asset_usage SET reserved_raw = MAX(0, reserved_raw - ?), spent_raw = spent_raw + ? WHERE wallet_id = ? AND asset_key = ? AND day_utc = ?`, splReservedAmount, splReservedAmount, w.ID, "spl:"+splMint, day); err != nil {
				return entry, err
			}
		}
	case LedgerFailed:
		if _, err := tx.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = MAX(0, reserved_raw - ?) WHERE wallet_id = ? AND day_utc = ?`, reservedAmount, w.ID, day); err != nil {
			return entry, err
		}
		if splMint != "" && splReservedAmount > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE daily_asset_usage SET reserved_raw = MAX(0, reserved_raw - ?) WHERE wallet_id = ? AND asset_key = ? AND day_utc = ?`, splReservedAmount, w.ID, "spl:"+splMint, day); err != nil {
				return entry, err
			}
		}
	}
	if entry.Operation == MiningOpFallbackSettle && (nextState == LedgerConfirmed || nextState == LedgerFailed) {
		var fallbackGas uint64 = 10_000
		if revPayloadRaw != "" {
			var rev WalletReview
			if json.Unmarshal([]byte(revPayloadRaw), &rev) == nil && rev.EstimatedFeeLamports > 0 {
				fallbackGas = rev.EstimatedFeeLamports
			}
		}
		if err := s.settleFallbackGasTxLocked(ctx, tx, w.ID, day, fallbackGas, nextState == LedgerConfirmed); err != nil {
			return entry, err
		}
	}
	if err := s.appendAuditExecLocked(ctx, tx, w.ID, "tx_reconciled", "owner", fmt.Sprintf("Reconciled %s -> %s", entry.ID, entry.State), entry.PolicyHash); err != nil {
		return entry, err
	}
	if err := tx.Commit(); err != nil {
		return entry, err
	}
	return entry, nil
}

// ListActivity returns recent prepared reviews, durable ledger entries, and signer audit logs.
func (s *Service) ListActivity(ctx context.Context, walletID string) (reviews []WalletReview, ledger []LedgerEntry, audit []AuditEntry, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		return nil, nil, nil, err
	}
	reviews = []WalletReview{}
	if rows, qErr := s.db.QueryContext(ctx, `SELECT payload_json FROM reviews WHERE wallet_id = ? ORDER BY created_at DESC LIMIT 30`, w.ID); qErr == nil {
		for rows.Next() {
			var r string
			var item WalletReview
			if rows.Scan(&r) == nil && json.Unmarshal([]byte(r), &item) == nil {
				reviews = append(reviews, item)
			}
		}
		_ = rows.Close()
	}
	ledger = []LedgerEntry{}
	if rows, qErr := s.db.QueryContext(ctx, `SELECT payload_json FROM ledger WHERE wallet_id = ? ORDER BY created_at DESC LIMIT 50`, w.ID); qErr == nil {
		for rows.Next() {
			var r string
			var item LedgerEntry
			if rows.Scan(&r) == nil && json.Unmarshal([]byte(r), &item) == nil {
				ledger = append(ledger, item)
			}
		}
		_ = rows.Close()
	}
	audit = []AuditEntry{}
	if rows, qErr := s.db.QueryContext(ctx, `SELECT payload_json FROM audit_log WHERE wallet_id = ? ORDER BY timestamp DESC LIMIT 50`, w.ID); qErr == nil {
		for rows.Next() {
			var r string
			var item AuditEntry
			if rows.Scan(&r) == nil && json.Unmarshal([]byte(r), &item) == nil {
				audit = append(audit, item)
			}
		}
		_ = rows.Close()
	}
	return reviews, ledger, audit, nil
}

func recoveryV2AAD(pkg RecoveryPackage) []byte {
	return []byte(fmt.Sprintf("%s:v2:%s:%s:%s:%s:%d:%d:%d",
		pkg.Format, pkg.WalletID, pkg.Role, pkg.PublicKey, pkg.KDF, pkg.KDFTime, pkg.KDFMemory, pkg.KDFThreads))
}

// ExportEncryptedRecovery seals the native signer key into an Argon2id+AES-GCM recovery archive + SHA-256 checksum.
func (s *Service) ExportEncryptedRecovery(ctx context.Context, walletID, passphrase, actor string) (archive []byte, fingerprint string, err error) {
	if len(strings.TrimSpace(passphrase)) < recoveryMinPass {
		return nil, "", fmt.Errorf("recovery passphrase must be at least %d characters", recoveryMinPass)
	}
	s.mu.Lock()
	if s.closed || s.db == nil {
		s.mu.Unlock()
		return nil, "", errors.New("custody store closed")
	}
	w, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		s.mu.Unlock()
		return nil, "", err
	}
	if w.Custody != CustodyNativeSigner {
		s.mu.Unlock()
		return nil, "", fmt.Errorf("%w: only native signer wallets have an exportable key", ErrRoleDenied)
	}
	var kn, kc []byte
	if err := s.db.QueryRowContext(ctx, `SELECT key_nonce, key_ciphertext FROM wallet_keys WHERE wallet_id = ?`, w.ID).Scan(&kn, &kc); err != nil {
		s.mu.Unlock()
		return nil, "", err
	}
	seed, err := s.openBytes("custody-key:"+w.ID+":"+w.PublicKey, kn, kc)
	kdfT, kdfM, kdfP := s.kdfTime, s.kdfMemory, s.kdfThreads
	s.mu.Unlock()
	if err != nil {
		return nil, "", err
	}
	defer clear(seed)

	salt, err := vault.RandomBytes(16)
	if err != nil {
		return nil, "", err
	}
	nonce, err := vault.RandomBytes(12)
	if err != nil {
		return nil, "", err
	}
	if err := s.acquireKDFSlot(ctx); err != nil {
		return nil, "", err
	}
	// Run Argon2id outside s.mu so concurrent signer RPCs never stall.
	key := vault.DeriveKey(passphrase, salt, kdfT, kdfM, kdfP)
	s.releaseKDFSlot()
	defer clear(key)
	pkg := RecoveryPackage{
		Version:    2,
		Format:     recoveryFormat,
		WalletID:   w.ID,
		Role:       w.Role,
		PublicKey:  w.PublicKey,
		KDF:        "argon2id",
		KDFTime:    kdfT,
		KDFMemory:  kdfM,
		KDFThreads: kdfP,
		Salt:       salt,
		Nonce:      nonce,
	}
	ct, err := vault.SealAESGCM(key, nonce, seed, recoveryV2AAD(pkg))
	if err != nil {
		return nil, "", err
	}
	pkg.Ciphertext = ct
	archive, err = json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(archive)
	fp := hex.EncodeToString(sum[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return nil, "", errors.New("custody store closed")
	}
	if err := s.appendAuditLocked(ctx, w.ID, "encrypted_recovery_exported", firstStr(actor, "owner"), "Exported Argon2id v2 encrypted backup fp="+fp[:16], w.Policy.PolicyHash); err != nil {
		return nil, "", err
	}
	return archive, fp, nil
}

// ImportEncryptedRecovery decrypts an Argon2id RecoveryPackage inside signer and imports the wallet.
func (s *Service) ImportEncryptedRecovery(ctx context.Context, archiveRaw []byte, passphrase, name, role, primaryRPCURL string) (WalletPublic, error) {
	if len(archiveRaw) == 0 || len(archiveRaw) > 64*1024 {
		return WalletPublic{}, errors.New("invalid recovery archive size")
	}
	var pkg RecoveryPackage
	if err := json.Unmarshal(archiveRaw, &pkg); err != nil || (pkg.Version != 1 && pkg.Version != 2) || pkg.Format != recoveryFormat {
		return WalletPublic{}, errors.New("unsupported encrypted recovery package")
	}
	if pkg.KDF != "argon2id" || len(pkg.Salt) != 16 || len(pkg.Nonce) != 12 {
		return WalletPublic{}, errors.New("invalid recovery KDF parameters")
	}
	if pkg.KDFTime < 1 || pkg.KDFTime > 8 || pkg.KDFThreads < 1 || pkg.KDFThreads > 8 {
		return WalletPublic{}, errors.New("recovery KDF time/threads out of safe bounds")
	}
	minMem := uint32(16384)
	if s.cfg.FastKDF {
		minMem = 64
	}
	if pkg.KDFMemory < minMem || pkg.KDFMemory > 262144 {
		return WalletPublic{}, errors.New("recovery KDF memory out of safe bounds")
	}
	if err := s.acquireKDFSlot(ctx); err != nil {
		return WalletPublic{}, err
	}
	key := vault.DeriveKey(passphrase, pkg.Salt, pkg.KDFTime, pkg.KDFMemory, pkg.KDFThreads)
	s.releaseKDFSlot()
	defer clear(key)
	var aad []byte
	if pkg.Version == 2 {
		aad = recoveryV2AAD(pkg)
	} else {
		aad = []byte(recoveryFormat + ":" + pkg.PublicKey)
	}
	seed, err := vault.OpenAESGCM(key, pkg.Nonce, pkg.Ciphertext, aad)
	if err != nil || len(seed) != ed25519.SeedSize {
		return WalletPublic{}, ErrWrongPassword
	}
	defer clear(seed)
	priv := ed25519.NewKeyFromSeed(seed)
	derivedPub := Base58Encode(priv[32:])
	clear(priv)
	if derivedPub != pkg.PublicKey {
		return WalletPublic{}, errors.New("recovered keypair does not match package public key")
	}
	targetRole := strings.ToLower(strings.TrimSpace(role))
	if targetRole == "" {
		targetRole = pkg.Role
	}
	// Legacy v1 backups did not bind pkg.Role inside AES-GCM AAD; refuse non-vault target roles
	// on v1 imports to prevent plaintext role-downgrade attacks against Vault backups.
	if pkg.Version == 1 && targetRole != RoleVault {
		return WalletPublic{}, fmt.Errorf("%w: legacy v1 recovery archive lacks authenticated role metadata and may only be restored into role %q", ErrRoleDenied, RoleVault)
	}
	if pkg.Role == RoleVault && targetRole != RoleVault {
		return WalletPublic{}, fmt.Errorf("%w: vault recovery backup cannot be restored into role %q", ErrRoleDenied, targetRole)
	}
	if !ValidRole(targetRole) || targetRole == RoleWatch {
		return WalletPublic{}, errors.New("invalid restored wallet role")
	}
	targetName := strings.TrimSpace(name)
	if targetName == "" {
		targetName = "Restored " + targetRole
	}
	if err := s.checkRPCURLsAllowed(primaryRPCURL, ""); err != nil {
		return WalletPublic{}, err
	}
	rpcStatus, canonRPC, _, err := VerifyAndPinRPC(ctx, s.cfg.RPCTransport, primaryRPCURL, "", "", s.cfg.FixtureMode)
	if err != nil {
		return WalletPublic{}, err
	}
	return s.insertWalletRecord(ctx, targetName, CustodyNativeSigner, targetRole, derivedPub, seed, rpcStatus, canonRPC, "", "owner-restore")
}

// ImportSolanaKeypairFile is strictly for native Local owner/CLI use: reads a 0o600 regular
// non-symlink single-link Solana CLI keypair JSON file directly into signer custody.
func (s *Service) ImportSolanaKeypairFile(ctx context.Context, absPath, name, role, primaryRPCURL string) (WalletPublic, error) {
	clean := strings.TrimSpace(absPath)
	if !filepath.IsAbs(clean) || filepath.Clean(clean) != clean {
		return WalletPublic{}, errors.New("--file must be a clean absolute path")
	}
	raw, err := readPrivateRegularFile(clean, 16*1024)
	if err != nil {
		return WalletPublic{}, err
	}
	priv, pubB58, err := ParseSolanaKeypair64(raw)
	clear(raw)
	if err != nil {
		return WalletPublic{}, err
	}
	seed := append([]byte(nil), priv.Seed()...)
	clear(priv)
	defer clear(seed)

	cleanRole := strings.ToLower(strings.TrimSpace(role))
	if !ValidRole(cleanRole) || cleanRole == RoleWatch {
		return WalletPublic{}, errors.New("role must be agent, trading, vault, or mining")
	}
	if err := s.checkRPCURLsAllowed(primaryRPCURL, ""); err != nil {
		return WalletPublic{}, err
	}
	rpcStatus, canonRPC, _, err := VerifyAndPinRPC(ctx, s.cfg.RPCTransport, primaryRPCURL, "", "", s.cfg.FixtureMode)
	if err != nil {
		return WalletPublic{}, err
	}
	return s.insertWalletRecord(ctx, firstStr(name, "Imported "+cleanRole), CustodyNativeSigner, cleanRole, pubB58, seed, rpcStatus, canonRPC, "", "owner-cli-import")
}

// ExportRawKeypairFile is strictly for native Local owner/CLI use (never exposed over HTTP):
// writes an atomic O_CREATE|O_EXCL 0o600 64-byte Solana CLI keypair JSON file after explicit
// custody-reduction acknowledgement and logs an immutable audit event.
func (s *Service) ExportRawKeypairFile(ctx context.Context, walletID, absOutPath string, acknowledgeCustodyReduction bool) error {
	if !acknowledgeCustodyReduction {
		return errors.New("--acknowledge-custody-reduction is required for raw keypair export")
	}
	clean := strings.TrimSpace(absOutPath)
	if !filepath.IsAbs(clean) || filepath.Clean(clean) != clean {
		return errors.New("--output must be a clean absolute path")
	}
	if _, err := os.Lstat(clean); err == nil {
		return errors.New("output file already exists; choose a new path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		return err
	}
	if w.Custody != CustodyNativeSigner {
		return fmt.Errorf("%w: only native signer wallets have raw key material", ErrRoleDenied)
	}
	var kn, kc []byte
	if err := s.db.QueryRowContext(ctx, `SELECT key_nonce, key_ciphertext FROM wallet_keys WHERE wallet_id = ?`, w.ID).Scan(&kn, &kc); err != nil {
		return err
	}
	seed, err := s.openBytes("custody-key:"+w.ID+":"+w.PublicKey, kn, kc)
	if err != nil {
		return err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	clear(seed)
	defer clear(priv)

	nums := make([]int, len(priv))
	for i, by := range priv {
		nums[i] = int(by)
	}
	rawJSON, _ := json.Marshal(nums)
	rawJSON = append(rawJSON, '\n')
	defer clear(rawJSON)

	f, err := os.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, wErr := f.Write(rawJSON); wErr != nil {
		_ = f.Close()
		_ = os.Remove(clean)
		return wErr
	}
	if sErr := f.Sync(); sErr != nil {
		_ = f.Close()
		_ = os.Remove(clean)
		return sErr
	}
	_ = f.Close()
	s.appendAuditLocked(ctx, w.ID, "raw_keypair_exported", "owner-cli", "Exported 0600 raw Solana CLI keypair with custody-reduction ack", w.Policy.PolicyHash)
	return nil
}

// DeleteWallet removes a wallet entry when no unresolved broadcast/unknown tx remains.
func (s *Service) DeleteWallet(ctx context.Context, walletID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.db == nil {
		return false, errors.New("custody store closed")
	}
	w, err := s.getWalletLocked(ctx, walletID)
	if errors.Is(err, ErrWalletNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if s.hasUnresolvedBroadcastLocked(ctx, w.ID) {
		return false, ErrAmbiguousState
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM wallets WHERE id = ?`, w.ID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		if err := s.appendAuditExecLocked(ctx, tx, w.ID, "wallet_deleted", "owner", "Removed wallet "+w.PublicKey, w.Policy.PolicyHash); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Sanity helper for tests / static checks so bytes.Equal stays imported cleanly.
var _ = bytes.Equal

func firstStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}
