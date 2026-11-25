package hub

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	hubDirName = "hub"
	hubDBName  = "hub.sqlite"
)

type store struct {
	dir string
	db  *sql.DB
	now func() time.Time
	mu  sync.Mutex
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

func openStore(dataDir string, nowFn func() time.Time) (*store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("hub DataDir required")
	}
	dir := filepath.Join(dataDir, hubDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dir, hubDBName)
	dsn := sqliteFileURI(dbPath) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const schemaSQL = `
CREATE TABLE IF NOT EXISTS hub_packages(
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	version TEXT NOT NULL,
	author_pubkey_hex TEXT NOT NULL,
	payload TEXT NOT NULL,
	payload_sha256 TEXT NOT NULL,
	signature_hex TEXT NOT NULL,
	sat_bond_amount INTEGER NOT NULL DEFAULT 0,
	sat_bond_status TEXT NOT NULL DEFAULT 'deferred_wen_hold',
	verified INTEGER NOT NULL DEFAULT 0,
	installed INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_hub_packages_created ON hub_packages(created_at DESC);
`
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &store{dir: dir, db: db, now: nowFn}, nil
}

func (s *store) close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *store) savePackage(ctx context.Context, pkg Package) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ver, inst := 0, 0
	if pkg.Verified {
		ver = 1
	}
	if pkg.Installed {
		inst = 1
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO hub_packages(id, name, kind, version, author_pubkey_hex, payload, payload_sha256, signature_hex, sat_bond_amount, sat_bond_status, verified, installed, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name,
	kind = excluded.kind,
	version = excluded.version,
	author_pubkey_hex = excluded.author_pubkey_hex,
	payload = excluded.payload,
	payload_sha256 = excluded.payload_sha256,
	signature_hex = excluded.signature_hex,
	sat_bond_amount = excluded.sat_bond_amount,
	sat_bond_status = excluded.sat_bond_status,
	verified = excluded.verified
`, pkg.ID, pkg.Name, pkg.Kind, pkg.Version, pkg.AuthorPubKeyHex, pkg.Payload, pkg.PayloadSHA256, pkg.SignatureHex, pkg.SATBond.AmountSAT, pkg.SATBond.Status, ver, inst, pkg.CreatedAt)
	return err
}

func (s *store) listPackages(ctx context.Context) ([]Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, kind, version, author_pubkey_hex, payload, payload_sha256, signature_hex, sat_bond_amount, sat_bond_status, verified, installed, created_at
FROM hub_packages ORDER BY created_at DESC, id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Package{}
	for rows.Next() {
		var p Package
		var ver, inst int
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.Version, &p.AuthorPubKeyHex, &p.Payload, &p.PayloadSHA256, &p.SignatureHex, &p.SATBond.AmountSAT, &p.SATBond.Status, &ver, &inst, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Verified = ver == 1
		p.Installed = inst == 1
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *store) getPackage(ctx context.Context, id string) (Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p Package
	var ver, inst int
	err := s.db.QueryRowContext(ctx, `
SELECT id, name, kind, version, author_pubkey_hex, payload, payload_sha256, signature_hex, sat_bond_amount, sat_bond_status, verified, installed, created_at
FROM hub_packages WHERE id = ?
`, id).Scan(&p.ID, &p.Name, &p.Kind, &p.Version, &p.AuthorPubKeyHex, &p.Payload, &p.PayloadSHA256, &p.SignatureHex, &p.SATBond.AmountSAT, &p.SATBond.Status, &ver, &inst, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Package{}, ErrPackageNotFound
	}
	if err != nil {
		return Package{}, err
	}
	p.Verified = ver == 1
	p.Installed = inst == 1
	return p, nil
}

func (s *store) markInstalled(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE hub_packages SET installed = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrPackageNotFound
	}
	return nil
}
