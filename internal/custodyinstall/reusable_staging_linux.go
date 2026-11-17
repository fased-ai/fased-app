//go:build linux

package custodyinstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

type reusableStageInput struct {
	kind, slot, path, digest string
	data                     []byte
	size                     int64
}

// stageSource keeps one bounded descriptor, so candidate binaries are never
// accumulated in memory and a symlink cannot redirect a later read.
func stageSource(path, digest string) (*os.File, int64, error) {
	if !validHex(digest, 32) {
		return nil, 0, errors.New("exact staging digest required")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, 0, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<20 {
		f.Close()
		return nil, 0, errors.New("bounded regular staging source required")
	}
	return f, info.Size(), nil
}

func openReusableInbox(inbox string, owner, signerGID int, trust func(string) error) (*os.File, error) {
	if owner <= 0 || signerGID <= 0 || trust == nil || trust(filepath.Dir(inbox)) != nil {
		return nil, errors.New("protected maintenance inbox required")
	}
	fd, err := unix.Open(inbox, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&07777 != 02710 || int(st.Uid) != owner || int(st.Gid) != signerGID {
		unix.Close(fd)
		return nil, errors.New("foreign maintenance inbox")
	}
	return os.NewFile(uintptr(fd), inbox), nil
}

func inspectOwnerStage(dfd int, slot string, owner, signerGID int, size int64, digest string) (unix.Stat_t, error) {
	var zero unix.Stat_t
	fd, err := unix.Openat(dfd, slot, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return zero, err
	}
	f := os.NewFile(uintptr(fd), slot)
	defer f.Close()
	var before, after unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&07777 != 0640 || int(before.Uid) != owner || int(before.Gid) != signerGID || before.Nlink != 1 || before.Size != size {
		return zero, errors.New("foreign owner staging input; preserving it")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, size+1))
	if err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != digest || unix.Fstat(fd, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Ctim != after.Ctim || before.Size != after.Size {
		return zero, errors.New("owner staging input differs; preserving it")
	}
	return before, nil
}

// Owner-side temporary input is exclusive and contains only a copy of an
// already checked component. A different existing file is never overwritten.
func prepareOwnerStage(inbox *os.File, slot string, owner, signerGID int, input io.Reader, size int64, digest string) (unix.Stat_t, error) {
	var zero unix.Stat_t
	dfd := int(inbox.Fd())
	existing, err := inspectOwnerStage(dfd, slot, owner, signerGID, size, digest)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, unix.ENOENT) {
		return zero, err
	}
	temp := ".stage-" + update.NewProbe()
	fd, err := unix.Openat(dfd, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return zero, err
	}
	var created unix.Stat_t
	if err = unix.Fstat(fd, &created); err != nil {
		unix.Close(fd)
		return zero, err
	}
	defer func() {
		var current unix.Stat_t
		if unix.Fstatat(dfd, temp, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == created.Dev && current.Ino == created.Ino {
			_ = unix.Unlinkat(dfd, temp, 0)
		}
	}()
	f := os.NewFile(uintptr(fd), temp)
	h := sha256.New()
	n, copyErr := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(input, size+1), make([]byte, 32*1024))
	if copyErr == nil && (n != size || hex.EncodeToString(h.Sum(nil)) != digest) {
		copyErr = errors.New("staging source differs from the reviewed component")
	}
	if copyErr == nil {
		copyErr = f.Chmod(0640)
	}
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		return zero, errors.New("could not copy the exact staging source")
	}
	if err = unix.Linkat(dfd, temp, dfd, slot, 0); err != nil {
		return zero, errors.New("owner staging slot already exists; preserving it")
	}
	if err = unix.Unlinkat(dfd, temp, 0); err != nil {
		return zero, err
	}
	if err = unix.Fsync(dfd); err != nil {
		return zero, err
	}
	return inspectOwnerStage(dfd, slot, owner, signerGID, size, digest)
}

