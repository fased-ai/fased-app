package signer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/fased-ai/fased-app/internal/onchain"
	"github.com/fased-ai/fased-app/internal/update"
)

type journal struct {
	name string
	seq  int
	hash string
	size int
}

type journalRecord struct {
	FreshManaged    bool                 `json:"freshManaged,omitempty"`
	Swap            *SwapProposal        `json:"swap,omitempty"`
	SwapObservation *onchain.Observation `json:"swapObservation,omitempty"`
	Installation    *update.Installation `json:"installation,omitempty"`
	Update          *update.Intent       `json:"update,omitempty"`
	Proposal        *FixtureProposal     `json:"proposal,omitempty"`
	Version         int                  `json:"version"`
	Sequence        int                  `json:"sequence"`
	Previous        string               `json:"previous"`
	Hash            string               `json:"hash"`
	At              int64                `json:"at"`
	Kind            string               `json:"kind"`
	Account         *AccountRef          `json:"account,omitempty"`
	MissingHistory  bool                 `json:"missingHistory,omitempty"`
	Policy          *FixturePolicy       `json:"policy,omitempty"`
	Grant           *FixtureGrant        `json:"grant,omitempty"`
	Intent          *FixtureIntent       `json:"intent,omitempty"`
	ID              string               `json:"id,omitempty"`
	Original        []byte               `json:"original,omitempty"`
	OriginalHash    string               `json:"originalHash,omitempty"`
	Outcome         string               `json:"outcome,omitempty"`
}

func recordHash(r journalRecord) string {
	r.Hash = ""
	b, _ := json.Marshal(r)
	h := sha256.Sum256(append([]byte("fased-fixture-journal:v1\x00"), b...))
	return hex.EncodeToString(h[:])
}
