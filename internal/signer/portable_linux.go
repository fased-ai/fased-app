//go:build linux

package signer

// Portable recovery preserves evidence, never spending authority. Archived files
// have .snapshot names and cannot be opened as a live signer. A locally retained
// archive/hash cannot prove latest history, retirement of the source or finality.
import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/crypto/argon2"
	"golang.org/x/sys/unix"
)

const MaxPortableSnapshot = 18 << 20
const portableMagic = "FASED-ENCRYPTED-HISTORY-FIXTURE:v1\n"
const portablePurpose = "fased-app-portable-history-fixture:v1"

type PortableHistory struct {
	Account           AccountRef `json:"account"`
	AuthoritySHA256   string     `json:"authoritySha256"`
	Spent             uint64     `json:"spent,string"`
	Held              uint64     `json:"held,string"`
	Pending           int        `json:"pending"`
	Unknown           int        `json:"unknown"`
	InstalledSequence uint64     `json:"installedSequence,string"`
	ReservedFloor     uint64     `json:"reservedFloor,string"`
	VaultSHA256       string     `json:"vaultSha256"`
	OperationsSHA256  string     `json:"operationsSha256"`
	MockSHA256        string     `json:"mockSha256"`
}
type portableHeader struct {
	Version        int             `json:"version"`
	Purpose        string          `json:"purpose"`
	FixtureOnly    bool            `json:"fixtureOnly"`
	CapturedAt     int64           `json:"capturedAt"`
	ContractSHA256 string          `json:"contractSha256"`
	History        PortableHistory `json:"history"`
}
type PortableReceipt struct {
	Version            int             `json:"version"`
	Purpose            string          `json:"purpose"`
	FixtureOnly        bool            `json:"fixtureOnly"`
	CapturedAt         int64           `json:"capturedAt"`
	ArchiveSHA256      string          `json:"archiveSha256"`
	History            PortableHistory `json:"history"`
	Historical         bool            `json:"historical"`
	ReadOnly           bool            `json:"readOnly"`
	Locked             bool            `json:"locked"`
	RecoveryRequired   bool            `json:"recoveryRequired"`
	InstallAllowed     bool            `json:"installAllowed"`
	ResumeAllowed      bool            `json:"resumeAllowed"`
	WalletSigner       bool            `json:"walletSigner"`
	FinancialExecution bool            `json:"financialExecution"`
	AutomaticUpdates   bool            `json:"automaticUpdates"`
}

func portableHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type portableData struct {
	header portableHeader
	files  [3][]byte
}

var portableNames = [3]string{"vault.json.snapshot", "operations.log.snapshot", "mock-receipts.log.snapshot"}