func reusableRootStageMatches(t InstallTransaction, item reusableStageInput) (bool, error) {
	path := reusableMaintenancePath(t.Base, "staging") + "/" + item.slot
	if item.kind == "image" {
		size, err := verifiedMigrationRunnerSize(path, item.digest)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil || size != item.size {
			return false, errors.New("foreign root staging executable; preserving it")
		}
		return true, nil
	}
	f, err := readRoot(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !known(f, item.data, 0644) {
		return false, errors.New("foreign root staging metadata; preserving it")
	}
	return true, nil
}

func stageReusableInput(ctx context.Context, t InstallTransaction, s *System, inbox *os.File, item reusableStageInput) error {
	ready, err := reusableRootStageMatches(t, item)
	if err != nil {
		return err
	}
	if ready {
		staged, inspectErr := inspectOwnerStage(int(inbox.Fd()), item.slot, t.Base.Owner, t.Base.Signer.GID, item.size, item.digest)
		if errors.Is(inspectErr, unix.ENOENT) {
			return nil
		}
		if inspectErr != nil {
			return inspectErr
		}
		return clearOwnerStage(inbox, item.slot, staged)
	}
	var input io.Reader = bytes.NewReader(item.data)
	if item.kind == "image" {
		f, size, openErr := stageSource(item.path, item.digest)
		if openErr != nil || size != item.size {
			if f != nil {
				f.Close()
			}
			return errors.New("reviewed staging component is unavailable")
		}
		defer f.Close()
		input = f
	}
	staged, err := prepareOwnerStage(inbox, item.slot, t.Base.Owner, t.Base.Signer.GID, input, item.size, item.digest)
	if err != nil {
		return err
	}
	unit, nameErr := reusableStageName(t.Base.Owner, item.kind, item.slot)
	if nameErr != nil {
		return nameErr
	}
	requestID, err := writeReusableAtomicRequest(t, "stage", item.kind, item.slot, "")
	if err != nil {
		return err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", reusableAtomicLauncherName(t.Base.Owner)); err != nil {
		return fmt.Errorf("bounded staging did not complete; preserve it for retry: %w", err)
	}
	if err = requireReusableWorkerCompletion(t.Base, requestID, unit, readReusableWorkerCompletion); err != nil {
		return err
	}
	ready, err = reusableRootStageMatches(t, item)
	if err != nil {
		return err
	}
	if ready {
		return clearOwnerStage(inbox, item.slot, staged)
	}
	return errors.New("protected staging did not publish the reviewed input; preserve it for retry")
}

// This reserved file is only a duplicate of the now-verified root slot.
func clearOwnerStage(inbox *os.File, slot string, staged unix.Stat_t) error {
	var current unix.Stat_t
	if unix.Fstatat(int(inbox.Fd()), slot, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && current.Dev == staged.Dev && current.Ino == staged.Ino && current.Ctim == staged.Ctim && current.Nlink == 1 {
		if err := unix.Unlinkat(int(inbox.Fd()), slot, 0); err != nil {
			return err
		}
		return unix.Fsync(int(inbox.Fd()))
	}
	return errors.New("owner staging input changed; preserving it")
}

func reusableControllerInputs(t InstallTransaction, c signer.Config, pin *reusableMaintenanceAuthority, artifacts string) ([]reusableStageInput, error) {
	load := func() (InstallTransaction, error) {
		return LoadCanonicalController(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID)
	}
	if err := requirePublishedPredecessorStage(t, load, readRoot, os.Lstat, func(original, staged string, mode os.FileMode) error {
		return sameProtectedUpdateFileAt(original, staged, mode, 0, 0, trustedParents)
	}); err != nil {
		return nil, err
	}
	return reusableControllerInputsAt(t, c, pin, artifacts, t.Base.Root, func(slot, digest string) (string, int64, error) {
		return reusableProtectedCandidate(t, slot, digest, verifiedMigrationRunnerSize, sameProtectedCandidate, load)
	})
}

// The signer uses the same refusal at its own stage and provision admissions;
// an owner-side preflight alone cannot restrict an already-installed launcher.
func requireProtectedPredecessorStage(t InstallTransaction, read readHost) error {
	if read == nil {
		return errors.New("protected predecessor inspection required")
	}
	return requirePublishedPredecessorStage(t, func() (InstallTransaction, error) {
		var zero InstallTransaction
		f, err := read(t.dir() + "/transaction.json")
		if err != nil {
			return zero, err
		}
		if !known(f, mustJSON(t), 0644) {
			return zero, errors.New("published predecessor changed")
		}
		return t, nil
	}, read, os.Lstat, func(original, staged string, mode os.FileMode) error {
		return sameProtectedUpdateFileAt(original, staged, mode, 0, 0, trustedParents)
	})
}

// Until the predecessor files can be adopted atomically, refuse before making
// any stage copies that would differ from the already-published inodes.
func requirePublishedPredecessorStage(t InstallTransaction, load func() (InstallTransaction, error), read readHost, stat func(string) (os.FileInfo, error), same func(string, string, os.FileMode) error) error {
	if !t.valid() || load == nil || read == nil || stat == nil || same == nil {
		return errors.New("exact predecessor inspection required")
	}
	published, err := load()
	if errors.Is(err, os.ErrNotExist) {
		// Both the predecessor and this provisioner can write images before
		// the transaction. Accept an interrupted provisioner's own prefix
		// only when its protected active record and each link are identical.
		for _, name := range []string{"runner", "previous-signer", "next-core", "next-signer", "transaction.json"} {
			pending := filepath.Join(t.dir(), "."+name+"-"+t.Base.ID+".pending")
			if _, inspectErr := stat(pending); !errors.Is(inspectErr, os.ErrNotExist) {
				return errors.New("predecessor has a retained pending file; preserve it for recovery")
			}
		}
		var existing []string
		for _, slot := range []string{"runner", "previous-signer", "next-core", "next-signer"} {
			_, inspectErr := stat(t.dir() + "/" + slot)
			if inspectErr == nil {
				existing = append(existing, slot)
			} else if !errors.Is(inspectErr, os.ErrNotExist) {
				return errors.New("partial predecessor requires recovery; preserve all original files")
			}
		}
		stage, active := reusableMaintenancePath(t.Base, "staging"), reusableMaintenancePath(t.Base, "active.json")
		f, activeErr := read(active)
		if errors.Is(activeErr, os.ErrNotExist) && len(existing) == 0 {
			return nil
		}
		if activeErr != nil || !known(f, mustJSON(t), 0644) || same(active, stage+"/transaction", 0644) != nil {
			return errors.New("partial predecessor requires recovery; preserve all original files")
		}
		for _, slot := range existing {
			if same(t.dir()+"/"+slot, stage+"/"+slot, 0755) != nil {
				return errors.New("partial predecessor requires recovery; preserve all original files")
			}
		}
		return nil
	}
	if err != nil || !bytes.Equal(mustJSON(published), mustJSON(t)) {
		return errors.New("published predecessor differs or cannot be inspected; preserve it")
	}
	stage := reusableMaintenancePath(t.Base, "staging")
	active := reusableMaintenancePath(t.Base, "active.json")
	f, activeErr := read(active)
	if activeErr == nil {
		if !known(f, mustJSON(t), 0644) || same(active, stage+"/transaction", 0644) != nil {
			return errors.New("active preparation differs from protected staging; preserve it")
		}
	} else if !errors.Is(activeErr, os.ErrNotExist) {
		return errors.New("active preparation cannot be inspected; preserve it")
	}
	for _, item := range []struct {
		slot, source string
		mode         os.FileMode
	}{
		{"transaction", t.dir() + "/transaction.json", 0644},
		{"runner", t.dir() + "/runner", 0755},
		{"previous-signer", t.dir() + "/previous-signer", 0755},
		{"next-core", t.dir() + "/next-core", 0755},
		{"next-signer", t.dir() + "/next-signer", 0755},
	} {
		if err := same(item.source, stage+"/"+item.slot, item.mode); err != nil {
			return errors.New("published predecessor requires protected staging adoption; preserve all original files")
		}
	}
	return nil
}

// A published predecessor already owns its reviewed candidate images. Once
// that transaction exists, missing or altered originals cannot be replaced by
// a later owner-supplied artifact directory during recovery.
func reusableProtectedCandidate(t InstallTransaction, slot, digest string, check func(string, string) (int64, error), same func(string, string) error, load func() (InstallTransaction, error)) (string, int64, error) {
	if !t.valid() || (slot != "next-core" && slot != "next-signer") || check == nil || same == nil || load == nil {
		return "", 0, errors.New("exact protected candidate required")
	}
	published, err := load()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", 0, err
	}
	if err == nil && !bytes.Equal(mustJSON(published), mustJSON(t)) {
		return "", 0, errors.New("published predecessor differs; preserve it")
	}
	stage := reusableMaintenancePath(t.Base, "staging") + "/" + slot
	if errors.Is(err, os.ErrNotExist) {
		size, stageErr := check(stage, digest)
		return stage, size, stageErr
	}
	original := t.dir() + "/" + slot
	size, err := check(original, digest)
	if err != nil {
		return "", 0, errors.New("protected predecessor candidate unavailable or changed; preserve it")
	}
	_, stageErr := check(stage, digest)
	if errors.Is(stageErr, os.ErrNotExist) {
		return "", 0, errors.New("published candidate requires protected staging adoption; preserve it")
	}
	if stageErr != nil || same(original, stage) != nil {
		return "", 0, errors.New("staged candidate is not the original protected inode; preserve it")
	}
	return stage, size, nil
}

func sameProtectedCandidate(original, staged string) error {
	return sameProtectedUpdateFileAt(original, staged, 0755, 0, 0, trustedParents)
}

// A same-byte copy is not an adopted predecessor file: later cleanup relies
// on the actual root-owned inode. Lstat avoids following a staged symlink.
func sameProtectedUpdateFileAt(original, staged string, mode os.FileMode, uid, gid int, trust func(string) error) error {
	if trust == nil || trust(filepath.Dir(original)) != nil || trust(filepath.Dir(staged)) != nil {
		return errors.New("protected update file parents required")
	}
	inspect := func(path string) (os.FileInfo, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode() != mode || int(st.Uid) != uid || int(st.Gid) != gid {
			return nil, errors.New("foreign protected update file; preserve it")
		}
		return info, nil
	}
	a, err := inspect(original)
	if err != nil {
		return err
	}
	b, err := inspect(staged)
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) {
		return errors.New("staged file is not the protected original inode")
	}
	return nil
}

