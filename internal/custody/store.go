package custody

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	DataDir      string
	FixtureMode  bool
	FastKDF      bool
	RequireOSKEK bool
	KEKSource    providersecrets.Source // Optional override in tests
	SocketPath   string                 // Optional explicit socket path reported in status
	RPCTransport http.RoundTripper      // Tests only
	Now          func() time.Time
}

// Service owns private wallet keys, encrypted execution RPCs, fail-closed policies,
// immutable transaction reviews, and the durable broadcast ledger.
type Service struct {
	dir           string
	db            *sql.DB
	masterKey     []byte
	kekProtection string
	cfg           Config
	now           func() time.Time
	mu            sync.Mutex
	closed        bool
	kdfTime       uint32
	kdfMemory     uint32
	kdfThreads    uint8
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
	return &Service{
		dir:           dir,
		db:            db,
		masterKey:     masterKey,
		kekProtection: kekProt,
		cfg:           cfg,
		now:           nowFn,
		kdfTime:       kdfT,
		kdfMemory:     kdfM,
		kdfThreads:    kdfP,
	}, nil
}

// SecurityStatus reports KEK protection and signer process isolation posture.
func (s *Service) SecurityStatus(_ context.Context) (SecurityStatus, error) {
	return SecurityStatus{
		ProcessIsolated: false,
		KEKProtection:   firstStr(s.kekProtection, KEKFileDevOnly),
		SocketPath:      s.cfg.SocketPath,
		AllowedPeerUID:  os.Getuid(),
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

func computeAuditEntryHash(prevHash string, entry AuditEntry) string {
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

func (s *Service) appendAuditLocked(ctx context.Context, walletID, action, actor, summary, policyHash string) {
	id, err := randomHexID("aud", 8)
	if err != nil {
		return
	}
	var prevHash string
	var prevRaw string
	if err := s.db.QueryRowContext(ctx, `SELECT payload_json FROM audit_log ORDER BY rowid DESC LIMIT 1`).Scan(&prevRaw); err == nil && prevRaw != "" {
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
	entry.EntryHash = computeAuditEntryHash(prevHash, entry)
	raw, _ := json.Marshal(entry)
	_, _ = s.db.ExecContext(ctx,
		`INSERT INTO audit_log(id, wallet_id, payload_json, timestamp) VALUES (?, ?, ?, ?)`,
		entry.ID, entry.WalletID, string(raw), entry.Timestamp,
	)
}

// VerifyAuditChain walks the tamper-evident audit log in insertion order and verifies every SHA-256 link.
func (s *Service) VerifyAuditChain(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM audit_log ORDER BY rowid ASC`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prevHash := ""
	count := 0
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
		expected := computeAuditEntryHash(prevHash, e)
		if e.EntryHash != expected {
			return count, fmt.Errorf("audit entry %s hash tampered", e.ID)
		}
		prevHash = e.EntryHash
		count++
	}
	return count, rows.Err()
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
	if cleanCustody == CustodyHardware && (cleanRole == RoleAgent || cleanRole == RoleMining) {
		return WalletPublic{}, errors.New("hardware wallets require manual/trading review; assign vault or trading role")
	}
	cleanName := strings.TrimSpace(name)
	if cleanName == "" {
		if cleanCustody == CustodyHardware {
			cleanName = "Hardware Vault"
		} else {
			cleanName = "Watch Wallet"
		}
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
	if err := tx.Commit(); err != nil {
		return WalletPublic{}, err
	}
	s.appendAuditLocked(ctx, id, "wallet_created", actor, fmt.Sprintf("Created %s %s wallet (%s) pinned to %s", role, custodyType, pubB58, rpcStatus.Cluster), pol.PolicyHash)
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

	// Validate destinations and programs format when provided.
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

	if _, err := s.db.ExecContext(ctx,
		`UPDATE wallets SET policy_json = ?, policy_acknowledged = 1, updated_at = ? WHERE id = ?`,
		string(polBytes), nowSec, cur.ID,
	); err != nil {
		return WalletPublic{}, err
	}
	kind := "policy_tightened"
	if !tightening {
		kind = "policy_updated"
	}
	s.appendAuditLocked(ctx, cur.ID, kind, firstStr(actor, "owner"), fmt.Sprintf("Policy v%d applied (mode=%s frozen=%t)", next.Version, next.ApprovalMode, next.Frozen), next.PolicyHash)
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
	if _, err := s.db.ExecContext(ctx,
		`UPDATE wallets SET policy_json = ?, updated_at = ? WHERE id = ?`,
		string(polBytes), nowSec, cur.ID,
	); err != nil {
		return WalletPublic{}, err
	}
	action := "wallet_frozen"
	if !frozen {
		action = "wallet_unfrozen"
	}
	s.appendAuditLocked(ctx, cur.ID, action, firstStr(actor, "owner"), fmt.Sprintf("%s: %s", action, cur.Policy.FreezeReason), cur.Policy.PolicyHash)
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
	if _, err := s.db.ExecContext(ctx,
		`UPDATE wallets SET connections_json = ?, updated_at = ? WHERE id = ?`,
		string(conBytes), nowSec, cur.ID,
	); err != nil {
		return WalletPublic{}, err
	}
	s.appendAuditLocked(ctx, cur.ID, "connections_updated", firstStr(actor, "owner"), fmt.Sprintf("agents=%v manual=%t bots=%t venues=%v", conn.AgentIDs, conn.AllowTradeManual, conn.AllowTradeBots, conn.AllowedVenues), cur.Policy.PolicyHash)
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

	nextStatus, canonPrimary, canonFallback, err := VerifyAndPinRPC(ctx, s.cfg.RPCTransport, primaryURL, fallbackURL, cur.RPC.GenesisHash, s.cfg.FixtureMode)
	if err != nil {
		return WalletPublic{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
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
	if err := tx.Commit(); err != nil {
		return WalletPublic{}, err
	}
	s.appendAuditLocked(ctx, cur2.ID, "rpc_updated", firstStr(actor, "owner"), fmt.Sprintf("RPC v%d -> %s (%s)", nextStatus.Version, nextStatus.Host, nextStatus.Cluster), cur2.Policy.PolicyHash)
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
	if s.cfg.FixtureMode && s.cfg.RPCTransport == nil {
		lamports = 2_500_000_000
		latency = 3
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
	}
	return BalanceResult{
		WalletID:  w.ID,
		PublicKey: w.PublicKey,
		Cluster:   w.RPC.Cluster,
		RPCHost:   w.RPC.Host,
		Lamports:  lamports,
		SOL:       FormatLamportsToSOL(lamports),
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
		if sel.IsAutonomous && (w.Role == RoleVault || w.Role == RoleMining || !w.Connections.AllowTradeBots) {
			return fmt.Errorf("%w: wallet %s is not authorized for autonomous execution", ErrRoleDenied, w.Handle)
		}
		if sel.Venue != "" && len(w.Connections.AllowedVenues) > 0 && !slices.Contains(w.Connections.AllowedVenues, strings.ToLower(strings.TrimSpace(sel.Venue))) {
			return fmt.Errorf("%w: wallet %s is not connected to venue %s", ErrPolicyDenied, w.Handle, sel.Venue)
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
		`SELECT COUNT(1) FROM ledger WHERE wallet_id = ? AND state IN (?, ?)`,
		walletID, LedgerBroadcast, LedgerUnknown,
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
	case "solana.nativeTransfer":
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
	switch op {
	case "solana.nativeTransfer", "solana.splTransferChecked", "jupiter.swap":
	default:
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
	if s.hasUnresolvedBroadcastLocked(ctx, w.ID) {
		s.mu.Unlock()
		return WalletReview{}, ErrAmbiguousState
	}
	if w.Policy.ExpiresAt > 0 && s.now().UTC().Unix() > w.Policy.ExpiresAt {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: wallet policy expired", ErrPolicyDenied)
	}
	if req.IsBotOrAuto && (w.Role == RoleVault || w.Role == RoleMining || w.Policy.ApprovalMode == ApprovalManualReview || !w.Connections.AllowTradeBots) {
		s.mu.Unlock()
		return WalletReview{}, fmt.Errorf("%w: wallet prohibits autonomous execution", ErrRoleDenied)
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
		mintPK         [32]byte
		symbol         = "SOL"
		amountRaw      = req.AmountRaw
		amountHuman    string
		decimals       uint8  = 9
		estFeeLamports uint64 = 5_000
		programs       []string
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
		if w.Policy.SOLCap.MaxPerTxRaw < estFeeLamports ||
			w.Policy.SOLCap.MaxDailyRaw == 0 ||
			w.DailySpentLamports+w.DailyReservedLamports+estFeeLamports > w.Policy.SOLCap.MaxDailyRaw {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: fee lamports exceed remaining SOL cap", ErrPolicyDenied)
		}
		symbol = firstStr(tc.Symbol, mintAddr[:6])
		amountHuman = FormatRawAmount(amountRaw, decimals)
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
		if w.Policy.MaxSlippageBps == 0 || uint32(q.SlippageBPS) > w.Policy.MaxSlippageBps {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap slippage %d bps exceeds max %d bps", ErrPolicyDenied, q.SlippageBPS, w.Policy.MaxSlippageBps)
		}
		if req.OutputMint != "" && q.OutputMint != strings.TrimSpace(req.OutputMint) {
			s.mu.Unlock()
			return WalletReview{}, fmt.Errorf("%w: swap output mint mismatch", ErrPolicyDenied)
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
		if q.FeeLamports > estFeeLamports {
			estFeeLamports = q.FeeLamports
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
		programs = []string{SolanaComputeBudget, SolanaJupiterV6Program}
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

	var msgBytes []byte
	switch op {
	case "solana.nativeTransfer":
		bh, bErr := s.fetchBlockhashOrFixture(ctx, w.ID, rpcCreds.PrimaryURL)
		if bErr != nil {
			return WalletReview{}, bErr
		}
		msgBytes, err = onchain.BuildSolanaTransferMessage(feePayerPK, destPK, bh, amountRaw, 50_000, 1_000)
	case "solana.splTransferChecked":
		bh, bErr := s.fetchBlockhashOrFixture(ctx, w.ID, rpcCreds.PrimaryURL)
		if bErr != nil {
			return WalletReview{}, bErr
		}
		msgBytes, err = onchain.BuildSPLTransferMessage(feePayerPK, destPK, mintPK, bh, amountRaw, decimals, req.CreateDestATA, 100_000, 1_000)
	case "jupiter.swap":
		q := *req.JupiterQuote
		q.Owner = w.PublicKey
		if strings.TrimSpace(q.Blockhash) == "" {
			bh, bErr := s.fetchBlockhashOrFixture(ctx, w.ID, rpcCreds.PrimaryURL)
			if bErr != nil {
				return WalletReview{}, bErr
			}
			q.Blockhash = Base58Encode(bh[:])
		}
		msgBytes, err = onchain.Message(q)
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
		Symbol:               symbol,
		AmountRaw:            amountRaw,
		AmountHuman:          amountHuman,
		EstimatedFeeLamports: estFeeLamports,
		PolicyVersion:        w.Policy.Version,
		PolicyHash:           w.Policy.PolicyHash,
		TxDigest:             hex.EncodeToString(digestBytes[:]),
		UnsignedTxBase64:     base64.StdEncoding.EncodeToString(unsignedPkt),
		UnsignedMsgBase64:    base64.StdEncoding.EncodeToString(msgBytes),
		SimulationOK:         simOK,
		SimulationError:      simErr,
		ExpiresAt:            nowSec + 120,
		Status:               ReviewStatusPrepared,
		CreatedAt:            nowSec,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rawRev, _ := json.Marshal(review)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews (id, wallet_id, status, payload_json, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		review.ID, review.WalletID, review.Status, string(rawRev), review.ExpiresAt, review.CreatedAt,
	); err != nil {
		return WalletReview{}, err
	}
	s.appendAuditLocked(ctx, w.ID, "review_prepared", "system", fmt.Sprintf("Prepared %s %s %s -> %s (simOK=%t)", review.ID, review.AmountHuman, review.Symbol, review.Destination, review.SimulationOK), w.Policy.PolicyHash)
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
	if review.ExpiresAt <= nowSec {
		s.mu.Unlock()
		return LedgerEntry{}, errors.New("review expired; prepare a new transaction")
	}
	if review.PolicyHash != w.Policy.PolicyHash {
		s.mu.Unlock()
		return LedgerEntry{}, ErrPolicyConflict
	}
	if !review.SimulationOK {
		s.mu.Unlock()
		return LedgerEntry{}, fmt.Errorf("%w: simulation failed: %s", ErrPolicyDenied, firstStr(review.SimulationError, "unknown error"))
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

	// Atomically reserve daily cap and mark review executed BEFORE broadcast.
	day := s.dayUTC()
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
	entry.State = nextState
	entry.TxSignature = sigStr
	entry.UpdatedAt = s.now().UTC().Unix()
	if sendErr != nil {
		entry.Error = sendErr.Error()
	}
	if nextState == LedgerConfirmed {
		_, _ = s.db.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = MAX(0, reserved_raw - ?), spent_raw = spent_raw + ? WHERE wallet_id = ? AND day_utc = ?`, debitRaw, debitRaw, w.ID, day)
	}
	updatedBytes, _ := json.Marshal(entry)
	_, _ = s.db.ExecContext(ctx, `UPDATE ledger SET state = ?, payload_json = ?, updated_at = ? WHERE id = ?`, entry.State, string(updatedBytes), entry.UpdatedAt, entry.ID)
	s.appendAuditLocked(ctx, w.ID, "tx_"+nextState, actor, fmt.Sprintf("%s %s SOL -> %s sig=%s", review.ID, review.AmountHuman, review.Destination, entry.TxSignature), review.PolicyHash)
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
		if nextState == LedgerFailed && rpcCreds.FallbackURL != "" {
			if fbState, _, fbErr := checkSignatureStatusRPC(ctx, s.cfg.RPCTransport, rpcCreds.FallbackURL, entry.TxSignature); fbErr != nil || fbState != LedgerFailed {
				return entry, errors.New("fallback RPC did not corroborate transaction failure; keeping reservation")
			}
		}
	}

	reservedAmount := uint64(5_000)
	var revPayloadRaw string
	if s.db.QueryRowContext(ctx, `SELECT payload_json FROM reviews WHERE id = ? AND wallet_id = ?`, entry.ReviewID, w.ID).Scan(&revPayloadRaw) == nil {
		var rev WalletReview
		if json.Unmarshal([]byte(revPayloadRaw), &rev) == nil {
			if amt, err := reviewSOLDebit(rev); err == nil {
				reservedAmount = amt
			}
		}
	} else if entry.Operation == "solana.nativeTransfer" && entry.AmountRaw <= math.MaxUint64-5_000 {
		reservedAmount = entry.AmountRaw + 5_000
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	day := time.Unix(entry.CreatedAt, 0).UTC().Format("2006-01-02")
	switch nextState {
	case LedgerConfirmed:
		_, _ = s.db.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = MAX(0, reserved_raw - ?), spent_raw = spent_raw + ? WHERE wallet_id = ? AND day_utc = ?`, reservedAmount, reservedAmount, w.ID, day)
	case LedgerFailed:
		_, _ = s.db.ExecContext(ctx, `UPDATE daily_usage SET reserved_raw = MAX(0, reserved_raw - ?) WHERE wallet_id = ? AND day_utc = ?`, reservedAmount, w.ID, day)
	}
	entry.State = nextState
	entry.Error = statusErr
	entry.UpdatedAt = s.now().UTC().Unix()
	b, _ := json.Marshal(entry)
	_, _ = s.db.ExecContext(ctx, `UPDATE ledger SET state = ?, payload_json = ?, updated_at = ? WHERE id = ?`, entry.State, string(b), entry.UpdatedAt, entry.ID)
	s.appendAuditLocked(ctx, w.ID, "tx_reconciled", "owner", fmt.Sprintf("Reconciled %s -> %s", entry.ID, entry.State), entry.PolicyHash)
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

// ExportEncryptedRecovery seals the native signer key into an Argon2id+AES-GCM recovery archive + SHA-256 checksum.
func (s *Service) ExportEncryptedRecovery(ctx context.Context, walletID, passphrase, actor string) (archive []byte, fingerprint string, err error) {
	if len(strings.TrimSpace(passphrase)) < recoveryMinPass {
		return nil, "", fmt.Errorf("recovery passphrase must be at least %d characters", recoveryMinPass)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, err := s.getWalletLocked(ctx, walletID)
	if err != nil {
		return nil, "", err
	}
	if w.Custody != CustodyNativeSigner {
		return nil, "", fmt.Errorf("%w: only native signer wallets have an exportable key", ErrRoleDenied)
	}
	var kn, kc []byte
	if err := s.db.QueryRowContext(ctx, `SELECT key_nonce, key_ciphertext FROM wallet_keys WHERE wallet_id = ?`, w.ID).Scan(&kn, &kc); err != nil {
		return nil, "", err
	}
	seed, err := s.openBytes("custody-key:"+w.ID+":"+w.PublicKey, kn, kc)
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
	key := vault.DeriveKey(passphrase, salt, s.kdfTime, s.kdfMemory, s.kdfThreads)
	defer clear(key)
	ct, err := vault.SealAESGCM(key, nonce, seed, []byte(recoveryFormat+":"+w.PublicKey))
	if err != nil {
		return nil, "", err
	}
	pkg := RecoveryPackage{
		Version:    1,
		Format:     recoveryFormat,
		WalletID:   w.ID,
		Role:       w.Role,
		PublicKey:  w.PublicKey,
		KDF:        "argon2id",
		KDFTime:    s.kdfTime,
		KDFMemory:  s.kdfMemory,
		KDFThreads: s.kdfThreads,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: ct,
	}
	archive, err = json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(archive)
	fp := hex.EncodeToString(sum[:])
	s.appendAuditLocked(ctx, w.ID, "encrypted_recovery_exported", firstStr(actor, "owner"), "Exported Argon2id encrypted backup fp="+fp[:16], w.Policy.PolicyHash)
	return archive, fp, nil
}

// ImportEncryptedRecovery decrypts an Argon2id RecoveryPackage inside signer and imports the wallet.
func (s *Service) ImportEncryptedRecovery(ctx context.Context, archiveRaw []byte, passphrase, name, role, primaryRPCURL string) (WalletPublic, error) {
	if len(archiveRaw) == 0 || len(archiveRaw) > 64*1024 {
		return WalletPublic{}, errors.New("invalid recovery archive size")
	}
	var pkg RecoveryPackage
	if err := json.Unmarshal(archiveRaw, &pkg); err != nil || pkg.Version != 1 || pkg.Format != recoveryFormat {
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
	key := vault.DeriveKey(passphrase, pkg.Salt, pkg.KDFTime, pkg.KDFMemory, pkg.KDFThreads)
	defer clear(key)
	seed, err := vault.OpenAESGCM(key, pkg.Nonce, pkg.Ciphertext, []byte(recoveryFormat+":"+pkg.PublicKey))
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
	if !ValidRole(targetRole) || targetRole == RoleWatch {
		return WalletPublic{}, errors.New("invalid restored wallet role")
	}
	targetName := strings.TrimSpace(name)
	if targetName == "" {
		targetName = "Restored " + targetRole
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM wallets WHERE id = ?`, w.ID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		s.appendAuditLocked(ctx, w.ID, "wallet_deleted", "owner", "Removed wallet "+w.PublicKey, w.Policy.PolicyHash)
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
