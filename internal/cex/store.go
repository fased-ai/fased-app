package cex

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	cexDirName = "cex"
	cexDBName  = "cex.sqlite"
	cexKeyName = "cex.key"
	maxOrders  = 200
)

type sealedCredentialPayload struct {
	APIKey     string `json:"apiKey"`
	APISecret  string `json:"apiSecret"`
	Passphrase string `json:"passphrase,omitempty"`
}

type store struct {
	dir    string
	db     *sql.DB
	master [32]byte
	now    func() time.Time
	mu     sync.Mutex
}

func sqliteFileURI(path string) string {
	slashPath := filepath.ToSlash(path)
	if runtime.GOOS == "windows" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	escaped := strings.ReplaceAll(slashPath, "?", "%3F")
	escaped = strings.ReplaceAll(escaped, "#", "%23")
	return "file://" + escaped
}

func loadOrCreateMasterKey(dir string) ([32]byte, error) {
	var key [32]byte
	keyPath := filepath.Join(dir, cexKeyName)
	raw, err := os.ReadFile(keyPath)
	if err == nil && len(raw) == 32 {
		copy(key[:], raw)
		return key, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return key, err
	}
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return key, err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", keyPath, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, key[:], 0o600); err != nil {
		return key, err
	}
	if err := os.Rename(tmpPath, keyPath); err != nil {
		_ = os.Remove(tmpPath)
		return key, err
	}
	return key, nil
}

