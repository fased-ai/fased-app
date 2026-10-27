//go:build linux

package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

func inspectionRootRead(path string, limit int) ([]byte, error) {
	if e := inspectionTrustedParents(filepath.Dir(path)); e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return inspectionRead(r, filepath.Base(path), 0, 0644, limit)
}
func inspectionTrustedParents(p string) error {
	for {
		st, e := os.Lstat(p)
		if e != nil || !inspectionFileIdentity(st, 0, true, 0755) {
			return errors.New("root-owned immutable fixture parents required")
		}
		if p == "/" {
			return nil
		}
		p = filepath.Dir(p)
	}
}
func inspectionInstallationRoot(root string) (int, error) {
	const prefix = "/var/lib/fased-app-custody-"
	if len(root) <= len(prefix) || root[:len(prefix)] != prefix {
		return 0, errors.New("fixed installed fixture root required")
	}
	uid, e := strconv.Atoi(root[len(prefix):])
	if e != nil || uid <= 0 || root != fmt.Sprintf("%s%d", prefix, uid) {
		return 0, errors.New("fixed owner root required")
	}
	return uid, inspectionTrustedParents(root)
}

// VerifyInstalledInspectionGuard is deliberately before all state opening. A
// root fence alone is insufficient: capsule, catalog, images and stored/actual
// unit pairs must also match. It never creates files or learns trust.
func VerifyInstalledInspectionGuard(root, role string) (FixtureInspectionFence, error) {
	var f FixtureInspectionFence
	if runtime.GOARCH != "amd64" {
		return f, errors.New("installed fixture requires Linux amd64")
	}
	owner, e := inspectionInstallationRoot(root)
	if e != nil {
		return f, e
	}
	base := root + "/fixture-inspection-v1"
	b, e := inspectionRootRead(base+"/forward.json", 4096)
	if e != nil || StrictJSON(b, &f) != nil || f.Version != 1 || f.Profile != inspectionProfile || !IDOK(f.Provision) || !hashOK(f.CatalogSHA) || !hashOK(f.BridgeSHA) {
		return f, errors.New("fixture reader is held: durable forward fence required")
	}
	capsule, e := inspectionRootRead(base+"/capsules/"+f.Provision+"/transaction.json", MaxDocument)
	if e != nil || digest(capsule) != f.BridgeSHA {
		return f, errors.New("immutable fixture capsule changed")
	}
	inRole := role
	if role == "core" {
		inRole = "owner"
	}
	in, e := openInspectionInputs(base+"/inputs", f.CatalogSHA, inRole, 0)
	if e != nil {
		return f, e
	}
	c := in.catalog
	config, e := inspectionRootRead(root+"/identity.json", MaxDocument)
	if e != nil || digest(config) != c.ConfigSHA || c.RootSHA != digest([]byte(root)) || c.UIDs[0] != owner || c.Provision != f.Provision {
		return f, errors.New("installation/config binding changed")
	}
	expectedUID, slot := owner, 0
	if role == "signer" {
		expectedUID, slot = c.UIDs[2], 1
	} else if role == "core" {
		expectedUID = c.UIDs[1]
	} else if role != "owner" {
		return f, errors.New("unsupported installed fixture role")
	}
	if os.Getuid() != expectedUID || os.Geteuid() != expectedUID || expectedUID <= 0 || os.Getgid() != c.GIDs[map[string]int{"owner": 0, "core": 1, "signer": 2}[role]] {
		return f, errors.New("installed fixture role UID refused")
	}
	self, e := os.Executable()
	if e != nil {
		return f, e
	}
	if e = inspectionTrustedParents(filepath.Dir(self)); e != nil {
		return f, e
	}
	r, e := os.OpenRoot(filepath.Dir(self))
	if e != nil {
		return f, e
	}
	image, e := inspectionRead(r, filepath.Base(self), 0, 0755, 64<<20)
	r.Close()
	if e != nil || digest(image) != c.CandidateImages[slot] {
		return f, errors.New("executing fixture reader differs from pinned capsule")
	}
	for _, system := range []bool{false, true} {
		pair := []string{}
		for _, name := range []string{"app", "signer"} {
			p := root + "/fased-" + name + ".service"
			if system {
				p = fmt.Sprintf("/etc/systemd/system/fased-custody-%d-%s.service", owner, name)
			}
			b, e := inspectionRootRead(p, MaxDocument)
			if e != nil {
				return f, e
			}
			pair = append(pair, string(b))
		}
		h := SnapshotDigest("fixture-unit-pair:v1", pair)
		if h != c.GuardedUnits && h != c.FinalUnits {
			return f, errors.New("fixture guard unit policy changed")
		}
	}
	return f, nil
}

