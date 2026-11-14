//go:build linux

package signer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// VerifyInstallationGuardInventory is run as the signer, never as root. The
// separate OS policy must keep both services stopped and serialize all writes.
func VerifyInstallationGuardInventory(c Config, i *update.Installation, intent, direction, stage string) error {
	read := func(path string) ([]byte, error) {
		var b bytes.Buffer
		if err := readGuardRootFile(path, 0644, 4<<20, &b); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	hash := func(path string) (string, error) {
		h := sha256.New()
		if err := readGuardRootFile(path, 0755, 64<<20, h); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	return validateInstallationGuardInventory(c, i, intent, direction, stage, read, hash, sameGuardRootFile)
}

func sameGuardRootFile(first, second string) error {
	var stats [2]unix.Stat_t
	for n, path := range []string{first, second} {
		if trustedPath(path) != nil {
			return errors.New("protected predecessor proof path required")
		}
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		var st, current unix.Stat_t
		err = unix.Fstat(fd, &st)
		pathErr := unix.Lstat(path, &current)
		unix.Close(fd)
		if err != nil || pathErr != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&07777 != 0644 || st.Uid != 0 || st.Gid != 0 || st.Nlink < 2 || st.Dev != current.Dev || st.Ino != current.Ino || st.Ctim != current.Ctim {
			return errors.New("predecessor proof inode changed; preserving it")
		}
		stats[n] = st
	}
	if stats[0].Dev != stats[1].Dev || stats[0].Ino != stats[1].Ino || stats[0].Ctim != stats[1].Ctim {
		return errors.New("predecessor proof is not the protected transaction inode")
	}
	return nil
}

// readGuardRootFile validates the descriptor and its path before and after a
// bounded streaming read. A trusted privileged policy must prevent root writes
// while this runs; these checks alone do not provide mutual exclusion.
func readGuardRootFile(path string, mode os.FileMode, limit int64, out io.Writer) error {
	if err := trustedPath(path); err != nil {
		return err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Gid != 0 || !before.Mode().IsRegular() || before.Mode() != mode || before.Size() <= 0 || before.Size() > limit {
		return errors.New("foreign or unsafe installation guard input")
	}
	n, err := io.Copy(out, io.LimitReader(f, limit+1))
	if err != nil || n != before.Size() || n > limit {
		return errors.New("installation guard input changed during read")
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, current) || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || current.Mode() != before.Mode() || current.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) {
		return errors.New("installation guard input changed during inspection")
	}
	afterStat, aok := after.Sys().(*syscall.Stat_t)
	currentStat, cok := current.Sys().(*syscall.Stat_t)
	if !aok || !cok || st.Uid != afterStat.Uid || st.Gid != afterStat.Gid || st.Uid != currentStat.Uid || st.Gid != currentStat.Gid || st.Ctim != afterStat.Ctim || afterStat.Ctim != currentStat.Ctim {
		return errors.New("installation guard metadata changed during inspection")
	}
	return trustedPath(path)
}

func validateInstallationGuardInventory(c Config, i *update.Installation, intent, direction, stage string, read func(string) ([]byte, error), hash func(string) (string, error), same func(string, string) error) error {
	if c.Validate() != nil || i == nil || !update.ValidInstallation(*i) || c.SignerID != i.Binding.InstallationID || i.Binding.Index.Binding.Pause.Request == nil || !update.IDOK(intent) || i.Binding.Index.Binding.Pause.Request.IntentID != intent || (stage != "before" && stage != "after" && stage != "predecessor") || (stage == "predecessor" && direction != "forward" && direction != "rollback") {
		return errors.New("exact guarded installation and stage required")
	}
	wantCore, wantSigner := i.Binding.TargetCore, i.Binding.TargetSigner
	if direction == "rollback" || direction == "cleanup-rollback" {
		wantCore, wantSigner = i.Binding.PreviousCore, i.Binding.PreviousSigner
	}
	component, source := "core", wantCore
	if direction == "cleanup-rollback" || direction == "cleanup-complete" {
		component, source = "installation", i.Digest
	}
	if err := ValidateInstallationReplacement(i, intent, i.Digest, direction, component, source); err != nil {
		return err
	}
	if direction != "completion" && direction != "cleanup-rollback" && direction != "cleanup-complete" {
		if err := ValidateInstallationReplacement(i, intent, i.Digest, direction, "signer", wantSigner); err != nil {
			return err
		}
	}
	root := fmt.Sprintf("/var/lib/fased-app-custody-%d", c.OwnerUID)
	if c.StateDir != filepath.Join(root, "signer-state") || c.SocketDir != filepath.Join(root, "signer-ipc") {
		return errors.New("signer storage differs from the installation")
	}
	dir := filepath.Join(root, "updates", intent)
	b, err := read(filepath.Join(dir, "transaction.json"))
	if err != nil {
		return errors.New("installation transaction unavailable")
	}
	var tx struct {
		Version int                   `json:"version"`
		Base    json.RawMessage       `json:"base"`
		Target  json.RawMessage       `json:"target"`
		Binding update.InstallBinding `json:"binding"`
	}
	if update.StrictJSON(b, &tx) != nil || tx.Version != 1 || update.InstallDigest(tx.Binding) != i.Digest || metadataHash(tx.Base) != i.Binding.PreviousManifest {
		return errors.New("installation transaction differs from the signer hold")
	}
	var base, target map[string]json.RawMessage
	if json.Unmarshal(tx.Base, &base) != nil || json.Unmarshal(tx.Target, &target) != nil || base == nil || target == nil {
		return errors.New("invalid installation inventory")
	}
	var identity struct {
		ID         string `json:"id"`
		Owner      int    `json:"owner"`
		Root       string `json:"root"`
		CoreHash   string `json:"coreHash"`
		SignerHash string `json:"signerHash"`
		App        struct {
			UID int `json:"uid"`
		} `json:"app"`
		Signer struct {
			UID int `json:"uid"`
		} `json:"signer"`
	}
	if json.Unmarshal(tx.Base, &identity) != nil || identity.ID != i.Binding.InstallationID || identity.Owner != c.OwnerUID || identity.Root != root || identity.App.UID != c.AppUID || identity.Signer.UID != c.SignerUID || identity.CoreHash != i.Binding.PreviousCore || identity.SignerHash != i.Binding.PreviousSigner {
		return errors.New("installation inventory differs from signer identity")
	}
	base["coreHash"], _ = json.Marshal(i.Binding.TargetCore)
	base["signerHash"], _ = json.Marshal(i.Binding.TargetSigner)
	expected, _ := json.Marshal(base)
	actual, _ := json.Marshal(target)
	if !bytes.Equal(expected, actual) {
		return errors.New("target changes installation identity")
	}
	for _, item := range []struct {
		path string
		want []byte
	}{{filepath.Join(dir, "base-install.json"), tx.Base}, {filepath.Join(dir, "target-install.json"), tx.Target}} {
		got, err := read(item.path)
		if err != nil || !bytes.Equal(got, item.want) {
			return errors.New("approved recovery inventory unavailable or changed")
		}
	}
	current, err := read(filepath.Join(root, "install.json"))
	if err != nil || (!bytes.Equal(current, tx.Base) && !bytes.Equal(current, tx.Target)) {
		return errors.New("foreign installed inventory; preserving it")
	}
	priorProof := false
	if stage == "predecessor" {
		proof, proofErr := read(filepath.Join(root, "maintenance", "predecessor-proof-v1.json"))
		if proofErr == nil {
			initial := *i
			initial.Phase, initial.InstalledSequenceCommitted = "active", false
			want, marshalErr := json.Marshal(initial)
			source := filepath.Join(dir, "atomic-held.json")
			sourceBytes, sourceErr := read(source)
			if marshalErr != nil || sourceErr != nil || !bytes.Equal(proof, want) || !bytes.Equal(sourceBytes, want) || same == nil || same(filepath.Join(root, "maintenance", "predecessor-proof-v1.json"), source) != nil {
				return errors.New("foreign predecessor proof; preserving it")
			}
			priorProof = true
		} else if !errors.Is(proofErr, os.ErrNotExist) {
			return errors.New("predecessor proof cannot be inspected")
		}
		if !priorProof && !bytes.Equal(current, tx.Base) {
			return errors.New("unproven predecessor inventory; preserve it")
		}
	}
	expectTarget := direction == "forward" || direction == "completion" || direction == "cleanup-complete"
	if (stage == "after" || direction == "completion" || direction == "cleanup-rollback" || direction == "cleanup-complete") && ((expectTarget && !bytes.Equal(current, tx.Target)) || (!expectTarget && !bytes.Equal(current, tx.Base))) {
		return errors.New("installed inventory does not match completed pair")
	}
	for _, image := range []struct{ path, first, second, completed string }{
		{filepath.Join(dir, "runner"), i.Binding.PreviousCore, "", ""},
		{filepath.Join(dir, "previous-signer"), i.Binding.PreviousSigner, "", ""},
		{filepath.Join(dir, "next-core"), i.Binding.TargetCore, "", ""},
		{filepath.Join(dir, "next-signer"), i.Binding.TargetSigner, "", ""},
		{filepath.Join(root, "fased-app"), i.Binding.PreviousCore, i.Binding.TargetCore, wantCore},
		{filepath.Join(root, "fased-signer"), i.Binding.PreviousSigner, i.Binding.TargetSigner, wantSigner},
	} {
		got, err := hash(image.path)
		if err != nil || (got != image.first && (image.second == "" || got != image.second)) || ((stage == "after" || direction == "completion" || direction == "cleanup-rollback" || direction == "cleanup-complete") && image.completed != "" && got != image.completed) {
			return errors.New("foreign or changed installation image; preserving it")
		}
		if stage == "predecessor" && !priorProof && image.completed != "" && got != image.first {
			return errors.New("unproven predecessor executable; preserve it")
		}
	}
	if direction == "completion" {
		if err := validateCompletionInventory(root, dir, tx.Base, tx.Target, stage, read); err != nil {
			return err
		}
	}
	if direction == "cleanup-rollback" || direction == "cleanup-complete" {
		decision, decisionErr := read(filepath.Join(dir, "rollback.json"))
		wantDecision, _ := json.Marshal(struct {
			Version int    `json:"version"`
			Digest  string `json:"digest"`
			Phase   string `json:"phase"`
		}{1, i.Digest, "rollback"})
		if direction == "cleanup-rollback" && (decisionErr != nil || !bytes.Equal(decision, wantDecision)) {
			return errors.New("durable rollback decision is unavailable or changed")
		}
		if direction == "cleanup-complete" && !errors.Is(decisionErr, os.ErrNotExist) {
			return errors.New("rollback decision conflicts with completed installation")
		}
		wanted := "after"
		if i.Phase == "rolled-back-held" {
			wanted = "base"
		}
		if err := validateCompletionInventory(root, dir, tx.Base, tx.Target, wanted, read); err != nil {
			return err
		}
		active, err := read(filepath.Join(root, "update-active.json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("active installation state unavailable")
		}
		want, _ := json.Marshal(struct {
			Version int    `json:"version"`
			Digest  string `json:"digest"`
			Phase   string `json:"phase"`
		}{1, i.Digest, intent})
		if err == nil && !bytes.Equal(active, want) {
			return errors.New("foreign active installation; preserving it")
		}
		if stage == "after" && err == nil {
			return errors.New("active installation was not cleared")
		}
	}
	return nil
}

// Installer metadata is derived from the existing manifests. No owner- or
// caller-supplied identity can replace a field, and partial completion remains
// recoverable only if each current record is one of the two exact versions.
func validateCompletionInventory(root, dir string, base, target []byte, stage string, read func(string) ([]byte, error)) error {
	intent := func(manifest []byte) ([]byte, error) {
		var m map[string]json.RawMessage
		if json.Unmarshal(manifest, &m) != nil {
			return nil, errors.New("invalid manifest")
		}
		var version int
		if json.Unmarshal(m["version"], &version) != nil || version < 1 || version > 3 {
			return nil, errors.New("invalid manifest version")
		}
		out := map[string]json.RawMessage{}
		for _, key := range []string{"nativeOwnerAccess", "profile", "provenance", "enrollment", "location", "publicOrigin", "network", "version", "id", "owner", "coreHash", "signerHash", "listen"} {
			if value, ok := m[key]; ok {
				out[key] = value
			}
		}
		if version == 3 {
			out["mode"] = m["mode"]
		}
		return json.Marshal(out)
	}
	baseIntent, err := intent(base)
	if err != nil {
		return err
	}
	targetIntent, err := intent(target)
	if err != nil {
		return err
	}
	ready := func(manifest []byte) []byte {
		b, _ := json.Marshal(map[string]any{"schema": 1, "manifest": metadataHash(manifest), "fixtureOnly": true})
		return b
	}
	for _, item := range []struct {
		name      string
		old, next []byte
		semantic  bool
	}{{"intent.json", baseIntent, targetIntent, true}, {"ready.json", ready(base), ready(target), false}} {
		old, err := read(filepath.Join(dir, "base-"+item.name))
		if err != nil {
			return errors.New("previous installer record unavailable")
		}
		next, err := read(filepath.Join(dir, "target-"+item.name))
		if err != nil {
			return errors.New("target installer record unavailable")
		}
		equal := func(a, b []byte) bool {
			if !item.semantic {
				return bytes.Equal(a, b)
			}
			var x, y map[string]json.RawMessage
			if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil || x == nil || y == nil {
				return false
			}
			xb, _ := json.Marshal(x)
			yb, _ := json.Marshal(y)
			return bytes.Equal(xb, yb)
		}
		if !equal(old, item.old) || !equal(next, item.next) {
			return errors.New("installer record differs from bound manifests")
		}
		current, err := read(filepath.Join(root, item.name))
		if err != nil || (!bytes.Equal(current, old) && !bytes.Equal(current, next)) || (stage == "after" && !bytes.Equal(current, next)) || (stage == "base" && !bytes.Equal(current, old)) {
			return errors.New("foreign or incomplete installer record; preserving it")
		}
	}
	return nil
}

func metadataHash(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