func openStore(dataDir string, nowFn func() time.Time) (*store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("cex DataDir required")
	}
	dir := filepath.Join(dataDir, cexDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	master, err := loadOrCreateMasterKey(dir)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, cexDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS cex_credentials(
	venue TEXT PRIMARY KEY,
	label TEXT NOT NULL,
	environment TEXT NOT NULL,
	key_fingerprint TEXT NOT NULL,
	permissions_json TEXT NOT NULL,
	withdrawal_disabled INTEGER NOT NULL CHECK(withdrawal_disabled = 1),
	nonce BLOB NOT NULL,
	ciphertext BLOB NOT NULL,
	updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cex_orders(
	id TEXT PRIMARY KEY,
	venue TEXT NOT NULL,
	symbol TEXT NOT NULL,
	side TEXT NOT NULL,
	order_type TEXT NOT NULL,
	quantity REAL NOT NULL,
	price REAL NOT NULL,
	environment TEXT NOT NULL,
	status TEXT NOT NULL,
	simulated INTEGER NOT NULL,
	digest TEXT NOT NULL,
	message TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cex_orders_created ON cex_orders(created_at DESC);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &store{dir: dir, db: db, master: master, now: nowFn}, nil
}

func (s *store) close() error {
	clear(s.master[:])
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *store) seal(venue, env string, payload sealedCredentialPayload) ([]byte, []byte, error) {
	block, err := aes.NewCipher(s.master[:])
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	defer clear(plain)
	aad := []byte("cex:" + venue + ":" + env)
	sealed := gcm.Seal(nil, nonce, plain, aad)
	return nonce, sealed, nil
}

func (s *store) open(venue, env string, nonce, ciphertext []byte) (sealedCredentialPayload, error) {
	block, err := aes.NewCipher(s.master[:])
	if err != nil {
		return sealedCredentialPayload{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return sealedCredentialPayload{}, err
	}
	aad := []byte("cex:" + venue + ":" + env)
	plain, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return sealedCredentialPayload{}, err
	}
	defer clear(plain)
	var out sealedCredentialPayload
	if err := json.Unmarshal(plain, &out); err != nil {
		return sealedCredentialPayload{}, err
	}
	return out, nil
}

func (s *store) saveCredential(ctx context.Context, in CredentialInput) (CredentialStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().Unix()
	nonce, ciphertext, err := s.seal(in.Venue, in.Environment, sealedCredentialPayload{
		APIKey:     in.APIKey,
		APISecret:  in.APISecret,
		Passphrase: in.Passphrase,
	})
	if err != nil {
		return CredentialStatus{}, err
	}
	fp := keyFingerprint(in.APIKey)
	permsJSON, _ := json.Marshal(in.Permissions)
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO cex_credentials(venue, label, environment, key_fingerprint, permissions_json, withdrawal_disabled, nonce, ciphertext, updated_at)
VALUES(?, ?, ?, ?, ?, 1, ?, ?, ?)
ON CONFLICT(venue) DO UPDATE SET
	label = excluded.label,
	environment = excluded.environment,
	key_fingerprint = excluded.key_fingerprint,
	permissions_json = excluded.permissions_json,
	withdrawal_disabled = 1,
	nonce = excluded.nonce,
	ciphertext = excluded.ciphertext,
	updated_at = excluded.updated_at
`, in.Venue, in.Label, in.Environment, fp, string(permsJSON), nonce, ciphertext, now); err != nil {
		return CredentialStatus{}, err
	}
	return CredentialStatus{
		Venue:              in.Venue,
		Label:              in.Label,
		Environment:        in.Environment,
		KeyFingerprint:     fp,
		Permissions:        in.Permissions,
		WithdrawalDisabled: true,
		UpdatedAt:          now,
	}, nil
}

func (s *store) deleteCredential(ctx context.Context, venue string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM cex_credentials WHERE venue = ?`, venue)
	return err
}

func (s *store) loadSealedCredential(ctx context.Context, venue string) (CredentialStatus, sealedCredentialPayload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st CredentialStatus
	var permsJSON string
	var wDisabled int
	var nonce, ciphertext []byte
	err := s.db.QueryRowContext(ctx, `
SELECT venue, label, environment, key_fingerprint, permissions_json, withdrawal_disabled, nonce, ciphertext, updated_at
FROM cex_credentials WHERE venue = ?
`, venue).Scan(&st.Venue, &st.Label, &st.Environment, &st.KeyFingerprint, &permsJSON, &wDisabled, &nonce, &ciphertext, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialStatus{}, sealedCredentialPayload{}, ErrCredentialNotFound
	}
	if err != nil {
		return CredentialStatus{}, sealedCredentialPayload{}, err
	}
	_ = json.Unmarshal([]byte(permsJSON), &st.Permissions)
	st.WithdrawalDisabled = wDisabled == 1
	payload, err := s.open(st.Venue, st.Environment, nonce, ciphertext)
	if err != nil {
		return CredentialStatus{}, sealedCredentialPayload{}, err
	}
	return st, payload, nil
}

func (s *store) listCredentials(ctx context.Context) ([]CredentialStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `
SELECT venue, label, environment, key_fingerprint, permissions_json, withdrawal_disabled, updated_at
FROM cex_credentials ORDER BY venue ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialStatus{}
	for rows.Next() {
		var st CredentialStatus
		var permsJSON string
		var wDisabled int
		if err := rows.Scan(&st.Venue, &st.Label, &st.Environment, &st.KeyFingerprint, &permsJSON, &wDisabled, &st.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(permsJSON), &st.Permissions)
		st.WithdrawalDisabled = wDisabled == 1
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *store) appendOrder(ctx context.Context, rec OrderReceipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sim := 0
	if rec.Simulated {
		sim = 1
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO cex_orders(id, venue, symbol, side, order_type, quantity, price, environment, status, simulated, digest, message, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, rec.ID, rec.Venue, rec.Symbol, rec.Side, rec.OrderType, rec.Quantity, rec.Price, rec.Environment, rec.Status, sim, rec.Digest, rec.Message, rec.CreatedAt); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM cex_orders WHERE id NOT IN (
	SELECT id FROM cex_orders ORDER BY created_at DESC LIMIT ?
)`, maxOrders)
	return err
}

func (s *store) listOrders(ctx context.Context, limit int) ([]OrderReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > maxOrders {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, venue, symbol, side, order_type, quantity, price, environment, status, simulated, digest, message, created_at
FROM cex_orders ORDER BY created_at DESC LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrderReceipt{}
	for rows.Next() {
		var rec OrderReceipt
		var sim int
		if err := rows.Scan(&rec.ID, &rec.Venue, &rec.Symbol, &rec.Side, &rec.OrderType, &rec.Quantity, &rec.Price, &rec.Environment, &rec.Status, &sim, &rec.Digest, &rec.Message, &rec.CreatedAt); err != nil {
			return nil, err
		}
		rec.Simulated = sim == 1
		out = append(out, rec)
	}
	return out, rows.Err()
}
