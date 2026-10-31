//go:build linux

package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// A new schema35 handoff must be reserved while its original core is unpaused.
// The reservation binds the owner lease and prior updater state; an old paused
// operation cannot become a new migration by changing its request version.
type applicationReaderPreparation struct {
	Version         int                               `json:"version"`
	Request         ApplicationReaderBootstrapRequest `json:"request"`
	OperationSHA256 string                            `json:"operationSha256"`
	SourcePhase     string                            `json:"sourcePhase"`
	SourcePause     pauseRecord                       `json:"sourcePause"`
}

func readerIntent(r ApplicationReaderBootstrapRequest) ApplicationReaderBootstrapRequest {
	r.Predecessor.State, r.Predecessor.PauseID = "prepared", ""
	return r
}

// Boot/probe challenges are ephemeral; source metadata, release and lease are
// immutable. Canonicalization never edits the retained original preparation.
func readerIntentDigest(r ApplicationReaderBootstrapRequest) string {
	r = readerIntent(r)
	r.Predecessor.Core.BootID = ""
	r.Predecessor.Core.ProbeID = ""
	if r.Predecessor.Request != nil {
		copyRequest := *r.Predecessor.Request
		copyRequest.Expected.BootID = ""
		copyRequest.Expected.ProbeID = ""
		r.Predecessor.Request = &copyRequest
	}
	return update.SnapshotDigest("reader-intent", r)
}

func readReaderPreparation(directory, id string) (applicationReaderPreparation, error) {
	var p applicationReaderPreparation
	d, err := recoveryDirectoryMode(directory, id, false)
	if err != nil {
		return p, err
	}
	defer unix.Close(d)
	b, err := recoveryRead(d, "reader-preparation.json", 16384)
	if err != nil {
		return p, err
	}
	if update.StrictJSON(b, &p) != nil || p.Version != 1 || !p.Request.prepared() || p.Request.OperationID != id || !hexValue(p.OperationSHA256, 32) || !oneOf(p.SourcePhase, "idle", "released") {
		return p, errors.New("invalid original reader preparation; preserve state")
	}
	return p, nil
}

// PrepareApplicationReaderBootstrap grants no update or pause. The owner driver
// supplies an independently verified release and then uses the old pause API.
func PrepareApplicationReaderBootstrap(ctx context.Context, directory string, request ApplicationReaderBootstrapRequest) error {
	if !request.prepared() {
		return errors.New("new schema35 owner intent required")
	}
	if p, err := readReaderPreparation(directory, request.OperationID); err == nil {
		if readerIntentDigest(p.Request) != readerIntentDigest(request) {
			return errors.New("original reader intent changed")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := CheckApplicationReaderPredecessor(ctx, directory, request.Predecessor.Core); err != nil {
		return err
	}
	dir, err := openApplicationMetadataDirectory(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:/proc/self/fd/%d/core.sqlite?mode=rw", dir.Fd()))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer tx.Close()
	if _, err = tx.ExecContext(ctx, "PRAGMA busy_timeout=5000; BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer tx.ExecContext(context.Background(), "ROLLBACK")
	phase, pause, err := readPause(ctx, tx)
	if err != nil {
		return err
	}
	if phase == "paused" {
		return ErrCorePaused
	}
	got, err := applicationReaderSourceSnapshot(ctx, tx, request.Predecessor.Core.ProbeID, request.Predecessor.Core.BootID, 35)
	if err != nil || !update.SameCoreMetadata(got, request.Predecessor.Core) {
		return errors.New("source changed before reader preparation")
	}
	operation, digest, err := reader35Operation(ctx, tx)
	if err != nil {
		return err
	}
	if err = reader35Quiescent(ctx, tx, directory, operation, phase, pause); err != nil {
		return err
	}
	if operation.ID == request.OperationID {
		return errors.New("a new reader operation identity is required")
	}
	p := applicationReaderPreparation{Version: 1, Request: request, OperationSHA256: digest, SourcePhase: phase, SourcePause: pause}
	d, err := recoveryDirectory(directory, request.OperationID)
	if err != nil {
		return err
	}
	defer unix.Close(d)

	b, _ := json.Marshal(p)
	s := &Store{dir: directory}
	name := "reader-preparation-" + metadataHash(b) + ".json"
	entries, err := os.ReadDir(fmt.Sprintf("/proc/self/fd/%d", d))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != name && entry.Name() != name+".pending" {
			return errors.New("existing recovery namespace must be preserved")
		}
	}
	if err = s.publishRecovery(d, name, b); err != nil {
		return err
	}
	if err = unix.Renameat2(d, name, d, "reader-preparation.json", unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return unix.Fsync(d)
}

func reader35Operation(ctx context.Context, r snapshotReader) (ApplicationUpdateOperation, string, error) {
	o, revision, err := readApplicationUpdate(ctx, r)
	if err != nil {
		return o, "", err
	}
	var raw string
	if err = r.QueryRowContext(ctx, "SELECT document FROM application_updates WHERE id=1").Scan(&raw); err != nil {
		return o, "", err
	}
	return o, update.SnapshotDigest("reader-source-updater", struct {
		Document string
		Revision int64
	}{raw, revision}), nil
}

func reader35Quiescent(ctx context.Context, r snapshotReader, directory string, o ApplicationUpdateOperation, phase string, pause pauseRecord) error {
	switch o.Phase {
	case "idle", "current", "available", "check-failed":
		if o.OwnerLease == "" && o.Receipt == nil {
			return nil
		}
		if o.Phase == "check-failed" {
			return ordinaryCustodySettledWithPause(ctx, r, directory, o, phase, pause)
		}
	case "completed", "rolled-back":
		return ordinaryCustodySettledWithPause(ctx, r, directory, o, phase, pause)
	}
	return errors.New("original updater operation must finish without replacing its lease or target")
}

func verifyReader35Preparation(ctx context.Context, r snapshotReader, directory string, request ApplicationReaderBootstrapRequest) error {
	p, err := readReaderPreparation(directory, request.OperationID)
	if err != nil {
		return err
	}
	if readerIntentDigest(p.Request) != readerIntentDigest(request) {
		return errors.New("reader handoff differs from original unpaused preparation")
	}
	o, digest, err := reader35Operation(ctx, r)
	if err != nil {
		return err
	}
	if digest != p.OperationSHA256 {
		return errors.New("prior updater state changed; preserve original transaction")
	}
	return reader35Quiescent(ctx, r, directory, o, p.SourcePhase, p.SourcePause)
}
