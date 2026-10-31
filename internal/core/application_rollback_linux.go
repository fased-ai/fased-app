//go:build linux

package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
	"os"
	"syscall"
)

// CheckApplicationRollbackMetadata reads only schema/public fence identity. It
// neither opens a writable Store nor migrates/restores data. The controller must
// still own the OS/service transaction and verify the exact retained old code.
// It works when a bad target cannot start its IPC server.
func CheckApplicationRollbackMetadata(ctx context.Context, directory, id string, schema int) error {
	if !update.IDOK(id) || schema <= 0 {
		return errors.New("exact held application reader required")
	}
	dir, e := openApplicationMetadataDirectory(directory)
	if e != nil {
		return e
	}
	defer dir.Close()
	st, e := dir.Stat()
	if e != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return errors.New("private metadata directory required")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return errors.New("metadata must be read by its ordinary OS owner")
	}
	db, e := sql.Open("sqlite", fmt.Sprintf("file:/proc/self/fd/%d/core.sqlite?mode=ro", dir.Fd()))
	if e != nil {
		return e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var current int
	if e = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); e != nil || current != schema {
		return errors.New("old reader cannot restore migrated metadata")
	}
	phase, p, e := readPause(ctx, tx)
	if e != nil || phase != "paused" || p.Request.IntentID != id || p.Request.Expected.State.Version != schema {
		return errors.New("exact application metadata hold required")
	}
	return nil
}

// ReadApplicationOwnerOffline admits only an existing, held, same-reader owner
// transaction. It never creates an instance/lease, accepts new release input or
// returns DB/vault bytes. A failed target cannot make rollback depend on its IPC.
func ReadApplicationOwnerOffline(ctx context.Context, directory, id string) (ApplicationOwnerView, error) {
	var out ApplicationOwnerView
	if !update.IDOK(id) {
		return out, errors.New("existing owner operation required")
	}
	dir, e := openApplicationMetadataDirectory(directory)
	if e != nil {
		return out, e
	}
	defer dir.Close()
	st, e := dir.Stat()
	if e != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return out, errors.New("private metadata directory required")
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return out, errors.New("ordinary metadata owner required")
	}
	db, e := sql.Open("sqlite", fmt.Sprintf("file:/proc/self/fd/%d/core.sqlite?mode=ro", dir.Fd()))
	if e != nil {
		return out, e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var schema int
	if e = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); e != nil || schema != StateSchema {
		return out, errors.New("offline reader does not support this metadata schema")
	}
	o, revision, e := readApplicationUpdate(ctx, tx)
	if e != nil || o.ID != id || o.Release == nil || !update.IDOK(o.OwnerLease) || !oneOf(o.Phase, "applying", "recovery", "completed", "rolled-back") {
		return out, errors.New("existing claimed update is required")
	}
	phase, held, e := readPause(ctx, tx)
	if e != nil {
		return out, e
	}
	if phase == "idle" {
		if o.Phase != "applying" {
			return out, errors.New("unfenced state is not an active original dispatch")
		}
	} else if held.Request.IntentID != id || held.Request.Expected.State.Version != schema || held.Request.ManifestSHA256 != o.Release.ManifestSHA256 {
		return out, errors.New("exact unchanged metadata hold required")
	}
	if phase == "released" && (!oneOf(o.Phase, "completed", "rolled-back") || o.Receipt == nil) {
		return out, errors.New("released operation has no authoritative terminal outcome")
	}

	var instance string
	if e = tx.QueryRowContext(ctx, "SELECT instance_id FROM instance WHERE id=1").Scan(&instance); e != nil || (phase != "idle" && instance != held.Request.Expected.InstanceID) {
		return out, errors.New("offline instance identity mismatch")
	}
	out = ApplicationOwnerView{BootID: o.BootID, View: ApplicationUpdateView{Version: 1, InstanceID: instance, Schema: schema, Revision: revision, Operation: o}}
	if phase == "idle" {
		return out, nil
	}
	recovery, e := recoveryDirectoryMode(directory, id, false)
	if e != nil {
		return out, e
	}
	defer unix.Close(recovery)
	raw, e := recoveryRead(recovery, "application.json", 16384)
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	var checkpoint ApplicationCheckpoint
	if update.StrictJSON(raw, &checkpoint) != nil || checkpoint.OperationID != id || checkpoint.ManifestSHA256 != o.Release.ManifestSHA256 || checkpoint.Pause.PauseID != held.PauseID || checkpoint.Snapshot.File != "application-"+checkpoint.Snapshot.SHA256+".sqlite" {
		return out, errors.New("offline checkpoint binding mismatch")
	}
	reader := &Store{bootID: checkpoint.Pause.Core.BootID}
	if e = reader.verifyMetadata(ctx, recovery, checkpoint.Snapshot, update.Preflight{Pause: checkpoint.Pause}); e != nil {
		return out, e
	}
	out.Checkpoint = &checkpoint
	return out, nil
}

func openApplicationMetadataDirectory(directory string) (*os.File, error) {
	fd, e := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	d := os.NewFile(uintptr(fd), directory)
	db, e := unix.Openat(fd, "core.sqlite", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		d.Close()
		return nil, e
	}
	var st unix.Stat_t
	e = unix.Fstat(db, &st)
	unix.Close(db)
	if e != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != os.Geteuid() || st.Mode&0077 != 0 || st.Nlink != 1 {
		d.Close()
		return nil, errors.New("private original core database required")
	}
	return d, nil
}
