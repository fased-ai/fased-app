//go:build linux

package vpsinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/update"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// This root-owned public record contains code/release identities only. AppUID
// cannot read the administrator's private journal and receives no lease, DB,
// credentials or signing authority through this retained-code admission.
func applicationOwnerAdmission(j applicationJournal) applicationJournal {
	j.Status.Phase = "prepared"
	return j
}

func stageApplicationOwnerAdmission(ctx context.Context, o hostOps, j applicationJournal, dir string) error {
	if !applicationJournalValid(j, o.uid) || dir != j.Previous.Root+"-update-"+j.Status.ID {
		return errors.New("exact Remote owner helper admission required")
	}
	return publishApplicationStage(ctx, o, dir+"/owner-helper.json", encoded(applicationOwnerAdmission(j)), 0644)
}

func activeApplicationOwnerJournal(j applicationJournal) bool {
	return j.Status.Phase != "completed" && j.Status.Phase != "rolled-back"
}

// LoadApplicationMaintenance admits an existing interrupted app-only transaction
// without weakening ordinary Load. It cannot discover/install a new release or
// recover a missing journal: only its exact original/target public generations
// may be mixed while an atomic replacement is incomplete.
func LoadApplicationMaintenance(ownerUID int, id string) (Manifest, error) {
	return loadApplicationMaintenance(liveOps(nil), ownerUID, id)
}

func loadApplicationMaintenance(o hostOps, ownerUID int, id string) (Manifest, error) {
	if e := ordinary(o); e != nil {
		return Manifest{}, e
	}
	if ownerUID != o.uid || !canonicalHex(id, 16) {
		return Manifest{}, errors.New("exact existing Remote owner transaction required")
	}
	j, e := readApplicationJournal(o)
	if e != nil {
		return Manifest{}, e
	}
	if j.Status.ID != id || !activeApplicationOwnerJournal(j) {
		return Manifest{}, errors.New("same active Remote maintenance journal required")
	}
	m, e := manifestAt(o, ownerUID)
	if e != nil {
		return Manifest{}, e
	}
	if m != j.Previous && m != j.Target {
		return Manifest{}, errors.New("Remote maintenance manifest contradicts original transaction")
	}
	if e = verifyApplicationOwnerGeneration(o, j); e != nil {
		return Manifest{}, e
	}
	if e = verifyApplicationOwnerRetained(o, j, m.Root+"-update-"+id); e != nil {
		return Manifest{}, e
	}
	return m, nil
}

func applicationOwnerImage(o hostOps, m Manifest, id string) (string, error) {
	j, e := readApplicationJournal(o)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	if e == nil && activeApplicationOwnerJournal(j) {
		if j.Status.ID != id || m != j.Previous && m != j.Target {
			return "", errors.New("another Remote owner transaction needs recovery")
		}
		dir := m.Root + "-update-" + id
		f, recordErr := rootFile(o, dir+"/owner-helper.json")
		if recordErr == nil {
			if !known(f, encoded(applicationOwnerAdmission(j)), 0644) {
				return "", errors.New("Remote retained helper admission contradicts owner journal")
			}
			if e = verifyApplicationOwnerGeneration(o, j); e != nil {
				return "", e
			}
			if e = verifyApplicationOwnerRetained(o, j, dir); e != nil {
				return "", e
			}
			return dir + "/previous-core", nil
		}
		if !errors.Is(recordErr, os.ErrNotExist) {
			return "", recordErr
		}
		// Before staging is complete the admitted original may still serve IPC.
		// Missing retained admission never authorizes executing a backup.
	}
	if e = verifyFiles(o, m); e != nil {
		return "", e
	}
	return m.Core, nil
}

func applicationReaderOwnerImage(o hostOps, m Manifest, id string) (string, error) {
	j, e := readApplicationJournal(o)
	if e != nil {
		return "", e
	}
	if j.Status.ID != id || !j.Status.ReaderBootstrap || !activeApplicationOwnerJournal(j) || (m != j.Previous && m != j.Target) {
		return "", errors.New("exact active reader-bootstrap transaction required")
	}
	if e = verifyApplicationOwnerGeneration(o, j); e != nil {
		return "", e
	}
	dir := m.Root + "-update-" + id
	if e = verifyApplicationOwnerRetained(o, j, dir); e != nil {
		return "", e
	}
	return dir + "/next-core", nil
}

