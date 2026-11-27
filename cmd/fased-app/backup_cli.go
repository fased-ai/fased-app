package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
)

const maxRestoreArchiveFileBytes = 256 * 1024 * 1024

type stateBackupManifest struct {
	Schema     int               `json:"schema"`
	Version    string            `json:"version"`
	CreatedAt  int64             `json:"createdAt"`
	Files      map[string]string `json:"files"`
	HasAIVault bool              `json:"hasAiVault"`
}

func stateBackupCommand(action string, args []string, out, errOut io.Writer) error {
	defaultData, err := defaultDir()
	if err != nil {
		return err
	}
	if action == "backup" {
		return runStateBackup(args, defaultData, out, errOut)
	}
	return runStateRestore(args, defaultData, out, errOut)
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func writeTarFile(tw *tar.Writer, name, srcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    name,
		Mode:    0600,
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func runStateBackup(args []string, defaultData string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("backup", flag.ContinueOnError)
	f.SetOutput(errOut)
	dataDir := f.String("data-dir", defaultData, "private instance state directory")
	outPath := f.String("out", "", "destination .tar.gz archive path")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || strings.TrimSpace(*outPath) == "" {
		return errors.New("usage: fased-app backup --out <archive.tar.gz> [--data-dir <dir>] [--json]")
	}
	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	absOut, err := filepath.Abs(*outPath)
	if err != nil {
		return err
	}
	coreDBPath := filepath.Join(absData, "core.sqlite")
	if _, err := os.Stat(coreDBPath); err != nil {
		return fmt.Errorf("core.sqlite not found in %s", absData)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	stageDir, err := os.MkdirTemp(absData, ".fased-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)
	_ = os.Chmod(stageDir, 0700)

	manifest := stateBackupManifest{
		Schema:    1,
		Version:   core.Version,
		CreatedAt: time.Now().UTC().Unix(),
		Files:     map[string]string{},
	}

	// Consistent hot snapshots via SQLite VACUUM INTO.
	dbPaths := append([]string{"core.sqlite", "agents.sqlite"}, core.OptionalStateSQLitePaths()...)
	for _, relPath := range dbPaths {
		src := filepath.Join(absData, filepath.FromSlash(relPath))
		if _, statErr := os.Stat(src); errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		dst := filepath.Join(stageDir, filepath.FromSlash(relPath))
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o700); mkErr != nil {
			return mkErr
		}
		if err := core.SnapshotSQLite(ctx, src, dst); err != nil {
			return err
		}
		sum, _, err := fileSHA256(dst)
		if err != nil {
			return err
		}
		manifest.Files[relPath] = sum
	}

	// Non-secret config/manifest files (never bundle private IPC sockets or locks).
	for _, name := range []string{serverConfigFile, "model-manifest.json", "ai-credentials.json"} {
		src := filepath.Join(absData, name)
		info, statErr := os.Lstat(src)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if name == "ai-credentials.json" {
			manifest.HasAIVault = true
		}
		raw, rErr := os.ReadFile(src)
		if rErr != nil {
			return rErr
		}
		dst := filepath.Join(stageDir, name)
		if wErr := os.WriteFile(dst, raw, 0600); wErr != nil {
			return wErr
		}
		sum, _, err := fileSHA256(dst)
		if err != nil {
			return err
		}
		manifest.Files[name] = sum
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(stageDir, "backup-manifest.json")
	if err := os.WriteFile(manifestPath, append(manifestBytes, '\n'), 0600); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(absOut), 0700); err != nil {
		return err
	}
	tmpArchive, err := os.CreateTemp(filepath.Dir(absOut), ".fased-backup-*.tar.gz")
	if err != nil {
		return err
	}
	tmpPath := tmpArchive.Name()
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmpArchive.Chmod(0600); err != nil {
		tmpArchive.Close()
		return err
	}
	gz := gzip.NewWriter(tmpArchive)
	tw := tar.NewWriter(gz)

	names := make([]string, 0, len(manifest.Files)+1)
	names = append(names, "backup-manifest.json")
	for k := range manifest.Files {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writeTarFile(tw, name, filepath.Join(stageDir, name)); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			_ = tmpArchive.Close()
			return err
		}
	}
	if err := errors.Join(tw.Close(), gz.Close(), tmpArchive.Sync(), tmpArchive.Close()); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, absOut); err != nil {
		return err
	}
	tmpPath = ""
	archiveSum, archiveBytes, err := fileSHA256(absOut)
	if err != nil {
		return err
	}
	result := map[string]any{
		"archive":    absOut,
		"sha256":     archiveSum,
		"bytes":      archiveBytes,
		"version":    manifest.Version,
		"files":      manifest.Files,
		"hasAiVault": manifest.HasAIVault,
	}
	if *jsonOut {
		return json.NewEncoder(out).Encode(result)
	}
	fmt.Fprintf(out, "Backup written: %s (%d bytes, sha256=%s)\n", absOut, archiveBytes, archiveSum[:16])
	return nil
}

func runStateRestore(args []string, defaultData string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("restore", flag.ContinueOnError)
	f.SetOutput(errOut)
	dataDir := f.String("data-dir", defaultData, "target private instance state directory")
	fromPath := f.String("from", "", "source .tar.gz backup archive path")
	force := f.Bool("force", false, "overwrite existing stopped state files in data-dir")
	jsonOut := f.Bool("json", false, "output JSON")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || strings.TrimSpace(*fromPath) == "" {
		return errors.New("usage: fased-app restore --from <archive.tar.gz> [--data-dir <dir>] [--force] [--json]")
	}
	absData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	if err := core.EnsurePrivateDirectory(absData); err != nil {
		return err
	}
	// Refuse restoring over a running daemon.
	lock, err := core.LockInstance(filepath.Join(absData, "instance.lock"))
	if err != nil {
		return errors.New("stop Fased before restoring into this data directory")
	}
	defer lock.Close()

	if !*force {
		if _, err := os.Stat(filepath.Join(absData, "core.sqlite")); err == nil {
			return errors.New("target data-dir already contains core.sqlite; pass --force to replace stopped state")
		}
	}

	stageDir, err := os.MkdirTemp(absData, ".fased-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)
	_ = os.Chmod(stageDir, 0700)

	archiveFile, err := os.Open(*fromPath)
	if err != nil {
		return err
	}
	defer archiveFile.Close()
	gz, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("invalid gzip backup archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	allowedFiles := map[string]bool{
		"backup-manifest.json": true,
		"core.sqlite":          true,
		"agents.sqlite":        true,
		serverConfigFile:       true,
		"model-manifest.json":  true,
		"ai-credentials.json":  true,
	}
	optionalDBSet := map[string]bool{}
	for _, relPath := range core.OptionalStateSQLitePaths() {
		allowedFiles[relPath] = true
		optionalDBSet[relPath] = true
	}
	extracted := map[string]string{}
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("corrupt tar backup archive: %w", nextErr)
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("refusing non-regular archive entry: %s", hdr.Name)
		}
		if strings.Contains(hdr.Name, "\\") {
			return fmt.Errorf("unexpected archive entry: %s", hdr.Name)
		}
		name := path.Clean(hdr.Name)
		if name != hdr.Name || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") || !allowedFiles[name] {
			return fmt.Errorf("unexpected archive entry: %s", hdr.Name)
		}
		if hdr.Size < 0 || hdr.Size > maxRestoreArchiveFileBytes {
			return fmt.Errorf("archive entry exceeds size bound: %s", name)
		}
		dst := filepath.Join(stageDir, filepath.FromSlash(name))
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o700); mkErr != nil {
			return mkErr
		}
		w, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(w, h), io.LimitReader(tr, maxRestoreArchiveFileBytes+1))
		closeErr := w.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if n != hdr.Size {
			return fmt.Errorf("truncated archive entry: %s", name)
		}
		extracted[name] = hex.EncodeToString(h.Sum(nil))
	}

	manifestRaw, err := os.ReadFile(filepath.Join(stageDir, "backup-manifest.json"))
	if err != nil {
		return errors.New("backup archive is missing backup-manifest.json")
	}
	var manifest stateBackupManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil || manifest.Schema != 1 || manifest.Files["core.sqlite"] == "" {
		return errors.New("invalid backup-manifest.json")
	}
	for name, wantSum := range manifest.Files {
		if !allowedFiles[name] || name == "backup-manifest.json" {
			return fmt.Errorf("disallowed file in backup manifest: %s", name)
		}
		gotSum, ok := extracted[name]
		if !ok || gotSum != wantSum {
			return fmt.Errorf("checksum mismatch for %s in backup archive", name)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dbNames := append([]string{"core.sqlite", "agents.sqlite"}, core.OptionalStateSQLitePaths()...)
	for _, dbName := range dbNames {
		if manifest.Files[dbName] == "" {
			continue
		}
		rep := core.InspectSQLiteHealth(ctx, filepath.Join(stageDir, filepath.FromSlash(dbName)))
		if !rep.Healthy {
			return fmt.Errorf("staged %s failed integrity check: %s", dbName, rep.QuickCheck)
		}
	}

	// Clean stale WAL/SHM sidecars before swapping restored SQLite files into place.
	for _, dbName := range dbNames {
		if manifest.Files[dbName] != "" {
			target := filepath.Join(absData, filepath.FromSlash(dbName))
			_ = os.Remove(target + "-wal")
			_ = os.Remove(target + "-shm")
		}
	}

	restored := make([]string, 0, len(manifest.Files))
	for name := range manifest.Files {
		src := filepath.Join(stageDir, filepath.FromSlash(name))
		raw, rErr := os.ReadFile(src)
		if rErr != nil {
			return rErr
		}
		dst := filepath.Join(absData, filepath.FromSlash(name))
		if optionalDBSet[name] {
			if mkErr := os.MkdirAll(filepath.Dir(dst), 0o700); mkErr != nil {
				return mkErr
			}
		}
		tmpDst, tErr := os.CreateTemp(filepath.Dir(dst), ".restore-*")
		if tErr != nil {
			return tErr
		}
		tmpName := tmpDst.Name()
		_ = tmpDst.Chmod(0600)
		_, wErr := tmpDst.Write(raw)
		sErr := tmpDst.Sync()
		cErr := tmpDst.Close()
		if err := errors.Join(wErr, sErr, cErr); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		if err := os.Rename(tmpName, dst); err != nil {
			_ = os.Remove(tmpName)
			return err
		}
		restored = append(restored, name)
	}
	sort.Strings(restored)
	res := map[string]any{
		"restored": true,
		"dataDir":  absData,
		"files":    restored,
		"version":  manifest.Version,
	}
	if *jsonOut {
		return json.NewEncoder(out).Encode(res)
	}
	fmt.Fprintf(out, "Restored %d file(s) into %s\n", len(restored), absData)
	return nil
}
