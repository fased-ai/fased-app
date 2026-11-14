//go:build linux

package custodyinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

type stageRecoveryFile struct {
	path string
	file *os.File
	stat unix.Stat_t
}

// All root staging entries count, including unknown/legacy files. With one
// systemd unit per allowlisted slot, concurrent guards can add at most one
// directory and one final link per slot beyond this threshold. This limits
// root-created copies, not bytes another owner process can write; no
// unclassified file is removed and a separate free-space reserve is needed.
const maxReusableStageEntries = 64

func openStageRecoveryFile(path string, uid, gid int, modes []uint32, max int64) (*stageRecoveryFile, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != uid || int(st.Gid) != gid || st.Size < 0 || st.Size > max {
		f.Close()
		return nil, errors.New("foreign staging recovery file; preserving it")
	}
	permitted := false
	for _, mode := range modes {
		permitted = permitted || st.Mode&07777 == mode
	}
	if !permitted {
		f.Close()
		return nil, errors.New("foreign staging recovery permissions; preserving them")
	}
	return &stageRecoveryFile{path: path, file: f, stat: st}, nil
}

func (f *stageRecoveryFile) stable() bool {
	var fd, path unix.Stat_t
	if unix.Fstat(int(f.file.Fd()), &fd) != nil || unix.Lstat(f.path, &path) != nil {
		return false
	}
	same := func(a, b unix.Stat_t) bool {
		return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink && a.Size == b.Size && a.Ctim == b.Ctim
	}
	return same(f.stat, fd) && same(fd, path)
}

func stageRecoveryPrefix(prefix, source *stageRecoveryFile) error {
	if prefix.stat.Size > source.stat.Size {
		return errors.New("staging retry contains unrelated bytes")
	}
	a, b := make([]byte, 32*1024), make([]byte, 32*1024)
	for off := int64(0); off < prefix.stat.Size; {
		n := int64(len(a))
		if remain := prefix.stat.Size - off; remain < n {
			n = remain
		}
		if _, err := prefix.file.ReadAt(a[:n], off); err != nil {
			return err
		}
		if _, err := source.file.ReadAt(b[:n], off); err != nil {
			return err
		}
		if !bytes.Equal(a[:n], b[:n]) {
			return errors.New("staging retry differs from the original input")
		}
		off += n
	}
	return nil
}

// validateStageRecoveryAt performs no writes. Earlier attempts and legacy
// pending files are never consumed or removed by a new invocation.
func validateStageRecoveryAt(stage, inbox string, owner, signerGID, rootUID, rootGID int, kind, slot, invocation string) error {
	if _, err := reusableStageName(owner, kind, slot); err != nil {
		return err
	}
	if !update.IDOK(invocation) {
		return errors.New("exact systemd staging invocation required")
	}
	base, suffix, err := reusableStageClass(kind)
	if err != nil {
		return err
	}
	limit := int64(64 << 20)
	if base == "meta" {
		limit = 4 << 20
	} else if base == "unit" {
		limit = 32768
	}
	var dir unix.Stat_t
	if unix.Lstat(stage, &dir) != nil || dir.Mode&unix.S_IFMT != unix.S_IFDIR || int(dir.Uid) != rootUID || int(dir.Gid) != rootGID || dir.Mode&0022 != 0 {
		return errors.New("foreign maintenance stage; preserving it")
	}
	fd, err := unix.Open(stage, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("maintenance stage cannot be inspected")
	}
	stageDir := os.NewFile(uintptr(fd), stage)
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || opened.Dev != dir.Dev || opened.Ino != dir.Ino || opened.Uid != dir.Uid || opened.Gid != dir.Gid || opened.Mode != dir.Mode {
		stageDir.Close()
		return errors.New("maintenance stage changed; preserving it")
	}
	names, readErr := stageDir.Readdirnames(maxReusableStageEntries)
	closeErr := stageDir.Close()
	if (readErr != nil && !errors.Is(readErr, io.EOF)) || closeErr != nil {
		return errors.New("maintenance stage cannot be inspected")
	}
	if len(names) >= maxReusableStageEntries {
		return errors.New("retained maintenance staging is full; preserve it for recovery")
	}
	if unix.Lstat(inbox, &dir) != nil || dir.Mode&unix.S_IFMT != unix.S_IFDIR || dir.Mode&07777 != 02710 || int(dir.Uid) != owner || int(dir.Gid) != signerGID {
		return errors.New("foreign maintenance inbox; preserving it")
	}
	input, err := openStageRecoveryFile(filepath.Join(inbox, slot), owner, signerGID, []uint32{0640}, limit)
	if err != nil {
		return err
	}
	defer input.file.Close()
	if input.stat.Nlink != 1 || input.stat.Size == 0 {
		return errors.New("incomplete original staging input")
	}
	if !input.stable() {
		return errors.New("staging recovery input changed; preserving it")
	}
	for _, path := range []string{filepath.Join(stage, ".stage-"+slot+suffix+"-"+invocation+".d"), filepath.Join(stage, slot)} {
		var st unix.Stat_t
		if err := unix.Lstat(path, &st); !errors.Is(err, unix.ENOENT) {
			return errors.New("staging invocation or final already exists; preserving it")
		}
	}
	return nil
}

