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

func readOwnerJournal(path string) (journal, error) {
	var j journal
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return j, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0600 || int(st.Uid) != os.Getuid() || st.Nlink != 1 || st.Size <= 0 || st.Size > 16384 {
		return j, errState
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 || update.StrictJSON(b, &j) != nil {
		return journal{}, errState
	}
	return j, nil
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

func (h *runtimeHost) export(ctx context.Context, m custodyinstall.Manifest, s sourceRecord) (*imageLease, error) {
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
	if core.ValidateEnrollmentImage(image) != nil || image.InstanceID != s.ExpectedInstanceID {
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
		if _, err = h.system.Run(ctx, nil, "/usr/bin/install", "-d", "-m", "0700", "-o", strconv.Itoa(j.Target.App.UID), "-g", strconv.Itoa(j.Target.App.GID), "--", stage); err != nil {
			return err
		}
		n, err = realNode(stage)
	}
	if err != nil || n.UID != j.Target.App.UID || n.GID != j.Target.App.GID || n.Mode != 0700 {
		return errState
	}
	return h.image(ctx, j.Target, "import", stage, j.Source.ExpectedInstanceID, j.Digest, imageBytes(image))
}

func (h *runtimeHost) publish(ctx context.Context, j journal) error {
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
	if err := h.checkFence(ctx, j); err != nil {
		return err
	}
	c, err := custodyinstall.ReadEnrollmentCommit(j.Target)
	if err != nil || c.InstanceID != j.Source.ExpectedInstanceID || c.Digest != j.Digest {
		return errState
	}
	return h.system.EnrollmentRecord(ctx, h.pinned(h.base)+"/ready.json", j.ID, jsonBytes(map[string]any{"version": 1, "binding": hash(jsonBytes(fenceFor(j)))}))
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
	op := operations{
		owner: os.Getuid(), load: func() (journal, error) { return readOwnerJournal(h.state) },
		save: func(j journal) error { return core.WritePrivateJSON(h.state, j) }, node: realNode,
		targetExists: func() bool { _, e := os.Lstat(custodyinstall.Root(os.Getuid())); return !errors.Is(e, os.ErrNotExist) },
		prepare: func(ctx context.Context, j journal, o Options) (custodyinstall.Manifest, error) {
			options := custodyinstall.Options{Mode: o.Mode, Profile: o.Profile, Provenance: o.Provenance, Core: o.Core, Signer: o.Signer, Network: o.Network, Listen: j.Source.Listen, Location: j.Source.Profile, PublicOrigin: j.Source.PublicOrigin, PrepareHost: true, Disposable: true, StageOnly: true}
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
		emptySigner: system.EnrollmentSignerEmpty, export: h.export, importImage: h.importImage,
		fence: h.fence, publish: h.publish,
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
			return custodyinstall.ActivateEnrollment(ctx, j.Target, j.Source.ExpectedInstanceID, j.Digest, j.Source.Running, j.Source.Autostart, system)
		}, finish: h.finish,
	}
	return run(ctx, s, o, coreHash, signerHash, op)
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