// ApplicationReaderBootstrapTemplate reconstructs only the already staged,
// consumed exact release before a predecessor lease was durably saved. The
// dropped target must still authenticate the old33 session and original pause.
func ApplicationReaderBootstrapTemplate(m Manifest, id, lease string) (core.ApplicationReaderBootstrapRequest, error) {
	var r core.ApplicationReaderBootstrapRequest
	o := liveOps(nil)
	if e := ownedBy(o, m); e != nil {
		return r, e
	}
	if !update.IDOK(lease) {
		return r, errors.New("exact owner reader lease required")
	}
	if _, e := applicationReaderOwnerImage(o, m, id); e != nil {
		return r, e
	}
	j, e := readApplicationJournal(o)
	if e != nil {
		return r, e
	}
	return core.ApplicationReaderBootstrapRequest{Version: 1, OperationID: id, OwnerLease: lease, PreviousEngineSHA256: j.Previous.CoreSHA256, Release: core.ApplicationRelease{Version: j.ProductVersion, EngineSHA256: j.Target.CoreSHA256, Sequence: j.Status.Sequence, ManifestSHA256: j.Status.ReleaseSHA256, Target: update.Target{OS: "linux", Arch: runtime.GOARCH}}}, nil
}

// The dropped target may handle a reader handoff only for its original fixed
// public admission. This does not grant an arbitrary helper or OS authority.
func ValidateApplicationReaderOwnerRequest(m Manifest, image string, r core.ApplicationReaderBootstrapRequest) error {
	return validateApplicationReaderOwnerRequest(liveOps(nil), m, image, r)
}

func validateApplicationReaderOwnerRequest(o hostOps, m Manifest, image string, r core.ApplicationReaderBootstrapRequest) error {
	if !update.IDOK(r.OperationID) || (r.Version != 1 && r.Version != 2 && r.Version != 3) || !update.IDOK(r.OwnerLease) || image != m.Root+"-update-"+r.OperationID+"/next-core" {
		return errors.New("fixed admitted target reader helper required")
	}
	dir := m.Root + "-update-" + r.OperationID
	f, e := rootFile(o, dir+"/owner-helper.json")
	var j applicationJournal
	if e != nil || strict(f.Data, &j) != nil || !applicationJournalValid(j, m.OwnerUID) || !j.Status.ReaderBootstrap || j.Status.ID != r.OperationID || (m != j.Previous && m != j.Target) || j.Status.Phase != "prepared" {
		return errors.New("reader-bootstrap target admission changed")
	}
	if r.PreviousEngineSHA256 != j.Previous.CoreSHA256 || r.Release.EngineSHA256 != j.Target.CoreSHA256 || r.Release.Sequence != j.Status.Sequence || r.Release.ManifestSHA256 != j.Status.ReleaseSHA256 || r.Release.Version != j.ProductVersion || r.Release.Target != (update.Target{OS: "linux", Arch: runtime.GOARCH}) {
		return errors.New("reader-bootstrap release differs from admitted transaction")
	}
	if e = verifyApplicationOwnerGeneration(o, j); e != nil {
		return e
	}
	return verifyApplicationOwnerRetained(o, j, dir)
}

// ValidateApplicationOwnerHelper admits only the exact app-UID metadata helper
// image. A retained original is accepted through its fixed root-owned public
// admission, never a caller-selected binary, copied session or privileged grant.
func ValidateApplicationOwnerHelper(config, actualExecutable string, uid int) (Manifest, error) {
	return validateApplicationOwnerHelper(config, actualExecutable, uid, liveOps(nil))
}

