// Package enrollment cuts over an existing logical instance to the existing
// isolated custody installation. It adds no engine or wallet authority.
package enrollment

import (
	"context"

	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

type Source struct {
	Profile, Data, Listen, PublicOrigin, ExpectedInstanceID string
	UID, GID                                                int
	Running, Autostart                                      bool
	Validate, Suspend                                       func(context.Context) error
	Restore                                                 func(context.Context, bool, bool) error
}

type Options struct {
	Core, Signer, Network   string
	PrepareHost, Disposable bool
}

type sourceRecord struct {
	Profile            string `json:"profile"`
	Data               string `json:"data"`
	Listen             string `json:"listen"`
	PublicOrigin       string `json:"publicOrigin,omitempty"`
	ExpectedInstanceID string `json:"expectedInstanceId"`
	UID                int    `json:"uid"`
	GID                int    `json:"gid"`
	Running            bool   `json:"running"`
	Autostart          bool   `json:"autostart"`
}

func record(s Source) sourceRecord {
	return sourceRecord{s.Profile, s.Data, s.Listen, s.PublicOrigin, s.ExpectedInstanceID, s.UID, s.GID, s.Running, s.Autostart}
}

type node struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Mode   uint32 `json:"mode"`
}

type journal struct {
	Version    int                     `json:"version"`
	ID         string                  `json:"id"`
	Owner      int                     `json:"owner"`
	Phase      string                  `json:"phase"`
	Source     sourceRecord            `json:"source"`
	Node       node                    `json:"node"`
	CoreHash   string                  `json:"coreHash"`
	SignerHash string                  `json:"signerHash"`
	Network    string                  `json:"network"`
	Digest     string                  `json:"digest,omitempty"`
	Target     custodyinstall.Manifest `json:"target"`
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
