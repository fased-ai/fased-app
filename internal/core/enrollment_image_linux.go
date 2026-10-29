//go:build linux

package core

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

var errEnrollmentImage = errors.New("invalid or conflicting enrollment image; preserve original state")

type enrollmentEntry struct {
	stat unix.Stat_t
	data []byte
}

type enrollmentSourceLock struct {
	root *os.File
	file *os.File
	once sync.Once
	err  error
}

func (l *enrollmentSourceLock) Close() error {
	l.once.Do(func() {
		l.err = errors.Join(unix.Flock(int(l.file.Fd()), unix.LOCK_UN), l.file.Close(), l.root.Close())
	})
	return l.err
}

func enrollmentRoot(dir string) (*os.File, error) {
	if os.Geteuid() == 0 || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, errEnrollmentImage
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || real != dir {
		return nil, errEnrollmentImage
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errEnrollmentImage
	}
	f := os.NewFile(uintptr(fd), dir)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&07777 != 0700 || int(st.Uid) != os.Geteuid() {
		f.Close()
		return nil, errEnrollmentImage
	}
	return f, nil
}

func enrollmentSource(dir string) (*enrollmentSourceLock, error) {
	r, err := enrollmentRoot(dir)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(r.Fd()), "instance.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_CREAT|unix.O_EXCL, 0600)
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(int(r.Fd()), "instance.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		r.Close()
		return nil, errEnrollmentImage
	}
	f := os.NewFile(uintptr(fd), "instance.lock")
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || !enrollmentRegular(st, 0) || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		f.Close()
		r.Close()
		return nil, errors.New("enrollment requires the stopped instance and exclusive source lock")
	}
	return &enrollmentSourceLock{root: r, file: f}, nil
}

func enrollmentRegular(st unix.Stat_t, max int64) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mode&07777 == 0600 && int(st.Uid) == os.Geteuid() && st.Nlink == 1 && st.Size >= 0 && st.Size <= max
}

// Only these existing app paths are classified. A private directory does not
// make arbitrary files safe to omit or copy, and signer paths are never opened.
func enrollmentPath(name string) (included bool, max int64, ok bool) {
	switch name {
	case "core.sqlite":
		return true, MaxEnrollmentFileBytes, true
	case "ai-credentials.json":
		return true, aiVaultMaxSerializedBytes, true
	case "core.sqlite-wal", "core.sqlite-shm":
		return false, MaxEnrollmentFileBytes, true
	case "endpoint.json", "claim-code.json", "cli-session.json":
		return false, 64 << 10, true
	case "instance.lock":
		return false, 0, true
	case "core.sock":
		return false, 0, true
	}
	if strings.HasPrefix(name, "rpc-credential-") && strings.HasSuffix(name, ".json") && hexValue(strings.TrimSuffix(strings.TrimPrefix(name, "rpc-credential-"), ".json"), 16) {
		return true, 2048, true
	}
	parts := strings.Split(name, "/")
	if len(parts) == 3 && parts[0] == "lifecycle" && update.IDOK(parts[1]) {
		switch strings.TrimSuffix(parts[2], ".pending") {
		case "metadata.sqlite":
			return true, MaxEnrollmentFileBytes, true
		case "request.json", "recovery.json":
			return true, 16384, true
		case "backup.lock":
			if parts[2] == "backup.lock" {
				return true, 0, true
			}
		}
	}
	return false, 0, false
}

func enrollmentDirectory(name string) bool {
	parts := strings.Split(name, "/")
	return name == "lifecycle" || len(parts) == 2 && parts[0] == "lifecycle" && update.IDOK(parts[1])
}

func enrollmentRead(fd int, name string, max int64) (enrollmentEntry, error) {
	var out enrollmentEntry
	n, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return out, errEnrollmentImage
	}
	f := os.NewFile(uintptr(n), name)
	defer f.Close()
	if unix.Fstat(n, &out.stat) != nil || !enrollmentRegular(out.stat, max) {
		return out, errEnrollmentImage
	}
	out.data, err = io.ReadAll(io.LimitReader(f, max+1))
	var after unix.Stat_t
	if err != nil || int64(len(out.data)) != out.stat.Size || unix.Fstat(n, &after) != nil || !enrollmentSameStat(out.stat, after) {
		return enrollmentEntry{}, errEnrollmentImage
	}
	return out, nil
}