type InstalledInspectionRole struct {
	session          *inspectionSession
	root, role, boot string
}

func OpenInstalledInspectionFixture(root, role string) (*InstalledInspectionRole, error) {
	f, e := VerifyInstalledInspectionGuard(root, role)
	if e != nil {
		return nil, e
	}
	if role != "owner" && role != "signer" {
		return nil, errors.New("private inspection role required")
	}
	in, e := openInspectionInputs(root+"/fixture-inspection-v1/inputs", f.CatalogSHA, role, 0)
	if e != nil {
		return nil, e
	}
	parent := root + "/fixture-inspection-v1/" + role
	if e = inspectionTrustedParents(parent); e != nil {
		return nil, e
	}
	s, e := openInspectionStore(parent+"/state", parent+"/anchor", in)
	if e != nil {
		return nil, e
	}
	x, e := newInspectionSession(s, false)
	if e != nil {
		s.close()
		return nil, e
	}
	return &InstalledInspectionRole{x, root, role, NewProbe()}, nil
}
func InitializeInstalledInspectionFixture(ctx context.Context, root, role string) error {
	f, e := VerifyInstalledInspectionGuard(root, role)
	if e != nil {
		return e
	}
	if role != "owner" && role != "signer" {
		return errors.New("private initialization role required")
	}
	in, e := openInspectionInputs(root+"/fixture-inspection-v1/inputs", f.CatalogSHA, role, 0)
	if e != nil {
		return e
	}
	parent := root + "/fixture-inspection-v1/" + role
	if e = inspectionTrustedParents(parent); e != nil {
		return e
	}
	s, e := openInspectionStore(parent+"/state", parent+"/anchor", in)
	if e != nil {
		return e
	}
	defer s.close()
	// An already complete, anchored initialization is an exact historical retry.
	if b, e := s.load(); e == nil {
		c, e := s.parse(b)
		if e != nil || c.Pending != "" || !c.Historical {
			return errors.New("held fixture initialization")
		}
		return nil
	}
	x, e := newInspectionSession(s, true)
	if e != nil {
		return e
	}
	_, e = x.inspect(ctx)
	return e
}
func (r *InstalledInspectionRole) Close() { r.session.store.close() }
func (r *InstalledInspectionRole) observe(ctx context.Context) (InstalledInspectionStatus, error) {
	var out InstalledInspectionStatus
	if _, e := VerifyInstalledInspectionGuard(r.root, r.role); e != nil {
		return out, e
	}
	v, e := r.session.inspect(ctx)
	out.FixtureOnly = true
	out.Historical = true
	out.Profile = inspectionProfile
	out.Admission = v.Admission
	if e == nil {
		var ev FixtureReleaseEvidence
		if StrictJSON(r.session.store.in.source.files["evidence.json"], &ev) != nil || StrictJSON(ev.Unit, &out.Unit) != nil {
			return out, errors.New("verified fixture unit unavailable")
		}
		out.ProvenanceSHA = digest(ev.Provenance)
		out.CatalogSHA = r.session.store.in.pin
	}
	return out, e
}
func (r *InstalledInspectionRole) head() string { return digest(r.session.previous) }

// Pre-fence structural validation reads the fixed bootstrap pins only. It does
// not run TUF admission, initialize checkpoints or authorize installed actions.
func ValidateProvisionedInspectionInputs(root, pin string) error {
	if _, e := inspectionInstallationRoot(root); e != nil {
		return e
	}
	for _, role := range []string{"owner", "signer"} {
		if _, e := openInspectionInputs(root+"/fixture-inspection-v1/inputs", pin, role, 0); e != nil {
			return e
		}
	}
	return nil
}
func FixtureInspectionNamespaceEmpty(root, role string) (bool, error) {
	if _, e := inspectionInstallationRoot(root); e != nil {
		return false, e
	}
	if role != "owner" && role != "signer" {
		return false, errors.New("fixed role required")
	}
	parent := root + "/fixture-inspection-v1/" + role
	if e := inspectionTrustedParents(parent); e != nil {
		return false, e
	}
	for _, name := range []string{"state", "anchor"} {
		r, e := inspectionRoot(parent+"/"+name, os.Getuid(), 0700)
		if e != nil {
			return false, e
		}
		n, e := inspectionNames(r)
		r.Close()
		if e != nil || len(n) != 0 {
			return false, e
		}
	}
	return true, nil
}
