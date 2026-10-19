package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db         *sql.DB
	dir        string
	instanceID string
	now        func() time.Time
	authMu     sync.Mutex
}

func OpenStore(dir string) (_ *Store, resultErr error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, "core.sqlite")
	if err = privateFile(dbPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = db.Close()
		}
	}()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, `PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		return nil, err
	}
	var schema int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); err != nil {
		return nil, err
	}
	if schema > 1 {
		return nil, errors.New("core state schema is newer than this binary; refusing downgrade")
	}
	if schema == 0 {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, `CREATE TABLE instance(id INTEGER PRIMARY KEY CHECK(id=1), instance_id TEXT NOT NULL, setup TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0, password_salt BLOB, password_hash BLOB, claim_hash BLOB, claim_expires INTEGER);
CREATE TABLE sessions(token_hash TEXT PRIMARY KEY, expires INTEGER NOT NULL);
PRAGMA user_version=1;`)
		if err != nil {
			return nil, err
		}
		id, err := randomToken()
		if err != nil {
			return nil, err
		}
		setup, _ := json.Marshal(DefaultSetup())
		if _, err = tx.ExecContext(ctx, `INSERT INTO instance(id,instance_id,setup) VALUES(1,?,?)`, id, string(setup)); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
	}
	store := &Store{db: db, dir: dir, now: time.Now}
	if err = db.QueryRowContext(ctx, "SELECT instance_id FROM instance WHERE id=1").Scan(&store.instanceID); err != nil {
		return nil, err
	}
	return store, nil
}
func (s *Store) Close() error { return s.db.Close() }
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func (s *Store) Claimed(ctx context.Context) (bool, error) {
	var claimed bool
	err := s.db.QueryRowContext(ctx, "SELECT password_hash IS NOT NULL FROM instance WHERE id=1").Scan(&claimed)
	return claimed, err
}
func (s *Store) Setup(ctx context.Context) (SetupRecord, error) {
	var result SetupRecord
	var text string
	err := s.db.QueryRowContext(ctx, "SELECT setup,revision FROM instance WHERE id=1").Scan(&text, &result.Revision)
	if err == nil {
		err = json.Unmarshal([]byte(text), &result.Settings)
	}
	return result, err
}
func (s *Store) UpdateSetup(ctx context.Context, expected int64, setup Setup) (SetupRecord, error) {
	if err := setup.Validate(); err != nil {
		return SetupRecord{}, err
	}
	bytes, err := json.Marshal(setup)
	if err != nil {
		return SetupRecord{}, err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE instance SET setup=?,revision=revision+1 WHERE id=1 AND revision=?", string(bytes), expected)
	if err != nil {
		return SetupRecord{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return SetupRecord{}, err
	}
	if count != 1 {
		return SetupRecord{}, ErrConflict
	}
	return SetupRecord{Revision: expected + 1, Settings: setup}, nil
}
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("state directory must be a real private directory")
	}
	return protectPath(dir, info)
}
func privateFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		var f *os.File
		f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular private state file: %s", filepath.Base(path))
	}
	return protectPath(path, info)
}
func WritePrivateJSON(path string, value any) error {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := privateFile(path); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// Parent is private and owner-controlled. No secrets are placed in arguments or logs.
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	info, err := f.Stat()
	if err == nil {
		err = protectPath(f.Name(), info)
	}
	if err != nil {
		f.Close()
		return err
	}
	_, writeErr := f.Write(append(data, '\n'))
	syncErr := f.Sync()
	closeErr := f.Close()
	if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func readPrivateFile(path string) ([]byte, error) {
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("private state must be a regular file")
	}
	if err = protectPath(path, info); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
