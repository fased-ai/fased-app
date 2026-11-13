//go:build linux

package enrollment

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

type runtimeHost struct {
	system *custodyinstall.System
	parent *os.File
	path   string
	base   string
	state  string
}

func realNode(path string) (node, error) {
	var n node
	i, err := os.Lstat(path)
	if err != nil {
		return n, err
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return n, errState
	}
	return node{uint64(st.Dev), st.Ino, int(st.Uid), int(st.Gid), uint32(i.Mode().Perm())}, nil
}

func (h *runtimeHost) pinned(name string) string {
	return fmt.Sprintf("/proc/%d/fd/%d/%s", os.Getpid(), h.parent.Fd(), name)
}

func (h *runtimeHost) unchangedParent() error {
	i, err := h.parent.Stat()
	if err != nil {
		return errState
	}
	now, err := os.Lstat(h.path)
	if err != nil || !os.SameFile(i, now) || now.Mode()&os.ModeSymlink != 0 {
		return errors.New("source parent changed; no privileged mutation performed")
	}
	return nil
}

func openRuntime(s Source, system *custodyinstall.System) (*runtimeHost, io.Closer, error) {
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || system == nil || !sourceValid(record(s)) {
		return nil, nil, errState
	}
	parent := filepath.Dir(s.Data)
	real, err := filepath.EvalSymlinks(parent)
	if err != nil || real != parent {
		return nil, nil, errState
	}
	fd, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, errState
	}
	f := os.NewFile(uintptr(fd), parent)
	config, err := os.UserConfigDir()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	dir := filepath.Join(config, "fased-app-enrollment")
	if err = core.EnsurePrivateDirectory(dir); err != nil {
		f.Close()
		return nil, nil, err
	}
	lock, err := core.LockInstance(filepath.Join(dir, "controller.lock"))
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return &runtimeHost{system, f, parent, filepath.Base(s.Data), filepath.Join(dir, "state.json")}, lock, nil
}

func pendingOwnerJournal(entries []os.DirEntry) (string, error) {
	name := ""
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			if name != "" {
				return "", errState
			}
			name = entry.Name()
		}
	}
	return name, nil
}

func readOwnerJournal(path string) (journal, error) {
	var j journal
	dirFD, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return j, err
	}
	dir := os.NewFile(uintptr(dirFD), "owner-journal")
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return j, err
	}
	name, err := pendingOwnerJournal(entries)
	if err != nil {
		return j, err
	}
	j, err = readOwnerJournalEntry(dirFD, filepath.Base(path), os.Getuid(), false)
	if err != nil {
		if name != "" {
			return journal{}, errState
		}
		return journal{}, err
	}
	if err = verifyRetainedPrepared(dirFD, name, j, os.Getuid()); err != nil {
		return journal{}, err
	}
	return j, nil
}