func validateApplicationOwnerHelper(config, actualExecutable string, uid int, o hostOps) (Manifest, error) {
	var m Manifest
	f, e := rootFile(o, config)
	if e != nil || strict(f.Data, &m) != nil || m.validate() != nil || m.Config != config {
		return Manifest{}, errors.New("fixed root-owned Remote helper configuration required")
	}
	if uid <= 0 || o.uid != uid || o.euid != uid || uid != m.AppUID || o.gid != m.AppGID {
		return Manifest{}, errors.New("dedicated Remote application identity required")
	}
	image, e := o.executable()
	if e != nil || image != actualExecutable {
		return Manifest{}, errors.New("actual Remote helper process image required")
	}
	groups, e := o.groups()
	if e != nil {
		return Manifest{}, e
	}
	for _, group := range groups {
		if group != m.AppGID {
			return Manifest{}, errors.New("Remote helper has unexpected supplementary groups")
		}
	}
	if actualExecutable == m.Core {
		return m, verifyFiles(o, m)
	}
	if filepath.Clean(actualExecutable) != actualExecutable {
		return Manifest{}, errors.New("fixed retained Remote helper image required")
	}
	tail, ok := strings.CutPrefix(actualExecutable, m.Root+"-update-")
	if !ok || len(tail) < 32 || !canonicalHex(tail[:32], 16) || (tail[32:] != "/previous-core" && tail[32:] != "/next-core") {
		return Manifest{}, errors.New("fixed retained Remote helper image required")
	}
	id := tail[:32]
	dir := m.Root + "-update-" + id
	f, e = rootFile(o, dir+"/owner-helper.json")
	var j applicationJournal
	if e != nil || strict(f.Data, &j) != nil || !applicationJournalValid(j, m.OwnerUID) || j.Status.ID != id || j.Status.Phase != "prepared" || m != j.Previous && m != j.Target || !known(f, encoded(j), 0644) {
		return Manifest{}, errors.New("Remote retained helper admission is missing or foreign")
	}
	if tail[32:] == "/next-core" && !j.Status.ReaderBootstrap {
		return Manifest{}, errors.New("ordinary transaction cannot execute a reader migration helper")
	}
	if e = verifyApplicationOwnerGeneration(o, j); e != nil {
		return Manifest{}, e
	}
	if e = verifyApplicationOwnerRetained(o, j, dir); e != nil {
		return Manifest{}, e
	}
	return m, nil
}

func verifyApplicationOwnerGeneration(o hostOps, j applicationJournal) error {
	m := j.Previous
	for _, d := range []struct {
		path     string
		uid, gid int
		mode     os.FileMode
	}{{m.Root, 0, 0, 0755}, {m.Data, m.AppUID, m.AppGID, 0700}} {
		if e := checkDirectory(o, d.path, d.uid, d.gid, d.mode); e != nil {
			return e
		}
	}
	if e := verifyRootInventory(o, m, ""); e != nil {
		return e
	}
	for _, v := range []struct {
		path      string
		old, next []byte
	}{
		{m.Config, encoded(j.Previous), encoded(j.Target)},
		{m.Root + "/intent.json", encoded(intentFor(j.Previous)), encoded(intentFor(j.Target))},
		{m.Root + "/" + m.Unit, unit(m), unit(m)},
	} {
		f, e := rootFile(o, v.path)
		if e != nil || !known(f, v.old, 0644) && !known(f, v.next, 0644) {
			return errors.New("Remote helper generation metadata changed")
		}
	}
	f, _, sum, e := rootFileDigest(o, m.Core)
	if e != nil || f.Mode.Perm() != 0755 || sum != j.Previous.CoreSHA256 && sum != j.Target.CoreSHA256 {
		return errors.New("Remote helper current generation is not admitted")
	}
	marker, e := o.read(m.Root + "/app-readonly-probe")
	if e != nil || !marker.Regular || marker.UID != m.AppUID || marker.GID != m.AppGID || marker.Mode.Perm() != 0600 || len(marker.Data) != 0 {
		return errors.New("Remote helper runtime marker changed")
	}
	return nil
}

type retainedContent struct {
	path, sha256 string
	size         int64
	data         []byte // Small canonical JSON only; executable payloads stay on disk.
}

