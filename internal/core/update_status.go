package core

import (
	"context"
	"time"

	"github.com/fased-ai/fased-app/internal/signer"
)

// UpdateStatus is an observation, never an installation receipt or a grant.
// It deliberately excludes owner inventory, financial counters and recovery
// images. The app-owned pause and the public signer reply are sampled separately.
type UpdateStatus struct {
	Version            int                `json:"version"`
	ObservedAt         time.Time          `json:"observedAt"`
	ReadOnly           bool               `json:"readOnly"`
	State              string             `json:"state"`
	Core               UpdateCoreStatus   `json:"core"`
	Signer             UpdateSignerStatus `json:"signer"`
	Guidance           []UpdateGuidance   `json:"guidance"`
	WalletSigner       bool               `json:"walletSigner"`
	FinancialExecution bool               `json:"financialExecution"`
	AutomaticUpdates   bool               `json:"automaticUpdates"`
}
type UpdateCoreStatus struct {
	Phase    string `json:"phase"`
	IntentID string `json:"intentId,omitempty"`
}
type UpdateSignerStatus struct {
	State            string `json:"state"`
	Locked           *bool  `json:"locked"`
	UpdateHeld       *bool  `json:"updateHeld"`
	RecoveryRequired *bool  `json:"recoveryRequired"`
}
type UpdateGuidance struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Command string `json:"command,omitempty"`
}

func (s *Store) ReadUpdateStatus(ctx context.Context) UpdateStatus {
	v := UpdateStatus{Version: 1, ReadOnly: true, Core: UpdateCoreStatus{Phase: "unavailable"}, Signer: UpdateSignerStatus{State: "not-configured"}}
	// A single read validates the persisted fence without changing it, taking
	// an owner lock, reading a backup or creating any lifecycle record.
	phase, record, err := readPause(ctx, s.db)
	if err == nil {
		v.Core.Phase = phase
		if phase == "paused" || phase == "released" {
			v.Core.IntentID = record.Request.IntentID
		}
	}
	if s.fixture != nil {
		v.Signer.State = "unavailable"
		r, e := s.fixture(ctx, signer.Request{Action: "status"})
		if e == nil && r.Error == "" && r.Status.FixtureOnly && !r.Status.WalletSigner && !r.Status.FinancialExecution && !r.Status.UnattendedUnlock {
			v.Signer = UpdateSignerStatus{State: "available", Locked: &r.Status.Locked, UpdateHeld: &r.Status.UpdateHeld, RecoveryRequired: &r.Status.RecoveryRequired}
		}
	}
	v.ObservedAt = s.now().UTC()
	switch {
	case v.Core.Phase == "unavailable":
		v.State = "unavailable"
	case v.Signer.RecoveryRequired != nil && *v.Signer.RecoveryRequired:
		v.State = "recovery-required"
	case v.Core.Phase == "paused":
		v.State = "paused"
	case v.Signer.State == "unavailable":
		v.State = "unavailable"
	case v.Signer.UpdateHeld != nil && *v.Signer.UpdateHeld:
		v.State = "signer-held"
	case v.Signer.State == "not-configured":
		v.State = "not-configured"
	default:
		v.State = "observed"
	}
	v.Guidance = []UpdateGuidance{
		{Code: "scope", Message: "Read-only observations can change independently. This is not proof of an installed or latest release. Real wallets and automatic updates are disabled."},
	}
	if v.Core.Phase == "unavailable" {
		v.Guidance = append(v.Guidance, UpdateGuidance{Code: "core-unavailable", Message: "Core pause metadata could not be verified. Preserve existing state and use the owner terminal to diagnose; do not reset or restore a database."})
	} else if v.Core.Phase == "paused" {
		v.Guidance = append(v.Guidance, UpdateGuidance{Code: "paused", Message: "Core metadata writes are paused. Keep this instance and its recovery files. Review the existing intent in the protected owner terminal before retrying the same operation."})
	} else if v.Core.Phase == "released" {
		v.Guidance = append(v.Guidance, UpdateGuidance{Code: "released", Message: "The core metadata fence was released. This app-owned record does not prove signer completion or permit trading."})
	}
	switch v.Signer.State {
	case "not-configured":
		v.Guidance = append(v.Guidance, UpdateGuidance{Code: "not-configured", Message: "No managed signer is configured for this core. An ordinary installation has no wallet custody."})
	case "unavailable":
		v.Guidance = append(v.Guidance, UpdateGuidance{Code: "signer-unavailable", Message: "The configured signer did not return supported public status. Its lock, update hold and recovery state are unknown. Reconnect to the existing services and refresh; do not create a replacement wallet."})
	case "available":
		if *v.Signer.UpdateHeld {
			v.Guidance = append(v.Guidance, UpdateGuidance{Code: "signer-held", Message: "The signer reports an update or storage hold. Public status cannot identify its installation phase or clear it. Inspect current owner inventory before choosing recovery."})
		}
		if *v.Signer.RecoveryRequired {
			v.Guidance = append(v.Guidance, UpdateGuidance{Code: "recovery-required", Message: "The signer reports a recovery hold. Key-only recovery cannot reconstruct financial history. Preserve keys, original records, counters and holds; never restore old signer databases or unlock automatically."})
		}
	}
	if s.fixture != nil {
		v.Guidance = append(v.Guidance, UpdateGuidance{Code: "owner-review", Message: "On the installed Linux owner's terminal, inspect actual core/signer inventory. This command requires the existing owner OS authorization and CLI login; it grants no installation, resume or unlock permission.", Command: "fased-app custody updates inventory --disposable-fixture --prepare-host"})
	}
	return v
}