func readOwnerJournalEntry(dirFD int, name string, owner int, pending bool) (journal, error) {
	var j journal
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return j, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || (st.Mode&07777 != 0600 && !(pending && st.Mode&07777 == 0400)) || int(st.Uid) != owner || st.Nlink != 1 || st.Size <= 0 || st.Size > 16384 {
		return j, errState
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 || update.StrictJSON(b, &j) != nil {
		return journal{}, errState
	}
	return j, nil
}

// The original first prepared write remains as a private recovery witness.
// Any other pending file, or any changed operation identity, stays held.
func verifyRetainedPrepared(dirFD int, name string, current journal, owner int) error {
	if name == "" {
		return nil
	}
	prepared, err := readOwnerJournalEntry(dirFD, name, owner, true)
	checked := current
	if checked.Phase == "source-lock-lost" {
		checked.Phase = "fencing"
	}
	if err != nil || !prepared.valid(owner) || prepared.Phase != "prepared" || prepared.Target != (custodyinstall.Manifest{}) || prepared.Digest != "" || prepared.CoreRecovery != nil || !checked.valid(owner) ||
		prepared.ID != current.ID || prepared.Owner != current.Owner || prepared.Source != current.Source || prepared.Node != current.Node || prepared.CoreHash != current.CoreHash || prepared.SignerHash != current.SignerHash ||
		prepared.Network != current.Network || prepared.Mode != current.Mode || prepared.Profile != current.Profile || prepared.Provenance != current.Provenance || prepared.NativeOwnerAccess != current.NativeOwnerAccess {
		return errState
	}
	return nil
}

// A successful enrollment transition must survive a power loss after rename.
func writeOwnerJournal(path string, j journal) error {
	if err := core.WritePrivateJSON(path, j); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (h *runtimeHost) image(ctx context.Context, m custodyinstall.Manifest, action, path, id, digest string, in io.Reader) error {
	out := &limitedOutput{remaining: 4096}
	if err := h.system.EnrollmentImage(ctx, m, action, path, id, digest, in, out); err != nil {
		return err
	}
	return decodeSmall(out.Bytes(), id, digest, action == "empty")
}

type limitedOutput struct {
	bytes.Buffer
	remaining int
}

func (w *limitedOutput) Write(b []byte) (int, error) {
	if len(b) > w.remaining {
		return 0, errState
	}
	w.remaining -= len(b)
	return w.Buffer.Write(b)
}

// A privileged caller may compare only with the digest of a stage already
// verified by the unprivileged app UID. Ordinary callers validate directly.
func validateExportedFrame(image core.EnrollmentImage, instance, verifiedStageDigest string, privileged bool) error {
	if image.InstanceID != instance {
		return errState
	}
	if privileged {
		if len(verifiedStageDigest) != 64 || core.EnrollmentImageDigest(image) != verifiedStageDigest {
			return errState
		}
		return nil
	}
	if verifiedStageDigest != "" || core.ValidateEnrollmentImage(image) != nil {
		return errState
	}
	return nil
}

func (h *runtimeHost) export(ctx context.Context, m custodyinstall.Manifest, s sourceRecord, verifiedStageDigest string) (*imageLease, error) {
	if err := h.unchangedParent(); err != nil {
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	// Bound acquisition, but never expire a successfully held lock during import.
	acquisition := time.AfterFunc(2*time.Minute, cancel)
	defer acquisition.Stop()
	p, err := h.system.EnrollmentExport(child, m, s.UID, s.GID, s.Data, s.ExpectedInstanceID)
	if err != nil {
		cancel()
		return nil, err
	}
	close := func() error { cancel(); return p.Close() }
	reader := bufio.NewReader(p.Output)
	raw, err := readEnrollmentLine(reader, core.MaxEnrollmentEncodedBytes)
	image, frameErr := decodeEnrollmentFrame(raw)
	if err != nil || frameErr != nil {
		_ = close()
		return nil, fmt.Errorf("enrollment source helper frame: %w", errState)
	}
	if validateExportedFrame(image, s.ExpectedInstanceID, verifiedStageDigest, os.Getuid() == 0 && os.Geteuid() == 0) != nil {
		_ = close()
		return nil, fmt.Errorf("enrollment source payload validation: %w", errState)
	}
	acquisition.Stop()
	digest := core.EnrollmentImageDigest(image)
	check := func() error {
		if err := child.Err(); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(p.Input, "CHECK "+digest); err != nil {
			return err
		}
		line, err := readEnrollmentLine(reader, 128)
		if err != nil || string(line) != "LOCKED "+digest+"\n" {
			return errors.New("source lock helper exited; preserve both copies")
		}
		return nil
	}
	return &imageLease{image: image, close: close, check: check, release: func() error { defer cancel(); return releaseImage(p, digest) }}, nil
}

func (h *runtimeHost) ensureRootDirectory(ctx context.Context, path string, mode uint32) error {
	n, err := realNode(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, err = h.system.Run(ctx, nil, "/usr/bin/mkdir", "--mode="+strconv.FormatUint(uint64(mode), 8), "--", path); err != nil {
			return err
		}
		n, err = realNode(path)
	}
	if err != nil || n.UID != 0 || n.GID != 0 || n.Mode != mode {
		return errors.New("foreign enrollment fence; preserving it")
	}
	return nil
}

func jsonBytes(v any) []byte { b, _ := json.Marshal(v); return b }

func readPublicFile(path string) ([]byte, error) { return readPublicLinks(path, 1) }

func readPublicLinks(path string, maxLinks uint64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Mode&07777 != 0644 || st.Nlink < 1 || uint64(st.Nlink) > maxLinks || st.Size < 0 || st.Size > 16384 {
		return nil, errState
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return nil, errState
	}
	return b, nil
}

func classifyFence(path string, j journal, published bool) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errState
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	entries, err := f.ReadDir(6)
	if err != nil && !errors.Is(err, io.EOF) || len(entries) > 5 {
		return errState
	}
	marker := false
	original := false
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case name == ".original":
			n, e := realNode(path + "/.original")
			if e != nil || n.UID != 0 || n.GID != 0 || n.Mode != 0700 {
				return errState
			}
			original = true
		case name == "enrollment.json", name == ".enrollment.json-"+j.ID+".pending":
			b, e := readPublicLinks(path+"/"+name, 2)
			expected := jsonBytes(fenceFor(j))
			if e != nil || !bytes.HasPrefix(expected, b) || name == "enrollment.json" && !bytes.Equal(expected, b) {
				return errState
			}
			if name == "enrollment.json" {
				marker = true
			}
		case published && (name == "ready.json" || name == ".ready.json-"+j.ID+".pending"):
			b, e := readPublicLinks(path+"/"+name, 2)
			expected := jsonBytes(map[string]any{"version": 1, "binding": hash(jsonBytes(fenceFor(j)))})
			if e != nil || !bytes.HasPrefix(expected, b) || name == "ready.json" && !bytes.Equal(expected, b) {
				return errState
			}
		default:
			return errState
		}
	}
	if original && !marker {
		return errState
	}
	return nil
}

