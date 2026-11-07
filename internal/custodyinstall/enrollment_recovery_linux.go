//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"

	"github.com/fased-ai/fased-app/internal/core"
)

type enrollmentCoreRecovery struct {
	Version      int      `json:"version"`
	ID           string   `json:"id"`
	Base         Manifest `json:"base"`
	NextCoreHash string   `json:"nextCoreHash"`
}

func (r enrollmentCoreRecovery) next() Manifest { m := r.Base; m.CoreHash = r.NextCoreHash; return m }
func (r enrollmentCoreRecovery) dir() string    { return r.Base.Root + "/enrollment-core-" + r.ID }
func (r enrollmentCoreRecovery) valid() bool {
	return r.Version == 1 && validHex(r.ID, 16) && r.Base.validate() == nil && r.Base.Version == 3 && r.Base.Enrollment && r.Base.Location == "remote" && !r.Base.NativeOwnerAccess && validHex(r.NextCoreHash, 32) && r.NextCoreHash != r.Base.CoreHash
}

// Read-only preflight. Empty private state and service inactivity are checked
// again under OS consent by RecoverEnrollmentCore before any publication.
func CanRecoverEnrollmentCore(m Manifest, id, next string) bool {
	r := enrollmentCoreRecovery{1, id, m, next}
	return r.valid() && verifyTopology(m) == nil && recoveryFiles(r, readRoot) == nil && recoveryIntent(r) == nil
}

func recoveryIntent(r enrollmentCoreRecovery) error {
	// Either exact generation is recoverable after an interrupted owner-intent
	// write. The root receipt remains authoritative for any changed generation.
	old, next := OwnsEnrollmentIntent(r.Base), OwnsEnrollmentIntent(r.next())
	inventoryDone, e := recoveryMark(r, "inventory-replaced", readRoot)
	if e != nil {
		return e
	}
	ownerDone, e := recoveryMark(r, "owner-intent-replaced", readRoot)
	if e != nil {
		return e
	}
	if old && !ownerDone || next && inventoryDone {
		return nil
	}
	return errors.New("enrollment owner intent changed; preserve state")
}

func recoveryFiles(r enrollmentCoreRecovery, read readHost) error {
	if !r.valid() {
		return errors.New("invalid pre-import core recovery")
	}
	for _, name := range []string{"enrollment-ready.json", "ready.json", "enrollment.json", "update-active.json", "updates", "bootstrap-active.json"} {
		if _, err := read(r.Base.Root + "/" + name); !errors.Is(err, os.ErrNotExist) {
			return errors.New("enrollment is committed, held or unclassified; preserve state")
		}
	}
	coreFile, err := read(r.Base.Root + "/fased-app")
	if err != nil || coreFile.UID != 0 || !coreFile.Regular || coreFile.Mode != 0755 {
		return errors.New("untrusted enrollment core")
	}
	inventory, err := read(r.Base.Root + "/install.json")
	if err != nil {
		return err
	}
	signer, err := read(r.Base.Root + "/fased-signer")
	if err != nil || signer.UID != 0 || !signer.Regular || signer.Mode != 0755 || sum(signer.Data) != r.Base.SignerHash {
		return errors.New("enrollment signer changed")
	}
	receipt, err := read(r.dir() + "/recovery.json")
	if errors.Is(err, os.ErrNotExist) {
		if sum(coreFile.Data) != r.Base.CoreHash || !known(inventory, mustJSON(r.Base), 0644) {
			return errors.New("no recovery receipt for changed installation")
		}
		return nil
	}
	if err != nil || !known(receipt, mustJSON(r), 0644) {
		return errors.New("foreign enrollment recovery receipt")
	}
	for _, row := range []struct {
		name, hash string
		mode       os.FileMode
	}{{"previous-core", r.Base.CoreHash, 0755}, {"previous-manifest", sum(mustJSON(r.Base)), 0644}, {"next-core", r.NextCoreHash, 0755}, {"previous-owner-intent.json", sum(mustJSON(recoveryOwnerIntent(r.Base))), 0644}} {
		f, e := read(r.dir() + "/" + row.name)
		if e != nil || f.UID != 0 || !f.Regular || f.Mode != row.mode || sum(f.Data) != row.hash {
			return errors.New("enrollment recovery evidence changed")
		}
	}
	coreNew := sum(coreFile.Data) == r.NextCoreHash
	manifestNew := known(inventory, mustJSON(r.next()), 0644)
	if !coreNew && sum(coreFile.Data) != r.Base.CoreHash || !manifestNew && !known(inventory, mustJSON(r.Base), 0644) || manifestNew && !coreNew {
		return errors.New("foreign or out-of-order enrollment generation")
	}
	coreDone, err := recoveryMark(r, "core-replaced", read)
	if err != nil {
		return err
	}
	inventoryDone, err := recoveryMark(r, "inventory-replaced", read)
	if err != nil {
		return err
	}
	ownerDone, err := recoveryMark(r, "owner-intent-replaced", read)
	if err != nil {
		return err
	}
	if coreDone && !coreNew || manifestNew && !coreDone || inventoryDone && !manifestNew || ownerDone && !inventoryDone {
		return errors.New("conflicting enrollment recovery phase")
	}
	return nil
}