// The conductor and signer guard derive metadata and workers identically.
func reusableBoundMetadata(t InstallTransaction, c signer.Config, pin *reusableMaintenanceAuthority) ([]reusableStageInput, error) {
	if !t.valid() || pin == nil {
		return nil, errors.New("pinned installation transaction required")
	}
	approval := migrationRunnerApproval{Version: 1, Owner: t.Base.Owner, InstallationID: t.Base.ID, IntentID: t.Binding.Index.Binding.Pause.Request.IntentID, BindingDigest: update.InstallDigest(t.Binding), PreviousCore: t.Base.CoreHash, TargetCore: t.Target.CoreHash, TargetSigner: t.Target.SignerHash, RunnerSHA256: pin.RunnerSHA256, GuardSHA256: pin.GuardSHA256}
	if !validMigrationApproval(t, approval) {
		return nil, errors.New("pinned reader is incompatible with this transaction")
	}
	plan, err := makeReusableAtomicPairPlan(t, c, pin.GuardSHA256, pin.RunnerSHA256)
	if err != nil {
		return nil, err
	}
	var inputs []reusableStageInput
	add := func(kind, slot string, data []byte) error {
		limit := 4 << 20
		if kind == "unit" {
			limit = 32768
		}
		if _, err := reusableStageName(t.Base.Owner, kind, slot); err != nil || len(data) == 0 || len(data) > limit {
			return errors.New("incomplete fixed staging input")
		}
		inputs = append(inputs, reusableStageInput{kind: kind, slot: slot, data: data, digest: sum(data), size: int64(len(data))})
		return nil
	}
	for _, item := range reusableMetadataFiles {
		var data []byte
		switch item.slot {
		case "transaction":
			data = mustJSON(t)
		case "approval":
			data = mustJSON(approval)
		default:
			data = plan.inputs[t.dir()+"/"+item.name]
		}
		if err = add("meta", item.slot, data); err != nil {
			return nil, err
		}
	}
	for _, direction := range reusableWorkerDirections {
		name, nameErr := atomicWorkerName(plan, direction)
		if nameErr != nil {
			return nil, nameErr
		}
		if err = add("unit", "unit-"+direction, plan.units[filepath.Join("/etc/systemd/system", name)]); err != nil {
			return nil, err
		}
	}
	return inputs, nil
}