func (h *runtimeHost) archiveNode(ctx context.Context, path string) (node, error) {
	b, err := h.system.Run(ctx, nil, "/usr/bin/stat", "--printf", "%d %i %u %g %f", "--", path)
	if err != nil {
		return node{}, err
	}
	parts := strings.Fields(string(b))
	if len(parts) != 5 {
		return node{}, errState
	}
	dev, e1 := strconv.ParseUint(parts[0], 10, 64)
	ino, e2 := strconv.ParseUint(parts[1], 10, 64)
	uid, e3 := strconv.Atoi(parts[2])
	gid, e4 := strconv.Atoi(parts[3])
	mode, e5 := strconv.ParseUint(parts[4], 16, 32)
	if errors.Join(e1, e2, e3, e4, e5) != nil || uint32(mode)&unix.S_IFMT != unix.S_IFDIR {
		return node{}, errState
	}
	return node{dev, ino, uid, gid, uint32(mode) & 07777}, nil
}

func (h *runtimeHost) checkFence(ctx context.Context, j journal) error {
	if j.Target.Version == 3 && os.Getuid() != 0 {
		return h.protectedCutover(ctx, j, "check")
	}
	if err := h.unchangedParent(); err != nil {
		return err
	}
	p := h.pinned(h.base)
	n, err := realNode(p)
	if err != nil || n.UID != 0 || n.GID != 0 || n.Mode != 0755 {
		return errState
	}
	if err = classifyFence(p, j, true); err != nil {
		return err
	}
	b, err := readPublicFile(p + "/enrollment.json")
	if err != nil || !bytes.Equal(bytes.TrimSpace(b), jsonBytes(fenceFor(j))) {
		return errState
	}
	archived, err := h.archiveNode(ctx, p+"/.original/data")
	if err != nil || archived != j.Node {
		return errors.New("archived source inode differs; activation remains disabled")
	}
	return nil
}