// VerifyReusableStageRecovery runs only as the pinned unprivileged signer and
// accepts fixed, allowlisted slots. It cannot write state or execute a worker.
func reusableStageGuardContext(owner int, kind, slot string) (Manifest, signer.Config, *reusableMaintenanceAuthority, error) {
	var m Manifest
	var c signer.Config
	if owner <= 0 || os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || os.Getgid() <= 0 || os.Getgid() != os.Getegid() {
		return m, c, nil, errors.New("configured unprivileged signer identity required")
	}
	if _, err := reusableStageName(owner, kind, slot); err != nil {
		return m, c, nil, err
	}
	m, err := Load(owner)
	if err != nil {
		return m, c, nil, err
	}
	if err = reusableStageManifestMatches(m, readRoot); err != nil {
		return m, c, nil, err
	}
	c, err = signer.LoadConfig(m.Root + "/identity.json")
	if err != nil || c != identityConfig(m) || c.SignerUID != os.Getuid() || m.Signer.GID != os.Getgid() || (c.Version == 3 && c.Provenance != signer.ControlledCustodyProfile) {
		return m, c, nil, errors.New("configured disposable signer identity required")
	}
	pin, err := readReusableMaintenanceAuthority(m, readRoot)
	if err != nil || pin == nil {
		return m, c, nil, errors.New("pinned maintenance authority required")
	}
	exe, err := os.Executable()
	if err != nil || exe != reusableMaintenancePath(m, "atomic-guard") || verifyRootExecutable(exe, pin.GuardSHA256) != nil {
		return m, c, nil, errors.New("pinned signer guard executable required")
	}
	return m, c, pin, nil
}

// Staging must use the exact protected predecessor inventory, not merely an
// equivalent decoded value. Later replacement performs the same byte check.
func reusableStageManifestMatches(m Manifest, read func(string) (hostFile, error)) error {
	f, err := read(filepath.Join(m.Root, "install.json"))
	if err != nil || !known(f, mustJSON(m), 0644) {
		return errors.New("protected predecessor inventory differs; preserving it")
	}
	return nil
}

