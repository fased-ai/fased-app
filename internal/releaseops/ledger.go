package releaseops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
)

// Immutable public bindings only. This is a disposable publisher journal, never
// an installed trust/financial store or authorization to publish anything.
type release struct {
	FixtureOnly    bool     `json:"fixtureOnly"`
	Purpose        string   `json:"purpose"`
	RepositoryID   string   `json:"repositoryId"`
	ReleaseID      string   `json:"releaseId"`
	Sequence       uint64   `json:"sequence,string"`
	UnitSHA256     string   `json:"unitSha256"`
	PreserveSHA256 string   `json:"preserveSha256"`
	InstalledFloor uint64   `json:"installedFloor,string"`
	ReservedFloor  uint64   `json:"reservedFloor,string"`
	Objects        []object `json:"objects"`
}

func (r release) valid() bool {
	if !r.FixtureOnly || r.Purpose != purpose || !number(r.RepositoryID) || !number(r.ReleaseID) || r.Sequence <= r.ReservedFloor || r.ReservedFloor < r.InstalledFloor || !hashOK(r.UnitSHA256) || !hashOK(r.PreserveSHA256) || len(r.Objects) != 10 {
		return false
	}
	names := []string{"core.fixture", "signer.fixture", "owner-helper.fixture", "installer.fixture", "evidence.json", "root.json", "targets.json", "snapshot.json", "timestamp.json", "descriptor.json"}
	ids := map[string]bool{}
	for i, o := range r.Objects {
		if o.Name != names[i] || !hashOK(o.SHA256) || o.Size <= 0 || o.Size > 64<<10 {
			return false
		}
		if i < 4 {
			if !number(o.AssetID) || ids[o.AssetID] {
				return false
			}
			ids[o.AssetID] = true
		} else if o.AssetID != "" {
			return false
		}
	}
	return true
}

type ledger struct {
	root  *os.Root
	fault func(string) error
}

