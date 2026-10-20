package update

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
)

func NewProbe() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func IDOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 16 && hex.EncodeToString(b) == s
}
func SnapshotDigest(kind string, v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return digest(append([]byte("fased-update-snapshot:v1:"+kind+"\x00"), b...))
}

type CoreSnapshot struct {
	Version    int    `json:"version"`
	ProbeID    string `json:"probeId"`
	BootID     string `json:"bootId"`
	InstanceID string `json:"instanceId"`
	State      State  `json:"state"`
}
type SignerSnapshot struct {
	InstalledSequence uint64  `json:"installedSequence,string,omitempty"`
	Version           int     `json:"version"`
	ProbeID           string  `json:"probeId"`
	BootID            string  `json:"bootId"`
	AuthoritySHA256   string  `json:"authoritySha256"`
	States            []State `json:"states"`
	Spent             uint64  `json:"spent,string"`
	Held              uint64  `json:"held,string"`
	Pending           int     `json:"pending"`
	Unknown           int     `json:"unknown"`
	RecoveryHold      bool    `json:"recoveryHold"`
	SequenceFloor     uint64  `json:"sequenceFloor,string"`
	ActiveIntentID    string  `json:"activeIntentId"`
}
type Intent struct {
	Contracts         []Contract         `json:"contracts,omitempty"`
	Artifacts         []Artifact         `json:"artifacts,omitempty"`
	Lifecycle         *LifecycleContract `json:"lifecycle,omitempty"`
	StageSHA256       string             `json:"stageSha256,omitempty"`
	Version           int                `json:"version"`
	ID                string             `json:"id"`
	Sequence          uint64             `json:"sequence,string"`
	ManifestSHA256    string             `json:"manifestSha256"`
	Core              CoreSnapshot       `json:"core"`
	Signer            SignerSnapshot     `json:"signer"`
	CreatedAt         int64              `json:"createdAt"`
	ManifestExpiresAt int64              `json:"manifestExpiresAt,omitempty"`
	State             string             `json:"state"`
}
type IntentRequest struct {
	ID       string         `json:"id"`
	Core     CoreSnapshot   `json:"core"`
	Signer   SignerSnapshot `json:"signer"`
	Manifest []byte         `json:"manifest"`
	Trust    []byte         `json:"trust"`
}

func JoinSnapshots(c CoreSnapshot, s SignerSnapshot, target Target) (Inventory, error) {
	if c.Version != 1 || s.Version != 1 || !IDOK(c.ProbeID) || c.ProbeID != s.ProbeID || !IDOK(c.BootID) || !IDOK(s.BootID) || !tokenOK(c.InstanceID) || !hashOK(s.AuthoritySHA256) || c.State.Name != "core-db" || len(s.States) != 3 || s.ActiveIntentID != "" && !IDOK(s.ActiveIntentID) {
		return Inventory{}, errors.New("invalid service snapshot identity")
	}
	states := append([]State{c.State}, s.States...)
	return Inventory{Version: 1, FixtureOnly: true, Target: target, SequenceFloor: s.SequenceFloor, States: states, Pending: s.Pending, Unknown: s.Unknown, RecoveryHold: s.RecoveryHold}, nil
}
func ValidateIntent(i Intent) error {
	if i.Lifecycle != nil && i.Lifecycle.Entrypoint >= 3 && i.ManifestExpiresAt <= i.CreatedAt {
		return errors.New("controller-aware intents require signed manifest expiry")
	}
	if i.StageSHA256 != "" || (len(i.Artifacts) > 0 || i.Lifecycle != nil) && !Stageable(i) {
		return errors.New("invalid staged intent contract")
	}
	if i.Version != 1 || !IDOK(i.ID) || i.Sequence == 0 || !hashOK(i.ManifestSHA256) || i.CreatedAt <= 0 || i.State != "prepared" || i.Signer.ActiveIntentID != "" || i.Sequence <= i.Signer.SequenceFloor {
		return errors.New("invalid update intent")
	}
	inv, e := JoinSnapshots(i.Core, i.Signer, Target{OS: "linux", Arch: "amd64"})
	if e != nil {
		return e
	}
	if len(inv.States) != 4 || inv.Pending < 0 || inv.Unknown < 0 || inv.Unknown > inv.Pending {
		return errors.New("invalid intent state")
	}
	return nil
}