// Recheck the same request and held signer binding before a root-created copy
// and again before publication. The enclosing OS boundary still has to exclude
// concurrent privileged writers; this read-only check does not provide a lock.
func verifyReusableStageHeld(m Manifest, c signer.Config, pin *reusableMaintenanceAuthority, r reusableAtomicRequest, kind, slot string, boundary func() error, inspect func(signer.Config, string) (*update.Installation, error), read readHost) (*update.Installation, error) {
	if _, err := validateReusableAtomicRequest(mustJSON(r), m.Owner); err != nil || r.InstallationID != m.ID || r.Direction != "stage" || r.StageKind != kind || r.StageSlot != slot || !update.IDOK(r.RequestID) || boundary == nil || inspect == nil || read == nil {
		return nil, errors.New("exact maintenance preparation request required")
	}
	if err := boundary(); err != nil {
		return nil, err
	}
	hold, err := inspect(c, r.IntentID)
	if err != nil || hold == nil || hold.Binding.Index.Binding.Pause.Request == nil || hold.Binding.Index.Binding.Pause.Request.IntentID != r.IntentID {
		return nil, errors.New("matching cold signer installation hold required")
	}
	if _, _, err = reusableHeldStageExpectation(m, c, pin, hold, kind, slot); err != nil {
		return nil, err
	}
	t := InstallTransaction{Version: 1, Base: m, Target: m, Binding: hold.Binding}
	t.Target.CoreHash, t.Target.SignerHash = hold.Binding.TargetCore, hold.Binding.TargetSigner
	if err = validateReusablePredecessor(t, read); err != nil {
		return nil, err
	}
	// A direct request to an already-installed stage worker must not copy
	// over the original predecessor's still-unadopted transaction or images.
	if kind == "image" || (kind == "meta" && slot == "transaction") {
		if err = requireProtectedPredecessorStage(t, read); err != nil {
			return nil, err
		}
	}
	if err = boundary(); err != nil {
		return nil, err
	}
	if err = validateReusablePredecessor(t, read); err != nil {
		return nil, err
	}
	if kind == "image" || (kind == "meta" && slot == "transaction") {
		if err = requireProtectedPredecessorStage(t, read); err != nil {
			return nil, err
		}
	}
	return hold, nil
}

func VerifyReusableStageRecovery(owner int, kind, slot string) error {
	m, c, pin, err := reusableStageGuardContext(owner, kind, slot)
	if err != nil {
		return err
	}
	r, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil {
		return errors.New("exact maintenance preparation request required")
	}
	return verifyReusableStageRecovery(m, c, pin, r, kind, slot)
}

func verifyReusableStageRecovery(m Manifest, c signer.Config, pin *reusableMaintenanceAuthority, r reusableAtomicRequest, kind, slot string) error {
	boundary := func() error { return signer.VerifyInstallationPreparationBoundary(c) }
	if _, err := verifyReusableStageHeld(m, c, pin, r, kind, slot, boundary, signer.InspectInstallationReadOnly, readRoot); err != nil {
		return err
	}
	stage, inbox := reusableMaintenancePath(m, "staging"), reusableMaintenancePath(m, "inbox")
	if err := trustedParents(stage); err != nil {
		return fmt.Errorf("protected maintenance stage required: %w", err)
	}
	if err := trustedParents(filepath.Dir(inbox)); err != nil {
		return fmt.Errorf("protected maintenance inbox required: %w", err)
	}
	if err := validateStageRecoveryAt(stage, inbox, m.Owner, m.Signer.GID, 0, 0, kind, slot, os.Getenv("INVOCATION_ID")); err != nil {
		return err
	}
	if err := boundary(); err != nil {
		return err
	}
	if current, readErr := readReusableAtomicRequest(m.Owner, os.Getgid()); readErr != nil || current != r {
		return errors.New("staging request changed during verification")
	}
	return nil
}

// The launcher performs pre-dispatch validation without an output record. A
// stage unit must additionally match the request selected by that launcher
// before root creates its attempt directory or copy.
func VerifyReusableStageWorker(owner int, kind, slot string) error {
	m, c, pin, err := reusableStageGuardContext(owner, kind, slot)
	if err != nil {
		return err
	}
	r, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || r.InstallationID != m.ID || r.Direction != "stage" || r.StageKind != kind || r.StageSlot != slot {
		return errors.New("exact maintenance preparation request required")
	}
	worker, err := reusableStageName(owner, kind, slot)
	if err != nil {
		return err
	}
	if err = requireReusableWorkerDispatch(m, r.RequestID, worker, readReusableWorkerCompletion); err != nil {
		return err
	}
	if err = verifyReusableStageRecovery(m, c, pin, r, kind, slot); err != nil {
		return err
	}
	if current, readErr := readReusableAtomicRequest(owner, os.Getgid()); readErr != nil || current != r {
		return errors.New("staging request changed during verification")
	}
	return requireReusableWorkerDispatch(m, r.RequestID, worker, readReusableWorkerCompletion)
}