func enrollmentSameStat(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func enrollmentScan(ctx context.Context, fd int) (map[string]enrollmentEntry, error) {
	out := map[string]enrollmentEntry{}
	var total int64
	var walk func(int, string) error
	walk = func(parent int, prefix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dup, err := unix.Openat(parent, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return errEnrollmentImage
		}
		f := os.NewFile(uintptr(dup), ".")
		entries, err := f.ReadDir(MaxEnrollmentFiles + 1)
		f.Close()
		if err != nil && !errors.Is(err, io.EOF) || len(entries) > MaxEnrollmentFiles {
			return errEnrollmentImage
		}
		for _, entry := range entries {
			name := prefix + entry.Name()
			var st unix.Stat_t
			if unix.Fstatat(parent, entry.Name(), &st, unix.AT_SYMLINK_NOFOLLOW) != nil || len(out) >= MaxEnrollmentFiles {
				return errEnrollmentImage
			}
			if st.Mode&unix.S_IFMT == unix.S_IFDIR {
				if !enrollmentDirectory(name) || st.Mode&07777 != 0700 || int(st.Uid) != os.Geteuid() {
					return errEnrollmentImage
				}
				child, err := unix.Openat(parent, entry.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if err != nil {
					return errEnrollmentImage
				}
				var held unix.Stat_t
				if unix.Fstat(child, &held) != nil || !enrollmentSameStat(st, held) {
					unix.Close(child)
					return errEnrollmentImage
				}
				out[name] = enrollmentEntry{stat: st}
				err = walk(child, name+"/")
				unix.Close(child)
				if err != nil {
					return err
				}
				continue
			}
			_, max, ok := enrollmentPath(name)
			if !ok {
				return errEnrollmentImage
			}
			if name == "core.sock" {
				if st.Mode&unix.S_IFMT != unix.S_IFSOCK || int(st.Uid) != os.Geteuid() {
					return errEnrollmentImage
				}
				out[name] = enrollmentEntry{stat: st}
				continue
			}
			r, err := enrollmentRead(parent, entry.Name(), max)
			if err != nil || !enrollmentSameStat(st, r.stat) {
				return errEnrollmentImage
			}
			total += int64(len(r.data))
			if total > MaxEnrollmentImageBytes {
				return errEnrollmentImage
			}
			out[name] = r
		}
		return nil
	}
	err := walk(fd, "")
	return out, err
}

func exportEnrollmentImage(ctx context.Context, dir string) (_ EnrollmentImage, closer io.Closer, resultErr error) {
	var out EnrollmentImage
	if err := ctx.Err(); err != nil {
		return out, nil, err
	}
	lock, err := enrollmentSource(dir)
	if err != nil {
		return out, nil, err
	}
	defer func() {
		if resultErr != nil {
			lock.Close()
		}
	}()
	before, err := enrollmentScan(ctx, int(lock.root.Fd()))
	if err != nil {
		return out, nil, err
	}
	var heldLock unix.Stat_t
	if unix.Fstat(int(lock.file.Fd()), &heldLock) != nil || !enrollmentSameStat(heldLock, before["instance.lock"].stat) {
		return out, nil, errors.New("source lock identity changed; preserve original state")
	}
	db, ok := before["core.sqlite"]
	if !ok || len(db.data) == 0 {
		return out, nil, errEnrollmentImage
	}
	serialized, id, err := enrollmentSQLite(ctx, db.data, before["core.sqlite-wal"].data, true)
	if err != nil {
		return out, nil, err
	}
	out = EnrollmentImage{Version: 1, InstanceID: id, Schema: StateSchema}
	for path, entry := range before {
		included, _, _ := enrollmentPath(path)
		if included {
			data := entry.data
			if path == "core.sqlite" {
				data = serialized
			}
			out.Files = append(out.Files, EnrollmentFile{Path: path, Data: data})
		}
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	if err = validateEnrollmentImage(out); err != nil {
		return EnrollmentImage{}, nil, err
	}
	after, err := enrollmentScan(ctx, int(lock.root.Fd()))
	if err != nil || len(before) != len(after) {
		return EnrollmentImage{}, nil, errEnrollmentImage
	}
	for path, original := range before {
		current, ok := after[path]
		if !ok || !enrollmentSameStat(original.stat, current.stat) || !bytes.Equal(original.data, current.data) {
			return EnrollmentImage{}, nil, errors.New("source changed during enrollment; preserve it and stop all writers")
		}
	}
	return out, lock, nil
}

// SQLite reads and WAL reconstruction operate only on a disposable private copy.
// Even a read-only WAL connection may create SHM; never let it touch the source.
func enrollmentSQLite(ctx context.Context, data, wal []byte, serialize bool) ([]byte, string, error) {
	if len(data) == 0 || len(data) > MaxEnrollmentFileBytes || len(wal) > MaxEnrollmentFileBytes {
		return nil, "", errEnrollmentImage
	}
	dir, err := os.MkdirTemp("", "fased-enrollment-snapshot-")
	if err != nil {
		return nil, "", errEnrollmentImage
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "core.sqlite")
	if os.WriteFile(path, data, 0600) != nil || len(wal) > 0 && os.WriteFile(path+"-wal", wal, 0600) != nil {
		return nil, "", errEnrollmentImage
	}
	query := "mode=ro&_pragma=query_only(1)&_pragma=trusted_schema(0)"
	if !serialize {
		query += "&immutable=1"
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: query}).String())
	if err != nil {
		return nil, "", errEnrollmentImage
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	c, err := db.Conn(ctx)
	if err != nil {
		return nil, "", errEnrollmentImage
	}
	defer c.Close()
	if _, err = c.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, "", errEnrollmentImage
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	var check, id string
	var schema, count int
	if c.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check) != nil || check != "ok" || c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema) != nil || schema != StateSchema || c.QueryRowContext(ctx, "SELECT count(*),min(instance_id) FROM instance").Scan(&count, &id) != nil || count != 1 || !validConnectionRef(id) {
		return nil, "", errEnrollmentImage
	}
	tables, err := c.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type IN ('table','view')")
	if err != nil {
		return nil, "", errEnrollmentImage
	}
	classified := map[string]bool{"instance": true, "sessions": true, "accounts": true, "update_pause": true, "agent_workspace": true, "wen_research": true, "agent_history": true, "sqlite_sequence": true}
	for tables.Next() {
		var name string
		if tables.Scan(&name) != nil || !classified[name] {
			tables.Close()
			return nil, "", errEnrollmentImage
		}
		delete(classified, name)
	}
	err = errors.Join(tables.Err(), tables.Close())
	if err != nil || len(classified) != 0 {
		return nil, "", errEnrollmentImage
	}
	phase, _, err := readPause(ctx, c)
	if err != nil || phase == "paused" {
		return nil, "", errors.New("finish existing update recovery before enrollment")
	}
	// Parse existing current-schema records without migration, execution, recovery
	// mutation or credential decryption; snapshot validates the archive contract.
	s := &Store{db: db}
	if _, _, err = readAgents(ctx, c); err != nil {
		return nil, "", errEnrollmentImage
	}
	if _, err = readSetupRecord(ctx, c); err != nil {
		return nil, "", errEnrollmentImage
	}
	if _, err = s.snapshotTx(ctx, c, "enrollment-image"); err != nil {
		return nil, "", errEnrollmentImage
	}
	if !serialize {
		return nil, id, nil
	}
	var pages, pageSize int64
	if c.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages) != nil || c.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize) != nil || pages <= 0 || pageSize <= 0 || pages > MaxEnrollmentFileBytes/pageSize {
		return nil, "", errEnrollmentImage
	}
	var image []byte
	err = c.Raw(func(raw any) error {
		s, ok := raw.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errEnrollmentImage
		}
		var e error
		image, e = s.Serialize()
		return e
	})
	if err != nil || len(image) == 0 || len(image) > MaxEnrollmentFileBytes {
		return nil, "", errEnrollmentImage
	}
	return image, id, nil
}

