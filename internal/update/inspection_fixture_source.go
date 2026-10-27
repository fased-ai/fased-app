package update

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const inspectionProfile = "fased-app-installed-inspection-fixture:v1"
const inspectionCapability = "fixture-installed-inspection-v1"
const inspectionMaxFiles = 64
const inspectionMaxBytes = 1 << 20

var inspectionMetadataName = regexp.MustCompile(`^[1-9][0-9]{0,9}\.(root|snapshot|targets)\.json$`)

type inspectionFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	SHA  string `json:"sha256"`
}
type inspectionIndex struct {
	Version     int              `json:"version"`
	Profile     string           `json:"profile"`
	FixtureOnly bool             `json:"fixtureOnly"`
	Files       []inspectionFile `json:"files"`
}

// All bytes are copied from a complete, pinned, closed file set. There is no
// fetch callback, network fallback, decompressor, executable or mutable cache.
type inspectionOfflineSource struct {
	files  map[string][]byte
	digest string
}

func inspectionNameLimit(n string) int {
	switch n {
	case "timestamp.json":
		return maxTrustMetadata
	case "evidence.json":
		return MaxDocument
	case "core.fixture", "signer.fixture", "owner-helper.fixture", "installer.fixture":
		return maxInertArtifact
	}
	if inspectionMetadataName.MatchString(n) {
		return maxTrustMetadata
	}
	if len(n) == 80 && hashOK(strings.TrimSuffix(n, ".descriptor.json")) {
		return 1024
	}
	return 0
}
func inspectionRead(r *os.Root, name string, uid int, mode os.FileMode, limit int) ([]byte, error) {
	st, e := r.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !inspectionFileIdentity(st, uid, false, mode) || st.Size() < 1 || st.Size() > int64(limit) {
		return nil, errors.New("bounded, owned, single-link fixture file required")
	}
	f, e := inspectionOpen(r, name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	after, e := f.Stat()
	if e != nil || !inspectionFileIdentity(after, uid, false, mode) || !os.SameFile(st, after) {
		return nil, errors.New("fixture identity changed")
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil || len(b) > limit || int64(len(b)) != after.Size() {
		return nil, errors.New("incomplete fixture file")
	}
	return b, nil
}
func inspectionRoot(dir string, uid int, mode os.FileMode) (*os.Root, error) {
	real, e := filepath.EvalSymlinks(dir)
	if e != nil || !filepath.IsAbs(dir) || real != dir || filepath.Clean(dir) != dir {
		return nil, errors.New("exact real fixture root required")
	}
	st, e := os.Lstat(dir)
	if e != nil || !inspectionFileIdentity(st, uid, true, mode) {
		return nil, errors.New("owned fixture root required")
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	a, e := r.Stat(".")
	if e != nil || !os.SameFile(st, a) {
		r.Close()
		return nil, errors.New("fixture root changed")
	}
	return r, nil
}
func inspectionNames(r *os.Root) ([]string, error) {
	f, e := r.Open(".")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	names, e := f.Readdirnames(257)
	if e != nil && e != io.EOF {
		return nil, e
	}
	if len(names) > 256 {
		return nil, errors.New("fixture file count exceeded")
	}
	sort.Strings(names)
	return names, nil
}
func openInspectionSource(dir, indexSHA, origin, fetchPin string, uid int) (*inspectionOfflineSource, error) {
	if !hashOK(indexSHA) || !hashOK(fetchPin) {
		return nil, errors.New("independent index and fetch policy pins required")
	}
	u, e := acquisitionURL(origin)
	if e != nil || u.Path != "" {
		return nil, errors.New("seeded HTTPS origin required")
	}
	r, e := inspectionRoot(dir, uid, 0755)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := inspectionRead(r, "index.json", uid, 0644, MaxDocument)
	if e != nil || digest(b) != indexSHA {
		return nil, errors.New("offline index pin mismatch")
	}
	var index inspectionIndex
	if StrictJSON(b, &index) != nil || index.Version != 1 || index.Profile != inspectionProfile || !index.FixtureOnly || len(index.Files) < 9 || len(index.Files)+1 > inspectionMaxFiles || !bytes.Equal(b, mustInspectionJSON(index)) {
		return nil, errors.New("canonical bounded fixture index required")
	}
	s := &inspectionOfflineSource{files: map[string][]byte{}, digest: digest([]byte("fased-app-offline-inspection-source:v1\x00" + origin + "\x00" + fetchPin + "\x00" + indexSHA))}
	total, last, roots := len(b), "", 0
	for _, f := range index.Files {
		limit := inspectionNameLimit(f.Name)
		if limit == 0 || f.Name <= last || f.Size < 1 || f.Size > int64(limit) || !hashOK(f.SHA) {
			return nil, errors.New("invalid fixed source entry")
		}
		last = f.Name
		if strings.HasSuffix(f.Name, ".root.json") {
			roots++
		}
		if roots > 32 {
			return nil, errors.New("root successor bound exceeded")
		}
		contents, e := inspectionRead(r, f.Name, uid, 0644, limit)
		if e != nil || int64(len(contents)) != f.Size || digest(contents) != f.SHA {
			return nil, errors.New("source file integrity mismatch")
		}
		total += len(contents)
		if total > inspectionMaxBytes {
			return nil, errors.New("offline bundle byte bound exceeded")
		}
		s.files[f.Name] = contents
	}
	counts := map[string]int{}
	for n := range s.files {
		for _, suffix := range []string{".snapshot.json", ".targets.json", ".descriptor.json"} {
			if strings.HasSuffix(n, suffix) {
				counts[suffix]++
			}
		}
	}
	for _, suffix := range []string{".snapshot.json", ".targets.json", ".descriptor.json"} {
		if counts[suffix] != 1 {
			return nil, errors.New("one complete active metadata set required")
		}
	}
	names, e := inspectionNames(r)
	if e != nil || len(names) != len(s.files)+1 {
		return nil, errors.New("offline directory/index mismatch")
	}
	for _, n := range names {
		if n != "index.json" && s.files[n] == nil {
			return nil, errors.New("foreign offline file")
		}
	}
	for _, n := range []string{"timestamp.json", "evidence.json", "core.fixture", "signer.fixture", "owner-helper.fixture", "installer.fixture"} {
		if s.files[n] == nil {
			return nil, errors.New("incomplete offline bundle")
		}
	}
	return s, nil
}
func (s *inspectionOfflineSource) get(name string, optional bool, limit int64) ([]byte, error) {
	if strings.HasPrefix(name, "targets/fixture/") {
		name = strings.TrimPrefix(name, "targets/fixture/")
	}
	b, ok := s.files[name]
	if !ok {
		if optional && strings.HasSuffix(name, ".root.json") && inspectionMetadataName.MatchString(name) {
			return nil, nil
		}
		return nil, fmt.Errorf("missing offline metadata %s", name)
	}
	if inspectionNameLimit(name) == 0 || int64(len(b)) > limit {
		return nil, errors.New("offline metadata bound exceeded")
	}
	return append([]byte(nil), b...), nil
}
func (s *inspectionOfflineSource) inspect(subjects []FixtureReleaseSubject) error {
	for _, a := range subjects {
		b := s.files[a.File]
		if int64(len(b)) != a.Size || digest(b) != a.SHA256 || !bytes.HasPrefix(b, []byte(inertArtifactPrefix)) {
			return errors.New("offline inert subject refused")
		}
	}
	return nil
}

func (s *inspectionOfflineSource) rootRange(seedVersion int64) error {
	versions := []int64{}
	for n := range s.files {
		if strings.HasSuffix(n, ".root.json") {
			v, e := strconv.ParseInt(strings.TrimSuffix(n, ".root.json"), 10, 64)
			if e != nil {
				return e
			}
			versions = append(versions, v)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	for i, v := range versions {
		if v != seedVersion+int64(i)+1 {
			return errors.New("complete sequential offline root successors required")
		}
	}
	return nil
}