// A held binding cannot adopt a different protected transaction with the same
// intent, and a reader repair keeps exclusive ownership of its predecessor.
func validateReusablePredecessor(t InstallTransaction, read readHost) error {
	if !t.valid() || read == nil {
		return errors.New("exact predecessor transaction required")
	}
	if err := refuseRepairedPredecessor(t, read); err != nil {
		return err
	}
	for _, path := range []string{t.dir() + "/transaction.json", reusableMaintenancePath(t.Base, "active.json"), reusableMaintenancePath(t.Base, "staging/transaction")} {
		f, err := read(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !known(f, mustJSON(t), 0644) {
			return errors.New("protected predecessor transaction differs from the signer hold; preserve it")
		}
	}
	return nil
}

// A first preparation fence may not replace an already active update. A retry
// may reuse only this installation's protected sentinel and exact transaction.
func validateReusablePreparationStart(m Manifest, r reusableAtomicRequest, read readHost, same func() error) error {
	if m.validate() != nil || r.Owner != m.Owner || r.InstallationID != m.ID || r.Direction != "fence" || !update.IDOK(r.IntentID) || read == nil || same == nil {
		return errors.New("exact preparation request required")
	}
	for _, path := range []string{m.Root + "/update-active.json", reusableMaintenancePath(m, "predecessor-proof-v1.json")} {
		if _, err := read(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("an existing update owns the installation; preserve it")
		}
	}
	marker, markerErr := read(m.Root + "/update-start-blocked.json")
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return errors.New("preparation fence cannot be inspected")
	}
	// Do not create a new fence until the unchanged predecessor can be safely
	// restarted after every preparation failure. An existing exact sentinel is
	// still admitted so an interrupted preparation is not abandoned here.
	if errors.Is(markerErr, os.ErrNotExist) {
		return errors.New("new preparation fence unavailable; preserve the installation")
	}
	if markerErr == nil {
		want, err := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
		if err != nil || !known(marker, want, 0644) || same() != nil {
			return errors.New("another restart fence owns the installation; preserve it")
		}
	}
	active, activeErr := read(reusableMaintenancePath(m, "active.json"))
	if activeErr != nil && !errors.Is(activeErr, os.ErrNotExist) {
		return errors.New("preparation state cannot be inspected")
	}
	var transaction InstallTransaction
	if activeErr == nil {
		if markerErr != nil || strict(active.Data, &transaction) != nil || !known(active, mustJSON(transaction), 0644) || !transaction.valid() || transaction.Base != m || transaction.Binding.Index.Binding.Pause.Request.IntentID != r.IntentID {
			return errors.New("another preparation owns the installation; preserve it")
		}
		if err := validateReusablePredecessor(transaction, read); err != nil {
			return err
		}
	}
	published, publishedErr := read(m.Root + "/updates/" + r.IntentID + "/transaction.json")
	if publishedErr != nil && !errors.Is(publishedErr, os.ErrNotExist) {
		return errors.New("published transaction cannot be inspected")
	}
	if publishedErr == nil {
		var original InstallTransaction
		if strict(published.Data, &original) != nil || !known(published, mustJSON(original), 0644) || !original.valid() || original.Base != m || original.Binding.Index.Binding.Pause.Request.IntentID != r.IntentID || (activeErr == nil && !known(published, mustJSON(transaction), 0644)) {
			return errors.New("a different published transaction requires its existing recovery path")
		}
		if err := validateReusablePredecessor(original, read); err != nil {
			return err
		}
		// The predecessor could have published the transaction before taking
		// the hold. Do not treat later replacement or completion as a fresh
		// adoption, even if the reusable preparation marker is absent.
		for _, phase := range []string{"stopped", "core-replaced", "pair-replaced", "inventory-replaced", "started", "rollback", "rollback-stopped", "rollback-core-replaced", "rollback-pair-replaced", "rollback-inventory-replaced", "rollback-started", "healthy-held", "rolled-back-held", "sequence-committed", "completion-intent.json", "completion-ready.json", "completion-owner", "signer-completed", "core-released", "completed"} {
			if _, phaseErr := read(original.dir() + "/" + phase + ".json"); !errors.Is(phaseErr, os.ErrNotExist) {
				return errors.New("a progressed predecessor requires its existing recovery path")
			}
		}
		if held, heldErr := read(original.dir() + "/held.json"); heldErr == nil {
			var receipt update.Installation
			if strict(held.Data, &receipt) != nil || !known(held, mustJSON(receipt), 0644) || !update.ValidInstallation(receipt) || receipt.Phase != "active" || receipt.Digest != update.InstallDigest(original.Binding) {
				return errors.New("a different predecessor hold requires its existing recovery path")
			}
		} else if !errors.Is(heldErr, os.ErrNotExist) {
			return errors.New("predecessor hold cannot be inspected")
		}
	}
	return nil
}