func retainedPrefixHash(o hostOps, value retainedContent, length int64) (string, error) {
	if length < 0 || length > value.size {
		return "", errors.New("retained prefix exceeds original content")
	}
	if value.data != nil {
		return hash(value.data[:length]), nil
	}
	var result string
	err := rootFileContent(o, value.path, func(f hostFile, size int64, reader io.Reader) error {
		if f.Mode.Perm() != 0755 || size != value.size {
			return errors.New("retained prefix reference changed")
		}
		full, prefix := sha256.New(), sha256.New()
		n, err := io.CopyN(io.MultiWriter(full, prefix), reader, length)
		if err != nil {
			return err
		}
		rest, err := io.Copy(full, reader)
		if err != nil || n+rest != size || hex.EncodeToString(full.Sum(nil)) != value.sha256 {
			return errors.New("retained prefix reference differs from admitted digest")
		}
		result = hex.EncodeToString(prefix.Sum(nil))
		return nil
	})
	return result, err
}

func verifyApplicationOwnerRetained(o hostOps, j applicationJournal, dir string) error {
	if dir != j.Previous.Root+"-update-"+j.Status.ID {
		return errors.New("Remote retained helper directory changed")
	}
	if e := checkDirectory(o, dir, 0, 0, 0755); e != nil {
		return e
	}
	old, oldSize, oldHash, oe := rootFileDigest(o, dir+"/previous-core")
	next, nextSize, nextHash, ne := rootFileDigest(o, dir+"/next-core")
	if oe != nil || ne != nil || old.Mode.Perm() != 0755 || next.Mode.Perm() != 0755 || oldHash != j.Previous.CoreSHA256 || nextHash != j.Target.CoreSHA256 {
		return errors.New("Remote retained helper code changed")
	}
	previous := retainedContent{path: dir + "/previous-core", size: oldSize, sha256: oldHash}
	target := retainedContent{path: dir + "/next-core", size: nextSize, sha256: nextHash}
	metadata := func(values ...[]byte) []retainedContent {
		result := make([]retainedContent, 0, len(values))
		for _, value := range values {
			result = append(result, retainedContent{size: int64(len(value)), sha256: hash(value), data: value})
		}
		return result
	}
	type publicFile struct {
		content []retainedContent
		mode    os.FileMode
	}
	allowed := map[string]publicFile{
		"previous-core":            {[]retainedContent{previous}, 0755},
		"next-core":                {[]retainedContent{target}, 0755},
		"previous-install.json":    {metadata(encoded(j.Previous)), 0644},
		"next-install.json":        {metadata(encoded(j.Target)), 0644},
		"previous-intent.json":     {metadata(encoded(intentFor(j.Previous))), 0644},
		"next-intent.json":         {metadata(encoded(intentFor(j.Target))), 0644},
		"owner-helper.json":        {metadata(encoded(applicationOwnerAdmission(j))), 0644},
		"replacement-fased-app":    {[]retainedContent{previous, target}, 0755},
		"replacement-install.json": {metadata(encoded(j.Previous), encoded(j.Target)), 0644},
		"replacement-intent.json":  {metadata(encoded(intentFor(j.Previous)), encoded(intentFor(j.Target))), 0644},
	}
	names, e := o.list(dir)
	if e != nil {
		return e
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		base, partial := name, false
		if strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".pending") {
			stem := strings.TrimSuffix(strings.TrimPrefix(name, "."), ".pending")
			if i := strings.LastIndex(stem, "-"); i > 0 && canonicalHex(stem[i+1:], 16) {
				base, partial = stem[:i], true
			}
		}
		p, ok := allowed[base]
		if !ok {
			return errors.New("unclassified retained Remote helper file")
		}
		f, size, sum, err := rootFileDigest(o, dir+"/"+name)
		if err != nil || f.Mode.Perm() != p.mode {
			return errors.New("unclassified or unsafe retained Remote helper file")
		}
		match := false
		for _, value := range p.content {
			if size == value.size && sum == value.sha256 {
				match = true
				break
			}
			if partial && size < value.size {
				prefix, err := retainedPrefixHash(o, value, size)
				if err != nil {
					return err
				}
				if prefix == sum {
					match = true
					break
				}
			}
		}
		if !match {
			return errors.New("retained Remote helper public inventory changed")
		}
		seen[name] = true
	}
	for _, required := range []string{"previous-core", "next-core", "previous-install.json", "next-install.json", "previous-intent.json", "next-intent.json", "owner-helper.json"} {
		if !seen[required] {
			return errors.New("required retained Remote helper file is missing")
		}
	}
	return nil
}
