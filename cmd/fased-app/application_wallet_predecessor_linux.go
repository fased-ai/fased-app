//go:build linux

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"path/filepath"
	"time"

	"github.com/fased-ai/fased-app/internal/core"
	"github.com/fased-ai/fased-app/internal/custodyinstall"
	"github.com/fased-ai/fased-app/internal/maintenance"
	"github.com/fased-ai/fased-app/internal/signer"
	"github.com/fased-ai/fased-app/internal/update"
)

func settleWalletPredecessorCommand(args []string, out, diagnostics io.Writer) error {
	f := flag.NewFlagSet("settle-wallet-predecessor", flag.ContinueOnError)
	f.SetOutput(diagnostics)
	owner := f.Int("owner", 0, "original package owner UID")
	instance := f.String("instance", "", "original Remote package instance")
	op := f.String("operation", "", "original completed update")
	packet := f.String("candidate-packet", "", "already staged successor packet")
	commit := f.Bool("commit", false, "settle the verified completed lease")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || !update.IDOK(*op) {
		return errors.New("one original instance, completed operation and staged packet required")
	}
	digest, err := hex.DecodeString(*packet)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != *packet {
		return errors.New("the exact staged successor packet is required")
	}
	const location = "remote"
	root, err := update.PackageInstanceRoot(*owner, location, *instance)
	if err != nil {
		return err
	}
	data := filepath.Join(root, "app-state")
	if err = signer.CheckApplication(filepath.Join(root, "identity.json"), root, data); err != nil {
		return err
	}
	current, err := update.ReadPackageMaintenanceRecord(*owner, location, *instance)
	if err != nil {
		return err
	}
	archived, _, err := custodyinstall.ReadPackageWalletPredecessor(current, *owner, location, *instance, *op)
	if err != nil {
		return err
	}
	m, err := custodyinstall.LoadPackageInstance(*owner, root)
	if err != nil {
		return err
	}
	enrollment, err := custodyinstall.ReadEnrollmentCommit(m)
	if err != nil {
		return err
	}
	candidate, err := maintenance.ReadPackageCandidateReceipt(*owner, location, *instance)
	if err != nil || candidate.PacketSHA256 != *packet || candidate.Sequence <= current.Package.Sequence ||
		!candidate.MatchesBase(*owner, current.Package.Sequence, current.Package.ReleaseSHA256, *instance, update.SnapshotDigest("candidate-base-manifest:v1", m)) {
		return errors.New("the staged successor differs from the configured package")
	}
	proof := core.ApplicationUpdateReceipt{ID: *op, Sequence: current.Package.Sequence, ManifestSHA256: current.Package.ReleaseSHA256, EngineSHA256: m.CoreHash, Phase: "completed"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	needsRepair, err := core.SettleArchivedWalletCompletion(ctx, data, enrollment.InstanceID, current.Package.Version, proof, archived.Application.Pause, *commit)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Operation string `json:"operation"`
		Changed   bool   `json:"changed"`
		Pending   bool   `json:"pending"`
	}{*op, *commit && needsRepair, !*commit && needsRepair})
}