func sameReusablePreparationFence(m Manifest) error {
	marker, source := m.Root+"/update-start-blocked.json", reusableMaintenancePath(m, "preparation-fence.json")
	if trustedParents(filepath.Dir(marker)) != nil || trustedParents(filepath.Dir(source)) != nil {
		return errors.New("protected preparation fence parents required")
	}
	var a, b unix.Stat_t
	if unix.Lstat(marker, &a) != nil || unix.Lstat(source, &b) != nil || a.Mode&unix.S_IFMT != unix.S_IFREG || b.Mode&unix.S_IFMT != unix.S_IFREG || a.Mode&07777 != 0644 || b.Mode&07777 != 0644 || a.Uid != 0 || b.Uid != 0 || a.Gid != 0 || b.Gid != 0 || a.Nlink < 2 || a.Dev != b.Dev || a.Ino != b.Ino || a.Ctim != b.Ctim {
		return errors.New("preparation fence is not the protected sentinel inode")
	}
	return nil
}

func VerifyReusablePreparationStart(owner int) error {
	m, c, _, err := reusableStageGuardContext(owner, "meta", "transaction")
	if err != nil {
		return err
	}
	r, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || r.Direction != "fence" {
		return errors.New("exact preparation fence request required")
	}
	if err = requireReusableWorkerDispatch(m, r.RequestID, reusablePreparationFenceName(owner), readReusableWorkerCompletion); err != nil {
		return err
	}
	for _, role := range []string{"app", "signer"} {
		if err = loadedUnit(m, role); err != nil {
			return err
		}
	}
	if err = signer.VerifyInstallationPreparationStart(c); err != nil {
		return err
	}
	if err = validateReusablePreparationStart(m, r, readRoot, func() error { return sameReusablePreparationFence(m) }); err != nil {
		return err
	}
	// Until unchanged-predecessor recovery is implemented, do not stop a live
	// pair. A retry may proceed only when the pair is already safely stopped and
	// the signer's actual matching hold can be inspected without mutation.
	if _, err = ReadReusablePreparationHold(owner, r.IntentID); err != nil {
		return errors.New("stopped predecessor and its exact signer hold required; preserve the installation")
	}
	current, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || current != r {
		return errors.New("preparation request changed; preserve it")
	}
	return requireReusableWorkerDispatch(m, r.RequestID, reusablePreparationFenceName(owner), readReusableWorkerCompletion)
}

// The sentinel itself is never signer authority. After both services stop,
// inspect the actual signer journal and exact predecessor before staging.
func VerifyReusablePreparationFence(owner int) error {
	m, _, _, err := reusableStageGuardContext(owner, "meta", "transaction")
	if err != nil {
		return err
	}
	r, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || r.Direction != "fence" || r.InstallationID != m.ID {
		return errors.New("exact preparation fence request required")
	}
	check := func() error {
		if err := requireReusableWorkerDispatch(m, r.RequestID, reusablePreparationFenceName(owner), readReusableWorkerCompletion); err != nil {
			return err
		}
		return validateReusablePreparationStart(m, r, readRoot, func() error { return sameReusablePreparationFence(m) })
	}
	if err = check(); err != nil {
		return err
	}
	if _, err = ReadReusablePreparationHold(owner, r.IntentID); err != nil {
		return err
	}
	if err = check(); err != nil {
		return err
	}
	current, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || current != r {
		return errors.New("preparation request changed; preserve the fence")
	}
	return nil
}

