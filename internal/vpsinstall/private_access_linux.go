//go:build linux

package vpsinstall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
)

// PrivateAccessProof reuses the already-authorized immutable app-UID owner
// helper. No sudo policy, login session, signer or password is added.
func PrivateAccessProof(ctx context.Context, m Manifest, system *custodyinstall.System, request core.PrivateAccessProofRequest) (core.PrivateAccessProof, error) {
	var empty core.PrivateAccessProof
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return empty, err
	}
	result, err := ApplicationOwnerCall(ctx, m, core.ApplicationOwnerCommand{Action: "private-access", ID: hex.EncodeToString(id[:]), PrivateProof: &request}, system)
	if err != nil {
		return empty, err
	}
	if result.PrivateProof == nil {
		return empty, errors.New("private device proof missing")
	}
	return *result.PrivateProof, nil
}