func (h *runtimeHost) fence(ctx context.Context, j journal) error {
	if j.Target.Version == 3 && os.Getuid() != 0 {
		return h.protectedCutover(ctx, j, "fence")
	}
	if err := h.unchangedParent(); err != nil {
		return err
	}
	if n, err := realNode(h.pinned(h.base)); err == nil && n.UID == 0 {
		if err = classifyFence(h.pinned(h.base), j, true); err != nil {
			return err
		}
		b, err := readPublicLinks(h.pinned(h.base)+"/enrollment.json", 2)
		if err != nil || !bytes.Equal(bytes.TrimSpace(b), jsonBytes(fenceFor(j))) {
			return errState
		}
		if err = h.system.EnrollmentRecord(ctx, h.pinned(h.base)+"/enrollment.json", j.ID, jsonBytes(fenceFor(j))); err != nil {
			return err
		}
		return h.checkFence(ctx, j)
	}
	pending := h.pinned(".enrollment-" + j.ID)
	if err := h.ensureRootDirectory(ctx, pending, 0755); err != nil {
		return err
	}
	if err := classifyFence(pending, j, false); err != nil {
		return err
	}
	if err := h.system.EnrollmentRecord(ctx, pending+"/enrollment.json", j.ID, jsonBytes(fenceFor(j))); err != nil {
		return err
	}
	if err := h.ensureRootDirectory(ctx, pending+"/.original", 0700); err != nil {
		return err
	}
	if n, err := realNode(h.pinned(h.base)); err == nil {
		if n != j.Node {
			return errors.New("source inode changed before cutover")
		}
		if _, err = h.system.Run(ctx, nil, "/usr/bin/mv", "--no-clobber", "--no-target-directory", "--", h.pinned(h.base), pending+"/.original/data"); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	archived, err := h.archiveNode(ctx, pending+"/.original/data")
	if err != nil || archived != j.Node {
		return errors.New("source archive identity mismatch; preserve it")
	}
	if _, err = h.system.Run(ctx, nil, "/usr/bin/sync", "-f", pending+"/.original"); err != nil {
		return err
	}
	if _, err = h.system.Run(ctx, nil, "/usr/bin/mv", "--no-clobber", "--no-target-directory", "--", pending, h.pinned(h.base)); err != nil {
		return err
	}
	if err = h.checkFence(ctx, j); err != nil {
		return err
	}
	_, err = h.system.Run(ctx, nil, "/usr/bin/sync", "-f", h.pinned("."))
	return err
}

func (h *runtimeHost) importImage(ctx context.Context, j journal, image core.EnrollmentImage) error {
	stage := stagePath(j)
	n, err := realNode(stage)
	if errors.Is(err, os.ErrNotExist) {
		if j.Target.Version == 3 {
			if err = h.system.PrepareEnrollmentStage(ctx, j.Target, j.ID); err != nil {
				return err
			}
		} else {
			if _, err = h.system.Run(ctx, nil, "/usr/bin/install", "-d", "-m", "0700", "-o", strconv.Itoa(j.Target.App.UID), "-g", strconv.Itoa(j.Target.App.GID), "--", stage); err != nil {
				return err
			}
		}
		n, err = realNode(stage)
	}
	if err != nil || n.UID != j.Target.App.UID || n.GID != j.Target.App.GID || n.Mode != 0700 {
		return errState
	}
	return h.image(ctx, j.Target, "import", stage, j.Source.ExpectedInstanceID, j.Digest, imageBytes(image))
}

func (h *runtimeHost) publish(ctx context.Context, j journal) error {
	if j.Target.Version == 3 && os.Getuid() != 0 {
		return h.protectedCutover(ctx, j, "publish")
	}
	if err := h.checkFence(ctx, j); err != nil {
		return err
	}
	target, stage := j.Target.Root+"/app-state", stagePath(j)
	if _, err := os.Lstat(stage); errors.Is(err, os.ErrNotExist) {
		return h.image(ctx, j.Target, "verify", target, j.Source.ExpectedInstanceID, j.Digest, nil)
	} else if err != nil {
		return err
	}
	if err := h.image(ctx, j.Target, "verify", stage, j.Source.ExpectedInstanceID, j.Digest, nil); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		if err = h.image(ctx, j.Target, "empty", target, "", "", nil); err != nil {
			return err
		}
		from, se := realNode(stage)
		to, te := realNode(target)
		if se != nil || te != nil || from.Device != to.Device || to.UID != j.Target.App.UID || to.GID != j.Target.App.GID || to.Mode != 0700 {
			return errState
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Same-filesystem rename atomically replaces the verified empty app-state
	// directory. A nonempty destination refuses rather than merging/copying it;
	// the active pathname is never absent and never exposes a partial database.
	if _, err := h.system.Run(ctx, nil, "/usr/bin/mv", "--no-target-directory", "--", stage, target); err != nil {
		return err
	}
	if err := h.image(ctx, j.Target, "verify", target, j.Source.ExpectedInstanceID, j.Digest, nil); err != nil {
		return err
	}
	_, err := h.system.Run(ctx, nil, "/usr/bin/sync", "-f", j.Target.Root)
	return err
}

func (h *runtimeHost) finish(ctx context.Context, j journal) error {
	if j.Target.Version == 3 && os.Getuid() != 0 {
		return h.protectedCutover(ctx, j, "finish")
	}
	if err := h.checkFence(ctx, j); err != nil {
		return err
	}
	c, err := custodyinstall.ReadEnrollmentCommit(j.Target)
	if err != nil || c.InstanceID != j.Source.ExpectedInstanceID || c.Digest != j.Digest {
		return errState
	}
	return h.system.EnrollmentRecord(ctx, h.pinned(h.base)+"/ready.json", j.ID, jsonBytes(map[string]any{"version": 1, "binding": hash(jsonBytes(fenceFor(j)))}))
}

func (h *runtimeHost) protectedCutover(ctx context.Context, j journal, step string) error {
	if err := h.unchangedParent(); err != nil {
		return err
	}
	if step == "fence" {
		q := maintenance.CustodyEnrollmentRequest{OperationID: j.ID, Step: "reserve"}
		if _, err := maintenance.Call(ctx, maintenance.Request{Version: 1, Action: "custody-enrollment-cutover", Enrollment: &q}, h.system.Out); err != nil {
			return err
		}
		// Local's parent belongs to the owner. Create this directory without
		// privilege; the helper binds its inode before taking ownership.
		if j.Source.Profile == "local" {
			if n, err := realNode(h.pinned(h.base)); err == nil && n == j.Node {
				if err = unix.Mkdirat(int(h.parent.Fd()), ".enrollment-"+j.ID, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
					return err
				}
			}
		}
	}
	q := maintenance.CustodyEnrollmentRequest{OperationID: j.ID, Step: step}
	_, err := maintenance.Call(ctx, maintenance.Request{Version: 1, Action: "custody-enrollment-cutover", Enrollment: &q}, h.system.Out)
	return err
}

func Run(ctx context.Context, s Source, o Options, system *custodyinstall.System) (custodyinstall.Manifest, error) {
	return runInstalled(ctx, s, o, system, false)
}

// RunControlledOwnerCustody is selected by compiled disposable acceptance only.
func RunControlledOwnerCustody(ctx context.Context, s Source, o Options, system *custodyinstall.System) (custodyinstall.Manifest, error) {
	if o.Mode != signer.OrdinaryCustodyMode || o.Profile != signer.OwnerCustodyProfile || o.Provenance != signer.ControlledCustodyProfile {
		return custodyinstall.Manifest{}, signer.ErrUnavailable
	}
	return runInstalled(ctx, s, o, system, true)
}
func runInstalled(ctx context.Context, s Source, o Options, system *custodyinstall.System, controlled bool) (custodyinstall.Manifest, error) {
	var out custodyinstall.Manifest
	if o.Mode == signer.OrdinaryCustodyMode && !controlled && (!signer.OrdinaryOwnerAdmissionEnabled() || o.Provenance != signer.OwnerKeyProvenance) {
		return out, signer.ErrUnavailable
	}
	if !o.Disposable || !o.PrepareHost {
		return out, errors.New("explicit disposable host consent required")
	}
	h, lock, err := openRuntime(s, system)
	if err != nil {
		return out, err
	}
	defer h.parent.Close()
	defer lock.Close()
	coreHash, err := boundedBinary(o.Core)
	if err != nil {
		return out, err
	}
	signerHash, err := boundedBinary(o.Signer)
	if err != nil {
		return out, err
	}
	if err = recoverPreparedOwnerJournal(ctx, h, s, o, coreHash, signerHash, func() error {
		return verifyFreshPreparedAuthority(ctx, s)
	}); err != nil {
		return out, err
	}
	op := operations{
		archiveRecovery: func(j journal) error {
			if err := h.unchangedParent(); err != nil {
				return err
			}
			path := filepath.Join(filepath.Dir(h.state), "before-core-recovery-"+j.ID+".json")
			if raw, err := core.ReadPrivateFile(path); err == nil {
				var saved journal
				if update.StrictJSON(raw, &saved) != nil || saved != j {
					return errState
				}
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return core.WritePrivateJSON(path, j)
		},
		recoverCore: func(ctx context.Context, j journal, o Options, next string) (custodyinstall.Manifest, error) {
			return system.RecoverEnrollmentCore(ctx, j.CoreRecovery.Previous, j.ID, o.Core, next)
		},
		owner: os.Getuid(), load: func() (journal, error) { return readOwnerJournal(h.state) },
		save: func(j journal) error { return writeOwnerJournal(h.state, j) }, node: realNode,
		targetExists: func() bool { _, e := os.Lstat(custodyinstall.Root(os.Getuid())); return !errors.Is(e, os.ErrNotExist) },
		prepare: func(ctx context.Context, j journal, o Options) (custodyinstall.Manifest, error) {
			if o.Mode == signer.OrdinaryCustodyMode {
				q := maintenance.CustodyEnrollmentRequest{OperationID: j.ID, Network: o.Network, Listen: j.Source.Listen, PublicOrigin: j.Source.PublicOrigin, NativeOwnerAccess: o.NativeOwnerAccess, CoreSHA256: j.CoreHash, SignerSHA256: j.SignerHash, Sequence: j.Source.ApplicationSequence, ReleaseSHA256: j.Source.ApplicationReleaseSHA256}
				b, err := maintenance.Call(ctx, maintenance.Request{Version: 1, Action: "custody-enrollment-prepare", Enrollment: &q}, system.Out)
				if err != nil {
					return custodyinstall.Manifest{}, err
				}
				var reply maintenance.Response
				var target custodyinstall.Manifest
				if update.StrictJSON(b, &reply) != nil || update.StrictJSON(reply.Payload, &target) != nil ||
					target.ID != j.ID || target.Owner != os.Getuid() || target.CoreHash != j.CoreHash || target.SignerHash != j.SignerHash ||
					target.Mode != o.Mode || target.Profile != o.Profile || target.Provenance != o.Provenance || target.Network != o.Network ||
					target.Location != j.Source.Profile || target.Listen != j.Source.Listen || target.PublicOrigin != j.Source.PublicOrigin ||
					target.NativeOwnerAccess != o.NativeOwnerAccess || custodyinstall.Verify(target) != nil {
					return custodyinstall.Manifest{}, errors.New("protected enrollment target differs from the source")
				}
				if err = custodyinstall.RecordConfinedEnrollmentIntent(target); err != nil {
					return custodyinstall.Manifest{}, err
				}
				return target, nil
			}
			options := custodyinstall.Options{NativeOwnerAccess: o.NativeOwnerAccess, Mode: o.Mode, Profile: o.Profile, Provenance: o.Provenance, Core: o.Core, Signer: o.Signer, Network: o.Network, Listen: j.Source.Listen, Location: j.Source.Profile, PublicOrigin: j.Source.PublicOrigin, PrepareHost: true, Disposable: true, StageOnly: true}
			if controlled {
				return custodyinstall.InstallControlledOwnerCustody(ctx, options, system)
			}
			return custodyinstall.Install(ctx, options, system)
		},
		target: func(m custodyinstall.Manifest) error {
			current, err := custodyinstall.Load(os.Getuid())
			if err != nil || current != m {
				return errState
			}
			return custodyinstall.Verify(m)
		},
		emptySigner: system.EnrollmentSignerEmpty, export: func(ctx context.Context, m custodyinstall.Manifest, s sourceRecord) (*imageLease, error) {
			return h.export(ctx, m, s, "")
		}, importImage: h.importImage,
		fence: h.fence, protectedFence: func(ctx context.Context, j journal) error { return h.protectedCutover(ctx, j, "fence") }, publish: h.publish,
		verifyTarget: func(ctx context.Context, j journal) error {
			if err := h.checkFence(ctx, j); err != nil {
				return err
			}
			return h.image(ctx, j.Target, "verify", j.Target.Root+"/app-state", j.Source.ExpectedInstanceID, j.Digest, nil)
		},
		activate: func(ctx context.Context, j journal) error {
			if err := h.checkFence(ctx, j); err != nil {
				return err
			}
			if j.Target.Version == 3 {
				return h.protectedActivation(ctx, j, "activate")
			}
			return custodyinstall.ActivateEnrollment(ctx, j.Target, j.Source.ExpectedInstanceID, j.Digest, j.Source.Running, j.Source.Autostart, system)
		}, restart: func(ctx context.Context, j journal) error {
			if err := h.checkFence(ctx, j); err != nil {
				return err
			}
			commit, err := custodyinstall.ReadEnrollmentCommit(j.Target)
			if err != nil || commit.InstanceID != j.Source.ExpectedInstanceID || commit.Digest != j.Digest {
				return errState
			}
			if j.Target.Version == 3 {
				return h.protectedActivation(ctx, j, "restart")
			}
			// The explicit native resume starts existing roles; it neither reimports
			// the original image nor enables/disables login/reboot autostart.
			return custodyinstall.ActivateEnrollment(ctx, j.Target, j.Source.ExpectedInstanceID, j.Digest, true, false, system)
		}, finish: h.finish,
	}
	return run(ctx, s, o, coreHash, signerHash, op)
}

func (h *runtimeHost) protectedActivation(ctx context.Context, j journal, step string) error {
	if err := h.unchangedParent(); err != nil {
		return err
	}
	q := maintenance.CustodyEnrollmentRequest{OperationID: j.ID, Step: step}
	_, err := maintenance.Call(ctx, maintenance.Request{Version: 1, Action: "custody-enrollment-activation", Enrollment: &q}, h.system.Out)
	return err
}

// EnrolledTarget reads only root-owned public fence/commit bindings. It grants
// no session, credential, wallet or service-control authority.
func EnrolledTarget(data string, ownerUID int) (custodyinstall.Manifest, error) {
	var zero custodyinstall.Manifest
	if ownerUID <= 0 || !filepath.IsAbs(data) || filepath.Clean(data) != data {
		return zero, errState
	}
	real, err := filepath.EvalSymlinks(data)
	if err != nil {
		return zero, err
	}
	if real != data {
		return zero, errState
	}
	n, err := realNode(data)
	if err != nil {
		return zero, err
	}
	if n.UID != 0 {
		return zero, os.ErrNotExist
	}
	if n.GID != 0 || n.Mode != 0755 {
		return zero, errState
	}
	b, err := readPublicFile(data + "/enrollment.json")
	if err != nil {
		return zero, errState
	}
	var f fence
	if update.StrictJSON(b, &f) != nil || f.Version != 1 || f.Owner != ownerUID || f.Source.Data != data || !sourceValid(f.Source) || !hexID(f.ID, 16) || !hexID(f.Digest, 32) || !f.Target.Enrollment {
		return zero, errState
	}
	m, err := custodyinstall.Load(ownerUID)
	if err != nil || custodyinstall.EnrollmentIdentity(m) != custodyinstall.EnrollmentIdentity(f.Target) || custodyinstall.Verify(m) != nil {
		return zero, errState
	}
	c, err := custodyinstall.ReadEnrollmentCommit(m)
	if err != nil || c.InstanceID != f.Source.ExpectedInstanceID || c.Digest != f.Digest {
		return zero, errState
	}
	b, err = readPublicFile(data + "/ready.json")
	var ready struct {
		Version int    `json:"version"`
		Binding string `json:"binding"`
	}
	if err != nil || update.StrictJSON(b, &ready) != nil || ready.Version != 1 || ready.Binding != hash(jsonBytes(f)) {
		return zero, errState
	}
	return m, nil
}

// Read a bounded protocol frame without allowing a non-terminating frame to grow.
func readEnrollmentLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > limit {
			return nil, errState
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}

func decodeEnrollmentFrame(raw []byte) (core.EnrollmentImage, error) {
	return core.DecodeEnrollmentImage(raw)
}