// ReadReusablePreparationHold runs as the pinned signer guard. It exposes only
// the existing held update metadata, after checking the stopped pair and the
// exact protected predecessor; it never changes or unlocks the vault.
func ReadReusablePreparationHold(owner int, intent string) (*update.Installation, error) {
	m, c, pin, err := reusableStageGuardContext(owner, "meta", "transaction")
	if err != nil || !update.IDOK(intent) {
		return nil, errors.New("exact protected preparation identity required")
	}
	if err = signer.VerifyInstallationPreparationBoundary(c); err != nil {
		return nil, err
	}
	hold, err := signer.InspectInstallationReadOnly(c, intent)
	if err != nil || hold == nil || hold.Binding.Index.Binding.Pause.Request == nil || hold.Binding.Index.Binding.Pause.Request.IntentID != intent {
		return nil, errors.New("actual active signer hold unavailable; preserve the fence")
	}
	if _, _, err = reusableHeldStageExpectation(m, c, pin, hold, "meta", "transaction"); err != nil {
		return nil, err
	}
	t := InstallTransaction{Version: 1, Base: m, Target: m, Binding: hold.Binding}
	t.Target.CoreHash, t.Target.SignerHash = hold.Binding.TargetCore, hold.Binding.TargetSigner
	if err = validateReusablePredecessor(t, readRoot); err != nil {
		return nil, err
	}
	for _, image := range []struct{ path, digest string }{{m.Root + "/fased-app", m.CoreHash}, {m.Root + "/fased-signer", m.SignerHash}} {
		if err = verifyRootExecutable(image.path, image.digest); err != nil {
			return nil, errors.New("predecessor image changed; preserve the fence")
		}
	}
	if err = signer.VerifyInstallationPreparationBoundary(c); err != nil {
		return nil, err
	}
	if err = validateReusablePredecessor(t, readRoot); err != nil {
		return nil, err
	}
	return hold, nil
}

// A held signer binding, not owner-supplied staging bytes, defines every slot.
func reusableHeldStageExpectation(m Manifest, c signer.Config, pin *reusableMaintenanceAuthority, hold *update.Installation, kind, slot string) (string, int64, error) {
	if hold == nil || !update.ValidInstallation(*hold) || hold.Phase != "active" || hold.InstalledSequenceCommitted {
		return "", 0, errors.New("exact active signer installation hold required")
	}
	if _, err := reusableStageName(m.Owner, kind, slot); err != nil {
		return "", 0, err
	}
	t := InstallTransaction{Version: 1, Base: m, Target: m, Binding: hold.Binding}
	t.Target.CoreHash, t.Target.SignerHash = hold.Binding.TargetCore, hold.Binding.TargetSigner
	if !t.valid() || c.Validate() != nil || c != identityConfig(m) || pin == nil || pin.Version != 1 || pin.Owner != m.Owner || pin.InstallationID != m.ID {
		return "", 0, errors.New("signer hold differs from the installed predecessor")
	}
	if kind == "image" {
		switch slot {
		case "runner":
			return m.CoreHash, -1, nil
		case "previous-signer":
			return m.SignerHash, -1, nil
		case "next-core", "next-signer":
			name := "fased-app"
			if slot == "next-signer" {
				name = "fased-signer"
			}
			for _, f := range hold.Binding.Index.Binding.Files {
				if f.Artifact.File == name {
					return f.Artifact.SHA256, f.Bytes, nil
				}
			}
		}
		return "", 0, errors.New("signer hold lacks the requested component")
	}
	inputs, err := reusableBoundMetadata(t, c, pin)
	if err != nil {
		return "", 0, err
	}
	for _, item := range inputs {
		if item.kind == kind && item.slot == slot {
			return item.digest, item.size, nil
		}
	}
	return "", 0, errors.New("signer hold lacks the requested staging slot")
}

