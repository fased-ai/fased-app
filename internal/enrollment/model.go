// Package enrollment cuts over an existing logical instance to the existing
// isolated custody installation. It adds no engine or wallet authority.
package enrollment

import (
	"context"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

type Source struct {
	Profile, Data, Listen, PublicOrigin, ExpectedInstanceID string
	ApplicationSequence                                     uint64
	ApplicationReleaseSHA256                                string
	UID, GID                                                int
	Running, Autostart                                      bool
	Validate, Suspend                                       func(context.Context) error
	Restore                                                 func(context.Context, bool, bool) error
}

type Options struct {
	RecoverSuspended          bool // Explicit pre-import Remote core recovery; signer and identity never change.
	StartExisting             bool // Explicit native resume; starts committed roles without changing autostart.
	NativeOwnerAccess         bool
	Core, Signer, Network     string
	Mode, Profile, Provenance string
	PrepareHost, Disposable   bool
}

type sourceRecord struct {
	Profile                  string `json:"profile"`
	Data                     string `json:"data"`
	Listen                   string `json:"listen"`
	PublicOrigin             string `json:"publicOrigin,omitempty"`
	ExpectedInstanceID       string `json:"expectedInstanceId"`
	UID                      int    `json:"uid"`
	GID                      int    `json:"gid"`
	Running                  bool   `json:"running"`
	Autostart                bool   `json:"autostart"`
	ApplicationSequence      uint64 `json:"applicationSequence,omitempty"`
	ApplicationReleaseSHA256 string `json:"applicationReleaseSha256,omitempty"`
}

func record(s Source) sourceRecord {
	return sourceRecord{Profile: s.Profile, Data: s.Data, Listen: s.Listen, PublicOrigin: s.PublicOrigin, ExpectedInstanceID: s.ExpectedInstanceID, UID: s.UID, GID: s.GID, Running: s.Running, Autostart: s.Autostart, ApplicationSequence: s.ApplicationSequence, ApplicationReleaseSHA256: s.ApplicationReleaseSHA256}
}

type node struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Mode   uint32 `json:"mode"`
}

type journal struct {
	CoreRecovery      *coreRecovery           `json:"coreRecovery,omitempty"`
	NativeOwnerAccess bool                    `json:"nativeOwnerAccess,omitempty"`
	Mode              string                  `json:"mode,omitempty"`
	Profile           string                  `json:"profile,omitempty"`
	Provenance        string                  `json:"provenance,omitempty"`
	Version           int                     `json:"version"`
	ID                string                  `json:"id"`
	Owner             int                     `json:"owner"`
	Phase             string                  `json:"phase"`
	Source            sourceRecord            `json:"source"`
	Node              node                    `json:"node"`
	CoreHash          string                  `json:"coreHash"`
	SignerHash        string                  `json:"signerHash"`
	Network           string                  `json:"network"`
	Digest            string                  `json:"digest,omitempty"`
	Target            custodyinstall.Manifest `json:"target"`
}

// The original binding remains durable; only a separately admitted empty-target
// code recovery can move the current core forward. Signer/topology never change.
type coreRecovery struct {
	Version      int                     `json:"version"`
	Previous     custodyinstall.Manifest `json:"previous"`
	NextCoreHash string                  `json:"nextCoreHash"`
}

type fence struct {
	Version int                     `json:"version"`
	ID      string                  `json:"id"`
	Owner   int                     `json:"owner"`
	Source  sourceRecord            `json:"source"`
	Node    node                    `json:"node"`
	Digest  string                  `json:"digest"`
	Target  custodyinstall.Manifest `json:"target"`
}

func fenceFor(j journal) fence {
	return fence{1, j.ID, j.Owner, j.Source, j.Node, j.Digest, j.Target}
}

var phases = []string{"prepared", "suspending", "suspended", "importing", "staged", "fencing", "fenced", "publishing", "committed", "activation-requested", "complete"}

func rank(p string) int {
	for i, phase := range phases {
		if p == phase {
			return i
		}
	}
	return -1
}

func custodyContext(mode, profile, provenance string) bool {
	return mode == "" && profile == "" && provenance == "" || mode == "ordinary-custody:v1" && profile == "owner-authorized:v1" && (provenance == "controlled-disposable:v1" || provenance == "owner-input:v1")
}