func recoveryMark(r enrollmentCoreRecovery, name string, read readHost) (bool, error) {
	f, e := read(r.dir() + "/" + name + ".json")
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil || !known(f, mustJSON(phaseRecord{1, sum(mustJSON(r)), name}), 0644) {
		return false, errors.New("foreign enrollment recovery phase")
	}
	return true, nil
}

func recoverEnrollmentCore(ctx context.Context, r enrollmentCoreRecovery, next []byte, o installOps, ownerIntent func() error) (Manifest, error) {
	if !r.valid() || sum(next) != r.NextCoreHash || o.topology(r.Base) != nil {
		return Manifest{}, errors.New("verified empty enrollment target required")
	}
	if err := recoveryFiles(r, o.read); err != nil {
		return Manifest{}, err
	}
	old, e := o.read(r.Base.Root + "/fased-app")
	if e != nil {
		return Manifest{}, e
	}
	// Preserve originals BEFORE publishing the receipt or replacing live files.
	previous, e := o.read(r.dir() + "/previous-core")
	if errors.Is(e, os.ErrNotExist) {
		previous = old
	} else if e != nil {
		return Manifest{}, e
	}
	if sum(previous.Data) != r.Base.CoreHash || previous.Mode != 0755 {
		return Manifest{}, errors.New("previous enrollment core unavailable")
	}
	for _, row := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{{"previous-core", previous.Data, 0755}, {"previous-manifest", mustJSON(r.Base), 0644}, {"next-core", next, 0755}, {"recovery.json", mustJSON(r), 0644}} {
		if err := publish(ctx, r.dir()+"/"+row.name, r.ID, row.data, row.mode, o.read, o.run); err != nil {
			return Manifest{}, err
		}
	}
	mark := func(name string) error {
		if err := publish(ctx, r.dir()+"/"+name+".json", r.ID, mustJSON(phaseRecord{1, sum(mustJSON(r)), name}), 0644, o.read, o.run); err != nil {
			return err
		}
		if o.checkpoint != nil {
			return o.checkpoint(name)
		}
		return nil
	}
	if err := replaceRecord(ctx, r.Base.Root+"/fased-app", r.Base.ID, next, o); err != nil {
		return Manifest{}, err
	}
	if err := mark("core-replaced"); err != nil {
		return Manifest{}, err
	}
	if err := replaceRecord(ctx, r.Base.Root+"/install.json", r.Base.ID, mustJSON(r.next()), o); err != nil {
		return Manifest{}, err
	}
	if err := mark("inventory-replaced"); err != nil {
		return Manifest{}, err
	}
	if err := ownerIntent(); err != nil {
		return Manifest{}, err
	}
	if err := mark("owner-intent-replaced"); err != nil {
		return Manifest{}, err
	}
	return r.next(), nil
}