func validateReusableStageCopyAt(stage string, m Manifest, c signer.Config, pin *reusableMaintenanceAuthority, hold *update.Installation, kind, slot, invocation string, uid, gid int) error {
	if !update.IDOK(invocation) {
		return errors.New("exact systemd staging invocation required")
	}
	digest, size, err := reusableHeldStageExpectation(m, c, pin, hold, kind, slot)
	if err != nil {
		return err
	}
	_, suffix, err := reusableStageClass(kind)
	if err != nil {
		return err
	}
	limit := int64(64 << 20)
	if kind == "meta" {
		limit = 4 << 20
	} else if kind == "unit" {
		limit = 32768
	}
	path := filepath.Join(stage, ".stage-"+slot+suffix+"-"+invocation+".d", "copy")
	f, err := openStageRecoveryFile(path, uid, gid, []uint32{0644}, limit)
	if err != nil {
		return err
	}
	defer f.file.Close()
	if f.stat.Nlink != 1 || f.stat.Size == 0 || (size >= 0 && f.stat.Size != size) {
		return errors.New("staged copy differs from the signer hold")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f.file, limit+1))
	if err != nil || n != f.stat.Size || hex.EncodeToString(h.Sum(nil)) != digest || !f.stable() {
		return errors.New("staged copy differs from the signer hold")
	}
	return nil
}

// VerifyReusableStageCopy requires the real cold signer hold and the OS fence.
// It runs as the signer; root only publishes after this read-only check.
func VerifyReusableStageCopy(owner int, kind, slot string) error {
	m, c, pin, err := reusableStageGuardContext(owner, kind, slot)
	if err != nil {
		return err
	}
	r, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || r.InstallationID != m.ID || r.Direction != "stage" || r.StageKind != kind || r.StageSlot != slot {
		return errors.New("exact maintenance preparation request required")
	}
	worker, err := reusableStageName(owner, kind, slot)
	if err != nil {
		return err
	}
	if err = requireReusableWorkerDispatch(m, r.RequestID, worker, readReusableWorkerCompletion); err != nil {
		return err
	}
	f, err := readRoot(m.Root + "/update-start-blocked.json")
	var marker signer.InstallationGuardMarker
	sentinel, sentinelErr := signer.InstallationPreparationFenceBytes(m.Owner, m.ID)
	preparing := err == nil && sentinelErr == nil && known(f, sentinel, 0644)
	if !preparing && (err != nil || strict(f.Data, &marker) != nil || !known(f, mustJSON(marker), 0644) || marker.Version != 1 || marker.Owner != owner || marker.SignerID != m.ID || marker.IntentID != r.IntentID || marker.Direction != "forward" || !validHex(marker.Digest, 32)) {
		return errors.New("exact preparation or forward signer restart fence required")
	}
	boundary := func() error {
		if preparing {
			return signer.VerifyInstallationPreparationBoundary(c)
		}
		return signer.VerifyInstallationGuardBoundary(c, r.IntentID, marker.Digest)
	}
	hold, err := verifyReusableStageHeld(m, c, pin, r, kind, slot, boundary, signer.InspectInstallationReadOnly, readRoot)
	if err != nil {
		return err
	}
	if !preparing && hold.Digest != marker.Digest {
		return errors.New("matching cold signer installation hold required")
	}
	stage := reusableMaintenancePath(m, "staging")
	attempt := filepath.Join(stage, ".stage-"+slot+"-"+os.Getenv("INVOCATION_ID")+".d")
	if err = trustedParents(attempt); err != nil {
		return errors.New("protected staging invocation required")
	}
	if err = validateReusableStageCopyAt(stage, m, c, pin, hold, kind, slot, os.Getenv("INVOCATION_ID"), 0, 0); err != nil {
		return err
	}
	if err = boundary(); err != nil {
		return err
	}
	current, err := readReusableAtomicRequest(owner, os.Getgid())
	if err != nil || current != r {
		return errors.New("staging request changed during verification")
	}
	return requireReusableWorkerDispatch(m, r.RequestID, worker, readReusableWorkerCompletion)
}