func reusableControllerInputsAt(t InstallTransaction, c signer.Config, pin *reusableMaintenanceAuthority, artifacts, installedRoot string, staged func(string, string) (string, int64, error)) ([]reusableStageInput, error) {
	inputs, err := reusableBoundMetadata(t, c, pin)
	if err != nil {
		return nil, err
	}
	declared := map[string]int64{}
	for _, file := range t.Binding.Index.Binding.Files {
		declared[file.Artifact.File] = file.Bytes
	}
	for _, image := range []struct {
		slot, path, digest string
		declared           int64
	}{
		{"runner", filepath.Join(installedRoot, "fased-app"), t.Base.CoreHash, 0},
		{"previous-signer", filepath.Join(installedRoot, "fased-signer"), t.Base.SignerHash, 0},
		{"next-core", filepath.Join(artifacts, "fased-app"), t.Target.CoreHash, declared["fased-app"]},
		{"next-signer", filepath.Join(artifacts, "fased-signer"), t.Target.SignerHash, declared["fased-signer"]},
	} {
		if staged != nil && (image.slot == "next-core" || image.slot == "next-signer") {
			path, size, stageErr := staged(image.slot, image.digest)
			if stageErr == nil {
				if image.declared <= 0 || image.declared != size {
					return nil, errors.New("protected staging size differs from the signed component")
				}
				inputs = append(inputs, reusableStageInput{kind: "image", slot: image.slot, path: path, digest: image.digest, size: size})
				continue
			}
			if !errors.Is(stageErr, os.ErrNotExist) {
				return nil, errors.New("protected staging component changed; preserve it")
			}
		}
		f, size, openErr := stageSource(image.path, image.digest)
		if openErr != nil {
			return nil, openErr
		}
		if (image.slot == "next-core" || image.slot == "next-signer") && (image.declared <= 0 || image.declared != size) {
			f.Close()
			return nil, errors.New("candidate size differs from the signed component")
		}
		verifyErr := verifyExecutableContents(f, size, image.digest)
		closeErr := f.Close()
		if verifyErr != nil || closeErr != nil {
			return nil, errors.New("staging component differs from the reviewed transaction")
		}
		inputs = append(inputs, reusableStageInput{kind: "image", slot: image.slot, path: image.path, digest: image.digest, size: size})
	}
	return inputs, nil
}