func openLedger(dir string) (*ledger, error) {
	if os.Geteuid() == 0 || !filepath.IsAbs(dir) {
		return nil, errors.New("ordinary-user absolute fixture ledger required")
	}
	s, e := os.Lstat(dir)
	if e != nil || !s.IsDir() || s.Mode().Perm() != 0700 || !owned(s) {
		return nil, errors.New("real private fixture ledger required")
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	a, e := r.Stat(".")
	if e != nil || !os.SameFile(s, a) {
		_ = r.Close()
		return nil, errors.New("fixture ledger changed")
	}
	return &ledger{root: r}, nil
}
func (l *ledger) close() error { return l.root.Close() }
func (l *ledger) read(name string) ([]byte, error) {
	s, e := l.root.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || !owned(s) || s.Size() > 64<<10 {
		return nil, errors.New("private bounded ledger record required")
	}
	f, e := l.root.OpenFile(name, os.O_RDONLY|noFollow, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	a, e := f.Stat()
	if e != nil || !os.SameFile(a, s) {
		return nil, errors.New("ledger record changed")
	}
	return io.ReadAll(io.LimitReader(f, 64<<10+1))
}
func (l *ledger) put(name string, b []byte) error {
	if old, e := l.read(name); e == nil {
		if bytes.Equal(old, b) {
			return l.sync(name)
		}
		return errors.New("conflicting immutable fixture ledger record")
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := l.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0600)
	if e != nil {
		return e
	}
	// Partial records deliberately survive. A torn reservation must block future
	// promotion, not disappear and make its sequence available for other bytes.
	if l.fault != nil {
		if e = l.fault("created"); e != nil {
			_ = f.Close()
			return e
		}
	}
	n, e := f.Write(b)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if l.fault != nil {
		if e = l.fault("file-synced"); e != nil {
			return e
		}
	}
	return l.sync(name)
}

func (l *ledger) sync(name string) error {
	// An exact retry must finish durability after an interrupted acknowledgement;
	// equality of cached bytes alone is not a successful durable reservation.
	f, e := l.root.OpenFile(name, os.O_RDONLY|noFollow, 0)
	if e != nil {
		return e
	}
	e = errors.Join(f.Sync(), f.Close())
	if e != nil {
		return e
	}
	d, e := l.root.Open(".")
	if e != nil {
		return e
	}
	e = errors.Join(d.Sync(), d.Close())
	if e != nil {
		return e
	}
	if l.fault != nil {
		return l.fault("synced")
	}
	return nil
}
func (l *ledger) transaction(r release, complete bool) error {
	if !r.valid() {
		return errors.New("invalid inert fixture release")
	}
	f, e := l.root.OpenFile("lock", os.O_RDWR|os.O_CREATE|noFollow, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || !owned(s) {
		return errors.New("invalid fixture ledger lock")
	}
	if e = lock(f); e != nil {
		return e
	}
	defer unlock(f)
	d, e := l.root.Open(".")
	if e != nil {
		return e
	}
	entries, e := d.ReadDir(258)
	_ = d.Close()
	if e != nil && e != io.EOF || len(entries) > 257 {
		return errors.New("fixture ledger bound exceeded")
	}
	var max uint64
	for _, entry := range entries {
		if entry.Name() == "lock" {
			continue
		}
		b, e := l.read(entry.Name())
		var v release
		if e != nil || update.StrictJSON(b, &v) != nil || !v.valid() || entry.Name() != strconv.FormatUint(v.Sequence, 10)+".reserved.json" && entry.Name() != strconv.FormatUint(v.Sequence, 10)+".readback.json" {
			return errors.New("foreign, missing or corrupt fixture ledger; preserve for recovery")
		}
		if v.RepositoryID != r.RepositoryID || v.PreserveSHA256 != r.PreserveSHA256 || v.InstalledFloor != r.InstalledFloor || v.ReservedFloor != r.ReservedFloor {
			return errors.New("fixture repository or preservation anchors changed")
		}
		if v.Sequence > max {
			max = v.Sequence
		}
		if entry.Name() == strconv.FormatUint(v.Sequence, 10)+".readback.json" {
			previous, e := l.read(strconv.FormatUint(v.Sequence, 10) + ".reserved.json")
			if e != nil || !bytes.Equal(previous, b) {
				return errors.New("unpaired fixture readback record")
			}
		}
	}
	b, _ := json.Marshal(r)
	name := strconv.FormatUint(r.Sequence, 10) + ".reserved.json"
	old, e := l.read(name)
	if e == nil {
		if !bytes.Equal(old, b) {
			return errors.New("sequence already bound to different fixture bytes")
		}
		if e = l.put(name, b); e != nil {
			return e
		}
	} else {
		if !os.IsNotExist(e) || complete || r.Sequence <= max || len(entries) >= 257 {
			return errors.New("missing or nonmonotonic fixture reservation")
		}
		if e = l.put(name, b); e != nil {
			return e
		}
	}
	if complete {
		name := strconv.FormatUint(r.Sequence, 10) + ".readback.json"
		if _, e := l.read(name); os.IsNotExist(e) && len(entries) >= 257 {
			return errors.New("fixture ledger bound exceeded")
		}
		return l.put(name, b)
	}
	return nil
}

// Reserve before any external work; readback all frozen bytes and invoke the
// real fixture TUF/provenance verifier before marking success. No publication,
// extraction, execution, financial store or installed controller is reachable.
func rehearse(ctx context.Context, l *ledger, r release, t *transport, scratch string, trust update.TrustRehearsalOptions, admission update.ReleaseAdmissionOptions) error {
	if e := l.transaction(r, false); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	dir, e := os.MkdirTemp(scratch, "readback-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	artifacts := filepath.Join(dir, "artifacts")
	if e = os.Mkdir(artifacts, 0700); e != nil {
		return e
	}
	for i, o := range r.Objects {
		b, e := t.fetch(ctx, r.RepositoryID, o)
		if e != nil {
			return e
		}
		if i < 4 {
			if e = os.WriteFile(filepath.Join(artifacts, o.Name), b, 0600); e != nil {
				return e
			}
		}
	}
	admission.ArtifactDir = artifacts
	_, report, e := update.AdmitFixtureRelease(ctx, trust, admission)
	if e != nil {
		return e
	}
	var snapshot update.TrustFixtureSnapshot
	if !report.ReleaseUnitVerified || !report.ProvenanceVerified || !report.ArtifactsVerified || report.UnitSHA256 != r.UnitSHA256 || report.Sequence != r.Sequence || report.EvidenceSHA256 != r.Objects[4].SHA256 || update.StrictJSON(report.NextState, &snapshot) != nil || snapshot.InstalledFloor != r.InstalledFloor || snapshot.ReservedFloor != r.ReservedFloor || snapshot.PreserveSHA256 != r.PreserveSHA256 || report.InstallAllowed || report.ResumeAllowed || report.UpdaterEnabled || report.WalletSigner || report.FinancialExecution {
		return errors.New("verified fixture readback/preservation binding refused")
	}
	if e = ctx.Err(); e != nil {
		return errors.New("fixture readback deadline expired")
	}
	return l.transaction(r, true)
}