func validateEnrollmentImage(image EnrollmentImage) error {
	if os.Geteuid() == 0 || image.Version != 1 || image.Schema != StateSchema || !validConnectionRef(image.InstanceID) || len(image.Files) == 0 || len(image.Files) > MaxEnrollmentFiles {
		return errEnrollmentImage
	}
	var total int64
	var database []byte
	prior := ""
	for _, f := range image.Files {
		include, max, ok := enrollmentPath(f.Path)
		if !ok || !include || f.Path <= prior || len(f.Path) > 160 || int64(len(f.Data)) > max {
			return errEnrollmentImage
		}
		prior = f.Path
		total += int64(len(f.Data))
		if total > MaxEnrollmentImageBytes {
			return errEnrollmentImage
		}
		if f.Path == "core.sqlite" {
			database = f.Data
		}
		if err := enrollmentCredential(f.Path, f.Data); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, id, err := enrollmentSQLite(ctx, database, nil, false)
	if err != nil || id != image.InstanceID {
		return errEnrollmentImage
	}
	return nil
}

func enrollmentCredential(name string, data []byte) error {
	if name == "ai-credentials.json" {
		var head map[string]json.RawMessage
		if update.StrictJSON(data, &head) != nil {
			return errEnrollmentImage
		}
		var version int
		if json.Unmarshal(head["version"], &version) != nil {
			return errEnrollmentImage
		}
		var col aiVaultCollection
		if version == 3 {
			if update.StrictJSON(data, &col) != nil {
				return errEnrollmentImage
			}
		} else {
			var vault aiVaultEnvelope
			if update.StrictJSON(data, &vault) != nil {
				return errEnrollmentImage
			}
			col = aiVaultCollection{Version: 3, Default: vault.Ref, Accounts: []aiVaultEnvelope{vault}}
		}
		if validateAIVaultCollection(col) != nil {
			return errEnrollmentImage
		}
	} else if strings.HasPrefix(name, "rpc-credential-") {
		var vault rpcVault
		if update.StrictJSON(data, &vault) != nil || !validRPCVault(vault) {
			return errEnrollmentImage
		}
	}
	return nil
}

func importEnrollmentImage(ctx context.Context, dir string, image EnrollmentImage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateEnrollmentImage(image); err != nil {
		return err
	}
	r, err := enrollmentRoot(dir)
	if err != nil {
		return err
	}
	defer r.Close()
	if unix.Flock(int(r.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return errEnrollmentImage
	}
	defer unix.Flock(int(r.Fd()), unix.LOCK_UN)
	existing, err := enrollmentScan(ctx, int(r.Fd()))
	if err != nil {
		return err
	}
	wanted := map[string][]byte{}
	dirs := map[string]bool{}
	for _, f := range image.Files {
		wanted[f.Path] = f.Data
		for p := filepath.Dir(f.Path); p != "."; p = filepath.Dir(p) {
			dirs[p] = true
		}
	}
	// Preflight every existing byte before any write. A conflicting complete or
	// partial stage belongs to its creator and is never overwritten or removed.
	for path, entry := range existing {
		if entry.stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			if !dirs[path] {
				return errEnrollmentImage
			}
			continue
		}
		data, ok := wanted[path]
		if !ok || !bytes.HasPrefix(data, entry.data) {
			return errEnrollmentImage
		}
	}
	for _, f := range image.Files {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = enrollmentStageFile(int(r.Fd()), f); err != nil {
			return err
		}
	}
	// Reinspect the exact complete output. Publication and activation belong to
	// the enrollment controller, never this app-state transfer function.
	after, err := enrollmentScan(ctx, int(r.Fd()))
	if err != nil || len(after) != len(wanted)+len(dirs) {
		return errEnrollmentImage
	}
	for name, data := range wanted {
		if !bytes.Equal(after[name].data, data) {
			return errEnrollmentImage
		}
	}
	return unix.Fsync(int(r.Fd()))
}

func enrollmentStageFile(root int, item EnrollmentFile) error {
	parts := strings.Split(item.Path, "/")
	parent := root
	var held []int
	defer func() {
		for _, fd := range held {
			unix.Close(fd)
		}
	}()
	for _, name := range parts[:len(parts)-1] {
		if err := unix.Mkdirat(parent, name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return errEnrollmentImage
		}
		child, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return errEnrollmentImage
		}
		held = append(held, child)
		var st unix.Stat_t
		if unix.Fstat(child, &st) != nil || st.Mode&07777 != 0700 || int(st.Uid) != os.Geteuid() {
			return errEnrollmentImage
		}
		if unix.Fsync(parent) != nil {
			return errEnrollmentImage
		}
		parent = child
	}
	name := parts[len(parts)-1]
	fd, err := unix.Openat(parent, name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(parent, name, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	}
	if err != nil {
		return errEnrollmentImage
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || !enrollmentRegular(st, int64(len(item.Data))) {
		return errEnrollmentImage
	}
	prefix := make([]byte, st.Size)
	if _, err = io.ReadFull(f, prefix); err != nil || !bytes.HasPrefix(item.Data, prefix) {
		return errEnrollmentImage
	}
	if _, err = f.WriteAt(item.Data[len(prefix):], int64(len(prefix))); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	var after unix.Stat_t
	if unix.Fstat(fd, &after) != nil || !enrollmentRegular(after, int64(len(item.Data))) || after.Size != int64(len(item.Data)) {
		return errEnrollmentImage
	}
	return unix.Fsync(parent)
}