func portableReceipt(d portableData, archive []byte) PortableReceipt {
	return PortableReceipt{Version: 1, Purpose: portablePurpose, FixtureOnly: true, CapturedAt: d.header.CapturedAt, ArchiveSHA256: portableHash(archive), History: d.header.History, Historical: true, ReadOnly: true, Locked: true, RecoveryRequired: true}
}
func portableJournal(s *storage, name string) ([]byte, error) {
	fd, e := unix.Openat(s.dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if e = journalFD(fd); e != nil {
		return nil, e
	}
	b, e := io.ReadAll(io.LimitReader(f, maxJournal+1))
	if e != nil || len(b) > maxJournal {
		return nil, errors.New("bounded private recovery history required")
	}
	return b, nil
}
func historyFor(v *Vault, files [3][]byte) (PortableHistory, error) {
	s, e := v.UpdateInventory(update.NewProbe())
	if e != nil {
		return PortableHistory{}, e
	}
	if s.ActiveIntentID != "" {
		return PortableHistory{}, errors.New("active update requires its original controller recovery; portable capture refused")
	}
	return PortableHistory{Account: *v.Status().Account, AuthoritySHA256: s.AuthoritySHA256, Spent: s.Spent, Held: s.Held, Pending: s.Pending, Unknown: s.Unknown, InstalledSequence: s.InstalledSequence, ReservedFloor: s.SequenceFloor, VaultSHA256: portableHash(files[0]), OperationsSHA256: portableHash(files[1]), MockSHA256: portableHash(files[2])}, nil
}

// PackPortableSnapshot requires the daemon to be stopped: the existing exclusive
// signer lock and cold reader forbid concurrent capture, creation or repair.
// Password encrypts the entire archive; the existing vault remains encrypted by
// its original unlock password. Neither password is stored in a receipt.
func PackPortableSnapshot(dir, signerID string, sourcePassword, password []byte) ([]byte, PortableReceipt, error) {
	if os.Geteuid() == 0 {
		return nil, PortableReceipt{}, ErrUnavailable
	}
	v, e := openVault(dir, signerID, true)
	if e != nil {
		return nil, PortableReceipt{}, e
	}
	defer v.Close()
	if v.record == nil {
		return nil, PortableReceipt{}, errors.New("complete existing signer history required")
	}
	seed, e := unseal(*v.record, sourcePassword, "vault")
	if e != nil {
		return nil, PortableReceipt{}, e
	}
	wipe(seed) // Authenticate the encrypted key without unlocking or granting authority.
	var d portableData
	defer func() {
		for _, b := range d.files {
			wipe(b)
		}
	}()
	d.files[0], e = v.store.read()
	if e == nil {
		d.files[1], e = portableJournal(v.store, "operations.log")
	}
	if e == nil {
		d.files[2], e = portableJournal(v.store, "mock-receipts.log")
	}
	if e != nil {
		return nil, PortableReceipt{}, e
	}
	d.header = portableHeader{Version: 1, Purpose: portablePurpose, FixtureOnly: true, CapturedAt: time.Now().Unix(), ContractSHA256: update.SnapshotDigest("portable-reader-contract", UpdateContract())}
	d.header.History, e = historyFor(v, d.files)
	if e != nil {
		return nil, PortableReceipt{}, e
	}
	// Re-opened bytes must still describe the actual cold inventory.
	after, e := historyFor(v, d.files)
	if e != nil || after != d.header.History {
		return nil, PortableReceipt{}, errors.New("cold recovery inventory changed")
	}
	document, e := sealPortable(d, password)
	if e != nil {
		return nil, PortableReceipt{}, e
	}
	return document, portableReceipt(d, document), nil
}
func portableCipher(password, salt []byte) (cipher.AEAD, error) {
	if !validPassword(password) {
		return nil, ErrCredential
	}
	k := argon2.IDKey(password, salt, 3, 64*1024, 2, 32)
	defer wipe(k)
	b, e := aes.NewCipher(k)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func sealPortable(d portableData, password []byte) ([]byte, error) {
	h, e := json.Marshal(d.header)
	if e != nil || len(h) > maxDocument {
		return nil, errors.New("invalid bounded snapshot header")
	}
	var p bytes.Buffer
	for _, b := range append([][]byte{h}, d.files[:]...) {
		if e = binary.Write(&p, binary.BigEndian, uint32(len(b))); e != nil {
			return nil, e
		}
		p.Write(b)
	}
	defer wipe(p.Bytes())
	header := append([]byte(portableMagic), randomBytes(28)...)
	a, e := portableCipher(password, header[len(portableMagic):len(portableMagic)+16])
	if e != nil {
		return nil, e
	}
	return a.Seal(header, header[len(header)-12:], p.Bytes(), header), nil
}
func unsealPortable(document, password []byte, expected string) (portableData, error) {
	var d portableData
	n := len(portableMagic) + 28
	if len(document) < n+16 || len(document) > MaxPortableSnapshot || !bytes.HasPrefix(document, []byte(portableMagic)) || len(expected) != 64 || portableHash(document) != expected {
		return d, errors.New("independently retained exact snapshot fingerprint required")
	}
	a, e := portableCipher(password, document[len(portableMagic):len(portableMagic)+16])
	if e != nil {
		return d, e
	}
	p, e := a.Open(nil, document[n-12:n], document[n:], document[:n])
	if e != nil {
		return d, ErrCredential
	}
	defer wipe(p)
	r := bytes.NewReader(p)
	for i, limit := range []uint32{maxDocument, maxDocument, maxJournal, maxJournal} {
		var size uint32
		if binary.Read(r, binary.BigEndian, &size) != nil || size == 0 || size > limit || uint64(size) > uint64(r.Len()) {
			return d, errors.New("invalid fixed recovery frame")
		}
		b := make([]byte, int(size))
		if _, e = io.ReadFull(r, b); e != nil {
			return d, e
		}
		if i == 0 {
			if update.StrictJSON(b, &d.header) != nil {
				return d, errors.New("invalid recovery header")
			}
		} else {
			d.files[i-1] = b
		}
	}
	if r.Len() != 0 || d.header.Version != 1 || d.header.Purpose != portablePurpose || !d.header.FixtureOnly || d.header.CapturedAt <= 0 || !validRef(d.header.History.Account) || d.header.ContractSHA256 != update.SnapshotDigest("portable-reader-contract", UpdateContract()) {
		return d, errors.New("unsupported recovery profile or reader")
	}
	return d, nil
}

func privatePortableParent(dir string) (*os.Root, string, error) {
	if os.Geteuid() == 0 || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, "", errors.New("ordinary-user absolute quarantine path required")
	}
	parent := filepath.Dir(dir)
	real, e := filepath.EvalSymlinks(parent)
	if e != nil || real != parent {
		return nil, "", errors.New("real private quarantine parent required")
	}
	s, e := os.Lstat(parent)
	if e != nil || !s.IsDir() || s.Mode().Perm() != 0700 {
		return nil, "", errors.New("private quarantine parent required")
	}
	var u unix.Stat_t
	if unix.Lstat(parent, &u) != nil || int(u.Uid) != os.Geteuid() {
		return nil, "", errors.New("quarantine parent belongs to another identity")
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return nil, "", e
	}
	a, e := r.Stat(".")
	if e != nil || !os.SameFile(s, a) {
		_ = r.Close()
		return nil, "", errors.New("quarantine parent changed")
	}
	return r, filepath.Base(dir), nil
}

// validatePortable reuses the actual cold vault/journal replay in an isolated,
// disposable validation directory. Only copied encrypted key/history are used;
// no live installation, journal append, history repair or grant rebind is called.
func validatePortable(d portableData, parent string) error {
	dir, e := os.MkdirTemp(parent, ".portable-validation-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	for i, name := range []string{"vault.json", "operations.log", "mock-receipts.log", "vault.lock"} {
		var b []byte
		if i < 3 {
			b = d.files[i]
		}
		if e = os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
			return e
		}
	}
	v, e := openVault(dir, d.header.History.Account.SignerID, true)
	if e != nil {
		return e
	}
	defer v.Close()
	h, e := historyFor(v, d.files)
	if e != nil || h != d.header.History {
		return errors.New("recovery history/floors/originals do not match authenticated header")
	}
	return nil
}