func prepareReusableController(ctx context.Context, t InstallTransaction, c signer.Config, artifacts string, s *System, pin *reusableMaintenanceAuthority, hold func(context.Context, string, update.InstallBinding) (*update.Installation, error)) (InstallTransaction, error) {
	var zero InstallTransaction
	if t.Binding.Index.Binding.Pause.Request == nil {
		return zero, errors.New("exact preparation intent required")
	}
	r := reusableAtomicRequest{Owner: t.Base.Owner, InstallationID: t.Base.ID, IntentID: t.Binding.Index.Binding.Pause.Request.IntentID, Direction: "fence"}
	if err := validateReusablePreparationStart(t.Base, r, readRoot, func() error { return sameReusablePreparationFence(t.Base) }); err != nil {
		return zero, fmt.Errorf("reusable custody preparation is unavailable: %w", err)
	}
	if err := verifyReusablePreparationHost(ctx, t, s, pin); err != nil {
		return zero, err
	}
	active, err := readRoot(reusableMaintenancePath(t.Base, "active.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return zero, err
	}
	if err == nil && !known(active, mustJSON(t), 0644) {
		return zero, errors.New("another preparation owns the installation; preserve it")
	}
	inputs, err := reusableControllerInputs(t, c, pin, artifacts)
	if err != nil {
		return zero, err
	}
	var missing, largest uint64
	for _, item := range inputs {
		ready, stageErr := reusableRootStageMatches(t, item)
		if stageErr != nil {
			return zero, stageErr
		}
		if !ready {
			missing += uint64(item.size)
			if uint64(item.size) > largest {
				largest = uint64(item.size)
			}
		}
	}
	var fs unix.Statfs_t
	if err = unix.Statfs(t.Base.Root, &fs); err != nil || fs.Bsize <= 0 || fs.Bavail < (missing+2*largest+(16<<20)+uint64(fs.Bsize)-1)/uint64(fs.Bsize) {
		return zero, errors.New("insufficient space for bounded staging and recovery")
	}
	inbox, err := openReusableInbox(reusableMaintenancePath(t.Base, "inbox"), t.Base.Owner, t.Base.Signer.GID, trustedParents)
	if err != nil {
		return zero, err
	}
	defer inbox.Close()
	if hold == nil {
		return zero, errors.New("bound signer installation hold required before staging")
	}
	receipt, err := hold(ctx, "active", t.Binding)
	if err != nil {
		return zero, err
	}
	if receipt == nil || !update.ValidInstallation(*receipt) || receipt.Phase != "active" || receipt.InstalledSequenceCommitted || receipt.Digest != update.InstallDigest(t.Binding) {
		return zero, errors.New("exact active signer hold required before staging")
	}
	fenceRequest, err := writeReusableAtomicRequest(t, "fence", "", "", "")
	if err != nil {
		return zero, err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", reusableAtomicLauncherName(t.Base.Owner)); err != nil {
		return zero, fmt.Errorf("guarded preparation fence did not complete: %w", err)
	}
	if err = requireReusableWorkerCompletion(t.Base, fenceRequest, reusablePreparationFenceName(t.Base.Owner), readReusableWorkerCompletion); err != nil {
		return zero, err
	}
	for _, item := range inputs {
		if err = stageReusableInput(ctx, t, s, inbox, item); err != nil {
			return zero, err
		}
	}
	for _, slot := range reusableCompatibilitySlots {
		requestID, requestErr := writeReusableAtomicRequest(t, "compatibility", "", "", slot)
		if requestErr != nil {
			return zero, requestErr
		}
		if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", reusableAtomicLauncherName(t.Base.Owner)); err != nil {
			return zero, fmt.Errorf("paired update compatibility did not complete: %w", err)
		}
		worker, nameErr := reusableCompatibilityName(t.Base.Owner, slot)
		if nameErr != nil {
			return zero, nameErr
		}
		if err = requireReusableWorkerCompletion(t.Base, requestID, worker, readReusableWorkerCompletion); err != nil {
			return zero, err
		}
	}
	requestID, err := writeReusableAtomicRequest(t, "prepare", "", "", "")
	if err != nil {
		return zero, err
	}
	if _, err = s.Run(ctx, nil, "/usr/bin/systemctl", "start", "--", reusableAtomicLauncherName(t.Base.Owner)); err != nil {
		return zero, fmt.Errorf("protected maintenance preparation did not complete: %w", err)
	}
	if err = requireReusableWorkerCompletion(t.Base, requestID, reusableAtomicProvisionName(t.Base.Owner), readReusableWorkerCompletion); err != nil {
		return zero, err
	}
	actual, err := LoadController(t.Base.Owner, t.Binding.Index.Binding.Pause.Request.IntentID)
	if err != nil || !bytes.Equal(mustJSON(actual), mustJSON(t)) {
		return zero, errors.New("prepared controller differs; preserve the transaction")
	}
	if _, err = admitAtomicPair(ctx, actual, s); err != nil {
		return zero, err
	}
	return actual, nil
}
