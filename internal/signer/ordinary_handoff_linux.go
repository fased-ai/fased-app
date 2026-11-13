//go:build linux

package signer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/fased-ai/fased-app/internal/update"
	"golang.org/x/sys/unix"
)

// Only the cold signer-UID entrypoint can append the one-time reader floor.
// No RPC, app session or plaintext/key export participates in this handoff.
func CommitOrdinaryReaderHandoff(c Config, id string) (*update.SignerSnapshot, error) {
	if !update.IDOK(id) || c.Version != 3 {
		return nil, ErrUnavailable
	}
	root := filepath.Dir(c.StateDir)
	dir := root + "/bootstraps/" + id
	read := func(path string) ([]byte, error) {
		if e := trustedPath(path); e != nil {
			return nil, e
		}
		f, e := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0644 || st.Size() > 4<<20 {
			return nil, ErrUnavailable
		}
		return io.ReadAll(io.LimitReader(f, 4<<20))
	}
	raw, e := read(dir + "/ordinary-signer.json")
	if e != nil {
		return nil, e
	}
	var a update.OrdinaryHandoffAuthority
	if update.StrictJSON(raw, &a) != nil || !a.Valid() || a.Root != root || a.Owner != c.OwnerUID || a.App != c.AppUID || a.Signer != c.SignerUID || a.SignerID != c.SignerID || a.Network != c.Network || a.ConfigSHA256 != update.SnapshotDigest("ordinary-handoff-config", c) {
		return nil, errors.New("foreign ordinary signer handoff")
	}
	bound, e := LoadConfig(root + "/identity.json")
	if e != nil || bound != c {
		return nil, errors.New("handoff config changed")
	}
	tx, e := read(dir + "/transaction.json")
	if e != nil {
		return nil, e
	}
	if e = a.VerifyTransaction(tx); e != nil {
		return nil, e
	}
	for _, f := range []struct{ path, phase string }{{root + "/update-active.json", id}, {dir + "/forward-only.json", "forward-only"}} {
		b, e := read(f.path)
		var marker struct {
			Version int    `json:"version"`
			Digest  string `json:"digest"`
			Phase   string `json:"phase"`
		}
		if e != nil || update.StrictJSON(b, &marker) != nil || marker.Version != 1 || marker.Digest != a.Digest || marker.Phase != f.phase {
			return nil, errors.New("durable matching forward fence required")
		}
	}
	path, e := os.Executable()
	if e != nil || path != dir+"/candidate/fased-signer" {
		return nil, ErrUnavailable
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != a.Receipt.TargetSigner {
		return nil, errors.New("handoff signer executable changed")
	}
	// readOnly opens neither publication recovery nor repair. Existing key bytes
	// stay untouched; appendJournal alone writes after the verified root fence.
	v, e := inspectVaultMode(c, true)
	if e != nil {
		return nil, e
	}
	defer v.Close()
	return v.commitOrdinaryHandoff(a.Before, a.Receipt)
}

func (v *Vault) commitOrdinaryHandoff(before update.SignerSnapshot, r update.OrdinaryHandoffReceipt) (*update.SignerSnapshot, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.ordinary || !update.ValidOrdinaryHandoff(r) {
		return nil, ErrUnavailable
	}
	s, e := v.ordinaryUpdateSnapshot(update.NewProbe())
	if e != nil {
		return nil, e
	}
	if h := v.ledger.ordinaryHandoff; h != nil {
		if *h != r || v.ledger.updateFloor != r.Sequence || v.ledger.installedSequence != 0 || v.ledger.activeUpdate != "" || len(v.ledger.updates) != 0 || len(v.ledger.installations) != 0 {
			return nil, errors.New("handoff superseded or foreign")
		}
		return &s, nil
	}
	if !update.SameOrdinarySnapshot(before, s) || before.SequenceFloor != 0 || r.AuthoritySHA256 != s.AuthoritySHA256 || r.GenesisSHA256 != s.States[1].Anchor {
		return nil, errors.New("ordinary genesis changed before handoff")
	}
	wasReadOnly := v.store.readOnly
	v.store.readOnly = false
	defer func() { v.store.readOnly = wasReadOnly }()
	if e = v.ledger.append(v, journalRecord{Kind: "ordinary-reader-handoff", OrdinaryHandoff: &r}); e != nil {
		return nil, e
	}
	after, e := v.ordinaryUpdateSnapshot(update.NewProbe())
	return &after, e
}

// While the root controller owns the handoff, public RPC cannot mutate keys or
// start a competing update. Status stays available for protected health checks.
func ordinaryHandoffRPCAllowed(c Config, action string, vault ...*Vault) bool {
	if c.Version != 3 || action == "status" || action == "lock" {
		return true
	}
	root := filepath.Dir(c.StateDir)
	if r, err := update.ReadMaintenanceRecord(c.OwnerUID); err == nil {
		if f, e := update.OpenProtected(root+"/update-active.json", 8192, 0644); e == nil {
			b, e := io.ReadAll(f)
			f.Close()
			if e == nil && bytes.Equal(b, update.MaintenanceFence(r.OperationID)) {
				return true
			}
		}
	}
	b, e := os.ReadFile(root + "/update-active.json")
	if errors.Is(e, os.ErrNotExist) {
		return true
	}
	var m struct {
		Version int    `json:"version"`
		Digest  string `json:"digest"`
		Phase   string `json:"phase"`
	}
	if e != nil || update.StrictJSON(b, &m) != nil || !update.IDOK(m.Phase) {
		return false
	}
	// The new repair conductor may only observe protected state while the
	// repair owns the root fence. Socket/OS owner authentication still applies;
	// this grants no prepare, abort, install or financial operation.
	if action == "fixture-update-inventory" || action == "fixture-update-status" {
		path := root + "/bootstraps/" + m.Phase + "/transaction.json"
		if trustedPath(path) == nil {
			if raw, e := os.ReadFile(path); e == nil && ordinaryRepairObservationAllowed(c, action, m.Phase, m.Digest, raw) {
				return true
			}
		}
	}
	// Only an actual current signer installation can classify a normal update.
	// Missing bootstrap metadata never grants authority; a retained cancelled
	// bootstrap directory also cannot block a separately approved normal hold.
	switch action {
	case "fixture-update-status", "fixture-update-inventory", "fixture-update-preflight", "fixture-update-abort", "fixture-install-controller":
	default:
		return false
	}
	if len(vault) != 1 || vault[0] == nil {
		return false
	}
	actual, err := vault[0].Installation(update.InstallRequest{Action: "status", IntentID: m.Phase})
	if err != nil || actual == nil || !update.ValidInstallation(*actual) || actual.Digest != m.Digest || actual.Binding.Index.Binding.Signer.Version != 3 || !update.OrdinaryContracts(actual.Binding.Contracts) {
		return false
	}
	b, e = os.ReadFile(root + "/updates/" + m.Phase + "/transaction.json")
	if e != nil {
		return false
	}
	var t struct {
		Version int                   `json:"version"`
		Base    json.RawMessage       `json:"base"`
		Target  json.RawMessage       `json:"target"`
		Binding update.InstallBinding `json:"binding"`
	}
	if update.StrictJSON(b, &t) != nil || t.Version != 1 || update.ValidateInstallBinding(t.Binding) != nil || update.InstallDigest(t.Binding) != m.Digest || t.Binding.Index.Binding.Pause.Request == nil || t.Binding.Index.Binding.Pause.Request.IntentID != m.Phase || update.InstallDigest(t.Binding) != update.InstallDigest(actual.Binding) {
		return false
	}
	var base struct {
		Version    int    `json:"version"`
		Owner      int    `json:"owner"`
		Root       string `json:"root"`
		ID         string `json:"id"`
		CoreHash   string `json:"coreHash"`
		SignerHash string `json:"signerHash"`
		Mode       string `json:"mode"`
		Network    string `json:"network"`
		Profile    string `json:"profile"`
		Provenance string `json:"provenance"`
		App        struct {
			UID int `json:"uid"`
		} `json:"app"`
		Signer struct {
			UID int `json:"uid"`
		} `json:"signer"`
	}
	var next struct {
		CoreHash   string `json:"coreHash"`
		SignerHash string `json:"signerHash"`
	}
	h := sha256.Sum256(t.Base)
	if json.Unmarshal(t.Base, &base) != nil || json.Unmarshal(t.Target, &next) != nil || base.Version != 3 || base.Owner != c.OwnerUID || base.Root != root || base.ID != c.SignerID || t.Binding.InstallationID != base.ID || base.Mode != c.Mode || base.Profile != c.Profile || base.Provenance != c.Provenance || base.Network != c.Network || base.App.UID != c.AppUID || base.Signer.UID != c.SignerUID || hex.EncodeToString(h[:]) != t.Binding.PreviousManifest || base.CoreHash != t.Binding.PreviousCore || base.SignerHash != t.Binding.PreviousSigner || next.CoreHash != t.Binding.TargetCore || next.SignerHash != t.Binding.TargetSigner {
		return false
	}
	var oldMap, nextMap map[string]json.RawMessage
	if json.Unmarshal(t.Base, &oldMap) != nil || json.Unmarshal(t.Target, &nextMap) != nil {
		return false
	}
	nextMap["coreHash"], nextMap["signerHash"] = oldMap["coreHash"], oldMap["signerHash"]
	return update.SnapshotDigest("ordinary-controller-topology", oldMap) == update.SnapshotDigest("ordinary-controller-topology", nextMap)
}

func ordinaryRepairObservationAllowed(c Config, action, id, digest string, raw []byte) bool {
	if c.Version != 3 || action != "fixture-update-inventory" && action != "fixture-update-status" {
		return false
	}
	var t struct {
		Version    int                    `json:"version"`
		ID         string                 `json:"id"`
		Base       json.RawMessage        `json:"base"`
		Target     json.RawMessage        `json:"target"`
		Bundle     update.BootstrapBundle `json:"bundle"`
		Trust      []byte                 `json:"trust"`
		ReviewedAt int64                  `json:"reviewedAt"`
		Core       json.RawMessage        `json:"core"`
		Signer     json.RawMessage        `json:"signer"`
		Repair     json.RawMessage        `json:"repair"`
	}
	if update.StrictJSON(raw, &t) != nil || t.Version != 3 || t.Bundle.Version != 3 || t.ID != id || !update.IDOK(id) || update.SnapshotDigest("reader-bootstrap:v1", json.RawMessage(raw)) != digest {
		return false
	}
	if _, e := update.VerifyBootstrap(t.Bundle, t.Trust, t.ReviewedAt); e != nil {
		return false
	}
	var b struct {
		Version int    `json:"version"`
		ID      string `json:"id"`
		Root    string `json:"root"`
		Owner   int    `json:"owner"`
		Network string `json:"network"`
		App     struct {
			UID int `json:"uid"`
		} `json:"app"`
		Signer struct {
			UID int `json:"uid"`
		} `json:"signer"`
	}
	return json.Unmarshal(t.Base, &b) == nil && b.Version == 3 && b.ID == c.SignerID && b.Root == filepath.Dir(c.StateDir) && b.Owner == c.OwnerUID && b.Network == c.Network && b.App.UID == c.AppUID && b.Signer.UID == c.SignerUID
}