func readPortableFile(r *os.Root, name string) ([]byte, error) {
	s, e := r.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() || s.Mode().Perm() != 0600 || s.Size() > MaxPortableSnapshot {
		return nil, errors.New("unsafe quarantine file")
	}
	f, e := r.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if e = journalFD(int(f.Fd())); e != nil {
		return nil, e
	}
	a, e := f.Stat()
	if e != nil || !os.SameFile(a, s) {
		return nil, errors.New("quarantine file changed")
	}
	return io.ReadAll(io.LimitReader(f, MaxPortableSnapshot+1))
}
func syncPortableFile(r *os.Root, name string) error {
	f, e := r.OpenFile(name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}
func syncPortableRoot(r *os.Root) error {
	f, e := r.Open(".")
	if e != nil {
		return e
	}
	return errors.Join(f.Sync(), f.Close())
}

// RestorePortableSnapshot writes only a read-only quarantine, never live signer
// filenames. The independent fingerprint is mandatory even after AEAD succeeds.
// Exact interrupted prefixes resume; conflicting bytes are preserved/refused.
func RestorePortableSnapshot(dir string, document, password []byte, expected string) (PortableReceipt, error) {
	return restorePortableSnapshot(dir, document, password, expected, nil)
}
func restorePortableSnapshot(dir string, document, password []byte, expected string, fault func(string) error) (PortableReceipt, error) {
	d, e := unsealPortable(document, password, expected)
	defer func() {
		for _, b := range d.files {
			wipe(b)
		}
	}()
	if e != nil {
		return PortableReceipt{}, e
	}
	r, base, e := privatePortableParent(dir)
	if e != nil {
		return PortableReceipt{}, e
	}
	defer r.Close()
	if e = validatePortable(d, filepath.Dir(dir)); e != nil {
		return PortableReceipt{}, e
	}
	receipt := portableReceipt(d, document)
	rb, _ := json.Marshal(receipt)
	expectedFiles := map[string][]byte{"receipt.json": rb, "intent.json": []byte(expected + "\n")}
	for i, name := range portableNames {
		expectedFiles[name] = d.files[i]
	}
	createdPending := false
	verify := func(name string, complete bool) (*os.Root, error) {
		s, e := r.Lstat(name)
		if e != nil {
			return nil, e
		}
		var st unix.Stat_t
		if !s.IsDir() || s.Mode().Perm() != 0700 || unix.Lstat(filepath.Join(filepath.Dir(dir), name), &st) != nil || int(st.Uid) != os.Geteuid() {
			return nil, errors.New("unsafe existing quarantine")
		}
		q, e := r.OpenRoot(name)
		if e != nil {
			return nil, e
		}
		ok := false
		defer func() {
			if !ok {
				_ = q.Close()
			}
		}()
		f, e := q.Open(".")
		if e != nil {
			return nil, e
		}
		entries, e := f.ReadDir(6)
		_ = f.Close()
		if e != nil && e != io.EOF || len(entries) > 5 {
			return nil, errors.New("foreign quarantine contents")
		}
		for _, entry := range entries {
			want, known := expectedFiles[entry.Name()]
			if !known {
				return nil, errors.New("foreign quarantine file preserved")
			}
			got, e := readPortableFile(q, entry.Name())
			if e != nil || !bytes.Equal(got, want) {
				return nil, errors.New("conflicting or partial quarantine file preserved")
			}
		}
		if !complete && len(entries) == 0 && !createdPending {
			return nil, errors.New("foreign empty pending quarantine preserved")
		}
		if complete && len(entries) != 5 {
			return nil, errors.New("incomplete published quarantine")
		}
		if !complete && len(entries) > 0 {
			if b, e := readPortableFile(q, "intent.json"); e != nil || !bytes.Equal(b, expectedFiles["intent.json"]) {
				return nil, errors.New("pending quarantine has no matching durable intent")
			}
		}
		ok = true
		return q, nil
	}
	if _, e = r.Lstat(base); e == nil {
		q, e := verify(base, true)
		if e != nil {
			return PortableReceipt{}, e
		}
		defer q.Close()
		if _, e = r.Lstat(base + ".pending"); !os.IsNotExist(e) {
			return PortableReceipt{}, errors.New("conflicting final/pending quarantine")
		}
		for name := range expectedFiles {
			if e = syncPortableFile(q, name); e != nil {
				return PortableReceipt{}, e
			}
		}
		if e = syncPortableRoot(q); e == nil {
			e = syncPortableRoot(r)
		}
		return receipt, e
	} else if !os.IsNotExist(e) {
		return PortableReceipt{}, e
	}
	pending := base + ".pending"
	if e = r.Mkdir(pending, 0700); e == nil {
		createdPending = true
	} else if !os.IsExist(e) {
		return PortableReceipt{}, e
	}
	q, e := verify(pending, false)
	if e != nil {
		return PortableReceipt{}, e
	}
	defer q.Close()
	for _, name := range []string{"intent.json", portableNames[0], portableNames[1], portableNames[2], "receipt.json"} {
		if _, e = q.Lstat(name); os.IsNotExist(e) {
			f, e := q.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
			if e != nil {
				return PortableReceipt{}, e
			}
			n, e := f.Write(expectedFiles[name])
			if e == nil && n != len(expectedFiles[name]) {
				e = io.ErrShortWrite
			}
			if e == nil {
				e = f.Sync()
			}
			e = errors.Join(e, f.Close())
			if e != nil {
				return PortableReceipt{}, e
			}
		} else if e != nil {
			return PortableReceipt{}, e
		}
		if e = syncPortableFile(q, name); e != nil {
			return PortableReceipt{}, e
		}
		if e = syncPortableRoot(q); e != nil {
			return PortableReceipt{}, e
		}
		if fault != nil {
			if e = fault(name); e != nil {
				return PortableReceipt{}, e
			}
		}
	}
	parent, e := r.Open(".")
	if e != nil {
		return PortableReceipt{}, e
	}
	defer parent.Close()
	if e = unix.Renameat2(int(parent.Fd()), pending, int(parent.Fd()), base, unix.RENAME_NOREPLACE); e != nil {
		return PortableReceipt{}, e
	}
	if fault != nil {
		if e = fault("renamed"); e != nil {
			return PortableReceipt{}, e
		}
	}
	if e = syncPortableRoot(r); e != nil {
		return PortableReceipt{}, e
	}
	return receipt, nil
}