// RecoverEnrollmentCore is initial-setup recovery, not an installed updater.
// It never touches keys, records, grants, service policy or signer executables.
func (s *System) RecoverEnrollmentCore(ctx context.Context, m Manifest, id, path, hash string) (Manifest, error) {
	r := enrollmentCoreRecovery{1, id, m, hash}
	if os.Getuid() != m.Owner || !r.valid() {
		return Manifest{}, errors.New("ordinary Remote enrollment owner required")
	}
	lock, err := OwnerLock()
	if err != nil {
		return Manifest{}, err
	}
	defer lock.Close()
	next, err := source(path)
	if err != nil || sum(next) != hash {
		return Manifest{}, errors.New("approved recovery core changed")
	}
	if err = verifyTopology(m); err != nil {
		return Manifest{}, err
	}
	if err = recoveryIntent(r); err != nil {
		return Manifest{}, err
	}
	if err = recoveryFiles(r, readRoot); err != nil {
		return Manifest{}, err
	}
	if err = checkEnrollmentRecoveryRoles(ctx, m, readRoot, s.Run); err != nil {
		return Manifest{}, err
	}
	if _, e := readRoot(r.dir() + "/recovery.json"); errors.Is(e, os.ErrNotExist) {
		var out bytes.Buffer
		if e = s.EnrollmentImage(ctx, m, "empty", m.Root+"/app-state", "", "", nil, &out); e != nil {
			return Manifest{}, e
		}
		var empty struct {
			Version int  `json:"version"`
			Empty   bool `json:"empty"`
		}
		if strict(out.Bytes(), &empty) != nil || empty.Version != 1 || !empty.Empty {
			return Manifest{}, errors.New("target app state is not empty")
		}
		if e = s.EnrollmentSignerEmpty(ctx, m); e != nil {
			return Manifest{}, e
		}
	} else if e != nil {
		return Manifest{}, e
	}
	if err = checkEnrollmentRecoveryEmpty(ctx, m, s.Run); err != nil {
		return Manifest{}, err
	}
	if err = directory(ctx, r.dir(), 0, 0, 0755, s); err != nil {
		return Manifest{}, err
	}
	intentDir, err := ownerJournal()
	if err != nil {
		return Manifest{}, err
	}
	oldIntent, newIntent := recoveryOwnerIntent(m), recoveryOwnerIntent(r.next())
	if !OwnsEnrollmentIntent(m) && !OwnsEnrollmentIntent(r.next()) {
		return Manifest{}, errors.New("foreign owner intent")
	}
	oldBytes := mustJSON(oldIntent)
	if err := publish(ctx, r.dir()+"/previous-owner-intent.json", r.ID, oldBytes, 0644, readRoot, s.Run); err != nil {
		return Manifest{}, err
	}
	ownerWrite := func() error {
		current, e := core.ReadPrivateFile(intentDir + "/intent.json")
		if e != nil {
			return e
		}
		var parsed journal
		if strict(current, &parsed) != nil || parsed != oldIntent && parsed != newIntent {
			return errors.New("owner intent changed during recovery")
		}
		return core.WritePrivateJSON(intentDir+"/intent.json", newIntent)
	}
	return recoverEnrollmentCore(ctx, r, next, installOps{read: readRoot, run: s.Run, topology: verifyTopology}, ownerWrite)
}

func recoveryOwnerIntent(m Manifest) journal {
	return journal{NativeOwnerAccess: m.NativeOwnerAccess, Mode: m.Mode, Profile: m.Profile, Provenance: m.Provenance, Enrollment: m.Enrollment, Location: m.Location, PublicOrigin: m.PublicOrigin, Network: m.Network, Version: m.Version, ID: m.ID, Owner: m.Owner, CoreHash: m.CoreHash, SignerHash: m.SignerHash, Listen: m.Listen}
}

func checkEnrollmentRecoveryRoles(ctx context.Context, m Manifest, read readHost, run runHost) error {
	for _, role := range []string{"app", "signer"} {
		b, e := run(ctx, nil, "/usr/bin/systemctl", "show", "--property=LoadState,ActiveState,MainPID,UnitFileState,FragmentPath", "--", Unit(m.Owner, role))
		fields := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok {
				fields[k] = v
			}
		}
		absent := fields["LoadState"] == "not-found" && fields["UnitFileState"] == "" && fields["FragmentPath"] == "" && (fields["MainPID"] == "" || fields["MainPID"] == "0")
		if e != nil || fields["ActiveState"] != "inactive" || !absent {
			return errors.New("StageOnly target units must be absent and inactive")
		}
		if _, e := read("/etc/systemd/system/" + Unit(m.Owner, role)); !errors.Is(e, os.ErrNotExist) {
			return errors.New("target unit file already exists; preserve it")
		}
	}
	return nil
}

func checkEnrollmentRecoveryEmpty(ctx context.Context, m Manifest, run runHost) error {
	for _, dir := range []string{"app-state", "signer-state", "signer-ipc"} {
		b, e := run(ctx, nil, "/usr/bin/find", m.Root+"/"+dir, "-mindepth", "1", "-maxdepth", "1", "-printf", "x", "-quit")
		if e != nil || len(b) != 0 {
			return errors.New("target state is not empty; preserve all state")
		}
	}
	return nil
}
